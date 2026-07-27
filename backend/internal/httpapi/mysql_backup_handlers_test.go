package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
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

func TestMySQLBackupHandlerValidatesInstanceAppAndStandaloneTopology(t *testing.T) {
	// Production break caught: dispatching a standalone task for a foreign app or cluster member would select the wrong safety model.
	tests := []struct{ name, app, topology string }{{"foreign app", "redis", "standalone"}, {"cluster", "mysql", "innodb-cluster"}}
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
	standaloneBackup, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: standalone.ID, ServerID: standalone.ServerID, BackupType: "logical-full", Status: "success", Path: filepathForTestBackup("one"), Checksum: strings.Repeat("a", 64), Size: 10})
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

	clusterID := "mysql_cluster_1234567890abcdef12345678"
	var members []store.AppInstance
	for index := 0; index < 3; index++ {
		_, member := saveMySQLBackupTarget(t, db, "innodb-cluster", clusterID)
		members = append(members, member)
	}
	clusterBackup, err := db.SaveAppBackup(store.AppBackup{App: "mysql", InstanceID: members[0].ID, ServerID: members[0].ServerID, BackupType: "logical-full", Status: "success", Path: filepathForTestBackup("cluster"), Checksum: strings.Repeat("c", 64), Size: 20, Metadata: `{"clusterId":"` + clusterID + `"}`})
	if err != nil {
		t.Fatal(err)
	}
	clusterItems := getBackupListItems(t, api, token, members[1].ID)
	if len(clusterItems) != 1 || clusterItems[0].ID != clusterBackup.ID {
		t.Fatalf("cluster items=%+v", clusterItems)
	}
}

var mysqlBackupHandlerSteps = []string{"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql", "check-backup-space", "prepare-workdir", "dry-run-dump", "dump-instance", "build-manifest", "package-backup", "transfer-backup", "verify-checksum", "record-backup", "apply-retention", "cleanup-workdir"}

type backupHandlerCall struct {
	request registry.BackupRequest
	run     registry.RunContext
}
type backupHandlerModule struct {
	calls   chan backupHandlerCall
	release chan struct{}
	planErr error
}

func newBackupHandlerModule() *backupHandlerModule {
	return &backupHandlerModule{calls: make(chan backupHandlerCall, 1), release: make(chan struct{})}
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
