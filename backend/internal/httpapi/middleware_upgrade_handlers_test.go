package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type offlineUpgradeTestModule struct {
	*backupHandlerModule
	called chan registry.RunContext
	finish chan struct{}
}

func (m *offlineUpgradeTestModule) Name() string { return "minio" }

func (m *offlineUpgradeTestModule) Manifest(string) registry.Manifest {
	return registry.Manifest{Name: "minio"}
}

func (m *offlineUpgradeTestModule) PlanCheck(_ context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	return []registry.InstallStepPlan{{Target: req.Server.ID, Name: "check", Title: "Check", Order: 1}}, nil
}

func (m *offlineUpgradeTestModule) Check(context.Context, registry.CheckRequest, registry.RunContext) (registry.InstanceStatus, error) {
	return registry.InstanceStatus{Status: "available"}, nil
}

func (m *offlineUpgradeTestModule) RunOfflineUpgrade(ctx context.Context, seed store.AppInstance, resources []store.Resource, run registry.RunContext) error {
	m.called <- run
	select {
	case <-m.finish:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestOfflineUpgradeRequiresOwnerConfirmationAndLocksBothStoragePeers(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	var peers []store.AppInstance
	for _, addr := range []string{"192.0.2.1", "192.0.2.2"} {
		server, err := db.SaveServer(store.Server{Name: addr, Host: addr, Username: "lab"})
		if err != nil {
			t.Fatal(err)
		}
		peer, err := db.SaveAppInstance(store.AppInstance{
			App: "minio", ServerID: server.ID, Version: "2025-10-15T17-29-55Z",
			Topology: "bucket-replication", Status: "installed", Metadata: `{"replicationGroupId":"test-group"}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		peers = append(peers, peer)
	}
	module := &offlineUpgradeTestModule{
		backupHandlerModule: newBackupHandlerModule(),
		called:              make(chan registry.RunContext, 1), finish: make(chan struct{}),
	}
	t.Cleanup(func() { close(module.finish) })
	api.apps = registry.New(module)
	owner := issueTestToken(t, db, secret, "owner", "owner")
	operator := issueTestToken(t, db, secret, "operator", "operator")
	post := func(id, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+id+"/upgrade", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, req)
		return rec
	}

	const confirmed = `{"targetVersion":"20261112","confirmed":true}`
	if rec := post(peers[0].ID, operator, confirmed); rec.Code != http.StatusForbidden {
		t.Fatalf("operator status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(peers[0].ID, owner, `{"targetVersion":"20261112"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("missing confirmation status=%d body=%s", rec.Code, rec.Body.String())
	}
	if rec := post(peers[0].ID, owner, confirmed); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "UPGRADE_RESOURCES_MISSING") {
		t.Fatalf("missing artifacts status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := db.ReplaceResources([]store.Resource{
		{App: "silo", Part: "backend", Version: "20261112", Path: "silo_20261112000000.0.0_linux_amd64.tar.gz"},
		{App: "silo", Part: "backend", Version: "20261112", Path: "mcli_20261112000000.0.0_linux_amd64.tar.gz"},
	}); err != nil {
		t.Fatal(err)
	}
	rec := post(peers[0].ID, owner, confirmed)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upgrade status=%d body=%s", rec.Code, rec.Body.String())
	}
	var reply struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &reply); err != nil || reply.TaskID == "" {
		t.Fatalf("task response=%s err=%v", rec.Body.String(), err)
	}
	select {
	case run := <-module.called:
		if run.TaskID != reply.TaskID || run.Log == nil || run.TargetLog == nil {
			t.Fatalf("upgrade task context=%+v", run)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not execute upgrade")
	}
	for _, peer := range peers {
		locks, err := db.ListOperationLocks("app-instance", peer.ID, true)
		if err != nil || len(locks) != 1 || locks[0].OwnerTaskID != reply.TaskID {
			t.Fatalf("instance %s locks=%+v err=%v", peer.ID, locks, err)
		}
	}
	if rec := post(peers[1].ID, owner, confirmed); rec.Code != http.StatusConflict {
		t.Fatalf("peer duplicate status=%d body=%s", rec.Code, rec.Body.String())
	}
	assertAuditExists(t, db, "apps.minio.upgrade", "running", "owner", peers[0].ID)
	check := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+peers[1].ID+"/check", nil)
	check.Header.Set("Authorization", "Bearer "+owner)
	rec = httptest.NewRecorder()
	api.Router().ServeHTTP(rec, check)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "OPERATION_LOCKED") {
		t.Fatalf("check can race the upgrade: status=%d body=%s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/tasks/"+reply.TaskID+"/cancel", nil)
	req.Header.Set("Authorization", "Bearer "+owner)
	rec = httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cancelled":false`) {
		t.Fatalf("committed upgrade can be cancelled: status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestMiddlewareMySQLUpgradeAllowsMixedVersionsOnlyWithCheckpoint(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	owner := issueTestToken(t, db, secret, "owner", "owner")
	const clusterID = "cluster_aabbccddeeff001122334455"
	_, instances := saveHTTPTestMySQLCluster(t, db, clusterID, "test")
	seed := instances[0]
	seed.Version = "8.4.13"
	seed, err := db.SaveAppInstance(seed)
	if err != nil {
		t.Fatal(err)
	}
	post := func() *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+seed.ID+"/upgrade", strings.NewReader(`{"targetVersion":"8.4.13","confirmed":true}`))
		req.Header.Set("Authorization", "Bearer "+owner)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, req)
		return rec
	}
	if rec := post(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "UPGRADE_PATH_UNSUPPORTED") {
		t.Fatalf("mixed cluster admitted without checkpoint: %d %s", rec.Code, rec.Body)
	}
	checkpoint := store.MySQLOfflineUpgradeCheckpoint{TaskID: "original-upgrade"}
	if err := db.CreateMySQLOfflineUpgradeCheckpoint(clusterID, checkpoint); err != nil {
		t.Fatal(err)
	}
	if rec := post(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "UPGRADE_RESOURCES_MISSING") {
		t.Fatalf("mixed cluster should pass source-version gating and check resources: %d %s", rec.Code, rec.Body)
	}
}
