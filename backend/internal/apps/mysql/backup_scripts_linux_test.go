package mysql

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const logicalSentinelSecret = "AIFAR_TEST_SENTINEL_6f4c2b8a19e735d0"

type logicalScriptKind string

const (
	logicalBackupKind  logicalScriptKind = "backup"
	logicalRestoreKind logicalScriptKind = "restore"
)

func TestLogicalBackupScriptLinuxUsesDistinctDescriptorBoundInputsAfterReplacement(t *testing.T) {
	// Production break caught: reopening either controlled pathname, aliasing descriptors, or changing any dump option would let replacements alter a backup after validation.
	requireLinuxDescriptors(t)
	paths := setupLogicalScriptLinux(t, logicalBackupKind)
	replacementSecret := filepath.Join(paths.root, "replacement.cnf")
	writeLogicalTestFile(t, replacementSecret, "replacement-value\n", 0o600)
	replacementDump := filepath.Join(paths.root, "replacement-dump")
	mustMkdirLogical(t, replacementDump, 0o700)
	movedDump := filepath.Join(paths.root, "opened-dump")

	result := runLogicalScriptLinux(t, paths, map[string]string{
		"REPLACE_SECRET_FROM": replacementSecret,
		"REPLACE_SECRET_TO":   paths.secretPath,
		"REPLACE_DUMP_FROM":   replacementDump,
		"REPLACE_DUMP_TO":     paths.dumpDir,
		"REPLACE_DUMP_OLD":    movedDump,
	})
	if result.err != nil {
		t.Fatalf("backup script failed with exit %d: stdout=%q stderr=%q", logicalExitCode(result.err), result.stdout, result.stderr)
	}
	assertLogicalInvocation(t, result, logicalBackupKind, exactLogicalBackupJS(result.capture.Paths["dump"]))
	if got, want := result.capture.Defaults, logicalSentinelSecret+"\n"; got != want {
		t.Fatalf("backup defaults read = %q, want original descriptor content", got)
	}
	if got, want := result.capture.Marker, "backup\n"; got != want {
		t.Fatalf("backup marker capture = %q, want %q", got, want)
	}
	marker := filepath.Join(movedDump, "aifar-fake-marker")
	if data, err := os.ReadFile(marker); err != nil || string(data) != "backup\n" {
		t.Fatalf("backup marker was not written through opened dump descriptor: data=%q err=%v", data, err)
	}
	if _, err := os.Stat(filepath.Join(paths.dumpDir, "aifar-fake-marker")); !os.IsNotExist(err) {
		t.Fatalf("backup wrote through replacement dump pathname: %v", err)
	}
	assertLogicalSecretAbsent(t, result)
	assertNoLogicalJSResidue(t, paths.root)
}

func TestLogicalRestoreScriptLinuxUsesDistinctDescriptorBoundInputsAfterReplacement(t *testing.T) {
	// Production break caught: restore reopening secret/dump paths or changing any load option would allow post-validation replacement or unsafe merge behavior.
	requireLinuxDescriptors(t)
	paths := setupLogicalScriptLinux(t, logicalRestoreKind)
	replacementSecret := filepath.Join(paths.root, "replacement.cnf")
	writeLogicalTestFile(t, replacementSecret, "replacement-value\n", 0o600)
	replacementDump := filepath.Join(paths.root, "replacement-dump")
	mustMkdirLogical(t, replacementDump, 0o700)
	movedDump := filepath.Join(paths.root, "opened-dump")

	result := runLogicalScriptLinux(t, paths, map[string]string{
		"REPLACE_SECRET_FROM": replacementSecret,
		"REPLACE_SECRET_TO":   paths.secretPath,
		"REPLACE_DUMP_FROM":   replacementDump,
		"REPLACE_DUMP_TO":     paths.dumpDir,
		"REPLACE_DUMP_OLD":    movedDump,
	})
	if result.err != nil {
		t.Fatalf("restore script failed with exit %d: stdout=%q stderr=%q", logicalExitCode(result.err), result.stdout, result.stderr)
	}
	assertLogicalInvocation(t, result, logicalRestoreKind, exactLogicalRestoreJS(result.capture.Paths["dump"]))
	if got, want := result.capture.Defaults, logicalSentinelSecret+"\n"; got != want {
		t.Fatalf("restore defaults read = %q, want original descriptor content", got)
	}
	if got, want := result.capture.Marker, "backup\n"; got != want {
		t.Fatalf("restore marker capture = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(paths.dumpDir, "aifar-fake-marker")); !os.IsNotExist(err) {
		t.Fatalf("restore read through replacement dump pathname: %v", err)
	}
	assertLogicalSecretAbsent(t, result)
	assertNoLogicalJSResidue(t, paths.root)
}

func TestLogicalScriptsLinuxCaptureFreshExactArgvForEveryRun(t *testing.T) {
	// Production break caught: append-only or shared captures can satisfy assertions with stale argv from another script invocation.
	requireLinuxDescriptors(t)
	for _, kind := range []logicalScriptKind{logicalBackupKind, logicalRestoreKind} {
		t.Run(string(kind), func(t *testing.T) {
			paths := setupLogicalScriptLinux(t, kind)
			for run := 1; run <= 2; run++ {
				result := runLogicalScriptLinux(t, paths, nil)
				if result.err != nil {
					t.Fatalf("%s run %d failed: %v", kind, run, result.err)
				}
				wantJS := exactLogicalBackupJS(result.capture.Paths["dump"])
				if kind == logicalRestoreKind {
					wantJS = exactLogicalRestoreJS(result.capture.Paths["dump"])
				}
				assertLogicalInvocation(t, result, kind, wantJS)
				assertLogicalSecretAbsent(t, result)
			}
			assertNoLogicalJSResidue(t, paths.root)
		})
	}
}

func TestLogicalScriptsLinuxRejectUnsafeControlledObjectsBeforeMySQLShell(t *testing.T) {
	// Production break caught: either script accepting a symlink, wrong object kind/mode, or missing executable would escape the descriptor-anchored trust boundary.
	requireLinuxDescriptors(t)
	mutations := []struct {
		name   string
		mutate func(*testing.T, logicalLinuxPaths)
	}{
		{"base symlink", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalPathWithSymlink(t, p.backupRoot) }},
		{"base wrong type", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalDirWithFile(t, p.backupRoot) }},
		{"base wrong mode", func(t *testing.T, p logicalLinuxPaths) { mustChmodLogical(t, p.backupRoot, 0o750) }},
		{"task symlink", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalPathWithSymlink(t, p.taskDir) }},
		{"task wrong type", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalDirWithFile(t, p.taskDir) }},
		{"task wrong mode", func(t *testing.T, p logicalLinuxPaths) { mustChmodLogical(t, p.taskDir, 0o750) }},
		{"dump symlink", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalPathWithSymlink(t, p.dumpDir) }},
		{"dump wrong type", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalDirWithFile(t, p.dumpDir) }},
		{"dump wrong mode", func(t *testing.T, p logicalLinuxPaths) { mustChmodLogical(t, p.dumpDir, 0o750) }},
		{"secret symlink", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalPathWithSymlink(t, p.secretPath) }},
		{"secret wrong type", func(t *testing.T, p logicalLinuxPaths) { replaceLogicalFileWithDir(t, p.secretPath) }},
		{"secret wrong mode", func(t *testing.T, p logicalLinuxPaths) { mustChmodLogical(t, p.secretPath, 0o640) }},
		{"missing mysqlsh", func(t *testing.T, p logicalLinuxPaths) {
			if err := os.Remove(p.mysqlshPath); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, kind := range []logicalScriptKind{logicalBackupKind, logicalRestoreKind} {
		for _, mutation := range mutations {
			t.Run(string(kind)+"/"+mutation.name, func(t *testing.T) {
				paths := setupLogicalScriptLinux(t, kind)
				mutation.mutate(t, paths)
				result := runLogicalScriptLinux(t, paths, nil)
				if got, want := logicalExitCode(result.err), 1; got != want {
					t.Fatalf("controlled validation exit = %d, want %d; stdout=%q stderr=%q", got, want, result.stdout, result.stderr)
				}
				if result.capture.Reached {
					t.Fatal("mysqlsh was reached for an unsafe controlled object")
				}
				assertLogicalSecretAbsent(t, result)
				assertNoLogicalJSResidue(t, paths.root)
			})
		}
	}
}

func TestLogicalScriptsLinuxRejectOwnerMismatchWhenPrivileged(t *testing.T) {
	// Production break caught: omitting the owner check would let a different local account replace the credential context even when type/mode remain valid.
	requireLinuxDescriptors(t)
	if os.Geteuid() != 0 {
		t.Skip("owner mismatch is only changed safely when the test process is root")
	}
	for _, kind := range []logicalScriptKind{logicalBackupKind, logicalRestoreKind} {
		t.Run(string(kind), func(t *testing.T) {
			paths := setupLogicalScriptLinux(t, kind)
			if err := os.Chown(paths.secretPath, 1, -1); err != nil {
				t.Fatalf("prepare owner mismatch: %v", err)
			}
			result := runLogicalScriptLinux(t, paths, nil)
			if got, want := logicalExitCode(result.err), 1; got != want {
				t.Fatalf("owner mismatch exit = %d, want %d", got, want)
			}
			if result.capture.Reached {
				t.Fatal("mysqlsh was reached for an owner mismatch")
			}
			assertLogicalSecretAbsent(t, result)
		})
	}
}

func TestLogicalScriptsLinuxPropagateExactMySQLShellExitCodesWithoutSecrets(t *testing.T) {
	// Production break caught: translating or swallowing mysqlsh failures could mark a failed dump/load successful; a capture proves the child was actually reached.
	requireLinuxDescriptors(t)
	for _, test := range []struct {
		kind logicalScriptKind
		code int
	}{
		{logicalBackupKind, 23},
		{logicalRestoreKind, 29},
	} {
		t.Run(string(test.kind), func(t *testing.T) {
			paths := setupLogicalScriptLinux(t, test.kind)
			result := runLogicalScriptLinux(t, paths, map[string]string{"FAKE_EXIT": fmt.Sprintf("%d", test.code)})
			if got := logicalExitCode(result.err); got != test.code {
				t.Fatalf("propagated exit = %d, want exact mysqlsh exit %d", got, test.code)
			}
			if !result.capture.Reached {
				t.Fatal("mysqlsh exit assertion has no reached capture")
			}
			wantJS := exactLogicalBackupJS(result.capture.Paths["dump"])
			if test.kind == logicalRestoreKind {
				wantJS = exactLogicalRestoreJS(result.capture.Paths["dump"])
			}
			assertLogicalInvocation(t, result, test.kind, wantJS)
			assertLogicalSecretAbsent(t, result)
			assertNoLogicalJSResidue(t, paths.root)
		})
	}
}

type logicalLinuxPaths struct {
	kind        logicalScriptKind
	root        string
	backupRoot  string
	taskDir     string
	dumpDir     string
	secretPath  string
	mysqlshPath string
	scriptPath  string
	capturePath string
}

type logicalDescriptorCapture struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity"`
	Mode     string `json:"mode"`
}

type logicalMySQLShellCapture struct {
	Reached     bool                                `json:"reached"`
	Argv        []string                            `json:"argv"`
	Paths       map[string]string                   `json:"paths"`
	Descriptors map[string]logicalDescriptorCapture `json:"descriptors"`
	Defaults    string                              `json:"defaults"`
	JS          string                              `json:"js"`
	Marker      string                              `json:"marker"`
}

type logicalLinuxResult struct {
	capture logicalMySQLShellCapture
	stdout  string
	stderr  string
	err     error
}

func setupLogicalScriptLinux(t *testing.T, kind logicalScriptKind) logicalLinuxPaths {
	t.Helper()
	root := t.TempDir()
	oldBackupRoot, oldInstallRoot := mysqlBackupWorkRoot, mysqlInstallRoot
	mysqlBackupWorkRoot, mysqlInstallRoot = filepath.Join(root, "backup"), filepath.Join(root, "mysql")
	t.Cleanup(func() { mysqlBackupWorkRoot, mysqlInstallRoot = oldBackupRoot, oldInstallRoot })

	taskDir := filepath.Join(mysqlBackupWorkRoot, "task-001")
	dumpDir := filepath.Join(taskDir, "dump")
	mysqlshDir := filepath.Join(mysqlInstallRoot, "mysql-shell", "bin")
	for _, directory := range []string{mysqlBackupWorkRoot, taskDir, dumpDir, mysqlshDir} {
		mustMkdirLogical(t, directory, 0o700)
	}
	secretPath := filepath.Join(taskDir, "secret-context.cnf")
	writeLogicalTestFile(t, secretPath, logicalSentinelSecret+"\n", 0o600)
	if kind == logicalRestoreKind {
		writeLogicalTestFile(t, filepath.Join(dumpDir, "aifar-fake-marker"), "backup\n", 0o600)
	}

	mysqlshPath := filepath.Join(mysqlshDir, "mysqlsh")
	writeLogicalTestFile(t, mysqlshPath, fakeLogicalMySQLShellPython, 0o700)
	script, err := renderLogicalLinuxTestScript(kind)
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, "logical-"+string(kind)+".sh")
	writeLogicalTestFile(t, scriptPath, script, 0o700)
	return logicalLinuxPaths{
		kind:        kind,
		root:        root,
		backupRoot:  mysqlBackupWorkRoot,
		taskDir:     taskDir,
		dumpDir:     dumpDir,
		secretPath:  secretPath,
		mysqlshPath: mysqlshPath,
		scriptPath:  scriptPath,
		capturePath: filepath.Join(root, "mysqlsh-capture.json"),
	}
}

func renderLogicalLinuxTestScript(kind logicalScriptKind) (string, error) {
	if kind == logicalBackupKind {
		return RenderLogicalBackupScript(LogicalBackupScriptOptions{TaskID: "task-001", Threads: 4, MaxRateMBps: 32})
	}
	return RenderLogicalRestoreScript(LogicalRestoreScriptOptions{TaskID: "task-001", Threads: 4})
}

func runLogicalScriptLinux(t *testing.T, paths logicalLinuxPaths, values map[string]string) logicalLinuxResult {
	t.Helper()
	if err := os.Remove(paths.capturePath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clear mysqlsh capture: %v", err)
	}
	command := exec.Command("/bin/sh", paths.scriptPath)
	command.Env = append(os.Environ(), "CAPTURE="+paths.capturePath)
	for key, value := range values {
		command.Env = append(command.Env, key+"="+value)
	}
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	result := logicalLinuxResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
	data, readErr := os.ReadFile(paths.capturePath)
	if readErr == nil {
		if err := json.Unmarshal(data, &result.capture); err != nil {
			t.Fatalf("decode mysqlsh capture: %v", err)
		}
	} else if !os.IsNotExist(readErr) {
		t.Fatalf("read mysqlsh capture: %v", readErr)
	}
	return result
}

func assertLogicalInvocation(t *testing.T, result logicalLinuxResult, kind logicalScriptKind, wantJS string) {
	t.Helper()
	if !result.capture.Reached {
		t.Fatal("mysqlsh was not reached")
	}
	argv := result.capture.Argv
	if len(argv) != 4 {
		t.Fatalf("mysqlsh argv count = %d, want 4: %#v", len(argv), argv)
	}
	if argv[1] != "--js" || argv[2] != "--file" {
		t.Fatalf("mysqlsh argv order = %#v, want defaults, --js, --file, JS descriptor", argv)
	}
	procFD := regexp.MustCompile(`^/proc/self/fd/[0-9]+$`)
	if !strings.HasPrefix(argv[0], "--defaults-file=") || !procFD.MatchString(strings.TrimPrefix(argv[0], "--defaults-file=")) || !procFD.MatchString(argv[3]) {
		t.Fatalf("mysqlsh argv descriptor forms are not exact: %#v", argv)
	}
	if result.capture.Paths["defaults"] != strings.TrimPrefix(argv[0], "--defaults-file=") || result.capture.Paths["js"] != argv[3] {
		t.Fatalf("captured descriptor paths disagree with exact argv: paths=%#v argv=%#v", result.capture.Paths, argv)
	}
	wantKinds := map[string]string{"mysqlsh": "regular", "defaults": "regular", "js": "regular", "dump": "directory"}
	wantModes := map[string]string{"mysqlsh": "0700", "defaults": "0600", "js": "0600", "dump": "0700"}
	identities := make(map[string]string, len(wantKinds))
	fdPaths := make(map[string]string, len(wantKinds))
	for name, wantKind := range wantKinds {
		path := result.capture.Paths[name]
		if !procFD.MatchString(path) {
			t.Fatalf("%s path = %q, want exact /proc/self/fd/N", name, path)
		}
		descriptor := result.capture.Descriptors[name]
		if descriptor.Kind != wantKind || descriptor.Mode != wantModes[name] {
			t.Fatalf("%s descriptor = %+v, want kind=%s mode=%s", name, descriptor, wantKind, wantModes[name])
		}
		if descriptor.Identity == "" {
			t.Fatalf("%s descriptor identity is empty", name)
		}
		if prior, duplicate := identities[descriptor.Identity]; duplicate {
			t.Fatalf("%s and %s alias descriptor identity %q", prior, name, descriptor.Identity)
		}
		identities[descriptor.Identity] = name
		if prior, duplicate := fdPaths[path]; duplicate {
			t.Fatalf("%s and %s reuse fd path %q", prior, name, path)
		}
		fdPaths[path] = name
	}
	if got := result.capture.JS; got != wantJS {
		t.Fatalf("%s JS differs from hand-derived exact program:\n--- got ---\n%s--- want ---\n%s", kind, got, wantJS)
	}
}

func exactLogicalBackupJS(dumpPath string) string {
	return fmt.Sprintf(`const options = {
  consistent: true,
  threads: 4,
  compression: "zstd",
  users: false,
  showProgress: false,
  excludeSchemas: ["information_schema", "mysql", "mysql_innodb_cluster_metadata", "performance_schema", "sys"]
};
if (32 > 0) {
  options.maxRate = "32M";
}
util.dumpInstance("%s", options);
`, dumpPath)
}

func exactLogicalRestoreJS(dumpPath string) string {
	return fmt.Sprintf(`util.loadDump("%s", {
  threads: 4,
  loadUsers: false,
  ignoreExistingObjects: false,
  skipBinlog: false,
  showProgress: false
});
`, dumpPath)
}

func assertLogicalSecretAbsent(t *testing.T, result logicalLinuxResult) {
	t.Helper()
	values := map[string]string{
		"argv":       strings.Join(result.capture.Argv, "\n"),
		"javascript": result.capture.JS,
		"stdout":     result.stdout,
		"stderr":     result.stderr,
	}
	if result.err != nil {
		values["returned error"] = result.err.Error()
	}
	for location, value := range values {
		if strings.Contains(value, logicalSentinelSecret) {
			t.Fatalf("sentinel secret leaked through %s", location)
		}
	}
}

func assertNoLogicalJSResidue(t *testing.T, root string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".js") {
			return fmt.Errorf("persistent JS residue at %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func logicalExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitError, ok := err.(*exec.ExitError); ok {
		return exitError.ExitCode()
	}
	return -1
}

func requireLinuxDescriptors(t *testing.T) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux /proc/self/fd and O_NOFOLLOW semantics")
	}
}

func mustMkdirLogical(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(path, mode); err != nil {
		t.Fatal(err)
	}
	mustChmodLogical(t, path, mode)
}

func mustChmodLogical(t *testing.T, path string, mode os.FileMode) {
	t.Helper()
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func writeLogicalTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	mustChmodLogical(t, path, mode)
}

func replaceLogicalPathWithSymlink(t *testing.T, path string) {
	t.Helper()
	moved := path + "-moved"
	if err := os.Rename(path, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, path); err != nil {
		t.Fatal(err)
	}
}

func replaceLogicalDirWithFile(t *testing.T, path string) {
	t.Helper()
	if err := os.Rename(path, path+"-moved"); err != nil {
		t.Fatal(err)
	}
	writeLogicalTestFile(t, path, "not-a-directory\n", 0o700)
}

func replaceLogicalFileWithDir(t *testing.T, path string) {
	t.Helper()
	if err := os.Rename(path, path+"-moved"); err != nil {
		t.Fatal(err)
	}
	mustMkdirLogical(t, path, 0o600)
}

const fakeLogicalMySQLShellPython = `#!/usr/bin/env python3
import json
import os
import re
import stat
import sys

if os.environ.get("REPLACE_SECRET_FROM"):
    os.replace(os.environ["REPLACE_SECRET_FROM"], os.environ["REPLACE_SECRET_TO"])
if os.environ.get("REPLACE_DUMP_FROM"):
    os.rename(os.environ["REPLACE_DUMP_TO"], os.environ["REPLACE_DUMP_OLD"])
    os.rename(os.environ["REPLACE_DUMP_FROM"], os.environ["REPLACE_DUMP_TO"])

argv = sys.argv[1:]
if len(argv) != 4 or not argv[0].startswith("--defaults-file=") or argv[1:3] != ["--js", "--file"]:
    raise SystemExit(97)
defaults_path = argv[0].split("=", 1)[1]
js_path = argv[3]
with open(defaults_path, "r", encoding="utf-8") as source:
    defaults = source.read()
with open(js_path, "r", encoding="utf-8") as source:
    javascript = source.read()
match = re.search(r'util\.(?:dumpInstance|loadDump)\("(/proc/self/fd/[0-9]+)"', javascript)
if match is None:
    raise SystemExit(98)
dump_path = match.group(1)

marker_path = os.path.join(dump_path, "aifar-fake-marker")
if "util.dumpInstance" in javascript:
    with open(marker_path, "w", encoding="utf-8") as marker_file:
        marker_file.write("backup\n")
    marker = "backup\n"
else:
    with open(marker_path, "r", encoding="utf-8") as marker_file:
        marker = marker_file.read()

paths = {
    "mysqlsh": sys.argv[0],
    "defaults": defaults_path,
    "js": js_path,
    "dump": dump_path,
}
descriptors = {}
for name, path in paths.items():
    details = os.stat(path)
    if stat.S_ISREG(details.st_mode):
        kind = "regular"
    elif stat.S_ISDIR(details.st_mode):
        kind = "directory"
    else:
        kind = "other"
    descriptors[name] = {
        "kind": kind,
        "identity": "%d:%d" % (details.st_dev, details.st_ino),
        "mode": "%04o" % stat.S_IMODE(details.st_mode),
    }

capture = {
    "reached": True,
    "argv": argv,
    "paths": paths,
    "descriptors": descriptors,
    "defaults": defaults,
    "js": javascript,
    "marker": marker,
}
with open(os.environ["CAPTURE"], "w", encoding="utf-8") as capture_file:
    json.dump(capture, capture_file, sort_keys=True)
print("fake mysqlsh reached")
print("fake mysqlsh diagnostic", file=sys.stderr)
raise SystemExit(int(os.environ.get("FAKE_EXIT", "0")))
`
