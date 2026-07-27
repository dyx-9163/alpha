package mysql

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/installer/installerkit"
)

func TestRenderLogicalBackupScriptUsesFixedSafeDumpOptions(t *testing.T) {
	// Production break caught: changing dump options or interpolating a root password would create an inconsistent archive or leak a secret.
	script, err := RenderLogicalBackupScript(LogicalBackupScriptOptions{TaskID: "task-001", Threads: 4, MaxRateMBps: 32})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"consistent: true",
		"users: false",
		"showProgress: false",
		`compression: "zstd"`,
		`"mysql_innodb_cluster_metadata"`,
		`"secret-context.cnf"`,
		`--defaults-file=/proc/self/fd/`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("backup script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(strings.ToLower(script), "rootpassword") || strings.Contains(script, "--password") {
		t.Fatalf("backup script exposes a password channel:\n%s", script)
	}
}

func TestRenderLogicalRestoreScriptUsesFixedSafeLoadOptions(t *testing.T) {
	// Production break caught: enabling users, merge semantics, binlog skipping, or command-line credentials would make restore unsafe or leak a secret.
	script, err := RenderLogicalRestoreScript(LogicalRestoreScriptOptions{TaskID: "task-001", Threads: 4})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"loadUsers: false",
		"ignoreExistingObjects: false",
		"skipBinlog: false",
		"showProgress: false",
		`"secret-context.cnf"`,
		`--defaults-file=/proc/self/fd/`,
	} {
		if !strings.Contains(script, want) {
			t.Fatalf("restore script missing %q:\n%s", want, script)
		}
	}
	if strings.Contains(strings.ToLower(script), "rootpassword") || strings.Contains(script, "--password") {
		t.Fatalf("restore script exposes a password channel:\n%s", script)
	}
}

func TestRenderLogicalScriptsRejectUntrustedTaskAndNumericInputs(t *testing.T) {
	// Production break caught: interpolating unsafe task text or non-bounded numeric values would permit shell/JavaScript injection or unbounded MySQL Shell work.
	for _, options := range []LogicalBackupScriptOptions{
		{TaskID: "../outside", Threads: 4, MaxRateMBps: 1},
		{TaskID: "task-001;touch pwned", Threads: 4, MaxRateMBps: 1},
		{TaskID: "task-001", Threads: 0, MaxRateMBps: 1},
		{TaskID: "task-001", Threads: 65, MaxRateMBps: 1},
		{TaskID: "task-001", Threads: 4, MaxRateMBps: -1},
	} {
		if _, err := RenderLogicalBackupScript(options); err == nil {
			t.Fatalf("unsafe backup options unexpectedly accepted: %+v", options)
		}
	}
	if _, err := RenderLogicalRestoreScript(LogicalRestoreScriptOptions{TaskID: "task-001 $(id)", Threads: 4}); err == nil {
		t.Fatal("unsafe restore task ID unexpectedly accepted")
	}
}

func TestRenderLogicalScriptsIgnoreLocalInstallerOverrides(t *testing.T) {
	// Production break caught: honoring config/installers overrides would let a local writable file replace fixed backup/restore behavior.
	overrideRoot := t.TempDir()
	overridePath := filepath.Join(overrideRoot, "mysql", "backup", "logical-backup.sh")
	if err := os.MkdirAll(filepath.Dir(overridePath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overridePath, []byte("echo attacker override\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(installerkit.TemplateDirEnv, overrideRoot)
	script, err := RenderLogicalBackupScript(LogicalBackupScriptOptions{TaskID: "task-001", Threads: 4, MaxRateMBps: 1})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "attacker override") {
		t.Fatal("logical backup renderer honored a local installer override")
	}
}
