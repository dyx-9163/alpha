package aifar

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type fakeStore struct {
	mu        sync.Mutex
	servers   map[string]store.Server
	instances []store.AppInstance
}

func (f *fakeStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.servers[id], nil
}

func (f *fakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	if v.ID == "" {
		v.ID = store.NewID("app")
		v.CreatedAt = now
	}
	if v.UpdatedAt.IsZero() {
		v.UpdatedAt = now
	}
	for idx, existing := range f.instances {
		if existing.ID == v.ID {
			if v.CreatedAt.IsZero() {
				v.CreatedAt = existing.CreatedAt
			}
			f.instances[idx] = v
			return v, nil
		}
	}
	f.instances = append(f.instances, v)
	return v, nil
}

func (f *fakeStore) DeleteAppInstance(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
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
	mu           sync.Mutex
	commands     []string
	uploads      []string
	statusStdout string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	if strings.Contains(command, "AIFAR_SERVICE_STATUS") && f.statusStdout != "" {
		return adapter.CommandResult{Stdout: f.statusStdout}, nil
	}
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

func (f *fakeRemote) joinedUploads() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.uploads, "\n")
}

type fakeLogger struct{}

func (fakeLogger) Info(format string, args ...any)  {}
func (fakeLogger) Error(format string, args ...any) {}

func TestServiceInstallsAIFARServiceFromDockerAppsBundle(t *testing.T) {
	root := createAIFARBundle(t)
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "srv-1",
		Language: "en",
		Parameters: map[string]any{
			"dbHost":     "10.0.0.20",
			"dbPort":     3306,
			"dbUser":     "root",
			"dbPassword": "secret-value",
			"webPort":    18080,
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one AIFAR instance, got %+v", s.instances)
	}
	instance := s.instances[0]
	if instance.App != "aifar" || instance.Version != "docker-apps" || instance.ServerID != "srv-1" || instance.Status != "installed" {
		t.Fatalf("unexpected instance: %+v", instance)
	}
	if strings.Contains(instance.Metadata, "secret-value") {
		t.Fatalf("metadata must not store database password: %s", instance.Metadata)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["endpoint"] != "10.0.0.10:18080" || metadata["networkName"] != defaultNetworkName {
		t.Fatalf("unexpected metadata: %s", instance.Metadata)
	}
	if !strings.Contains(remote.joinedUploads(), "aifar-service-bundle-") || !strings.Contains(remote.joinedCommands(), "install-aifar.sh") {
		t.Fatalf("expected bundle upload and install script run, uploads=%s commands=%s", remote.joinedUploads(), remote.joinedCommands())
	}
}

func TestSelectBundleIgnoresDockerSQLVersion(t *testing.T) {
	root := createAIFARBundle(t)
	resources := []store.Resource{
		{App: "aifar", Part: "backend", Version: "docker-sql", Path: filepath.Join(root, "docker-sql", "alpha_init.sql")},
		{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")},
	}
	bundle, err := SelectBundle(resources, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != "docker-apps" || filepath.Base(bundle.AppDir) != "docker-apps" {
		t.Fatalf("expected docker-apps bundle, got %+v", bundle)
	}
	if _, err := SelectBundle(resources, "docker-sql"); err == nil {
		t.Fatal("expected docker-sql to be rejected as an installable AIFAR version")
	}
}

func TestServiceChecksAIFARServiceAndUpdatesStatus(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"installRoot":"/aifar/apps/aifar"}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{statusStdout: strings.Join([]string{
		"status=degraded",
		"installRootExists=true",
		"totalContainers=3",
		"runningContainers=2",
		"unhealthyContainers=1",
		"containers=alpha-nacos:true:healthy,alpha-gateway:false:,alpha-web-vue3:true:unhealthy,",
	}, "\n")}
	service := NewService(s, remote)
	result, err := service.Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "degraded" {
		t.Fatalf("expected degraded status, got %+v", result)
	}
	if len(s.instances) != 1 || s.instances[0].Status != "degraded" || !strings.Contains(s.instances[0].Metadata, "alpha-gateway") {
		t.Fatalf("expected status to be persisted: %+v", s.instances)
	}
}

func createAIFARBundle(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	appDir := filepath.Join(root, "docker-apps")
	sqlDir := filepath.Join(root, "docker-sql")
	if err := os.MkdirAll(sqlDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sqlDir, "alpha_cloud_nacos.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sqlDir, "alpha_init.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("APP_NETWORK_NAME=alpha-network\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"nacos", "gateway", "web-vue3"} {
		dir := filepath.Join(appDir, service)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_CONTAINER_NAME=alpha-"+service+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
