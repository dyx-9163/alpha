package minio

import (
	"context"
	"slices"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func siloResumeTestStore() *offlineUpgradeStore {
	return &offlineUpgradeStore{
		fakeStore: fakeStore{servers: map[string]store.Server{
			"one": {ID: "one", Host: "one"}, "two": {ID: "two", Host: "two"},
		}},
		instances: []store.AppInstance{offlineUpgradeInstance("one", "two"), offlineUpgradeInstance("two", "one")},
	}
}

func TestOfflineSiloDoesNotStopHealthyNodesWhenFirstStopIsRefused(t *testing.T) {
	db := siloResumeTestStore()
	remote := &offlineUpgradeRemote{fail: "stop:one"}
	err := NewService(db, remote).runOfflineUpgrade(context.Background(), db, db.instances, offlineSiloBundle{}, registry.RunContext{TaskID: "task_upgrade"})
	if err == nil {
		t.Fatal("stop refusal was accepted")
	}
	for _, forbidden := range []string{"stop:two", "restore-minio:one", "start-old:one", "stop-for-recovery:one", "stop-for-recovery:two"} {
		if slices.Contains(remote.calls, forbidden) {
			t.Fatalf("healthy node was touched: %s", forbidden)
		}
	}
	if db.checkpoint.TaskID != "" {
		t.Fatal("proven untouched nodes should be eligible for a fresh preflight")
	}
}

func TestOfflineSiloResumesAnInterruptedPairFromOriginalBackups(t *testing.T) {
	for _, tc := range []struct {
		name   string
		states map[string]offlineSiloState
	}{
		{"both stopped before cold backup", map[string]offlineSiloState{
			"one": {runtime: "old-stopped", stopAttempted: true}, "two": {runtime: "old-stopped", stopAttempted: true},
		}},
		{"one Silo already active", map[string]offlineSiloState{
			"one": {runtime: "silo-running", stopAttempted: true, backupComplete: true, switchAttempted: true, startAttempted: true},
			"two": {runtime: "silo-stopped", stopAttempted: true, backupComplete: true, switchAttempted: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := siloResumeTestStore()
			remote := &offlineUpgradeRemote{states: tc.states}
			service := NewService(db, remote)
			const original = "task_original"
			nodes, err := service.loadOfflineSiloNodes(db.instances, original, false)
			if err != nil {
				t.Fatal(err)
			}
			for i, node := range nodes {
				nodes[i].before, err = parseOfflineSiloReplication(offlineUpgradeReplicationOutput(node.server.ID, node.meta.PeerID), node.meta)
				if err != nil {
					t.Fatal(err)
				}
			}
			db.checkpoint = newSiloCheckpoint(original, nodes)
			if err := service.runOfflineUpgrade(context.Background(), db, db.instances, offlineSiloBundle{}, registry.RunContext{TaskID: "task_resume"}); err != nil {
				t.Fatal(err)
			}
			if db.checkpoint.TaskID != "" || len(db.saved) != 2 || !strings.Contains(db.saved[0].Metadata, original) {
				t.Fatalf("resume lost original archive or did not close checkpoint: %+v %+v", db.saved, db.checkpoint)
			}
			for _, forbidden := range []string{"stage:one", "stage:two", "stop:one", "stop:two", "start-old:one", "start-old:two"} {
				if slices.Contains(remote.calls, forbidden) {
					t.Fatalf("resume ran %s", forbidden)
				}
			}
			for _, required := range []string{"verify-backup:one", "verify-backup:two", "after:one", "after:two"} {
				if !slices.Contains(remote.calls, required) {
					t.Fatalf("resume omitted %s", required)
				}
			}
		})
	}
}

func TestSiloResumeRejectsPossiblyStaleColdBackups(t *testing.T) {
	states := []offlineSiloState{
		{runtime: "old-running", stopAttempted: true, backupComplete: true},
		{runtime: "old-stopped", stopAttempted: true, backupComplete: true},
	}
	if err := validateSiloResumeBarrier(states); err == nil {
		t.Fatal("a restarted old MinIO might have accepted writes after the cold backup")
	}
}
