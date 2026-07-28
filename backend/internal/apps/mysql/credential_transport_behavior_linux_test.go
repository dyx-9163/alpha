package mysql

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

func TestCredentialTransportLinuxStandaloneExecutesSpecialCharactersAndCleans(t *testing.T) {
	for _, outcome := range []string{"success", "mysql-failure", "sql-cleanup-failure"} {
		t.Run(outcome, func(t *testing.T) {
			fixture := newStandaloneCredentialFixture(t)
			result := fixture.run(t, outcome)
			if outcome == "success" {
				if result.err != nil || !strings.Contains(result.stdout, "HARNESS_COMPLETED") {
					t.Fatalf("standalone harness success: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
				}
				client, err := os.ReadFile(fixture.clientCapture)
				if err != nil || !strings.Contains(string(client), `S3cr'et\"$\\:@{}[]!`) {
					t.Fatalf("special-character option context was not preserved: %q err=%v", client, err)
				}
				sql, err := os.ReadFile(fixture.sqlCapture)
				if err != nil || !strings.Contains(string(sql), `S3cr''et"$\\:@{}[]!`) {
					t.Fatalf("special-character SQL escaping was not preserved: %q err=%v", sql, err)
				}
			} else if result.err == nil || strings.Contains(result.stdout, "HARNESS_COMPLETED") {
				t.Fatalf("%s published success: err=%v stdout=%q", outcome, result.err, result.stdout)
			}
			assertPathAbsent(t, fixture.contextPath)
			assertPathAbsent(t, filepath.Join(fixture.workDir, "secure-client.cnf"))
			if outcome != "sql-cleanup-failure" {
				assertPathAbsent(t, filepath.Join(fixture.workDir, "secure-root.sql"))
			}
			if strings.Contains(result.stdout+result.stderr, credentialTransportSentinel) {
				t.Fatal("standalone executable output leaked the credential")
			}
		})
	}
}

func TestCredentialTransportLinuxStandaloneRejectsUnsafeContextBeforeRead(t *testing.T) {
	mutations := map[string]func(*testing.T, *standaloneCredentialFixture){
		"symlink": func(t *testing.T, f *standaloneCredentialFixture) {
			target := f.contextPath + ".target"
			mustWriteCredentialFile(t, target, installCredentialContents(), 0o600)
			if err := os.Remove(f.contextPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, f.contextPath); err != nil {
				t.Fatal(err)
			}
		},
		"wrong-type": func(t *testing.T, f *standaloneCredentialFixture) {
			if err := os.Remove(f.contextPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(f.contextPath, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"wrong-mode": func(t *testing.T, f *standaloneCredentialFixture) {
			if err := os.Chmod(f.contextPath, 0o640); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			fixture := newStandaloneCredentialFixture(t)
			mutate(t, fixture)
			result := fixture.run(t, "success")
			if result.err == nil || fileExists(fixture.sqlCapture) {
				t.Fatalf("unsafe context reached SQL execution: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
			}
		})
	}
}

func TestCredentialTransportLinuxRejectsOwnerMismatchWhenPrivileged(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("owner mismatch requires root")
	}
	fixture := newStandaloneCredentialFixture(t)
	if err := os.Chown(fixture.contextPath, 1, -1); err != nil {
		t.Fatal(err)
	}
	if result := fixture.run(t, "success"); result.err == nil || fileExists(fixture.sqlCapture) {
		t.Fatalf("owner mismatch reached SQL execution: err=%v", result.err)
	}
}

func TestCredentialTransportLinuxClusterScriptsCleanSuccessFailureAndCancellation(t *testing.T) {
	for _, action := range []string{"bootstrap", "start"} {
		for _, outcome := range []string{"success", "mysqlsh-failure", "js-cleanup-failure", "context-cleanup-failure", "cancel"} {
			t.Run(action+"/"+outcome, func(t *testing.T) {
				fixture := newClusterCredentialFixture(t, action)
				result := fixture.run(t, outcome)
				if outcome == "success" {
					if result.err != nil || !strings.Contains(result.stdout, "completed") {
						t.Fatalf("cluster success: err=%v stdout=%q stderr=%q", result.err, result.stdout, result.stderr)
					}
				} else if result.err == nil || strings.Contains(result.stdout, "completed") {
					t.Fatalf("%s published cluster completion: err=%v stdout=%q", outcome, result.err, result.stdout)
				}
				if outcome != "context-cleanup-failure" {
					assertPathAbsent(t, fixture.contextPath)
				}
				if outcome != "js-cleanup-failure" {
					assertPathAbsent(t, fixture.jsPath)
				}
				if strings.Contains(result.stdout+result.stderr, credentialTransportSentinel) {
					t.Fatal("cluster executable output leaked the credential")
				}
				capture, err := os.ReadFile(fixture.capturePath)
				if outcome != "cancel" && (err != nil || !clusterCaptureHasCredential(capture, credentialTransportSentinel)) {
					t.Fatalf("mysqlsh harness did not consume special credential context: capture=%q err=%v", capture, err)
				}
			})
		}
	}
}

func TestCredentialTransportLinuxStatusAndPrimaryExecuteContextAndLeaveNoResidue(t *testing.T) {
	root := t.TempDir()
	remote := &localCredentialRemote{root: root, reachedPath: filepath.Join(root, "client.reached")}
	server := store.Server{ID: "srv-1", DeployDir: filepath.Join(root, "apps")}
	instance := store.AppInstance{ID: "app-1", App: "mysql", Version: "8.0.36", ServerID: server.ID, Metadata: `{"port":3306}`}
	credential := store.Credential{Kind: "mysql", Status: "active", Username: "root", Secret: map[string]string{"password": credentialTransportSentinel}}
	installRoot := remoteInstallRoot(server, "mysql", instance.Version)
	remote.mkdirExecutable(t, filepath.Join(installRoot, "mysql", "bin", "mysqladmin"), fakeCredentialClient("runtime"))
	remote.mkdirExecutable(t, filepath.Join(installRoot, "mysql", "bin", "mysql"), fakeCredentialClient("primary"))
	service := NewService(&fakeStore{}, remote)
	probe, err := service.probeMySQLRuntime(context.Background(), server, instance, credential, fakeLogger{})
	if err != nil || !probe.runtimeRunning() {
		t.Fatalf("authenticated status harness failed: probe=%+v err=%v", probe, err)
	}
	primary, err := service.detectInnoDBPrimary(context.Background(), server, instance, credential, fakeLogger{})
	if err != nil || primary != "10.0.0.1:3306" {
		t.Fatalf("PRIMARY harness failed: primary=%q err=%v", primary, err)
	}
	if strings.Contains(strings.Join(remote.commands, "\n"), credentialTransportSentinel) {
		t.Fatal("status/PRIMARY command leaked the credential")
	}
	reached, err := os.ReadFile(remote.reachedPath)
	if err != nil || !strings.Contains(string(reached), `password="S3cr'et\"$\\:@{}[]!"`) {
		t.Fatalf("status/PRIMARY client did not consume the escaped credential context: reached=%q err=%v commands=%v", reached, err, remote.commands)
	}
	entries, err := os.ReadDir(remote.backupRoot())
	if err != nil || len(entries) != 0 {
		t.Fatalf("status/PRIMARY retained credential work: entries=%v err=%v", entries, err)
	}
}

func TestCredentialTransportLinuxStatusRejectsUnsafeContextBeforeClient(t *testing.T) {
	mutations := []string{"symlink", "wrong-type", "wrong-mode"}
	if os.Geteuid() == 0 {
		mutations = append(mutations, "wrong-owner")
	}
	for _, mutation := range mutations {
		t.Run(mutation, func(t *testing.T) {
			root := t.TempDir()
			remote := &localCredentialRemote{root: root, uploadMutation: mutation, reachedPath: filepath.Join(root, "client.reached")}
			server := store.Server{ID: "srv-1", DeployDir: filepath.Join(root, "apps")}
			instance := store.AppInstance{ID: "app-1", App: "mysql", Version: "8.0.36", ServerID: server.ID, Metadata: `{"port":3306}`}
			credential := store.Credential{Kind: "mysql", Status: "active", Username: "root", Secret: map[string]string{"password": credentialTransportSentinel}}
			installRoot := remoteInstallRoot(server, "mysql", instance.Version)
			remote.mkdirExecutable(t, filepath.Join(installRoot, "mysql", "bin", "mysqladmin"), fakeCredentialClient("runtime"))
			_, err := NewService(&fakeStore{}, remote).probeMySQLRuntime(context.Background(), server, instance, credential, fakeLogger{})
			if err == nil || fileExists(remote.reachedPath) {
				t.Fatalf("unsafe %s context reached authenticated client: err=%v", mutation, err)
			}
		})
	}
}

type shellResult struct {
	stdout, stderr string
	err            error
}

type standaloneCredentialFixture struct {
	root, workDir, contextPath, scriptPath, clientCapture, sqlCapture string
}

func newStandaloneCredentialFixture(t *testing.T) *standaloneCredentialFixture {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "_work", "mysql-8.0.36-1")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(work, "mysql-credential.context")
	mustWriteCredentialFile(t, contextPath, installCredentialContents(), 0o600)
	installRoot := filepath.Join(root, "mysql")
	mysqlPath := filepath.Join(installRoot, "mysql", "bin", "mysql")
	if err := os.MkdirAll(filepath.Dir(mysqlPath), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteCredentialFile(t, mysqlPath, `#!/bin/sh
cat > "$CAPTURE_SQL"
if [ "${OUTCOME:-}" = "sql-cleanup-failure" ]; then rm -f "$SECURE_SQL"; mkdir "$SECURE_SQL"; fi
if [ "${OUTCOME:-}" = "mysql-failure" ]; then exit 23; fi
`, 0o700)
	script, err := installStandaloneScript(InstallScriptRequest{Version: "8.0.36", WorkDir: work, ArchivePath: filepath.Join(root, "bundle.tar"), InstallRoot: installRoot, Port: 3306})
	if err != nil {
		t.Fatal(err)
	}
	prefixEnd := strings.Index(script, "\ndump_mysql_diagnostics()")
	sqlStart := strings.Index(script, "if [ \"$NEED_SECURE\" = \"1\" ]; then\n  echo \"setting MySQL administrator password")
	sqlEndMarker := "\nfi\n\necho \"verifying MySQL service\""
	sqlEnd := strings.Index(script[sqlStart:], sqlEndMarker)
	if prefixEnd < 0 || sqlStart < 0 || sqlEnd < 0 {
		t.Fatal("unable to extract production standalone credential harness")
	}
	sqlBlock := script[sqlStart : sqlStart+sqlEnd+len("\nfi")]
	harness := script[:prefixEnd] + `
cp "$SECURE_CLIENT_FILE" "$CAPTURE_CLIENT"
NEED_SECURE=1
MYSQL_BOOTSTRAP_PROTOCOL=socket
export SECURE_SQL="$SECURE_ROOT_SQL"
` + sqlBlock + `
if ! cleanup_secret_artifacts; then exit 1; fi
trap - EXIT HUP INT TERM
echo HARNESS_COMPLETED
`
	scriptPath := filepath.Join(root, "standalone-credential-harness.sh")
	mustWriteCredentialFile(t, scriptPath, harness, 0o700)
	return &standaloneCredentialFixture{root: root, workDir: work, contextPath: contextPath, scriptPath: scriptPath, clientCapture: filepath.Join(root, "client.capture"), sqlCapture: filepath.Join(root, "sql.capture")}
}

func (f *standaloneCredentialFixture) run(t *testing.T, outcome string) shellResult {
	t.Helper()
	cmd := exec.Command("/bin/sh", f.scriptPath, f.contextPath)
	cmd.Env = append(os.Environ(), "OUTCOME="+outcome, "CAPTURE_CLIENT="+f.clientCapture, "CAPTURE_SQL="+f.sqlCapture)
	return runShellCommand(cmd)
}

type clusterCredentialFixture struct{ action, root, contextPath, scriptPath, jsPath, capturePath string }

func newClusterCredentialFixture(t *testing.T, action string) *clusterCredentialFixture {
	t.Helper()
	root := t.TempDir()
	work := filepath.Join(root, "_work", "mysql-credential-"+action+"-1")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	contextPath := filepath.Join(work, "credential-context.json")
	data, _ := json.Marshal(mysqlJSONCredentialContext{Version: 1, Connections: []mysqlConnectionCredential{{Host: "10.0.0.1", Port: 3306, User: "root", Password: credentialTransportSentinel}}})
	mustWriteCredentialFile(t, contextPath, string(data)+"\n", 0o600)
	installRoot := filepath.Join(root, "mysql")
	mysqlsh := filepath.Join(installRoot, "mysql-shell", "bin", "mysqlsh")
	if err := os.MkdirAll(filepath.Dir(mysqlsh), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteCredentialFile(t, mysqlsh, `#!/bin/sh
set -eu
js="$3"
cat "$FAKE_CONTEXT" > "$CAPTURE"
cat "$js" >> "$CAPTURE"
case "${OUTCOME:-success}" in
  mysqlsh-failure) exit 29 ;;
  js-cleanup-failure) rm -f "$js"; mkdir "$js" ;;
  context-cleanup-failure) rm -f "$FAKE_CONTEXT"; mkdir "$FAKE_CONTEXT" ;;
  cancel) trap 'exit 130' INT TERM; while :; do sleep 1; done ;;
esac
`, 0o700)
	var script string
	var err error
	nodes := []InnoDBClusterNode{{Host: "10.0.0.1", Port: 3306}}
	if action == "bootstrap" {
		script, err = bootstrapInnoDBClusterScript(InnoDBClusterBootstrapScriptRequest{ClusterName: "aifarCluster", InstallRoot: installRoot, CredentialContextPath: contextPath, Nodes: nodes})
	} else {
		script, err = startInnoDBClusterScript(InnoDBClusterStartScriptRequest{ClusterName: "aifarCluster", InstallRoot: installRoot, CredentialContextPath: contextPath, Nodes: nodes})
	}
	if err != nil {
		t.Fatal(err)
	}
	scriptPath := filepath.Join(root, action+".sh")
	mustWriteCredentialFile(t, scriptPath, script, 0o700)
	return &clusterCredentialFixture{action: action, root: root, contextPath: contextPath, scriptPath: scriptPath, jsPath: filepath.Join(work, action+"-cluster.js"), capturePath: filepath.Join(root, "capture")}
}

func (f *clusterCredentialFixture) run(t *testing.T, outcome string) shellResult {
	t.Helper()
	cmd := exec.Command("/bin/sh", f.scriptPath)
	cmd.Env = append(os.Environ(), "OUTCOME="+outcome, "FAKE_CONTEXT="+f.contextPath, "CAPTURE="+f.capturePath)
	if outcome != "cancel" {
		return runShellCommand(cmd)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for !fileExists(f.capturePath) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if !fileExists(f.capturePath) {
		t.Fatal("mysqlsh cancellation seam was not reached")
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	err := cmd.Wait()
	return shellResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}

type localCredentialRemote struct {
	root                        string
	commands                    []string
	uploadMutation, reachedPath string
}

func (r *localCredentialRemote) backupRoot() string { return filepath.Join(r.root, "backup") }
func (r *localCredentialRemote) translate(value string) string {
	return strings.ReplaceAll(value, mysqlBackupWorkRoot, filepath.ToSlash(r.backupRoot()))
}
func (r *localCredentialRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", r.translate(command))
	cmd.Env = append(os.Environ(), "EXPECTED_SECRET="+credentialTransportSentinel, "REACHED="+r.reachedPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return adapter.CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}, err
}
func (r *localCredentialRemote) UploadFile(_ context.Context, _ store.Server, localPath, remotePath string, mode os.FileMode) error {
	remotePath = r.translate(remotePath)
	if err := os.MkdirAll(filepath.Dir(remotePath), 0o700); err != nil {
		return err
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if err := os.WriteFile(remotePath, data, mode); err != nil {
		return err
	}
	switch r.uploadMutation {
	case "symlink":
		target := remotePath + ".target"
		if err := os.Rename(remotePath, target); err != nil {
			return err
		}
		return os.Symlink(target, remotePath)
	case "wrong-type":
		if err := os.Remove(remotePath); err != nil {
			return err
		}
		return os.Mkdir(remotePath, 0o700)
	case "wrong-mode":
		return os.Chmod(remotePath, 0o640)
	case "wrong-owner":
		return os.Chown(remotePath, 1, -1)
	}
	return nil
}
func (r *localCredentialRemote) mkdirExecutable(t *testing.T, name, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWriteCredentialFile(t, name, contents, 0o700)
}

func fakeCredentialClient(kind string) string {
	output := "runtimeStatus=running\\nmysqlPingStatus=running\\nmysqlServiceStatus=running\\nmysqlPortStatus=listening\\nmysqlRuntimeSource=mysqladmin\\n"
	if kind == "primary" {
		output = "10.0.0.1:3306\\n"
	}
	return fmt.Sprintf(`#!/bin/sh
set -eu
defaults=""
for arg in "$@"; do case "$arg" in --defaults-file=*) defaults="${arg#*=}";; esac; done
[ -f "$defaults" ] && [ ! -L "$defaults" ] && [ "$(stat -c '%%a' "$defaults")" = 600 ]
[ -z "${REACHED:-}" ] || cp "$defaults" "$REACHED"
printf '%%b' %s
`, shellQuoteForTest(output))
}

func clusterCaptureHasCredential(capture []byte, password string) bool {
	line, _, found := bytes.Cut(capture, []byte("\n"))
	if !found {
		return false
	}
	var context mysqlJSONCredentialContext
	if err := json.Unmarshal(line, &context); err != nil || len(context.Connections) != 1 {
		return false
	}
	return context.Connections[0].Password == password
}

func shellQuoteForTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}
func installCredentialContents() string {
	return "AIFAR_MYSQL_CREDENTIAL_CONTEXT_V1\nroot\n" + credentialTransportSentinel + "\n"
}
func mustWriteCredentialFile(t *testing.T, name, contents string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(name, []byte(contents), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(name, mode); err != nil {
		t.Fatal(err)
	}
}
func runShellCommand(cmd *exec.Cmd) shellResult {
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	return shellResult{stdout: stdout.String(), stderr: stderr.String(), err: err}
}
func assertPathAbsent(t *testing.T, name string) {
	t.Helper()
	if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential residue remains at %s: %v", name, err)
	}
}
func fileExists(name string) bool { _, err := os.Stat(name); return err == nil }
