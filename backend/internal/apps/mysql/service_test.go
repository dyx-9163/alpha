package mysql

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

func TestServiceInstallsStandaloneMySQLAndRecordsInstalledInstance(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "db-1", Host: "10.0.0.4", Username: "root", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "8.0.36",
		ServerID:        "srv-1",
		Topology:        "single",
		Language:        "en",
		DefaultPassword: "Oversea.123",
		Parameters:      map[string]any{"port": 3307, "rootUser": "root"},
	}, []store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one mysql instance, got %d", len(s.instances))
	}
	instance := s.instances[0]
	if instance.Status != "installed" || instance.Topology != "standalone" || instance.App != "mysql" {
		t.Fatalf("expected installed standalone mysql instance: %+v", instance)
	}
	if strings.Contains(instance.Metadata, "Oversea.123") {
		t.Fatalf("metadata must not store root password: %s", instance.Metadata)
	}
	if !strings.Contains(instance.Metadata, `"port":3307`) || !strings.Contains(instance.Metadata, `"endpoint":"10.0.0.4:3307"`) {
		t.Fatalf("metadata should include endpoint and port: %s", instance.Metadata)
	}
	joinedCommands := strings.Join(remote.commands, "\n")
	if !strings.Contains(joinedCommands, "install-mysql.sh") {
		t.Fatalf("expected mysql install script to run: %s", joinedCommands)
	}
}

func TestServiceInstallsInnoDBClusterAndRecordsEachNode(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "mysql-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "mysql-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "mysql-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "8.0.36",
		Topology:        "innodb-cluster",
		Language:        "en",
		DefaultPassword: "Oversea.123",
		ServerIDs:       []string{"srv-1", "srv-2", "srv-3"},
		Parameters:      map[string]any{"port": 3306, "rootUser": "root", "clusterName": "aifarCluster"},
	}, []store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 3 {
		t.Fatalf("expected three mysql cluster instances, got %d", len(s.instances))
	}
	if s.instances[0].Topology != "innodb-cluster" || strings.Contains(s.instances[0].Metadata, "Oversea.123") {
		t.Fatalf("expected safe cluster metadata: %+v", s.instances[0])
	}
	joinedCommands := strings.Join(remote.commands, "\n")
	if strings.Count(joinedCommands, "mysqlsh --js --file") != 1 {
		t.Fatalf("expected one innodb cluster bootstrap action: %s", joinedCommands)
	}
}

func TestMySQLPasswordFallsBackToDefaultPassword(t *testing.T) {
	if got := passwordParam(nil, "Oversea.123"); got != "Oversea.123" {
		t.Fatalf("expected default mysql password, got %q", got)
	}
	if got := passwordParam(map[string]any{"rootPassword": "custom-password"}, "Oversea.123"); got != "custom-password" {
		t.Fatalf("expected explicit mysql password, got %q", got)
	}
}

func TestServiceDeletesMySQLRemotelyBeforeRemovingInstance(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "mysql", Version: "8.0.36", ServerID: "srv-1", Status: "installed", Metadata: `{"port":3307}`}
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
		t.Fatalf("expected mysql instance to be deleted: %+v", s.instances)
	}
	joinedCommands := strings.Join(remote.commands, "\n")
	if !strings.Contains(joinedCommands, `systemctl disable --now "$SERVICE_NAME"`) {
		t.Fatalf("expected remote command to stop mysql service: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, `rm -rf "$INSTALL_ROOT"`) {
		t.Fatalf("expected remote command to remove mysql install root: %s", joinedCommands)
	}
}
