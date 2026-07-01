package minio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type installerFakeRemote struct {
	commands      []string
	uploads       []string
	installScript string
}

func (f *installerFakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *installerFakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.uploads = append(f.uploads, filepath.Base(localPath)+"->"+remotePath)
	if strings.HasSuffix(remotePath, "/install-minio.sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.installScript = string(content)
	}
	return nil
}

type installerTestLogger struct{}

func (installerTestLogger) Info(format string, args ...any)  {}
func (installerTestLogger) Error(format string, args ...any) {}

func TestSelectBundleDiscoversMinioBuildResources(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz")
	goArchive := filepath.Join(root, "go", "1.24.8", "go1.24.8.linux-amd64.tar.gz")
	goModCache := filepath.Join(root, "go", "cache", "gomodcache-linux-amd64.tar.gz")
	mc := filepath.Join(root, "mc.linux-amd64.RELEASE.2025-08-13T08-35-41Z")
	rpmDir := filepath.Join(root, "rpms")
	for _, dir := range []string{filepath.Dir(goArchive), filepath.Dir(goModCache), rpmDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, file := range []string{archive, goArchive, goModCache, mc, filepath.Join(rpmDir, "git.rpm")} {
		if err := os.WriteFile(file, []byte("minio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	bundle, err := SelectBundle([]store.Resource{{App: "minio", Part: "backend", Version: "2025-10-15T17-29-55Z", Path: archive}}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != "2025-10-15T17-29-55Z" || bundle.ArchivePath != archive {
		t.Fatalf("unexpected minio bundle: %+v", bundle)
	}
	if bundle.GoArchivePath != goArchive || bundle.GoModCachePath != goModCache || bundle.MCPath != mc || len(bundle.RPMPaths) != 1 {
		t.Fatalf("expected go, gomod, mc, and rpm resources to be discovered: %+v", bundle)
	}
	if err := VerifyBundle(bundle); err != nil {
		t.Fatal(err)
	}
}

func TestInstallerUploadsResourcesAndRunsMinioScript(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz")
	goArchive := filepath.Join(root, "go1.24.8.linux-amd64.tar.gz")
	goModCache := filepath.Join(root, "gomodcache-linux-amd64.tar.gz")
	mc := filepath.Join(root, "mc.linux-amd64.RELEASE.2025-08-13T08-35-41Z")
	rpm := filepath.Join(root, "git.rpm")
	for _, file := range []string{archive, goArchive, goModCache, mc, rpm} {
		if err := os.WriteFile(file, []byte("minio"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "s3-1", DeployDir: "/aifar/apps"}, Bundle{
		Version:        "2025-10-15T17-29-55Z",
		ArchivePath:    archive,
		GoArchivePath:  goArchive,
		GoModCachePath: goModCache,
		MCPath:         mc,
		RPMPaths:       []string{rpm},
	}, InstallOptions{APIPort: 9000, ConsolePort: 9001, RootUser: "admin", RootPassword: "Oversea.123"}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	joinedUploads := strings.Join(remote.uploads, "\n")
	if !strings.Contains(joinedUploads, "minio-RELEASE.2025-10-15T17-29-55Z.tar.gz->/aifar/apps/_work/minio-2025-10-15T17-29-55Z-") {
		t.Fatalf("minio archive upload missing: %s", joinedUploads)
	}
	if !strings.Contains(joinedUploads, "go1.24.8.linux-amd64.tar.gz->/aifar/apps/_work/minio-2025-10-15T17-29-55Z-") {
		t.Fatalf("go archive upload missing: %s", joinedUploads)
	}
	if !strings.Contains(strings.Join(remote.commands, "\n"), "install-minio.sh") {
		t.Fatalf("install command missing: %+v", remote.commands)
	}
	if !strings.Contains(remote.installScript, `go build -tags kqueue -trimpath`) {
		t.Fatalf("installer should build minio from source:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `MINIO_ROOT_PASSWORD="$ROOT_PASSWORD"`) {
		t.Fatalf("installer should write root password into env file:\n%s", remote.installScript)
	}
	if strings.Contains(remote.installScript, `MINIO_API_REPLICATION_PRIORITY`) {
		t.Fatalf("standalone installer should not set replication tuning without replication options:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `DATA_DIR='/aifar/apps/minio/data'`) {
		t.Fatalf("installer should render selected data directory:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `ExecStart=$INSTALL_ROOT/bin/minio server \$MINIO_OPTS \$MINIO_VOLUMES`) {
		t.Fatalf("installer should use env-driven MinIO volumes and opts:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `/minio/health/live`) {
		t.Fatalf("installer should verify MinIO health:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `open_firewall_ports "$API_PORT" "$CONSOLE_PORT"`) ||
		!strings.Contains(remote.installScript, `allow_selinux_ports http_port_t "$API_PORT" "$CONSOLE_PORT"`) {
		t.Fatalf("installer should open firewall and SELinux rules for MinIO ports:\n%s", remote.installScript)
	}
}

func TestInstallerResolveDataDirUsesLocalDirectoryMode(t *testing.T) {
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	dataDir, err := installer.ResolveDataDir(context.Background(), store.Server{Name: "s3-1"}, DataDirRequest{
		Mode:        StorageModeLocalDisk,
		DataRoot:    "/data/minio",
		InstallRoot: "/aifar/apps/minio",
		APIPort:     9000,
	}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if dataDir != "/data/minio" {
		t.Fatalf("unexpected local data dir: %s", dataDir)
	}
	joined := strings.Join(remote.commands, "\n")
	if !strings.Contains(joined, `DATA_DIR='/data/minio'`) || !strings.Contains(joined, `mkdir -p "$DATA_DIR"`) {
		t.Fatalf("local mode should create the selected data directory: %s", joined)
	}
	if strings.Contains(joined, "lsblk") || strings.Contains(joined, "mkfs.ext4") {
		t.Fatalf("local mode must not probe or format disks: %s", joined)
	}
}

func TestInstallerResolveDataDirPreparesUnmountedDisk(t *testing.T) {
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	dataDir, err := installer.ResolveDataDir(context.Background(), store.Server{Name: "s3-1"}, DataDirRequest{
		Mode:        StorageModeUnmountedDisk,
		DataRoot:    "/data/minio",
		DiskDevice:  "/dev/sdb",
		InstallRoot: "/aifar/apps/minio/2025-10-15T17-29-55Z",
		APIPort:     9000,
	}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if dataDir != "/data/minio/disk1/minio" {
		t.Fatalf("unexpected unmounted disk data dir: %s", dataDir)
	}
	joined := strings.Join(remote.commands, "\n")
	for _, want := range []string{
		`DATA_DEVICE='/dev/sdb'`,
		`MOUNT_ROOT='/data/minio/disk1'`,
		`mkfs.ext4 -F "$DATA_DEVICE"`,
		`UUID=$UUID $MOUNT_ROOT ext4 defaults,nofail 0 2`,
		`mount "$MOUNT_ROOT"`,
		`AIFAR_SELECTED_MINIO_DATA_DIR=$DATA_DIR`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("unmounted disk command missing %q:\n%s", want, joined)
		}
	}
}

func TestInstallerResolveDataDirsPreparesMultipleUnmountedDisks(t *testing.T) {
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	dataDirs, err := installer.ResolveDataDirs(context.Background(), store.Server{Name: "s3-1"}, DataDirRequest{
		Mode:        StorageModeUnmountedDisk,
		DataRoot:    "/data/minio",
		DiskDevices: []string{"/dev/sdb", "/dev/sdc"},
		InstallRoot: "/aifar/apps/minio/2025-10-15T17-29-55Z",
		APIPort:     9000,
	}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	wantDirs := []string{"/data/minio/disk1/minio", "/data/minio/disk2/minio"}
	if strings.Join(dataDirs, "|") != strings.Join(wantDirs, "|") {
		t.Fatalf("unexpected unmounted disk data dirs: %+v", dataDirs)
	}
	joined := strings.Join(remote.commands, "\n")
	for _, want := range []string{
		`DATA_DEVICE='/dev/sdb'`,
		`MOUNT_ROOT='/data/minio/disk1'`,
		`DATA_DEVICE='/dev/sdc'`,
		`MOUNT_ROOT='/data/minio/disk2'`,
		`DEVICE_RM="$(lsblk -dn -o RM "$DATA_DEVICE" | head -n 1)"`,
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("multiple unmounted disk command missing %q:\n%s", want, joined)
		}
	}
}

func TestMinIOStandaloneScriptsRenderTemplates(t *testing.T) {
	install, err := installStandaloneScript(InstallScriptRequest{
		Version:        "2025-10-15T17-29-55Z",
		WorkDir:        "/aifar/apps/_work/minio",
		ArchivePath:    "/tmp/minio.tar.gz",
		GoArchivePath:  "/tmp/go.tar.gz",
		GoModCachePath: "/tmp/gomod.tar.gz",
		MCRemotePath:   "/tmp/mc",
		InstallRoot:    "/aifar/apps/minio",
		DataDir:        "/data/minio",
		APIPort:        9000,
		ConsolePort:    9001,
		RootUser:       "admin",
		RootPassword:   "Oversea.123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(install, "{{") {
		t.Fatalf("install script contains unrendered template markers:\n%s", install)
	}
	if !strings.Contains(install, "VERSION='2025-10-15T17-29-55Z'") || !strings.Contains(install, "API_PORT=9000") || !strings.Contains(install, "CONSOLE_PORT=9001") {
		t.Fatalf("install script did not render core standalone variables:\n%s", install)
	}
	replicationInstall, err := installStandaloneScript(InstallScriptRequest{
		Version:                    "2025-10-15T17-29-55Z",
		WorkDir:                    "/aifar/apps/_work/minio",
		ArchivePath:                "/tmp/minio.tar.gz",
		GoArchivePath:              "/tmp/go.tar.gz",
		GoModCachePath:             "/tmp/gomod.tar.gz",
		InstallRoot:                "/aifar/apps/minio",
		DataDir:                    "/data/minio",
		APIPort:                    9000,
		ConsolePort:                9001,
		RootUser:                   "admin",
		RootPassword:               "Oversea.123",
		ReplicationPriority:        "slow",
		ReplicationMaxWorkers:      8,
		ReplicationMaxLargeWorkers: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replicationInstall, "MINIO_API_REPLICATION_PRIORITY='slow'") ||
		!strings.Contains(replicationInstall, "MINIO_API_REPLICATION_MAX_WORKERS=8") ||
		!strings.Contains(replicationInstall, "MINIO_API_REPLICATION_MAX_LRG_WORKERS=1") {
		t.Fatalf("install script should render replication tuning env vars:\n%s", replicationInstall)
	}
	distributed, err := configureDistributedNodeScript(DistributedNodeConfig{
		Version:      "2025-10-15T17-29-55Z",
		InstallRoot:  "/aifar/apps/minio",
		APIPort:      9000,
		ConsolePort:  9001,
		RootUser:     "admin",
		RootPassword: "Oversea.123",
		Volumes:      []DistributedVolume{{Host: "10.0.0.1", Port: 9000, Path: "/data/minio"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(distributed, `open_firewall_ports "$API_PORT" "$CONSOLE_PORT"`) ||
		!strings.Contains(distributed, `allow_selinux_ports http_port_t "$API_PORT" "$CONSOLE_PORT"`) {
		t.Fatalf("distributed script should open firewall and SELinux rules for MinIO ports:\n%s", distributed)
	}
	replication, err := configureBucketReplicationScript(BucketReplicationConfig{
		InstallRoot:      "/aifar/apps/minio",
		APIPort:          9000,
		LocalEndpoint:    "http://127.0.0.1:9000",
		PeerEndpoint:     "http://10.0.0.2:9000",
		PeerRemotePrefix: "http://admin:secret@10.0.0.2:9000",
		RootUser:         "admin",
		RootPassword:     "Oversea.123",
		Buckets:          []string{"aifar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(replication, `"$MC" replicate add "$LOCAL_ALIAS/$BUCKET"`) || !strings.Contains(replication, `REPLICATE_FLAGS="existing-objects"`) {
		t.Fatalf("replication script should configure bucket replication:\n%s", replication)
	}
	uninstall, err := uninstallStandaloneScript("2025-10-15T17-29-55Z", "/aifar/apps/minio", "/aifar/apps/minio/2025-10-15T17-29-55Z", 9000, UninstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uninstall, "{{") {
		t.Fatalf("uninstall script contains unrendered template markers:\n%s", uninstall)
	}
	if !strings.Contains(uninstall, `SERVICE_NAME="aifar-minio"`) || !strings.Contains(uninstall, `LEGACY_SERVICE_NAME="aifar-minio-$API_PORT"`) || !strings.Contains(uninstall, "API_PORT=9000") {
		t.Fatalf("uninstall script did not render standalone service identity:\n%s", uninstall)
	}
}
