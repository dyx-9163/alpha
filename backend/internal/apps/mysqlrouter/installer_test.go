package mysqlrouter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type routerInstallerFakeRemote struct {
	commands            []string
	uploads             []string
	routerInstallScript string
}

func (f *routerInstallerFakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	f.commands = append(f.commands, command)
	return adapter.CommandResult{Stdout: "ok"}, nil
}

func (f *routerInstallerFakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	f.uploads = append(f.uploads, filepath.Base(localPath)+"->"+remotePath)
	if strings.HasSuffix(remotePath, "/install-mysql-router.sh") {
		content, err := os.ReadFile(localPath)
		if err != nil {
			return err
		}
		f.routerInstallScript = string(content)
	}
	return nil
}

func TestInstallerUploadsBundleAndRunsMySQLRouterScript(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	rpm := filepath.Join(root, "libaio.rpm")
	for _, file := range []string{archive, rpm} {
		if err := os.WriteFile(file, []byte("mysql"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	remote := &routerInstallerFakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "router-1", Host: "10.0.0.9", DeployDir: "/aifar/apps"}, Bundle{
		Version:     "8.0.36",
		ArchivePath: archive,
		RPMPaths:    []string{rpm},
	}, RouterInstallOptions{
		BasePort:          6446,
		BootstrapHost:     "10.0.0.1",
		BootstrapPort:     3306,
		BootstrapUser:     "root",
		BootstrapPassword: "Oversea.123",
		BindAddress:       "0.0.0.0",
	}, fakeLogger{})
	if err != nil {
		t.Fatal(err)
	}
	joinedUploads := strings.Join(remote.uploads, "\n")
	if !strings.Contains(joinedUploads, "mysql-aifar-8.0.36-official-bundle.tar->/aifar/apps/_work/mysql-router-8.0.36-") {
		t.Fatalf("mysql router bundle upload missing: %s", joinedUploads)
	}
	if !strings.Contains(strings.Join(remote.commands, "\n"), "install-mysql-router.sh") {
		t.Fatalf("router install command missing: %+v", remote.commands)
	}
	for _, want := range []string{
		"mysql-router-*-linux",
		`--bootstrap "$BOOTSTRAP_URI"`,
		`--conf-base-port "$BASE_PORT"`,
		`SERVICE_NAME="aifar-mysql-router"`,
		`ExecStart=$ROUTER_BASE/bin/mysqlrouter -c $ROUTER_DIR/mysqlrouter.conf`,
		`open_firewall_ports "$BASE_PORT" "$((BASE_PORT + 1))" "$((BASE_PORT + 2))" "$((BASE_PORT + 3))"`,
		`allow_selinux_ports mysqld_port_t "$BASE_PORT" "$((BASE_PORT + 1))" "$((BASE_PORT + 2))" "$((BASE_PORT + 3))"`,
	} {
		if !strings.Contains(remote.routerInstallScript, want) {
			t.Fatalf("router installer should contain %q:\n%s", want, remote.routerInstallScript)
		}
	}
}

func TestMySQLRouterScriptsRenderTemplates(t *testing.T) {
	install, err := installRouterScript(RouterInstallScriptRequest{
		Version:           "8.0.36",
		WorkDir:           "/aifar/apps/_work/mysql-router",
		ArchivePath:       "/tmp/mysql.tar",
		InstallRoot:       "/aifar/apps/mysql-router",
		BasePort:          6446,
		BootstrapHost:     "10.0.0.1",
		BootstrapPort:     3306,
		BootstrapUser:     "root",
		BootstrapPassword: "Oversea.123",
		BindAddress:       "0.0.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(install, "{{") {
		t.Fatalf("router install script contains unrendered template markers:\n%s", install)
	}
	if !strings.Contains(install, "BASE_PORT=6446") || !strings.Contains(install, "BOOTSTRAP_HOST='10.0.0.1'") {
		t.Fatalf("router install script did not render core variables:\n%s", install)
	}
	uninstall, err := uninstallRouterScript("8.0.36", "/aifar/apps/mysql-router", "/aifar/apps/mysql-router/8.0.36", 6446)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uninstall, "{{") {
		t.Fatalf("router uninstall script contains unrendered template markers:\n%s", uninstall)
	}
	if !strings.Contains(uninstall, `SERVICE_NAME="aifar-mysql-router"`) || !strings.Contains(uninstall, `LEGACY_SERVICE_NAME="aifar-mysql-router-$BASE_PORT"`) || !strings.Contains(uninstall, "BASE_PORT=6446") {
		t.Fatalf("router uninstall script did not render service identity:\n%s", uninstall)
	}
}
