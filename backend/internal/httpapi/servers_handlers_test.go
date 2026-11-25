package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestServerMutationsRespectMySQLUpgradeLock(t *testing.T) {
	for _, method := range []string{http.MethodPut, http.MethodPost, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			token := issueTestToken(t, db, secret, "owner", "owner")
			server, err := db.SaveServer(store.Server{Name: "mysql", Host: "192.0.2.1", Username: "root", DeployDir: "/aifar/apps"})
			if err != nil {
				t.Fatal(err)
			}
			lock, err := db.AcquireOperationLock(store.OperationLock{
				Scope: "server-config", ResourceID: server.ID, Operation: "mutate", OwnerTaskID: "tsk_upgrade",
			})
			if err != nil {
				t.Fatal(err)
			}
			url := "/api/v2/servers/" + server.ID
			if method == http.MethodPost {
				url = "/api/v2/servers"
			}
			body := `{"id":"` + server.ID + `","name":"mysql","host":"192.0.2.2","username":"root","deployDir":"/new/apps"}`
			router := api.Router()
			request := func() *httptest.ResponseRecorder {
				req := httptest.NewRequest(method, url, strings.NewReader(body))
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", "application/json")
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, req)
				return rec
			}
			if rec := request(); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "OPERATION_LOCKED") {
				t.Fatalf("locked server mutation status=%d body=%s", rec.Code, rec.Body.String())
			}
			current, err := db.GetServer(server.ID, false)
			if err != nil || current.Host != server.Host || current.DeployDir != server.DeployDir {
				t.Fatalf("locked server changed: %+v, %v", current, err)
			}
			if released, err := db.ReleaseOperationLock(lock.ID); err != nil || !released {
				t.Fatalf("release upgrade lock: %v, %v", released, err)
			}
			if rec := request(); rec.Code != http.StatusOK {
				t.Fatalf("unlocked server mutation status=%d body=%s", rec.Code, rec.Body.String())
			}
			if locks, err := db.ListOperationLocks("server-config", server.ID, true); err != nil || len(locks) != 0 {
				t.Fatalf("server request left an active lock: %+v, %v", locks, err)
			}
		})
	}
}

type serverLockMySQLUpgradeModule struct{ *offlineUpgradeTestModule }

func (m *serverLockMySQLUpgradeModule) Name() string { return "mysql" }

func (m *serverLockMySQLUpgradeModule) Manifest(string) registry.Manifest {
	return registry.Manifest{Name: "mysql"}
}

func TestMySQLUpgradeLocksServerConfigurationForMembersAndRouters(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "owner", "owner")
	const clusterID = "cluster_1234567890abcdef12345678"
	members, instances := saveHTTPTestMySQLCluster(t, db, clusterID, "test")
	routerServer, err := db.SaveServer(store.Server{Name: "router", Host: "192.0.2.4", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		App: "mysql-router", Version: "8.0.36", ServerID: routerServer.ID, Status: "installed", Topology: "router",
		Metadata: `{"clusterId":"` + clusterID + `"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceResources([]store.Resource{
		{App: "mysql", Part: "backend", Version: "8.4.13", Path: "mysql-aifar-8.4.13-official-bundle.tar"},
	}); err != nil {
		t.Fatal(err)
	}
	upgrade := &offlineUpgradeTestModule{
		backupHandlerModule: newBackupHandlerModule(),
		called:              make(chan registry.RunContext, 1),
		finish:              make(chan struct{}),
	}
	finish := sync.OnceFunc(func() { close(upgrade.finish) })
	defer finish()
	api.apps = registry.New(&serverLockMySQLUpgradeModule{upgrade})
	router := api.Router()
	request := func(method, url, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, url, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	upgradeURL := "/api/v2/apps/instances/" + instances[0].ID + "/upgrade"
	const upgradeBody = `{"targetVersion":"8.4.13","confirmed":true}`
	spec := serverConfigOperationLockSpec("servers.save", routerServer.ID)
	editing, err := db.AcquireOperationLock(store.OperationLock{
		Scope: spec.Scope, ResourceID: spec.ResourceID, Operation: spec.Operation, Metadata: spec.Metadata,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rec := request(http.MethodPost, upgradeURL, upgradeBody); rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "OPERATION_LOCKED") {
		t.Fatalf("upgrade started during a Router server edit: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if released, err := db.ReleaseOperationLock(editing.ID); err != nil || !released {
		t.Fatalf("release server edit lock: %v, %v", released, err)
	}

	start := request(http.MethodPost, upgradeURL, upgradeBody)
	if start.Code != http.StatusAccepted {
		t.Fatalf("upgrade status=%d body=%s", start.Code, start.Body.String())
	}
	var started struct {
		TaskID string `json:"taskId"`
	}
	if err := json.Unmarshal(start.Body.Bytes(), &started); err != nil || started.TaskID == "" {
		t.Fatalf("invalid task response=%s err=%v", start.Body.String(), err)
	}
	select {
	case <-upgrade.called:
	case <-time.After(3 * time.Second):
		t.Fatal("upgrade worker did not start")
	}
	for _, server := range append(members, routerServer) {
		rec := request(http.MethodDelete, "/api/v2/servers/"+server.ID, "")
		if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), started.TaskID) {
			t.Fatalf("upgrade server %s can be deleted: status=%d body=%s", server.ID, rec.Code, rec.Body.String())
		}
	}
	created := request(http.MethodPost, "/api/v2/servers", `{"name":"unrelated","host":"192.0.2.5","username":"root"}`)
	if created.Code != http.StatusOK {
		t.Fatalf("upgrade blocked creation of another server: status=%d body=%s", created.Code, created.Body.String())
	}
	finish()
	waitForTaskStatus(t, db, started.TaskID, "success")
}
