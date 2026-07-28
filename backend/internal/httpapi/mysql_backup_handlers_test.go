package httpapi

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/store"
)

func TestMySQLBackupHandlerCreatesPlannedLockedAuditedSSETaskWithServerOwnedSettings(t *testing.T) {
	// Production break caught: bypassing task planning, the mutate lock, server-owned repository settings, or audit/SSE integration makes backup unobservable or user-controlled.
	api, db, secret := newAuthzTestAPI(t)
	server, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	module := newBackupHandlerModule()
	api.apps = registry.New(module)
	token := issueTestToken(t, db, secret, "operator", "operator")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{"name":"nightly","threads":12,"maxRateMBps":96}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST backup status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.TaskID == "" {
		t.Fatalf("task response=%s err=%v", rec.Body.String(), err)
	}

	var call backupHandlerCall
	select {
	case call = <-module.calls:
	case <-time.After(2 * time.Second):
		t.Fatal("backup worker did not call module")
	}
	if call.request.RepositoryDir != api.cfg.MySQLBackupDir || call.request.KeepLast != api.cfg.MySQLBackupKeepLast {
		t.Fatalf("repository/default retention = %q/%d, want %q/%d", call.request.RepositoryDir, call.request.KeepLast, api.cfg.MySQLBackupDir, api.cfg.MySQLBackupKeepLast)
	}
	if call.request.Instance.ID != instance.ID || len(call.request.Servers) != 1 || call.request.Servers[0].ID != server.ID {
		t.Fatalf("resolved target request = %+v", call.request)
	}
	if call.run.TaskID != response.TaskID || call.run.Log == nil || call.run.TargetLog == nil || call.run.Concurrency != api.cfg.DeploymentConcurrency {
		t.Fatalf("run context = %+v", call.run)
	}
	if call.request.Parameters["name"] != "nightly" || call.request.Parameters["threads"] != 12 || call.request.Parameters["maxRateMBps"] != 96 {
		t.Fatalf("structured parameters = %+v", call.request.Parameters)
	}
	task, _, err := db.GetTask(response.TaskID)
	if err != nil || task.Type != "apps.mysql.backup" || task.Target != instance.ID {
		t.Fatalf("persisted task=%+v err=%v", task, err)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotSteps := make([]string, len(steps))
	for index := range steps {
		gotSteps[index] = steps[index].Name
	}
	if !reflect.DeepEqual(gotSteps, mysqlBackupHandlerSteps) {
		t.Fatalf("persisted steps=%v", gotSteps)
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Target != server.ID {
		t.Fatalf("persisted targets=%+v", targets)
	}
	locks, err := db.ListOperationLocks("app-instance", instance.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].Operation != operationLockMutation || locks[0].OwnerTaskID != task.ID {
		t.Fatalf("backup lock=%+v", locks)
	}
	assertAuditExists(t, db, "apps.mysql.backup", "running", "operator", instance.ID)

	sseCtx, cancel := context.WithCancel(context.Background())
	sseReq := httptest.NewRequest(http.MethodGet, "/api/v2/tasks/"+task.ID+"/events", nil).WithContext(sseCtx)
	sseReq.Header.Set("Authorization", "Bearer "+token)
	sseRec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { api.Router().ServeHTTP(sseRec, sseReq); close(done) }()
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("task SSE did not stop on cancellation")
	}
	if !strings.HasPrefix(sseRec.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(sseRec.Body.String(), "event: task-event") {
		t.Fatalf("task SSE incompatible: headers=%v body=%s", sseRec.Header(), sseRec.Body.String())
	}
	close(module.release)
	waitForTaskStatus(t, db, task.ID, "success")
}

func TestMySQLRestoreHandlerIsOwnerOnlyAndCreatesExactPlannedLockedAuditedTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	server, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	backup, err := db.SaveAppBackup(store.AppBackup{
		App: "mysql", InstanceID: instance.ID, ServerID: server.ID, BackupType: "logical-full", Status: "success",
		Path: filepathForTestBackup("restore"), Checksum: strings.Repeat("a", 64), Size: 10, Metadata: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	module := newBackupHandlerModule()
	api.apps = registry.New(module)
	body := `{"backupId":"` + backup.ID + `","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"threads":4}`

	operator := issueTestToken(t, db, secret, "operator", "operator")
	denied := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/restore", strings.NewReader(body))
	denied.Header.Set("Authorization", "Bearer "+operator)
	denied.Header.Set("Content-Type", "application/json")
	deniedRec := httptest.NewRecorder()
	api.Router().ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("operator restore status=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}

	owner := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/restore", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("owner restore status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.TaskID == "" {
		t.Fatalf("task response=%s err=%v", rec.Body.String(), err)
	}
	var call restoreHandlerCall
	select {
	case call = <-module.restoreCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("restore worker did not call module")
	}
	if call.request.RepositoryDir != api.cfg.MySQLBackupDir || call.request.Instance.ID != instance.ID || call.request.Backup.ID != backup.ID || call.request.Actor != "owner" {
		t.Fatalf("restore request = %+v", call.request)
	}
	if call.run.TaskID != response.TaskID {
		t.Fatalf("restore run = %+v", call.run)
	}
	steps, err := db.ListTaskSteps(response.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(steps))
	for i := range steps {
		got[i] = steps[i].Name
	}
	if !reflect.DeepEqual(got, mysqlRestoreHandlerSteps) {
		t.Fatalf("restore steps=%v", got)
	}
	locks, err := db.ListOperationLocks("app-instance", instance.ID, false)
	if err != nil || len(locks) != 1 || locks[0].Operation != operationLockMutation || locks[0].OwnerTaskID != response.TaskID {
		t.Fatalf("restore lock=%+v err=%v", locks, err)
	}
	assertAuditExists(t, db, "apps.mysql.restore", "running", "owner", instance.ID)
	close(module.restoreRelease)
	waitForTaskStatus(t, db, response.TaskID, "success")
}

func TestMySQLRestoreHandlerRejectsUnsafeRequestsBeforeTaskOrMutation(t *testing.T) {
	tests := []struct {
		name     string
		body     string
		topology string
		planErr  error
	}{
		{"maintenance missing", `{"backupId":"BACKUP","mode":"standalone","createPreRestoreBackup":true,"threads":4}`, "standalone", nil},
		{"topology mismatch", `{"backupId":"BACKUP","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"threads":4}`, "innodb-cluster", nil},
		{"failed verification", `{"backupId":"BACKUP","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"threads":4}`, "standalone", &mysqlapp.MySQLOperationError{Code: mysqlapp.MySQLBackupVerifyFailed}},
		{"unknown field", `{"backupId":"BACKUP","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"threads":4,"path":"/tmp/free"}`, "standalone", nil},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			server, instance := saveMySQLBackupTarget(t, db, test.topology, "")
			backup, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: instance.ID, ServerID: server.ID, BackupType: "logical-full", Status: "success", Path: filepathForTestBackup("unsafe"), Checksum: strings.Repeat("b", 64), Size: 10, Metadata: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			module := newBackupHandlerModule()
			module.restorePlanErr = test.planErr
			api.apps = registry.New(module)
			body := strings.ReplaceAll(test.body, "BACKUP", backup.ID)
			owner := issueTestToken(t, db, secret, "owner", "owner")
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/restore", strings.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+owner)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code == http.StatusAccepted {
				t.Fatalf("unsafe restore accepted: %s", rec.Body.String())
			}
			tasks, err := db.ListTasks()
			if err != nil {
				t.Fatal(err)
			}
			if len(tasks) != 0 {
				t.Fatalf("unsafe restore created tasks: %+v", tasks)
			}
			if len(module.restoreCalls) != 0 {
				t.Fatal("unsafe restore reached module mutation")
			}
		})
	}
}

func TestDisasterRebuildHandlerRequiresOwnerConfirmationsMappingAndEveryServerPassword(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	cluster, err := db.SaveAppCluster(store.AppCluster{App: "mysql", Name: "disaster-cluster", Topology: "innodb-cluster", Status: "maintenance", Metadata: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	servers := make([]store.Server, 0, 3)
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, saveErr := db.SaveServer(store.Server{Name: fmt.Sprintf("mysql-%d", index+1), Host: fmt.Sprintf("10.0.0.%d", index+11), Username: "root", Password: fmt.Sprintf("ssh-pass-%d", index+1)})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		instance, saveErr := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "failed", Topology: "innodb-cluster", Metadata: fmt.Sprintf(`{"clusterId":%q,"port":3306,"endpoint":%q}`, cluster.ID, server.Host+":3306")})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		if _, saveErr = db.SaveAppClusterMember(store.AppClusterMember{ClusterID: cluster.ID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "failed", Metadata: `{}`}); saveErr != nil {
			t.Fatal(saveErr)
		}
		servers = append(servers, server)
		instances = append(instances, instance)
	}
	backup, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: instances[0].ID, ServerID: servers[0].ID, BackupType: "logical-full", Status: "success", Path: filepathForTestBackup("disaster"), Checksum: strings.Repeat("d", 64), Size: 10, Metadata: fmt.Sprintf(`{"clusterId":%q}`, cluster.ID)})
	if err != nil {
		t.Fatal(err)
	}
	marker := store.MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: "cluster", ClusterID: cluster.ID, BackupID: backup.ID, TaskID: store.NewID("tsk"), RestorePhase: "load_complete", RecordedAt: time.Now().UTC()}
	ids := []string{instances[0].ID, instances[1].ID, instances[2].ID}
	if err := db.SetMySQLMaintenance(ids, marker); err != nil {
		t.Fatal(err)
	}

	targetMapping := map[string]string{}
	passwords := map[string]string{}
	for index := range instances {
		targetMapping[instances[index].ID] = servers[index].ID
		passwords[servers[index].ID] = fmt.Sprintf("ssh-pass-%d", index+1)
	}
	body := map[string]any{"backupId": backup.ID, "mode": "disaster-rebuild", "maintenanceConfirmed": true, "disasterConfirmed": true, "threads": 4, "targetMapping": targetMapping, "serverPasswords": passwords}
	encoded, _ := json.Marshal(body)
	module := newBackupHandlerModule()
	api.apps = registry.New(module)
	owner := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[1].ID+"/restore", strings.NewReader(string(encoded)))
	req.Header.Set("Authorization", "Bearer "+owner)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("disaster rebuild status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil || response.TaskID == "" {
		t.Fatalf("task response=%s err=%v", rec.Body.String(), err)
	}
	var call restoreHandlerCall
	select {
	case call = <-module.restoreCalls:
	case <-time.After(2 * time.Second):
		t.Fatal("disaster worker did not call module")
	}
	if call.request.Parameters["mode"] != "disaster-rebuild" || call.request.Parameters["serverPasswordsConfirmed"] != true || !reflect.DeepEqual(call.request.Parameters["targetMapping"], mapStringAny(targetMapping)) {
		t.Fatalf("controlled disaster parameters=%+v", call.request.Parameters)
	}
	parameterJSON, _ := json.Marshal(call.request.Parameters)
	if strings.Contains(string(parameterJSON), "ssh-pass-") {
		t.Fatalf("SSH confirmations leaked into worker parameters: %s", parameterJSON)
	}
	locks, err := db.ListOperationLocks("app-cluster", cluster.ID, false)
	if err != nil || len(locks) != 1 || locks[0].Operation != operationLockMutation || locks[0].OwnerTaskID != response.TaskID {
		t.Fatalf("disaster cluster lock=%+v err=%v", locks, err)
	}
	close(module.restoreRelease)
	waitForTaskStatus(t, db, response.TaskID, "success")

	for _, test := range []struct {
		name     string
		wantCode string
		mutate   func(map[string]any)
	}{
		{"maintenance confirmation", mysqlapp.MySQLRebuildConfirmationRequired, func(value map[string]any) { value["maintenanceConfirmed"] = false }},
		{"disaster confirmation", mysqlapp.MySQLRebuildConfirmationRequired, func(value map[string]any) { value["disasterConfirmed"] = false }},
		{"exact mapping", mysqlapp.MySQLRebuildConfirmationRequired, func(value map[string]any) { delete(value["targetMapping"].(map[string]string), instances[2].ID) }},
		{"all SSH passwords", mysqlapp.MySQLRebuildConfirmationRequired, func(value map[string]any) { delete(value["serverPasswords"].(map[string]string), servers[2].ID) }},
		{"matching SSH passwords", "SERVER_PASSWORD_INVALID", func(value map[string]any) { value["serverPasswords"].(map[string]string)[servers[2].ID] = "wrong" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			candidate := map[string]any{"backupId": backup.ID, "mode": "disaster-rebuild", "maintenanceConfirmed": true, "disasterConfirmed": true, "threads": 4, "targetMapping": cloneStringMap(targetMapping), "serverPasswords": cloneStringMap(passwords)}
			test.mutate(candidate)
			raw, _ := json.Marshal(candidate)
			request := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/restore", strings.NewReader(string(raw)))
			request.Header.Set("Authorization", "Bearer "+owner)
			request.Header.Set("Content-Type", "application/json")
			responseRecorder := httptest.NewRecorder()
			api.Router().ServeHTTP(responseRecorder, request)
			if responseRecorder.Code == http.StatusAccepted {
				t.Fatalf("unsafe disaster request accepted: %s", responseRecorder.Body.String())
			}
			var errorBody struct {
				Code string `json:"code"`
			}
			if json.Unmarshal(responseRecorder.Body.Bytes(), &errorBody) != nil || errorBody.Code != test.wantCode {
				t.Fatalf("unsafe disaster code=%q want=%q body=%s", errorBody.Code, test.wantCode, responseRecorder.Body.String())
			}
		})
	}
}

func cloneStringMap(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func mapStringAny(input map[string]string) map[string]any {
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func TestMySQLBackupHandlerValidatesInstanceAppAndSupportedTopology(t *testing.T) {
	// Production break caught: dispatching a MySQL backup task for a foreign app
	// or unsupported topology would select the wrong safety model.
	tests := []struct{ name, app, topology string }{{"foreign app", "redis", "standalone"}, {"unsupported topology", "mysql", "replica"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			server, err := db.SaveServer(store.Server{Name: "db", Host: "10.0.0.8", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := db.SaveAppInstance(store.AppInstance{App: test.app, Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: test.topology})
			if err != nil {
				t.Fatal(err)
			}
			api.apps = registry.New(newBackupHandlerModule())
			token := issueTestToken(t, db, secret, "operator", "operator")
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMySQLBackupHandlerReturnsConflictForExistingMutationLock(t *testing.T) {
	// Production break caught: action-specific locks would allow backup to overlap check/delete/start on the same instance.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	api.apps = registry.New(newBackupHandlerModule())
	ownerTask, err := db.CreateTask(store.Task{Type: "apps.mysql.check", Target: instance.ID, Status: "running", CreatedBy: "operator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireOperationLock(store.OperationLock{Scope: "app-instance", ResourceID: instance.ID, Operation: operationLockMutation, OwnerTaskID: ownerTask.ID, Owner: "operator", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"OPERATION_LOCKED"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Production break caught: using action-specific instance locks would permit a
// backup and restore for different members of one cluster to overlap.
func TestMySQLClusterBackupAndRestoreUseOneMutateLock(t *testing.T) {
	instance := store.AppInstance{ID: "app_1234567890abcdef12345678", App: "mysql", ServerID: "srv_1234567890abcdef12345678", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`}
	backup := mysqlBackupOperationLockSpecs(instance)
	restore := mysqlClusterOperationLockSpecs("mysql-restore", instance)
	check := mysqlClusterOperationLockSpecs("mysql-check", instance)
	delete := appMutationOperationLockSpecs("delete", []store.AppInstance{instance})
	start := appMutationOperationLockSpecs("mysql-cluster-start", []store.AppInstance{instance, instance})
	for action, specs := range map[string][]operationLockSpec{"backup": backup, "restore": restore, "check": check, "delete": delete, "start": start} {
		if len(specs) != 1 || specs[0].Scope != "app-cluster" || specs[0].ResourceID != "cluster_1234567890abcdef12345678" || specs[0].Operation != operationLockMutation {
			t.Fatalf("%s cluster lock specs = %+v", action, specs)
		}
	}
}

// Production break caught: treating the UI grouping key (id:<clusterId>) as a
// store key would make valid cluster backup/restore requests fail before task
// planning or lock the wrong resource.
func TestMySQLClusterBackupTargetsUseRawStoredClusterID(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "production", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	var instances []store.AppInstance
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: fmt.Sprintf("mysql-%d", index), Host: fmt.Sprintf("10.0.0.%d", index+11), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `","port":3306}`})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	targets, servers, err := api.mysqlBackupTargets(instances[1])
	if err != nil || len(targets) != 3 || len(servers) != 3 || mysqlClusterID(targets[0]) != clusterID {
		t.Fatalf("cluster targets=%+v servers=%+v err=%v", targets, servers, err)
	}
}

func TestMySQLBackupHandlerPreservesAndLocalizesStablePlanError(t *testing.T) {
	// Production break caught: wrapping a stable module error in a generic handler code/raw message breaks clients and can expose sensitive internals.
	tests := []struct {
		language string
		message  string
	}{
		{"en", "insufficient space for MySQL backup"},
		{"zh-CN", "MySQL 备份空间不足"},
	}
	for _, test := range tests {
		t.Run(test.language, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
			module := newBackupHandlerModule()
			module.planErr = &mysqlapp.MySQLOperationError{Code: mysqlapp.MySQLBackupSpaceInsufficient}
			api.apps = registry.New(module)
			token := issueTestToken(t, db, secret, "operator", "operator")
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-AIFAR-Language", test.language)
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"MYSQL_BACKUP_SPACE_INSUFFICIENT"`) || !strings.Contains(rec.Body.String(), test.message) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

func TestMySQLBackupHandlerDoesNotExposeGenericPlanErrorDetails(t *testing.T) {
	// Production break caught: generic planning failures must use catalog text rather than reflecting arbitrary error strings to API users.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	module := newBackupHandlerModule()
	module.planErr = errors.New("raw internal detail top-secret")
	api.apps = registry.New(module)
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIFAR-Language", "en")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"MYSQL_BACKUP_PLAN_FAILED"`) || !strings.Contains(rec.Body.String(), "Unable to plan MySQL backup") || strings.Contains(rec.Body.String(), "top-secret") {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMySQLBackupListExcludesDeletedAndExpandsClusterMembersOnce(t *testing.T) {
	// Production break caught: listing only the clicked cluster member or including deleted rows would hide/duplicate cluster-level history.
	api, db, secret := newAuthzTestAPI(t)
	_, standalone := saveMySQLBackupTarget(t, db, "standalone", "")
	standaloneBackup, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: standalone.ID, ServerID: standalone.ServerID, BackupType: "logical-full", Status: "success", Path: filepathForTestBackup("one"), Checksum: strings.Repeat("a", 64), Size: 10, Metadata: `{"manifestVersion":2,"topology":"standalone","mysqlVersion":"8.0.36","mysqlShellVersion":"8.0.36","schemas":["aifar"],"phase":"success"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: standalone.ID, ServerID: standalone.ServerID, BackupType: "logical-full", Status: "deleted", Path: filepathForTestBackup("deleted"), Checksum: strings.Repeat("b", 64), Size: 10}); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "viewer", "viewer")
	items := getBackupListItems(t, api, token, standalone.ID)
	if len(items) != 1 || items[0].ID != standaloneBackup.ID {
		t.Fatalf("standalone items=%+v", items)
	}
	if items[0].Metadata != standaloneBackup.Metadata {
		t.Fatalf("list dropped controlled manifest metadata: got=%s want=%s", items[0].Metadata, standaloneBackup.Metadata)
	}

	clusterID := "mysql_cluster_1234567890abcdef12345678"
	var members []store.AppInstance
	for index := 0; index < 3; index++ {
		_, member := saveMySQLBackupTarget(t, db, "innodb-cluster", clusterID)
		members = append(members, member)
	}
	clusterBackup, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: members[0].ID, ServerID: members[0].ServerID, BackupType: "logical-full", Status: "success", Path: filepathForTestBackup("cluster"), Checksum: strings.Repeat("c", 64), Size: 20, Metadata: `{"manifestVersion":2,"topology":"innodb-cluster","clusterId":"` + clusterID + `","mysqlVersion":"8.0.36","mysqlShellVersion":"8.0.36","schemas":["aifar"],"phase":"success"}`})
	if err != nil {
		t.Fatal(err)
	}
	clusterItems := getBackupListItems(t, api, token, members[1].ID)
	if len(clusterItems) != 1 || clusterItems[0].ID != clusterBackup.ID {
		t.Fatalf("cluster items=%+v", clusterItems)
	}
	if clusterItems[0].Metadata != clusterBackup.Metadata {
		t.Fatalf("cluster list dropped controlled manifest metadata: got=%s want=%s", clusterItems[0].Metadata, clusterBackup.Metadata)
	}
}

func TestMySQLBackupVerifyCreatesLockedAuditedWorkerWithExactSteps(t *testing.T) {
	// Production break caught: verification without its worker plan, shared lifecycle lock, permission, or audit trail is neither observable nor serialized.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	backup, _ := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "verify")
	operator := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/backups/"+backup.ID+"/verify", nil)
	req.Header.Set("Authorization", "Bearer "+operator)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("POST verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.TaskID == "" {
		t.Fatalf("verify response=%s err=%v", rec.Body.String(), err)
	}
	task, _, err := db.GetTask(body.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type != "apps.mysql.backup.verify" || task.Target != backup.ID {
		t.Fatalf("verify task=%+v", task)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(steps))
	for i := range steps {
		got[i] = steps[i].Name
	}
	if !reflect.DeepEqual(got, []string{"load-backup", "verify-manifest", "verify-checksum", "record-verification"}) {
		t.Fatalf("verify steps=%v", got)
	}
	for _, persisted := range steps {
		if persisted.Target != backup.ID {
			t.Fatalf("verify step target=%q, want backup id %q", persisted.Target, backup.ID)
		}
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Target != backup.ID {
		t.Fatalf("verify targets=%+v, want one backup target", targets)
	}
	locks, err := db.ListOperationLocks("app-instance", instance.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].Operation != operationLockMutation || locks[0].OwnerTaskID != task.ID {
		t.Fatalf("verify lock=%+v", locks)
	}
	assertAuditExists(t, db, "apps.mysql.backup.verify", "running", "operator", backup.ID)
	waitForTaskStatus(t, db, task.ID, "success")
	steps, err = db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, persisted := range steps {
		if persisted.Status != "success" || persisted.FinishedAt.IsZero() {
			t.Fatalf("verify step is not terminal: %+v", persisted)
		}
	}
	targets, err = db.ListTaskTargets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Status != "success" || targets[0].FinishedAt.IsZero() {
		t.Fatalf("verify target is not terminal: %+v", targets)
	}
	verified, err := db.GetAppBackup(backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Path != backup.Path || verified.Checksum != backup.Checksum || verified.Size != backup.Size || !strings.Contains(verified.Metadata, `"verificationResult":"success"`) {
		t.Fatalf("verified record=%+v", verified)
	}

	viewer := issueTestToken(t, db, secret, "viewer", "viewer")
	deniedReq := httptest.NewRequest(http.MethodPost, "/api/v2/apps/backups/"+backup.ID+"/verify", nil)
	deniedReq.Header.Set("Authorization", "Bearer "+viewer)
	deniedRec := httptest.NewRecorder()
	api.Router().ServeHTTP(deniedRec, deniedReq)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("viewer verify status=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}
}

func TestMySQLBackupDeleteRejectsClusterRecordsBeforeTakingInstanceLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "innodb-cluster", "mysql_cluster_1234567890abcdef12345678")
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	backup, _ := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "cluster")
	_, _ = saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "cluster-survivor")
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+backup.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "MYSQL_BACKUP_STANDALONE_REQUIRED") {
		t.Fatalf("cluster backup delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	locks, err := db.ListOperationLocks("app-instance", instance.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("delete acquired wrong instance lock: %+v", locks)
	}
}

func TestMySQLClusterBackupVerifyUsesAuthoritativeClusterLockAndPublishesEligibility(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	clusterID := "cluster_verify_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "verify-cluster", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: fmt.Sprintf("mysql-%d", index+1), Host: fmt.Sprintf("10.0.0.%d", index+21), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `","port":3306}`})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "active"}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	backup, _ := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instances[0], "cluster-verify")
	backup.Metadata = `{"phase":"success","topology":"innodb-cluster","clusterId":"` + clusterID + `","manifestVersion":2}`
	if _, err := db.SaveAppBackup(backup); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/backups/"+backup.ID+"/verify", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("cluster verify status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || body.TaskID == "" {
		t.Fatalf("cluster verify response=%s err=%v", rec.Body.String(), err)
	}
	locks, err := db.ListOperationLocks("app-cluster", clusterID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].OwnerTaskID != body.TaskID || locks[0].Operation != operationLockMutation {
		t.Fatalf("cluster verify lock=%+v", locks)
	}
	waitForTaskStatus(t, db, body.TaskID, "success")
	verified, err := db.GetAppBackup(backup.ID)
	if err != nil || !strings.Contains(verified.Metadata, `"verificationResult":"success"`) {
		t.Fatalf("cluster verification did not publish eligibility: backup=%+v err=%v", verified, err)
	}
}

func TestMySQLBackupDeleteRemovesFilesBeforeMarkingDeletedAndAudits(t *testing.T) {
	// Production break caught: marking first or deleting an unchecked path can lose the record of a live archive or remove arbitrary files.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	target, directory := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "delete-target")
	_, _ = saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "delete-survivor")
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+target.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	deleted, err := db.GetAppBackup(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != "deleted" {
		t.Fatalf("deleted record=%+v", deleted)
	}
	if _, err := os.Stat(directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("managed directory remains: %v", err)
	}
	assertAuditExists(t, db, "apps.mysql.backup.delete", "success", "operator", target.ID)
}

func TestMySQLBackupDeleteRevalidatesInstanceOwnershipAfterLock(t *testing.T) {
	// Production break caught: a BEFORE INSERT trigger changes the authoritative instance exactly while the mutation lock is acquired, after preflight read it.
	tests := []struct {
		name       string
		assignment func(store.Server) string
		sensitive  func(store.Server) string
	}{
		{name: "app changed", assignment: func(store.Server) string { return `app='redis-private-detail'` }, sensitive: func(store.Server) string { return "redis-private-detail" }},
		{name: "topology changed", assignment: func(store.Server) string { return `topology='cluster-private-detail'` }, sensitive: func(store.Server) string { return "cluster-private-detail" }},
		{name: "server changed", assignment: func(server store.Server) string { return fmt.Sprintf("server_id=%q", server.ID) }, sensitive: func(server store.Server) string { return server.ID }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
			otherServer, err := db.SaveServer(store.Server{Name: "other", Host: "10.0.0.9", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
			target, directory := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "stale-owner-target")
			_, _ = saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "stale-owner-survivor")
			trigger := fmt.Sprintf(`create trigger mutate_mysql_backup_owner before insert on operation_locks when new.scope='app-instance' and new.resource_id=%q begin update app_instances set %s where id=%q; end`, instance.ID, test.assignment(otherServer), instance.ID)
			if _, err := rawExecSQLite(api.cfg.DatabasePath, trigger); err != nil {
				t.Fatal(err)
			}
			token := issueTestToken(t, db, secret, "operator", "operator")
			req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+target.ID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("X-AIFAR-Language", "en")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "MYSQL_BACKUP_DELETE_NOT_ALLOWED") || !strings.Contains(rec.Body.String(), "cannot be deleted") || strings.Contains(rec.Body.String(), test.sensitive(otherServer)) {
				t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
			}
			after, err := db.GetAppBackup(target.ID)
			if err != nil || after.Status != "success" {
				t.Fatalf("record=%+v err=%v", after, err)
			}
			if _, err := os.Stat(directory); err != nil {
				t.Fatalf("archive directory changed: %v", err)
			}
			assertAuditExists(t, db, "apps.mysql.backup.delete", "failed", "operator", target.ID)
		})
	}
}

func TestMySQLBackupDeleteRejectsActiveSoleOrLockedBackupWithoutChangingRecord(t *testing.T) {
	// Production break caught: deletion must preserve in-flight, sole recovery-point, and operation-locked backups.
	tests := []struct {
		name, status         string
		addSurvivor, addLock bool
	}{
		{"pending", "pending", true, false}, {"running", "running", true, false}, {"sole success", "success", false, false}, {"locked success", "success", true, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
			api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
			backup, directory := saveManagedMySQLBackupWithStatus(t, db, api.cfg.MySQLBackupDir, instance, "protected", test.status)
			if test.addSurvivor {
				_, _ = saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "survivor")
			}
			if test.addLock {
				ownerTask, err := db.CreateTask(store.Task{Type: "apps.mysql.backup", Target: instance.ID, Status: "running", CreatedBy: "operator"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.AcquireOperationLock(store.OperationLock{Scope: "app-instance", ResourceID: instance.ID, Operation: operationLockMutation, OwnerTaskID: ownerTask.ID, Owner: "operator", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			token := issueTestToken(t, db, secret, "operator", "operator")
			req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+backup.ID, nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict {
				t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
			}
			after, err := db.GetAppBackup(backup.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Status != test.status {
				t.Fatalf("record changed=%+v", after)
			}
			if _, err := os.Stat(directory); err != nil {
				t.Fatalf("archive directory changed: %v", err)
			}
			assertAuditExists(t, db, "apps.mysql.backup.delete", "failed", "operator", backup.ID)
		})
	}
}

func TestMySQLBackupDeleteFileFailurePreservesOriginalStatus(t *testing.T) {
	// Production break caught: a failed managed-file deletion must never advance the database row to deleted.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	backup, directory := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "tampered")
	_, _ = saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "survivor")
	if err := os.WriteFile(filepath.Join(directory, "checksums.txt"), []byte(strings.Repeat("0", 64)+"  dump.tar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+backup.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	after, err := db.GetAppBackup(backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Status != "success" {
		t.Fatalf("failed deletion changed status=%+v", after)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("failed deletion removed directory: %v", err)
	}
	assertAuditExists(t, db, "apps.mysql.backup.delete", "failed", "operator", backup.ID)
}

func TestMySQLBackupDeleteRequiresAnotherVerifiedRecoveryPoint(t *testing.T) {
	// Production break caught: a status=success row whose archive no longer verifies must not unprotect the last usable recovery point.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	target, targetDirectory := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "delete-target")
	_, survivorDirectory := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "broken-survivor")
	if err := os.WriteFile(filepath.Join(survivorDirectory, "checksums.txt"), []byte(strings.Repeat("0", 64)+"  dump.tar\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+target.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "MYSQL_BACKUP_LAST_SUCCESS_PROTECTED") {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(targetDirectory); err != nil {
		t.Fatalf("protected archive changed: %v", err)
	}
}

func TestMySQLBackupDeleteRollsBackQuarantineWhenRecordTransitionFails(t *testing.T) {
	// Production break caught: deleting files before a failed DB transition must restore the exact verified directory without overwriting any replacement.
	api, db, secret := newAuthzTestAPI(t)
	_, instance := saveMySQLBackupTarget(t, db, "standalone", "")
	api.cfg.MySQLBackupDir = filepath.Join(t.TempDir(), "mysql-backups")
	target, directory := saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "rollback-target")
	_, _ = saveCommittedMySQLBackup(t, db, api.cfg.MySQLBackupDir, instance, "rollback-survivor")
	rawDB, err := sql.Open("sqlite", api.cfg.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer rawDB.Close()
	trigger := fmt.Sprintf(`create trigger reject_backup_delete before update of status on app_backups when new.id=%q and new.status='deleted' begin select raise(fail, 'injected mark failure'); end`, target.ID)
	if _, err := rawDB.Exec(trigger); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "operator", "operator")
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+target.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "MYSQL_BACKUP_DELETE_RECORD_FAILED") {
		t.Fatalf("DELETE status=%d body=%s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatalf("verified directory was not restored: %v", err)
	}
	after, err := db.GetAppBackup(target.ID)
	if err != nil || after.Status != "success" {
		t.Fatalf("record=%+v err=%v", after, err)
	}
}

func TestMySQLBackupDeleteAuditsMissingAndInvalidIDsWithSanitizedErrors(t *testing.T) {
	// Production break caught: failure audit must exist even when the first lookup fails, without persisting untrusted path text or database errors.
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "operator", "operator")
	for _, tc := range []struct {
		id, auditTarget, code string
		status                int
	}{
		{"backup_aaaaaaaaaaaaaaaaaaaaaaaa", "backup_aaaaaaaaaaaaaaaaaaaaaaaa", "MYSQL_BACKUP_DELETE_NOT_FOUND", http.StatusNotFound},
		{"not-valid!", "invalid-backup-id", "MYSQL_BACKUP_ID_INVALID", http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+tc.id, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, req)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) || strings.Contains(rec.Body.String(), "sql:") {
			t.Fatalf("DELETE %q status=%d body=%s", tc.id, rec.Code, rec.Body.String())
		}
		assertAuditExists(t, db, "apps.mysql.backup.delete", "failed", "operator", tc.auditTarget)
	}
	if _, err := rawExecSQLite(api.cfg.DatabasePath, `drop table app_backups`); err != nil {
		t.Fatal(err)
	}
	lookupID := "backup_bbbbbbbbbbbbbbbbbbbbbbbb"
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/apps/backups/"+lookupID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusInternalServerError || !strings.Contains(rec.Body.String(), "MYSQL_BACKUP_DELETE_LOOKUP_FAILED") || strings.Contains(strings.ToLower(rec.Body.String()), "no such table") {
		t.Fatalf("store failure status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertAuditExists(t, db, "apps.mysql.backup.delete", "failed", "operator", lookupID)
}

func rawExecSQLite(path, statement string) (sql.Result, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	return db.Exec(statement)
}

func saveCommittedMySQLBackup(t *testing.T, db *store.Store, root string, instance store.AppInstance, label string) (store.AppBackup, string) {
	return saveManagedMySQLBackupWithStatus(t, db, root, instance, label, "success")
}

func saveManagedMySQLBackupWithStatus(t *testing.T, db *store.Store, root string, instance store.AppInstance, label, status string) (store.AppBackup, string) {
	t.Helper()
	repository, err := backuprepo.New(root)
	if err != nil {
		t.Fatal(err)
	}
	id := store.NewID("backup")
	paths, err := repository.Prepare(id)
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("archive-" + label)
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if err := repository.Commit(paths, []byte(`{"backupId":"`+id+`","app":"mysql"}`), digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	backup, err := db.SaveAppBackup(store.AppBackup{ID: id, App: "mysql", InstanceID: instance.ID, ServerID: instance.ServerID, BackupType: "logical-full", Status: status, Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: `{"phase":"` + status + `"}`, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	return backup, paths.Directory
}

var mysqlBackupHandlerSteps = []string{"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql", "check-backup-space", "prepare-workdir", "dry-run-dump", "dump-instance", "build-manifest", "package-backup", "transfer-backup", "verify-checksum", "record-backup", "apply-retention", "cleanup-workdir"}

type backupHandlerCall struct {
	request registry.BackupRequest
	run     registry.RunContext
}
type restoreHandlerCall struct {
	request registry.RestoreRequest
	run     registry.RunContext
}
type backupHandlerModule struct {
	calls          chan backupHandlerCall
	release        chan struct{}
	planErr        error
	restoreCalls   chan restoreHandlerCall
	restoreRelease chan struct{}
	restorePlanErr error
}

func newBackupHandlerModule() *backupHandlerModule {
	return &backupHandlerModule{calls: make(chan backupHandlerCall, 1), release: make(chan struct{}), restoreCalls: make(chan restoreHandlerCall, 1), restoreRelease: make(chan struct{})}
}
func (m *backupHandlerModule) Name() string { return "mysql" }
func (m *backupHandlerModule) Manifest(string) registry.Manifest {
	return registry.Manifest{Name: "mysql"}
}
func (m *backupHandlerModule) PreflightInstall(context.Context, registry.InstallRequest, []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}
func (m *backupHandlerModule) PlanInstall(context.Context, registry.InstallRequest, []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}
func (m *backupHandlerModule) ValidateInstall(context.Context, registry.InstallRequest, []store.Resource) error {
	return nil
}
func (m *backupHandlerModule) Install(context.Context, registry.InstallRequest, registry.RunContext) error {
	return nil
}
func (m *backupHandlerModule) PlanBackup(_ context.Context, req registry.BackupRequest) ([]registry.InstallStepPlan, error) {
	if m.planErr != nil {
		return nil, m.planErr
	}
	out := make([]registry.InstallStepPlan, len(mysqlBackupHandlerSteps))
	for index, name := range mysqlBackupHandlerSteps {
		out[index] = registry.InstallStepPlan{Target: req.Instance.ServerID, Name: name, Title: name, Order: index + 1}
	}
	return out, nil
}
func (m *backupHandlerModule) Backup(ctx context.Context, req registry.BackupRequest, run registry.RunContext) error {
	m.calls <- backupHandlerCall{request: req, run: run}
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

var mysqlRestoreHandlerSteps = []string{"load-backup", "acquire-instance-lock", "verify-maintenance-confirmation", "verify-manifest", "verify-checksum", "verify-version", "create-pre-restore-backup", "upload-backup", "extract-backup", "dry-run-load", "capture-local-infile", "enable-local-infile", "drop-target-schemas", "load-dump", "restore-local-infile", "verify-schemas", "verify-data", "record-restore", "cleanup-workdir", "release-lock"}

func (m *backupHandlerModule) PlanRestore(_ context.Context, req registry.RestoreRequest) ([]registry.InstallStepPlan, error) {
	if m.restorePlanErr != nil {
		return nil, m.restorePlanErr
	}
	out := make([]registry.InstallStepPlan, len(mysqlRestoreHandlerSteps))
	for i, name := range mysqlRestoreHandlerSteps {
		out[i] = registry.InstallStepPlan{Target: req.Instance.ServerID, Name: name, Title: name, Order: i + 1}
	}
	return out, nil
}

func (m *backupHandlerModule) Restore(ctx context.Context, req registry.RestoreRequest, run registry.RunContext) error {
	m.restoreCalls <- restoreHandlerCall{request: req, run: run}
	select {
	case <-m.restoreRelease:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func saveMySQLBackupTarget(t *testing.T, db *store.Store, topology, clusterID string) (store.Server, store.AppInstance) {
	t.Helper()
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	metadata := `{"port":3306,"endpoint":"10.0.0.8:3306"}`
	if clusterID != "" {
		metadata = `{"port":3306,"endpoint":"10.0.0.8:3306","clusterId":"` + clusterID + `"}`
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: topology, Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	return server, instance
}

func getBackupListItems(t *testing.T, api *API, token, instanceID string) []store.AppBackup {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/apps/instances/"+instanceID+"/backups", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET backups status=%d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []store.AppBackup `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Items
}

func filepathForTestBackup(name string) string { return "/managed/mysql-backups/" + name + "/dump.tar" }

var _ registry.BackupModule = (*backupHandlerModule)(nil)
var _ registry.RestoreModule = (*backupHandlerModule)(nil)
