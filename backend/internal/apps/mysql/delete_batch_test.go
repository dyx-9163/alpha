package mysql

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

// Production break caught: after the first member was deleted, the next
// member repeated the three-member maintenance check and made the batch
// permanently non-resumable.
func TestBatchDeletePreflightAllowsAllRemainingClusterMembers(t *testing.T) {
	db := openMaintenanceTestStore(t)
	servers, instances := saveDeleteBatchCluster(t, db)
	if err := db.DeleteAppInstance(instances[0].ID); err != nil {
		t.Fatal(err)
	}

	module := NewModule(db, &fakeRemote{})
	requests := deleteBatchRequests(servers[1:], instances[1:])
	if err := module.PreflightDeleteBatch(context.Background(), requests); err != nil {
		t.Fatalf("remaining complete selection should be resumable: %v", err)
	}
	scope := registry.NewDeleteBatchScope([]string{instances[1].ID, instances[2].ID})
	for index := range requests {
		requests[index].Batch = scope
		if err := module.Delete(context.Background(), requests[index], registry.RunContext{Log: fakeLogger{}}); err != nil {
			t.Fatalf("delete remaining member %d: %v", index, err)
		}
	}
	for _, instance := range instances[1:] {
		if _, err := db.GetAppInstance(instance.ID); !errors.Is(err, sql.ErrNoRows) {
			t.Fatalf("instance %s still exists or returned unexpected error: %v", instance.ID, err)
		}
	}
}

// Production break caught: relaxing the three-member check without requiring
// selection closure could delete one node while silently leaving another.
func TestBatchDeletePreflightRejectsMissingRemainingMember(t *testing.T) {
	db := openMaintenanceTestStore(t)
	servers, instances := saveDeleteBatchCluster(t, db)
	if err := db.DeleteAppInstance(instances[0].ID); err != nil {
		t.Fatal(err)
	}
	module := NewModule(db, &fakeRemote{})

	err := module.PreflightDeleteBatch(context.Background(), deleteBatchRequests(servers[1:2], instances[1:2]))
	if stableMySQLCode(err) != MySQLBackupClusterUnhealthy {
		t.Fatalf("code=%q err=%v", stableMySQLCode(err), err)
	}
}

// Production break caught: a resumable delete must not turn into a bypass for
// incomplete restore or reconciliation safety markers.
func TestBatchDeletePreflightRejectsRemainingMemberSafetyMarkers(t *testing.T) {
	tests := []struct {
		name     string
		mark     func(*testing.T, *store.Store, store.AppInstance)
		wantCode string
	}{
		{
			name: "maintenance",
			mark: func(t *testing.T, db *store.Store, instance store.AppInstance) {
				t.Helper()
				marker := maintenanceTestMarker("cluster", clusterIDFromInstance(instance), "schema_mutation_started")
				metadata := map[string]any{}
				if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
					t.Fatal(err)
				}
				metadata["mysqlMaintenance"] = marker
				raw, err := json.Marshal(metadata)
				if err != nil {
					t.Fatal(err)
				}
				instance.Metadata = string(raw)
				if _, err := db.SaveAppInstance(instance); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: MySQLMaintenanceStateInvalid,
		},
		{
			name: "reconciliation",
			mark: func(t *testing.T, db *store.Store, instance store.AppInstance) {
				t.Helper()
				instance.Metadata = fmt.Sprintf(`{"clusterId":%q,"port":3306,"mysqlReconciliation":{"version":1,"kind":"local_infile","originalValue":"OFF","recordedAt":"2026-08-02T00:00:00Z","taskId":"tsk_abcdefabcdefabcdefabcdef"}}`, clusterIDFromInstance(instance))
				if _, err := db.SaveAppInstance(instance); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: MySQLReconciliationRequired,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := openMaintenanceTestStore(t)
			servers, instances := saveDeleteBatchCluster(t, db)
			if err := db.DeleteAppInstance(instances[0].ID); err != nil {
				t.Fatal(err)
			}
			test.mark(t, db, instances[1])
			err := NewModule(db, &fakeRemote{}).PreflightDeleteBatch(context.Background(), deleteBatchRequests(servers[1:], instances[1:]))
			if stableMySQLCode(err) != test.wantCode {
				t.Fatalf("code=%q want=%q err=%v", stableMySQLCode(err), test.wantCode, err)
			}
		})
	}
}

func saveDeleteBatchCluster(t *testing.T, db *store.Store) ([]store.Server, []store.AppInstance) {
	t.Helper()
	const clusterID = "cluster_1234567890abcdef12345678"
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "aifarCluster", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	servers := make([]store.Server, 0, 3)
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: fmt.Sprintf("mysql-delete-%d", index), Host: fmt.Sprintf("192.0.2.%d", index+1), Username: "root", Password: "server-pass"})
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
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		servers = append(servers, server)
		instances = append(instances, instance)
	}
	return servers, instances
}

func deleteBatchRequests(servers []store.Server, instances []store.AppInstance) []registry.DeleteRequest {
	requests := make([]registry.DeleteRequest, 0, len(instances))
	for index := range instances {
		requests = append(requests, registry.DeleteRequest{
			Instance: instances[index], Server: servers[index], Language: "en",
			Parameters: map[string]any{registry.DeleteParamConfirmedWithServerPassword: true},
		})
	}
	return requests
}

func stableMySQLCode(err error) string {
	var stable interface{ StableCode() string }
	if errors.As(err, &stable) {
		return stable.StableCode()
	}
	return ""
}
