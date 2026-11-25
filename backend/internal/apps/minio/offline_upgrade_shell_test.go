package minio

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func runSiloUpgradeShell(t *testing.T, phase, behavior string) (string, string, error) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("requires Linux shell, tar, flock and mount tools")
	}
	dir := t.TempDir()
	root := filepath.Join(dir, "minio")
	backup := filepath.Join(dir, "_backups", "minio-silo", "task-one")
	before, dispatch, ok := strings.Cut(offlineSiloScript, "\ncase \"$phase\" in\n")
	if !ok {
		t.Fatal("could not load the Silo upgrade script")
	}
	lockPath := filepath.ToSlash(filepath.Join(dir, "test-phase.lock"))
	before = strings.ReplaceAll(before, "/run/lock/aifar-minio-offline-upgrade.lock", lockPath)
	script := before + "\n" + siloUpgradeShellFixture + "\n" + behavior + "\ncase \"$phase\" in\n" + dispatch
	cmd := exec.Command("bash", "-s", "--", phase, root, backup, dir, "http://127.0.0.1:9000", "http://192.0.2.21:9000", "9001",
		"2025-10-15T17-29-55Z", "2026-11-12T00-00-00Z", "silo.tar.gz", "unused", "mcli.tar.gz", "unused",
		"1", filepath.Join(root, "data"), "bucket")
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	return backup, string(output), err
}

const siloUpgradeShellFixture = `
mkdir -p "$root/bin" "$root/conf/mc" "$root/data/.minio.sys" "$candidate"
chmod 0700 "$backup" "$candidate"
unit_path="$stage/aifar-minio.service"
printf '%s\n' unit > "$unit_path"
printf '%s\n' environment > "$env_file"
printf '%s\n' config > "$mc_config/config.json"
make_test_binary() {
  printf '#!/bin/bash\nprintf "test version RELEASE.%s (fixture)\\n"\n' "$2" > "$1"
  chmod 0755 "$1"
}
make_test_binary "$server_bin" "$old_release"
make_test_binary "$legacy_mc" "$old_release"
make_test_binary "$candidate/silo" "$silo_release"
make_test_binary "$candidate/mcli" "$silo_release"
(cd "$candidate" && sha256sum silo mcli > binaries.sha256)
cp -- /proc/sys/kernel/random/boot_id "$backup/boot-id"
sha256sum -- "$server_bin" "$legacy_mc" "$unit_path" "$env_file" "$mc_config/config.json" > "$backup/source.sha256"
state_file="$stage/runtime"
printf 'active\n' > "$state_file"
findmnt() {
  if [[ "$*" == *MAJ:MIN ]]; then printf 'test-volume-mount\n'; else
    printf '/\n'
    if [[ -f "$stage/nested" ]]; then printf '%s\n' "${volumes[0]}/nested"; fi
  fi
}
verify_mount "${volumes[0]}" > "$backup/volume-0.mount"
check_unit() { [[ ! -e "$stage/bad-unit" ]]; }
check_active_process() { [[ "$(cat "$state_file")" == active ]] && verify_version "$server_bin" "$1"; }
readiness() { [[ "$(cat "$state_file")" == active ]]; }
sleep() { :; }
ss() { :; }
systemctl() {
  case "$1:$*" in
    show:*ActiveState*) cat "$state_file" ;;
    show:*MainPID*) if [[ "$(cat "$state_file")" == active ]]; then printf '42\n'; else printf '0\n'; fi ;;
    show:*Result*) printf 'success\n' ;;
    stop:*) touch "$stage/stop-called"; printf 'inactive\n' > "$state_file" ;;
    start:*) touch "$stage/start-called"; printf 'active\n' > "$state_file" ;;
    *) return 1 ;;
  esac
}
df() { printf 'Avail\n1073741824\n'; }

make_complete_backup() {
  printf 'inactive\n' > "$state_file"
  touch "$backup/stop-attempted"
  cp -a -- "$server_bin" "$backup/minio.original"
  cp -a -- "$unit_path" "$backup/$unit"
  printf '%s\n' "${volumes[@]}" > "$backup/minio-volume-paths.txt"
  tar -cf "$backup/minio-bin.tar" -C "$root/bin" .
  tar -cf "$backup/minio-conf.tar" -C "$root/conf" .
  tar -cf "$backup/volume-0.tar" -C "${volumes[0]}" .
  (cd "$backup" && sha256sum -- minio-bin.tar minio-conf.tar volume-0.tar "$unit" minio.original minio-volume-paths.txt volume-0.mount > checksums.txt)
  touch "$backup/cold-backup-complete"
}
`

func TestOfflineSiloShellMarksOnlyAnActualStopAttempt(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "stop", `touch "$stage/bad-unit"`)
	if err == nil {
		t.Fatal("stop accepted a changed unit")
	}
	assertSiloPathMissing(t, filepath.Join(backup, "stop-attempted"), output)
	backup, output, err = runSiloUpgradeShell(t, "stop", `printf 'changed\n' > "$env_file"`)
	if err == nil {
		t.Fatal("stop accepted a changed environment")
	}
	assertSiloPathMissing(t, filepath.Join(backup, "stop-attempted"), output)
	assertSiloPathMissing(t, filepath.Join(backup, "..", "..", "..", "stop-called"), output)
	backup, output, err = runSiloUpgradeShell(t, "stop", "")
	if err != nil {
		t.Fatalf("valid stop failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(backup, "stop-attempted")); err != nil {
		t.Fatalf("stop did not record its ownership: %v\n%s", err, output)
	}
}

func TestOfflineSiloShellAcceptsOnlyTheVerifiedLegacyMinIOBuild(t *testing.T) {
	const fixture = `
printf '#!/bin/bash\nprintf "minio version DEVELOPMENT.GOGET (fixture)\\n"\n' > "$server_bin"
verify_checksum() {
  [[ "$1" = "42ff331ae6dd701aaa25b8bec27c0a4dc381cdca13174f6a9aff42a4485d34e4" ]]
}
verify_version "$server_bin" "$old_release"
if verify_version "$server_bin" "$silo_release"; then exit 20; fi
verify_checksum() { return 1; }
if verify_version "$server_bin" "$old_release"; then exit 21; fi
exit 0
`
	_, output, err := runSiloUpgradeShell(t, "before", fixture)
	if err != nil {
		t.Fatalf("legacy MinIO release verification failed: %v\n%s", err, output)
	}
}

func TestOfflineSiloShellRecoveryDoesNotStopIntactMinIO(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "stop-for-recovery", "")
	if err != nil {
		t.Fatalf("intact original service failed recovery inspection: %v\n%s", err, output)
	}
	assertSiloPathMissing(t, filepath.Join(backup, "..", "..", "..", "stop-called"), output)
}

func TestOfflineSiloShellComparesBootIDAsAStream(t *testing.T) {
	const procfsCmp = `
cmp() {
  # Some cmp versions reject the reported zero-size procfs file before reading.
  if [[ "${@: -1}" = /proc/sys/kernel/random/boot_id ]]; then return 1; fi
  command cmp "$@"
}
`
	_, output, err := runSiloUpgradeShell(t, "state", procfsCmp)
	if err != nil || !strings.Contains(output, "old-running") {
		t.Fatalf("same boot ID did not preserve the running MinIO state: %v\n%s", err, output)
	}
	_, output, err = runSiloUpgradeShell(t, "state", procfsCmp+`printf 'different-boot-id\n' > "$backup/boot-id"`)
	if err == nil || strings.Contains(output, "old-running") {
		t.Fatalf("changed boot ID did not block resumption: %v\n%s", err, output)
	}
}

func TestOfflineSiloShellBackupRejectsLateNestedMount(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "backup", `
printf 'inactive\n' > "$state_file"
touch "$backup/stop-attempted" "$stage/nested"
`)
	if err == nil {
		t.Fatalf("cold backup accepted a newly mounted descendant:\n%s", output)
	}
	assertSiloPathMissing(t, filepath.Join(backup, "cold-backup-complete"), output)
}

func TestOfflineSiloShellBackupRetriesOnlyWhenArchiveIsIncomplete(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "backup", `
printf 'inactive\n' > "$state_file"
touch "$backup/stop-attempted"
printf 'incomplete\n' > "$backup/volume-0.tar.partial"
printf 'interrupted\n' > "$backup/minio-conf.tar"
printf 'leave unowned files intact\n' > "$backup/other.tar"
`)
	if err != nil {
		t.Fatalf("incomplete backup could not be retried: %v\n%s", err, output)
	}
	assertSiloPathMissing(t, filepath.Join(backup, "volume-0.tar.partial"), output)
	assertSiloFile(t, filepath.Join(backup, "other.tar"), "leave unowned files intact\n", output)
	assertSiloFile(t, filepath.Join(backup, "cold-backup-complete"), "", output)
	backup, output, err = runSiloUpgradeShell(t, "backup", `
make_complete_backup
printf 'corrupt\n' > "$backup/volume-0.tar"
`)
	if err == nil {
		t.Fatal("corrupt completed backup was accepted or overwritten")
	}
	assertSiloFile(t, filepath.Join(backup, "volume-0.tar"), "corrupt\n", output)
	assertSiloFile(t, filepath.Join(backup, "cold-backup-complete"), "", output)
}

func TestOfflineSiloShellSwitchResumesExactPartialBinaryMove(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "switch", `
make_complete_backup
touch "$backup/switch-attempted"
cp -- "$candidate/mcli" "$mcli"
`)
	if err != nil {
		t.Fatalf("could not finish partial switch: %v\n%s", err, output)
	}
	server := filepath.Join(backup, "..", "..", "..", "minio", "bin", "minio")
	content, err := os.ReadFile(server)
	if err != nil || !strings.Contains(string(content), "RELEASE.2026-11-12T00-00-00Z") {
		t.Fatalf("the server was not switched to the candidate: %s (%v)\n%s", content, err, output)
	}
}

func TestOfflineSiloShellStateRecognizesInterruptedAndRestoredLayouts(t *testing.T) {
	for _, tc := range []struct {
		name, setup, state string
	}{
		{"untouched", "", "old-running\t0\t0\t0\t0\t0"},
		{"untouched with a changed environment", `printf 'changed\n' > "$env_file"`, "old-running\t0\t0\t0\t0\t0"},
		{"partially switched", `make_complete_backup; touch "$backup/switch-attempted"; cp -- "$candidate/mcli" "$mcli"`, "old-stopped\t1\t1\t1\t0\t0"},
		{"restored", `touch "$backup/stop-attempted" "$backup/restore-attempted"`, "old-running\t1\t0\t0\t0\t1"},
		{"rebooted", `printf 'another-boot\n' > "$backup/boot-id"`, ""},
		{"externally stopped", `printf 'inactive\n' > "$state_file"`, ""},
		{"ambiguous marker", `ln -s "$backup/no-such-marker" "$backup/stop-attempted"`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, output, err := runSiloUpgradeShell(t, "state", tc.setup)
			if tc.state == "" {
				if err == nil {
					t.Fatalf("unowned state was accepted: %s", output)
				}
			} else if err != nil || output != "AIFAR_SILO_STATE\t"+tc.state+"\n" {
				t.Fatalf("unexpected state response: %v\n%s", err, output)
			}
		})
	}
}

func TestOfflineSiloShellNeverRestoresMinIOAfterSiloStart(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "restore-minio", `
make_complete_backup
touch "$backup/switch-attempted" "$backup/start-attempted"
cp -- "$candidate/mcli" "$mcli"
cp -- "$candidate/silo" "$server_bin"
`)
	if err == nil {
		t.Fatal("restored MinIO after Silo might have changed its data")
	}
	assertSiloPathMissing(t, filepath.Join(backup, "restore-attempted"), output)
	assertSiloPathMissing(t, filepath.Join(backup, "..", "..", "..", "start-called"), output)
}

func TestOfflineSiloShellIdleChecksOriginalWorkspace(t *testing.T) {
	if _, output, err := runSiloUpgradeShell(t, "idle", ""); err != nil || output != "" {
		t.Fatalf("idle check refused the original workspace: %v\n%s", err, output)
	}
	if _, output, err := runSiloUpgradeShell(t, "idle", `backup="$root/data"`); err == nil {
		t.Fatalf("idle check accepted a path that is not this task's backup: %s", output)
	}
}

func TestOfflineSiloShellVerifiesOldServiceWithoutChangingIt(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "verify-old", `printf 'changed\n' > "$env_file"`)
	if err != nil {
		t.Fatalf("untouched old service with correct running layout was rejected: %v\n%s", err, output)
	}
	assertSiloPathMissing(t, filepath.Join(backup, "restore-attempted"), output)
	assertSiloPathMissing(t, filepath.Join(backup, "..", "..", "..", "start-called"), output)
	if _, output, err := runSiloUpgradeShell(t, "verify-old", `readiness() { return 1; }`); err == nil {
		t.Fatalf("accepted an old service that was not ready: %s", output)
	}
}

func TestOfflineSiloShellRecoveryMarksRestoreBeforeRestartingOriginal(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "restore-minio", `
make_complete_backup
touch "$backup/switch-attempted"
cp -- "$candidate/silo" "$server_bin"
cp -- "$candidate/mcli" "$mcli"
`)
	if err != nil {
		t.Fatalf("binary recovery failed before a Silo start was attempted: %v\n%s", err, output)
	}
	if _, err := os.Stat(filepath.Join(backup, "restore-attempted")); err != nil {
		t.Fatalf("restore did not mark the checkpoint for old-service recovery: %v\n%s", err, output)
	}
}

func TestOfflineSiloShellStartKeepsAlreadyRunningCandidate(t *testing.T) {
	backup, output, err := runSiloUpgradeShell(t, "start", `
make_complete_backup
touch "$backup/switch-attempted" "$backup/start-attempted"
cp -- "$candidate/silo" "$server_bin"
cp -- "$candidate/mcli" "$mcli"
printf 'active\n' > "$state_file"
`)
	if err != nil {
		t.Fatalf("already running Silo was rejected: %v\n%s", err, output)
	}
	assertSiloPathMissing(t, filepath.Join(backup, "..", "..", "..", "start-called"), output)
}

func assertSiloPathMissing(t *testing.T, path, output string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("unexpected path %s (%v)\n%s", path, err, output)
	}
}

func assertSiloFile(t *testing.T, path, want, output string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || string(data) != want {
		t.Fatalf("%s = %q, want %q (%v)\n%s", path, data, want, err, output)
	}
}
