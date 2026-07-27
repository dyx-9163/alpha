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
	replacementDump, movedDump := filepath.Join(paths.root, "replacement-dump"), filepath.Join(paths.root, "opened-dump")
	if err := os.Mkdir(replacementDump, 0o700); err != nil {
		t.Fatal(err)
	}
	result := runLogicalBackupLinux(t, paths, map[string]string{"REPLACE_FROM": replacement, "REPLACE_TO": filepath.Join(paths.taskDir, "secret-context.cnf"), "REPLACE_DUMP_FROM": replacementDump, "REPLACE_DUMP_TO": filepath.Join(paths.taskDir, "dump"), "REPLACE_DUMP_OLD": movedDump})
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
	for _, want := range []string{
		`util.dumpInstance("/proc/self/fd/`,
		"consistent: true",
		"threads: 4",
		`compression: "zstd"`,
		"users: false",
		"showProgress: false",
		`excludeSchemas: ["information_schema", "mysql", "mysql_innodb_cluster_metadata", "performance_schema", "sys"]`,
		`options.maxRate = "32M"`,
	} {
		if !strings.Contains(result.js, want) {
			t.Fatalf("mysqlsh backup JS missing %q: %s", want, result.js)
		}
	}
	if strings.Contains(result.js, "original-secret") || strings.Contains(result.js, "replacement-secret") {
		t.Fatalf("mysqlsh JS leaked a secret: %q", result.js)
	}
	if data, err := os.ReadFile(filepath.Join(movedDump, "aifar-fake-marker")); err != nil || strings.TrimSpace(string(data)) != "backup" {
		t.Fatalf("backup dump write escaped the opened descriptor: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(paths.taskDir, "dump", "aifar-fake-marker")); !os.IsNotExist(err) {
		t.Fatalf("backup wrote through replacement dump pathname: %v", err)
	}
	if got := strings.Split(strings.TrimSpace(result.args), "\n"); len(got) != 4 || !strings.HasPrefix(got[0], "--defaults-file=/proc/self/fd/") || got[1] != "--js" || got[2] != "--file" || !strings.HasPrefix(got[3], "/proc/self/fd/") {
		t.Fatalf("mysqlsh argv order/count is not exact: %#v", got)
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

func TestLogicalBackupScriptLinuxRejectsUnsafeBackupPathsAndInputs(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux O_NOFOLLOW semantics")
	}
	tests := []struct {
		name   string
		mutate func(t *testing.T, paths logicalBackupLinuxPaths)
	}{
		{
			name: "backup root symlink",
			mutate: func(t *testing.T, paths logicalBackupLinuxPaths) {
				t.Helper()
				moved := filepath.Join(paths.root, "moved-backup")
				if err := os.Rename(filepath.Join(paths.root, "backup"), moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, filepath.Join(paths.root, "backup")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "dump symlink",
			mutate: func(t *testing.T, paths logicalBackupLinuxPaths) {
				t.Helper()
				external := filepath.Join(paths.root, "external-dump")
				if err := os.Mkdir(external, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(paths.taskDir, "dump")); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, filepath.Join(paths.taskDir, "dump")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "secret wrong mode",
			mutate: func(t *testing.T, paths logicalBackupLinuxPaths) {
				t.Helper()
				if err := os.Chmod(filepath.Join(paths.taskDir, "secret-context.cnf"), 0o640); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "secret directory",
			mutate: func(t *testing.T, paths logicalBackupLinuxPaths) {
				t.Helper()
				secret := filepath.Join(paths.taskDir, "secret-context.cnf")
				if err := os.Remove(secret); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(secret, 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "missing mysql shell",
			mutate: func(t *testing.T, paths logicalBackupLinuxPaths) {
				t.Helper()
				if err := os.Remove(filepath.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := setupLogicalBackupLinux(t)
			test.mutate(t, paths)
			result := runLogicalBackupLinux(t, paths, nil)
			if result.err == nil || result.args != "" {
				t.Fatalf("unsafe controlled input was accepted: err=%v args=%q output=%q", result.err, result.args, result.output)
			}
		})
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
	if err := os.WriteFile(filepath.Join(paths.taskDir, "dump", "aifar-fake-marker"), []byte("backup\n"), 0o600); err != nil {
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
	if err := os.WriteFile(filepath.Join(paths.taskDir, "dump", "aifar-fake-marker"), []byte("backup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	replacementDump, movedDump := filepath.Join(paths.root, "replacement-dump"), filepath.Join(paths.root, "opened-dump")
	if err := os.Mkdir(replacementDump, 0o700); err != nil {
		t.Fatal(err)
	}
	result = runLogicalBackupLinux(t, paths, map[string]string{"REPLACE_DUMP_FROM": replacementDump, "REPLACE_DUMP_TO": filepath.Join(paths.taskDir, "dump"), "REPLACE_DUMP_OLD": movedDump})
	if result.err != nil || strings.TrimSpace(result.dump) != "backup" {
		t.Fatalf("restore did not read marker through dump descriptor: %v %q", result.err, result.dump)
	}
	if _, err := os.Stat(filepath.Join(paths.taskDir, "dump", "aifar-fake-marker")); !os.IsNotExist(err) {
		t.Fatalf("restore used replacement dump pathname: %v", err)
	}
}

func TestLogicalRestoreScriptLinuxPropagatesMySQLShellFailure(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(paths.taskDir, "dump", "aifar-fake-marker"), []byte("backup\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := runLogicalBackupLinux(t, paths, map[string]string{"FAKE_EXIT": "29"})
	if result.err == nil {
		t.Fatal("mysqlsh restore failure was swallowed")
	}
}

type logicalBackupLinuxPaths struct{ root, taskDir, script, args, defaults, js, dump string }
type logicalBackupLinuxResult struct {
	args, defaults, js, dump, output string
	err                              error
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
	args, defaults, js, dump := filepath.Join(root, "args"), filepath.Join(root, "defaults"), filepath.Join(root, "js"), filepath.Join(root, "dump-read")
	fake := "#!/bin/sh\nset -eu\nif [ -n \"${REPLACE_FROM:-}\" ]; then mv \"$REPLACE_FROM\" \"$REPLACE_TO\"; fi\nif [ -n \"${REPLACE_DUMP_FROM:-}\" ]; then mv \"$REPLACE_DUMP_TO\" \"$REPLACE_DUMP_OLD\"; mv \"$REPLACE_DUMP_FROM\" \"$REPLACE_DUMP_TO\"; fi\ndefaults=; js=\nwhile [ \"$#\" -gt 0 ]; do printf '%s\\n' \"$1\" >> \"$ARGV\"; case \"$1\" in --defaults-file=*) defaults=${1#--defaults-file=};; --file) shift; printf '%s\\n' \"$1\" >> \"$ARGV\"; js=$1;; esac; shift; done\ncat \"$defaults\" > \"$DEFAULTS\"\ncat \"$js\" > \"$JS\"\ndump=$(sed -n 's/.*util\\.[A-Za-z]*(\"\\([^\"]*\\)\".*/\\1/p' \"$js\")\nif grep -q 'dumpInstance' \"$js\"; then printf 'backup\\n' > \"$dump/aifar-fake-marker\"; else cat \"$dump/aifar-fake-marker\" > \"$DUMP\"; fi\nexit \"${FAKE_EXIT:-0}\"\n"
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
	return logicalBackupLinuxPaths{root, taskDir, scriptPath, args, defaults, js, dump}
}

func runLogicalBackupLinux(t *testing.T, paths logicalBackupLinuxPaths, values map[string]string) logicalBackupLinuxResult {
	t.Helper()
	command := exec.Command("/bin/sh", paths.script)
	command.Env = append(os.Environ(), "ARGV="+paths.args, "DEFAULTS="+paths.defaults, "JS="+paths.js, "DUMP="+paths.dump)
	for key, value := range values {
		command.Env = append(command.Env, key+"="+value)
	}
	output, err := command.CombinedOutput()
	read := func(path string) string { data, _ := os.ReadFile(path); return string(data) }
	return logicalBackupLinuxResult{read(paths.args), read(paths.defaults), read(paths.js), read(paths.dump), string(output), err}
}
