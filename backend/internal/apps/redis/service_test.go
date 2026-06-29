package redis

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
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
	if v.ID == "" {
		v.ID = store.NewID("app")
	}
	v.CreatedAt = time.Now()
	v.UpdatedAt = v.CreatedAt
	f.instances = append(f.instances, v)
	return v, nil
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
	commands []string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	return nil
}

type fakeLogger struct{}

func (fakeLogger) Info(format string, args ...any)  {}
func (fakeLogger) Error(format string, args ...any) {}

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
	joinedCommands := strings.Join(remote.commands, "\n")
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
		ServerIDs: []string{
			"srv-1", "srv-2", "srv-3",
		},
		Parameters: map[string]any{"port": 6379, "sentinelPort": 26379, "masterName": "orders-primary", "sentinelMasterId": "srv-2", "password": "Oversea.123"},
	}, []store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 3 {
		t.Fatalf("expected three sentinel instances, got %d", len(s.instances))
	}
	if s.instances[0].Topology != "sentinel" || !strings.Contains(s.instances[0].Metadata, `"sentinelPort":26379`) {
		t.Fatalf("expected sentinel metadata: %+v", s.instances[0])
	}
	if !strings.Contains(s.instances[1].Metadata, `"role":"master"`) || !strings.Contains(s.instances[1].Metadata, `"masterHost":"10.0.0.2"`) || !strings.Contains(s.instances[1].Metadata, `"masterName":"orders-primary"`) {
		t.Fatalf("expected selected srv-2 to be sentinel master: %+v", s.instances)
	}
	joinedCommands := strings.Join(remote.commands, "\n")
	if !strings.Contains(joinedCommands, "AIFAR_REDIS_SENTINEL_CONFIGURE") {
		t.Fatalf("expected sentinel configure action: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, "sentinel monitor $MASTER_NAME $MASTER_HOST $MASTER_PORT $QUORUM") || !strings.Contains(joinedCommands, "MASTER_NAME='orders-primary'") {
		t.Fatalf("expected sentinel config to use monitor master name: %s", joinedCommands)
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
	joinedCommands := strings.Join(remote.commands, "\n")
	if strings.Count(joinedCommands, "redis-cli\" --cluster create") != 1 {
		t.Fatalf("expected one cluster bootstrap action: %s", joinedCommands)
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
	joinedCommands := strings.Join(remote.commands, "\n")
	if !strings.Contains(joinedCommands, `systemctl disable --now "$SERVICE_NAME"`) {
		t.Fatalf("expected remote command to stop redis service: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, `rm -rf "$INSTALL_ROOT"`) {
		t.Fatalf("expected remote command to remove redis install root: %s", joinedCommands)
	}
}
