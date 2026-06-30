package redis

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
	if v.ID == "" {
		v.ID = store.NewID("app")
	}
	v.CreatedAt = time.Now()
	v.UpdatedAt = v.CreatedAt
	for idx := range f.instances {
		if f.instances[idx].ID == v.ID {
			if f.instances[idx].CreatedAt.IsZero() {
				f.instances[idx].CreatedAt = v.CreatedAt
			}
			v.CreatedAt = f.instances[idx].CreatedAt
			f.instances[idx] = v
			return v, nil
		}
	}
	f.instances = append(f.instances, v)
	return v, nil
}

func (f *fakeStore) ListAppInstances() ([]store.AppInstance, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]store.AppInstance, len(f.instances))
	copy(out, f.instances)
	return out, nil
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
	mu             sync.Mutex
	commands       []string
	responses      map[string]adapter.CommandResult
	failAll        bool
	blockInstall   bool
	installStarted chan string
	releaseInstall chan struct{}
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if f.blockInstall && strings.Contains(command, "install-redis.sh") {
		f.installStarted <- server.ID
		select {
		case <-ctx.Done():
			return adapter.CommandResult{}, ctx.Err()
		case <-f.releaseInstall:
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, command)
	if f.failAll {
		return adapter.CommandResult{}, fmt.Errorf("remote offline")
	}
	for needle, result := range f.responses {
		if strings.Contains(command, needle) {
			return result, nil
		}
	}
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
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

func metadataForTest(t *testing.T, instance store.AppInstance) map[string]any {
	t.Helper()
	var metadata map[string]any
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	return metadata
}

func metadataListContains(value any, want string) bool {
	items, ok := value.([]any)
	if !ok {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(fmt.Sprint(item)) == want {
			return true
		}
	}
	return false
}

func TestServiceInstallsStandaloneRedisAndRecordsInstalledInstance(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Username: "root", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:    "7.2.14",
		ServerID:   "srv-1",
		Topology:   "standalone",
		Language:   "en",
		Parameters: map[string]any{"port": 6380, "password": "Oversea.123"},
	}, []store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one redis instance, got %d", len(s.instances))
	}
	if s.instances[0].Status != "installed" || s.instances[0].Topology != "standalone" {
		t.Fatalf("expected installed standalone instance: %+v", s.instances[0])
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "install-redis.sh") {
		t.Fatalf("expected redis install script to run: %s", joinedCommands)
	}
}

func TestServiceInstallsRedisSentinelAndRecordsEachNode(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "redis-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "redis-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "redis-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "7.2.14",
		Topology: "sentinel",
		Language: "en",
		Parameters: map[string]any{
			"port":              6379,
			"sentinelPort":      26379,
			"masterName":        "orders-primary",
			"sentinelMasterId":  "srv-2",
			"replicaServerIds":  []string{"srv-1"},
			"sentinelServerIds": []string{"srv-2", "srv-1", "srv-3"},
			"password":          "Oversea.123",
		},
	}, []store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 3 {
		t.Fatalf("expected three sentinel instances, got %d", len(s.instances))
	}
	instancesByServer := map[string]store.AppInstance{}
	for _, instance := range s.instances {
		instancesByServer[instance.ServerID] = instance
	}
	if instancesByServer["srv-2"].Topology != "sentinel" || !strings.Contains(instancesByServer["srv-2"].Metadata, `"sentinelPort":26379`) {
		t.Fatalf("expected sentinel metadata: %+v", instancesByServer["srv-2"])
	}
	if !strings.Contains(instancesByServer["srv-2"].Metadata, `"role":"master"`) || !strings.Contains(instancesByServer["srv-2"].Metadata, `"masterHost":"10.0.0.2"`) || !strings.Contains(instancesByServer["srv-2"].Metadata, `"masterName":"orders-primary"`) {
		t.Fatalf("expected selected srv-2 to be sentinel master: %+v", s.instances)
	}
	if !strings.Contains(instancesByServer["srv-1"].Metadata, `"role":"replica"`) {
		t.Fatalf("expected srv-1 to be redis replica: %+v", s.instances)
	}
	if !strings.Contains(instancesByServer["srv-3"].Metadata, `"role":"sentinel"`) || !strings.Contains(instancesByServer["srv-3"].Metadata, `"sentinel":true`) {
		t.Fatalf("expected srv-3 to be sentinel-only node: %+v", s.instances)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "AIFAR_REDIS_SENTINEL_CONFIGURE") {
		t.Fatalf("expected sentinel configure action: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, "sentinel monitor $MASTER_NAME $MASTER_HOST $MASTER_PORT $QUORUM") || !strings.Contains(joinedCommands, "MASTER_NAME='orders-primary'") {
		t.Fatalf("expected sentinel config to use monitor master name: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, "ROLE='sentinel'") {
		t.Fatalf("expected sentinel-only role to be configured: %s", joinedCommands)
	}
}

func TestRedisSentinelMasterNameDefaultsAndValidates(t *testing.T) {
	defaultName, err := redisSentinelMasterName(nil, "invalid")
	if err != nil {
		t.Fatal(err)
	}
	if defaultName != "aifar-master" {
		t.Fatalf("expected default master name, got %q", defaultName)
	}
	if _, err := redisSentinelMasterName(map[string]any{"masterName": "bad name"}, "invalid"); err == nil {
		t.Fatal("expected invalid sentinel master name to fail")
	}
}

func TestServiceChecksRedisSentinelAndUpdatesCurrentMasterRoles(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "redis-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "redis-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "redis-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:  "7.2.14",
		Topology: "sentinel",
		Language: "en",
		Parameters: map[string]any{
			"port":              6379,
			"sentinelPort":      26379,
			"masterName":        "orders-primary",
			"sentinelMasterId":  "srv-2",
			"replicaServerIds":  []string{"srv-1"},
			"sentinelServerIds": []string{"srv-2", "srv-1", "srv-3"},
			"password":          "Oversea.123",
		},
	}, []store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	remote.responses = map[string]adapter.CommandResult{
		"SENTINEL get-master-addr-by-name": {Stdout: "10.0.0.1\n6379\n"},
		"SENTINEL replicas": {Stdout: strings.Join([]string{
			"name", "10.0.0.2:6379",
			"ip", "10.0.0.2",
			"port", "6379",
			"flags", "slave",
		}, "\n")},
		"SENTINEL sentinels": {Stdout: strings.Join([]string{
			"name", "redis-1",
			"ip", "10.0.0.1",
			"port", "26379",
			"flags", "sentinel",
			"name", "redis-3",
			"ip", "10.0.0.3",
			"port", "26379",
			"flags", "sentinel",
		}, "\n")},
	}
	instancesByServer := map[string]store.AppInstance{}
	for _, instance := range s.instances {
		instancesByServer[instance.ServerID] = instance
	}
	if _, err := service.Check(context.Background(), CheckRequest{
		Instance:        instancesByServer["srv-2"],
		Server:          s.servers["srv-2"],
		Language:        "en",
		DefaultPassword: "Oversea.123",
	}, fakeLogger{}, nil); err != nil {
		t.Fatal(err)
	}
	instancesByServer = map[string]store.AppInstance{}
	for _, instance := range s.instances {
		instancesByServer[instance.ServerID] = instance
	}
	if got := metadataForTest(t, instancesByServer["srv-1"])["role"]; got != "master" {
		t.Fatalf("expected srv-1 to become master, got %v", got)
	}
	if got := metadataForTest(t, instancesByServer["srv-2"])["role"]; got != "replica" {
		t.Fatalf("expected srv-2 to become replica, got %v", got)
	}
	if got := metadataForTest(t, instancesByServer["srv-3"])["role"]; got != "sentinel" {
		t.Fatalf("expected srv-3 to remain sentinel, got %v", got)
	}
	if instancesByServer["srv-2"].Status != "running" {
		t.Fatalf("expected checked instance to be running, got %s", instancesByServer["srv-2"].Status)
	}
	if instancesByServer["srv-1"].Status != "installed" {
		t.Fatalf("expected unchecked data instance status to stay installed, got %s", instancesByServer["srv-1"].Status)
	}
	if instancesByServer["srv-3"].Status != "installed" {
		t.Fatalf("expected unchecked sentinel instance status to stay installed, got %s", instancesByServer["srv-3"].Status)
	}
	if got := metadataForTest(t, instancesByServer["srv-1"])["currentMasterEndpoint"]; got != "10.0.0.1:6379" {
		t.Fatalf("expected current master endpoint to be recorded, got %v", got)
	}
	metadata := metadataForTest(t, instancesByServer["srv-2"])
	if !metadataListContains(metadata["replicaEndpoints"], "10.0.0.2:6379") {
		t.Fatalf("expected detected replica endpoints to be recorded, got %v", metadata["replicaEndpoints"])
	}
	for _, endpoint := range []string{"10.0.0.1:26379", "10.0.0.2:26379", "10.0.0.3:26379"} {
		if !metadataListContains(metadata["sentinelEndpoints"], endpoint) {
			t.Fatalf("expected detected sentinel endpoint %s to be recorded, got %v", endpoint, metadata["sentinelEndpoints"])
		}
	}
	if !strings.Contains(remote.joinedCommands(), "SENTINEL get-master-addr-by-name") {
		t.Fatal("expected check to query Redis Sentinel current master")
	}
	if !strings.Contains(remote.joinedCommands(), "SENTINEL replicas") || !strings.Contains(remote.joinedCommands(), "SENTINEL sentinels") {
		t.Fatal("expected check to query Redis Sentinel replicas and sentinel peers")
	}
}

func TestServiceCheckFailureClearsStaleRedisSentinelTopology(t *testing.T) {
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "redis-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
	}}
	metadata, _ := json.Marshal(map[string]any{
		"clusterId":             "redis_sentinel_test",
		"port":                  6379,
		"sentinelPort":          26379,
		"masterName":            "orders-primary",
		"role":                  "master",
		"sentinel":              true,
		"topology":              "sentinel",
		"currentMasterEndpoint": "10.0.0.1:6379",
		"replicaEndpoints":      []string{"10.0.0.2:6379"},
		"sentinelEndpoints":     []string{"10.0.0.1:26379", "10.0.0.2:26379"},
		"masterDetectedAt":      time.Now().UTC().Format(time.RFC3339),
	})
	instance, err := s.SaveAppInstance(store.AppInstance{
		App:      "redis",
		Version:  "7.2.14",
		ServerID: "srv-1",
		Status:   "running",
		Topology: "sentinel",
		Metadata: string(metadata),
	})
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(s, &fakeRemote{failAll: true})
	if _, err := service.Check(context.Background(), CheckRequest{
		Instance:        instance,
		Server:          s.servers["srv-1"],
		Language:        "en",
		DefaultPassword: "Oversea.123",
	}, fakeLogger{}, nil); err == nil {
		t.Fatal("expected check to fail when remote is offline")
	}
	updated := s.instances[0]
	if updated.Status != "failed" {
		t.Fatalf("expected failed status, got %s", updated.Status)
	}
	updatedMetadata := metadataForTest(t, updated)
	for _, key := range []string{"currentMasterEndpoint", "replicaEndpoints", "sentinelEndpoints", "masterDetectedAt"} {
		if _, ok := updatedMetadata[key]; ok {
			t.Fatalf("expected stale %s to be cleared, got metadata %+v", key, updatedMetadata)
		}
	}
}

func TestServiceInstallsRedisClusterAndBootstrapsOnce(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "redis-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "redis-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "redis-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:    "7.2.14",
		Topology:   "cluster",
		Language:   "en",
		ServerIDs:  []string{"srv-1", "srv-2", "srv-3"},
		Parameters: map[string]any{"port": 6379, "password": "Oversea.123"},
	}, []store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 3 {
		t.Fatalf("expected three cluster instances, got %d", len(s.instances))
	}
	joinedCommands := remote.joinedCommands()
	if strings.Count(joinedCommands, "redis-cli\" --cluster create") != 1 {
		t.Fatalf("expected one cluster bootstrap action: %s", joinedCommands)
	}
}

func TestServiceUsesConcurrencyForRedisClusterInstalls(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "redis-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "redis-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "redis-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{
		blockInstall:   true,
		installStarted: make(chan string, 3),
		releaseInstall: make(chan struct{}),
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(remote.releaseInstall) })
	service := NewService(s, remote)
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Install(context.Background(), InstallRequest{
			Version:     "7.2.14",
			Topology:    "cluster",
			Language:    "en",
			ServerIDs:   []string{"srv-1", "srv-2", "srv-3"},
			Concurrency: 2,
			Parameters:  map[string]any{"port": 6379, "password": "Oversea.123"},
		}, []store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, fakeLogger{}, nil)
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case target := <-remote.installStarted:
			seen[target] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("expected two Redis installs to start, got %v", seen)
		}
	}
	select {
	case target := <-remote.installStarted:
		t.Fatalf("third Redis install %s started before concurrency slot was released", target)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(remote.releaseInstall) })
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Redis cluster install did not finish after releasing concurrent installs")
	}
}

func TestRedisPasswordFallsBackToDefaultPassword(t *testing.T) {
	if got := redisPassword(nil, "Oversea.123"); got != "Oversea.123" {
		t.Fatalf("expected default redis password, got %q", got)
	}
	if got := redisPassword(map[string]any{"password": "custom"}, "Oversea.123"); got != "custom" {
		t.Fatalf("expected explicit redis password, got %q", got)
	}
}

func TestServiceDeletesRedisRemotelyBeforeRemovingInstance(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "redis", Version: "7.2.14", ServerID: "srv-1", Status: "installed", Metadata: `{"port":6380}`}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "db-1", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Delete(context.Background(), DeleteRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 0 {
		t.Fatalf("expected redis instance to be deleted: %+v", s.instances)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, `systemctl disable --now "$SERVICE_NAME"`) {
		t.Fatalf("expected remote command to stop redis service: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, `INSTALL_ROOT='/aifar/apps/redis'`) || !strings.Contains(joinedCommands, `rm -rf "$ROOT"`) {
		t.Fatalf("expected remote command to remove redis install root: %s", joinedCommands)
	}
}
