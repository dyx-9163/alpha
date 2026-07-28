package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type reconciliationCredentialRemote struct {
	password string
	commands []string
}

func (r *reconciliationCredentialRemote) Run(_ context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if server.Password != r.password && strings.TrimSpace(server.PrivateKey) == "" {
		return adapter.CommandResult{}, errors.New("saved SSH credential missing")
	}
	r.commands = append(r.commands, command)
	if strings.Contains(command, "SELECT @@GLOBAL.local_infile") {
		return adapter.CommandResult{Stdout: "OFF\n"}, nil
	}
	return adapter.CommandResult{}, nil
}

func (r *reconciliationCredentialRemote) UploadFile(_ context.Context, server store.Server, _ string, _ string, _ os.FileMode) error {
	if server.Password != r.password && strings.TrimSpace(server.PrivateKey) == "" {
		return errors.New("saved SSH credential missing")
	}
	return nil
}

func TestMySQLReconciliationWorkerKeepsSSHSecretOutOfLogsAuditResponseCommandsAndMetadata(t *testing.T) {
	api, db, authSecret := newAuthzTestAPI(t)
	sshSecret := "ssh-handler-test-only-secret"
	mysqlSecret := "mysql-handler-test-only-secret"
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root", Password: sshSecret})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "standalone", Metadata: `{"port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.SaveCredential(store.Credential{Name: "mysql-admin", Kind: "mysql", Username: "root", Scope: "app-instance", Status: "active", Secret: map[string]string{"password": mysqlSecret}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.BindCredential(store.CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}
	remote := &reconciliationCredentialRemote{password: sshSecret}
	module := mysqlapp.NewModule(db, remote)
	api.apps = registry.New(module)
	req := reconciliationRequest(instance.ID, issueTestToken(t, db, authSecret, "owner", "owner"), `{"reconciliationConfirmed":true}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	waitForTaskStatus(t, db, taskID, "success")
	_, logs, err := db.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	audit, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	evidence, _ := json.Marshal([]any{rec.Body.String(), logs, audit, fresh.Metadata, remote.commands})
	if strings.Contains(string(evidence), sshSecret) || strings.Contains(string(evidence), mysqlSecret) {
		t.Fatalf("credential leaked into public or persisted evidence: %s", evidence)
	}
}

func TestMySQLReconciliationHandlerOwnerOnlyCreatesRawLockedAuditedTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "test-reconciliation", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `","mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "PRIMARY", Status: "ONLINE"}); err != nil {
		t.Fatal(err)
	}
	for index := 1; index < 3; index++ {
		secondaryServer, err := db.SaveServer(store.Server{Name: "mysql-secondary", Host: "10.0.0." + string(rune('8'+index)), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		secondary, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: secondaryServer.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"` + clusterID + `"}`})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: secondary.ID, ServerID: secondaryServer.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
	}
	module := newReconciliationHandlerModule()
	api.apps = registry.New(module)

	denied := reconciliationRequest(instance.ID, issueTestToken(t, db, secret, "operator", "operator"), `{"reconciliationConfirmed":true}`)
	deniedRec := httptest.NewRecorder()
	api.Router().ServeHTTP(deniedRec, denied)
	if deniedRec.Code != http.StatusForbidden {
		t.Fatalf("non-owner status=%d body=%s", deniedRec.Code, deniedRec.Body.String())
	}

	req := reconciliationRequest(instance.ID, issueTestToken(t, db, secret, "owner", "owner"), `{"reconciliationConfirmed":true}`)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	select {
	case call := <-module.calls:
		if call.plan.Instance.ID != instance.ID || call.taskID != taskID || call.plan.Cluster.ID != clusterID || len(call.plan.Members) != 3 {
			t.Fatalf("call=%+v", call)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reconciliation worker did not start")
	}
	task, _, err := db.GetTask(taskID)
	if err != nil || task.Type != "apps.mysql.reconciliation.run" || task.Target != clusterID {
		t.Fatalf("task=%+v err=%v", task, err)
	}
	steps, _ := db.ListTaskSteps(taskID)
	targets, _ := db.ListTaskTargets(taskID)
	locks, _ := db.ListOperationLocks("app-cluster", clusterID, false)
	if len(steps) != 1 || steps[0].Name != "reconcile-local-infile" || len(targets) != 1 || targets[0].Target != clusterID || len(locks) != 1 || locks[0].Operation != operationLockMutation {
		t.Fatalf("steps=%+v targets=%+v locks=%+v", steps, targets, locks)
	}
	assertAuditExists(t, db, "apps.mysql.reconciliation.run", "running", "owner", instance.ID)
	close(module.release)
	waitForTaskStatus(t, db, taskID, "success")
}

func TestMySQLReconciliationHandlerRejectsInvalidBodyMissingAndMalformedMarkersBeforeTask(t *testing.T) {
	for _, test := range []struct {
		name, metadata, body, code string
		status                     int
	}{
		{name: "missing confirmation", metadata: `{"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`, body: `{}`, code: "MYSQL_RECONCILIATION_CONFIRMATION_REQUIRED", status: http.StatusBadRequest},
		{name: "duplicate confirmation", metadata: `{"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`, body: `{"reconciliationConfirmed":true,"reconciliationConfirmed":true}`, code: "MYSQL_RECONCILIATION_CONFIRMATION_REQUIRED", status: http.StatusBadRequest},
		{name: "no marker", metadata: `{}`, body: `{"reconciliationConfirmed":true}`, code: "MYSQL_RECONCILIATION_NOT_REQUIRED", status: http.StatusConflict},
		{name: "malformed marker", metadata: `{"mysqlReconciliation":{"version":2}}`, body: `{"reconciliationConfirmed":true}`, code: "MYSQL_RECONCILIATION_REQUIRED", status: http.StatusConflict},
	} {
		t.Run(test.name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: test.metadata})
			if err != nil {
				t.Fatal(err)
			}
			module := newReconciliationHandlerModule()
			api.apps = registry.New(module)
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, reconciliationRequest(instance.ID, issueTestToken(t, db, secret, "owner", "owner"), test.body))
			if rec.Code != test.status || !strings.Contains(rec.Body.String(), `"code":"`+test.code+`"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
		})
	}
}

func TestOrdinaryClusterLifecycleRejectsReconciliationOnNonRequestedSecondaryBeforeTask(t *testing.T) {
	for _, action := range []string{"check", "backup", "restore", "delete", "start"} {
		t.Run(action, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			instances := saveReconciliationGateCluster(t, db, false)
			owner := issueTestToken(t, db, secret, "owner", "owner")
			var req *http.Request
			switch action {
			case "check":
				api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/check", nil)
			case "backup":
				api.apps = registry.New(newBackupHandlerModule())
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/backup", strings.NewReader(`{}`))
			case "restore":
				api.apps = registry.New(newBackupHandlerModule())
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/restore", strings.NewReader(`{"backupId":"backup_1234567890abcdef12345678","mode":"innodb-cluster","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"threads":4}`))
			case "delete":
				api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/delete", strings.NewReader(`{"serverPassword":"unused"}`))
			case "start":
				api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
				body := `{"instanceIds":["` + instances[0].ID + `","` + instances[1].ID + `","` + instances[2].ID + `"]}`
				req = httptest.NewRequest(http.MethodPost, "/api/v2/database/mysql/clusters/start", strings.NewReader(body))
			}
			req.Header.Set("Authorization", "Bearer "+owner)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-AIFAR-Language", "en")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_RECONCILIATION_REQUIRED"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
				t.Fatalf("blocked %s created tasks=%+v err=%v", action, tasks, err)
			}
		})
	}
}

func TestOrdinaryClusterLifecycleFailsClosedForMalformedSecondaryAndMembershipDrift(t *testing.T) {
	for _, malformed := range []bool{true, false} {
		name := "member server drift"
		if malformed {
			name = "malformed secondary marker"
		}
		t.Run(name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			instances := saveReconciliationGateCluster(t, db, malformed)
			if !malformed {
				members, err := db.ListAppClusterMembers("cluster_1234567890abcdef12345678")
				if err != nil {
					t.Fatal(err)
				}
				members[1].ServerID = instances[0].ServerID
				if _, err := db.SaveAppClusterMember(members[1]); err != nil {
					t.Fatal(err)
				}
			}
			api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/check", nil)
			req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_RECONCILIATION_REQUIRED"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
				t.Fatalf("unsafe topology created tasks=%+v err=%v", tasks, err)
			}
		})
	}
}

func saveReconciliationGateCluster(t *testing.T, db *store.Store, malformedMarker bool) []store.AppInstance {
	t.Helper()
	clusterID := "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "reconciliation-gate", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0." + string(rune('1'+index)), Username: "root"})
		if err != nil {
			t.Fatal(err)
		}
		metadata := `{"clusterId":"` + clusterID + `"}`
		if index == 1 {
			if malformedMarker {
				metadata = `{"clusterId":"` + clusterID + `","mysqlReconciliation":{"version":2}}`
			} else {
				metadata = `{"clusterId":"` + clusterID + `","mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`
			}
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "innodb-cluster", Metadata: metadata})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	return instances
}

func TestOrdinaryMySQLLifecycleRejectsReconciliationBeforeTask(t *testing.T) {
	for _, action := range []string{"check", "backup", "restore"} {
		t.Run(action, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			server, err := db.SaveServer(store.Server{Name: "mysql", Host: "10.0.0.8", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "standalone", Metadata: `{"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-07-28T00:00:00Z","taskId":"tsk_abcdef1234567890abcdef12"}}`})
			if err != nil {
				t.Fatal(err)
			}
			var req *http.Request
			switch action {
			case "check":
				api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/check", nil)
			case "backup":
				api.apps = registry.New(newBackupHandlerModule())
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/backup", strings.NewReader(`{}`))
			case "restore":
				api.apps = registry.New(newBackupHandlerModule())
				req = httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/restore", strings.NewReader(`{"backupId":"backup_1234567890abcdef12345678","mode":"standalone","maintenanceConfirmed":true,"createPreRestoreBackup":true,"disasterConfirmed":false,"threads":4}`))
			}
			req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_RECONCILIATION_REQUIRED"`) {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tasks, err := db.ListTasks(); err != nil || len(tasks) != 0 {
				t.Fatalf("tasks=%+v err=%v", tasks, err)
			}
		})
	}
}

func reconciliationRequest(instanceID, token, body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instanceID+"/mysql/reconciliation/run", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AIFAR-Language", "en")
	return req
}

type reconciliationHandlerCall struct {
	plan   mysqlapp.ReconciliationPlan
	taskID string
}
type reconciliationHandlerModule struct {
	*backupHandlerModule
	calls   chan reconciliationHandlerCall
	release chan struct{}
}

func newReconciliationHandlerModule() *reconciliationHandlerModule {
	return &reconciliationHandlerModule{backupHandlerModule: newBackupHandlerModule(), calls: make(chan reconciliationHandlerCall, 1), release: make(chan struct{})}
}
func (m *reconciliationHandlerModule) Reconcile(ctx context.Context, plan mysqlapp.ReconciliationPlan, _ string, taskID string, _ mysqlapp.Logger) error {
	m.calls <- reconciliationHandlerCall{plan: plan, taskID: taskID}
	select {
	case <-m.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
