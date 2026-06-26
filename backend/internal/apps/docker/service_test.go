package docker

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func (f *fakeStore) SaveServer(v store.Server) (store.Server, error) {
	f.servers[v.ID] = v
	return v, nil
}

func (f *fakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	if v.ID == "" {
		v.ID = store.NewID("app")
	}
	v.CreatedAt = time.Now()
	v.UpdatedAt = v.CreatedAt
	for idx, existing := range f.instances {
		if existing.ID == v.ID {
			if existing.CreatedAt.IsZero() {
				v.CreatedAt = time.Now()
			} else {
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
	commands      []string
	failUninstall bool
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	if f.failUninstall && strings.Contains(command, "AIFAR_DOCKER_UNINSTALL") {
		return adapter.CommandResult{Stderr: "remote uninstall failed"}, errors.New("remote uninstall failed")
	}
	if strings.Contains(command, "AIFAR_DOCKER_STATUS") {
		return adapter.CommandResult{Stdout: "status=missing\ndockerVersion=\ncomposeVersion=\nunitExists=false\ninstallRootExists=false\n"}, nil
	}
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	return nil
}

type fakeLogger struct{}

func (fakeLogger) Info(format string, args ...any)  {}
func (fakeLogger) Error(format string, args ...any) {}

func TestServiceInstallsDockerOnMultipleServers(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "aifar-docker-static-24.0.9-linux-x86_64.tar")
	if err := os.WriteFile(archive, []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Username: "root"},
		"srv-2": {ID: "srv-2", Name: "db-2", Host: "10.0.0.2", Username: "root"},
	}}
	service := NewService(s, &fakeRemote{})
	err := service.Install(context.Background(), InstallRequest{
		Version:   "24.0.9",
		ServerIDs: []string{"srv-1", "srv-2"},
		Language:  "en",
	}, []store.Resource{{App: "docker", Part: "backend", Version: "24.0.9", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 2 {
		t.Fatalf("expected two docker instances, got %d", len(s.instances))
	}
	if s.servers["srv-1"].DockerHost == "" || s.servers["srv-2"].DockerHost == "" {
		t.Fatalf("expected docker hosts to be recorded: %+v", s.servers)
	}
}

func TestServiceDeletesDockerRemotelyBeforeRemovingInstance(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "docker", Version: "24.0.9", ServerID: "srv-1", Status: "installed"}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Username: "root", DeployDir: "/aifar/apps", DockerHost: "ssh://root@10.0.0.1"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Delete(context.Background(), DeleteRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 0 {
		t.Fatalf("expected app instance to be deleted: %+v", s.instances)
	}
	if s.servers["srv-1"].DockerHost != "" {
		t.Fatalf("expected DockerHost to be cleared: %+v", s.servers["srv-1"])
	}
	joinedCommands := strings.Join(remote.commands, "\n")
	if !strings.Contains(joinedCommands, "systemctl disable --now docker") {
		t.Fatalf("expected remote command to stop docker: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, "rm -rf \"$INSTALL_ROOT\"") {
		t.Fatalf("expected remote command to remove install root: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, "AIFAR_DOCKER_STATUS") {
		t.Fatalf("expected remote status verification after delete: %s", joinedCommands)
	}
}

func TestModuleDeleteRequiresServerPasswordConfirmation(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "docker", Version: "24.0.9", ServerID: "srv-1", Status: "installed"}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Username: "root", DeployDir: "/aifar/apps", DockerHost: "ssh://root@10.0.0.1"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	module := NewModule(s, remote)
	err := module.Delete(context.Background(), registry.DeleteRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
	}, registry.RunContext{Log: fakeLogger{}})
	if err == nil {
		t.Fatal("expected missing server password confirmation to block delete")
	}
	if len(remote.commands) != 0 {
		t.Fatalf("delete must not reach remote without password confirmation: %v", remote.commands)
	}
	if len(s.instances) != 1 {
		t.Fatalf("instance must remain when delete is not confirmed: %+v", s.instances)
	}
}

func TestServiceKeepsInstanceWhenRemoteDockerDeleteFails(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "docker", Version: "24.0.9", ServerID: "srv-1", Status: "installed"}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Username: "root", DeployDir: "/aifar/apps", DockerHost: "ssh://root@10.0.0.1"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{failUninstall: true}
	service := NewService(s, remote)
	err := service.Delete(context.Background(), DeleteRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected remote uninstall failure")
	}
	if len(s.instances) != 1 {
		t.Fatalf("instance must remain when remote delete fails: %+v", s.instances)
	}
	if s.servers["srv-1"].DockerHost == "" {
		t.Fatalf("DockerHost must remain when remote delete fails: %+v", s.servers["srv-1"])
	}
}

func TestServiceChecksDockerInstanceAndUpdatesStatus(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "docker", Version: "24.0.9", ServerID: "srv-1", Status: "installed"}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.1", Username: "root", DeployDir: "/aifar/apps", DockerHost: "ssh://root@10.0.0.1"},
		},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	result, err := service.Check(context.Background(), CheckRequest{
		Instance: instance,
		Server:   s.servers["srv-1"],
		Language: "en",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "missing" {
		t.Fatalf("expected missing status, got %+v", result)
	}
	if len(s.instances) != 1 || s.instances[0].Status != "missing" {
		t.Fatalf("expected existing instance status to be updated: %+v", s.instances)
	}
}
