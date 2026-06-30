package mysql

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
	mu             sync.Mutex
	commands       []string
	blockInstall   bool
	installStarted chan string
	releaseInstall chan struct{}
	primaryOutput  string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if f.blockInstall && strings.Contains(command, "install-mysql.sh") {
		f.installStarted <- server.ID
		select {
		case <-ctx.Done():
			return adapter.CommandResult{}, ctx.Err()
		case <-f.releaseInstall:
		}
	}
	f.mu.Lock()
	f.commands = append(f.commands, command)
	f.mu.Unlock()
	if strings.Contains(command, "replication_group_members") {
		return adapter.CommandResult{Stdout: f.primaryOutput}, nil
	}
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	return nil
}

type fakeLogger struct{}

func (fakeLogger) Info(format string, args ...any)  {}
func (fakeLogger) Error(format string, args ...any) {}

type recordingLogger struct {
	mu    sync.Mutex
	lines []string
}

func (r *recordingLogger) Info(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

func (r *recordingLogger) Error(format string, args ...any) {
	r.Info(format, args...)
}

func (r *recordingLogger) joined() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.lines, "\n")
}

func (f *fakeRemote) joinedCommands() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

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
	joinedCommands := remote.joinedCommands()
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
	joinedCommands := remote.joinedCommands()
	if strings.Count(joinedCommands, `"$MYSQLSH" --js --file`) != 1 || !strings.Contains(joinedCommands, `MYSQLSH="$INSTALL_ROOT/mysql-shell/bin/mysqlsh"`) {
		t.Fatalf("expected one innodb cluster bootstrap action: %s", joinedCommands)
	}
}

func TestServiceLogsClusterNodeCompletionForInnoDBCluster(t *testing.T) {
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
	log := &recordingLogger{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "8.0.36",
		Topology:        "innodb-cluster",
		Language:        "en",
		DefaultPassword: "Oversea.123",
		ServerIDs:       []string{"srv-1", "srv-2", "srv-3"},
		Parameters:      map[string]any{"port": 3306, "rootUser": "root", "clusterName": "aifarCluster"},
	}, []store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}}, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := log.joined()
	if !strings.Contains(lines, "MySQL InnoDB Cluster node installed") {
		t.Fatalf("expected cluster node completion log, got:\n%s", lines)
	}
	if strings.Contains(lines, "MySQL standalone installed") {
		t.Fatalf("cluster install should not log standalone completion:\n%s", lines)
	}
}

func TestServiceCheckInnoDBClusterRecordsCurrentPrimary(t *testing.T) {
	clusterID := "mysql_cluster_test"
	now := time.Now()
	instances := []store.AppInstance{
		mysqlClusterInstance("app-1", "srv-1", clusterID, "10.0.0.1:3306", now),
		mysqlClusterInstance("app-2", "srv-2", clusterID, "10.0.0.2:3306", now),
		mysqlClusterInstance("app-3", "srv-3", clusterID, "10.0.0.3:3306", now),
	}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "mysql-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
			"srv-2": {ID: "srv-2", Name: "mysql-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
			"srv-3": {ID: "srv-3", Name: "mysql-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
		},
		instances: instances,
	}
	remote := &fakeRemote{primaryOutput: "10.0.0.2:3306\n"}
	service := NewService(s, remote)

	result, err := service.Check(context.Background(), CheckRequest{
		Instance:        instances[0],
		Server:          s.servers["srv-1"],
		Language:        "en",
		DefaultPassword: "Oversea.123",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "running" {
		t.Fatalf("expected running check result, got %+v", result)
	}
	for _, instance := range s.instances {
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
			t.Fatal(err)
		}
		if got := metadata["currentPrimaryEndpoint"]; got != "10.0.0.2:3306" {
			t.Fatalf("expected current primary to be recorded for %s, got %v", instance.ID, got)
		}
		expectedStatus := "installed"
		if instance.ID == "app-1" {
			expectedStatus = "running"
		}
		if instance.Status != expectedStatus {
			t.Fatalf("expected instance %s status %s, got %s", instance.ID, expectedStatus, instance.Status)
		}
		expectedRole := "secondary"
		if metadata["endpoint"] == "10.0.0.2:3306" {
			expectedRole = "primary"
		}
		if got := metadata["role"]; got != expectedRole {
			t.Fatalf("expected %s role %s, got %v", instance.ID, expectedRole, got)
		}
	}
	if !strings.Contains(remote.joinedCommands(), "replication_group_members") {
		t.Fatal("expected check to query InnoDB Cluster replication group members")
	}
}

func TestServiceCheckInnoDBClusterRecordsRuntimeWhenPrimaryMissing(t *testing.T) {
	clusterID := "mysql_cluster_test"
	now := time.Now()
	instances := []store.AppInstance{
		mysqlClusterInstance("app-1", "srv-1", clusterID, "10.0.0.1:3306", now),
		mysqlClusterInstance("app-2", "srv-2", clusterID, "10.0.0.2:3306", now),
		mysqlClusterInstance("app-3", "srv-3", clusterID, "10.0.0.3:3306", now),
	}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "mysql-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
			"srv-2": {ID: "srv-2", Name: "mysql-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
			"srv-3": {ID: "srv-3", Name: "mysql-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
		},
		instances: instances,
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)

	_, err := service.Check(context.Background(), CheckRequest{
		Instance:        instances[0],
		Server:          s.servers["srv-1"],
		Language:        "en",
		DefaultPassword: "Oversea.123",
	}, fakeLogger{}, nil)
	if err == nil {
		t.Fatal("expected missing primary to fail cluster check")
	}
	metadata := map[string]any{}
	if err := json.Unmarshal([]byte(s.instances[0].Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	lastCheck, _ := metadata["lastCheck"].(map[string]any)
	details, _ := lastCheck["details"].(map[string]any)
	if got := details["runtimeStatus"]; got != "running" {
		t.Fatalf("expected runtimeStatus running after mysqladmin ping succeeded, got %v", got)
	}
	if s.instances[0].Status != "failed" {
		t.Fatalf("expected cluster check status failed, got %s", s.instances[0].Status)
	}
}

func TestServiceStartsInnoDBClusterAndMarksAllNodesRunning(t *testing.T) {
	clusterID := "mysql_cluster_test"
	now := time.Now()
	instances := []store.AppInstance{
		mysqlClusterInstance("app-1", "srv-1", clusterID, "10.0.0.1:3306", now),
		mysqlClusterInstance("app-2", "srv-2", clusterID, "10.0.0.2:3306", now),
		mysqlClusterInstance("app-3", "srv-3", clusterID, "10.0.0.3:3306", now),
	}
	for idx := range instances {
		instances[idx].Status = "failed"
	}
	s := &fakeStore{
		servers: map[string]store.Server{
			"srv-1": {ID: "srv-1", Name: "mysql-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
			"srv-2": {ID: "srv-2", Name: "mysql-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
			"srv-3": {ID: "srv-3", Name: "mysql-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
		},
		instances: instances,
	}
	remote := &fakeRemote{primaryOutput: "10.0.0.2:3306\n"}
	service := NewService(s, remote)

	err := service.StartInnoDBCluster(context.Background(), StartClusterRequest{
		Instances:       instances,
		Servers:         []store.Server{s.servers["srv-1"], s.servers["srv-2"], s.servers["srv-3"]},
		Language:        "en",
		DefaultPassword: "Oversea.123",
	}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "rebootClusterFromCompleteOutage") || !strings.Contains(joinedCommands, "rejoinInstance") {
		t.Fatalf("expected InnoDB Cluster start script to run, got: %s", joinedCommands)
	}
	for _, instance := range s.instances {
		if instance.Status != "running" {
			t.Fatalf("expected instance %s to be running, got %s", instance.ID, instance.Status)
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
			t.Fatal(err)
		}
		if got := metadata["currentPrimaryEndpoint"]; got != "10.0.0.2:3306" {
			t.Fatalf("expected primary endpoint on %s, got %v", instance.ID, got)
		}
		expectedRole := "secondary"
		if metadata["endpoint"] == "10.0.0.2:3306" {
			expectedRole = "primary"
		}
		if got := metadata["role"]; got != expectedRole {
			t.Fatalf("expected %s role %s, got %v", instance.ID, expectedRole, got)
		}
	}
}

func TestServiceInstallsInnoDBClusterBaseConcurrently(t *testing.T) {
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
	remote := &fakeRemote{
		blockInstall:   true,
		installStarted: make(chan string, 3),
		releaseInstall: make(chan struct{}),
	}
	service := NewService(s, remote)
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Install(context.Background(), InstallRequest{
			Version:         "8.0.36",
			Topology:        "innodb-cluster",
			Language:        "en",
			DefaultPassword: "Oversea.123",
			ServerIDs:       []string{"srv-1", "srv-2", "srv-3"},
			Concurrency:     3,
			Parameters:      map[string]any{"port": 3306, "rootUser": "root", "clusterName": "aifarCluster"},
		}, []store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}}, fakeLogger{}, nil)
	}()

	seen := map[string]bool{}
	for len(seen) < 3 {
		select {
		case target := <-remote.installStarted:
			seen[target] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("expected all MySQL base installs to start concurrently, got %v", seen)
		}
	}
	close(remote.releaseInstall)
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cluster install did not finish after releasing concurrent installs")
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
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, `systemctl disable --now "$SERVICE_NAME"`) {
		t.Fatalf("expected remote command to stop mysql service: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, `INSTALL_ROOT='/aifar/apps/mysql'`) || !strings.Contains(joinedCommands, `rm -rf "$ROOT"`) {
		t.Fatalf("expected remote command to remove mysql install root: %s", joinedCommands)
	}
}

func mysqlClusterInstance(id, serverID, clusterID, endpoint string, createdAt time.Time) store.AppInstance {
	metadata, _ := json.Marshal(map[string]any{
		"clusterId":   clusterID,
		"clusterName": "aifarCluster",
		"endpoint":    endpoint,
		"port":        3306,
		"rootUser":    "root",
		"topology":    "innodb-cluster",
	})
	return store.AppInstance{
		ID:        id,
		App:       "mysql",
		Version:   "8.0.36",
		ServerID:  serverID,
		Status:    "installed",
		Topology:  "innodb-cluster",
		Metadata:  string(metadata),
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}
