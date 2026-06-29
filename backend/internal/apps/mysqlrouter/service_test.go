package mysqlrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type fakeStore struct {
	servers   map[string]store.Server
	instances []store.AppInstance
}

func (f *fakeStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	return f.servers[id], nil
}

func (f *fakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = store.NewID("app")
		v.CreatedAt = now
	}
	for idx, existing := range f.instances {
		if existing.ID == v.ID {
			if v.CreatedAt.IsZero() {
				v.CreatedAt = existing.CreatedAt
			}
			v.UpdatedAt = now
			f.instances[idx] = v
			return v, nil
		}
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	f.instances = append(f.instances, v)
	return v, nil
}

func (f *fakeStore) ListAppInstances() ([]store.AppInstance, error) {
	out := make([]store.AppInstance, len(f.instances))
	copy(out, f.instances)
	return out, nil
}

func (f *fakeStore) DeleteAppInstance(id string) error {
	next := f.instances[:0]
	for _, instance := range f.instances {
		if instance.ID != id {
			next = append(next, instance)
		}
	}
	f.instances = next
	return nil
}

type fakeRemote struct {
	mu       sync.Mutex
	commands []string
	uploads  []string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploads = append(f.uploads, filepath.Base(localPath)+"->"+remotePath)
	return nil
}

func (f *fakeRemote) joinedCommands() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

type fakeLogger struct{}

func (fakeLogger) Info(format string, args ...any)  {}
func (fakeLogger) Error(format string, args ...any) {}

func TestServiceInstallsRouterForExistingInnoDBCluster(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{
		servers: map[string]store.Server{
			"router-1": {ID: "router-1", Name: "router-1", Host: "10.0.0.9", DeployDir: "/aifar/apps"},
		},
		instances: mysqlClusterInstances("cluster-1", time.Now()),
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "8.0.36",
		Topology:        "router",
		Language:        "en",
		ServerID:        "router-1",
		DefaultPassword: "Oversea.123",
		Parameters: map[string]any{
			"clusterId":    "cluster-1",
			"basePort":     6446,
			"rootUser":     "root",
			"rootPassword": "Oversea.123",
		},
	}, []store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var routerInstances []store.AppInstance
	for _, instance := range s.instances {
		if instance.App == "mysql-router" {
			routerInstances = append(routerInstances, instance)
		}
	}
	if len(routerInstances) != 1 {
		t.Fatalf("expected one mysql router instance, got %d: %+v", len(routerInstances), s.instances)
	}
	instance := routerInstances[0]
	if instance.Status != "installed" || instance.Topology != "router" || instance.ServerID != "router-1" {
		t.Fatalf("expected installed router instance: %+v", instance)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["clusterId"] != "cluster-1" || int(metadata["basePort"].(float64)) != 6446 || metadata["endpoint"] != "10.0.0.9:6446" {
		t.Fatalf("unexpected router metadata: %s", instance.Metadata)
	}
	if strings.Contains(instance.Metadata, "Oversea.123") {
		t.Fatalf("metadata must not store mysql password: %s", instance.Metadata)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "install-mysql-router.sh") {
		t.Fatalf("expected mysql router install script to run: %s", joinedCommands)
	}
}

func TestModuleRejectsRouterWithoutInnoDBCluster(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"router-1": {ID: "router-1", Name: "router-1", Host: "10.0.0.9", DeployDir: "/aifar/apps"},
	}}
	module := NewModule(s, &fakeRemote{}, "Oversea.123")
	err := module.ValidateInstall(context.Background(), registry.InstallRequest{
		Version:  "8.0.36",
		Topology: "router",
		Language: "en",
		ServerID: "router-1",
		Parameters: map[string]any{
			"clusterId":    "missing",
			"rootPassword": "Oversea.123",
		},
	}, []store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}})
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected missing cluster validation error, got %v", err)
	}
}

func TestServiceDeletesRouterRemotelyBeforeRemovingInstance(t *testing.T) {
	instance := store.AppInstance{
		ID:       "router-app-1",
		App:      "mysql-router",
		Version:  "8.0.36",
		ServerID: "router-1",
		Status:   "installed",
		Topology: "router",
		Metadata: `{"basePort":6447}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"router-1": {ID: "router-1", Name: "router-1", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Delete(context.Background(), DeleteRequest{Instance: instance, Server: s.servers["router-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 0 {
		t.Fatalf("expected mysql router instance to be deleted: %+v", s.instances)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "AIFAR_MYSQL_ROUTER_UNINSTALL") || !strings.Contains(joinedCommands, `SERVICE_NAME="aifar-mysql-router-$BASE_PORT"`) {
		t.Fatalf("expected remote router uninstall script: %s", joinedCommands)
	}
}

func mysqlClusterInstances(clusterID string, createdAt time.Time) []store.AppInstance {
	out := make([]store.AppInstance, 0, 3)
	for idx, endpoint := range []string{"10.0.0.1:3306", "10.0.0.2:3306", "10.0.0.3:3306"} {
		metadata, _ := json.Marshal(map[string]any{
			"clusterId":              clusterID,
			"clusterName":            "aifarCluster",
			"endpoint":               endpoint,
			"currentPrimaryEndpoint": "10.0.0.2:3306",
			"port":                   3306,
			"rootUser":               "root",
			"topology":               "innodb-cluster",
		})
		out = append(out, store.AppInstance{
			ID:        fmt.Sprintf("mysql-app-%d", idx+1),
			App:       "mysql",
			Version:   "8.0.36",
			ServerID:  fmt.Sprintf("mysql-%d", idx+1),
			Status:    "installed",
			Topology:  "innodb-cluster",
			Metadata:  string(metadata),
			CreatedAt: createdAt,
			UpdatedAt: createdAt,
		})
	}
	return out
}
