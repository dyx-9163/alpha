package mysql

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLogicalBackupScriptLinuxUsesDescriptorBoundSecretAfterReplacement(t *testing.T) {
	// Production break caught: reopening the secret after validation would let a replacement file change mysqlsh authentication input.
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /proc/self/fd semantics")
	}
	paths := setupLogicalBackupLinux(t)
	replacement := filepath.Join(paths.root, "replacement.cnf")
	if err := os.WriteFile(replacement, []byte("replacement-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runLogicalBackupLinux(t, paths, map[string]string{"REPLACE_FROM": replacement, "REPLACE_TO": filepath.Join(paths.taskDir, "secret-context.cnf")})
	if result.err != nil {
		t.Fatalf("script failed: %v: %s", result.err, result.output)
	}
	if !strings.Contains(result.args, "--defaults-file=/proc/self/fd/") || !strings.Contains(result.args, "/proc/self/fd/") {
		t.Fatalf("mysqlsh did not receive descriptor-bound files: %q", result.args)
	}
	if strings.Contains(result.args, "original-secret") || strings.Contains(result.args, "replacement-secret") || strings.Contains(result.args, "--password") {
		t.Fatalf("mysqlsh arguments leaked a secret: %q", result.args)
	}
	if strings.TrimSpace(result.defaults) != "original-secret" {
		t.Fatalf("replacement won after secret open: %q", result.defaults)
	}
	if !strings.Contains(result.js, "consistent: true") || !strings.Contains(result.js, `compression: "zstd"`) {
		t.Fatalf("mysqlsh did not receive fixed dump options: %s", result.js)
	}
}

func TestLogicalBackupScriptLinuxRejectsFinalAndParentSymlinks(t *testing.T) {
	// Production break caught: accepting a final secret symlink or task-root symlink would escape the controlled backup work directory.
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux O_NOFOLLOW semantics")
	}
	for _, parent := range []bool{false, true} {
		paths := setupLogicalBackupLinux(t)
		if parent {
			moved := filepath.Join(paths.root, "moved-task")
			if err := os.Rename(paths.taskDir, moved); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(moved, paths.taskDir); err != nil {
				t.Fatal(err)
			}
		} else {
			external := filepath.Join(paths.root, "outside.cnf")
			if err := os.WriteFile(external, []byte("outside\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(filepath.Join(paths.taskDir, "secret-context.cnf")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, filepath.Join(paths.taskDir, "secret-context.cnf")); err != nil {
				t.Fatal(err)
			}
		}
		result := runLogicalBackupLinux(t, paths, nil)
		if result.err == nil || result.args != "" {
			t.Fatalf("controlled symlink was accepted: err=%v args=%q", result.err, result.args)
		}
	}
}

func TestLogicalBackupScriptLinuxPropagatesMySQLShellFailure(t *testing.T) {
	// Production break caught: swallowing a non-zero mysqlsh exit would mark a failed dump as successful.
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /proc/self/fd semantics")
	}
	paths := setupLogicalBackupLinux(t)
	result := runLogicalBackupLinux(t, paths, map[string]string{"FAKE_EXIT": "23"})
	if result.err == nil {
		t.Fatal("mysqlsh failure was swallowed")
	}
}

func TestLogicalRestoreScriptLinuxUsesDescriptorBoundInputsAndNoJSResidue(t *testing.T) {
	// Production break caught: restore reopening controlled files or leaving a pathname-backed JS file would permit a replacement attack between validation and mysqlsh execution.
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /proc/self/fd semantics")
	}
	paths := setupLogicalBackupLinux(t)
	script, err := RenderLogicalRestoreScript(LogicalRestoreScriptOptions{TaskID: "task-001", Threads: 4})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.script, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	result := runLogicalBackupLinux(t, paths, nil)
	if result.err != nil {
		t.Fatalf("restore script failed: %v: %s", result.err, result.output)
	}
	if !strings.Contains(result.args, "--defaults-file=/proc/self/fd/") || strings.Contains(result.args, "--password") {
		t.Fatalf("restore mysqlsh argv is unsafe: %q", result.args)
	}
	for _, want := range []string{"loadUsers: false", "ignoreExistingObjects: false", "skipBinlog: false", "showProgress: false", "threads: 4"} {
		if !strings.Contains(result.js, want) {
			t.Fatalf("restore JS missing %q: %s", want, result.js)
		}
	}
	for _, name := range []string{"logical-backup.js", "logical-restore.js"} {
		if _, err := os.Lstat(filepath.Join(paths.taskDir, name)); !os.IsNotExist(err) {
			t.Fatalf("persistent JS residue %q: %v", name, err)
		}
	}
}

type logicalBackupLinuxPaths struct{ root, taskDir, script, args, defaults, js string }
type logicalBackupLinuxResult struct {
	args, defaults, js, output string
	err                        error
}

func setupLogicalBackupLinux(t *testing.T) logicalBackupLinuxPaths {
	t.Helper()
	root := t.TempDir()
	oldBackupRoot, oldInstallRoot := mysqlBackupWorkRoot, mysqlInstallRoot
	mysqlBackupWorkRoot, mysqlInstallRoot = filepath.Join(root, "backup"), filepath.Join(root, "mysql")
	t.Cleanup(func() { mysqlBackupWorkRoot, mysqlInstallRoot = oldBackupRoot, oldInstallRoot })
	taskDir := filepath.Join(mysqlBackupWorkRoot, "task-001")
	for _, directory := range []string{mysqlBackupWorkRoot, taskDir, filepath.Join(taskDir, "dump"), filepath.Join(mysqlInstallRoot, "mysql-shell", "bin")} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(taskDir, "secret-context.cnf"), []byte("original-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args, defaults, js := filepath.Join(root, "args"), filepath.Join(root, "defaults"), filepath.Join(root, "js")
	fake := "#!/bin/sh\nset -eu\nif [ -n \"${REPLACE_FROM:-}\" ]; then mv \"$REPLACE_FROM\" \"$REPLACE_TO\"; fi\ndefaults=; js=\nfor arg in \"$@\"; do case \"$arg\" in --defaults-file=*) defaults=${arg#--defaults-file=};; esac; done\nwhile [ \"$#\" -gt 0 ]; do if [ \"$1\" = --file ]; then shift; js=$1; fi; printf '%s\\n' \"$1\" >> \"$ARGV\"; shift; done\ncat \"$defaults\" > \"$DEFAULTS\"\ncat \"$js\" > \"$JS\"\nexit \"${FAKE_EXIT:-0}\"\n"
	if err := os.WriteFile(filepath.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh"), []byte(fake), 0o700); err != nil {
		t.Fatal(err)
	}
	script, err := RenderLogicalBackupScript(LogicalBackupScriptOptions{TaskID: "task-001", Threads: 4, MaxRateMBps: 32})
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "logical-backup.sh")
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return logicalBackupLinuxPaths{root, taskDir, scriptPath, args, defaults, js}
}

func runLogicalBackupLinux(t *testing.T, paths logicalBackupLinuxPaths, values map[string]string) logicalBackupLinuxResult {
	t.Helper()
	command := exec.Command("/bin/sh", paths.script)
	command.Env = append(os.Environ(), "ARGV="+paths.args, "DEFAULTS="+paths.defaults, "JS="+paths.js)
	for key, value := range values {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	read := func(path string) string { data, _ := os.ReadFile(path); return string(data) }
	return logicalBackupLinuxResult{read(paths.args), read(paths.defaults), read(paths.js), string(output), err}
}
