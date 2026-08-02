package mysql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
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

func TestDisasterRebuildInitializedInspectionVerifiesBoundCredential(t *testing.T) {
	script, err := renderDisasterRebuildScript(DisasterRebuildScriptOptions{
		TaskID: "tsk_1234567890abcdef12345678", InstallRoot: "/aifar/apps/mysql", WorkDir: "/aifar/apps/mysql/_disaster/tsk_1234567890abcdef12345678", DataDir: "/aifar/apps/mysql/data",
		QuarantineDir: "/aifar/apps/mysql/data.quarantine-tsk_1234567890abcdef12345678", Port: 3306,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := `"$INSTALL_ROOT/mysql/bin/mysql" --defaults-file="$WORK_DIR/secret-context.cnf" --protocol=tcp --host=127.0.0.1 --port="$PORT" --batch --skip-column-names --execute "SELECT 1"`
	if !strings.Contains(script, want) {
		t.Fatalf("initialized-state inspection does not verify the bound credential with a SQL query:\n%s", script)
	}
}

func TestDisasterRebuildMySQLShellUsesSupportedTabbedOutput(t *testing.T) {
	script, err := renderDisasterRebuildScript(DisasterRebuildScriptOptions{
		TaskID: "tsk_1234567890abcdef12345678", InstallRoot: "/aifar/apps/mysql", WorkDir: "/aifar/apps/mysql/_disaster/tsk_1234567890abcdef12345678", DataDir: "/aifar/apps/mysql/data",
		QuarantineDir: "/aifar/apps/mysql/data.quarantine-tsk_1234567890abcdef12345678", Port: 3306,
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "mysqlsh\" --defaults-file=\"$WORK_DIR/secret-context.cnf\" --sql --result-format=tabbed --skip-column-names") {
		t.Fatalf("disaster rebuild uses unsupported mysqlsh --skip-column-names:\n%s", script)
	}
	if !strings.Contains(script, `--sql --result-format=tabbed`) {
		t.Fatalf("disaster rebuild is missing tabbed mysqlsh output:\n%s", script)
	}
}

func TestDisasterRebuildScriptValidatesCredentialFilesBeforeEveryRead(t *testing.T) {
	script, err := renderDisasterRebuildScript(DisasterRebuildScriptOptions{
		TaskID: "tsk_1234567890abcdef12345678", InstallRoot: "/aifar/apps/mysql", WorkDir: "/aifar/apps/mysql/_disaster/tsk_1234567890abcdef12345678", DataDir: "/aifar/apps/mysql/data",
		QuarantineDir: "/aifar/apps/mysql/data.quarantine-tsk_1234567890abcdef12345678", Port: 3306,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`test -f "$secret_file"`, `test ! -L "$secret_file"`, `stat -c '%u'`, `id -u`, `stat -c '%a'`, `= "600"`} {
		if !strings.Contains(script, want) {
			t.Fatalf("credential validation missing %q:\n%s", want, script)
		}
	}
	for _, action := range []struct {
		name string
		use  string
	}{
		{name: "stop-gr", use: `"$INSTALL_ROOT/mysql/bin/mysqladmin" --defaults-file="$WORK_DIR/secret-context.cnf"`},
		{name: "initialize", use: `-uroot < "$WORK_DIR/admin-init.sql"`},
		{name: "inspect-initialized", use: `"$INSTALL_ROOT/mysql/bin/mysql" --defaults-file="$WORK_DIR/secret-context.cnf"`},
	} {
		start := strings.Index(script, "  "+action.name+")")
		if start < 0 {
			t.Fatalf("missing %s action", action.name)
		}
		section := script[start:]
		validation := strings.Index(section, "validate_secret_file")
		read := strings.Index(section, action.use)
		if validation < 0 || read < 0 || validation > read {
			t.Fatalf("%s reads a credential before validation", action.name)
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

func TestDisasterRebuildUsesSecretBearingSSHServersWithoutPersistingSecrets(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	fixture.remote.requireSSHCredential = true
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatalf("disaster rebuild did not receive production SSH credentials: %v", err)
	}
	if len(fixture.remote.credentialServerIDs) != 4 {
		t.Fatalf("credential-bearing remote targets=%v, want three members plus Router", fixture.remote.credentialServerIDs)
	}
	joined := strings.Join(fixture.remote.commands, "\n")
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(joined, "ssh-fixture-secret") || strings.Contains(backup.Metadata, "ssh-fixture-secret") {
		t.Fatalf("SSH credential leaked to command/progress: commands=%s metadata=%s", joined, backup.Metadata)
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

func TestBootstrapDisasterRouterLocalCredentialCleanupFailureAlsoCleansRemoteAndFails(t *testing.T) {
	remote := newBackupFakeRemote()
	service := NewService(newBackupFakeStore(t), remote)
	originalRemove := removeMySQLCredentialContextFile
	removeMySQLCredentialContextFile = func(name string) error {
		_ = os.Remove(name)
		return errors.New("private router cleanup failure")
	}
	t.Cleanup(func() { removeMySQLCredentialContextFile = originalRemove })
	server := store.Server{ID: "srv_router_cleanup", Host: "10.0.0.9", DeployDir: "/aifar/apps"}
	seed := clusterMemberNode{instance: store.AppInstance{ID: "app_router_seed_1234567890", Version: "8.0.36", Metadata: `{"port":3306}`}, server: store.Server{Host: "10.0.0.8"}}
	router := RouterRef{InstanceID: "app_router_cleanup_1234567890", ServerID: server.ID, Endpoint: "10.0.0.9:6446"}
	credential := store.Credential{Username: "root", Secret: map[string]string{"password": "router-cleanup-test-secret"}}

	err := service.bootstrapDisasterRouter(context.Background(), server, router, seed, credential, "tsk_router_cleanup_123456")
	if err == nil || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "router-cleanup-test-secret") {
		t.Fatalf("local cleanup failure was not fatal and sanitized: %v", err)
	}
	joined := strings.Join(remote.commands, "\n")
	if !strings.Contains(joined, "rm -f --") || !strings.Contains(joined, "router-password") || strings.Contains(joined, "router-cleanup-test-secret") {
		t.Fatalf("remote router credential was not cleaned safely: %s", joined)
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

func TestDisasterRebuildRetryRecreatesFresh0600MySQLContext(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "__AIFAR_CLONE_MEMBER__")
	fixture.remote.enforceRemoteFiles = true
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("first clone failure unexpectedly succeeded")
	}
	fixture.remote.failAt = ""
	fixture.remote.failed = false
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatalf("retry referenced a cleaned MySQL context: %v", err)
	}
	seedID := fixture.backup.InstanceID
	needle := "-" + seedID + "/secret-context.cnf"
	uploads := 0
	for _, event := range fixture.remote.uploadEvents {
		if strings.HasSuffix(event.path, needle) {
			uploads++
			if event.mode != 0o600 {
				t.Fatalf("seed MySQL context mode=%#o path=%s", event.mode, event.path)
			}
		}
	}
	if uploads != 2 {
		t.Fatalf("fresh seed MySQL contexts=%d, want one per attempt; events=%v", uploads, fixture.remote.uploadEvents)
	}
}

func TestDisasterRebuildRetryNeverStopsRebuiltMembers(t *testing.T) {
	for _, boundary := range []string{"clone", "router", "final-publication"} {
		t.Run(boundary, func(t *testing.T) {
			failAt := map[string]string{"clone": "__AIFAR_CLONE_MEMBER__", "router": "__AIFAR_ROUTER_BOOTSTRAP__"}[boundary]
			fixture := newDisasterRebuildFixture(t, failAt)
			fixture.remote.statefulEffects = true
			if boundary == "final-publication" {
				failing := &disasterCompletionFailOnceStore{Store: fixture.db, failures: 1}
				fixture.module = NewModule(failing, fixture.remote)
				fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
					return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
				}
			}
			if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
				t.Fatalf("%s failure unexpectedly succeeded", boundary)
			}
			commandsBefore := len(fixture.remote.commands)
			fixture.remote.failAt = ""
			fixture.remote.failed = false
			if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
				t.Fatalf("%s retry failed: %v", boundary, err)
			}
			retryCommands := strings.Join(fixture.remote.commands[commandsBefore:], "\n")
			if strings.Contains(retryCommands, " stop-gr") {
				t.Fatalf("%s retry stopped rebuilt members: %s", boundary, retryCommands)
			}
			if len(fixture.remote.onlineMembers) != 3 || !fixture.remote.routerRunning {
				t.Fatalf("%s retry did not rebuild the modeled service state: ONLINE=%v RouterRunning=%v", boundary, fixture.remote.onlineMembers, fixture.remote.routerRunning)
			}
			if fixture.remote.effectCounts["clone"] != 2 || fixture.remote.effectCounts["router-bootstrap"] != 1 {
				t.Fatalf("%s retry repeated or skipped a required modeled effect: %v", boundary, fixture.remote.effectCounts)
			}
		})
	}
}

func TestDisasterRebuildStopGroupReplicationAcceptsOnlyControlledOfflineOutcomes(t *testing.T) {
	for _, test := range []struct {
		name      string
		mode      string
		wantError bool
	}{
		{name: "running Group Replication", mode: "stopped"},
		{name: "already stopped", mode: "already-stopped"},
		{name: "mysqld unavailable or lost data", mode: "mysqld-offline"},
		{name: "authentication or command failure", mode: "real-failure", wantError: true},
		{name: "missing controlled result", mode: "missing-marker", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDisasterRebuildFixture(t, "")
			fixture.remote.stopGRMode = test.mode
			err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}})
			if test.wantError && err == nil {
				t.Fatalf("stop-gr mode %q unexpectedly continued into quarantine", test.mode)
			}
			if !test.wantError && err != nil {
				t.Fatalf("controlled stop-gr mode %q failed: %v", test.mode, err)
			}
		})
	}
}

func TestDisasterRebuildStopGroupReplicationScriptClassifiesOnlyVerifiedOutcomes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Git for Windows POSIX layer reports ACL-derived mode 0644 for an os.Chmod(0600) fixture; execute this permission boundary on Linux")
	}
	for _, test := range []struct {
		name          string
		environment   map[string]string
		missingBinary string
		wantOutput    string
		wantSuccess   bool
	}{
		{name: "running Group Replication", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "count1"}, wantOutput: "__AIFAR_STOP_GR__\tstopped\n", wantSuccess: true},
		{name: "authenticated already stopped", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "count0"}, wantOutput: "__AIFAR_STOP_GR__\talready-stopped\n", wantSuccess: true},
		{name: "verified inactive mysqld", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "inactive"}, wantOutput: "__AIFAR_STOP_GR__\tmysqld-offline\n", wantSuccess: true},
		{name: "verified missing service", environment: map[string]string{"FAKE_LOAD_STATE": "not-found", "FAKE_ACTIVE_STATE": "inactive"}, wantOutput: "__AIFAR_STOP_GR__\tmysqld-offline\n", wantSuccess: true},
		{name: "failed service state", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "failed"}},
		{name: "missing systemctl", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "count1"}, missingBinary: "systemctl"},
		{name: "missing mysqladmin", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "count1"}, missingBinary: "mysqladmin"},
		{name: "missing mysqlsh", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "count1"}, missingBinary: "mysqlsh"},
		{name: "sudo failure", environment: map[string]string{"FAKE_UID": "1000", "FAKE_SUDO_FAIL": "1", "FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "count1"}},
		{name: "authentication failure", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "auth-failure"}},
		{name: "malformed member count", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "malformed"}},
		{name: "STOP command failure", environment: map[string]string{"FAKE_LOAD_STATE": "loaded", "FAKE_ACTIVE_STATE": "active", "FAKE_MYSQLSH": "stop-failure"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := runDisasterStopGRScript(t, test.environment, test.missingBinary)
			if test.wantSuccess {
				if result.err != nil || result.stdout != test.wantOutput || result.stderr != "" {
					t.Fatalf("controlled outcome err=%v stdout=%q stderr=%q, want stdout=%q", result.err, result.stdout, result.stderr, test.wantOutput)
				}
				return
			}
			if result.err == nil || strings.Contains(result.stdout, "__AIFAR_STOP_GR__") {
				t.Fatalf("uncontrolled failure was classified as safe: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
			}
		})
	}
}

type disasterStopGRResult struct {
	stdout string
	stderr string
	err    error
}

func runDisasterStopGRScript(t *testing.T, environment map[string]string, missingBinary string) disasterStopGRResult {
	t.Helper()
	shell := disasterTestShell(t)
	root := t.TempDir()
	installRoot := filepath.Join(root, "mysql")
	workDir := filepath.Join(installRoot, "_disaster", "tsk_1234567890abcdef12345678")
	dataDir := filepath.Join(installRoot, "data")
	quarantineDir := dataDir + ".quarantine-tsk_1234567890abcdef12345678"
	fakeBin := filepath.Join(root, "fake-bin")
	for _, directory := range []string{workDir, dataDir, filepath.Join(installRoot, "mysql", "bin"), filepath.Join(installRoot, "mysql-shell", "bin"), fakeBin} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(workDir, "secret-context.cnf"), []byte("[client]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeDisasterShellFake(t, filepath.Join(fakeBin, "id"), `if [ "${1:-}" != "-u" ]; then exit 64; fi; if [ -n "${FAKE_UID:-}" ]; then printf '%s\n' "$FAKE_UID"; else /usr/bin/id -u; fi`)
	writeDisasterShellFake(t, filepath.Join(fakeBin, "sudo"), `if [ "${FAKE_SUDO_FAIL:-0}" = "1" ]; then exit 70; fi; if [ "${1:-}" = "-n" ]; then shift; fi; exec "$@"`)
	writeDisasterShellFake(t, filepath.Join(fakeBin, "systemctl"), `
case "${1:-}" in
  show)
    case "$*" in
      *LoadState*) printf '%s\n' "${FAKE_LOAD_STATE:-loaded}" ;;
      *ActiveState*) printf '%s\n' "${FAKE_ACTIVE_STATE:-active}" ;;
      *) exit 64 ;;
    esac
    ;;
  is-active)
    [ "${FAKE_LOAD_STATE:-loaded}" = "loaded" ] && [ "${FAKE_ACTIVE_STATE:-active}" = "active" ]
    ;;
  *) exit 64 ;;
esac`)
	writeDisasterShellFake(t, filepath.Join(installRoot, "mysql", "bin", "mysqladmin"), `exit "${FAKE_MYSQLADMIN_EXIT:-0}"`)
	writeDisasterShellFake(t, filepath.Join(installRoot, "mysql-shell", "bin", "mysqlsh"), `
case "${FAKE_MYSQLSH:-count1}" in
  auth-failure) exit 75 ;;
  malformed) printf 'not-a-count\n'; exit 0 ;;
  count0) printf '0\n'; exit 0 ;;
  stop-failure)
    case "$*" in *STOP*GROUP_REPLICATION*) exit 76 ;; *) printf '1\n'; exit 0 ;; esac
    ;;
  *)
    case "$*" in *STOP*GROUP_REPLICATION*) exit 0 ;; *) printf '1\n'; exit 0 ;; esac
    ;;
esac`)
	if missingBinary != "" {
		binary := filepath.Join(fakeBin, missingBinary)
		if missingBinary == "mysqladmin" {
			binary = filepath.Join(installRoot, "mysql", "bin", "mysqladmin")
		}
		if missingBinary == "mysqlsh" {
			binary = filepath.Join(installRoot, "mysql-shell", "bin", "mysqlsh")
		}
		if err := os.Remove(binary); err != nil {
			t.Fatal(err)
		}
	}
	script, err := renderDisasterRebuildScript(DisasterRebuildScriptOptions{
		TaskID: "tsk_1234567890abcdef12345678", InstallRoot: disasterShellPath(installRoot), WorkDir: disasterShellPath(workDir),
		DataDir: disasterShellPath(dataDir), QuarantineDir: disasterShellPath(quarantineDir), Port: 3306,
	})
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "disaster-rebuild.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(shell, disasterShellPath(scriptPath), "stop-gr")
	command.Env = append(os.Environ(), "PATH="+disasterShellPath(fakeBin)+":/usr/bin:/bin")
	for key, value := range environment {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err = command.Run()
	return disasterStopGRResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

func disasterTestShell(t *testing.T) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return "/bin/sh"
	}
	for _, candidate := range []string{`D:\tools\git\bin\sh.exe`, `D:\tools\git\usr\bin\bash.exe`} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("POSIX shell is unavailable")
	return ""
}

func disasterShellPath(value string) string {
	value = filepath.Clean(value)
	if runtime.GOOS != "windows" {
		return filepath.ToSlash(value)
	}
	volume := filepath.VolumeName(value)
	drive := strings.ToLower(strings.TrimSuffix(volume, ":"))
	return "/" + drive + filepath.ToSlash(strings.TrimPrefix(value, volume))
}

func writeDisasterShellFake(t *testing.T, name, body string) {
	t.Helper()
	if err := os.WriteFile(name, []byte("#!/bin/sh\nset -eu\n"+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
}

func TestDisasterRebuildRetryReconcilesLocalInfileBeforeContinuing(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	local := &fakeLocalInfileSession{value: "OFF", setErrors: map[string]error{"OFF": errors.New("target unavailable during local_infile restore")}}
	fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return local, func() {}, nil
	}
	fixture.module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
		return local, func() error { return nil }, nil
	}
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("failed local_infile restoration unexpectedly reported success")
	}
	seed, err := fixture.db.GetAppInstance(fixture.backup.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	_, marker, present, parseErr := parseMySQLReconciliationMarker(seed.Metadata)
	if parseErr != nil || !present || marker.OriginalValue != "OFF" || marker.TaskID != fixture.taskID {
		t.Fatalf("local_infile reconciliation marker=%+v present=%v err=%v metadata=%s", marker, present, parseErr, seed.Metadata)
	}
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	progress := mustDisasterProgress(t, backup.Metadata)
	if progress.SeedStage == "loaded" || progress.SeedStage == "verified" {
		t.Fatalf("failed local_infile restoration published seed stage %q", progress.SeedStage)
	}
	loadsBefore := fixture.remote.count("logical-restore.sh")
	commandsBefore := len(fixture.remote.commands)
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("retry bypassed unresolved local_infile reconciliation")
	}
	if len(fixture.remote.commands) != commandsBefore || fixture.remote.count("logical-restore.sh") != loadsBefore {
		t.Fatalf("unreconciled retry performed remote rebuild work: before=%d after=%d", commandsBefore, len(fixture.remote.commands))
	}
	delete(local.setErrors, "OFF")
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatalf("reconciled retry failed: %v", err)
	}
	if fixture.remote.count("logical-restore.sh") != loadsBefore {
		t.Fatalf("reconciled retry reloaded seed: before=%d after=%d", loadsBefore, fixture.remote.count("logical-restore.sh"))
	}
	seed, err = fixture.db.GetAppInstance(fixture.backup.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, present, parseErr := parseMySQLReconciliationMarker(seed.Metadata); parseErr != nil || present {
		t.Fatalf("reconciled retry left marker present=%v err=%v metadata=%s", present, parseErr, seed.Metadata)
	}
}

func TestDisasterRebuildReconciliationCleanupFailuresRetainMarkerAndProgress(t *testing.T) {
	for _, test := range []struct {
		name       string
		cleanupErr error
		cancel     bool
	}{
		{name: "local cleanup failure", cleanupErr: errors.New("private-local-path cleanup failure")},
		{name: "remote cleanup failure", cleanupErr: errors.New("private-remote-path cleanup failure")},
		{name: "both cleanup failures", cleanupErr: errors.Join(errors.New("private-local-path cleanup failure"), errors.New("private-remote-path cleanup failure"))},
		{name: "cancelled reconciliation", cancel: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDisasterRebuildFixture(t, "")
			initial := &fakeLocalInfileSession{value: "OFF", setErrors: map[string]error{"OFF": errors.New("initial restore failed")}}
			fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				return initial, func() {}, nil
			}
			if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
				t.Fatal("fixture did not create a reconciliation marker")
			}
			cleanupCalls := 0
			ctx := context.Background()
			var cancel context.CancelFunc
			if test.cancel {
				ctx, cancel = context.WithCancel(ctx)
			}
			fixture.module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
				session := localInfileSession(&fakeLocalInfileSession{value: "ON"})
				if test.cancel {
					session = cancelOnReconciliationSession{cancel: cancel}
				}
				return session, func() error {
					cleanupCalls++
					return test.cleanupErr
				}, nil
			}
			loadsBefore := fixture.remote.count("logical-restore.sh")
			err := fixture.module.Restore(ctx, fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}})
			if err == nil || cleanupCalls != 1 || strings.Contains(err.Error(), "private-local-path") || strings.Contains(err.Error(), "private-remote-path") {
				t.Fatalf("unsafe reconciliation cleanup result: calls=%d err=%v", cleanupCalls, err)
			}
			seed, getErr := fixture.db.GetAppInstance(fixture.backup.InstanceID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if _, marker, present, parseErr := parseMySQLReconciliationMarker(seed.Metadata); parseErr != nil || !present || marker.TaskID != fixture.taskID {
				t.Fatalf("cleanup failure cleared marker: marker=%+v present=%v err=%v", marker, present, parseErr)
			}
			backup, getErr := fixture.db.GetAppBackup(fixture.backup.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if progress := mustDisasterProgress(t, backup.Metadata); progress.SeedStage != "load-effect-complete" {
				t.Fatalf("cleanup failure published seed stage %q", progress.SeedStage)
			}
			if fixture.remote.count("logical-restore.sh") != loadsBefore {
				t.Fatal("failed reconciliation reloaded the disaster seed")
			}
		})
	}
}

type cancelOnReconciliationSession struct{ cancel context.CancelFunc }

func (s cancelOnReconciliationSession) SetLocalInfile(ctx context.Context, _ string) error {
	s.cancel()
	return ctx.Err()
}

func (s cancelOnReconciliationSession) ReadLocalInfile(ctx context.Context) (string, error) {
	return "", ctx.Err()
}

func TestDisasterRebuildRetryResumesWhenReconciliationProgressTransactionFails(t *testing.T) {
	fixture := newDisasterRebuildFixture(t, "")
	local := &fakeLocalInfileSession{value: "OFF", setErrors: map[string]error{"OFF": errors.New("target unavailable during initial local_infile restore")}}
	fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return local, func() {}, nil
	}
	fixture.module.service.reconciliationSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func() error, error) {
		return local, func() error { return nil }, nil
	}
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("initial local_infile restoration failure unexpectedly succeeded")
	}
	delete(local.setErrors, "OFF")
	rawMaintenanceExec(t, fixture.dbPath, fmt.Sprintf(`create trigger fail_disaster_reconciliation_progress before update on app_backups when old.id='%s' and new.metadata like '%%"seedStage":"loaded"%%' begin select raise(abort,'injected reconciliation progress failure'); end`, fixture.backup.ID))
	loadsBefore := fixture.remote.count("logical-restore.sh")
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("reconciliation progress persistence failure unexpectedly succeeded")
	}
	if local.value != "OFF" {
		t.Fatalf("successful remote reconciliation left local_infile=%s", local.value)
	}
	seed, err := fixture.db.GetAppInstance(fixture.backup.InstanceID)
	if err != nil {
		t.Fatal(err)
	}
	if _, marker, present, parseErr := parseMySQLReconciliationMarker(seed.Metadata); parseErr != nil || !present || marker.TaskID != fixture.taskID {
		t.Fatalf("failed reconciliation-progress transaction did not retain its marker: marker=%+v present=%v err=%v metadata=%s", marker, present, parseErr, seed.Metadata)
	}
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if progress := mustDisasterProgress(t, backup.Metadata); progress.SeedStage != "load-effect-complete" {
		t.Fatalf("failed reconciliation-progress transaction published seed stage %q", progress.SeedStage)
	}
	rawMaintenanceExec(t, fixture.dbPath, `drop trigger fail_disaster_reconciliation_progress`)
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
		t.Fatalf("retry after reconciliation-progress transaction failure did not resume: %v", err)
	}
	if fixture.remote.count("logical-restore.sh") != loadsBefore {
		t.Fatalf("retry reloaded seed after successful reconciliation: before=%d after=%d", loadsBefore, fixture.remote.count("logical-restore.sh"))
	}
	seed, _ = fixture.db.GetAppInstance(fixture.backup.InstanceID)
	if _, _, present, parseErr := parseMySQLReconciliationMarker(seed.Metadata); parseErr != nil || present {
		t.Fatalf("successful retry left reconciliation marker present=%v err=%v", present, parseErr)
	}
}

func TestDisasterRebuildRetryAdoptsRemoteEffectsAfterProgressPersistenceFailure(t *testing.T) {
	for _, stage := range []string{"quarantine", "initialize", "clone"} {
		t.Run(stage, func(t *testing.T) {
			fixture := newDisasterRebuildFixture(t, "")
			fixture.remote.statefulEffects = true
			failing := &disasterProgressFailStore{Store: fixture.db, stage: stage, failures: restorePersistenceAttempts}
			fixture.module = NewModule(failing, fixture.remote)
			fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
				return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
			}
			if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
				t.Fatalf("%s progress persistence failure unexpectedly succeeded", stage)
			}
			if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err != nil {
				t.Fatalf("%s retry did not adopt matching remote effect: %v", stage, err)
			}
			if fixture.remote.effectCounts["quarantine"] != 3 || fixture.remote.effectCounts["initialize"] != 3 || fixture.remote.effectCounts["clone"] != 2 {
				t.Fatalf("%s retry repeated or omitted remote effect: counts=%v", stage, fixture.remote.effectCounts)
			}
		})
	}
}

func TestDisasterRebuildCompletionRejectsDivergentFullProgress(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(*disasterRebuildProgress, *store.MySQLDisasterRebuildCompletion)
	}{
		{name: "canonical manifest digest", apply: func(_ *disasterRebuildProgress, completion *store.MySQLDisasterRebuildCompletion) {
			completion.ManifestSHA256 = strings.Repeat("0", 64)
		}},
		{name: "member stages", apply: func(progress *disasterRebuildProgress, _ *store.MySQLDisasterRebuildCompletion) {
			for id := range progress.MemberStages {
				progress.MemberStages[id] = "cloned"
				break
			}
		}},
		{name: "Router stages", apply: func(progress *disasterRebuildProgress, _ *store.MySQLDisasterRebuildCompletion) {
			for id := range progress.RouterStages {
				delete(progress.RouterStages, id)
				break
			}
		}},
		{name: "quarantine identities", apply: func(progress *disasterRebuildProgress, _ *store.MySQLDisasterRebuildCompletion) {
			for id := range progress.QuarantinePaths {
				delete(progress.QuarantinePaths, id)
				break
			}
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			fixture, marker, completion := readyDisasterCompletionFixture(t)
			backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
			if err != nil {
				t.Fatal(err)
			}
			progress := mustDisasterProgress(t, backup.Metadata)
			mutate.apply(&progress, &completion)
			metadata, _ := strictBackupMetadata(backup.Metadata)
			metadata["disasterRebuild"], _ = json.Marshal(progress)
			encoded, _ := json.Marshal(metadata)
			backup.Metadata = string(encoded)
			if _, err := fixture.db.SaveAppBackup(backup); err != nil {
				t.Fatal(err)
			}
			ids := []string{fixture.instances[0].ID, fixture.instances[1].ID, fixture.instances[2].ID}
			if err := fixture.db.CompleteMySQLDisasterRebuild(ids, marker, completion); err == nil {
				t.Fatalf("completion accepted divergent %s", mutate.name)
			}
			for _, instance := range fixture.instances {
				fresh, _ := fixture.db.GetAppInstance(instance.ID)
				if _, present, _ := store.ParseMySQLMaintenanceMarker(fresh.Metadata); !present {
					t.Fatalf("divergent %s cleared maintenance for %s", mutate.name, instance.ID)
				}
			}
		})
	}
}

func TestDisasterRebuildCompletionRejectsRouterServerOrEndpointDrift(t *testing.T) {
	for _, mutate := range []struct {
		name  string
		apply func(*testing.T, disasterRebuildFixture, store.AppInstance)
	}{
		{name: "Router server", apply: func(t *testing.T, fixture disasterRebuildFixture, router store.AppInstance) {
			router.ServerID = fixture.instances[0].ServerID
			if _, err := fixture.db.SaveAppInstance(router); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "Router host endpoint", apply: func(t *testing.T, fixture disasterRebuildFixture, router store.AppInstance) {
			server, err := fixture.db.GetServer(router.ServerID, false)
			if err != nil {
				t.Fatal(err)
			}
			server.Host = "10.99.0.21"
			if _, err := fixture.db.SaveServer(server); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "Router port endpoint", apply: func(t *testing.T, fixture disasterRebuildFixture, router store.AppInstance) {
			metadata, err := strictBackupMetadata(router.Metadata)
			if err != nil {
				t.Fatal(err)
			}
			metadata["readWritePort"], _ = json.Marshal(7446)
			encoded, _ := json.Marshal(metadata)
			router.Metadata = string(encoded)
			if _, err := fixture.db.SaveAppInstance(router); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			fixture, marker, completion := readyDisasterCompletionFixture(t)
			var router store.AppInstance
			instances, err := fixture.db.ListAppInstances()
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range instances {
				if candidate.App == "mysql-router" && clusterIDFromInstance(candidate) == completion.ClusterID {
					router = candidate
					break
				}
			}
			if router.ID == "" {
				t.Fatal("Router fixture missing")
			}
			identity := completion.RouterIdentities[router.ID]
			if identity.ServerID != router.ServerID || identity.Endpoint != "10.0.0.21:6446" {
				t.Fatalf("pre-drift Router identity=%+v", identity)
			}
			mutate.apply(t, fixture, router)
			ids := []string{fixture.instances[0].ID, fixture.instances[1].ID, fixture.instances[2].ID}
			if err := fixture.db.CompleteMySQLDisasterRebuild(ids, marker, completion); err == nil {
				t.Fatalf("completion accepted %s drift", mutate.name)
			}
			for _, instance := range fixture.instances {
				fresh, _ := fixture.db.GetAppInstance(instance.ID)
				if _, present, _ := store.ParseMySQLMaintenanceMarker(fresh.Metadata); !present {
					t.Fatalf("%s drift cleared maintenance for %s", mutate.name, instance.ID)
				}
			}
		})
	}
}

func TestDisasterRebuildSQLiteFailureRollsBackEveryPublishedState(t *testing.T) {
	fixture, marker, completion := readyDisasterCompletionFixture(t)
	beforeBackup, _ := fixture.db.GetAppBackup(fixture.backup.ID)
	beforeCluster, _ := fixture.db.GetAppCluster(clusterIDFromInstance(fixture.instances[0]))
	beforeMembers, _ := fixture.db.ListAppClusterMembers(beforeCluster.ID)
	beforeInstances, _ := fixture.db.ListAppInstances()
	rawMaintenanceExec(t, fixture.dbPath, `create trigger fail_disaster_router_publication before update on app_instances when old.app='mysql-router' and new.metadata like '%mysqlDisasterRestore%' begin select raise(abort,'injected Router publication failure'); end`)
	ids := []string{fixture.instances[0].ID, fixture.instances[1].ID, fixture.instances[2].ID}
	if err := fixture.db.CompleteMySQLDisasterRebuild(ids, marker, completion); err == nil {
		t.Fatal("real SQLite trigger failure unexpectedly committed")
	} else if !strings.Contains(err.Error(), "injected Router publication failure") {
		t.Fatalf("completion failed before reaching the real SQLite trigger: %v", err)
	}
	afterBackup, _ := fixture.db.GetAppBackup(fixture.backup.ID)
	afterCluster, _ := fixture.db.GetAppCluster(beforeCluster.ID)
	afterMembers, _ := fixture.db.ListAppClusterMembers(beforeCluster.ID)
	afterInstances, _ := fixture.db.ListAppInstances()
	if beforeBackup.Metadata != afterBackup.Metadata || !reflect.DeepEqual(beforeCluster, afterCluster) || !reflect.DeepEqual(beforeMembers, afterMembers) || !reflect.DeepEqual(beforeInstances, afterInstances) {
		t.Fatalf("SQLite rollback diverged:\nbackup before=%s after=%s\ncluster before=%+v after=%+v\nmembers before=%+v after=%+v\ninstances before=%+v after=%+v", beforeBackup.Metadata, afterBackup.Metadata, beforeCluster, afterCluster, beforeMembers, afterMembers, beforeInstances, afterInstances)
	}
}

func readyDisasterCompletionFixture(t *testing.T) (disasterRebuildFixture, store.MySQLMaintenanceMarker, store.MySQLDisasterRebuildCompletion) {
	t.Helper()
	fixture := newDisasterRebuildFixture(t, "")
	failing := &disasterCompletionFailOnceStore{Store: fixture.db, failures: 1}
	fixture.module = NewModule(failing, fixture.remote)
	fixture.module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
	}
	if err := fixture.module.Restore(context.Background(), fixture.request, registry.RunContext{TaskID: fixture.taskID, Log: &restoreProgressRecorder{}}); err == nil {
		t.Fatal("completion setup unexpectedly succeeded")
	}
	backup, err := fixture.db.GetAppBackup(fixture.backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	progress := mustDisasterProgress(t, backup.Metadata)
	fresh, _ := fixture.db.GetAppInstance(fixture.instances[0].ID)
	marker, present, err := store.ParseMySQLMaintenanceMarker(fresh.Metadata)
	if err != nil || !present {
		t.Fatalf("maintenance marker present=%v err=%v", present, err)
	}
	roles := map[string]string{}
	for index, server := range fixture.remote.servers {
		role := "SECONDARY"
		if index == 0 {
			role = "PRIMARY"
		}
		roles[fixture.remote.memberByServer[server.ID]] = role
	}
	completion := store.MySQLDisasterRebuildCompletion{
		ClusterID: progress.ClusterID, SourceBackupID: backup.ID, TaskID: fixture.taskID,
		Generation: progress.RestoreGeneration, Roles: roles, CompletedAt: time.Now().UTC(),
		ManifestSHA256: progress.ManifestSHA256, QuarantinePaths: cloneDisasterStringMap(progress.QuarantinePaths),
		MemberStages: cloneDisasterStringMap(progress.MemberStages), RouterStages: cloneDisasterStringMap(progress.RouterStages),
		RouterIdentities: cloneDisasterRouterIdentities(progress.RouterIdentities),
	}
	return fixture, marker, completion
}

func cloneDisasterRouterIdentities(source map[string]store.MySQLDisasterRouterIdentity) map[string]store.MySQLDisasterRouterIdentity {
	cloned := make(map[string]store.MySQLDisasterRouterIdentity, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func cloneDisasterStringMap(source map[string]string) map[string]string {
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

type disasterProgressFailStore struct {
	*store.Store
	stage    string
	failures int
}

func (s *disasterProgressFailStore) SaveAppBackup(backup store.AppBackup) (store.AppBackup, error) {
	if s.failures > 0 {
		progress := mustDisasterProgressValue(backup.Metadata)
		matches := (s.stage == "quarantine" && len(progress.QuarantinePaths) == 1) ||
			(s.stage == "initialize" && progress.SeedStage == "initialized") ||
			(s.stage == "clone" && containsDisasterStage(progress.MemberStages, "cloned"))
		if matches {
			s.failures--
			return store.AppBackup{}, errors.New("injected disaster progress persistence failure")
		}
	}
	return s.Store.SaveAppBackup(backup)
}

func mustDisasterProgressValue(raw string) disasterRebuildProgress {
	metadata, _ := strictBackupMetadata(raw)
	var progress disasterRebuildProgress
	_ = json.Unmarshal(metadata["disasterRebuild"], &progress)
	return progress
}

func containsDisasterStage(stages map[string]string, expected string) bool {
	for _, stage := range stages {
		if stage == expected {
			return true
		}
	}
	return false
}

func mustDisasterProgress(t *testing.T, raw string) disasterRebuildProgress {
	t.Helper()
	metadata, err := strictBackupMetadata(raw)
	if err != nil {
		t.Fatal(err)
	}
	var progress disasterRebuildProgress
	if err := json.Unmarshal(metadata["disasterRebuild"], &progress); err != nil {
		t.Fatal(err)
	}
	return progress
}

type disasterCompletionFailOnceStore struct {
	*store.Store
	failures int
}

func (s *disasterCompletionFailOnceStore) CompleteMySQLDisasterRebuild(ids []string, marker store.MySQLMaintenanceMarker, completion store.MySQLDisasterRebuildCompletion) error {
	if s.failures > 0 {
		s.failures--
		return errors.New("injected one-time completion failure")
	}
	return s.Store.CompleteMySQLDisasterRebuild(ids, marker, completion)
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
	dbPath    string
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
	storedServers, err := maintenance.db.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	for _, server := range storedServers {
		server.Password = "ssh-fixture-secret"
		if _, err := maintenance.db.SaveServer(server); err != nil {
			t.Fatal(err)
		}
	}
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
	memberByServer := map[string]string{}
	for _, instance := range instances {
		memberByServer[instance.ServerID] = instance.ID
	}
	remote := &disasterRebuildRemote{restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}, servers: manifestServers, failAt: failAt, memberByServer: memberByServer}
	module := NewModule(maintenance.db, remote)
	module.service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
	}
	taskID := "tsk_bbbbbbbbbbbbbbbbbbbbbbbb"
	return disasterRebuildFixture{
		db: maintenance.db, dbPath: maintenance.path, module: module, remote: remote, instances: instances, backup: backup, taskID: taskID,
		request: registry.RestoreRequest{
			Instance: instances[0], Instances: instances, Servers: manifestServers, Backup: backup,
			RepositoryDir: repositoryDir, Language: "en", Actor: "owner",
			Parameters: map[string]any{"mode": "disaster-rebuild", "maintenanceConfirmed": true, "disasterConfirmed": true, "serverPasswordsConfirmed": true, "targetMapping": mapping, "threads": 4},
		},
	}
}

type disasterRebuildRemote struct {
	*restoreFakeRemote
	servers              []store.Server
	failAt               string
	failed               bool
	uploadModes          map[string]os.FileMode
	requireSSHCredential bool
	credentialServerIDs  map[string]bool
	enforceRemoteFiles   bool
	activeFiles          map[string]os.FileMode
	uploadEvents         []disasterUploadEvent
	stopGRMode           string
	statefulEffects      bool
	quarantinedServers   map[string]bool
	initializedMembers   map[string]bool
	onlineMembers        map[string]bool
	memberByServer       map[string]string
	effectCounts         map[string]int
	routerRunning        bool
}

type disasterUploadEvent struct {
	serverID string
	path     string
	mode     os.FileMode
}

var disasterQuotedPath = regexp.MustCompile(`'([^']+)'`)
var disasterScriptMemberID = regexp.MustCompile(`-(app_[0-9a-f]{24})/disaster-rebuild[.]sh`)
var disasterCloneMemberID = regexp.MustCompile(`__AIFAR_CLONE_MEMBER__ (app_[0-9a-f]{24})`)

func disasterRemoteFileKey(serverID, remotePath string) string { return serverID + "\x00" + remotePath }

func (r *disasterRebuildRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if r.requireSSHCredential {
		if server.Password == "" && server.PrivateKey == "" {
			return adapter.CommandResult{}, errors.New("production remote target has no SSH credential")
		}
		if r.credentialServerIDs == nil {
			r.credentialServerIDs = map[string]bool{}
		}
		r.credentialServerIDs[server.ID] = true
	}
	if r.enforceRemoteFiles {
		if r.activeFiles == nil {
			r.activeFiles = map[string]os.FileMode{}
		}
		if strings.Contains(command, "rm -rf --") || strings.Contains(command, "rm -f --") {
			for _, match := range disasterQuotedPath.FindAllStringSubmatch(command, -1) {
				for key := range r.activeFiles {
					prefix := disasterRemoteFileKey(server.ID, match[1])
					if key == prefix || (strings.Contains(command, "rm -rf --") && strings.HasPrefix(key, prefix+"/")) {
						delete(r.activeFiles, key)
					}
				}
			}
		}
		for _, match := range regexp.MustCompile(`--defaults-file='([^']+)'`).FindAllStringSubmatch(command, -1) {
			if _, present := r.activeFiles[disasterRemoteFileKey(server.ID, match[1])]; !present {
				return adapter.CommandResult{}, errors.New("referenced MySQL context was cleaned")
			}
		}
	}
	if r.failAt != "" && !r.failed && strings.Contains(command, r.failAt) {
		r.failed = true
		r.commands = append(r.commands, command)
		return adapter.CommandResult{}, errors.New("injected disaster boundary")
	}
	if strings.HasSuffix(command, " stop-gr") {
		if r.statefulEffects && len(r.onlineMembers) > 0 {
			r.commands = append(r.commands, command)
			return adapter.CommandResult{}, errors.New("cannot stop Group Replication after rebuilt members are ONLINE")
		}
		mode := r.stopGRMode
		if mode == "" {
			mode = "stopped"
		}
		if mode == "real-failure" {
			r.commands = append(r.commands, command)
			return adapter.CommandResult{}, errors.New("injected stop-gr authentication failure")
		}
		r.commands = append(r.commands, command)
		if mode == "missing-marker" {
			return adapter.CommandResult{}, nil
		}
		return adapter.CommandResult{Stdout: "__AIFAR_STOP_GR__\t" + mode + "\n"}, nil
	}
	if r.quarantinedServers == nil {
		r.quarantinedServers = map[string]bool{}
		r.initializedMembers = map[string]bool{}
		r.onlineMembers = map[string]bool{}
		r.effectCounts = map[string]int{}
	}
	{
		memberID := ""
		if match := disasterScriptMemberID.FindStringSubmatch(command); len(match) == 2 {
			memberID = match[1]
		}
		switch {
		case strings.HasSuffix(command, " inspect-quarantine"):
			state := "absent"
			if r.quarantinedServers[server.ID] {
				state = "present"
			}
			r.commands = append(r.commands, command)
			return adapter.CommandResult{Stdout: "__AIFAR_QUARANTINE__\t" + state + "\n"}, nil
		case strings.HasSuffix(command, " quarantine"):
			if r.quarantinedServers[server.ID] {
				return adapter.CommandResult{}, errors.New("quarantine destination already exists")
			}
			r.quarantinedServers[server.ID] = true
			r.effectCounts["quarantine"]++
		case strings.HasSuffix(command, " verify-quarantine"):
			if !r.quarantinedServers[server.ID] {
				return adapter.CommandResult{}, errors.New("quarantine is absent")
			}
		case strings.HasSuffix(command, " inspect-initialized"):
			state := "absent"
			if r.initializedMembers[memberID] {
				state = "present"
			}
			r.commands = append(r.commands, command)
			return adapter.CommandResult{Stdout: "__AIFAR_INITIALIZED__\t" + state + "\n"}, nil
		case strings.HasSuffix(command, " initialize"):
			if r.initializedMembers[memberID] {
				return adapter.CommandResult{}, errors.New("member data directory is already initialized")
			}
			r.initializedMembers[memberID] = true
			r.effectCounts["initialize"]++
		case strings.Contains(command, "__AIFAR_CREATE_CLUSTER__"):
			if r.statefulEffects {
				if !r.initializedMembers[r.memberByServer[server.ID]] {
					return adapter.CommandResult{}, errors.New("seed was not initialized before cluster creation")
				}
				r.onlineMembers[r.memberByServer[server.ID]] = true
			}
		case strings.Contains(command, "__AIFAR_CLONE_MEMBER__"):
			if r.statefulEffects {
				match := disasterCloneMemberID.FindStringSubmatch(command)
				if len(match) != 2 || !r.onlineMembers[r.memberByServer[r.servers[0].ID]] || !r.initializedMembers[match[1]] || r.onlineMembers[match[1]] {
					return adapter.CommandResult{}, errors.New("clone target is already ONLINE")
				}
				r.onlineMembers[match[1]] = true
				r.effectCounts["clone"]++
			}
		}
	}
	if strings.Contains(command, "__AIFAR_CLUSTER__") || strings.Contains(command, "__AIFAR_WAIT_ONLINE__") {
		r.commands = append(r.commands, command)
		if r.statefulEffects {
			return adapter.CommandResult{Stdout: r.statefulClusterRuntime()}, nil
		}
		if strings.Contains(command, "__AIFAR_RECONCILE_CLONE__") {
			server := r.servers[0]
			return adapter.CommandResult{Stdout: "__AIFAR_CLUSTER__\t" + server.Host + "\t3306\tPRIMARY\tONLINE\t123e4567-e89b-12d3-a456-426614174000\n"}, nil
		}
		return adapter.CommandResult{Stdout: healthyClusterRuntime(r.servers)}, nil
	}
	if strings.Contains(command, "__AIFAR_ROUTER_WRITE__") {
		r.commands = append(r.commands, command)
		if r.statefulEffects && (!r.routerRunning || len(r.onlineMembers) != 3) {
			return adapter.CommandResult{}, errors.New("Router DML requires a bootstrapped Router and three ONLINE members")
		}
		return adapter.CommandResult{Stdout: "__AIFAR_ROUTER_WRITE__\t1\n__AIFAR_ROUTER_READ__\t1\n"}, nil
	}
	if strings.Contains(command, "systemctl stop aifar-mysql-router") {
		if r.statefulEffects && r.routerRunning {
			return adapter.CommandResult{}, errors.New("cannot stop Router after it has been rebuilt")
		}
		r.routerRunning = false
	}
	if strings.Contains(command, "__AIFAR_ROUTER_BOOTSTRAP__") {
		if r.statefulEffects {
			if len(r.onlineMembers) != 3 || r.routerRunning {
				return adapter.CommandResult{}, errors.New("invalid Router bootstrap state")
			}
			r.routerRunning = true
			r.effectCounts["router-bootstrap"]++
		}
	}
	return r.restoreFakeRemote.Run(ctx, server, command)
}

func (r *disasterRebuildRemote) statefulClusterRuntime() string {
	var output strings.Builder
	for index, server := range r.servers {
		instanceID := r.memberByServer[server.ID]
		if !r.onlineMembers[instanceID] {
			continue
		}
		role := "SECONDARY"
		if index == 0 {
			role = "PRIMARY"
		}
		fmt.Fprintf(&output, "__AIFAR_CLUSTER__\t%s\t3306\t%s\tONLINE\t%d23e4567-e89b-12d3-a456-426614174000\n", server.Host, role, index+1)
	}
	return output.String()
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
	if r.requireSSHCredential {
		if server.Password == "" && server.PrivateKey == "" {
			return errors.New("production upload target has no SSH credential")
		}
		if r.credentialServerIDs == nil {
			r.credentialServerIDs = map[string]bool{}
		}
		r.credentialServerIDs[server.ID] = true
	}
	if r.enforceRemoteFiles {
		if r.activeFiles == nil {
			r.activeFiles = map[string]os.FileMode{}
		}
		r.activeFiles[disasterRemoteFileKey(server.ID, remotePath)] = mode.Perm()
		r.uploadEvents = append(r.uploadEvents, disasterUploadEvent{serverID: server.ID, path: remotePath, mode: mode.Perm()})
	}
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
