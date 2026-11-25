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

type siloUpgradeLockRecoveryModule struct {
	*offlineUpgradeTestModule
	checked  chan string
	decision chan error
}

func (m *siloUpgradeLockRecoveryModule) CheckOfflineUpgradeResumeIdle(ctx context.Context, _ store.AppInstance, taskID string, _ registry.RunContext) error {
	m.checked <- taskID
	select {
	case err := <-m.decision:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestSiloInterruptedUpgradeChecksBothPeersBeforeTakingLocks(t *testing.T) {
	for _, busy := range []bool{true, false} {
		name := "idle"
		if busy {
			name = "busy"
		}
		t.Run(name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			token := issueTestToken(t, db, secret, "owner", "owner")
			const group = "replicated-storage"
			checkpoint := store.SiloOfflineUpgradeCheckpoint{TaskID: "tsk_0123456789abcdef01234567"}
			for _, addr := range []string{"192.0.2.1", "192.0.2.2"} {
				server, err := db.SaveServer(store.Server{Name: addr, Host: addr, Username: "root"})
				if err != nil {
					t.Fatal(err)
				}
				peer, err := db.SaveAppInstance(store.AppInstance{
					App: "minio", ServerID: server.ID, Version: "2025-10-15T17-29-55Z",
					Topology: "bucket-replication", Status: "unavailable", Metadata: `{"replicationGroupId":"` + group + `"}`,
				})
				if err != nil {
					t.Fatal(err)
				}
				checkpoint.Nodes = append(checkpoint.Nodes, store.SiloOfflineUpgradeNode{InstanceID: peer.ID, ServerID: peer.ServerID})
			}
			if err := db.CreateSiloOfflineUpgradeCheckpoint(group, checkpoint); err != nil {
				t.Fatal(err)
			}
			previous, err := db.CreateTask(store.Task{Type: "apps.minio.upgrade", Target: group, Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
			prior, err := db.AcquireOperationLock(store.OperationLock{Scope: "app-cluster", ResourceID: "minio-silo:" + group, Operation: "mutate", OwnerTaskID: previous.ID})
			if err != nil {
				t.Fatal(err)
			}
			priorServer, err := db.AcquireOperationLock(store.OperationLock{Scope: "server-config", ResourceID: checkpoint.Nodes[0].ServerID, Operation: "mutate", OwnerTaskID: previous.ID})
			if err != nil {
				t.Fatal(err)
			}
			other, err := db.AcquireOperationLock(store.OperationLock{Scope: "app-cluster", ResourceID: group, Operation: "mutate", OwnerTaskID: "another-product"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.RecoverInterruptedTasks("interrupted at startup"); err != nil {
				t.Fatal(err)
			}
			if err := db.ReplaceResources([]store.Resource{
				{App: "silo", Part: "backend", Version: "20261112", Path: "silo_20261112000000.0.0_linux_amd64.tar.gz"},
				{App: "silo", Part: "backend", Version: "20261112", Path: "mcli_20261112000000.0.0_linux_amd64.tar.gz"},
			}); err != nil {
				t.Fatal(err)
			}
			module := &siloUpgradeLockRecoveryModule{
				offlineUpgradeTestModule: &offlineUpgradeTestModule{backupHandlerModule: newBackupHandlerModule(), called: make(chan registry.RunContext, 1), finish: make(chan struct{})},
				checked:                  make(chan string, 1), decision: make(chan error, 1),
			}
			finish := sync.OnceFunc(func() { close(module.finish) })
			defer finish()
			defer close(module.decision)
			api.apps = registry.New(module)
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+checkpoint.Nodes[0].InstanceID+"/upgrade", strings.NewReader(`{"targetVersion":"20261112","confirmed":true}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			var reply struct {
				TaskID string `json:"taskId"`
			}
			if rec.Code != http.StatusAccepted || json.Unmarshal(rec.Body.Bytes(), &reply) != nil || reply.TaskID == "" {
				t.Fatalf("expected a recovery task, got %d %s", rec.Code, rec.Body.String())
			}
			select {
			case checked := <-module.checked:
				if checked != checkpoint.TaskID {
					t.Fatalf("guard received the wrong checkpoint: %s", checked)
				}
			case <-module.called:
				t.Fatal("migration started without checking the old task")
			case <-time.After(3 * time.Second):
				t.Fatal("migration guard did not start")
			}
			active, err := db.GetOperationLock(prior.ID)
			if err != nil || active.Status != "active" || active.OwnerTaskID != previous.ID {
				t.Fatalf("original lock changed before idle verification: %+v (%v)", active, err)
			}
			if busy {
				module.decision <- errors.New("still active")
				waitForTaskStatus(t, db, reply.TaskID, "failed")
				active, err := db.GetOperationLock(prior.ID)
				if err != nil || active.Status != "active" || active.OwnerTaskID != previous.ID {
					t.Fatalf("busy guard lost the original lock: %+v (%v)", active, err)
				}
				active, err = db.GetOperationLock(priorServer.ID)
				if err != nil || active.Status != "active" || active.OwnerTaskID != previous.ID {
					t.Fatalf("busy guard lost the original server lock: %+v (%v)", active, err)
				}
				return
			}
			module.decision <- nil
			select {
			case <-module.called:
			case <-time.After(3 * time.Second):
				t.Fatal("migration did not advance after its idle check")
			}
			for _, node := range checkpoint.Nodes {
				locks, err := db.ListOperationLocks("server-config", node.ServerID, true)
				if err != nil || len(locks) != 1 || locks[0].OwnerTaskID != reply.TaskID {
					t.Fatalf("server lock missing after takeover: %+v (%v)", locks, err)
				}
			}
			archived, err := db.GetOperationLock(priorServer.ID)
			if err != nil || archived.Status != "released" || archived.OwnerTaskID != previous.ID {
				t.Fatalf("original server lock was not archived on takeover: %+v (%v)", archived, err)
			}
			preserved, err := db.GetOperationLock(other.ID)
			if err != nil || preserved.Status != other.Status || preserved.OwnerTaskID != other.OwnerTaskID || !preserved.UpdatedAt.Equal(other.UpdatedAt) {
				t.Fatalf("takeover touched another product's cluster lock: %+v (%v)", preserved, err)
			}
			finish()
			waitForTaskStatus(t, db, reply.TaskID, "success")
		})
	}
}
