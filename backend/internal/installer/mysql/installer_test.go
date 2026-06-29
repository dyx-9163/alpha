package mysql

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
	if strings.HasSuffix(remotePath, "/install-mysql.sh") {
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

func TestSelectBundleDiscoversMySQLRPMs(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	if err := os.WriteFile(archive, []byte("mysql"), 0o644); err != nil {
		t.Fatal(err)
	}
	rpmDir := filepath.Join(root, "rpms")
	if err := os.MkdirAll(rpmDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rpmDir, "libaio.rpm"), []byte("rpm"), 0o644); err != nil {
		t.Fatal(err)
	}

	bundle, err := SelectBundle([]store.Resource{{App: "mysql", Part: "backend", Version: "8.0.36", Path: archive}}, "latest")
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Version != "8.0.36" || bundle.ArchivePath != archive || len(bundle.RPMPaths) != 1 {
		t.Fatalf("unexpected mysql bundle: %+v", bundle)
	}
	if err := VerifyBundle(bundle); err != nil {
		t.Fatal(err)
	}
}

func TestInstallerUploadsBundleAndRunsMySQLScript(t *testing.T) {
	root := t.TempDir()
	archive := filepath.Join(root, "mysql-aifar-8.0.36-official-bundle.tar")
	rpm := filepath.Join(root, "libaio.rpm")
	for _, file := range []string{archive, rpm} {
		if err := os.WriteFile(file, []byte("mysql"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	remote := &fakeRemote{}
	installer := NewInstaller(remote)
	err := installer.Install(context.Background(), store.Server{Name: "db-1", Host: "10.0.0.4", DeployDir: "/aifar/apps"}, Bundle{
		Version:     "8.0.36",
		ArchivePath: archive,
		RPMPaths:    []string{rpm},
	}, InstallOptions{Port: 3307, RootUser: "root", RootPassword: "Oversea.123"}, testLogger{})
	if err != nil {
		t.Fatal(err)
	}
	joinedUploads := strings.Join(remote.uploads, "\n")
	if !strings.Contains(joinedUploads, "mysql-aifar-8.0.36-official-bundle.tar->/aifar/apps/_work/mysql-8.0.36-") {
		t.Fatalf("mysql bundle upload missing: %s", joinedUploads)
	}
	if !strings.Contains(strings.Join(remote.commands, "\n"), "install-mysql.sh") {
		t.Fatalf("install command missing: %+v", remote.commands)
	}
	if !strings.Contains(remote.installScript, `--initialize-insecure --user="$MYSQL_USER"`) {
		t.Fatalf("installer should initialize mysql data directory:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `ExecStart=$MYSQL_BASE/bin/mysqld --defaults-file=$CONFIG_FILE`) {
		t.Fatalf("installer should write a systemd mysqld service:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `MYSQL_SHELL_BASE="$INSTALL_ROOT/mysql-shell"`) || !strings.Contains(remote.installScript, `installing MySQL Shell`) {
		t.Fatalf("installer should extract bundled mysql shell for cluster bootstrap:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, "report_host=10.0.0.4") {
		t.Fatalf("installer should set report_host for cluster communication:\n%s", remote.installScript)
	}
	for _, want := range []string{
		"server-id=",
		"log-bin=$LOG_DIR/mysql-bin",
		"relay-log=$LOG_DIR/mysql-relay-bin",
		"binlog_format=ROW",
		"log_replica_updates=ON",
		"gtid_mode=ON",
		"enforce_gtid_consistency=ON",
		"binlog_transaction_dependency_tracking=WRITESET",
	} {
		if !strings.Contains(remote.installScript, want) {
			t.Fatalf("installer should preconfigure InnoDB Cluster option %q:\n%s", want, remote.installScript)
		}
	}
	if !strings.Contains(remote.installScript, `MYSQL_PWD="$ROOT_PASSWORD" "$MYSQL_BASE/bin/mysqladmin" --protocol=tcp`) {
		t.Fatalf("installer should verify password login via mysqladmin:\n%s", remote.installScript)
	}
	if !strings.Contains(remote.installScript, `if [ "$NEED_SECURE" = "1" ]; then`) ||
		!strings.Contains(remote.installScript, "Existing MySQL data directory is present") {
		t.Fatalf("installer should use password verification for existing data directories:\n%s", remote.installScript)
	}
}

func TestMySQLStandaloneScriptsRenderTemplates(t *testing.T) {
	install, err := installStandaloneScript(InstallScriptRequest{
		Version:      "8.0.36",
		WorkDir:      "/aifar/apps/_work/mysql",
		ArchivePath:  "/tmp/mysql.tar",
		InstallRoot:  "/aifar/apps/mysql/8.0.36",
		ReportHost:   "10.0.0.1",
		Port:         3307,
		ServerID:     12345,
		RootUser:     "root",
		RootPassword: "Oversea.123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(install, "{{") {
		t.Fatalf("install script contains unrendered template markers:\n%s", install)
	}
	if !strings.Contains(install, "VERSION='8.0.36'") || !strings.Contains(install, "PORT=3307") || !strings.Contains(install, "report_host=10.0.0.1") {
		t.Fatalf("install script did not render core standalone variables:\n%s", install)
	}
	if !strings.Contains(install, "server-id=12345") || !strings.Contains(install, "gtid_mode=ON") || !strings.Contains(install, "binlog_transaction_dependency_tracking=WRITESET") {
		t.Fatalf("install script did not render InnoDB Cluster bootstrap-friendly config:\n%s", install)
	}
	bootstrap, err := bootstrapInnoDBClusterScript(InnoDBClusterBootstrapRequest{
		ClusterName:  "aifarCluster",
		InstallRoot:  "/aifar/apps/mysql/8.0.36",
		RootUser:     "root",
		RootPassword: "Oversea.123",
		Nodes:        []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(bootstrap, `MYSQLSH="$INSTALL_ROOT/mysql-shell/bin/mysqlsh"`) || !strings.Contains(bootstrap, `"$MYSQLSH" --js --file "$JS_FILE"`) {
		t.Fatalf("bootstrap script should use bundled mysql shell:\n%s", bootstrap)
	}
	if !strings.Contains(bootstrap, "clusterAdminPassword is not allowed") || !strings.Contains(bootstrap, "cluster admin account already exists") {
		t.Fatalf("bootstrap script should retry existing cluster admin accounts:\n%s", bootstrap)
	}
	uninstall, err := uninstallStandaloneScript("8.0.36", "/aifar/apps/mysql/8.0.36", 3307)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(uninstall, "{{") {
		t.Fatalf("uninstall script contains unrendered template markers:\n%s", uninstall)
	}
	if !strings.Contains(uninstall, "SERVICE_NAME=\"aifar-mysql-$PORT\"") || !strings.Contains(uninstall, "PORT=3307") {
		t.Fatalf("uninstall script did not render standalone service identity:\n%s", uninstall)
	}
}

func TestMySQLServerIDIsStableAndDistinct(t *testing.T) {
	one := mysqlServerID(store.Server{ID: "srv-1", Host: "192.168.74.128"}, 3306)
	two := mysqlServerID(store.Server{ID: "srv-2", Host: "192.168.74.129"}, 3306)
	again := mysqlServerID(store.Server{ID: "srv-1", Host: "192.168.74.128"}, 3306)
	if one == 0 || two == 0 {
		t.Fatalf("mysql server id must be non-zero: %d %d", one, two)
	}
	if one == two {
		t.Fatalf("mysql server ids should differ across servers: %d", one)
	}
	if one != again {
		t.Fatalf("mysql server id should be stable, got %d then %d", one, again)
	}
}
