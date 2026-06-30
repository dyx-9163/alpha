package docker

import (
	"context"
	"fmt"
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
	stderr        string
}

func (f *installerFakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	return adapter.CommandResult{Stdout: "ok", Stderr: f.stderr}, nil
}

func (f *installerFakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.uploads = append(f.uploads, filepath.Base(localPath)+"->"+remotePath)
	if strings.HasSuffix(remotePath, "/install-docker.sh") {
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

type installerCollectingLogger struct {
	infos  []string
	errors []string
}

func (l *installerCollectingLogger) Info(format string, args ...any) {
	l.infos = append(l.infos, fmt.Sprintf(format, args...))
}

func (l *installerCollectingLogger) Error(format string, args ...any) {
	l.errors = append(l.errors, fmt.Sprintf(format, args...))
}

func TestSelectBundleUsesCurrentDockerResource(t *testing.T) {
	root := t.TempDir()
	bundle := filepath.Join(root, "aifar-docker-static-24.0.9-linux-x86_64.tar")
	if err := os.WriteFile(bundle, []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	rpmDir := filepath.Join(root, "rpms")
	if err := os.MkdirAll(rpmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rpmDir, "tar.rpm"), []byte("rpm"), 0o644); err != nil {
		t.Fatal(err)
	}
	selected, err := SelectBundle([]store.Resource{{App: "docker", Part: "backend", Version: "24.0.9", Path: bundle}}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if selected.Version != "24.0.9" || selected.ArchivePath != bundle || len(selected.RPMPaths) != 1 {
		t.Fatalf("unexpected bundle: %+v", selected)
	}
}

func TestInstallerUploadsBundleRPMsAndRunsScript(t *testing.T) {
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "db-1", DeployDir: "/aifar/apps"}, Bundle{
		Version:     "24.0.9",
		ArchivePath: filepath.Join("resources", "docker", "24.0.9", "aifar-docker-static-24.0.9-linux-x86_64.tar"),
		RPMPaths:    []string{filepath.Join("resources", "docker", "24.0.9", "rpms", "tar.rpm")},
	}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	joinedUploads := strings.Join(remote.uploads, "\n")
	if !strings.Contains(joinedUploads, "aifar-docker-static-24.0.9-linux-x86_64.tar->/aifar/apps/_work/docker-24.0.9-") {
		t.Fatalf("bundle upload missing: %s", joinedUploads)
	}
	if !strings.Contains(joinedUploads, "tar.rpm->/aifar/apps/_work/docker-24.0.9-") {
		t.Fatalf("rpm upload missing: %s", joinedUploads)
	}
	joinedCommands := strings.Join(remote.commands, "\n")
	if !strings.Contains(joinedCommands, "install-docker.sh") {
		t.Fatalf("install script command missing: %s", joinedCommands)
	}
}

func TestInstallerDefaultsRemoteWorkDirToAifarApps(t *testing.T) {
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "db-1"}, Bundle{
		Version:     "24.0.9",
		ArchivePath: filepath.Join("resources", "docker", "24.0.9", "aifar-docker-static-24.0.9-linux-x86_64.tar"),
	}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(remote.uploads, "\n"), "->/aifar/apps/_work/docker-24.0.9-") {
		t.Fatalf("default work dir should use /aifar/apps: %s", strings.Join(remote.uploads, "\n"))
	}
}

func TestInstallerKeepsDockerDaemonConfigUnderInstallRoot(t *testing.T) {
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "db-1", DeployDir: "/aifar/apps"}, Bundle{
		Version:     "24.0.9",
		ArchivePath: filepath.Join("resources", "docker", "24.0.9", "aifar-docker-static-24.0.9-linux-x86_64.tar"),
	}, installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.installScript, `DAEMON_DIR="$INSTALL_ROOT/daemon"`) {
		t.Fatalf("daemon directory should be under install root:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `"data-root": "$DATA_ROOT"`) {
		t.Fatalf("daemon data-root should be under install root:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `--config-file=$DAEMON_CONFIG --containerd=/run/containerd/containerd.sock`) {
		t.Fatalf("docker.service should use the custom daemon config:\n%s", remote.installScript)
	}
	if strings.Contains(remote.installScript, "/etc/docker/daemon.json") {
		t.Fatalf("install script should not depend on /etc/docker/daemon.json:\n%s", remote.installScript)
	}
}

func TestDockerScriptsRenderEmbeddedTemplates(t *testing.T) {
	install, err := installScript("24.0.9", "/aifar/apps/_work/docker", "/tmp/docker.tar", "/aifar/apps/docker/24.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(install, "{{") {
		t.Fatalf("install script contains unrendered template markers:\n%s", install)
	}
	if !strings.Contains(install, "VERSION='24.0.9'") || !strings.Contains(install, "WORK_DIR='/aifar/apps/_work/docker'") {
		t.Fatalf("install script did not render core variables:\n%s", install)
	}
	uninstall, err := uninstallScript("24.0.9", "/aifar/apps/docker/24.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uninstall, "{{") {
		t.Fatalf("uninstall script contains unrendered template markers:\n%s", uninstall)
	}
	if !strings.Contains(uninstall, "INSTALL_ROOT='/aifar/apps/docker/24.0.9'") {
		t.Fatalf("uninstall script did not render install root:\n%s", uninstall)
	}
}

func TestInstallScriptInstallsRPMsBeforeCheckingTar(t *testing.T) {
	install, err := installScript("24.0.9", "/aifar/apps/_work/docker", "/tmp/docker.tar", "/aifar/apps/docker/24.0.9")
	if err != nil {
		t.Fatal(err)
	}
	rpmIndex := strings.Index(install, "installing local RPM dependencies when available")
	checkIndex := strings.Index(install, "checking required commands")
	if rpmIndex < 0 || checkIndex < 0 {
		t.Fatalf("install script missing RPM install or command check block:\n%s", install)
	}
	if rpmIndex > checkIndex {
		t.Fatalf("RPM dependencies must be installed before checking tar/gzip:\n%s", install)
	}
	if !strings.Contains(install, "tar is required after local RPM dependency installation") {
		t.Fatalf("tar check should run after local RPM installation:\n%s", install)
	}
	if !strings.Contains(install, "gzip is required after local RPM dependency installation") {
		t.Fatalf("gzip check should run after local RPM installation:\n%s", install)
	}
}

func TestInstallScriptEnablesRemoteAPIAndBridgeCIDR(t *testing.T) {
	install, err := installScript("24.0.9", "/aifar/apps/_work/docker", "/tmp/docker.tar", "/aifar/apps/docker/24.0.9", InstallOptions{
		BridgeCIDR:    "172.30.0.1/16",
		RemoteAPIPort: 2376,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`BRIDGE_CIDR='172.30.0.1/16'`,
		`REMOTE_API_PORT=2376`,
		`"bip": "$BRIDGE_CIDR"`,
		`-H tcp://0.0.0.0:$REMOTE_API_PORT`,
		`docker -H "tcp://127.0.0.1:$REMOTE_API_PORT" version`,
		`open_firewall_ports "$REMOTE_API_PORT"`,
		`allow_selinux_ports docker_port_t "$REMOTE_API_PORT"`,
	} {
		if !strings.Contains(install, want) {
			t.Fatalf("install script missing %q:\n%s", want, install)
		}
	}
}

func TestRunLogsSuccessfulStderrAsInfo(t *testing.T) {
	remote := &installerFakeRemote{stderr: "warning: rpm dependency installation reported issues"}
	installer := NewInstaller(remote)
	logger := &installerCollectingLogger{}
	if _, err := installer.run(context.Background(), store.Server{Name: "db-1"}, "sh install-docker.sh", logger, ""); err != nil {
		t.Fatal(err)
	}
	if len(logger.errors) != 0 {
		t.Fatalf("successful stderr should not be logged as error: %+v", logger.errors)
	}
	if !strings.Contains(strings.Join(logger.infos, "\n"), remote.stderr) {
		t.Fatalf("successful stderr should be preserved as info: %+v", logger.infos)
	}
}
