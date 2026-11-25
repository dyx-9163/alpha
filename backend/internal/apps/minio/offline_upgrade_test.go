package minio

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type offlineUpgradeStore struct {
	fakeStore
	instances  []store.AppInstance
	saved      []store.AppInstance
	saveErr    error
	checkpoint store.SiloOfflineUpgradeCheckpoint
}

func (s *offlineUpgradeStore) ListAppInstances() ([]store.AppInstance, error) {
	return s.instances, nil
}

func (s *offlineUpgradeStore) GetSiloOfflineUpgradeCheckpoint(string) (store.SiloOfflineUpgradeCheckpoint, error) {
	if s.checkpoint.TaskID == "" {
		return s.checkpoint, sql.ErrNoRows
	}
	return s.checkpoint, nil
}

func (s *offlineUpgradeStore) CreateSiloOfflineUpgradeCheckpoint(_ string, checkpoint store.SiloOfflineUpgradeCheckpoint) error {
	if s.checkpoint.TaskID != "" {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	s.checkpoint = checkpoint
	return nil
}

func (s *offlineUpgradeStore) DiscardSiloOfflineUpgradeCheckpoint(_, taskID string) error {
	if taskID != s.checkpoint.TaskID {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	s.checkpoint = store.SiloOfflineUpgradeCheckpoint{}
	return nil
}

func (s *offlineUpgradeStore) CompleteSiloOfflineUpgrade(groupID, taskID string, updates []store.AppInstanceVersionUpdate) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	if err := s.DiscardSiloOfflineUpgradeCheckpoint(groupID, taskID); err != nil {
		return err
	}
	for _, update := range updates {
		item := update.Expected
		item.Version = update.Version
		raw, err := json.Marshal(update.Metadata)
		if err != nil {
			return err
		}
		item.Metadata = string(raw)
		s.saved = append(s.saved, item)
	}
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
	mu         sync.Mutex
	calls      []string
	commands   []string
	states     map[string]offlineSiloState
	probes     map[string]offlineFixtureProbe
	fail       string
	noDeadline bool
}

func (r *offlineUpgradeRemote) Run(ctx context.Context, node store.Server, command string) (adapter.CommandResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, bounded := ctx.Deadline(); !bounded {
		r.noDeadline = true
	}
	header := strings.SplitN(command, "\n", 2)[0]
	if strings.HasPrefix(header, "# aifar-silo-probe ") {
		return r.runProbe(node, command, strings.TrimPrefix(header, "# aifar-silo-probe "))
	}
	phase := strings.TrimPrefix(header, "# aifar-silo ")
	event := phase + ":" + node.ID
	r.calls = append(r.calls, event)
	r.commands = append(r.commands, command)
	if r.states == nil {
		r.states = map[string]offlineSiloState{}
	}
	state := r.states[node.ID]
	if state.runtime == "" {
		state.runtime = "old-running"
	}
	defer func() { r.states[node.ID] = state }()
	if phase == "start" {
		state.startAttempted = true
	}
	if r.fail == event {
		return adapter.CommandResult{Stderr: "SECRET REMOTE OUTPUT"}, errors.New("SECRET ERROR")
	}
	switch phase {
	case "stage-dir":
		return adapter.CommandResult{Stdout: "/tmp/aifar-silo.ABCD1234\n"}, nil
	case "state":
		n := func(flag bool) int {
			if flag {
				return 1
			}
			return 0
		}
		return adapter.CommandResult{Stdout: fmt.Sprintf("AIFAR_SILO_STATE\t%s\t%d\t%d\t%d\t%d\t%d\n",
			state.runtime, n(state.stopAttempted), n(state.backupComplete), n(state.switchAttempted), n(state.startAttempted), n(state.restoreAttempted))}, nil
	case "stop":
		state.stopAttempted, state.runtime = true, "old-stopped"
	case "backup":
		state.backupComplete = true
	case "switch":
		state.switchAttempted, state.runtime = true, "silo-stopped"
	case "start":
		state.runtime = "silo-running"
	case "restore-minio":
		state.restoreAttempted, state.runtime = true, "old-stopped"
	case "start-old":
		state.runtime = "old-running"
	case "stop-for-recovery":
		state.runtime = "silo-stopped"
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

type offlineFixtureProbe struct {
	body    string
	removed map[string]bool
}

var fixtureProbeArg = regexp.MustCompile(`'([^']*)'`)

func (r *offlineUpgradeRemote) runProbe(node store.Server, command, phase string) (adapter.CommandResult, error) {
	r.calls = append(r.calls, "probe:"+phase+":"+node.ID)
	if phase == "retention" {
		return adapter.CommandResult{Stdout: `{"status":"error","error":{"message":"Remote bucket ` + "`%s`" + ` does not support locking","cause":{"message":"","error":{}},"type":"fatal"}}`}, nil
	}
	for _, line := range strings.Split(command, "\n") {
		args, _, ok := strings.Cut(line, "<<'AIFAR_SILO_REPLICATION_PROBE'")
		if !ok {
			continue
		}
		fields := fixtureProbeArg.FindAllStringSubmatch(args, -1)
		if len(fields) != 6 {
			break
		}
		url, bucket, key, value := fields[2][1], fields[3][1], fields[4][1], fields[5][1]
		probeID := bucket + "/" + key
		probe, found := r.probes[probeID]
		const version = "ba91b4f1-7f39-4416-aea1-cf1560349709"
		switch phase {
		case "list":
			if !found || probe.removed[node.ID] {
				return adapter.CommandResult{}, nil
			}
			return adapter.CommandResult{Stdout: fmt.Sprintf(`{"status":"success","type":"file","key":%q,"versionId":%q,"isDeleteMarker":false,"url":%q}`, path.Base(key), version, url+"/"+probeID)}, nil
		case "put":
			if r.probes == nil {
				r.probes = map[string]offlineFixtureProbe{}
			}
			r.probes[probeID] = offlineFixtureProbe{body: value, removed: map[string]bool{}}
		case "stat":
			return adapter.CommandResult{Stdout: fmt.Sprintf(`{"status":"success","versionID":%q,"replicationStatus":"REPLICA"}`, version)}, nil
		case "hash":
			return adapter.CommandResult{Stdout: fmt.Sprintf("%x\n", sha256.Sum256([]byte(probe.body)))}, nil
		case "remove":
			probe.removed[node.ID] = true
		default:
			return adapter.CommandResult{}, fmt.Errorf("unexpected probe phase %s", phase)
		}
		return adapter.CommandResult{}, nil
	}
	return adapter.CommandResult{}, errors.New("unexpected probe invocation")
}

func offlineUpgradeReplicationOutput(node, peer string) string {
	return fmt.Sprintf(`{"status":"success","info":{"deploymentID":%q,"servers":[{"state":"online","drives":[{"state":"ok"}]}]}}
{"status":"success","info":{"deploymentID":%q,"servers":[{"state":"online","drives":[{"state":"ok"}]}]}}
AIFAR_SILO_BUCKET_INVENTORY_BEGIN
{"status":"success","type":"folder","key":"aifar/"}
AIFAR_SILO_BUCKET_INVENTORY_END
{"status":"success","url":"aifar-local/aifar","versioning":{"status":"Enabled"}}
{"status":"success","url":"","rule":{"ID":"rule","Status":"Enabled","Destination":{"Bucket":"arn:rule"}}}
{"status":"success","url":"aifar-local/aifar","remoteTargets":[{"arn":"arn:rule","type":"replication","endpoint":"%s:9000","sourcebucket":"aifar","targetBucket":"aifar","isOnline":false,"lastOnline":"0001-01-01T00:00:00Z","credentials":{"secretKey":"secret"}}],"replicationstats":{"currStats":{"queued":{"curr":{"count":0,"bytes":0}}}}}
`, node, peer, peer)
}

func TestOfflineUpgradeRejectsQueuedOrMisdirectedReplication(t *testing.T) {
	meta, err := parseOfflineSiloMember(offlineUpgradeInstance("one", "two"), false)
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

func (r *offlineUpgradeRemote) UploadFile(ctx context.Context, node store.Server, _, _ string, _ os.FileMode) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, bounded := ctx.Deadline(); !bounded {
		r.noDeadline = true
	}
	r.calls = append(r.calls, "upload:"+node.ID)
	return nil
}

func TestOfflineUpgradeGroupBarriersAndFailureRecovery(t *testing.T) {
	for _, tt := range []struct {
		name, fail   string
		wantOrdered  []string
		wantRecovery []string
	}{
		{"success", "", []string{"stop:one", "stop:two", "backup:one", "backup:two", "verify-backup:one", "verify-backup:two", "switch:one", "switch:two", "start:one", "start:two", "after:one", "after:two"}, nil},
		{"failed backup", "backup:two", []string{"backup:one", "backup:two"}, []string{"restore-minio:one", "restore-minio:two", "start-old:one", "start-old:two"}},
		{"failed verification", "verify-backup:two", []string{"verify-backup:one", "verify-backup:two"}, []string{"restore-minio:one", "restore-minio:two", "start-old:one", "start-old:two"}},
		{"failed Silo start", "start:two", []string{"start:one", "start:two"}, []string{"stop-for-recovery:one", "stop-for-recovery:two"}},
		{"changed replication rule", "changed rule", []string{"after:one", "after:two"}, []string{"stop-for-recovery:one", "stop-for-recovery:two"}},
		{"failed Panel version update", "panel save", []string{"after:one", "after:two"}, nil},
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
			for _, event := range tt.wantRecovery {
				if !slices.Contains(remote.calls, event) {
					t.Fatalf("missing recovery call %s in %v", event, remote.calls)
				}
			}
			if remote.noDeadline {
				t.Fatal("migration remote call or upload ran without a deadline")
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
