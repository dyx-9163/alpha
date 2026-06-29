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

type fakeRemote struct {
	commands      []string
	uploads       []string
	installScript string
}

func (f *fakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *fakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
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

type testLogger struct{}

func (testLogger) Info(format string, args ...any)  {}
func (testLogger) Error(format string, args ...any) {}

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

	remote := &fakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "s3-1", DeployDir: "/aifar/apps"}, Bundle{
		Version:        "2025-10-15T17-29-55Z",
		ArchivePath:    archive,
		GoArchivePath:  goArchive,
		GoModCachePath: goModCache,
		MCPath:         mc,
		RPMPaths:       []string{rpm},
	}, InstallOptions{APIPort: 9000, ConsolePort: 9001, RootUser: "admin", RootPassword: "Oversea.123"}, testLogger{})
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
	if !strings.Contains(remote.installScript, `DATA_DIR='/aifar/apps/minio/2025-10-15T17-29-55Z/data'`) {
		t.Fatalf("installer should render selected data directory:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `ExecStart=$INSTALL_ROOT/bin/minio server \$MINIO_OPTS \$MINIO_VOLUMES`) {
		t.Fatalf("installer should use env-driven MinIO volumes and opts:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `/minio/health/live`) {
		t.Fatalf("installer should verify MinIO health:\n%s", remote.installScript)
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
		InstallRoot:    "/aifar/apps/minio/2025-10-15T17-29-55Z",
		DataDir:        "/data/minio/aifar-minio-9000",
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
	uninstall, err := uninstallStandaloneScript("2025-10-15T17-29-55Z", "/aifar/apps/minio/2025-10-15T17-29-55Z", 9000)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uninstall, "{{") {
		t.Fatalf("uninstall script contains unrendered template markers:\n%s", uninstall)
	}
	if !strings.Contains(uninstall, "SERVICE_NAME=\"aifar-minio-$API_PORT\"") || !strings.Contains(uninstall, "API_PORT=9000") {
		t.Fatalf("uninstall script did not render standalone service identity:\n%s", uninstall)
	}
}
