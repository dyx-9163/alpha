package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestClearMaintenanceStandaloneUsesCompleteBoundCredentialWithoutSecretCommand(t *testing.T) {
	// Production break caught: using instance metadata for the username or
	// embedding the bound password in a command can reject a healthy remediated
	// instance and expose its credential in process/audit-visible command text.
	db := openMaintenanceTestStore(t)
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone",
		Metadata: `{"port":3306,"rootUser":"metadata_root"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	marker := maintenanceTestMarker("standalone", "", "schema_mutation_started")
	if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
		t.Fatal(err)
	}
	credential, err := db.SaveCredential(store.Credential{
		Name: "mysql-admin", Kind: "mysql", Username: "bound_admin", Status: "active",
		Secret: map[string]string{"password": "bound-password-sentinel"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindCredential(store.CredentialBinding{
		CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin",
	}); err != nil {
		t.Fatal(err)
	}
	remote := &maintenancePingRemote{}
	module := NewModule(db, remote, "must-not-be-used-default")

	if err := module.ClearMaintenance(context.Background(), instance, "en", marker.TaskID, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.uploadedSecret, `user="bound_admin"`) ||
		!strings.Contains(remote.uploadedSecret, `password="bound-password-sentinel"`) {
		t.Fatalf("uploaded controlled credential context did not use complete binding: %q", remote.uploadedSecret)
	}
	commands := strings.Join(remote.commands, "\n")
	for _, forbidden := range []string{"bound_admin", "bound-password-sentinel", "metadata_root", "must-not-be-used-default"} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("maintenance ping command exposed or substituted credential value %q: %s", forbidden, commands)
		}
	}
	if !strings.Contains(commands, "--defaults-file=") {
		t.Fatalf("maintenance ping did not consume the controlled credential context: %s", commands)
	}
	fresh, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := store.ParseMySQLMaintenanceMarker(fresh.Metadata); err != nil || present {
		t.Fatalf("successful health confirmation did not clear marker: present=%v err=%v metadata=%s", present, err, fresh.Metadata)
	}
}

func TestClearMaintenanceStandaloneRejectsFailedHealthAndRetainsMarker(t *testing.T) {
	db := openMaintenanceTestStore(t)
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{"port":3306}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	marker := maintenanceTestMarker("standalone", "", "schema_mutation_started")
	if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
		t.Fatal(err)
	}
	bindMaintenanceCredential(t, db, instance.ID, "bound_admin", "bound-password")
	remote := &maintenancePingRemote{pingErr: errors.New("ping rejected")}

	err = NewModule(db, remote).ClearMaintenance(context.Background(), instance, "en", marker.TaskID, fakeLogger{})
	if maintenanceErrorCode(err) != MySQLMaintenanceStateInvalid {
		t.Fatalf("error=%T %v", err, err)
	}
	fresh, getErr := db.GetAppInstance(instance.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got, present, parseErr := store.ParseMySQLMaintenanceMarker(fresh.Metadata); parseErr != nil || !present || !sameMaintenanceMarker(got, marker) {
		t.Fatalf("failed health changed marker: marker=%+v present=%v err=%v metadata=%s", got, present, parseErr, fresh.Metadata)
	}
}

func TestClearMaintenanceStandaloneRequiresIndependentCredentialContextCleanup(t *testing.T) {
	// Production break caught: a successful ping must not clear the durable
	// marker unless both the local credential file and remote work directory
	// were cleaned. Every failure path still attempts both cleanups.
	tests := []struct {
		name               string
		configureRemote    func(*maintenancePingRemote)
		configureRemoval   func(*Module, *string)
		wantCode           string
		wantMarker         bool
		wantUploadCalls    int
		wantPingCalls      int
		wantLocalFile      bool
		forbiddenRawDetail string
	}{
		{
			name:            "success",
			wantMarker:      false,
			wantUploadCalls: 1,
			wantPingCalls:   1,
		},
		{
			name: "upload failure",
			configureRemote: func(remote *maintenancePingRemote) {
				remote.uploadErr = errors.New("upload raw detail bound_admin bound-password")
			},
			wantCode:           MySQLMaintenanceStateInvalid,
			wantMarker:         true,
			wantUploadCalls:    1,
			forbiddenRawDetail: "upload raw detail",
		},
		{
			name: "ping command failure",
			configureRemote: func(remote *maintenancePingRemote) {
				remote.pingErr = errors.New("ping raw detail bound_admin bound-password")
			},
			wantCode:           MySQLMaintenanceStateInvalid,
			wantMarker:         true,
			wantUploadCalls:    1,
			wantPingCalls:      1,
			forbiddenRawDetail: "ping raw detail",
		},
		{
			name: "local cleanup failure",
			configureRemoval: func(module *Module, failedPath *string) {
				module.service.removeMaintenanceSecret = func(localPath string) error {
					*failedPath = localPath
					return fmt.Errorf("remove raw detail %s bound_admin bound-password", localPath)
				}
			},
			wantCode:           MySQLMaintenanceStateInvalid,
			wantMarker:         true,
			wantUploadCalls:    1,
			wantPingCalls:      1,
			wantLocalFile:      true,
			forbiddenRawDetail: "remove raw detail",
		},
		{
			name: "remote cleanup failure",
			configureRemote: func(remote *maintenancePingRemote) {
				remote.cleanupErr = errors.New("cleanup raw detail bound_admin bound-password")
			},
			wantCode:           MySQLMaintenanceStateInvalid,
			wantMarker:         true,
			wantUploadCalls:    1,
			wantPingCalls:      1,
			forbiddenRawDetail: "cleanup raw detail",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMaintenanceTestStore(t)
			server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := db.SaveAppInstance(store.AppInstance{
				App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{"port":3306}`,
			})
			if err != nil {
				t.Fatal(err)
			}
			marker := maintenanceTestMarker("standalone", "", "schema_mutation_started")
			if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
				t.Fatal(err)
			}
			bindMaintenanceCredential(t, db, instance.ID, "bound_admin", "bound-password")
			remote := &maintenancePingRemote{}
			if test.configureRemote != nil {
				test.configureRemote(remote)
			}
			module := NewModule(db, remote)
			failedLocalPath := ""
			if test.configureRemoval != nil {
				test.configureRemoval(&module, &failedLocalPath)
			}
			logger := &recordingLogger{}
			err = module.ClearMaintenance(context.Background(), instance, "en", marker.TaskID, logger)
			if got := maintenanceErrorCode(err); got != test.wantCode {
				t.Fatalf("error code=%q want=%q err=%T %v", got, test.wantCode, err, err)
			}
			if remote.uploadCalls != test.wantUploadCalls || remote.pingCalls != test.wantPingCalls || remote.cleanupRuns != 1 {
				t.Fatalf("remote calls upload=%d ping=%d cleanup=%d", remote.uploadCalls, remote.pingCalls, remote.cleanupRuns)
			}
			localPath := remote.uploadedLocalPath
			if failedLocalPath != "" {
				localPath = failedLocalPath
				t.Cleanup(func() { _ = os.Remove(failedLocalPath) })
			}
			if localPath == "" {
				t.Fatal("probe did not expose its local credential path to the controlled test boundary")
			}
			_, statErr := os.Lstat(localPath)
			if test.wantLocalFile {
				if statErr != nil {
					t.Fatalf("injected cleanup failure did not leave the test credential file: %v", statErr)
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("local credential context survived completed cleanup: %v", statErr)
			}
			fresh, getErr := db.GetAppInstance(instance.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			_, markerPresent, parseErr := store.ParseMySQLMaintenanceMarker(fresh.Metadata)
			if parseErr != nil || markerPresent != test.wantMarker {
				t.Fatalf("marker present=%v want=%v err=%v metadata=%s", markerPresent, test.wantMarker, parseErr, fresh.Metadata)
			}
			visible := ""
			if err != nil {
				visible = err.Error()
			}
			visible += "\n" + logger.joined()
			for _, forbidden := range []string{localPath, "bound_admin", "bound-password", test.forbiddenRawDetail} {
				if forbidden != "" && strings.Contains(visible, forbidden) {
					t.Fatalf("sensitive cleanup detail leaked in user-visible evidence: %q", visible)
				}
			}
		})
	}
}

func TestProbeMySQLMaintenanceCredentialJoinsPrimaryAndCleanupFailuresGenerically(t *testing.T) {
	// Production break caught: a primary ping error must not mask either local
	// or remote cleanup failure, and raw cleanup details must never escape.
	tests := []struct {
		name            string
		configure       func(*Service, *maintenancePingRemote)
		wantCleanupText string
	}{
		{
			name: "local cleanup",
			configure: func(service *Service, _ *maintenancePingRemote) {
				service.removeMaintenanceSecret = func(localPath string) error {
					return fmt.Errorf("raw local cleanup detail %s bound_admin bound-password", localPath)
				}
			},
			wantCleanupText: "unable to clean local MySQL maintenance credential context",
		},
		{
			name: "remote cleanup",
			configure: func(_ *Service, remote *maintenancePingRemote) {
				remote.cleanupErr = errors.New("raw remote cleanup detail bound_admin bound-password")
			},
			wantCleanupText: "unable to clean MySQL maintenance health check",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			remote := &maintenancePingRemote{pingErr: errors.New("raw ping detail bound_admin bound-password")}
			service := NewService(nil, remote)
			test.configure(&service, remote)
			server := store.Server{Host: "10.0.0.8"}
			instance := store.AppInstance{Version: "8.0.36", Metadata: `{"port":3306}`}
			credential := store.Credential{Username: "bound_admin", Secret: map[string]string{"password": "bound-password"}}
			err := service.probeMySQLMaintenanceCredential(context.Background(), server, instance, credential, "tsk_1234567890abcdef12345678", &recordingLogger{})
			if err == nil || !strings.Contains(err.Error(), "MySQL maintenance health check failed") || !strings.Contains(err.Error(), test.wantCleanupText) {
				t.Fatalf("primary and cleanup errors were not joined: %v", err)
			}
			for _, forbidden := range []string{"raw ping detail", "raw local cleanup detail", "raw remote cleanup detail", "bound_admin", "bound-password", remote.uploadedLocalPath} {
				if forbidden != "" && strings.Contains(err.Error(), forbidden) {
					t.Fatalf("sensitive cleanup detail leaked from probe: %v", err)
				}
			}
			if remote.cleanupRuns != 1 {
				t.Fatalf("remote cleanup runs=%d want=1", remote.cleanupRuns)
			}
			if remote.uploadedLocalPath != "" {
				_ = os.Remove(remote.uploadedLocalPath)
			}
		})
	}
}

func TestClearMaintenanceRejectsAbsentDivergentAndReconciliationState(t *testing.T) {
	t.Run("absent marker", func(t *testing.T) {
		db := openMaintenanceTestStore(t)
		server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{}`})
		if err != nil {
			t.Fatal(err)
		}
		remote := &maintenancePingRemote{}
		err = NewModule(db, remote).ClearMaintenance(context.Background(), instance, "en", "tsk_1234567890abcdef12345678", fakeLogger{})
		if maintenanceErrorCode(err) != MySQLMaintenanceStateInvalid || len(remote.commands) != 0 {
			t.Fatalf("absent marker error=%v commands=%v", err, remote.commands)
		}
	})

	t.Run("reconciliation marker", func(t *testing.T) {
		db := openMaintenanceTestStore(t)
		server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{
			App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone",
			Metadata: `{"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_1234567890abcdef12345678"}}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		marker := maintenanceTestMarker("standalone", "", "schema_mutation_started")
		if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
			t.Fatal(err)
		}
		remote := &maintenancePingRemote{}
		err = NewModule(db, remote).ClearMaintenance(context.Background(), instance, "en", marker.TaskID, fakeLogger{})
		if maintenanceErrorCode(err) != MySQLReconciliationRequired || len(remote.commands) != 0 {
			t.Fatalf("reconciliation error=%v commands=%v", err, remote.commands)
		}
	})

	t.Run("divergent cluster marker", func(t *testing.T) {
		fixture := newMaintenanceClusterFixture(t)
		fresh, err := fixture.db.GetAppInstance(fixture.instances[1].ID)
		if err != nil {
			t.Fatal(err)
		}
		metadata := map[string]json.RawMessage{}
		if err := json.Unmarshal([]byte(fresh.Metadata), &metadata); err != nil {
			t.Fatal(err)
		}
		divergent := fixture.marker
		divergent.TaskID = "tsk_abcdefabcdefabcdefabcdef"
		metadata["mysqlMaintenance"], _ = json.Marshal(divergent)
		encoded, _ := json.Marshal(metadata)
		fresh.Metadata = string(encoded)
		if _, err := fixture.db.SaveAppInstance(fresh); err != nil {
			t.Fatal(err)
		}
		remote := fixture.healthyRemote()
		err = NewModule(fixture.db, remote).ClearMaintenance(context.Background(), fixture.instances[0], "en", fixture.marker.TaskID, fakeLogger{})
		if maintenanceErrorCode(err) != MySQLMaintenanceStateInvalid || len(remote.commands) != 0 {
			t.Fatalf("divergent marker error=%v commands=%v", err, remote.commands)
		}
		for _, instance := range fixture.instances {
			current, getErr := fixture.db.GetAppInstance(instance.ID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if _, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata); parseErr != nil || !present {
				t.Fatalf("divergent rejection partially cleared %s: present=%v err=%v", instance.ID, present, parseErr)
			}
		}
	})
}

func TestClearMaintenanceClusterRequiresThreeOnlineOnePrimaryAndRouterDMLThenClearsAtomically(t *testing.T) {
	fixture := newMaintenanceClusterFixture(t)
	remote := fixture.healthyRemote()
	if err := NewModule(fixture.db, remote).ClearMaintenance(context.Background(), fixture.instances[1], "en", fixture.marker.TaskID, fakeLogger{}); err != nil {
		t.Fatal(err)
	}
	if remote.runtimeCalls < 2 {
		t.Fatalf("cluster health was not rechecked for clear: runtimeCalls=%d", remote.runtimeCalls)
	}
	commands := strings.Join(remote.commands, "\n")
	if !strings.Contains(commands, "START TRANSACTION READ WRITE") ||
		!strings.Contains(commands, "CREATE TEMPORARY TABLE aifar_router_verify") ||
		!strings.Contains(commands, "ROLLBACK") {
		t.Fatalf("Router 6446 DML health transaction was not required:\n%s", commands)
	}
	for _, instance := range fixture.instances {
		current, err := fixture.db.GetAppInstance(instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata); parseErr != nil || present {
			t.Fatalf("cluster marker not atomically cleared for %s: present=%v err=%v metadata=%s", instance.ID, present, parseErr, current.Metadata)
		}
	}
}

func TestClearMaintenanceClusterHealthOrAtomicClearFailureRetainsEveryMarker(t *testing.T) {
	t.Run("split primary", func(t *testing.T) {
		fixture := newMaintenanceClusterFixture(t)
		remote := fixture.healthyRemote()
		remote.runtimes = []string{runtimeWithPrimaryRoles(fixture.orderedServers, []string{"PRIMARY", "PRIMARY", "SECONDARY"})}
		err := NewModule(fixture.db, remote).ClearMaintenance(context.Background(), fixture.instances[0], "en", fixture.marker.TaskID, fakeLogger{})
		if maintenanceErrorCode(err) != MySQLMaintenanceStateInvalid {
			t.Fatalf("split-primary error=%v", err)
		}
		fixture.assertAllMarkersPresent(t)
	})

	t.Run("Router DML failure", func(t *testing.T) {
		fixture := newMaintenanceClusterFixture(t)
		remote := fixture.healthyRemote()
		remote.routerErr = errors.New("Router write rejected")
		err := NewModule(fixture.db, remote).ClearMaintenance(context.Background(), fixture.instances[0], "en", fixture.marker.TaskID, fakeLogger{})
		if maintenanceErrorCode(err) != MySQLMaintenanceStateInvalid {
			t.Fatalf("Router error=%v", err)
		}
		fixture.assertAllMarkersPresent(t)
	})

	t.Run("transaction update failure", func(t *testing.T) {
		fixture := newMaintenanceClusterFixture(t)
		rawMaintenanceExec(t, fixture.path, fmt.Sprintf(`create trigger fail_mysql_maintenance_clear before update on app_instances
when old.id='%s' and old.metadata like '%%"mysqlMaintenance"%%' and new.metadata not like '%%"mysqlMaintenance"%%'
begin select raise(abort,'injected maintenance clear failure'); end`, fixture.instances[1].ID))
		err := NewModule(fixture.db, fixture.healthyRemote()).ClearMaintenance(context.Background(), fixture.instances[0], "en", fixture.marker.TaskID, fakeLogger{})
		if maintenanceErrorCode(err) != MySQLMaintenanceStatePersistFailed {
			t.Fatalf("atomic clear failure error=%v", err)
		}
		fixture.assertAllMarkersPresent(t)
	})
}

func TestMySQLMaintenancePostLockServiceGateBlocksEveryOrdinaryLifecycleAction(t *testing.T) {
	// Production break caught: a handler-only check is vulnerable to state
	// appearing after task creation but before the worker acquires the mutate
	// lock. Every service entry point must reread authoritative Store state.
	for _, action := range []string{"check", "delete", "backup", "restore"} {
		t.Run(action, func(t *testing.T) {
			db := openMaintenanceTestStore(t)
			server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			stale, err := db.SaveAppInstance(store.AppInstance{
				App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{"port":3306}`,
			})
			if err != nil {
				t.Fatal(err)
			}
			marker := maintenanceTestMarker("standalone", "", "load_complete")
			if err := db.SetMySQLMaintenance([]string{stale.ID}, marker); err != nil {
				t.Fatal(err)
			}
			remote := &maintenancePingRemote{}
			module := NewModule(db, remote, "must-not-be-used")
			run := registry.RunContext{TaskID: "tsk_abcdefabcdefabcdefabcdef", Log: fakeLogger{}}
			switch action {
			case "check":
				_, err = module.Check(context.Background(), registry.CheckRequest{Instance: stale, Server: server, Language: "en"}, run)
			case "delete":
				err = module.Delete(context.Background(), registry.DeleteRequest{
					Instance: stale, Server: server, Language: "en",
					Parameters: map[string]any{registry.DeleteParamConfirmedWithServerPassword: true},
				}, run)
			case "backup":
				err = module.Backup(context.Background(), registry.BackupRequest{
					Instance: stale, Servers: []store.Server{server}, Language: "en",
					RepositoryDir: t.TempDir(), KeepLast: 1, Parameters: map[string]any{"threads": 4},
				}, run)
			case "restore":
				err = module.Restore(context.Background(), registry.RestoreRequest{
					Instance: stale, Servers: []store.Server{server}, Language: "en", RepositoryDir: t.TempDir(),
					Backup: store.AppBackup{
						ID: "backup_abcdefabcdefabcdefabcdef", App: "mysql", InstanceID: stale.ID, ServerID: server.ID,
						BackupType: "logical-full", Status: "success",
					},
					Parameters: map[string]any{
						"mode": "standalone", "maintenanceConfirmed": true,
						"createPreRestoreBackup": true, "disasterConfirmed": false, "threads": 4,
					},
				}, run)
			}
			if maintenanceErrorCode(err) != MySQLMaintenanceRequired {
				t.Fatalf("%s error=%T %v", action, err, err)
			}
			if len(remote.commands) != 0 {
				t.Fatalf("%s reached remote after authoritative reread: %v", action, remote.commands)
			}
			if _, getErr := db.GetAppInstance(stale.ID); getErr != nil {
				t.Fatalf("%s deleted or changed instance after gate: %v", action, getErr)
			}
		})
	}

	t.Run("cluster start", func(t *testing.T) {
		fixture := newMaintenanceClusterFixture(t)
		remote := fixture.healthyRemote()
		module := NewModule(fixture.db, remote, "must-not-be-used")
		err := module.StartCluster(context.Background(), registry.ClusterStartRequest{
			Instances: fixture.instances, Servers: fixture.orderedServers, Language: "en",
		}, registry.RunContext{TaskID: "tsk_abcdefabcdefabcdefabcdef", Log: fakeLogger{}})
		if maintenanceErrorCode(err) != MySQLMaintenanceRequired || len(remote.commands) != 0 {
			t.Fatalf("cluster start error=%v commands=%v", err, remote.commands)
		}
	})
}

func TestRestoreMaintenanceProductionStorePersistenceFailuresPreserveSafetyBoundary(t *testing.T) {
	// Production break caught: a fake-store-only path can miss SQLite
	// transaction failures and let schema mutation continue or report success
	// after the durable maintenance marker was not established/advanced/cleared.
	t.Run("initial marker set fails before first schema mutation", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		rawMaintenanceExec(t, fixture.path, `create trigger fail_mysql_maintenance_set before update on app_instances
when old.metadata not like '%"mysqlMaintenance"%' and new.metadata like '%"restorePhase":"schema_mutation_started"%'
begin select raise(abort,'injected maintenance set failure'); end`)
		err := fixture.run()
		if maintenanceErrorCode(err) != MySQLMaintenanceStatePersistFailed {
			t.Fatalf("initial marker failure error=%T %v", err, err)
		}
		commands := strings.Join(fixture.remote.commands, "\n")
		if strings.Contains(commands, "DROP DATABASE") || strings.Contains(commands, "logical-restore.sh") {
			t.Fatalf("schema mutation occurred before initial marker persistence:\n%s", commands)
		}
		current, getErr := fixture.db.GetAppInstance(fixture.instance.ID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if _, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata); parseErr != nil || present {
			t.Fatalf("failed initial set left marker: present=%v err=%v metadata=%s", present, parseErr, current.Metadata)
		}
	})

	t.Run("phase advance failure retains schema mutation marker", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		rawMaintenanceExec(t, fixture.path, `create trigger fail_mysql_maintenance_advance before update on app_instances
when old.metadata like '%"restorePhase":"schema_mutation_started"%' and new.metadata like '%"restorePhase":"load_complete"%'
begin select raise(abort,'injected maintenance advance failure'); end`)
		err := fixture.run()
		if maintenanceErrorCode(err) != MySQLMaintenanceStatePersistFailed {
			t.Fatalf("phase advance failure error=%T %v", err, err)
		}
		commands := strings.Join(fixture.remote.commands, "\n")
		if !strings.Contains(commands, "DROP DATABASE IF EXISTS `aifar_business`") || !strings.Contains(commands, "logical-restore.sh") {
			t.Fatalf("fixture did not reach schema mutation and logical load:\n%s", commands)
		}
		fixture.assertMarkerPhase(t, "schema_mutation_started")
	})

	t.Run("final clear failure retains load complete marker and returns failure", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		rawMaintenanceExec(t, fixture.path, `create trigger fail_mysql_maintenance_final_clear before update on app_instances
when old.metadata like '%"restorePhase":"load_complete"%' and new.metadata not like '%"mysqlMaintenance"%'
begin select raise(abort,'injected maintenance clear failure'); end`)
		err := fixture.run()
		if maintenanceErrorCode(err) != MySQLMaintenanceStatePersistFailed {
			t.Fatalf("final clear failure reported success or wrong error=%T %v", err, err)
		}
		fixture.assertMarkerPhase(t, "load_complete")
		if !strings.Contains(strings.Join(fixture.remote.commands, "\n"), "__AIFAR_VERIFY_FINAL__") {
			t.Fatal("final-clear failure fixture did not reach final verification")
		}
	})
}

func TestRestoreMaintenanceProductionStoreMarkerLifecycleAroundRemoteMutation(t *testing.T) {
	t.Run("preflight failure writes no marker", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		if err := os.WriteFile(fixture.backup.Path, []byte("tampered"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := fixture.run(); err == nil {
			t.Fatal("tampered preflight unexpectedly succeeded")
		}
		if len(fixture.remote.commands) != 0 {
			t.Fatalf("preflight failure reached remote: %v", fixture.remote.commands)
		}
		current, err := fixture.db.GetAppInstance(fixture.instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata); parseErr != nil || present {
			t.Fatalf("preflight failure wrote marker: present=%v err=%v metadata=%s", present, parseErr, current.Metadata)
		}
	})

	t.Run("marker is durable after phase write and before DROP", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		markerVisible := false
		fixture.remote.onDrop = func() {
			current, err := fixture.db.GetAppInstance(fixture.instance.ID)
			if err != nil {
				return
			}
			marker, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata)
			backup, backupErr := fixture.db.GetAppBackup(fixture.backup.ID)
			markerVisible = parseErr == nil && present && marker.RestorePhase == "schema_mutation_started" &&
				backupErr == nil && restorePhase(backup.Metadata) == "schema_mutation_started"
		}
		if err := fixture.run(); err != nil {
			t.Fatal(err)
		}
		if !markerVisible {
			t.Fatal("schema mutation started before durable backup phase and maintenance marker")
		}
	})

	t.Run("load failure retains schema mutation marker", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		fixture.remote.loadErr = errors.New("logical load failed")
		if err := fixture.run(); err == nil {
			t.Fatal("load failure reported success")
		}
		fixture.assertMarkerPhase(t, "schema_mutation_started")
	})

	t.Run("final verification failure retains load complete marker", func(t *testing.T) {
		fixture := newProductionMaintenanceRestoreFixture(t)
		fixture.remote.finalOutput = strings.Replace(finalRestoreVerificationLiteral(), "__AIFAR_VERIFY_PING__\t1\n", "", 1)
		if err := fixture.run(); maintenanceErrorCode(err) != MySQLRestoreIncomplete {
			t.Fatalf("final verification error=%T %v", err, err)
		}
		fixture.assertMarkerPhase(t, "load_complete")
	})
}

type maintenancePingRemote struct {
	commands          []string
	uploadedSecret    string
	uploadedLocalPath string
	uploadErr         error
	pingErr           error
	cleanupErr        error
	uploadCalls       int
	pingCalls         int
	cleanupRuns       int
}

func (r *maintenancePingRemote) Run(_ context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	if strings.Contains(command, "rm -rf") {
		r.cleanupRuns++
		return adapter.CommandResult{}, r.cleanupErr
	}
	if strings.Contains(command, "mysqladmin") && strings.Contains(command, "ping") {
		r.pingCalls++
		return adapter.CommandResult{Stdout: "__AIFAR_MYSQL_PING__\t1\n"}, r.pingErr
	}
	return adapter.CommandResult{}, nil
}

func (r *maintenancePingRemote) UploadFile(_ context.Context, _ store.Server, localPath, _ string, _ os.FileMode) error {
	r.uploadCalls++
	r.uploadedLocalPath = localPath
	contents, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	r.uploadedSecret = string(contents)
	return r.uploadErr
}

func openMaintenanceTestStore(t *testing.T) *store.Store {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func bindMaintenanceCredential(t *testing.T, db *store.Store, instanceID, username, password string) {
	t.Helper()
	credential, err := db.SaveCredential(store.Credential{
		Name: "mysql-admin-" + instanceID, Kind: "mysql", Username: username, Status: "active",
		Secret: map[string]string{"password": password},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindCredential(store.CredentialBinding{
		CredentialID: credential.ID, AppInstanceID: instanceID, Purpose: "admin",
	}); err != nil {
		t.Fatal(err)
	}
}

type maintenanceClusterFixture struct {
	db             *store.Store
	path           string
	clusterID      string
	instances      []store.AppInstance
	orderedServers []store.Server
	marker         store.MySQLMaintenanceMarker
}

func newMaintenanceClusterFixture(t *testing.T) maintenanceClusterFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aifar.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "maintenance", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	fixture := maintenanceClusterFixture{db: db, path: path, clusterID: clusterID}
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: fmt.Sprintf("mysql-%d", index), Host: fmt.Sprintf("10.0.0.%d", index+11), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{
			App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster",
			Metadata: `{"clusterId":"` + clusterID + `","port":3306}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{
			ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE",
		}); err != nil {
			t.Fatal(err)
		}
		bindMaintenanceCredential(t, db, instance.ID, "cluster_admin", "cluster-password")
		fixture.instances = append(fixture.instances, instance)
	}
	routerServer, err := db.SaveServer(store.Server{Name: "router", Host: "10.0.0.21", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql-router", Version: "8.0.36", ServerID: routerServer.ID, Status: "installed", Topology: "router",
		Metadata: `{"clusterId":"` + clusterID + `","readWritePort":6446}`,
	}); err != nil {
		t.Fatal(err)
	}
	members, err := db.ListAppClusterMembers(clusterID)
	if err != nil {
		t.Fatal(err)
	}
	byInstance := map[string]store.AppInstance{}
	for _, instance := range fixture.instances {
		byInstance[instance.ID] = instance
	}
	for _, member := range members {
		server, err := db.GetServer(byInstance[member.InstanceID].ServerID, false)
		if err != nil {
			t.Fatal(err)
		}
		fixture.orderedServers = append(fixture.orderedServers, server)
	}
	fixture.marker = maintenanceTestMarker("cluster", clusterID, "load_complete")
	ids := make([]string, 0, len(fixture.instances))
	for _, instance := range fixture.instances {
		ids = append(ids, instance.ID)
	}
	if err := db.SetMySQLMaintenance(ids, fixture.marker); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (f maintenanceClusterFixture) healthyRemote() *clusterRestoreRemote {
	return &clusterRestoreRemote{
		restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")},
		runtimes:          []string{healthyClusterRuntime(f.orderedServers)},
	}
}

func (f maintenanceClusterFixture) assertAllMarkersPresent(t *testing.T) {
	t.Helper()
	for _, instance := range f.instances {
		current, err := f.db.GetAppInstance(instance.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata); parseErr != nil || !present {
			t.Fatalf("marker missing after failed cluster clear for %s: present=%v err=%v metadata=%s", instance.ID, present, parseErr, current.Metadata)
		}
	}
}

func runtimeWithPrimaryRoles(servers []store.Server, roles []string) string {
	uuids := []string{
		"123e4567-e89b-12d3-a456-426614174000",
		"223e4567-e89b-12d3-a456-426614174000",
		"323e4567-e89b-12d3-a456-426614174000",
	}
	var output strings.Builder
	for index, server := range servers {
		output.WriteString("__AIFAR_CLUSTER__\t" + server.Host + "\t3306\t" + roles[index] + "\tONLINE\t" + uuids[index] + "\n")
	}
	return output.String()
}

func rawMaintenanceExec(t *testing.T, path, statement string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(statement); err != nil {
		t.Fatal(err)
	}
}

type productionMaintenanceRestoreFixture struct {
	db         *store.Store
	path       string
	instance   store.AppInstance
	backup     store.AppBackup
	request    registry.RestoreRequest
	service    Service
	remote     *productionMaintenanceRestoreRemote
	runContext registry.RunContext
}

type productionMaintenanceRestoreRemote struct {
	*restoreFakeRemote
	onDrop func()
}

func (r *productionMaintenanceRestoreRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if strings.Contains(command, "DROP DATABASE") && r.onDrop != nil {
		r.onDrop()
	}
	return r.restoreFakeRemote.Run(ctx, server, command)
}

func newProductionMaintenanceRestoreFixture(t *testing.T) *productionMaintenanceRestoreFixture {
	t.Helper()
	path := filepath.Join(t.TempDir(), "aifar.db")
	db, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone",
		Metadata: `{"port":3306,"rootUser":"metadata-root"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	bindMaintenanceCredential(t, db, instance.ID, "restore_admin", "restore-password")
	repositoryDir, backup := createStandaloneRestoreBackup(t, instance)
	backup, err = db.SaveAppBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	remote := &productionMaintenanceRestoreRemote{restoreFakeRemote: &restoreFakeRemote{inspect: standaloneInspection("aifar_business")}}
	service := NewService(db, remote)
	service.preRestoreBackup = func(context.Context, registry.BackupRequest, registry.RunContext) error { return nil }
	service.localInfileSession = func(context.Context, store.AppInstance, store.Server, store.Credential) (localInfileSession, func(), error) {
		return &fakeLocalInfileSession{value: "OFF"}, func() {}, nil
	}
	return &productionMaintenanceRestoreFixture{
		db: db, path: path, instance: instance, backup: backup,
		request: standaloneRestoreRequest(instance, backup, repositoryDir),
		service: service, remote: remote,
		runContext: registry.RunContext{TaskID: "tsk_abcdefabcdefabcdefabcdef", Log: fakeLogger{}},
	}
}

func (f *productionMaintenanceRestoreFixture) run() error {
	return f.service.restoreStandalone(context.Background(), f.request, f.runContext)
}

func (f *productionMaintenanceRestoreFixture) assertMarkerPhase(t *testing.T, want string) {
	t.Helper()
	current, err := f.db.GetAppInstance(f.instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	marker, present, parseErr := store.ParseMySQLMaintenanceMarker(current.Metadata)
	if parseErr != nil || !present || marker.RestorePhase != want {
		t.Fatalf("maintenance marker phase=%q want=%q present=%v err=%v metadata=%s", marker.RestorePhase, want, present, parseErr, current.Metadata)
	}
}

func maintenanceTestMarker(scope, clusterID, phase string) store.MySQLMaintenanceMarker {
	return store.MySQLMaintenanceMarker{
		Version: 1, State: "required", Reason: "restore_incomplete", Scope: scope,
		ClusterID: clusterID, BackupID: "backup_1234567890abcdef12345678",
		TaskID: "tsk_1234567890abcdef12345678", RestorePhase: phase,
		RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
}
