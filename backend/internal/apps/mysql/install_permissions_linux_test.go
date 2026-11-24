package mysql

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStandaloneInstallKeepsMySQLClientsAccessibleToSSHUser(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("archive extraction must run as an unprivileged SSH user")
	}
	fixture := newStandaloneCredentialFixture(t)
	installRoot := filepath.Join(fixture.root, "mysql")
	bundleDir := filepath.Join(fixture.root, "bundle")
	distributionDir := filepath.Join(fixture.root, "distributions")
	if err := os.MkdirAll(bundleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, distribution := range []struct {
		name    string
		clients []string
	}{
		{"mysql-8.0.36-minimal", []string{"mysqld", "mysql", "mysqladmin"}},
		{"mysql-shell-8.0.36", []string{"mysqlsh"}},
	} {
		bin := filepath.Join(distributionDir, distribution.name, "bin")
		if err := os.MkdirAll(bin, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, client := range distribution.clients {
			mustWriteCredentialFile(t, filepath.Join(bin, client), "#!/bin/sh\nexit 0\n", 0o755)
		}
		archive := filepath.Join(bundleDir, distribution.name+"-linux.tar.gz")
		if output, err := exec.Command("tar", "-czf", archive, "-C", distributionDir, distribution.name).CombinedOutput(); err != nil {
			t.Fatalf("create sample distribution: %v\n%s", err, output)
		}
	}
	archive := filepath.Join(fixture.root, "bundle.tar")
	if output, err := exec.Command("tar", "-cf", archive, "-C", bundleDir, ".").CombinedOutput(); err != nil {
		t.Fatalf("create sample bundle: %v\n%s", err, output)
	}

	script, err := installStandaloneScript(InstallScriptRequest{
		Version: "8.0.36", WorkDir: fixture.workDir, ArchivePath: archive, InstallRoot: installRoot, Port: 3306,
	})
	if err != nil {
		t.Fatal(err)
	}
	credentials := strings.Index(script, "\ndump_mysql_diagnostics()")
	setup := strings.Index(script, "\necho \"preparing MySQL install directories\"")
	initialize := strings.Index(script, "\nNEED_SECURE=0")
	if credentials < 0 || setup < credentials || initialize < setup {
		t.Fatal("could not find the standalone installer setup")
	}
	// The harness performs real extraction, copy and chmod without changing host users or services.
	fakeSudo := filepath.Join(fixture.root, "fake-sudo")
	mustWriteCredentialFile(t, fakeSudo, "#!/bin/sh\ncase \"$1\" in systemctl|chown|useradd) exit 0;; esac\nexec \"$@\"\n", 0o700)
	setupScript := strings.ReplaceAll(script[setup:initialize], "/etc/systemd/system", filepath.Join(fixture.root, "systemd"))
	harness := script[:credentials] + "\nSUDO=\"$TEST_SUDO\"\n" + setupScript + "\nstat -c '%a' \"$SECURE_CLIENT_FILE\"\n"
	cmd := exec.Command("/bin/sh", "-c", harness, "install-permissions", fixture.contextPath)
	cmd.Env = append(os.Environ(), "TEST_SUDO="+fakeSudo)
	result := runShellCommand(cmd)
	if result.err != nil {
		t.Fatalf("standalone setup: %v\n%s\n%s", result.err, result.stdout, result.stderr)
	}
	if !strings.HasSuffix(strings.TrimSpace(result.stdout), "\n600") {
		t.Fatal("administrator options file lost its private mode")
	}

	// The SSH account is not the owner after the installer's chown on a real target.
	for _, path := range []string{"", "mysql", "mysql/bin", "mysql/bin/mysql", "mysql/bin/mysqladmin",
		"mysql/bin/mysqld", "mysql-shell", "mysql-shell/bin", "mysql-shell/bin/mysqlsh"} {
		info, err := os.Stat(filepath.Join(installRoot, path))
		if err != nil {
			t.Fatal(err)
		}
		required := os.FileMode(0o005)
		if info.IsDir() {
			required = 0o001
		}
		if info.Mode().Perm()&required != required {
			t.Errorf("%q mode %03o blocks the SSH user after ownership changes", path, info.Mode().Perm())
		}
	}
	info, err := os.Stat(filepath.Join(installRoot, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("MySQL data directory mode %03o exposes group/other access", info.Mode().Perm())
	}
}
