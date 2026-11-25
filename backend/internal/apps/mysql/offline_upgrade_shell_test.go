package mysql

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runOfflineUpgradeShell(t *testing.T, phase, app, fixture string, args ...string) (string, string, error) {
	t.Helper()
	requireLinuxDescriptors(t)
	root := t.TempDir()
	work := filepath.Join(root, "work")
	mustMkdirLogical(t, work, 0o700)
	scriptPath := filepath.Join(work, "upgrade.sh")
	before, after, ok := strings.Cut(offlineUpgradeScript, "\ncase \"$phase\" in\n")
	if !ok {
		t.Fatal("could not load offline upgrade shell script")
	}
	writeLogicalTestFile(t, scriptPath, before+"\n"+fixture+"\ncase \"$phase\" in\n"+after, 0o700)
	cmd := exec.Command("bash", append([]string{scriptPath, phase, app, root, work, filepath.Join(root, "backup"), "test", "3306"}, args...)...)
	output, err := cmd.CombinedOutput()
	return root, string(output), err
}

func TestOfflineUpgradeShellWaitsForOldRouterAfterFailedArchive(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "backup", "mysql-router", `
mkdir "$router_base" "$root/router"
unit_file="$work/router.service"
touch "$unit_file"
as_root() {
  [[ "$1" != tar ]] || return 1
  "$@"
}
df() { printf 'Avail\n1073741824\n'; }
findmnt() { printf 'test mount\n'; }
stop_cleanly() { :; }
systemctl() { if [[ "$1" == start ]]; then touch "$work/start-requested"; fi; }
sleep() { :; }
check_version() { :; }
check_process() {
  if [[ ! -e "$work/start-requested" ]]; then return 0; fi
  if [[ ! -e "$work/first-probe" ]]; then
    touch "$work/first-probe"
    return 1
  fi
  touch "$work/old-router-ready"
}
`)
	if err == nil {
		t.Fatal("failed archive was reported successful")
	}
	if _, err := os.Stat(filepath.Join(root, "work", "old-router-ready")); err != nil {
		t.Fatalf("archive failure returned without waiting for the restored Router: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "backup", "complete")); !os.IsNotExist(err) {
		t.Fatalf("failed backup produced a complete marker: %v", err)
	}
}

func TestOfflineUpgradeShellRejectsWrongSourceBeforeStoppingForBackup(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "backup", "mysql-router", `
mkdir "$router_base" "$root/router"
unit_file="$work/router.service"
touch "$unit_file"
as_root() { "$@"; }
df() { printf 'Avail\n1073741824\n'; }
findmnt() { printf 'test mount\n'; }
stop_cleanly() { touch "$work/stopped"; }
systemctl() { return 1; }
check_process() { :; }
check_version() { [[ "$3" != 8.0.36 ]]; }
`)
	if err == nil {
		t.Fatal("cold backup accepted an unexpected source version")
	}
	if _, err := os.Stat(filepath.Join(root, "work", "stopped")); !os.IsNotExist(err) {
		t.Fatalf("the Router was stopped before verifying the original version: %v\n%s", err, output)
	}
}

const offlineUpgradeRouterFixture = `
mkdir "$router_base" "$root/router"
printf 'old\n' > "$router_base/version"
printf 'old\n' > "$work/running"
mkdir "$router_next"
printf 'new\n' > "$router_next/version"
as_root() { "$@"; }
verify_backup() { :; }
check_service_layout() { :; }
restore_labels() { :; }
sleep() { :; }
stop_cleanly() { rm -f "$work/running"; }
systemctl() {
  case "$1" in
    start) cp "$router_base/version" "$work/running" ;;
    stop) rm -f "$work/running" ;;
    show) if [[ "$3" == ActiveState ]]; then printf 'inactive\n'; else printf '0\n'; fi ;;
  esac
}
check_process() { [[ "$(cat "$work/running")" == old ]]; }
check_version() {
  if [[ "$3" == 26.10.0 ]]; then
    grep -Fxq new "$(dirname "$(dirname "$1")")/version"
  else
    grep -Fxq old "$(dirname "$(dirname "$1")")/version"
  fi
}
`

func TestOfflineUpgradeShellRestoresRouterAfterNewProcessFails(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "tools", "mysql-router", offlineUpgradeRouterFixture)
	if err == nil {
		t.Fatal("failed new Router startup was reported successful")
	}
	assertOfflineUpgradeShellFile(t, root, "mysql-router/version", "old\n", output)
	assertOfflineUpgradeShellFile(t, root, "work/running", "old\n", output)
}

func TestOfflineUpgradeShellRestoresRouterAfterFailedSQLGate(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "revert-router", "mysql-router", offlineUpgradeRouterFixture+`
mv -T "$router_base" "$router_base.old-$task"
mv -T "$router_next" "$router_base"
printf 'new\n' > "$work/running"
`)
	if err != nil {
		t.Fatalf("Router revert failed: %v\n%s", err, output)
	}
	assertOfflineUpgradeShellFile(t, root, "mysql-router/version", "old\n", output)
	assertOfflineUpgradeShellFile(t, root, "work/running", "old\n", output)
}

const offlineUpgradeServerFixture = `
mkdir "$base" "$server_next" "$root/conf"
printf 'old\n' > "$base/version"
printf 'new\n' > "$server_next/version"
printf 'configuration\nbinlog_transaction_dependency_tracking=WRITESET\n' > "$config"
printf 'configuration\n' > "$candidate"
as_root() {
  case "$1" in
    sha256sum) return 0 ;;
    tee) if [[ "${TEST_CONFIG_FAIL:-0}" == 1 ]]; then return 1; fi ;;
    rm) if [[ "${TEST_CLEANUP_FAIL:-0}" == 1 && "${*: -1}" == "$stop_override" ]]; then return 1; fi ;;
  esac
  "$@"
}
direct_sql() {
  case "$1" in
    *__AIFAR_UPGRADE_UUID__*) printf '__AIFAR_UPGRADE_UUID__\t%s\n' "${args[0]}" ;;
    *__AIFAR_UPGRADE_CUTOVER__*) printf '__AIFAR_UPGRADE_CUTOVER__\t%s\n' "${TEST_LIVE_QUORUM:-1}" ;;
    *'STOP GROUP_REPLICATION'*) touch "$work/group-stopped" ;;
    *) return 1 ;;
  esac
}
verify_backup() { :; }
restore_labels() { :; }
allow_mysql_shutdown() {
  if [[ "${TEST_CLEANUP_FAIL:-0}" == 1 ]]; then stop_override="$work/shutdown.conf"; touch "$stop_override"; fi
}
stop_cleanly() { :; }
systemctl() {
  case "$1" in
    start) cat "$base/version" >&2; cp "$base/version" "$work/running" ;;
    is-active) return 1 ;;
    show) printf '0\n' ;;
  esac
}
sleep() { :; }
check_process() { [[ "${TEST_NEW_PROCESS_FAIL:-0}" == 0 || "$(cat "$work/running")" == old ]]; }
`

var offlineUpgradeServerArguments = []string{
	"00000000-0000-0000-0000-000000000001",
	"00000000-0000-0000-0000-000000000002",
	"00000000-0000-0000-0000-000000000001,00000000-0000-0000-0000-000000000002,00000000-0000-0000-0000-000000000003",
}

func TestOfflineUpgradeShellRestoresMySQLWhenConfigWriteFailsBeforeFirstStart(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "server", "mysql", offlineUpgradeServerFixture+"\nTEST_CONFIG_FAIL=1\n", offlineUpgradeServerArguments...)
	if err == nil {
		t.Fatal("failed config write was reported successful")
	}
	assertOfflineUpgradeShellFile(t, root, "mysql/version", "old\n", output)
	assertOfflineUpgradeShellFile(t, root, "conf/my.cnf", "configuration\nbinlog_transaction_dependency_tracking=WRITESET\n", output)
	assertOfflineUpgradeShellFile(t, root, "work/running", "old\n", output)
}

func TestOfflineUpgradeShellNeverRestartsOldMySQLAfterNewStartWasAttempted(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "server", "mysql", offlineUpgradeServerFixture+"\nTEST_NEW_PROCESS_FAIL=1\n", offlineUpgradeServerArguments...)
	if err == nil {
		t.Fatal("failed new server startup was reported successful")
	}
	assertOfflineUpgradeShellFile(t, root, "mysql/version", "new\n", output)
	assertOfflineUpgradeShellFile(t, root, "mysql.old-test/version", "old\n", output)
	assertOfflineUpgradeShellFile(t, root, "conf/my.cnf", "configuration\n", output)
	if strings.Contains(output, "\nold\n") {
		t.Fatalf("old binary was started after the new binary may have opened the data:\n%s", output)
	}
}

func TestOfflineUpgradeShellRejectsChangedCandidateBeforeApplyingMySQLConfig(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "server", "mysql", offlineUpgradeServerFixture+`
stop_cleanly() { printf 'changed configuration\n' > "$candidate"; }
`, offlineUpgradeServerArguments...)
	if err == nil {
		t.Fatal("changed candidate configuration was applied")
	}
	assertOfflineUpgradeShellFile(t, root, "conf/my.cnf", "configuration\nbinlog_transaction_dependency_tracking=WRITESET\n", output)
	assertOfflineUpgradeShellFile(t, root, "mysql/version", "old\n", output)
	assertOfflineUpgradeShellFile(t, root, "work/running", "old\n", output)
}

func TestOfflineUpgradeShellStopsBeforeMutationWhenLiveQuorumChanged(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "server", "mysql", offlineUpgradeServerFixture+"\nTEST_LIVE_QUORUM=0\n", offlineUpgradeServerArguments...)
	if err == nil {
		t.Fatal("cutover was allowed after the live quorum changed")
	}
	assertOfflineUpgradeShellFile(t, root, "mysql/version", "old\n", output)
	if _, err := os.Stat(filepath.Join(root, "work", "group-stopped")); !os.IsNotExist(err) {
		t.Fatalf("group replication was stopped despite changed quorum: %v\n%s", err, output)
	}
}

func TestOfflineUpgradeShellReportsCleanupFailureAfterNewProcessStarted(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "server", "mysql", offlineUpgradeServerFixture+"\nTEST_CLEANUP_FAIL=1\n", offlineUpgradeServerArguments...)
	if err == nil || !strings.Contains(output, "MySQL 8.4.13 process started") {
		t.Fatalf("post-start cleanup failure did not report the new process state: %v\n%s", err, output)
	}
	assertOfflineUpgradeShellFile(t, root, "mysql/version", "new\n", output)
}

func TestOfflineUpgradeShellResumeRecognizesOnlyBackedUpRouterVersion(t *testing.T) {
	fixture := offlineUpgradeRouterFixture + `
mv -T "$router_base" "$router_base.old-$task"
mv -T "$router_next" "$router_base"
check_process() { :; }
mkdir "$backup"
touch "$backup/complete"
verify_backup() { test -f "$backup/complete"; }
`
	if _, output, err := runOfflineUpgradeShell(t, "resume-tools-probe", "mysql-router", fixture); err != nil ||
		!strings.Contains(output, "__AIFAR_UPGRADE_TOOL__\t26.10.0\n") {
		t.Fatalf("could not recognize backed-up Router version: %v\n%s", err, output)
	}
	for _, change := range []string{`rm "$backup/complete"`, `mv -T "$router_base.old-$task" "$work/lost"`} {
		if _, output, err := runOfflineUpgradeShell(t, "resume-tools-probe", "mysql-router", fixture+"\n"+change+"\n"); err == nil {
			t.Fatalf("recognized upgraded Router after evidence was removed (%s):\n%s", change, output)
		}
	}
}

func TestOfflineUpgradeShellRestartsRestoredRouterOnTerm(t *testing.T) {
	root, output, err := runOfflineUpgradeShell(t, "revert-router", "mysql-router", offlineUpgradeRouterFixture+`
mv -T "$router_base" "$router_base.old-$task"
mv -T "$router_next" "$router_base"
mv() {
  command mv "$@" || return
  if [[ "$*" == *"$router_base.old-$task $router_base" ]]; then
    kill -TERM "$$"
  fi
}
`)
	if err == nil {
		t.Fatal("terminated recovery unexpectedly succeeded")
	}
	running, readErr := os.ReadFile(filepath.Join(root, "work", "running"))
	if readErr != nil || string(running) != "old\n" {
		t.Fatalf("restored Router did not restart when recovery received TERM: %q %v\n%s", running, readErr, output)
	}
}

func TestOfflineUpgradeShellResumeIdentifiesInterruptedRouterEvenWhenStopped(t *testing.T) {
	fixture := offlineUpgradeRouterFixture + `
mv -T "$router_base" "$router_base.old-$task"
mv -T "$router_next" "$router_base"
rm "$work/running"
check_process() { return 1; }
`
	if _, output, err := runOfflineUpgradeShell(t, "resume-tools-probe", "mysql-router", fixture); err != nil ||
		!strings.Contains(output, "__AIFAR_UPGRADE_TOOL__\t26.10.0\n") {
		t.Fatalf("could not classify the stopped Router with a verified prior tree: %v\n%s", err, output)
	}
	for _, bad := range []string{
		`check_service_layout() { return 1; }`,
		`systemctl() { if [[ "$3" == ActiveState ]]; then printf 'active\n'; else printf '100\n'; fi; }`,
	} {
		if _, output, err := runOfflineUpgradeShell(t, "resume-tools-probe", "mysql-router", fixture+"\n"+bad+"\n"); err == nil {
			t.Fatalf("resume accepted a changed service definition or another running executable: %s", output)
		}
	}
}

func TestOfflineUpgradeShellResumeKeepsAlreadyUpgradedRouterRunning(t *testing.T) {
	_, output, err := runOfflineUpgradeShell(t, "tools", "mysql-router", offlineUpgradeRouterFixture+`
mv -T "$router_base" "$router_base.old-$task"
mv -T "$router_next" "$router_base"
check_process() { :; }
stop_cleanly() { printf 'Unexpected stop of an already upgraded Router\n' >&2; return 1; }
`)
	if err != nil {
		t.Fatalf("repeated tool phase tried to cut over again: %v\n%s", err, output)
	}
}

func TestOfflineUpgradeShellBackupCheckRejectsChecksumForDifferentArchive(t *testing.T) {
	fixture := `
as_root() { "$@"; }
findmnt() { printf 'test mount\n'; }
mkdir "$backup" "$router_base" "$root/router"
touch "$backup/complete"
for tree in mysql-router router; do
  printf '%s\n' "$tree" > "$backup/$tree.tar"
  sha256sum "$backup/$tree.tar" > "$backup/$tree.tar.sha256"
  printf 'test mount\n' > "$backup/$tree.mount"
done
`
	if _, output, err := runOfflineUpgradeShell(t, "resume-backup-check", "mysql-router", fixture); err != nil {
		t.Fatalf("valid backup check failed: %v\n%s", err, output)
	}
	if _, output, err := runOfflineUpgradeShell(t, "resume-backup-check", "mysql-router", fixture+
		`sha256sum "$backup/router.tar" > "$backup/mysql-router.tar.sha256"`); err == nil {
		t.Fatalf("a checksum that points at a different archive passed:\n%s", output)
	}
}

func TestOfflineUpgradeShellResumeOnlyRemovesItsOwnShutdownOverride(t *testing.T) {
	fixture := offlineUpgradeServerFixture + `
runtime_unit_dir="$work/unit.d"
mkdir "$runtime_unit_dir"
override="$runtime_unit_dir/92-aifar-upgrade-$task.conf"
printf '[Service]\nTimeoutStopSec=30min\n' > "$override"
check_version() {
  if [[ "$1" == "$server_next/bin/mysqld" ]]; then [[ "$3" == 8.4.13 ]]; else [[ "$3" == 8.0.36 ]]; fi
}
systemctl() {
  case "$1" in
    show) if [[ -e "$override" ]]; then printf '%s\n' "$override"; fi ;;
    daemon-reload) touch "$work/reloaded" ;;
  esac
}
`
	root, output, err := runOfflineUpgradeShell(t, "resume-server-check", "mysql", fixture)
	if err != nil {
		t.Fatalf("resume rejected an otherwise sound source node: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "work", "reloaded")); err != nil {
		t.Fatalf("resume did not reload systemd after removing its shutdown override: %v\n%s", err, output)
	}
	const foreignOverride = "[Service]\nTimeoutStopSec=2h\n"
	root, output, err = runOfflineUpgradeShell(t, "resume-server-check", "mysql", fixture+
		`printf '[Service]\nTimeoutStopSec=2h\n' > "$override"`)
	if err == nil {
		t.Fatal("resume accepted an unexpected override at its file path")
	}
	assertOfflineUpgradeShellFile(t, root, "work/unit.d/92-aifar-upgrade-test.conf", foreignOverride, output)
}

func TestOfflineUpgradeShellResumeValidatesLiveConfigAgainstOriginal(t *testing.T) {
	fixture := offlineUpgradeServerFixture + `
as_root() { "$@"; }
mv -T "$base" "$base.old-$task"
mv -T "$server_next" "$base"
mv -T "$config" "$config.old-$task"
read -r digest ignored < <(sha256sum "$config.old-$task")
printf '%s  %s\n' "$digest" "$config" > "$work/source-config.sha256"
printf 'configuration\n' > "$config"
printf 'unexpected old staging content\n' > "$candidate"
systemctl() { [[ "$1" == show ]]; }
check_version() {
  if [[ "$1" == "$base/bin/mysqld" ]]; then [[ "$3" == 8.4.13 ]]; else [[ "$3" == 8.0.36 ]]; fi
}
`
	if _, output, err := runOfflineUpgradeShell(t, "resume-server-check", "mysql", fixture); err != nil {
		t.Fatalf("resume rejected a live configuration derived from the original: %v\n%s", err, output)
	}
	if _, output, err := runOfflineUpgradeShell(t, "resume-server-check", "mysql", fixture+
		`printf 'unexpected live configuration\n' > "$config"`); err == nil {
		t.Fatalf("resume accepted a changed live configuration:\n%s", output)
	}
}

func TestOfflineUpgradeShellVerifiesReadOnlyRouterSelection(t *testing.T) {
	for _, tc := range []struct {
		uuid, readOnly string
		wantSuccess    bool
	}{
		{"00000000-0000-0000-0000-000000000002", "1", true},
		{"00000000-0000-0000-0000-000000000001", "1", false},
		{"00000000-0000-0000-0000-000000000003", "0", false},
	} {
		fixture := "timeout() { printf '__AIFAR_UPGRADE_ROUTER__\\t%s\\t%s\\n' " + tc.uuid + " " + tc.readOnly + "; }; sleep() { :; }"
		_, output, err := runOfflineUpgradeShell(t, "route-sql-ro", "mysql", fixture,
			"127.0.0.1", "6447", "00000000-0000-0000-0000-000000000002,00000000-0000-0000-0000-000000000003")
		if tc.wantSuccess != (err == nil) {
			t.Fatalf("read-only Router check for %s (read_only=%s): %v\n%s", tc.uuid, tc.readOnly, err, output)
		}
	}
}

func assertOfflineUpgradeShellFile(t *testing.T, root, relative, want, output string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, relative))
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, want %q (read error %v)\n%s", relative, data, want, err, output)
	}
}
