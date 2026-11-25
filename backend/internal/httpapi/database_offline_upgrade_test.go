package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aifar-deployment/backend/internal/store"
)

func TestDatabaseInstancesProjectsPendingMySQLUpgradeFromCheckpointMembership(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	owner := issueTestToken(t, db, secret, "owner", "owner")
	const clusterID = "cluster_0123456789abcdef01234567"
	const otherClusterID = "cluster_1123456789abcdef01234567"
	const sourceVersion = "8.0.36"
	save := func(app, server, cluster, status, extra string) store.AppInstance {
		t.Helper()
		row, err := db.SaveAppInstance(store.AppInstance{
			App: app, ServerID: server, Version: sourceVersion, Topology: "innodb-cluster", Status: status,
			Metadata: `{"clusterId":"` + cluster + `","lastCheck":{"status":"` + status + `"}` + extra + `}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	first := save("mysql", "server-a", clusterID, "offline", "")
	second := save("mysql", "server-b", clusterID, "running", "")
	third := save("mysql", "server-c", clusterID, "running", "")
	wrongServer := save("mysql", "server-current", clusterID, "running", `,"offlineUpgradePending":true`)
	wrongCluster := save("mysql", "server-d", otherClusterID, "running", `,"offlineUpgradePending":true`)
	redis := save("redis", "server-r", "", "running", "")
	if err := db.CreateMySQLOfflineUpgradeCheckpoint(clusterID, store.MySQLOfflineUpgradeCheckpoint{
		TaskID: "original-task", Nodes: []store.MySQLOfflineUpgradeNode{
			{InstanceID: first.ID, ServerID: first.ServerID},
			{InstanceID: second.ID, ServerID: second.ServerID},
			{InstanceID: third.ID, ServerID: third.ServerID},
			{InstanceID: wrongServer.ID, ServerID: "server-other"},
			{InstanceID: wrongCluster.ID, ServerID: wrongCluster.ServerID},
		},
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v2/database/instances", nil)
	req.Header.Set("Authorization", "Bearer "+owner)
	res := httptest.NewRecorder()
	api.Router().ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("database list returned %d: %s", res.Code, res.Body.String())
	}
	var instances []store.AppInstance
	if err := json.Unmarshal(res.Body.Bytes(), &instances); err != nil {
		t.Fatal(err)
	}
	if len(instances) != 6 {
		t.Fatalf("expected six database instances, got %d", len(instances))
	}
	byID := make(map[string]store.AppInstance, len(instances))
	for _, row := range instances {
		byID[row.ID] = row
	}
	for _, expected := range []store.AppInstance{first, second, third, wrongServer, wrongCluster, redis} {
		row := byID[expected.ID]
		var metadata struct {
			Pending   bool `json:"offlineUpgradePending"`
			LastCheck struct {
				Status string `json:"status"`
			} `json:"lastCheck"`
		}
		err := json.Unmarshal([]byte(row.Metadata), &metadata)
		shouldBePending := row.ID == first.ID || row.ID == second.ID || row.ID == third.ID
		if err != nil || metadata.Pending != shouldBePending || metadata.LastCheck.Status != expected.Status || row.Status != expected.Status {
			t.Fatalf("unexpected database instance %s: %+v, %v", expected.ID, row, err)
		}
		stored, err := db.GetAppInstance(expected.ID)
		if err != nil || stored.Metadata != expected.Metadata || !stored.UpdatedAt.Equal(expected.UpdatedAt) {
			t.Fatalf("database list modified the stored instance %s: %+v, %v", expected.ID, stored, err)
		}
	}
}
