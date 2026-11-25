package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type mysqlUpgradeLockRecoveryModule struct {
	*serverLockMySQLUpgradeModule
	checking chan mysqlUpgradeLockGuardCall
	decision chan error
}

type mysqlUpgradeLockGuardCall struct {
	checkpointTaskID string
	deadline         time.Time
}

func (m *mysqlUpgradeLockRecoveryModule) CheckOfflineUpgradeResumeIdle(ctx context.Context, _ store.AppInstance, checkpointTaskID string, _ registry.RunContext) error {
	deadline, _ := ctx.Deadline()
	m.checking <- mysqlUpgradeLockGuardCall{checkpointTaskID, deadline}
	select {
	case err := <-m.decision:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestMySQLInterruptedUpgradeLockRecovery(t *testing.T) {
	for _, scenario := range []string{"busy", "timeout", "cancelled", "new owner", "concurrent recovery", "normal failure", "running", "pending at restart", "other cluster", "no checkpoint"} {
		t.Run(scenario, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			token := issueTestToken(t, db, secret, "owner", "owner")
			const clusterID = "cluster_1234567890abcdef12345678"
			servers, instances := saveHTTPTestMySQLCluster(t, db, clusterID, "test")
			oldTask := store.Task{Type: "apps.mysql.upgrade", Target: clusterID, Status: "running"}
			if scenario == "other cluster" {
				oldTask.Target = "cluster_aaaaaaaaaaaaaaaaaaaaaaaa"
			}
			if scenario == "pending at restart" {
				oldTask.Status = "pending"
			}
			oldTask, err := db.CreateTask(oldTask)
			if err != nil {
				t.Fatal(err)
			}
			setupLock := func(scope, resourceID, owner string) store.OperationLock {
				t.Helper()
				lock, err := db.AcquireOperationLock(store.OperationLock{
					Scope: scope, ResourceID: resourceID, Operation: "mutate", OwnerTaskID: owner, Owner: "owner",
				})
				if err != nil {
					t.Fatal(err)
				}
				return lock
			}
			oldCluster := setupLock("app-cluster", clusterID, oldTask.ID)
			oldServer := setupLock("server-config", servers[0].ID, oldTask.ID)
			unrelated := setupLock("server-config", "unrelated-server", oldTask.ID)
			if scenario != "no checkpoint" {
				// The interrupted owner can itself be a retry of the earlier task.
				checkpoint := store.MySQLOfflineUpgradeCheckpoint{TaskID: "tsk_0123456789abcdef01234567", BundleSHA256: strings.Repeat("a", 64)}
				for _, instance := range instances {
					checkpoint.Nodes = append(checkpoint.Nodes, store.MySQLOfflineUpgradeNode{InstanceID: instance.ID, ServerID: instance.ServerID})
				}
				if err := db.CreateMySQLOfflineUpgradeCheckpoint(clusterID, checkpoint); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.ReplaceResources([]store.Resource{{App: "mysql", Version: "8.4.13", Part: "backend", Path: "mysql-aifar-8.4.13-official-bundle.tar"}}); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "normal failure":
				if err := db.UpdateTaskStatus(oldTask.ID, "failed", "interrupted at startup"); err != nil {
					t.Fatal(err)
				}
			case "running":
			default:
				if recovered, err := db.RecoverInterruptedTasks("interrupted at startup"); err != nil || len(recovered) != 1 {
					t.Fatalf("startup recovery=%v err=%v", recovered, err)
				}
			}

			module := &mysqlUpgradeLockRecoveryModule{
				serverLockMySQLUpgradeModule: &serverLockMySQLUpgradeModule{&offlineUpgradeTestModule{
					backupHandlerModule: newBackupHandlerModule(), called: make(chan registry.RunContext, 2), finish: make(chan struct{}),
				}},
				checking: make(chan mysqlUpgradeLockGuardCall, 2), decision: make(chan error, 2),
			}
			finish := sync.OnceFunc(func() { close(module.finish) })
			defer finish()
			defer close(module.decision)
			api.apps = registry.New(module)
			router := api.Router()
			request := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instances[0].ID+"/upgrade", strings.NewReader(`{"targetVersion":"8.4.13","confirmed":true}`))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			accept := func(rec *httptest.ResponseRecorder) string {
				t.Helper()
				var reply struct {
					TaskID string `json:"taskId"`
				}
				if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.TaskID == "" {
					t.Fatalf("upgrade status=%d body=%s", rec.Code, rec.Body.String())
				}
				return reply.TaskID
			}
			waitGuard := func() {
				t.Helper()
				select {
				case call := <-module.checking:
					if call.checkpointTaskID != "tsk_0123456789abcdef01234567" || call.deadline.IsZero() || time.Until(call.deadline) > 30*time.Second {
						t.Fatalf("guard did not check the expected checkpoint with a deadline: %+v", call)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("recovery guard did not start")
				}
			}
			checkLock := func(lock store.OperationLock, status string) {
				t.Helper()
				current, err := db.GetOperationLock(lock.ID)
				if err != nil || current.OwnerTaskID != lock.OwnerTaskID || current.Status != status {
					t.Fatalf("lock %s changed unexpectedly: %+v err=%v", lock.ID, current, err)
				}
			}

			rec := request()
			switch scenario {
			case "normal failure", "running", "pending at restart", "other cluster", "no checkpoint":
				if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "OPERATION_LOCKED") {
					t.Fatalf("unrecoverable lock allowed upgrade: status=%d body=%s", rec.Code, rec.Body.String())
				}
				checkLock(oldCluster, "active")
				checkLock(oldServer, "active")
				return
			}
			first := accept(rec)
			waitGuard()
			checkLock(oldCluster, "active")
			checkLock(oldServer, "active")
			select {
			case <-module.called:
				t.Fatal("upgrade ran before the remote idle guard finished")
			default:
			}
			var second string
			var competitor store.OperationLock
			wantStatus := "failed"
			switch scenario {
			case "busy":
				module.decision <- errors.New("a remote phase still holds its lock")
			case "timeout":
				module.decision <- context.DeadlineExceeded
			case "cancelled":
				wantStatus = "cancelled"
				if !api.tasks.Cancel(first) {
					t.Fatal("the guard was not cancellable")
				}
			case "new owner":
				competitor = setupLock("server-config", servers[1].ID, "other-running-operation")
				module.decision <- nil
			case "concurrent recovery":
				second = accept(request())
				waitGuard()
				module.decision <- nil
				module.decision <- nil
			}
			if second == "" {
				waitForTaskStatus(t, db, first, wantStatus)
				prior, _, err := db.GetTask(oldTask.ID)
				if err != nil || prior.Status != "failed" {
					t.Fatalf("interrupted task changed: %+v err=%v", prior, err)
				}
				checkLock(oldCluster, "active")
				checkLock(oldServer, "active")
				if competitor.ID != "" {
					checkLock(competitor, "active")
				}
				return
			}

			var winner string
			select {
			case run := <-module.called:
				winner = run.TaskID
			case <-time.After(3 * time.Second):
				t.Fatal("neither checked retry took over")
			}
			loser := first
			if winner == first {
				loser = second
			} else if winner != second {
				t.Fatalf("unexpected lock owner: %s", winner)
			}
			waitForTaskStatus(t, db, loser, "failed")
			locks, err := db.ListOperationLocks("app-cluster", clusterID, true)
			if err != nil || len(locks) != 1 || locks[0].OwnerTaskID != winner {
				t.Fatalf("cluster lock takeover failed: %+v err=%v", locks, err)
			}
			checkLock(oldCluster, "released")
			checkLock(oldServer, "released")
			checkLock(unrelated, "active")
			finish()
			waitForTaskStatus(t, db, winner, "success")
		})
	}
}
