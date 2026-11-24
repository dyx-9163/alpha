package minio

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type offlineUpgradeStore struct {
	fakeStore
	instances []store.AppInstance
	saved     []store.AppInstance
	saveErr   error
}

func (s *offlineUpgradeStore) ListAppInstances() ([]store.AppInstance, error) {
	return s.instances, nil
}

func (s *offlineUpgradeStore) SaveAppInstancesIfUnchanged(items []store.AppInstance) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	s.saved = items
	return nil
}

func offlineUpgradeInstance(server, peer string) store.AppInstance {
	return store.AppInstance{
		ID: server + "-instance", App: "minio", Version: "2025-10-15T17-29-55Z", Status: "installed",
		Topology: "bucket-replication", ServerID: server,
		Metadata: fmt.Sprintf(`{"replicationGroupId":"group","replicationMode":"bucket","peerServerId":%q,"apiPort":9000,"consolePort":9001,"serviceName":"aifar-minio","replicationBuckets":["aifar"],"dataDir":"/vol/minio","dataDirs":["/vol/minio"],"endpoint":"http://%s:9000","peerEndpoint":"http://%s:9000"}`, peer, server, peer),
	}
}

func TestResolveOfflineUpgradeGroupRejectsAmbiguousMembership(t *testing.T) {
	first := offlineUpgradeInstance("one", "two")
	second := offlineUpgradeInstance("two", "one")
	third := offlineUpgradeInstance("three", "one")
	db := &offlineUpgradeStore{instances: []store.AppInstance{first, second, third}}
	module := NewModule(db, nil)
	if _, err := module.ResolveOfflineUpgradeGroup(first); err == nil {
		t.Fatal("a third row in the recorded group must block the migration")
	}
	db.instances = []store.AppInstance{second, first}
	group, err := module.ResolveOfflineUpgradeGroup(first)
	if err != nil || !reflect.DeepEqual(group, []store.AppInstance{first, second}) {
		t.Fatalf("unexpected group %v, error %v", group, err)
	}
	db.instances[0].Metadata = strings.Replace(second.Metadata, `"peerServerId":"one"`, `"peerServerId":"other"`, 1)
	if _, err := module.ResolveOfflineUpgradeGroup(first); err == nil {
		t.Fatal("a nonreciprocal peer must block the migration")
	}
}

func TestResolveOfflineUpgradeGroupAcceptsSuccessfullyCheckedMembers(t *testing.T) {
	first := offlineUpgradeInstance("one", "two")
	second := offlineUpgradeInstance("two", "one")
	first.Status, second.Status = "available", "available"
	db := &offlineUpgradeStore{instances: []store.AppInstance{first, second}}
	group, err := NewModule(db, nil).ResolveOfflineUpgradeGroup(first)
	if err != nil || !reflect.DeepEqual(group, db.instances) {
		t.Fatalf("healthy checked members were rejected: group=%v err=%v", group, err)
	}
}

type offlineUpgradeRemote struct {
	calls []string
	fail  string
}

func (r *offlineUpgradeRemote) Run(_ context.Context, node store.Server, command string) (adapter.CommandResult, error) {
	phase := strings.SplitN(command, "\n", 2)[0]
	phase = strings.TrimPrefix(phase, "# aifar-silo ")
	r.calls = append(r.calls, phase+":"+node.ID)
	switch phase + ":" + node.ID {
	case r.fail:
		return adapter.CommandResult{Stderr: "SECRET REMOTE OUTPUT"}, errors.New("SECRET ERROR")
	case "stage-dir:one", "stage-dir:two":
		return adapter.CommandResult{Stdout: "/tmp/aifar-silo.ABCD1234\n"}, nil
	}
	switch phase {
	case "before", "after":
		peer := "one"
		if node.ID == "one" {
			peer = "two"
		}
		output := offlineUpgradeReplicationOutput(node.ID, peer)
		if phase == "after" && node.ID == "two" && r.fail == "changed rule" {
			output = strings.Replace(output, `"ID":"rule"`, `"ID":"modified"`, 1)
		}
		return adapter.CommandResult{Stdout: output}, nil
	}
	return adapter.CommandResult{}, nil
}

func offlineUpgradeReplicationOutput(node, peer string) string {
	return fmt.Sprintf(`{"status":"success","info":{"deploymentID":%q,"servers":[{"state":"online","drives":[{"state":"ok"}]}]}}
{"status":"success","info":{"deploymentID":%q,"servers":[{"state":"online","drives":[{"state":"ok"}]}]}}
{"status":"success","url":"aifar-local/aifar","versioning":{"status":"Enabled"}}
{"status":"success","url":"","rule":{"ID":"rule","Status":"Enabled","Destination":{"Bucket":"arn:rule"}}}
{"status":"success","url":"aifar-local/aifar","remoteTargets":[{"arn":"arn:rule","type":"replication","endpoint":"%s:9000","sourcebucket":"aifar","targetBucket":"aifar","isOnline":false,"lastOnline":"0001-01-01T00:00:00Z","credentials":{"secretKey":"secret"}}],"replicationstats":{"currStats":{"queued":{"curr":{"count":0,"bytes":0}}}}}
`, node, peer, peer)
}

func TestOfflineUpgradeRejectsQueuedOrMisdirectedReplication(t *testing.T) {
	meta, err := parseOfflineSiloMember(offlineUpgradeInstance("one", "two"))
	if err != nil {
		t.Fatal(err)
	}
	output := offlineUpgradeReplicationOutput("one", "two")
	if _, err := parseOfflineSiloReplication(output, meta); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{
		strings.Replace(output, `"count":0`, `"count":1`, 1),
		strings.Replace(output, `"endpoint":"two:9000"`, `"endpoint":"wrong:9000"`, 1),
		strings.Replace(output, `"state":"ok"`, `"state":"offline"`, 1),
		strings.Replace(output, `"lastOnline":"0001-01-01T00:00:00Z"`, `"lastOnline":"2026-11-23T10:00:00Z"`, 1),
	} {
		if _, err := parseOfflineSiloReplication(invalid, meta); err == nil {
			t.Fatal("accepted unhealthy replication")
		}
	}
}

func (r *offlineUpgradeRemote) UploadFile(_ context.Context, node store.Server, _, _ string, _ os.FileMode) error {
	r.calls = append(r.calls, "upload:"+node.ID)
	return nil
}

func TestOfflineUpgradeGroupBarriersAndFailureRecovery(t *testing.T) {
	for _, tt := range []struct {
		name, fail  string
		wantOrdered []string
	}{
		{"success", "", []string{"stop:one", "stop:two", "backup:one", "backup:two", "verify-backup:one", "verify-backup:two", "switch:one", "switch:two", "start:one", "start:two", "after:one", "after:two"}},
		{"failed backup", "backup:two", []string{"backup:one", "backup:two", "restore-minio:one", "restore-minio:two", "start-old:one", "start-old:two"}},
		{"failed verification", "verify-backup:two", []string{"verify-backup:one", "verify-backup:two", "restore-minio:one", "restore-minio:two", "start-old:one", "start-old:two"}},
		{"failed Silo start", "start:two", []string{"start:one", "start:two", "stop-for-recovery:one", "stop-for-recovery:two"}},
		{"changed replication rule", "changed rule", []string{"after:one", "after:two", "stop-for-recovery:one", "stop-for-recovery:two"}},
		{"failed Panel version update", "panel save", []string{"after:one", "after:two"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			first, second := offlineUpgradeInstance("one", "two"), offlineUpgradeInstance("two", "one")
			db := &offlineUpgradeStore{
				fakeStore: fakeStore{servers: map[string]store.Server{
					"one": {ID: "one", Host: "one"},
					"two": {ID: "two", Host: "two"},
				}},
				instances: []store.AppInstance{first, second},
			}
			if tt.fail == "panel save" {
				db.saveErr = store.ErrAppInstanceConflict
			}
			remote := &offlineUpgradeRemote{fail: tt.fail}
			err := NewService(db, remote).runOfflineUpgrade(context.Background(), db, []store.AppInstance{first, second}, offlineSiloBundle{}, registry.RunContext{TaskID: "task_upgrade"})
			if (err != nil) != (tt.fail != "") {
				t.Fatalf("unexpected error %v with calls %v", err, remote.calls)
			}
			if err != nil && strings.Contains(err.Error(), "SECRET") {
				t.Fatal("operation exposed remote output")
			}
			remaining := tt.wantOrdered
			for _, event := range remote.calls {
				if len(remaining) > 0 && remaining[0] == event {
					remaining = remaining[1:]
				}
			}
			if len(remaining) > 0 {
				t.Fatalf("missing ordered calls %v after %v", remaining, remote.calls)
			}
			if tt.fail == "" {
				if len(db.saved) != 2 || db.saved[0].Version != "20261112" || db.saved[1].Version != "20261112" {
					t.Fatalf("versions not saved together: %v", db.saved)
				}
			} else if len(db.saved) != 0 {
				t.Fatalf("failed migration saved versions: %v", db.saved)
			}
			if (tt.fail == "start:two" || tt.fail == "changed rule") && strings.Contains(strings.Join(remote.calls, ","), "restore-minio") {
				t.Fatal("cannot start the old binary on Silo-touched storage")
			}
			if tt.fail == "panel save" {
				if strings.Contains(strings.Join(remote.calls, ","), "stop-for-recovery") {
					t.Fatal("a failed Panel version update must leave verified Silo processes running")
				}
				if !errors.Is(err, store.ErrAppInstanceConflict) || !strings.Contains(err.Error(), "/_backups/minio-silo/task_upgrade") {
					t.Fatalf("expected Panel reconciliation error with retained backup path: %v", err)
				}
			}
			if strings.Contains(tt.fail, "backup:") && strings.Contains(strings.Join(remote.calls, ","), "switch:") {
				t.Fatal("cannot switch either binary before both backups are verified")
			}
		})
	}
}
