package mysql

import (
	"strings"
	"testing"
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
		`SECRET_CONTEXT="$WORK_DIR/secret-context.cnf"`,
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
		`SECRET_CONTEXT="$WORK_DIR/secret-context.cnf"`,
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
