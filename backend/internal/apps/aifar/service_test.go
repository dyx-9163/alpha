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

func (f *fakeStore) ListAppInstances() ([]store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.AppInstance, len(f.instances))
	copy(out, f.instances)
	return out, nil
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
	mu            sync.Mutex
	commands      []string
	uploads       []string
	installScript string
	statusStdout  string
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
	if strings.HasSuffix(remotePath, "/install-aifar.sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.installScript = string(content)
	}
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

func TestOptionsDefaultsUseRequestedAIFARValues(t *testing.T) {
	opts := optionsFromParameters(nil)
	if opts.Timezone != "system" || opts.NacosWebPort != 9849 || opts.NacosNamespace != "prod" {
		t.Fatalf("unexpected defaults: %+v", opts)
	}
	if got := installRootFromDeployDir("/aifar/apps"); got != "/aifar/apps/admin" {
		t.Fatalf("expected AIFAR install root /aifar/apps/admin, got %s", got)
	}
}

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
	if !strings.Contains(remote.installScript, `open_firewall_ports "$GATEWAY_PORT" "$WEB_VUE3_PORT" "$NACOS_PORT_WEB" "$NACOS_PORT_API"`) ||
		!strings.Contains(remote.installScript, `allow_selinux_ports http_port_t "$GATEWAY_PORT" "$WEB_VUE3_PORT" "$NACOS_PORT_WEB" "$NACOS_PORT_API"`) {
		t.Fatalf("AIFAR install script should open firewall and SELinux rules for service ports:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "prepare_compose_networks") || !strings.Contains(remote.installScript, "external: true") {
		t.Fatalf("AIFAR install script should mark shared Docker network as external:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "resolve_system_timezone") || !strings.Contains(remote.installScript, "timedatectl show -p Timezone") {
		t.Fatalf("AIFAR install script should resolve system timezone:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "patch_nacos_server_port") || !strings.Contains(remote.installScript, "patch_nacos_sql_namespace") {
		t.Fatalf("AIFAR install script should patch Nacos defaults from install options:\n%s", remote.installScript)
	}
}

func TestServiceResolvesManagedDatabaseAndRedisInstances(t *testing.T) {
	root := createAIFARBundle(t)
	s := &fakeStore{
		servers: map[string]store.Server{
			"app-1":    {ID: "app-1", Name: "app-1", Host: "10.0.0.10", DeployDir: "/aifar/apps"},
			"mysql-1":  {ID: "mysql-1", Name: "mysql-1", Host: "10.0.0.31", DeployDir: "/aifar/apps"},
			"router-1": {ID: "router-1", Name: "router-1", Host: "10.0.0.30", DeployDir: "/aifar/apps"},
			"redis-1":  {ID: "redis-1", Name: "redis-1", Host: "10.0.0.41", DeployDir: "/aifar/apps"},
			"redis-2":  {ID: "redis-2", Name: "redis-2", Host: "10.0.0.42", DeployDir: "/aifar/apps"},
		},
		instances: []store.AppInstance{
			{
				ID:       "mysql-node-1",
				App:      "mysql",
				Version:  "8.0.36",
				ServerID: "mysql-1",
				Status:   "running",
				Topology: "innodb-cluster",
				Metadata: mustMetadata(t, map[string]any{
					"clusterId": "mysql-cluster-1",
					"endpoint":  "10.0.0.31:3306",
					"topology":  "innodb-cluster",
				}),
			},
			{
				ID:       "mysql-router-1",
				App:      "mysql-router",
				Version:  "8.0.36",
				ServerID: "router-1",
				Status:   "running",
				Topology: "router",
				Metadata: mustMetadata(t, map[string]any{
					"clusterId": "mysql-cluster-1",
					"basePort":  6446,
					"endpoint":  "10.0.0.30:6446",
					"topology":  "router",
				}),
			},
			{
				ID:       "redis-node-1",
				App:      "redis",
				Version:  "7.2.14",
				ServerID: "redis-1",
				Status:   "running",
				Topology: "sentinel",
				Metadata: mustMetadata(t, map[string]any{
					"clusterId":    "redis-sentinel-1",
					"masterName":   "aifar-master",
					"sentinel":     true,
					"sentinelPort": 26379,
					"topology":     "sentinel",
				}),
			},
			{
				ID:       "redis-node-2",
				App:      "redis",
				Version:  "7.2.14",
				ServerID: "redis-2",
				Status:   "running",
				Topology: "sentinel",
				Metadata: mustMetadata(t, map[string]any{
					"clusterId":    "redis-sentinel-1",
					"masterName":   "aifar-master",
					"sentinel":     true,
					"sentinelPort": 26379,
					"topology":     "sentinel",
				}),
			},
		},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "latest",
		ServerID: "app-1",
		Language: "en",
		Parameters: map[string]any{
			"dbSource":        "existing",
			"dbInstanceId":    "mysql-node-1",
			"dbNameNacos":     "aifar_cloud_nacos",
			"dbUser":          "root",
			"dbPassword":      "secret-value",
			"redisSource":     "existing",
			"redisInstanceId": "redis-node-1",
			"redisPassword":   "redis-secret",
		},
	}, []store.Resource{{App: "aifar", Part: "backend", Version: "docker-apps", Path: filepath.Join(root, "docker-apps", ".env")}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var instance store.AppInstance
	for _, candidate := range s.instances {
		if candidate.App == "aifar" {
			instance = candidate
			break
		}
	}
	if instance.ID == "" {
		t.Fatalf("expected AIFAR instance, got %+v", s.instances)
	}
	if strings.Contains(instance.Metadata, "secret-value") || strings.Contains(instance.Metadata, "redis-secret") {
		t.Fatalf("metadata must not store database or redis passwords: %s", instance.Metadata)
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["dbHost"] != "10.0.0.30" || int(metadata["dbPort"].(float64)) != 6446 {
		t.Fatalf("expected MySQL Router endpoint to be used, got %s", instance.Metadata)
	}
	if metadata["redisMode"] != "sentinel" || metadata["redisHost"] != "10.0.0.41" || int(metadata["redisPort"].(float64)) != 26379 {
		t.Fatalf("expected Redis Sentinel endpoint to be used, got %s", instance.Metadata)
	}
	for _, want := range []string{
		"DB_HOST='10.0.0.30'",
		"DB_PORT='6446'",
		"REDIS_MODE='sentinel'",
		"REDIS_SENTINEL_MASTER='aifar-master'",
		"REDIS_SENTINEL_NODES='10.0.0.41:26379,10.0.0.42:26379'",
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("install script should contain %q:\n%s", want, remote.installScript)
		}
	}
}

func TestSelectBundleIgnoresDockerSQLVersion(t *testing.T) {
	root := createAIFARBundle(t)
	resources := []store.Resource{
		{App: "aifar", Part: "backend", Version: "docker-sql", Path: filepath.Join(root, "docker-sql", "aifar_init.sql")},
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

func mustMetadata(t *testing.T, value map[string]any) string {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestServiceChecksAIFARServiceAndUpdatesStatus(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"installRoot":"/aifar/apps/admin"}`,
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
		"containers=aifar-nacos:true:healthy,aifar-gateway:false:,aifar-web-vue3:true:unhealthy,",
	}, "\n")}
	service := NewService(s, remote)
	result, err := service.Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "degraded" {
		t.Fatalf("expected degraded status, got %+v", result)
	}
	if len(s.instances) != 1 || s.instances[0].Status != "degraded" || !strings.Contains(s.instances[0].Metadata, "aifar-gateway") {
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
	if err := os.WriteFile(filepath.Join(sqlDir, "aifar_cloud_nacos.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sqlDir, "aifar_init.sql"), []byte("select 1;"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(appDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(appDir, ".env"), []byte("APP_NETWORK_NAME=aifar-network\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"nacos", "gateway", "web-vue3"} {
		dir := filepath.Join(appDir, service)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_CONTAINER_NAME=aifar-"+service+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "docker-compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}
