package minio

import (
	"context"
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
		v.CreatedAt = time.Now()
	} else {
		for idx := range f.instances {
			if f.instances[idx].ID == v.ID {
				if v.CreatedAt.IsZero() {
					v.CreatedAt = f.instances[idx].CreatedAt
				}
				v.UpdatedAt = time.Now()
				f.instances[idx] = v
				return v, nil
			}
		}
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}
	v.UpdatedAt = time.Now()
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
	mu             sync.Mutex
	commands       []string
	responses      []adapter.CommandResult
	blockInstall   bool
	installStarted chan string
	releaseInstall chan struct{}
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	if f.blockInstall && strings.Contains(command, "install-minio.sh") {
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
	if len(f.responses) > 0 {
		result := f.responses[0]
		f.responses = f.responses[1:]
		return result, nil
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

func TestServiceInstallsStandaloneMinioAndRecordsInstalledInstance(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz")
	goArchive := filepath.Join(root, "go", "1.24.8", "go1.24.8.linux-amd64.tar.gz")
	goModCache := filepath.Join(root, "go", "cache", "gomodcache-linux-amd64.tar.gz")
	mc := filepath.Join(root, "mc.linux-amd64.RELEASE.2025-08-13T08-35-41Z")
	for _, dir := range []string{filepath.Dir(goArchive), filepath.Dir(goModCache)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{archive, goArchive, goModCache, mc} {
		if err := os.WriteFile(file, []byte("minio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.3", Username: "root", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "2025-10-15T17-29-55Z",
		ServerID:        "srv-1",
		Topology:        "standalone",
		Language:        "en",
		DefaultPassword: "Oversea.123",
		Parameters:      map[string]any{"apiPort": 9002, "consolePort": 9003, "rootUser": "admin"},
	}, []store.Resource{{App: "minio", Part: "backend", Version: "2025-10-15T17-29-55Z", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 1 {
		t.Fatalf("expected one minio instance, got %d", len(s.instances))
	}
	instance := s.instances[0]
	if instance.Status != "installed" || instance.Topology != "standalone" || instance.App != "minio" {
		t.Fatalf("expected installed standalone minio instance: %+v", instance)
	}
	if strings.Contains(instance.Metadata, "Oversea.123") {
		t.Fatalf("metadata must not store root password: %s", instance.Metadata)
	}
	if !strings.Contains(instance.Metadata, `"apiPort":9002`) || !strings.Contains(instance.Metadata, `"endpoint":"http://10.0.0.3:9002"`) || !strings.Contains(instance.Metadata, `"dataDir":"/aifar/apps/minio/data"`) || !strings.Contains(instance.Metadata, `"storageMode":"local-disk"`) {
		t.Fatalf("metadata should include endpoint and ports: %s", instance.Metadata)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "install-minio.sh") {
		t.Fatalf("expected minio install script to run: %s", joinedCommands)
	}
}

func TestServiceInstallsDistributedMinioAndRecordsEachNode(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz")
	goArchive := filepath.Join(root, "go", "1.24.8", "go1.24.8.linux-amd64.tar.gz")
	goModCache := filepath.Join(root, "go", "cache", "gomodcache-linux-amd64.tar.gz")
	for _, dir := range []string{filepath.Dir(goArchive), filepath.Dir(goModCache)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{archive, goArchive, goModCache} {
		if err := os.WriteFile(file, []byte("minio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "s3-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "s3-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
		"srv-4": {ID: "srv-4", Name: "s3-4", Host: "10.0.0.4", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "2025-10-15T17-29-55Z",
		Topology:        "distributed",
		Language:        "en",
		DefaultPassword: "Oversea.123",
		ServerIDs:       []string{"srv-1", "srv-2", "srv-3", "srv-4"},
		Parameters:      map[string]any{"apiPort": 9000, "consolePort": 9001, "rootUser": "admin"},
	}, []store.Resource{{App: "minio", Part: "backend", Version: "2025-10-15T17-29-55Z", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 4 {
		t.Fatalf("expected four minio distributed instances, got %d", len(s.instances))
	}
	if s.instances[0].Topology != "distributed" || strings.Contains(s.instances[0].Metadata, "Oversea.123") {
		t.Fatalf("expected safe distributed metadata: %+v", s.instances[0])
	}
	if !strings.Contains(s.instances[0].Metadata, `"dataDir":"/aifar/apps/minio/data"`) || !strings.Contains(s.instances[0].Metadata, `"storageMode":"local-disk"`) {
		t.Fatalf("distributed metadata should include selected data directory: %+v", s.instances[0])
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "AIFAR_MINIO_DISTRIBUTED_CONFIGURE") {
		t.Fatalf("expected minio distributed configure action: %s", joinedCommands)
	}
}

func TestServiceInstallsBucketReplicationMinioAndRecordsEachNode(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz")
	goArchive := filepath.Join(root, "go", "1.24.8", "go1.24.8.linux-amd64.tar.gz")
	goModCache := filepath.Join(root, "go", "cache", "gomodcache-linux-amd64.tar.gz")
	mc := filepath.Join(root, "mc.linux-amd64.RELEASE.2025-08-13T08-35-41Z")
	for _, dir := range []string{filepath.Dir(goArchive), filepath.Dir(goModCache)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{archive, goArchive, goModCache, mc} {
		if err := os.WriteFile(file, []byte("minio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "s3-a", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "s3-b", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Install(context.Background(), InstallRequest{
		Version:         "2025-10-15T17-29-55Z",
		Topology:        "bucket-replication",
		Language:        "en",
		DefaultPassword: "Oversea.123",
		ServerIDs:       []string{"srv-1", "srv-2"},
		Parameters: map[string]any{
			"apiPort":                    9000,
			"consolePort":                9001,
			"rootUser":                   "admin",
			"replicationBuckets":         "aifar,logs",
			"replicationPriority":        "slow",
			"replicationMaxWorkers":      8,
			"replicationMaxLargeWorkers": 1,
		},
	}, []store.Resource{{App: "minio", Part: "backend", Version: "2025-10-15T17-29-55Z", Path: archive}}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 2 {
		t.Fatalf("expected two minio replication instances, got %d", len(s.instances))
	}
	for _, instance := range s.instances {
		if instance.Topology != "bucket-replication" || !strings.Contains(instance.Metadata, `"replicationMode":"bucket"`) || strings.Contains(instance.Metadata, "Oversea.123") {
			t.Fatalf("expected safe bucket replication metadata: %+v", instance)
		}
		if !strings.Contains(instance.Metadata, `"replicationMaxWorkers":8`) || !strings.Contains(instance.Metadata, `"replicationMaxLargeWorkers":1`) {
			t.Fatalf("expected replication tuning metadata: %s", instance.Metadata)
		}
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "AIFAR_MINIO_BUCKET_REPLICATION") || !strings.Contains(joinedCommands, `"$MC" replicate add`) {
		t.Fatalf("expected bucket replication configure action: %s", joinedCommands)
	}
}

func TestServiceUsesConcurrencyForDistributedMinioInstalls(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz")
	goArchive := filepath.Join(root, "go", "1.24.8", "go1.24.8.linux-amd64.tar.gz")
	goModCache := filepath.Join(root, "go", "cache", "gomodcache-linux-amd64.tar.gz")
	for _, dir := range []string{filepath.Dir(goArchive), filepath.Dir(goModCache)} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{archive, goArchive, goModCache} {
		if err := os.WriteFile(file, []byte("minio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	s := &fakeStore{servers: map[string]store.Server{
		"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.1", DeployDir: "/aifar/apps"},
		"srv-2": {ID: "srv-2", Name: "s3-2", Host: "10.0.0.2", DeployDir: "/aifar/apps"},
		"srv-3": {ID: "srv-3", Name: "s3-3", Host: "10.0.0.3", DeployDir: "/aifar/apps"},
		"srv-4": {ID: "srv-4", Name: "s3-4", Host: "10.0.0.4", DeployDir: "/aifar/apps"},
	}}
	remote := &fakeRemote{
		blockInstall:   true,
		installStarted: make(chan string, 4),
		releaseInstall: make(chan struct{}),
	}
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(remote.releaseInstall) })
	service := NewService(s, remote)
	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Install(context.Background(), InstallRequest{
			Version:         "2025-10-15T17-29-55Z",
			Topology:        "distributed",
			Language:        "en",
			DefaultPassword: "Oversea.123",
			ServerIDs:       []string{"srv-1", "srv-2", "srv-3", "srv-4"},
			Concurrency:     2,
			Parameters:      map[string]any{"apiPort": 9000, "consolePort": 9001, "rootUser": "admin"},
		}, []store.Resource{{App: "minio", Part: "backend", Version: "2025-10-15T17-29-55Z", Path: archive}}, fakeLogger{}, nil)
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case target := <-remote.installStarted:
			seen[target] = true
		case <-time.After(2 * time.Second):
			t.Fatalf("expected two MinIO installs to start, got %v", seen)
		}
	}
	select {
	case target := <-remote.installStarted:
		t.Fatalf("third MinIO install %s started before concurrency slot was released", target)
	case <-time.After(50 * time.Millisecond):
	}

	releaseOnce.Do(func() { close(remote.releaseInstall) })
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("MinIO distributed install did not finish after releasing concurrent installs")
	}
}

func TestMinioPasswordFallsBackToDefaultPassword(t *testing.T) {
	if got := passwordParam(nil, "Oversea.123"); got != "Oversea.123" {
		t.Fatalf("expected default minio password, got %q", got)
	}
	if got := passwordParam(map[string]any{"rootPassword": "custom-password"}, "Oversea.123"); got != "custom-password" {
		t.Fatalf("expected explicit minio password, got %q", got)
	}
}

func TestValidateMinioStorageRequiresDeviceForUnmountedDisk(t *testing.T) {
	err := validateMinioStorage(map[string]any{
		"storageMode": "unmounted-disk",
		"dataRoot":    "/data/minio",
	})
	if err == nil || !strings.Contains(err.Error(), "requires a disk device") {
		t.Fatalf("expected missing disk device validation error, got %v", err)
	}
	if err := validateMinioStorage(map[string]any{
		"storageMode": "unmounted-disk",
		"dataRoot":    "/data/minio",
		"diskDevice":  "/dev/sdb",
	}); err != nil {
		t.Fatalf("expected valid unmounted disk storage config: %v", err)
	}
	if err := validateMinioStorage(map[string]any{
		"storageMode": "unmounted-disk",
		"dataRoot":    "/data/minio",
		"diskDevice": map[string]any{
			"srv-1": "/dev/sdb",
			"srv-2": "/dev/vdb",
		},
	}, "srv-1", "srv-2"); err != nil {
		t.Fatalf("expected valid per-server unmounted disk config: %v", err)
	}
	if err := validateMinioStorage(map[string]any{
		"storageMode": "unmounted-disk",
		"dataRoot":    "/data/minio",
		"diskDevice": map[string]any{
			"srv-1": []any{"/dev/sdb", "/dev/sdc"},
			"srv-2": []any{"/dev/vdb", "/dev/vdc"},
		},
	}, "srv-1", "srv-2"); err != nil {
		t.Fatalf("expected valid per-server multi-disk storage config: %v", err)
	}
	err = validateMinioStorage(map[string]any{
		"storageMode": "unmounted-disk",
		"dataRoot":    "/data/minio",
		"diskDevice": map[string]any{
			"srv-1": "/dev/sdb",
		},
	}, "srv-1", "srv-2")
	if err == nil || !strings.Contains(err.Error(), "requires a disk device") {
		t.Fatalf("expected missing per-server disk validation error, got %v", err)
	}
	if err := validateMinioStorage(map[string]any{
		"storageMode": "local-disk",
		"dataRoot":    "/data/minio",
	}); err != nil {
		t.Fatalf("expected valid local storage config: %v", err)
	}
}

func TestMinioReplicationOptionsDefaultToConservativeAsyncProfile(t *testing.T) {
	options := minioReplicationOptions(map[string]any{})
	if strings.Join(options.Buckets, ",") != "aifar" || options.Priority != "slow" || options.MaxWorkers != 8 || options.MaxLargeWorkers != 1 || options.ReplicateDeletes {
		t.Fatalf("unexpected default replication options: %+v", options)
	}
	if err := validateMinioReplicationOptions(options); err != nil {
		t.Fatalf("expected default replication options to be valid: %v", err)
	}
}

func TestServiceDeletesMinioRemotelyBeforeRemovingInstance(t *testing.T) {
	instance := store.AppInstance{ID: "app-1", App: "minio", Version: "2025-10-15T17-29-55Z", ServerID: "srv-1", Status: "installed", Metadata: `{"apiPort":9002}`}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "s3-1", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	err := service.Delete(context.Background(), DeleteRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.instances) != 0 {
		t.Fatalf("expected minio instance to be deleted: %+v", s.instances)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, `systemctl disable --now "$SERVICE_NAME"`) {
		t.Fatalf("expected remote command to stop minio service: %s", joinedCommands)
	}
	if !strings.Contains(joinedCommands, `INSTALL_ROOT='/aifar/apps/minio'`) || !strings.Contains(joinedCommands, `rm -rf "$ROOT"`) {
		t.Fatalf("expected remote command to remove minio install root: %s", joinedCommands)
	}
}

func TestServiceChecksMinioHealthAndUpdatesInstanceStatus(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "minio",
		Version:  "2025-10-15T17-29-55Z",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"apiPort":9002,"endpoint":"http://10.0.0.3:9002","serviceName":"aifar-minio"}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.3", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{}
	service := NewService(s, remote)
	result, err := service.Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "available" {
		t.Fatalf("expected available minio status, got %+v", result)
	}
	if len(s.instances) != 1 || s.instances[0].Status != "available" {
		t.Fatalf("expected minio instance status to be updated in place: %+v", s.instances)
	}
	if !strings.Contains(s.instances[0].Metadata, `"lastCheck"`) || !strings.Contains(s.instances[0].Metadata, `"status":"available"`) {
		t.Fatalf("expected lastCheck metadata to be recorded: %s", s.instances[0].Metadata)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "/minio/health/live") || !strings.Contains(joinedCommands, "API_PORT=9002") {
		t.Fatalf("expected health endpoint probe command: %s", joinedCommands)
	}
}

func TestServiceCheckRecordsStorageCapacityAndCleanupEstimate(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "minio",
		Version:  "2025-10-15T17-29-55Z",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"apiPort":9002,"endpoint":"http://10.0.0.3:9002","serviceName":"aifar-minio","dataDirs":["/data/minio/disk1/minio","/data/minio/disk2/minio"]}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.3", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{responses: []adapter.CommandResult{{
		Stdout: strings.Join([]string{
			"runtimeStatus=available",
			"minioHealthStatus=running",
			"minioServiceStatus=running",
			"minioPortStatus=listening",
			"minioRuntimeSource=curl",
			"minioStorageTotalBytes=3000",
			"minioStorageUsedBytes=1200",
			"minioStorageAvailableBytes=1800",
			"minioStorageUsagePercent=40",
			"minioStoragePathCount=2",
			"minioStorageDiskCount=2",
			"minioStorageDisk1Path=/data/minio/disk1/minio",
			"minioStorageDisk1Device=/dev/nvme0n2",
			"minioStorageDisk1MountPoint=/data/minio/disk1",
			"minioStorageDisk1TotalBytes=1000",
			"minioStorageDisk1UsedBytes=400",
			"minioStorageDisk1AvailableBytes=600",
			"minioStorageDisk1UsagePercent=40",
			"minioStorageDisk2Path=/data/minio/disk2/minio",
			"minioStorageDisk2Device=/dev/nvme0n3",
			"minioStorageDisk2MountPoint=/data/minio/disk2",
			"minioStorageDisk2TotalBytes=2000",
			"minioStorageDisk2UsedBytes=800",
			"minioStorageDisk2AvailableBytes=1200",
			"minioStorageDisk2UsagePercent=40",
			"cleanupEstimateStatus=available",
			"cleanupEstimateRetentionDays=30",
			"cleanupEstimateObjectCount=7",
			"cleanupEstimateBytes=456",
			"cleanupEstimateSource=mc",
		}, "\n"),
	}}}
	service := NewService(s, remote)
	result, err := service.Check(context.Background(), CheckRequest{Instance: instance, Server: s.servers["srv-1"], Language: "en"}, fakeLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Details["minioStorageTotalBytes"] != int64(3000) || result.Details["cleanupEstimateBytes"] != int64(456) {
		t.Fatalf("expected storage capacity and cleanup estimate details, got %+v", result.Details)
	}
	disks, ok := result.Details["minioStorageDisks"].([]map[string]any)
	if !ok || len(disks) != 2 {
		t.Fatalf("expected per-disk storage details, got %#v", result.Details["minioStorageDisks"])
	}
	if disks[0]["path"] != "/data/minio/disk1/minio" || disks[0]["device"] != "/dev/nvme0n2" || disks[0]["totalBytes"] != int64(1000) {
		t.Fatalf("unexpected first disk detail: %#v", disks[0])
	}
	if disks[1]["path"] != "/data/minio/disk2/minio" || disks[1]["device"] != "/dev/nvme0n3" || disks[1]["totalBytes"] != int64(2000) {
		t.Fatalf("unexpected second disk detail: %#v", disks[1])
	}
	if !strings.Contains(s.instances[0].Metadata, `"minioStorageTotalBytes":3000`) || !strings.Contains(s.instances[0].Metadata, `"cleanupEstimateObjectCount":7`) || !strings.Contains(s.instances[0].Metadata, `"device":"/dev/nvme0n2"`) {
		t.Fatalf("expected storage details to be persisted in lastCheck metadata: %s", s.instances[0].Metadata)
	}
	joinedCommands := remote.joinedCommands()
	if !strings.Contains(joinedCommands, "RETENTION_DAYS=30") || !strings.Contains(joinedCommands, "/data/minio/disk1/minio") || !strings.Contains(joinedCommands, "/data/minio/disk2/minio") {
		t.Fatalf("expected probe command to include retention and data directories: %s", joinedCommands)
	}
}

func TestServiceEstimatesCleanupWithRequestedRetentionDays(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "minio",
		Version:  "2025-10-15T17-29-55Z",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"apiPort":9002,"endpoint":"http://10.0.0.3:9002","serviceName":"aifar-minio","dataDir":"/data/minio"}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.3", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{responses: []adapter.CommandResult{{
		Stdout: strings.Join([]string{
			"runtimeStatus=available",
			"cleanupEstimateStatus=available",
			"cleanupEstimateRetentionDays=14",
			"cleanupEstimateObjectCount=3",
			"cleanupEstimateBytes=2048",
			"cleanupEstimateSource=mc",
		}, "\n"),
	}}}
	service := NewService(s, remote)
	result, err := service.EstimateCleanup(context.Background(), CleanupEstimateRequest{
		Instance:      instance,
		Server:        s.servers["srv-1"],
		RetentionDays: 14,
	}, fakeLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "available" || result.RetentionDays != 14 || result.ObjectCount != 3 || result.Bytes != 2048 {
		t.Fatalf("unexpected cleanup estimate: %+v", result)
	}
	if !strings.Contains(remote.joinedCommands(), "RETENTION_DAYS=14") {
		t.Fatalf("expected requested retention days in command: %s", remote.joinedCommands())
	}
}

func TestServiceAppliesCleanupPolicyWithILMRule(t *testing.T) {
	instance := store.AppInstance{
		ID:       "app-1",
		App:      "minio",
		Version:  "2025-10-15T17-29-55Z",
		ServerID: "srv-1",
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"apiPort":9002,"endpoint":"http://10.0.0.3:9002","serviceName":"aifar-minio"}`,
	}
	s := &fakeStore{
		servers:   map[string]store.Server{"srv-1": {ID: "srv-1", Name: "s3-1", Host: "10.0.0.3", DeployDir: "/aifar/apps"}},
		instances: []store.AppInstance{instance},
	}
	remote := &fakeRemote{responses: []adapter.CommandResult{{
		Stdout: strings.Join([]string{
			"cleanupPolicyStatus=enabled",
			"cleanupPolicyBucket=aifar",
			"cleanupPolicyPrefix=logs/",
			"cleanupPolicyRetentionDays=60",
			"cleanupPolicyRuleID=rule-60",
			"cleanupPolicySource=mc-ilm",
		}, "\n"),
	}}}
	service := NewService(s, remote)

	result, err := service.ApplyCleanupPolicy(context.Background(), CleanupPolicyRequest{
		Instance:       instance,
		Server:         s.servers["srv-1"],
		Bucket:         "aifar",
		Prefix:         "logs/",
		RetentionDays:  60,
		Enabled:        true,
		ExistingRuleID: "old-rule",
	}, fakeLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "enabled" || result.Bucket != "aifar" || result.Prefix != "logs/" || result.RetentionDays != 60 || result.RuleID != "rule-60" {
		t.Fatalf("unexpected cleanup policy result: %+v", result)
	}
	command := remote.joinedCommands()
	if !strings.Contains(command, `BUCKET='aifar'`) || !strings.Contains(command, `PREFIX='logs/'`) || !strings.Contains(command, "RETENTION_DAYS=60") {
		t.Fatalf("expected cleanup policy command to include bucket, prefix and retention: %s", command)
	}
	if !strings.Contains(command, `ilm rule rm --id "$PREVIOUS_RULE_ID"`) || !strings.Contains(command, `ilm rule add --prefix "$PREFIX" --expire-days "$RETENTION_DAYS" "$TARGET"`) {
		t.Fatalf("expected cleanup policy command to replace old rule and add ILM expiration rule: %s", command)
	}
}
