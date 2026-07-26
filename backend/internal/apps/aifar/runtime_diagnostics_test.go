package aifar

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
)

type runtimeDiagnosticStore struct {
	*fakeStore
	exports          map[string]store.DiagnosticExport
	saveErrForStatus map[string]error
}

func (s *runtimeDiagnosticStore) SaveDiagnosticExport(v store.DiagnosticExport) (store.DiagnosticExport, error) {
	if err := s.saveErrForStatus[v.Status]; err != nil {
		return store.DiagnosticExport{}, err
	}
	if s.exports == nil {
		s.exports = map[string]store.DiagnosticExport{}
	}
	s.exports[v.ID] = v
	return v, nil
}

func (s *runtimeDiagnosticStore) GetDiagnosticExport(id string) (store.DiagnosticExport, error) {
	v, ok := s.exports[id]
	if !ok {
		return store.DiagnosticExport{}, errors.New("diagnostic export not found")
	}
	return v, nil
}

type runtimeDiagnosticRemote struct {
	calls         int
	command       string
	commands      []string
	stdout        string
	stderr        string
	err           error
	run           func(context.Context, string) (adapter.CommandResult, error)
	streamContent []byte
	streamPath    string
	streamCalls   int
	streamErr     error
}

func (r *runtimeDiagnosticRemote) Run(ctx context.Context, _ store.Server, command string) (adapter.CommandResult, error) {
	r.calls++
	r.command = command
	r.commands = append(r.commands, command)
	if r.run != nil {
		return r.run(ctx, command)
	}
	return adapter.CommandResult{Stdout: r.stdout, Stderr: r.stderr}, r.err
}

func (*runtimeDiagnosticRemote) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

func (r *runtimeDiagnosticRemote) StreamFile(_ context.Context, _ store.Server, remotePath string, dst io.Writer) (int64, error) {
	r.streamCalls++
	r.streamPath = remotePath
	if r.streamErr != nil {
		return 0, r.streamErr
	}
	n, err := dst.Write(r.streamContent)
	return int64(n), err
}

func runtimeDiagnosticFixture(now time.Time) (*runtimeDiagnosticStore, store.AppInstance, store.Server, store.DiagnosticExport) {
	instance := store.AppInstance{
		ID:       "instance-1",
		App:      AppName,
		Version:  "runtime-v2",
		ServerID: "server-1",
		Status:   "running",
		Topology: "standalone",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin","credential":"do-not-export","runtimeSpec":{"env":{"PASSWORD":"do-not-export-either"}}}`,
	}
	server := store.Server{ID: "server-1", Name: "server", DeployDir: "/aifar/apps"}
	export := store.DiagnosticExport{
		ID: "diag_1234567890abcdef12345678", TaskID: "task-1", InstanceID: instance.ID, ServerID: server.ID,
		Status: "pending", Services: []string{"gateway"}, SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute),
		CreatedBy: "owner", CreatedAt: now, ExpiresAt: now.Add(runtimeDiagnosticRetention), CleanupStatus: "none",
	}
	db := &runtimeDiagnosticStore{
		fakeStore: &fakeStore{
			servers:   map[string]store.Server{server.ID: server},
			instances: []store.AppInstance{instance},
			deployments: []store.AIFARDeployment{{
				InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1, CurrentRevision: "rev-1",
				Status: "ready", MetadataJSON: `{"secret":"deployment-secret"}`, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			}},
			pods: []store.AIFARPod{{
				InstanceID: instance.ID, ServiceName: "gateway", Revision: "rev-1", PodID: "pod-1",
				ContainerName: "aifar-gateway-1", Port: 38000, Status: "running", Ready: true,
				MetadataJSON: `{"env":{"TOKEN":"pod-secret"}}`, CreatedAt: now.Add(-time.Hour), UpdatedAt: now,
			}},
			releases: []store.AppRelease{{
				InstanceID: instance.ID, App: AppName, Version: "runtime-v2", ReleaseID: "release-1", ServerID: server.ID,
				Status: "success", ManifestJSON: `{"credential":"release-secret"}`, ConfigHash: "config-hash",
				CreatedAt: now.Add(-time.Hour), ActivatedAt: now.Add(-30 * time.Minute),
			}},
		},
		exports: map[string]store.DiagnosticExport{export.ID: export},
	}
	return db, instance, server, export
}

func runtimeDiagnosticEstimateOutput() string {
	return strings.Join([]string{
		"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
		"AIFAR_DIAG_TOTAL\t100\t200\t300\t9000000000\t1610613036",
	}, "\n")
}

func runtimeDiagnosticExportOutput(exportID, archiveName string, warnings int) string {
	return strings.Join([]string{
		runtimeDiagnosticResultRecord,
		exportID + "/" + archiveName,
		archiveName,
		"1024",
		"300",
		strings.Repeat("a", 64),
		strconv.Itoa(warnings),
	}, "\t")
}

func TestExportRuntimeDiagnosticsPersistsReadyWithWarnings(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, _, export := runtimeDiagnosticFixture(now)
	archiveName := "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	remote := &runtimeDiagnosticRemote{}
	remote.run = func(_ context.Context, command string) (adapter.CommandResult, error) {
		switch {
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticEstimateOutput()}, nil
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_EXPORT"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticExportOutput(export.ID, archiveName, 1)}, nil
		default:
			return adapter.CommandResult{}, errors.New("unexpected remote command")
		}
	}
	log := &recordingStepLogger{}
	err := NewService(db, remote).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Language: "en", Actor: "owner",
	}, log, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.WarningCount != 1 || got.ArchiveName != archiveName {
		t.Fatalf("unexpected ready export: %+v", got)
	}
	if !strings.HasPrefix(got.RemoteRelativePath, export.ID+"/") || path.Clean(got.RemoteRelativePath) != got.RemoteRelativePath {
		t.Fatalf("unexpected controlled remote path: %q", got.RemoteRelativePath)
	}
	if got.ReadyAt.IsZero() || got.ExpiresAt.Sub(got.ReadyAt) != runtimeDiagnosticRetention {
		t.Fatalf("ready retention must be exactly 24h: ready=%s expires=%s", got.ReadyAt, got.ExpiresAt)
	}
	steps, targetStatus := log.snapshot()
	expectedSteps := []string{
		"load-instance", "validate-request", "estimate-size", "collect-file-logs", "collect-container-logs",
		"collect-diagnostics", "redact-and-manifest", "create-archive", "record-export",
	}
	for _, step := range expectedSteps {
		if !containsString(steps, step+"=success") {
			t.Fatalf("step %q did not finish successfully: %v", step, steps)
		}
	}
	if targetStatus != "success" {
		t.Fatalf("unexpected target status: %q", targetStatus)
	}
	joined := strings.Join(remote.commands, "\n")
	if !strings.Contains(joined, "setsid sh -s <<'AIFAR_RUNTIME_DIAGNOSTIC_EXPORT'") {
		t.Fatalf("export must start the embedded collector with setsid sh -s:\n%s", joined)
	}
	for _, secret := range []string{"do-not-export", "do-not-export-either", "deployment-secret", "pod-secret", "release-secret"} {
		if strings.Contains(joined, secret) {
			t.Fatalf("unallowlisted metadata leaked into export script: %q", secret)
		}
	}
	for _, allowlisted := range []string{`"instanceId":"instance-1"`, `"serviceName":"gateway"`, `"podId":"pod-1"`, `"releaseId":"release-1"`} {
		if !strings.Contains(joined, allowlisted) {
			t.Fatalf("allowlisted store data missing from export script: %s", allowlisted)
		}
	}
}

func TestExportRuntimeDiagnosticsCriticalFailureDeletesPartial(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, _, export := runtimeDiagnosticFixture(now)
	remote := &runtimeDiagnosticRemote{}
	remote.run = func(_ context.Context, command string) (adapter.CommandResult, error) {
		switch {
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticEstimateOutput()}, nil
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_EXPORT"):
			return adapter.CommandResult{}, errors.New("tar failed")
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP"):
			return adapter.CommandResult{}, nil
		default:
			return adapter.CommandResult{}, errors.New("unexpected remote command")
		}
	}
	err := NewService(db, remote).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Language: "en", Actor: "owner",
	}, &recordingStepLogger{}, nil)
	if err == nil {
		t.Fatal("expected archive failure")
	}
	got, getErr := db.GetDiagnosticExport(export.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status != "failed" || got.RemoteRelativePath != "" || got.CleanupStatus != "complete" {
		t.Fatalf("critical failure must be failed and cleaned without a ready path: %+v", got)
	}
	cleanup := commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP")
	if cleanup == "" || !strings.Contains(cleanup, "EXPORT_ID='"+export.ID+"'") {
		t.Fatalf("cleanup did not target the exact export id:\n%s", cleanup)
	}
	if strings.Contains(cleanup, "another-export") || strings.Contains(cleanup, `rm -rf -- "$INSTALL_ROOT/runtime/logs`) {
		t.Fatalf("cleanup escaped the failed export root:\n%s", cleanup)
	}
}

func TestExportRuntimeDiagnosticsRecordFailureRemovesPromotedArchive(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, _, export := runtimeDiagnosticFixture(now)
	db.saveErrForStatus = map[string]error{"ready": errors.New("database unavailable")}
	archiveName := "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	remote := &runtimeDiagnosticRemote{}
	remote.run = func(_ context.Context, command string) (adapter.CommandResult, error) {
		switch {
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticEstimateOutput()}, nil
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_EXPORT"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticExportOutput(export.ID, archiveName, 0)}, nil
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP"):
			return adapter.CommandResult{}, nil
		default:
			return adapter.CommandResult{}, errors.New("unexpected remote command")
		}
	}
	err := NewService(db, remote).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Language: "en", Actor: "owner",
	}, &recordingStepLogger{}, nil)
	if err == nil {
		t.Fatal("expected ready record failure")
	}
	cleanup := commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP")
	if !strings.Contains(cleanup, "EXPORT_ID='"+export.ID+"'") {
		t.Fatalf("record failure lost the promoted export cleanup identity:\n%s", cleanup)
	}
	got, getErr := db.GetDiagnosticExport(export.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status != "failed" || got.CleanupStatus != "complete" || got.RemoteRelativePath != "" {
		t.Fatalf("record failure must clean the promoted file and persist failed: %+v", got)
	}
}

func TestExportRuntimeDiagnosticsRejectsReadyWithoutMutationOrCleanup(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, _, export := runtimeDiagnosticFixture(now)
	export.Status = "ready"
	export.ArchiveName = "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	export.RemoteRelativePath = export.ID + "/" + export.ArchiveName
	export.ArchiveBytes = 1024
	export.UncompressedBytes = 300
	export.SHA256 = strings.Repeat("a", 64)
	export.ReadyAt = now
	export.ExpiresAt = now.Add(time.Hour)
	db.exports[export.ID] = export
	remote := &runtimeDiagnosticRemote{}
	err := NewService(db, remote).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Language: "en", Actor: "owner",
	}, &recordingStepLogger{}, nil)
	if err == nil {
		t.Fatal("expected ready export to be rejected")
	}
	got, getErr := db.GetDiagnosticExport(export.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status != "ready" || got.RemoteRelativePath != export.RemoteRelativePath || got.SHA256 != export.SHA256 {
		t.Fatalf("rejecting a terminal record must not mutate it: %+v", got)
	}
	if remote.calls != 0 {
		t.Fatalf("terminal record rejection must not run export or cleanup, calls=%d", remote.calls)
	}
}

func TestExportRuntimeDiagnosticsCancellationCleansOnlyOwnPartial(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, _, export := runtimeDiagnosticFixture(now)
	exportStarted := make(chan struct{})
	remote := &runtimeDiagnosticRemote{}
	remote.run = func(ctx context.Context, command string) (adapter.CommandResult, error) {
		switch {
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticEstimateOutput()}, nil
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_EXPORT"):
			close(exportStarted)
			<-ctx.Done()
			return adapter.CommandResult{}, ctx.Err()
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP"):
			if ctx.Err() != nil {
				t.Fatalf("cleanup must use an independent background context: %v", ctx.Err())
			}
			return adapter.CommandResult{}, nil
		default:
			return adapter.CommandResult{}, errors.New("unexpected remote command")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewService(db, remote).ExportRuntimeDiagnostics(ctx, RuntimeDiagnosticRequest{
			ExportID: export.ID, Instance: instance, Language: "en", Actor: "owner",
		}, &recordingStepLogger{}, nil)
	}()
	select {
	case <-exportStarted:
	case err := <-errCh:
		t.Fatalf("export exited before collector start: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for export collector start")
	}
	cancel()
	err := <-errCh
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	got, getErr := db.GetDiagnosticExport(export.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if got.Status != "cancelled" || got.RemoteRelativePath != "" || got.CleanupStatus != "complete" {
		t.Fatalf("cancelled export must clean only its incomplete roots: %+v", got)
	}
	cleanup := commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP")
	for _, required := range []string{
		"EXPORT_ID='" + export.ID + "'",
		`case "$pid" in`,
		`''|*[!0-9]*)`,
		`PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"`,
		`FINAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID"`,
	} {
		if !strings.Contains(cleanup, required) {
			t.Fatalf("cleanup is missing %q:\n%s", required, cleanup)
		}
	}
	if strings.Contains(cleanup, `rm -rf -- "$LOG_ROOT`) || strings.Contains(cleanup, `rm -rf -- "$INSTALL_ROOT/runtime/logs`) {
		t.Fatalf("cleanup must never remove original runtime logs:\n%s", cleanup)
	}
}

func TestRuntimeDiagnosticExportScriptSecurityContract(t *testing.T) {
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{
		InstallRoot:    "'/aifar/apps/admin'",
		ExportID:       "'diag_1234567890abcdef12345678'",
		InstanceID:     "'instance-1'",
		Services:       "'gateway oauth'",
		Since:          "'2026-07-27T07:00:00Z'",
		Until:          "'2026-07-27T08:00:00Z'",
		ArchiveBase:    "'aifar-diagnostics-instance-1-20260727T080000Z'",
		RuntimeSummary: `'{}'`,
		Deployments:    `'[]'`,
		Pods:           `'[]'`,
		ReleaseSummary: `'[]'`,
		Readme:         "'readme'",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{
		"umask 077",
		`PARTIAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID.partial"`,
		`FINAL_ROOT="$DIAGNOSTICS_ROOT/$EXPORT_ID"`,
		`printf '%s\n' "$$" > "$PARTIAL_ROOT/.collector.pid"`,
		`trap 'touch "$PARTIAL_ROOT/.cancelled" 2>/dev/null || true' INT TERM`,
		`find "$LOG_ROOT/$service" -xdev -type f`,
		`mkdir -p "$BUNDLE_ROOT/services/$service/file-logs" "$BUNDLE_ROOT/services/$service/container-logs"`,
		`-print0 > "$CANDIDATE_LIST"`,
		`xargs -0`,
		`case "$resolved" in`,
		`"$service_root"/*)`,
		`docker ps -a`,
		`label=aifar.instance=$INSTANCE_ID`,
		`label=aifar.service=$service`,
		`docker logs --since "$SINCE" --until "$UNTIL" --timestamps "$container"`,
		"3221225472",
		"json_escape()",
		`ulimit -f 2097152`,
		`tar -czf "$ARCHIVE_PARTIAL"`,
		`tar -tzf "$ARCHIVE_PARTIAL"`,
		`sha256sum "$ARCHIVE_PARTIAL"`,
		`rm -rf -- "$BUNDLE_ROOT"`,
		`mv "$PARTIAL_ROOT" "$FINAL_ROOT"`,
		`printf 'AIFAR_DIAG_RESULT\t%s\t%s\t%s\t%s\t%s\t%s\n'`,
		"stage_generated_redacted_file()",
		`stage_generated_redacted_file "diagnostics/containers.txt"`,
		`stage_generated_redacted_file "diagnostics/health-checks.txt"`,
		`stage_generated_redacted_file "diagnostics/agent-status.txt"`,
		`stage_generated_redacted_file "diagnostics/host-resources.txt"`,
		"in_private_key",
		`relative_lower=$(printf '%s' "$relative_name" | tr '[:upper:]' '[:lower:]')`,
		`.env.*|*/.env.*`,
		`*.cnf|*.toml|*.xml|*.jks|*.p12|*.pfx|*.keystore`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("export script is missing required contract %q:\n%s", required, script)
		}
	}
	for _, forbidden := range []string{
		"jq ", "python", "node ", `rm -rf -- "$LOG_ROOT`, `.env" "$BUNDLE_ROOT`, "runtimeSpec", "credential",
		`{{.Labels}}`, `s/([A-Za-z0-9+\/_=-]{80})[A-Za-z0-9+\/_=-]*/\1[REDACTED]/g`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("export script contains forbidden behavior %q:\n%s", forbidden, script)
		}
	}
	if got := strings.Count(script, `"$service_root"/*)`); got < 3 {
		t.Fatalf("file candidates must be prefix-revalidated before stat and staged reads, checks=%d:\n%s", got, script)
	}
	if !strings.Contains(script, "*.gz|*.zip|*.xz|*.bz2|*.zst") {
		t.Fatalf("compressed file logs must be skipped because byte-level redaction cannot sanitize them:\n%s", script)
	}
}

func TestRuntimeDiagnosticExportJSONEscape(t *testing.T) {
	sh := runtimeDiagnosticTestShell(t)
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	helper := shellFunction(t, script, "json_escape")
	input := "quote=\" slash=\\ tab=\t line1\nline2"
	cmd := exec.Command(sh, "-c", helper+"\nprintf '%s' \"$AIFAR_JSON_INPUT\" | json_escape")
	cmd.Env = append(os.Environ(), "AIFAR_JSON_INPUT="+input)
	got, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	want := `quote=\" slash=\\ tab=\t line1\nline2`
	if string(got) != want {
		t.Fatalf("json_escape mismatch: got %q want %q", got, want)
	}
}

func TestRuntimeDiagnosticExportRedactsStructuredAndMultilineSecrets(t *testing.T) {
	sh := runtimeDiagnosticTestShell(t)
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	helper := shellFunction(t, script, "redact_file")
	inputPath := filepath.ToSlash(filepath.Join(t.TempDir(), "input.log"))
	outputPath := filepath.ToSlash(filepath.Join(t.TempDir(), "output.log"))
	privateBody := strings.Repeat("A", 64)
	longToken := strings.Repeat("B", 80)
	input := strings.Join([]string{
		`{"password":"json-secret","token":"json-token"}`,
		"Authorization: Bearer bearer-secret",
		"-----BEGIN PRIVATE KEY-----",
		privateBody,
		"-----END PRIVATE KEY-----",
		longToken,
	}, "\n")
	if err := os.WriteFile(filepath.FromSlash(inputPath), []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(sh, "-c", helper+"\nredact_file \"$AIFAR_REDACT_INPUT\" \"$AIFAR_REDACT_OUTPUT\"")
	cmd.Env = append(os.Environ(), "AIFAR_REDACT_INPUT="+inputPath, "AIFAR_REDACT_OUTPUT="+outputPath)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("redact_file failed: %v: %s", err, output)
	}
	redacted, err := os.ReadFile(filepath.FromSlash(outputPath))
	if err != nil {
		t.Fatal(err)
	}
	for _, leaked := range []string{"json-secret", "json-token", "bearer-secret", privateBody, longToken, "BEGIN PRIVATE KEY", "END PRIVATE KEY"} {
		if strings.Contains(string(redacted), leaked) {
			t.Fatalf("redacted output leaked %q:\n%s", leaked, redacted)
		}
	}
	if !strings.Contains(string(redacted), "[REDACTED]") || !strings.Contains(string(redacted), "[REDACTED PRIVATE KEY]") {
		t.Fatalf("redacted markers are missing:\n%s", redacted)
	}
}

func TestRuntimeDiagnosticExportAndCleanupScriptsCannotBeOverridden(t *testing.T) {
	overrideRoot := t.TempDir()
	overrideDir := filepath.Join(overrideRoot, AppName)
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runtime-diagnostics-export.sh", "runtime-diagnostics-cleanup.sh"} {
		if err := os.WriteFile(filepath.Join(overrideDir, name), []byte("printf malicious-override\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AIFAR_INSTALLER_TEMPLATE_DIR", overrideRoot)
	exportScript, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	cleanupScript, err := renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"export": exportScript, "cleanup": cleanupScript} {
		if strings.Contains(script, "malicious-override") || !strings.Contains(script, "umask 077") {
			t.Fatalf("%s diagnostic script was not loaded exclusively from go:embed:\n%s", name, script)
		}
	}
}

func TestDiagnosticStreamUsesControlledRemotePath(t *testing.T) {
	now := time.Now().UTC()
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.Status = "ready"
	export.ArchiveName = "aifar-diagnostics-instance-1-20260727T080000Z.tar.gz"
	export.RemoteRelativePath = export.ID + "/" + export.ArchiveName
	export.ArchiveBytes = int64(len("archive"))
	export.SHA256 = strings.Repeat("a", 64)
	export.ReadyAt = now.Add(-time.Minute)
	export.ExpiresAt = now.Add(time.Hour)
	db.exports[export.ID] = export
	remote := &runtimeDiagnosticRemote{streamContent: []byte("archive")}
	var dst bytes.Buffer
	n, err := NewService(db, remote).StreamRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticStreamRequest{
		Instance: instance,
		Server:   server,
		Export: store.DiagnosticExport{
			ID: export.ID, RemoteRelativePath: "../../etc/shadow",
		},
		Language: "en",
	}, &dst)
	if err != nil {
		t.Fatal(err)
	}
	if n != int64(len("archive")) || dst.String() != "archive" {
		t.Fatalf("unexpected stream result: n=%d body=%q", n, dst.String())
	}
	wantPath := "/aifar/apps/admin/runtime/diagnostics/" + export.RemoteRelativePath
	if remote.streamCalls != 1 || remote.streamPath != wantPath {
		t.Fatalf("stream escaped controlled diagnostics root: calls=%d path=%q want=%q", remote.streamCalls, remote.streamPath, wantPath)
	}
	got, _ := db.GetDiagnosticExport(export.ID)
	if got.DownloadedAt.IsZero() {
		t.Fatal("successful stream must record downloadedAt")
	}
}

func TestDiagnosticStreamRejectsExpiredAndUnsafeRecords(t *testing.T) {
	now := time.Now().UTC()
	tests := map[string]func(*store.DiagnosticExport){
		"expired": func(v *store.DiagnosticExport) {
			v.Status = "ready"
			v.ExpiresAt = now.Add(-time.Second)
		},
		"unsafe path": func(v *store.DiagnosticExport) {
			v.Status = "ready"
			v.ExpiresAt = now.Add(time.Hour)
			v.RemoteRelativePath = "../etc/shadow"
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			db, instance, server, export := runtimeDiagnosticFixture(now)
			export.ArchiveName = "aifar-diagnostics-instance-1-20260727T080000Z.tar.gz"
			export.RemoteRelativePath = export.ID + "/" + export.ArchiveName
			export.ArchiveBytes = int64(len("archive"))
			export.SHA256 = strings.Repeat("a", 64)
			mutate(&export)
			db.exports[export.ID] = export
			remote := &runtimeDiagnosticRemote{streamContent: []byte("archive")}
			_, err := NewService(db, remote).StreamRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticStreamRequest{
				Instance: instance, Server: server, Export: export, Language: "en",
			}, io.Discard)
			if err == nil || remote.streamCalls != 0 {
				t.Fatalf("invalid record reached stream: err=%v calls=%d", err, remote.streamCalls)
			}
		})
	}
}

func TestDiagnosticDeleteConfirmsAbsenceBeforeDeleted(t *testing.T) {
	now := time.Now().UTC()
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.Status = "ready"
	export.ArchiveName = "aifar-diagnostics-instance-1-20260727T080000Z.tar.gz"
	export.RemoteRelativePath = export.ID + "/" + export.ArchiveName
	export.ArchiveBytes = 1024
	export.SHA256 = strings.Repeat("a", 64)
	export.ReadyAt = now.Add(-time.Minute)
	export.ExpiresAt = now.Add(time.Hour)
	db.exports[export.ID] = export
	remote := &runtimeDiagnosticRemote{}
	remote.run = func(_ context.Context, command string) (adapter.CommandResult, error) {
		if !strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP") {
			return adapter.CommandResult{}, errors.New("unexpected remote command")
		}
		return adapter.CommandResult{}, nil
	}
	err := NewService(db, remote).DeleteRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticDeleteRequest{
		Instance: instance,
		Server:   server,
		Export: store.DiagnosticExport{
			ID: export.ID, RemoteRelativePath: "../../etc/shadow",
		},
		Language: "en",
	}, fakeLogger{})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetDiagnosticExport(export.ID)
	if got.Status != "deleted" || got.CleanupStatus != "complete" || got.DeletedAt.IsZero() || got.CleanupAttemptedAt.IsZero() {
		t.Fatalf("delete must be recorded only after cleanup success: %+v", got)
	}
	cleanup := commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP")
	for _, required := range []string{
		"EXPORT_ID='" + export.ID + "'",
		`[ ! -e "$PARTIAL_ROOT" ]`,
		`[ ! -e "$FINAL_ROOT" ]`,
	} {
		if !strings.Contains(cleanup, required) {
			t.Fatalf("cleanup does not confirm remote absence with %q:\n%s", required, cleanup)
		}
	}
}

func TestDiagnosticDeleteDoesNotMarkDeletedWhenCleanupFails(t *testing.T) {
	now := time.Now().UTC()
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.Status = "ready"
	export.ArchiveName = "aifar-diagnostics-instance-1-20260727T080000Z.tar.gz"
	export.RemoteRelativePath = export.ID + "/" + export.ArchiveName
	export.ArchiveBytes = 1024
	export.SHA256 = strings.Repeat("a", 64)
	export.ReadyAt = now.Add(-time.Minute)
	export.ExpiresAt = now.Add(time.Hour)
	db.exports[export.ID] = export
	remote := &runtimeDiagnosticRemote{err: errors.New("cleanup failed")}
	err := NewService(db, remote).DeleteRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticDeleteRequest{
		Instance: instance, Server: server, Export: export, Language: "en",
	}, fakeLogger{})
	if err == nil {
		t.Fatal("expected cleanup failure")
	}
	got, _ := db.GetDiagnosticExport(export.ID)
	if got.Status == "deleted" || got.CleanupStatus != "failed" {
		t.Fatalf("failed cleanup must not mark export deleted: %+v", got)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func commandContaining(commands []string, marker string) string {
	for _, command := range commands {
		if strings.Contains(command, marker) {
			return command
		}
	}
	return ""
}

func shellFunction(t *testing.T, script, name string) string {
	t.Helper()
	start := strings.Index(script, name+"() {")
	if start < 0 {
		t.Fatalf("shell function %s is missing", name)
	}
	rest := script[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("shell function %s terminator is missing", name)
	}
	return rest[:end+3]
}

func runtimeDiagnosticTestShell(t *testing.T) string {
	t.Helper()
	if sh, err := exec.LookPath("sh"); err == nil {
		return sh
	}
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{`D:\tools\git\bin\sh.exe`, `C:\Program Files\Git\bin\sh.exe`} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
	}
	t.Skip("sh is not available")
	return ""
}

func TestEstimateRuntimeDiagnosticsRejectsDisabledAndUnknownServices(t *testing.T) {
	now := time.Now().UTC()
	instance := store.AppInstance{
		ID:       "instance-1",
		App:      AppName,
		ServerID: "server-1",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`,
	}
	db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
		{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1},
		{InstanceID: instance.ID, ServiceName: "oauth", DesiredReplicas: 0},
	}}}

	for _, service := range []string{"oauth", "../../etc"} {
		t.Run(service, func(t *testing.T) {
			remote := &runtimeDiagnosticRemote{}
			svc := NewService(db, remote)
			_, err := svc.EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
				Instance: instance,
				Server:   store.Server{ID: "server-1"},
				Services: []string{service},
				SinceAt:  now.Add(-time.Hour),
				UntilAt:  now.Add(-time.Minute),
			}, nil)
			if err == nil {
				t.Fatalf("expected service %q to be rejected", service)
			}
			if remote.calls != 0 {
				t.Fatalf("validation must reject %q before remote execution", service)
			}
		})
	}
}

func TestEstimateRuntimeDiagnosticsRendersTrustedSelectionAndComputesAllowed(t *testing.T) {
	now := time.Now().UTC()
	instance := store.AppInstance{
		ID:       "instance'; echo injected",
		App:      AppName,
		ServerID: "server-1",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin's runtime"}`,
	}
	db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
		{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1},
	}}}
	remote := &runtimeDiagnosticRemote{stdout: strings.Join([]string{
		"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
		"AIFAR_DIAG_TOTAL\t100\t200\t300\t9000000000\t1610613036",
		"AIFAR_DIAG_WARNING\tdocker-log-conservative\tgateway",
	}, "\n")}

	got, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		Instance: instance,
		Server:   store.Server{ID: "server-1"},
		Services: []string{"gateway"},
		SinceAt:  now.Add(-time.Hour),
		UntilAt:  now.Add(-time.Minute),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Allowed || got.TotalBytes != 300 || got.AvailableBytes != 9000000000 {
		t.Fatalf("unexpected estimate: %+v", got)
	}
	for _, want := range []string{
		`INSTALL_ROOT='/aifar/apps/admin'"'"'s runtime'`,
		`INSTANCE_ID='instance'"'"'; echo injected'`,
		`SERVICES='gateway'`,
	} {
		if !strings.Contains(remote.command, want) {
			t.Fatalf("rendered estimate command must quote trusted field %q:\n%s", want, remote.command)
		}
	}
}

func TestRuntimeDiagnosticEstimateRejectsHeredocDelimiterInjectionBeforeRemoteRun(t *testing.T) {
	now := time.Now().UTC()
	base := store.AppInstance{
		ID:       "instance-1",
		App:      AppName,
		ServerID: "server-1",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`,
	}
	tests := map[string]func(*store.AppInstance){
		"instance id": func(instance *store.AppInstance) {
			instance.ID = "instance-1\nAIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE\nprintf pwned"
		},
		"install root": func(instance *store.AppInstance) {
			instance.Metadata = `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin\nAIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE\nprintf pwned"}`
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			instance := base
			mutate(&instance)
			db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
				{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1},
			}}}
			remote := &runtimeDiagnosticRemote{}
			_, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
				Instance: instance,
				Server:   store.Server{ID: "server-1"},
				Services: []string{"gateway"},
				SinceAt:  now.Add(-time.Hour),
				UntilAt:  now.Add(-time.Minute),
			}, nil)
			if err == nil {
				t.Fatal("expected control-character injection to be rejected")
			}
			if remote.calls != 0 {
				t.Fatalf("unsafe heredoc input reached remote execution: calls=%d", remote.calls)
			}
		})
	}
}

func TestRuntimeDiagnosticEstimateScriptFailsClosedOnCandidateDiscovery(t *testing.T) {
	script, err := renderRuntimeDiagnosticEstimateScript(runtimeDiagnosticEstimateScriptData{
		InstallRoot: "'/aifar/apps/admin'",
		InstanceID:  "'instance-1'",
		Services:    "'gateway'",
		SinceUnix:   "'1'",
		UntilUnix:   "'2'",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{
		"for file_size in $(find",
		"for container_id in $(docker ps",
		"docker inspect --format='{{.LogPath}}' \"$container_id\" 2>/dev/null || true",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("estimate script must not swallow candidate discovery failure with %q:\n%s", forbidden, script)
		}
	}
	for _, required := range []string{
		"if ! file_sizes=$(find",
		"if ! container_ids=$(docker ps",
		"if ! log_path=$(docker inspect --format='{{.LogPath}}'",
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("estimate script must explicitly fail closed around %q:\n%s", required, script)
		}
	}
}

func TestEstimateRuntimeDiagnosticsRejectsInvalidDomainBeforeRemoteRun(t *testing.T) {
	now := time.Now().UTC()
	validInstance := store.AppInstance{ID: "instance-1", App: AppName, ServerID: "server-1", Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`}
	db := &runtimeDiagnosticStore{fakeStore: &fakeStore{deployments: []store.AIFARDeployment{
		{InstanceID: validInstance.ID, ServiceName: "gateway", DesiredReplicas: 1},
	}}}
	valid := RuntimeDiagnosticRequest{
		Instance: validInstance,
		Server:   store.Server{ID: "server-1"},
		Services: []string{"gateway"},
		SinceAt:  now.Add(-time.Hour),
		UntilAt:  now.Add(-time.Minute),
	}
	tests := map[string]func(*RuntimeDiagnosticRequest){
		"legacy instance":      func(req *RuntimeDiagnosticRequest) { req.Instance.Metadata = `{}` },
		"wrong server":         func(req *RuntimeDiagnosticRequest) { req.Server.ID = "server-2" },
		"empty selection":      func(req *RuntimeDiagnosticRequest) { req.Services = nil },
		"unnormalized service": func(req *RuntimeDiagnosticRequest) { req.Services = []string{"Gateway"} },
		"reversed window":      func(req *RuntimeDiagnosticRequest) { req.SinceAt = req.UntilAt },
		"oversized window": func(req *RuntimeDiagnosticRequest) {
			req.SinceAt = req.UntilAt.Add(-runtimeDiagnosticRetention - time.Second)
		},
		"future window": func(req *RuntimeDiagnosticRequest) { req.UntilAt = now.Add(time.Hour) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			req := valid
			mutate(&req)
			remote := &runtimeDiagnosticRemote{}
			if _, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), req, nil); err == nil {
				t.Fatal("expected invalid request to be rejected")
			}
			if remote.calls != 0 {
				t.Fatal("domain validation must finish before remote execution")
			}
		})
	}
}

func TestEstimateRuntimeDiagnosticsRequiresDiagnosticStoreCapability(t *testing.T) {
	remote := &runtimeDiagnosticRemote{}
	now := time.Now().UTC()
	_, err := NewService(&fakeStore{}, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		Instance: store.AppInstance{ID: "instance-1", App: AppName, ServerID: "server-1", Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`},
		Server:   store.Server{ID: "server-1"},
		Services: []string{"gateway"},
		SinceAt:  now.Add(-time.Hour),
		UntilAt:  now.Add(-time.Minute),
	}, nil)
	if err == nil || remote.calls != 0 {
		t.Fatalf("expected missing diagnostic store capability before remote run, err=%v calls=%d", err, remote.calls)
	}
}

func TestParseRuntimeDiagnosticEstimate(t *testing.T) {
	raw := strings.Join([]string{
		"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
		"AIFAR_DIAG_SERVICE\toauth\t50\t0",
		"AIFAR_DIAG_TOTAL\t150\t200\t350\t9000000000\t1610613086",
		"AIFAR_DIAG_WARNING\tdocker-log-conservative\tgateway",
	}, "\n")
	got, err := parseRuntimeDiagnosticEstimate(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.TotalBytes != 350 || got.AvailableBytes != 9000000000 || len(got.Services) != 2 {
		t.Fatalf("unexpected estimate: %+v", got)
	}
}

func TestParseRuntimeDiagnosticEstimateRejectsRequiredBytesMismatchAndOverflow(t *testing.T) {
	maxInt64 := strconv.FormatInt(1<<63-1, 10)
	tests := map[string]string{
		"required bytes mismatch": strings.Join([]string{
			"AIFAR_DIAG_SERVICE\tgateway\t100\t200",
			"AIFAR_DIAG_TOTAL\t100\t200\t300\t9000000000\t300",
		}, "\n"),
		"required bytes overflow": strings.Join([]string{
			"AIFAR_DIAG_SERVICE\tgateway\t" + maxInt64 + "\t0",
			"AIFAR_DIAG_TOTAL\t" + maxInt64 + "\t0\t" + maxInt64 + "\t" + maxInt64 + "\t" + maxInt64,
		}, "\n"),
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimeDiagnosticEstimate(raw); err == nil {
				t.Fatal("expected invalid required byte estimate to be rejected")
			}
		})
	}
}

func TestParseRuntimeDiagnosticEstimateRejectsMalformedProtocol(t *testing.T) {
	tests := map[string]string{
		"duplicate total":     "AIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"malformed integer":   "AIFAR_DIAG_SERVICE\tgateway\tbad\t0\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"negative bytes":      "AIFAR_DIAG_SERVICE\tgateway\t-1\t0\nAIFAR_DIAG_TOTAL\t-1\t0\t-1\t1\t1",
		"unknown service":     "AIFAR_DIAG_SERVICE\t../../etc\t0\t0\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"extra service field": "AIFAR_DIAG_SERVICE\tgateway\t0\t0\textra\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"extra total field":   "AIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1\textra",
		"extra warning field": "AIFAR_DIAG_WARNING\tcode\t-\textra\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"unknown line":        "AIFAR_DIAG_OTHER\t0\nAIFAR_DIAG_TOTAL\t0\t0\t0\t1\t1",
		"missing total":       "AIFAR_DIAG_SERVICE\tgateway\t0\t0",
	}
	for name, raw := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := parseRuntimeDiagnosticEstimate(raw); err == nil {
				t.Fatal("expected malformed protocol to be rejected")
			}
		})
	}
}
