package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestDisasterRebuildPlanRequiresIndependentConfirmationsAndExactMapping(t *testing.T) {
	instances, servers := healthyClusterRequestFixtures()
	backupID := "backup_1234567890abcdef12345678"
	marker := store.MySQLMaintenanceMarker{
		Version: 1, State: "required", Reason: "restore_incomplete", Scope: "cluster",
		ClusterID: "cluster_1234567890abcdef12345678", BackupID: backupID,
		TaskID: "tsk_aaaaaaaaaaaaaaaaaaaaaaaa", RestorePhase: "load_complete", RecordedAt: time.Now().UTC(),
	}
	for index := range instances {
		instances[index].Metadata = maintenanceMetadata(t, instances[index].Metadata, marker)
	}
	mapping := map[string]any{}
	for index := range instances {
		mapping[instances[index].ID] = servers[index].ID
	}
	base := registry.RestoreRequest{
		Instance: instances[0], Instances: instances, Servers: servers,
		Backup: store.AppBackup{
			ID: backupID, App: "mysql", InstanceID: instances[0].ID, ServerID: servers[0].ID,
			BackupType: "logical-full", Status: "success", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`,
		},
		RepositoryDir: t.TempDir(), Actor: "owner", Language: "en",
		Parameters: map[string]any{"mode": "disaster-rebuild", "maintenanceConfirmed": true, "disasterConfirmed": true, "serverPasswordsConfirmed": true, "targetMapping": mapping, "threads": 4},
	}
	for _, test := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"maintenance confirmation", func(values map[string]any) { values["maintenanceConfirmed"] = false }},
		{"disaster confirmation", func(values map[string]any) { values["disasterConfirmed"] = false }},
		{"SSH password confirmation", func(values map[string]any) { values["serverPasswordsConfirmed"] = false }},
		{"three-member mapping", func(values map[string]any) { delete(values["targetMapping"].(map[string]any), instances[2].ID) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := base.Clone()
			test.mutate(request.Parameters)
			_, err := NewModule(newBackupFakeStore(t), newBackupFakeRemote()).PlanRestore(context.Background(), request)
			var operation *MySQLOperationError
			if !errors.As(err, &operation) || operation.Code != MySQLRebuildConfirmationRequired {
				t.Fatalf("plan error = %v, want %s", err, MySQLRebuildConfirmationRequired)
			}
		})
	}

	plan, err := NewModule(newBackupFakeStore(t), newBackupFakeRemote()).PlanRestore(context.Background(), base)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"stop-router", "stop-group-replication", "quarantine-old-data", "initialize-clean-seed", "restore-seed", "verify-seed", "create-cluster", "clone-members", "wait-members-online", "bootstrap-router", "verify-router-6446", "record-completion"}
	if got := planStepNames(plan); !reflect.DeepEqual(got, want) {
		t.Fatalf("disaster plan steps = %v, want %v", got, want)
	}
	if got := planTargets(plan); !reflect.DeepEqual(got, []string{"cluster_1234567890abcdef12345678"}) {
		t.Fatalf("disaster plan targets = %v", got)
	}
}

func TestDisasterRebuildQuarantineScriptUsesAtomicTaskScopedRename(t *testing.T) {
	script, err := renderDisasterRebuildScript(DisasterRebuildScriptOptions{
		TaskID: "tsk_1234567890abcdef12345678", InstallRoot: "/aifar/apps/mysql", WorkDir: "/aifar/apps/mysql/_disaster/tsk_1234567890abcdef12345678", DataDir: "/aifar/apps/mysql/data",
		QuarantineDir: "/aifar/apps/mysql/data.quarantine-tsk_1234567890abcdef12345678", Port: 3306,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"readlink -f", "test ! -L", "findmnt", "stat -c %d", "df -Pk", "mv --", "data.quarantine-tsk_1234567890abcdef12345678"} {
		if !strings.Contains(script, want) {
			t.Fatalf("quarantine script missing %q:\n%s", want, script)
		}
	}
	for _, forbidden := range []string{"rm -rf \"$QUARANTINE_DIR\"", "rm -rf /aifar/apps/mysql/data.quarantine", "top-secret"} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("quarantine script contains unsafe %q:\n%s", forbidden, script)
		}
	}
}

func TestDisasterRebuildSuccessfulLifecycleClearsAllMaintenanceMarkers(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	recorder := &restoreProgressRecorder{}
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: recorder}); err != nil {
		t.Fatalf("disaster rebuild failed: %v commands=%v", err, fixture.remote.commands)
	}
	joined := strings.Join(fixture.remote.commands, "\n")
	for _, want := range []string{"systemctl stop aifar-mysql-router", " stop-gr", " quarantine", " initialize", "logical-restore.sh", "__AIFAR_CREATE_CLUSTER__", `recoveryMethod: "clone"`, "__AIFAR_WAIT_ONLINE__", "__AIFAR_ROUTER_BOOTSTRAP__", "__AIFAR_ROUTER_WRITE__"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("successful disaster lifecycle missing %q: %s", want, joined)
		}
	}
	for _, instance := range fixture.instances {
		fresh, err := fixture.db.GetAppInstance(instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, parseErr := store.ParseMySQLMaintenanceMarker(fresh.Metadata); parseErr != nil || present {
			t.Fatalf("member %s maintenance marker present=%v err=%v metadata=%s", instance.ID, present, parseErr, fresh.Metadata)
		}
		if !strings.Contains(fresh.Metadata, `"mysqlDisasterRestore"`) || !strings.Contains(fresh.Metadata, fixture.backup.ID) || !strings.Contains(fresh.Metadata, fixture.taskID) {
			t.Fatalf("member %s missing controlled completion metadata: %s", instance.ID, fresh.Metadata)
		}
	}
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(backup.Metadata, `"seedStage":"verified"`) || !strings.Contains(backup.Metadata, `"routerStage":"verified"`) || !strings.Contains(backup.Metadata, `"restorePhase":"verified"`) || !strings.Contains(backup.Metadata, `"completionStage":"completed"`) || strings.Contains(strings.ToLower(backup.Metadata), "password") {
		t.Fatalf("disaster progress metadata=%s", backup.Metadata)
	}
	if got := recorder.targetFinishes; !reflect.DeepEqual(got, []string{"success"}) {
		t.Fatalf("disaster target status=%v steps=%v", got, recorder.stepStatus)
	}
}

func TestDisasterRebuildUsesAndCleans0600AdminInitializationSecrets(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatal(err)
	}
	adminFiles := 0
	for remotePath, contents := range fixture.remote.uploads {
		if !strings.HasSuffix(remotePath, "/admin-init.sql") {
			continue
		}
		adminFiles++
		if fixture.remote.uploadModes[remotePath] != 0o600 || !strings.Contains(contents, "cluster-password") || !strings.Contains(contents, "ALTER USER 'root'@'localhost'") {
			t.Fatalf("admin init upload path=%s mode=%#o contents=%q", remotePath, fixture.remote.uploadModes[remotePath], contents)
		}
	}
	if adminFiles != 3 {
		t.Fatalf("admin init uploads=%d want=3 uploads=%v", adminFiles, fixture.remote.uploads)
	}
	joined := strings.Join(fixture.remote.commands, "\n")
	if strings.Contains(joined, "cluster-password") || !strings.Contains(joined, "admin-init.sql") {
		t.Fatalf("admin initialization secret cleanup/command boundary invalid: %s", joined)
	}
}

func TestDisasterRebuildAdvancesPersistedRestoreGeneration(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	cluster, err := fixture.db.GetAppCluster(clusterIDFromInstance(fixture.instances[0]))
	if err != nil {
		t.Fatal(err)
	}
	cluster.Metadata = `{"mysqlDisasterRestore":{"version":1,"generation":4,"sourceBackupId":"backup_aaaaaaaaaaaaaaaaaaaaaaaa","taskId":"tsk_aaaaaaaaaaaaaaaaaaaaaaaa","completedAt":"2026-07-27T00:00:00Z"}}`
	if _, err := fixture.db.SaveAppCluster(cluster); err != nil {
		t.Fatal(err)
	}
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatal(err)
	}
	fresh, err := fixture.db.GetAppInstance(fixture.instances[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fresh.Metadata, `"generation":5`) {
		t.Fatalf("restore generation did not advance: %s", fresh.Metadata)
	}
}

func TestDisasterRebuildFailureBoundariesPreserveMaintenanceAndRecoverableState(t *testing.T) {
	for _, test := range []struct {
		name, failAt, required, forbidden string
	}{
		{"before quarantine", "systemctl stop aifar-mysql-router", "", " quarantine"},
		{"seed load", "logical-restore.sh", " quarantine", "__AIFAR_CREATE_CLUSTER__"},
		{"clone", "__AIFAR_CLONE_MEMBER__", "__AIFAR_CREATE_CLUSTER__", "__AIFAR_ROUTER_BOOTSTRAP__"},
		{"router", "__AIFAR_ROUTER_BOOTSTRAP__", "__AIFAR_WAIT_ONLINE__", "__AIFAR_ROUTER_WRITE__"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDisasterRebuildFixture(t, test.failAt)
			err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}})
			if err == nil {
				t.Fatal("injected disaster boundary unexpectedly succeeded")
			}
			joined := strings.Join(fixture.remote.commands, "\n")
			if test.required != "" && !strings.Contains(joined, test.required) {
				t.Fatalf("failure boundary missing prior stage %q: %s", test.required, joined)
			}
			if test.forbidden != "" && strings.Contains(joined, test.forbidden) {
				t.Fatalf("failure boundary crossed into %q: %s", test.forbidden, joined)
			}
			for _, instance := range fixture.instances {
				fresh, getErr := fixture.db.GetAppInstance(instance.ID)
				if getErr != nil {
					t.Fatal(getErr)
				}
				if _, present, parseErr := store.ParseMySQLMaintenanceMarker(fresh.Metadata); parseErr != nil || !present {
					t.Fatalf("member %s lost maintenance after %s: present=%v err=%v metadata=%s", instance.ID, test.name, present, parseErr, fresh.Metadata)
				}
			}
			if test.name != "before quarantine" {
				backup, getErr := fixture.db.GetAppBackup(fixture.backup.ID)
				if getErr != nil || !strings.Contains(backup.Metadata, `"quarantinePaths"`) || strings.Contains(strings.ToLower(backup.Metadata), "password") {
					t.Fatalf("recoverable progress after %s: backup=%+v err=%v", test.name, backup, getErr)
				}
			}
		})
	}
}

func TestDisasterRebuildCompletionTransactionFailurePreservesAllMaintenance(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	failingStore := disasterCompletionFailStore{Store: fixture.db}
	fixture.module = NewModule(failingStore, fixture.remote)
	fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
	}
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("completion transaction failure unexpectedly reported success")
	}
	for _, instance := range fixture.instances {
		fresh, err := fixture.db.GetAppInstance(instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, parseErr := store.ParseMySQLMaintenanceMarker(fresh.Metadata); parseErr != nil || !present {
			t.Fatalf("member %s lost maintenance after transaction failure: present=%v err=%v metadata=%s", instance.ID, present, parseErr, fresh.Metadata)
		}
	}
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil || !strings.Contains(backup.Metadata, `"restorePhase":"restore_incomplete"`) || strings.Contains(backup.Metadata, `"completionStage":"completed"`) {
		t.Fatalf("backup completion published after failed transaction: metadata=%s err=%v", backup.Metadata, err)
	}
}

type disasterCompletionFailStore struct {
	*store.Store
}

func (disasterCompletionFailStore) CompleteMySQLDisasterRebuild([]string, store.MySQLMaintenanceMarker, store.MySQLDisasterRebuildCompletion) error {
	return errors.New("injected control-plane transaction failure")
}

func TestDisasterRebuildRetryDoesNotReloadVerifiedSeed(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "__AIFAR_CLONE_MEMBER__")
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("first clone failure unexpectedly succeeded")
	}
	loadsBefore := fixture.remote.count("logical-restore.sh")
	fixture.remote.failAt = ""
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatalf("same-task retry failed: %v", err)
	}
	if loadsAfter := fixture.remote.count("logical-restore.sh"); loadsAfter != loadsBefore {
		t.Fatalf("verified seed reloaded on retry: before=%d after=%d commands=%v", loadsBefore, loadsAfter, fixture.remote.commands)
	}
}

func TestDisasterRebuildRetryRevalidatesExistingQuarantineIdentity(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "__AIFAR_CLONE_MEMBER__")
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("first clone failure unexpectedly succeeded")
	}
	loadsBefore := fixture.remote.count("logical-restore.sh")
	fixture.remote.failAt = "verify-quarantine"
	fixture.remote.failed = false
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("retry accepted an unverified recorded quarantine")
	}
	if fixture.remote.count("verify-quarantine") == 0 {
		t.Fatalf("retry did not revalidate recorded quarantine: %v", fixture.remote.commands)
	}
	if loadsAfter := fixture.remote.count("logical-restore.sh"); loadsAfter != loadsBefore {
		t.Fatalf("quarantine revalidation failure reloaded seed: before=%d after=%d", loadsBefore, loadsAfter)
	}
}

func TestDisasterRebuildRetryRejectsProgressForUnknownTopologyMember(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "__AIFAR_CLONE_MEMBER__")
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("first clone failure unexpectedly succeeded")
	}
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := strictBackupMetadata(backup.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	var progress disasterRebuildProgress
	if err := json.Unmarshal(metadata["disasterRebuild"], &progress); err != nil {
		t.Fatal(err)
	}
	progress.QuarantinePaths["srv_ffffffffffffffffffffffff"] = "/aifar/apps/mysql/data.quarantine-" + fixture.taskID
	metadata["disasterRebuild"], _ = json.Marshal(progress)
	encoded, _ := json.Marshal(metadata)
	backup.Metadata = string(encoded)
	if _, err := fixture.db.SaveAppBackup(backup); err != nil {
		t.Fatal(err)
	}
	commandsBefore := len(fixture.remote.commands)
	fixture.remote.failAt = ""
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("retry accepted progress for an unknown topology member")
	}
	if len(fixture.remote.commands) != commandsBefore {
		t.Fatalf("invalid progress caused remote mutation: before=%d after=%d", commandsBefore, len(fixture.remote.commands))
	}
}

type disasterRebuildFixture struct {
	db        *store.Store
	module    Module
	remote    *disasterRebuildRemote
	request   registry.RestoreRequest
	instances []store.AppInstance
	backup    store.AppBackup
	taskID    string
}

func newDisasterRebuildFixture(t *testing.T, failAt string) disasterRebuildFixture {
	t.Helper()
	maintenance := newMaintenanceClusterFixture(t)
	serverByID := map[string]store.Server{}
	for _, server := range maintenance.orderedServers {
		serverByID[server.ID] = server
	}
	manifestServers := make([]store.Server, 0, 3)
	for _, instance := range maintenance.instances {
		manifestServers = append(manifestServers, serverByID[instance.ServerID])
	}
	repositoryDir, backup := createClusterRestoreBackup(t, maintenance.instances, manifestServers)
	allInstances, err := maintenance.db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range allInstances {
		if candidate.App != "mysql-router" || clusterIDFromInstance(candidate) != maintenance.clusterID {
			continue
		}
		routerServer, getErr := maintenance.db.GetServer(candidate.ServerID, false)
		if getErr != nil {
			t.Fatal(getErr)
		}
		manifestPath := filepath.Join(filepath.Dir(backup.Path), "backup-manifest.json")
		raw, readErr := os.ReadFile(manifestPath)
		if readErr != nil {
			t.Fatal(readErr)
		}
		manifest, decodeErr := decodeRestoreManifest(raw)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		manifest.Routers = []RouterRef{{InstanceID: candidate.ID, ServerID: candidate.ServerID, Endpoint: routerServer.Host + ":6446", Status: candidate.Status}}
		canonical, encodeErr := CanonicalBackupManifestJSON(manifest)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		if writeErr := os.WriteFile(manifestPath, canonical, 0o600); writeErr != nil {
			t.Fatal(writeErr)
		}
	}
	backup, err = maintenance.db.SaveAppBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	instances := make([]store.AppInstance, 0, 3)
	mapping := map[string]any{}
	for _, instance := range maintenance.instances {
		fresh, getErr := maintenance.db.GetAppInstance(instance.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		instances = append(instances, fresh)
		mapping[fresh.ID] = fresh.ServerID
	}
	remote := &disasterRebuildRemote{restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}, servers: manifestServers, failAt: failAt}
	module := NewModule(maintenance.db, remote)
	module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
	}
	taskID := "tsk_bbbbbbbbbbbbbbbbbbbbbbbb"
	return disasterRebuildFixture{
		db: maintenance.db, module: module, remote: remote, instances: instances, backup: backup, taskID: taskID,
		request: registry.RestoreRequest{
			Instance: instances[0], Instances: instances, Servers: manifestServers, Backup: backup,
			RepositoryDir: repositoryDir, Language: "en", Actor: "owner",
			Parameters: map[string]any{"mode": "disaster-rebuild", "maintenanceConfirmed": true, "disasterConfirmed": true, "serverPasswordsConfirmed": true, "targetMapping": mapping, "threads": 4},
		},
	}
}

type disasterRebuildRemote struct {
	*restoreFakeRemote
	servers     []store.Server
	failAt      string
	failed      bool
	uploadModes map[string]os.FileMode
}

func (r *disasterRebuildRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if r.failAt != "" && !r.failed && strings.Contains(command, r.failAt) {
		r.failed = true
		r.commands = append(r.commands, command)
		return adapter.CommandResult{}, errors.New("injected disaster boundary")
	}
	if strings.Contains(command, "__AIFAR_CLUSTER__") || strings.Contains(command, "__AIFAR_WAIT_ONLINE__") {
		r.commands = append(r.commands, command)
		return adapter.CommandResult{Stdout: healthyClusterRuntime(r.servers)}, nil
	}
	if strings.Contains(command, "__AIFAR_ROUTER_WRITE__") {
		r.commands = append(r.commands, command)
		return adapter.CommandResult{Stdout: "__AIFAR_ROUTER_WRITE__\t1\n__AIFAR_ROUTER_READ__\t1\n"}, nil
	}
	return r.restoreFakeRemote.Run(ctx, server, command)
}

func (r *disasterRebuildRemote) count(fragment string) int {
	count := 0
	for _, command := range r.commands {
		if strings.Contains(command, fragment) {
			count++
		}
	}
	return count
}

func (r *disasterRebuildRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	if r.uploadModes == nil {
		r.uploadModes = map[string]os.FileMode{}
	}
	r.uploadModes[remotePath] = mode.Perm()
	return r.restoreFakeRemote.UploadFile(ctx, server, localPath, remotePath, mode)
}

func maintenanceMetadata(t *testing.T, raw string, marker store.MySQLMaintenanceMarker) string {
	t.Helper()
	metadata, err := strictBackupMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	encodedMarker, err := json.Marshal(marker)
	if err != nil {
		t.Fatal(err)
	}
	metadata["mysqlMaintenance"] = encodedMarker
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
