package nacos

import (
	"encoding/json"
	"testing"

	"aifar-deployment/backend/internal/store"
)

type dependencyStore struct {
	servers   map[string]store.Server
	instances []store.AppInstance
}

func (s *dependencyStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	return s.servers[id], nil
}

func (s *dependencyStore) ListAppInstances() ([]store.AppInstance, error) {
	out := make([]store.AppInstance, len(s.instances))
	copy(out, s.instances)
	return out, nil
}

func (s *dependencyStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	for i := range s.instances {
		if s.instances[i].ID == v.ID {
			s.instances[i] = v
			return v, nil
		}
	}
	s.instances = append(s.instances, v)
	return v, nil
}

func (s *dependencyStore) DeleteAppInstance(id string) error {
	return nil
}

func TestResolveNacosDatabaseDependencyUsesMySQLRouter(t *testing.T) {
	service := NewService(&dependencyStore{
		servers: map[string]store.Server{
			"mysql-1":  {ID: "mysql-1", Host: "10.0.0.31"},
			"router-1": {ID: "router-1", Host: "10.0.0.30"},
		},
		instances: []store.AppInstance{
			{
				ID:       "mysql-node-1",
				App:      "mysql",
				ServerID: "mysql-1",
				Topology: "innodb-cluster",
				Metadata: nacosTestMetadata(t, map[string]any{
					"clusterId": "mysql-cluster-1",
					"endpoint":  "10.0.0.31:3306",
				}),
			},
			{
				ID:       "mysql-router-1",
				App:      "mysql-router",
				ServerID: "router-1",
				Topology: "router",
				Metadata: nacosTestMetadata(t, map[string]any{
					"clusterId": "mysql-cluster-1",
					"basePort":  6446,
					"endpoint":  "10.0.0.30:6446",
				}),
			},
		},
	}, nil)
	options := nacosOptions(map[string]any{
		"dbSource":     "existing",
		"dbInstanceId": "mysql-node-1",
		"dbPassword":   "Oversea.123",
	}, "cluster")
	resolved, err := service.resolveInstallOptions(options)
	if err != nil {
		t.Fatalf("resolveInstallOptions returned error: %v", err)
	}
	if resolved.Database.Host != "10.0.0.30" || resolved.Database.Port != 6446 {
		t.Fatalf("expected MySQL Router endpoint, got %+v", resolved.Database)
	}
	if resolved.Database.Name != "aifar_nacos" || resolved.Database.User != "root" {
		t.Fatalf("expected AIFAR Nacos database defaults, got %+v", resolved.Database)
	}
	if err := resolved.Validate(); err != nil {
		t.Fatalf("resolved options should validate: %v", err)
	}
}

func TestMarkInstanceStatusRunningClearsInstallFailureMetadata(t *testing.T) {
	instance := store.AppInstance{
		ID:     "nacos-1",
		App:    "nacos",
		Status: "failed",
		Metadata: nacosTestMetadata(t, map[string]any{
			"installFailed": true,
			"failedAt":      "2026-07-01T01:02:03Z",
			"taskId":        "task-1",
			"error":         "old failure",
			"port":          8848,
		}),
	}
	s := &dependencyStore{instances: []store.AppInstance{instance}}
	service := NewService(s, nil)
	if err := service.markInstanceStatus(instance, "running", map[string]any{"checkedAt": "2026-07-02T01:02:03Z"}); err != nil {
		t.Fatalf("markInstanceStatus returned error: %v", err)
	}
	if len(s.instances) != 1 || s.instances[0].Status != "running" {
		t.Fatalf("expected saved running instance, got %+v", s.instances)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(s.instances[0].Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"installFailed", "failedAt", "taskId", "error"} {
		if _, ok := metadata[key]; ok {
			t.Fatalf("expected %s to be cleared after successful check, got %s", key, s.instances[0].Metadata)
		}
	}
	if _, ok := metadata["lastCheck"].(map[string]any); !ok {
		t.Fatalf("expected lastCheck metadata, got %s", s.instances[0].Metadata)
	}
}

func nacosTestMetadata(t *testing.T, value map[string]any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
