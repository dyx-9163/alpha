package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestMySQLMaintenanceClearHandlerOwnerOnlyCreatesRawLockedLocalizedAuditedTask(t *testing.T) {
	// Production break caught: changing the route/auth/body, using a member
	// lock, or omitting the durable task plan/audit makes the recovery
	// acknowledgement unsafe or unauditable.
	api, db, secret := newAuthzTestAPI(t)
	clusterID := "cluster_1234567890abcdef12345678"
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster",
		Metadata: `{"clusterId":"` + clusterID + `"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	module := newMaintenanceHandlerModule()
	api.apps = registry.New(module)

	operator := issueTestToken(t, db, secret, "operator", "operator")
	denied := maintenanceClearRequest(instance.ID, operator, "en", `{"recoveryConfirmed":true}`)
	deniedRec := httptest.NewRecorder()
	api.Router().ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}
	if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("non-owner created tasks=%+v err=%v", tasks, err)
	}

	owner := issueTestToken(t, db, secret, "owner", "owner")
	req := maintenanceClearRequest(instance.ID, owner, "zh-CN", `{"recoveryConfirmed":true}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("owner status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	select {
	case call := <-module.calls:
		if call.instance.ID != instance.ID || call.language != "zh-CN" || call.taskID != taskID {
			t.Fatalf("clear call=%+v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("clear worker did not call module")
	}
	task, _, err := db.GetTask(taskID)
	if err != nil || task.Type != "apps.mysql.maintenance.clear" || task.Target != clusterID || task.CreatedBy != "owner" {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	steps, err := db.ListTaskSteps(taskID)
	if err != nil || len(steps) != 1 || steps[0].Target != clusterID || steps[0].Name != "clear-maintenance" || steps[0].Title != "清除 MySQL 维护状态" {
		t.Fatalf("steps=%+v err=%v", steps, err)
	}
	targets, err := db.ListTaskTargets(taskID)
	if err != nil || len(targets) != 1 || targets[0].Target != clusterID {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	locks, err := db.ListOperationLocks("app-cluster", clusterID, false)
	if err != nil || len(locks) != 1 || locks[0].Operation != operationLockMutation || locks[0].OwnerTaskID != taskID {
		t.Fatalf("locks=%+v err=%v", locks, err)
	}
	assertAuditExists(t, db, "apps.mysql.maintenance.clear", "running", "owner", instance.ID)
	close(module.release)
	waitForTaskStatus(t, db, taskID, "success")
	steps, _ = db.ListTaskSteps(taskID)
	targets, _ = db.ListTaskTargets(taskID)
	if steps[0].Status != "success" || targets[0].Status != "success" {
		t.Fatalf("terminal success plan steps=%+v targets=%+v", steps, targets)
	}
}

func TestMySQLMaintenanceClearHandlerTerminalizesFailure(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	module := newMaintenanceHandlerModule()
	module.err = errors.New("controlled maintenance health rejection")
	close(module.release)
	api.apps = registry.New(module)
	req := maintenanceClearRequest(instance.ID, issueTestToken(t, db, secret, "owner", "owner"), "en", `{"recoveryConfirmed":true}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	waitForTaskStatus(t, db, taskID, "failed")
	steps, _ := db.ListTaskSteps(taskID)
	targets, _ := db.ListTaskTargets(taskID)
	if len(steps) != 1 || steps[0].Status != "failed" || len(targets) != 1 || targets[0].Status != "failed" {
		t.Fatalf("terminal failure steps=%+v targets=%+v", steps, targets)
	}
	assertAuditExists(t, db, "apps.mysql.maintenance.clear", "running", "owner", instance.ID)
}

func TestMySQLMaintenanceClearHandlerUsesStandaloneInstanceMutationLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	module := newMaintenanceHandlerModule()
	api.apps = registry.New(module)
	req := maintenanceClearRequest(instance.ID, issueTestToken(t, db, secret, "owner", "owner"), "en", `{"recoveryConfirmed":true}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	select {
	case <-module.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("clear worker did not call module")
	}
	locks, err := db.ListOperationLocks("app-instance", instance.ID, false)
	if err != nil || len(locks) != 1 || locks[0].Operation != operationLockMutation || locks[0].OwnerTaskID != taskID {
		t.Fatalf("standalone locks=%+v err=%v", locks, err)
	}
	close(module.release)
	waitForTaskStatus(t, db, taskID, "success")
}

func TestMySQLMaintenanceClearHandlerRejectsMissingConfirmationBeforeTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	module := newMaintenanceHandlerModule()
	api.apps = registry.New(module)
	req := maintenanceClearRequest(instance.ID, issueTestToken(t, db, secret, "owner", "owner"), "en", `{}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 || len(module.calls) != 0 {
		t.Fatalf("missing confirmation created work: tasks=%+v calls=%d err=%v", tasks, len(module.calls), err)
	}
}

func TestMySQLMaintenanceClearHandlerRejectsMalformedRawClusterIDBeforeTask(t *testing.T) {
	// Production break caught: a merely non-empty clusterId can create a lock
	// outside the controlled raw cluster namespace.
	api, db, secret := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster",
		Metadata: `{"clusterId":"../../uncontrolled"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	module := newMaintenanceHandlerModule()
	api.apps = registry.New(module)
	req := maintenanceClearRequest(instance.ID, issueTestToken(t, db, secret, "owner", "owner"), "en", `{"recoveryConfirmed":true}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_MAINTENANCE_STATE_INVALID"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 || len(module.calls) != 0 {
		t.Fatalf("malformed cluster ID created work: tasks=%+v calls=%d err=%v", tasks, len(module.calls), err)
	}
}

func TestMySQLMaintenanceHandlerGateBlocksEveryOrdinaryStandaloneActionBeforeTask(t *testing.T) {
	// Production break caught: moving the guard into the worker alone allows
	// forbidden lifecycle tasks/audits to be created while recovery is active.
	for _, action := range []string{"check", "delete", "batch-delete", "backup", "restore"} {
		t.Run(action, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			server, instance := saveMySQLBackupTarget(t, db, "standalone", "")
			marker := store.MySQLMaintenanceMarker{
				Version: 1, State: "required", Reason: "restore_incomplete", Scope: "standalone",
				BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678",
				RestorePhase: "load_complete", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
			}
			if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
				t.Fatal(err)
			}
			owner := issueTestToken(t, db, secret, "owner", "owner")
			var req *http.Request
			switch action {
			case "check":
				module := &fakePlannedLifecycleModule{name: "mysql"}
				api.apps = registry.New(module)
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/check", nil)
			case "delete":
				module := &fakePlannedLifecycleModule{name: "mysql"}
				api.apps = registry.New(module)
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/delete", strings.NewReader(`{"serverPassword":"unused"}`))
			case "batch-delete":
				module := &fakePlannedLifecycleModule{name: "mysql"}
				api.apps = registry.New(module)
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/batch-delete", strings.NewReader(`{"instanceIds":["`+instance.ID+`"],"serverPasswords":{"`+server.ID+`":"unused"}}`))
			case "backup":
				api.apps = registry.New(newBackupHandlerModule())
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{"schemas":["orders"]}`))
			case "restore":
				api.apps = registry.New(newBackupHandlerModule())
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/restore", strings.NewReader(`{"backupId":"backup_1234567890abcdef12345678","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"threads":4}`))
			}
			req.Header.Set("Authorization", "Bearer "+owner)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-AIFAR-Language", "en")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_MAINTENANCE_REQUIRED"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
				t.Fatalf("blocked %s created tasks=%+v err=%v", action, tasks, err)
			}
		})
	}
}

func TestMySQLMaintenanceHandlerAllowsSameBackupStandaloneResume(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	server, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	backup, err := db.SaveAppBackup(store.AppBackup{
		App: "mysql", InstanceID: instance.ID, ServerID: server.ID, BackupType: "logical-full", Status: "success",
		Path: filepathForTestBackup("maintenance-resume"), Checksum: strings.Repeat("a", 64), Size: 10, Metadata: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	marker := store.MySQLMaintenanceMarker{
		Version: 1, State: "required", Reason: "restore_incomplete", Scope: "standalone",
		BackupID: backup.ID, TaskID: "tsk_1234567890abcdef12345678",
		RestorePhase: "schema_mutation_started", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
	if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
		t.Fatal(err)
	}
	module := newBackupHandlerModule()
	api.apps = registry.New(module)
	body := `{"backupId":"` + backup.ID + `","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"resumeMaintenance":true,"threads":4}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/restore", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("resume restore status=%d body=%s", rec.Code, rec.Body.String())
	}
	select {
	case call := <-module.restoreCalls:
		if call.request.Parameters["resumeMaintenance"] != true || call.request.Backup.ID != backup.ID {
			t.Fatalf("resume restore request=%+v", call.request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("resume restore worker did not call module")
	}
	close(module.restoreRelease)
}

func TestMySQLMaintenanceHandlerGateBlocksClusterStartBeforeTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "maintenance", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	var instances []store.AppInstance
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: "mysql-" + string(rune('1'+index)), Host: "10.0.0." + string(rune('1'+index)), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{
			App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster",
			Metadata: `{"clusterId":"` + clusterID + `"}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	marker := store.MySQLMaintenanceMarker{
		Version: 1, State: "required", Reason: "restore_incomplete", Scope: "cluster", ClusterID: clusterID,
		BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678",
		RestorePhase: "load_complete", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
	ids := []string{instances[0].ID, instances[1].ID, instances[2].ID}
	if err := db.SetMySQLMaintenance(ids, marker); err != nil {
		t.Fatal(err)
	}
	module := &fakePlannedLifecycleModule{name: "mysql"}
	api.apps = registry.New(module)
	body, _ := json.Marshal(map[string]any{"instanceIds": ids})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/database/mysql/clusters/start", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_MAINTENANCE_REQUIRED"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 || module.clusterStartCalls != 0 {
		t.Fatalf("blocked cluster start created work: tasks=%+v starts=%d err=%v", tasks, module.clusterStartCalls, err)
	}
}

func TestMySQLMaintenanceHandlerGateFailsClosedForMalformedMarker(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone",
		Metadata: `{"mysqlMaintenance":{"version":2}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/check", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_MAINTENANCE_STATE_INVALID"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
		t.Fatalf("malformed marker created tasks=%+v err=%v", tasks, err)
	}
}

func maintenanceClearRequest(instanceID, token, language, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instanceID+"/mysql/maintenance/clear", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIFAR-Language", language)
	return req
}

type maintenanceHandlerCall struct {
	instance store.AppInstance
	language string
	taskID   string
}

type maintenanceHandlerModule struct {
	*backupHandlerModule
	calls   chan maintenanceHandlerCall
	release chan struct{}
	err     error
}

func newMaintenanceHandlerModule() *maintenanceHandlerModule {
	return &maintenanceHandlerModule{
		backupHandlerModule: newBackupHandlerModule(),
		calls:               make(chan maintenanceHandlerCall, 1),
		release:             make(chan struct{}),
	}
}

func (m *maintenanceHandlerModule) ClearMaintenance(ctx context.Context, instance store.AppInstance, language, taskID string, _ mysqlapp.Logger) error {
	m.calls <- maintenanceHandlerCall{instance: instance, language: language, taskID: taskID}
	select {
	case <-m.release:
		return m.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
