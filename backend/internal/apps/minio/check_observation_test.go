package minio

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

func TestMinioCheckDoesNotChangeTheInstanceRevision(t *testing.T) {
	for _, action := range []string{"unchanged", "migrated", "deleted"} {
		t.Run(action, func(t *testing.T) {
			db, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			instance, err := db.SaveAppInstance(store.AppInstance{App: "minio", Version: offlineMinioVersion, ServerID: "s3-1", Metadata: `{"replicationGroupId":"test"}`})
			if err != nil {
				t.Fatal(err)
			}
			switch action {
			case "migrated":
				if err := db.SaveAppInstanceVersionsIfUnchanged([]store.AppInstanceVersionUpdate{{Expected: instance, Version: offlineSiloVersion, Metadata: map[string]any{"serverProduct": "silo", "offlineUpgradeTaskId": "original"}}}); err != nil {
					t.Fatal(err)
				}
			case "deleted":
				if err := db.DeleteAppInstance(instance.ID); err != nil {
					t.Fatal(err)
				}
			}
			remote := &fakeRemote{responses: []adapter.CommandResult{{Stdout: "runtimeStatus=available\nruntimeProduct=minio\nruntimeVersionLine=exe version RELEASE." + offlineMinioVersion + "\n"}}}
			_, err = NewService(db, remote).Check(context.Background(), CheckRequest{Instance: instance, Server: store.Server{ID: instance.ServerID}}, fakeLogger{}, nil)
			if err != nil {
				t.Fatal(err)
			}
			current, err := db.GetAppInstance(instance.ID)
			if action == "deleted" {
				if !store.IsNotFound(err) {
					t.Fatalf("stale observation recreated a deleted instance: %+v, %v", current, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if action == "migrated" {
				if current.Version != offlineSiloVersion || !strings.Contains(current.Metadata, `"offlineUpgradeTaskId":"original"`) || strings.Contains(current.Metadata, `"lastCheck"`) {
					t.Fatalf("old observation overwrote a migrated instance: %+v", current)
				}
				return
			}
			if !instance.UpdatedAt.Equal(current.UpdatedAt) || !strings.Contains(current.Metadata, `"replicationGroupId":"test"`) || !strings.Contains(current.Metadata, `"lastCheck"`) || current.Status != "available" {
				t.Fatalf("runtime observation changed structural state: before=%+v after=%+v", instance, current)
			}
		})
	}
}

func TestMinioCheckReportsTheRunningReleaseSeparatelyFromRegisteredSilo(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(store.AppInstance{App: "minio", Version: offlineSiloVersion, ServerID: "s3-1", Metadata: `{"serverProduct":"silo"}`})
	if err != nil {
		t.Fatal(err)
	}
	remote := &fakeRemote{responses: []adapter.CommandResult{{Stdout: "runtimeStatus=available\nruntimeProduct=minio\nruntimeVersionLine=exe version RELEASE." + offlineMinioVersion + "\n"}}}
	result, err := NewService(db, remote).Check(context.Background(), CheckRequest{Instance: instance, Server: store.Server{ID: instance.ServerID}}, fakeLogger{}, nil)
	if err == nil || result.Status != "unavailable" || result.Details["runtimeProduct"] != "minio" || result.Details["runtimeVersion"] != offlineMinioVersion || result.Details["registeredProduct"] != "silo" || result.Details["registeredVersion"] != offlineSiloVersion || result.Details["runtimeVersionStatus"] != "mismatch" {
		t.Fatalf("runtime drift not reported: %+v (%v)", result, err)
	}
	if !strings.Contains(remote.joinedCommands(), `"/proc/$pid/exe"`) {
		t.Fatal("the check does not inspect the actual running executable")
	}
	current, err := db.GetAppInstance(instance.ID)
	var meta map[string]json.RawMessage
	if err != nil || current.Version != offlineSiloVersion || json.Unmarshal([]byte(current.Metadata), &meta) != nil || string(meta["serverProduct"]) != `"silo"` {
		t.Fatalf("expected Silo registration to remain intact: %+v (%v)", current, err)
	}
	remote.responses = []adapter.CommandResult{{Stdout: "runtimeStatus=available\nruntimeProduct=silo\nruntimeVersionLine=exe version RELEASE." + offlineSiloRelease + "\n"}}
	result, err = NewService(db, remote).Check(context.Background(), CheckRequest{Instance: current, Server: store.Server{ID: current.ServerID}}, fakeLogger{}, nil)
	if err != nil || result.Status != "available" || result.Details["runtimeProduct"] != "silo" || result.Details["runtimeVersion"] != offlineSiloRelease || result.Details["runtimeVersionStatus"] != "match" {
		t.Fatalf("renamed Silo binary was not verified: %+v (%v)", result, err)
	}
	remote.responses = []adapter.CommandResult{{Stdout: "runtimeStatus=available\n"}}
	result, err = NewService(db, remote).Check(context.Background(), CheckRequest{Instance: current, Server: store.Server{ID: current.ServerID}}, fakeLogger{}, nil)
	if err == nil || result.Status != "unavailable" || result.Details["runtimeVersionStatus"] != "unknown" {
		t.Fatalf("an HTTP health check alone verified Silo: %+v (%v)", result, err)
	}
}
