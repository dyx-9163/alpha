package redis

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
	if strings.HasSuffix(remotePath, "/install-redis.sh") {
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

func TestSelectBundleDiscoversRedisRPMs(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	rpmDir := filepath.Join(root, "rpms")
	if err := os.MkdirAll(rpmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rpmDir, "make.rpm"), []byte("rpm"), 0o644); err != nil {
		t.Fatal(err)
	}
	bundle, err := SelectBundle([]store.Resource{{App: "redis", Part: "backend", Version: "7.2.14", Path: archive}}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != "7.2.14" || bundle.ArchivePath != archive || len(bundle.RPMPaths) != 1 {
		t.Fatalf("unexpected bundle: %+v", bundle)
	}
}

func TestInstallerUploadsArchiveAndRunsRealRedisScript(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "db-1", DeployDir: "/aifar/apps"}, Bundle{
		Version:     "7.2.14",
		ArchivePath: archive,
	}, 6379, "Oversea.123", installerTestLogger{})
	if err != nil {
		t.Fatal(err)
	}
	joinedUploads := strings.Join(remote.uploads, "\n")
	if !strings.Contains(joinedUploads, "redis-7.2.14.tar.gz->/aifar/apps/_work/redis-7.2.14-") {
		t.Fatalf("archive upload missing: %s", joinedUploads)
	}
	if !strings.Contains(strings.Join(remote.commands, "\n"), "install-redis.sh") {
		t.Fatalf("install command missing: %+v", remote.commands)
	}
	if !strings.Contains(remote.installScript, `make -C "$SRC_DIR" BUILD_TLS=no MALLOC=libc`) {
		t.Fatalf("installer should build redis from source:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `requirepass $REDIS_PASSWORD`) {
		t.Fatalf("installer should configure redis password:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `"$INSTALL_ROOT/bin/redis-cli" -p "$PORT" -a "$REDIS_PASSWORD" --no-auth-warning ping`) {
		t.Fatalf("installer should verify redis-cli ping:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `open_firewall_ports "$PORT"`) ||
		!strings.Contains(remote.installScript, `allow_selinux_ports redis_port_t "$PORT"`) {
		t.Fatalf("installer should open firewall and SELinux rules for the Redis port:\n%s", remote.installScript)
	}
}

func TestInstallerCanInstallRedisBinariesForSentinelOnlyNode(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "redis-7.2.14.tar.gz")
	if err := os.WriteFile(archive, []byte("redis"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := &installerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.InstallBinariesWithLanguage(context.Background(), store.Server{Name: "sentinel-1", DeployDir: "/aifar/apps"}, Bundle{
		Version:     "7.2.14",
		ArchivePath: archive,
	}, 6379, "Oversea.123", installerTestLogger{}, "en")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(remote.installScript, `Redis binaries installed for Sentinel`) {
		t.Fatalf("installer should render sentinel-only binary install script:\n%s", remote.installScript)
	}
	if strings.Contains(remote.installScript, `systemctl enable --now "$SERVICE_NAME"`) {
		t.Fatalf("sentinel-only binary install should not start redis data service:\n%s", remote.installScript)
	}
}

func TestRedisStandaloneScriptsRenderTemplates(t *testing.T) {
	install, err := installStandaloneScript("7.2.14", "/aifar/apps/_work/redis", "/tmp/redis.tar.gz", "/aifar/apps/redis", 6380, "Oversea.123")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(install, "{{") {
		t.Fatalf("install script contains unrendered template markers:\n%s", install)
	}
	if !strings.Contains(install, "VERSION='7.2.14'") || !strings.Contains(install, "PORT=6380") {
		t.Fatalf("install script did not render core standalone variables:\n%s", install)
	}
	uninstall, err := uninstallStandaloneScript("7.2.14", "/aifar/apps/redis", "/aifar/apps/redis/7.2.14", 6380)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uninstall, "{{") {
		t.Fatalf("uninstall script contains unrendered template markers:\n%s", uninstall)
	}
	if !strings.Contains(uninstall, `SERVICE_NAME="aifar-redis"`) || !strings.Contains(uninstall, `LEGACY_SERVICE_NAME="aifar-redis-$PORT"`) || !strings.Contains(uninstall, "PORT=6380") {
		t.Fatalf("uninstall script did not render standalone service identity:\n%s", uninstall)
	}
}

func TestRedisSentinelScriptUsesConfiguredMasterName(t *testing.T) {
	script, err := configureSentinelNodeScript(SentinelNodeConfig{
		Version:      "7.2.14",
		InstallRoot:  "/aifar/apps/redis/7.2.14",
		RedisPort:    6379,
		SentinelPort: 26379,
		Password:     "Oversea.123",
		MasterName:   "orders-primary",
		MasterHost:   "10.0.0.2",
		MasterPort:   6379,
		Quorum:       2,
		Role:         "replica",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "sentinel monitor aifar-master") {
		t.Fatalf("sentinel script should not hard-code master name:\n%s", script)
	}
	if !strings.Contains(script, "MASTER_NAME='orders-primary'") || !strings.Contains(script, "sentinel monitor $MASTER_NAME $MASTER_HOST $MASTER_PORT $QUORUM") {
		t.Fatalf("sentinel script did not render configured master name:\n%s", script)
	}
	if !strings.Contains(script, `user default on >$REDIS_PASSWORD allcommands allkeys allchannels`) {
		t.Fatalf("sentinel script should configure default ACL user for client connections:\n%s", script)
	}
	if !strings.Contains(script, `sentinel sentinel-pass $REDIS_PASSWORD`) {
		t.Fatalf("sentinel script should configure sentinel-to-sentinel authentication:\n%s", script)
	}
	if !strings.Contains(script, `redis-cli" -p "$SENTINEL_PORT" -a "$REDIS_PASSWORD" --no-auth-warning sentinel masters`) {
		t.Fatalf("sentinel verification should authenticate to sentinel:\n%s", script)
	}
	if !strings.Contains(script, `journalctl -u "$SENTINEL_SERVICE" -n 120 --no-pager`) || !strings.Contains(script, `sed -n '1,160p' "$SENTINEL_CONFIG"`) {
		t.Fatalf("sentinel script should print service diagnostics on startup failure:\n%s", script)
	}
	if strings.Contains(script, "Requires=$SERVICE_NAME.service") {
		t.Fatalf("sentinel unit should not require local Redis data service:\n%s", script)
	}
	if !strings.Contains(script, `open_firewall_ports "$REDIS_PORT" "$SENTINEL_PORT"`) ||
		!strings.Contains(script, `allow_selinux_ports redis_port_t "$REDIS_PORT" "$SENTINEL_PORT"`) {
		t.Fatalf("sentinel script should open Redis and Sentinel ports:\n%s", script)
	}
}

func TestRedisClusterScriptOpensServiceAndBusPorts(t *testing.T) {
	script, err := enableClusterNodeScript(ClusterNodeConfig{
		Version:     "7.2.14",
		InstallRoot: "/aifar/apps/redis",
		Port:        6379,
		Password:    "Oversea.123",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`BUS_PORT=$((PORT + 10000))`,
		`open_firewall_ports "$PORT" "$BUS_PORT"`,
		`allow_selinux_ports redis_port_t "$PORT" "$BUS_PORT"`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("cluster script missing %q:\n%s", want, script)
		}
	}
}
