package aifar

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type runtimeDiagnosticStore struct {
	*fakeStore
	exports                  map[string]store.DiagnosticExport
	saveErrForStatus         map[string]error
	beforeMarkDownloaded     func()
	beforeMarkCleanupPending func()
}

func (s *runtimeDiagnosticStore) ReserveDiagnosticExportBytes(id string, bytes, quota int64) (store.DiagnosticExportStorageUsage, error) {
	v, ok := s.exports[id]
	if !ok || v.StorageKind != "local" || (v.Status != "pending" && v.Status != "building") {
		return store.DiagnosticExportStorageUsage{}, errors.New("reservation record not found")
	}
	var readyBytes, reservedBytes int64
	for exportID, item := range s.exports {
		if item.StorageKind != "local" || !item.DeletedAt.IsZero() {
			continue
		}
		readyBytes += item.ArchiveBytes
		if exportID != id {
			reservedBytes += item.ReservedBytes
		}
	}
	if readyBytes+reservedBytes+bytes > quota {
		return store.DiagnosticExportStorageUsage{ReadyBytes: readyBytes, ReservedBytes: reservedBytes, QuotaBytes: quota}, store.ErrDiagnosticExportQuotaExceeded
	}
	v.ReservedBytes = bytes
	s.exports[id] = v
	return store.DiagnosticExportStorageUsage{ReadyBytes: readyBytes, ReservedBytes: reservedBytes + bytes, QuotaBytes: quota}, nil
}

func (s *runtimeDiagnosticStore) ReleaseDiagnosticExportReservation(id string) (bool, error) {
	v, ok := s.exports[id]
	if !ok || v.ReservedBytes == 0 {
		return false, nil
	}
	v.ReservedBytes = 0
	s.exports[id] = v
	return true, nil
}

func (s *runtimeDiagnosticStore) CommitLocalDiagnosticExport(commit store.LocalDiagnosticExportCommit) (store.DiagnosticExport, error) {
	v, ok := s.exports[commit.ID]
	if !ok || v.StorageKind != "local" || v.Status != "building" {
		return store.DiagnosticExport{}, errors.New("local commit record not found")
	}
	if err := s.saveErrForStatus["ready"]; err != nil {
		return store.DiagnosticExport{}, err
	}
	v.Status = "ready"
	v.StorageRelativePath = commit.StorageRelativePath
	v.ArchiveName = commit.ArchiveName
	v.ArchiveBytes = commit.ArchiveBytes
	v.UncompressedBytes = commit.UncompressedBytes
	v.SHA256 = commit.SHA256
	v.WarningCount = commit.WarningCount
	v.Warnings = append([]string(nil), commit.Warnings...)
	v.ReadyAt = commit.ReadyAt
	v.ExpiresAt = commit.ExpiresAt
	v.ReservedBytes = 0
	s.exports[commit.ID] = v
	return v, nil
}

func (s *runtimeDiagnosticStore) MarkDiagnosticExportFailed(id, errorText string, _ time.Time) (bool, error) {
	v, ok := s.exports[id]
	if !ok || (v.Status != "pending" && v.Status != "building") {
		return false, nil
	}
	v.Status = "failed"
	v.ErrorText = errorText
	v.ReservedBytes = 0
	s.exports[id] = v
	return true, nil
}

func (s *runtimeDiagnosticStore) ListDiagnosticExportsForReconcile() ([]store.DiagnosticExport, error) {
	result := make([]store.DiagnosticExport, 0, len(s.exports))
	for _, item := range s.exports {
		if item.StorageKind == "local" && item.DeletedAt.IsZero() {
			result = append(result, item)
		}
	}
	return result, nil
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

func (s *runtimeDiagnosticStore) MarkDiagnosticExportDownloaded(id string, downloadedAt time.Time) (bool, error) {
	if s.beforeMarkDownloaded != nil {
		s.beforeMarkDownloaded()
	}
	v, ok := s.exports[id]
	if !ok || v.Status != "ready" || !v.DeletedAt.IsZero() || !v.ExpiresAt.After(downloadedAt) {
		return false, nil
	}
	v.DownloadedAt = downloadedAt
	s.exports[id] = v
	return true, nil
}

func (s *runtimeDiagnosticStore) MarkDiagnosticExportCleanupPending(id string, attemptedAt time.Time) (bool, error) {
	if s.beforeMarkCleanupPending != nil {
		s.beforeMarkCleanupPending()
	}
	v, ok := s.exports[id]
	if !ok || !runtimeDiagnosticCleanupEligible(v) || v.CleanupStatus == "complete" {
		return false, nil
	}
	v.CleanupStatus = "pending"
	v.CleanupError = ""
	v.CleanupAttemptedAt = attemptedAt
	s.exports[id] = v
	return true, nil
}

func (s *runtimeDiagnosticStore) MarkDiagnosticExportCleanupFailed(id, cleanupError string) (bool, error) {
	v, ok := s.exports[id]
	if !ok || !runtimeDiagnosticCleanupEligible(v) || v.CleanupStatus != "pending" {
		return false, nil
	}
	v.CleanupStatus = "failed"
	v.CleanupError = cleanupError
	s.exports[id] = v
	return true, nil
}

func (s *runtimeDiagnosticStore) MarkDiagnosticExportDeleted(id string, deletedAt time.Time) (bool, error) {
	v, ok := s.exports[id]
	if !ok || !runtimeDiagnosticCleanupEligible(v) || v.CleanupStatus != "pending" {
		return false, nil
	}
	v.Status = "deleted"
	v.DeletedAt = deletedAt
	v.CleanupStatus = "complete"
	v.CleanupError = ""
	s.exports[id] = v
	return true, nil
}

func runtimeDiagnosticCleanupEligible(v store.DiagnosticExport) bool {
	if !v.DeletedAt.IsZero() {
		return false
	}
	switch v.Status {
	case "ready", "expired", "failed", "cancelled":
		return true
	default:
		return false
	}
}

type runtimeDiagnosticRemote struct {
	calls              int
	command            string
	commands           []string
	stdout             string
	stderr             string
	err                error
	run                func(context.Context, string) (adapter.CommandResult, error)
	streamContent      []byte
	streamPath         string
	streamCalls        int
	streamErr          error
	beforeStreamReturn func()
	commandStream      []byte
	commandStreamErr   error
	commandStreamCalls int
	commandStreamHook  func(context.Context, io.Writer) error
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
	if r.beforeStreamReturn != nil {
		r.beforeStreamReturn()
	}
	return int64(n), err
}

func (r *runtimeDiagnosticRemote) StreamCommand(ctx context.Context, _ store.Server, command string, dst io.Writer) (adapter.CommandStreamResult, error) {
	r.commandStreamCalls++
	r.command = command
	r.commands = append(r.commands, command)
	if r.commandStreamHook != nil {
		err := r.commandStreamHook(ctx, dst)
		return adapter.CommandStreamResult{}, err
	}
	if r.commandStreamErr != nil {
		return adapter.CommandStreamResult{}, r.commandStreamErr
	}
	n, err := dst.Write(r.commandStream)
	return adapter.CommandStreamResult{Bytes: int64(n)}, err
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
		StorageKind: "remote", CreatedBy: "owner", CreatedAt: now, ExpiresAt: now.Add(runtimeDiagnosticRetention), CleanupStatus: "none",
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

func TestRuntimeDiagnosticLocalStorageIsRequiredForNewExports(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, server, _ := runtimeDiagnosticFixture(now)
	remote := &runtimeDiagnosticRemote{stdout: strings.Join([]string{
		"AIFAR_DIAG_SERVICE_V2\tgateway\t1\t1024",
		"AIFAR_DIAG_TOTAL_V2\t1\t1024\tAsia/Shanghai\t-",
	}, "\n")}

	_, err := NewService(db, remote).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		Instance: instance, Server: server, Language: "en", Services: []string{"gateway"},
		SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute),
	}, nil)
	if err == nil {
		t.Fatal("estimate unexpectedly succeeded without configured local archive storage")
	}
}

func TestRuntimeDiagnosticLocalTransactionUsesSevenSteps(t *testing.T) {
	want := []string{
		"validate-local-storage",
		"discover-log-files",
		"filter-and-redact",
		"build-manifest",
		"stream-local-archive",
		"verify-local-archive",
		"cleanup-remote",
	}
	if !slices.Equal(runtimeDiagnosticSteps, want) {
		t.Fatalf("runtime diagnostic steps = %v, want %v", runtimeDiagnosticSteps, want)
	}
}

func TestEstimateRuntimeDiagnosticsCombinesRemoteMetadataAndLocalCapacity(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, server, _ := runtimeDiagnosticFixture(now)
	remote := &runtimeDiagnosticRemote{stdout: strings.Join([]string{
		"AIFAR_DIAG_SERVICE_V2\tgateway\t2\t33554432",
		"AIFAR_DIAG_TOTAL_V2\t2\t33554432\tAsia/Shanghai\t-",
	}, "\n")}
	archives := NewRuntimeDiagnosticArchiveStorage(t.TempDir(), 1<<30, runtimeDiagnosticRetention, db)
	estimate, err := NewServiceWithDiagnosticStorage(db, remote, archives).EstimateRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		Instance: instance, Server: server, Language: "en", Services: []string{"gateway"},
		SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !estimate.Allowed || estimate.LogSource != "host-mounted" || estimate.CandidateFiles != 2 || estimate.CandidateScanBytes != 33554432 {
		t.Fatalf("unexpected estimate metadata: %+v", estimate)
	}
	if estimate.LocalAvailableBytes <= 0 || estimate.LocalQuotaBytes != 1<<30 || estimate.MaxArchiveBytes != 256<<20 || estimate.TimeoutSeconds != 900 {
		t.Fatalf("unexpected local capacity or limits: %+v", estimate)
	}
	if estimate.EstimatedSecondsMin <= 0 || estimate.EstimatedSecondsMax < estimate.EstimatedSecondsMin || estimate.ExpiresAt.IsZero() {
		t.Fatalf("unexpected duration/expiry estimate: %+v", estimate)
	}
}

func TestLegacyRemoteDiagnosticExportStillStreamsAndDeletesRemotely(t *testing.T) {
	now := time.Now().UTC()
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.Status = "ready"
	export.StorageKind = "remote"
	export.ArchiveName = "aifar-diagnostics-instance-1-20260727T080000Z.tar.gz"
	export.RemoteRelativePath = path.Join(export.ID, export.ArchiveName)
	export.ArchiveBytes = int64(len("legacy-archive"))
	export.SHA256 = strings.Repeat("a", 64)
	export.ReadyAt = now.Add(-time.Minute)
	export.ExpiresAt = now.Add(time.Hour)
	db.exports[export.ID] = export
	remote := &runtimeDiagnosticRemote{streamContent: []byte("legacy-archive")}
	service := NewServiceWithDiagnosticStorage(db, remote, NewRuntimeDiagnosticArchiveStorage(t.TempDir(), 5<<30, runtimeDiagnosticRetention, db))
	var downloaded bytes.Buffer
	if _, err := service.StreamRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticStreamRequest{
		Export: export, Instance: instance, Server: server, Language: "en",
	}, &downloaded); err != nil {
		t.Fatal(err)
	}
	if downloaded.String() != "legacy-archive" || remote.streamCalls != 1 {
		t.Fatalf("legacy archive was not streamed remotely: body=%q calls=%d", downloaded.String(), remote.streamCalls)
	}
	if err := service.DeleteRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticDeleteRequest{
		Export: export, Instance: instance, Server: server, Language: "en",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP") == "" {
		t.Fatal("legacy remote archive cleanup was not executed")
	}
}

func TestExportRuntimeDiagnosticsStreamsIntoLocalStorage(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.StorageKind = "local"
	db.exports[export.ID] = export
	archiveName := "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	archive := runtimeDiagnosticTestArchive(t, strings.TrimSuffix(archiveName, ".tar.gz"))
	remote := &runtimeDiagnosticRemote{
		stdout: strings.Join([]string{
			"AIFAR_DIAG_SERVICE_V2\tgateway\t1\t1024",
			"AIFAR_DIAG_TOTAL_V2\t1\t1024\tAsia/Shanghai\t-",
		}, "\n"),
		commandStream: append([]byte(fmt.Sprintf("AIFAR_DIAG_STREAM_V1\t%s\t4096\t1\tAsia/Shanghai\n", archiveName)), archive...),
	}
	archives := NewRuntimeDiagnosticArchiveStorage(t.TempDir(), 5<<30, runtimeDiagnosticRetention, db)
	log := &recordingStepLogger{}
	if err := NewServiceWithDiagnosticStorage(db, remote, archives).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Server: server, Language: "en", Actor: "owner",
	}, log, nil); err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.StorageKind != "local" || got.StorageRelativePath != path.Join(export.ID, archiveName) || got.RemoteRelativePath != "" {
		t.Fatalf("unexpected local ready export: %+v", got)
	}
	if got.ArchiveBytes != int64(len(archive)) || got.WarningCount != 1 || got.ReservedBytes != 0 {
		t.Fatalf("unexpected local archive metadata: %+v", got)
	}
	if remote.commandStreamCalls != 1 || remote.streamCalls != 0 {
		t.Fatalf("unexpected stream calls: command=%d file=%d", remote.commandStreamCalls, remote.streamCalls)
	}
	steps, targetStatus := log.snapshot()
	for _, step := range runtimeDiagnosticSteps {
		if !containsString(steps, step+"=success") {
			t.Fatalf("step %q did not finish successfully: %v", step, steps)
		}
	}
	if targetStatus != "success" {
		t.Fatalf("target status = %q", targetStatus)
	}

	var downloaded bytes.Buffer
	if _, err := NewServiceWithDiagnosticStorage(db, remote, archives).StreamRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticStreamRequest{
		Export: got, Instance: instance, Server: server, Language: "en",
	}, &downloaded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(downloaded.Bytes(), archive) || remote.streamCalls != 0 {
		t.Fatal("local download did not read the aifar-server archive directly")
	}
	if err := NewServiceWithDiagnosticStorage(db, remote, archives).DeleteRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticDeleteRequest{
		Export: got, Instance: instance, Server: server, Language: "en",
	}, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := archives.Open(got.StorageRelativePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("deleted local archive remains readable: %v", err)
	}
}

func TestExportRuntimeDiagnosticsDeletesFinalFileWhenReadyCommitFails(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.StorageKind = "local"
	db.exports[export.ID] = export
	db.saveErrForStatus = map[string]error{"ready": errors.New("database unavailable")}
	archiveName := "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	archive := runtimeDiagnosticTestArchive(t, strings.TrimSuffix(archiveName, ".tar.gz"))
	remote := &runtimeDiagnosticRemote{
		stdout:        "AIFAR_DIAG_SERVICE_V2\tgateway\t1\t1024\nAIFAR_DIAG_TOTAL_V2\t1\t1024\tAsia/Shanghai\t-",
		commandStream: append([]byte(fmt.Sprintf("AIFAR_DIAG_STREAM_V1\t%s\t4096\t0\tAsia/Shanghai\n", archiveName)), archive...),
	}
	archives := NewRuntimeDiagnosticArchiveStorage(t.TempDir(), 5<<30, runtimeDiagnosticRetention, db)
	err := NewServiceWithDiagnosticStorage(db, remote, archives).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Server: server, Language: "en",
	}, &recordingStepLogger{}, nil)
	if err == nil {
		t.Fatal("expected local database commit failure")
	}
	if _, openErr := archives.Open(path.Join(export.ID, archiveName)); !errors.Is(openErr, os.ErrNotExist) {
		t.Fatalf("final archive survived failed database commit: %v", openErr)
	}
}

func TestExportRuntimeDiagnosticsRejectsArchiveAboveLimitAndCleansBothSides(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.StorageKind = "local"
	db.exports[export.ID] = export
	archiveName := "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	archive := runtimeDiagnosticTestArchive(t, strings.TrimSuffix(archiveName, ".tar.gz"))
	remote := &runtimeDiagnosticRemote{
		stdout:        "AIFAR_DIAG_SERVICE_V2\tgateway\t1\t1024\nAIFAR_DIAG_TOTAL_V2\t1\t1024\tAsia/Shanghai\t-",
		commandStream: append([]byte(fmt.Sprintf("AIFAR_DIAG_STREAM_V1\t%s\t4096\t0\tAsia/Shanghai\n", archiveName)), archive...),
	}
	root := t.TempDir()
	archives := newRuntimeDiagnosticArchiveStorageWithLimit(root, 5<<30, runtimeDiagnosticRetention, db, int64(len(archive)-1))
	err := NewServiceWithDiagnosticStorage(db, remote, archives).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Server: server, Language: "en",
	}, &recordingStepLogger{}, nil)
	if err == nil {
		t.Fatal("expected archive limit failure")
	}
	if _, statErr := os.Stat(filepath.Join(root, export.ID, archiveName+".partial")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("local partial survived archive limit failure: %v", statErr)
	}
	if commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP") == "" {
		t.Fatal("remote partial cleanup was not attempted")
	}
}

func TestExportRuntimeDiagnosticsCancellationAbortsLocalPartialAndRemoteWork(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, server, export := runtimeDiagnosticFixture(now)
	export.StorageKind = "local"
	db.exports[export.ID] = export
	archiveName := "aifar-diagnostics-instance-1-" + export.CreatedAt.UTC().Format("20060102T150405Z") + ".tar.gz"
	started := make(chan struct{})
	remote := &runtimeDiagnosticRemote{
		stdout: "AIFAR_DIAG_SERVICE_V2\tgateway\t1\t1024\nAIFAR_DIAG_TOTAL_V2\t1\t1024\tAsia/Shanghai\t-",
		commandStreamHook: func(ctx context.Context, dst io.Writer) error {
			if _, err := io.WriteString(dst, fmt.Sprintf("AIFAR_DIAG_STREAM_V1\t%s\t4096\t0\tAsia/Shanghai\n", archiveName)); err != nil {
				return err
			}
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	root := t.TempDir()
	archives := NewRuntimeDiagnosticArchiveStorage(root, 5<<30, runtimeDiagnosticRetention, db)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- NewServiceWithDiagnosticStorage(db, remote, archives).ExportRuntimeDiagnostics(ctx, RuntimeDiagnosticRequest{
			ExportID: export.ID, Instance: instance, Server: server, Language: "en",
		}, &recordingStepLogger{}, nil)
	}()
	select {
	case <-started:
		cancel()
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for streamed archive header")
	}
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("export error = %v, want context.Canceled", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, export.ID, archiveName+".partial")); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("local partial survived cancellation: %v", statErr)
	}
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "cancelled" || got.ReservedBytes != 0 {
		t.Fatalf("cancelled export state = %+v", got)
	}
	if commandContaining(remote.commands, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP") == "" {
		t.Fatal("remote partial cleanup was not attempted after cancellation")
	}
}

func runtimeDiagnosticTestArchive(t *testing.T, root string) []byte {
	t.Helper()
	var compressed bytes.Buffer
	gzipWriter := gzip.NewWriter(&compressed)
	tarWriter := tar.NewWriter(gzipWriter)
	for _, entry := range []struct {
		name string
		body string
	}{
		{root + "/README.txt", "readme\n"},
		{root + "/manifest.json", "{}\n"},
		{root + "/collection-errors.txt", ""},
	} {
		if err := tarWriter.WriteHeader(&tar.Header{Name: entry.name, Mode: 0o600, Size: int64(len(entry.body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tarWriter, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	return compressed.Bytes()
}

func legacyRemovedExportRuntimeDiagnosticsPersistsReadyWithWarnings(t *testing.T) {
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

func legacyRemovedExportRuntimeDiagnosticsCriticalFailureDeletesPartial(t *testing.T) {
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

func legacyRemovedExportRuntimeDiagnosticsRecordFailureRemovesPromotedArchive(t *testing.T) {
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

func legacyRemovedRuntimeDiagnosticExportScriptSecurityContract(t *testing.T) {
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
		`printf '%s\t%s\t%s\n' "$$" "$pid_start" "$pid_pgid" > "$PARTIAL_ROOT/.collector.pid"`,
		`trap 'touch "$PARTIAL_ROOT/.cancelled" 2>/dev/null || true' INT TERM`,
		`find "$service_root" -xdev -type f`,
		`mkdir -p "$BUNDLE_ROOT/services/$service/file-logs" "$BUNDLE_ROOT/services/$service/container-logs"`,
		`-print0 > "$CANDIDATE_LIST"`,
		`xargs -0`,
		`case "$resolved" in`,
		`"$service_root"/*)`,
		`docker ps -a`,
		`label=aifar.instance=$INSTANCE_ID`,
		`label=aifar.service=$service`,
		`docker logs --since "$SINCE" --until "$UNTIL" --timestamps "$container_id"`,
		`docker ps -a --no-trunc`,
		"revalidate_container_identity()",
		"3221225472",
		"json_escape()",
		`FILE_LIMIT_BLOCKS=1048576`,
		`remaining_blocks=$((remaining / 1024))`,
		`[ "$remaining_blocks" -gt 0 ]`,
		`[ "$snapshot_blocks" -le "$remaining_blocks" ]`,
		"run_limited()",
		"is_safe_log_relative()",
		`tar -czf "$ARCHIVE_PARTIAL"`,
		`tar -tzf "$ARCHIVE_PARTIAL"`,
		`archive_sha=$(sha256_file "$ARCHIVE_PARTIAL") || exit 28`,
		`BUNDLE_PARENT="$PARTIAL_ROOT/bundle"`,
		`WORK_ROOT="$PARTIAL_ROOT/work"`,
		`cp -P -- "$candidate" "$raw"`,
		`mv -Tn -- "$PARTIAL_ROOT" "$FINAL_ROOT"`,
		`[ ! -e "$PARTIAL_ROOT" ] && [ ! -L "$PARTIAL_ROOT" ] || exit 22`,
		`printf 'AIFAR_DIAG_RESULT\t%s\t%s\t%s\t%s\t%s\t%s\n'`,
		"stage_generated_redacted_file()",
		`stage_generated_redacted_file "diagnostics/containers.txt"`,
		`stage_generated_redacted_file "diagnostics/health-checks.txt"`,
		`stage_generated_redacted_file "diagnostics/agent-status.txt"`,
		`stage_generated_redacted_file "diagnostics/host-resources.txt"`,
		"in_private_key",
		`case "$last_component" in`,
	} {
		if !strings.Contains(script, required) {
			t.Fatalf("export script is missing required contract %q:\n%s", required, script)
		}
	}
	for _, forbidden := range []string{
		"jq ", "python", "node ", `rm -rf -- "$LOG_ROOT`, `.env" "$BUNDLE_ROOT`, "runtimeSpec",
		`{{.Labels}}`, `s/([A-Za-z0-9+\/_=-]{80})[A-Za-z0-9+\/_=-]*/\1[REDACTED]/g`,
		"2097152", "remaining / 512", "if ! (ulimit",
		`docker logs --since "$SINCE" --until "$UNTIL" --timestamps "$container"`,
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("export script contains forbidden behavior %q:\n%s", forbidden, script)
		}
	}
	if got := strings.Count(script, `"$service_root"/*)`); got < 3 {
		t.Fatalf("file candidates must be prefix-revalidated before stat and staged reads, checks=%d:\n%s", got, script)
	}
}

func legacyRemovedRuntimeDiagnosticExportUsesRevalidatedImmutableContainerIDs(t *testing.T) {
	containerID := strings.Repeat("a", 64)
	for _, tc := range []struct {
		name         string
		identityMode string
		wantLog      bool
	}{
		{name: "matching labels", identityMode: "match", wantLog: true},
		{name: "mismatched labels", identityMode: "mismatch", wantLog: false},
		{name: "vanished container", identityMode: "vanished", wantLog: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newRuntimeDiagnosticExportShellFixture(t)
			callsPath := filepath.Join(fixture.rootNative, "docker-calls")
			writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "docker", strings.Join([]string{
				`AIFAR_CONTAINER_ID=` + containerID,
				`AIFAR_IDENTITY_MODE=` + tc.identityMode,
				`printf '%s\n' "$*" >> "$AIFAR_DOCKER_CALLS"`,
				`case "${1:-}" in`,
				`  ps) printf '%s\n' "$AIFAR_CONTAINER_ID" ;;`,
				`  logs)`,
				`    last=""; for value do last=$value; done`,
				`    if [ "$last" = "reused-name" ]; then printf '%s\n' 'RACE_LEAK'; else printf '%s\n' 'safe immutable log'; fi`,
				`    ;;`,
				`  inspect)`,
				`    case "$*" in`,
				`      *'.Config.Labels'*)`,
				`        case "$AIFAR_IDENTITY_MODE" in`,
				`          match) printf 'instance-1\tgateway\t/original-name\n' ;;`,
				`          mismatch) printf 'other-instance\tgateway\t/reused-name\n' ;;`,
				`          vanished) exit 1 ;;`,
				`        esac`,
				`        ;;`,
				`      *) printf '%s\n' '{"Status":"healthy"}' ;;`,
				`    esac`,
				`    ;;`,
				`esac`,
			}, "\n"))

			output, err := fixture.run(
				"AIFAR_DOCKER_CALLS=" + runtimeDiagnosticShellPath(callsPath),
			)
			if err != nil {
				t.Fatalf("diagnostic export failed: %v: %s", err, output)
			}
			calls, err := os.ReadFile(callsPath)
			if err != nil {
				t.Fatal(err)
			}
			callsText := string(calls)
			if strings.Contains(callsText, "logs --since 2020-01-01T00:00:00Z --until 2035-01-01T00:00:00Z --timestamps reused-name") {
				t.Fatalf("docker logs used a mutable container name:\n%s", callsText)
			}
			if !strings.Contains(callsText, "{{.ID}}") {
				t.Fatalf("container enumeration did not use immutable IDs:\n%s", callsText)
			}
			for _, call := range strings.Split(callsText, "\n") {
				if strings.Contains(call, "label=aifar.service=") && strings.Contains(call, "{{.Names}}") {
					t.Fatalf("service container enumeration used mutable names:\n%s", callsText)
				}
			}

			listing := runtimeDiagnosticArchiveList(t, fixture.sh, fixture.archiveNative())
			entry := fixture.archiveBase + "/services/gateway/container-logs/original-name.log"
			if tc.wantLog {
				if !strings.Contains(listing, entry) {
					t.Fatalf("revalidated container log missing from archive:\n%s\ndocker calls:\n%s", listing, callsText)
				}
				content := runtimeDiagnosticArchiveFile(t, fixture.sh, fixture.archiveNative(), entry)
				if content != "safe immutable log\n" || !strings.Contains(callsText, "logs --since 2020-01-01T00:00:00Z --until 2035-01-01T00:00:00Z --timestamps "+containerID) {
					t.Fatalf("container log did not come from immutable ID: content=%q calls=%s", content, callsText)
				}
			} else {
				if strings.Contains(listing, "/services/gateway/container-logs/") && strings.Contains(listing, ".log") {
					t.Fatalf("vanished or mismatched container log entered archive:\n%s", listing)
				}
				errorsText := runtimeDiagnosticArchiveFile(t, fixture.sh, fixture.archiveNative(), fixture.archiveBase+"/collection-errors.txt")
				if !strings.Contains(errorsText, "container-identity-changed\tgateway\t-") {
					t.Fatalf("fixed identity warning missing: %s\ndocker calls:\n%s", errorsText, callsText)
				}
			}
		})
	}
}

func legacyRemovedRuntimeDiagnosticExportJSONEscape(t *testing.T) {
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

func legacyRemovedRuntimeDiagnosticExportRedactsStructuredAndMultilineSecrets(t *testing.T) {
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
	cmd := exec.Command(sh, "-c", helper+"\nredact_file \"$AIFAR_REDACT_INPUT\" \"$AIFAR_REDACT_OUTPUT\" \"$AIFAR_REDACT_SCRATCH\"")
	cmd.Env = append(os.Environ(),
		"AIFAR_REDACT_INPUT="+inputPath,
		"AIFAR_REDACT_OUTPUT="+outputPath,
		"AIFAR_REDACT_SCRATCH="+outputPath+".scratch",
	)
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

func TestRuntimeDiagnosticScriptsCannotBeOverridden(t *testing.T) {
	overrideRoot := t.TempDir()
	overrideDir := filepath.Join(overrideRoot, AppName)
	if err := os.MkdirAll(overrideDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runtime-diagnostics-estimate.sh", "runtime-diagnostics-export.sh", "runtime-diagnostics-cleanup.sh"} {
		if err := os.WriteFile(filepath.Join(overrideDir, name), []byte("printf malicious-override\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("AIFAR_INSTALLER_TEMPLATE_DIR", overrideRoot)
	estimateScript, err := renderRuntimeDiagnosticEstimateScript(runtimeDiagnosticEstimateScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	exportScript, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	cleanupScript, err := renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{"estimate": estimateScript, "export": exportScript, "cleanup": cleanupScript} {
		if strings.Contains(script, "malicious-override") || !strings.Contains(script, "set -eu") {
			t.Fatalf("%s diagnostic script was not loaded exclusively from go:embed:\n%s", name, script)
		}
	}
}

func TestRuntimeDiagnosticHostLogSourceSelectionNoDocker(t *testing.T) {
	estimateScript, err := renderRuntimeDiagnosticEstimateScript(runtimeDiagnosticEstimateScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	exportScript, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	combined := estimateScript + exportScript
	for _, forbidden := range []string{"docker logs", ".LogPath", "container-logs", "docker-log-conservative"} {
		if strings.Contains(combined, forbidden) {
			t.Fatalf("rendered scripts contain forbidden source %q", forbidden)
		}
	}
	for _, required := range []string{
		`LOG_ROOT="$INSTALL_ROOT/runtime/logs"`,
		"MAX_FILE_SCAN=1073741824",
		"MAX_TOTAL_SCAN=2147483648",
		"MAX_FILTERED=524288000",
		"AIFAR_DIAG_STREAM_V1",
	} {
		if !strings.Contains(combined, required) {
			t.Fatalf("rendered scripts missing %q", required)
		}
	}
	for _, accepted := range []string{"app.log", "app.log.1", "app.log.2026-07-27.1"} {
		if !runtimeDiagnosticLogFileAllowed(accepted) {
			t.Fatalf("safe log rotation rejected: %q", accepted)
		}
	}
	for _, rejected := range []string{"app.idx", ".hidden.log", "config.log", "database.log", "secret.log", "credentials.log", "app.env"} {
		if runtimeDiagnosticLogFileAllowed(rejected) {
			t.Fatalf("unsafe log name accepted: %q", rejected)
		}
	}
}

func TestRuntimeDiagnosticHostLogScriptsHaveValidBashSyntax(t *testing.T) {
	bashPath := findRuntimeDiagnosticBash(t)
	for name, render := range map[string]func() (string, error){
		"estimate": func() (string, error) {
			return renderRuntimeDiagnosticEstimateScript(runtimeDiagnosticEstimateScriptData{})
		},
		"export": func() (string, error) {
			return renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
		},
		"cleanup": func() (string, error) {
			return renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			script, err := render()
			if err != nil {
				t.Fatal(err)
			}
			command := exec.Command(bashPath, "-n")
			command.Stdin = strings.NewReader(script)
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("rendered script syntax error: %v output=%s", err, output)
			}
		})
	}
}

func TestRuntimeDiagnosticCleanupRemovesOnlyRemotePartial(t *testing.T) {
	script, err := renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(script, "FINAL_ROOT") || strings.Contains(script, `rm -rf -- "$PARTIAL_ROOT" "$FINAL_ROOT"`) {
		t.Fatal("cleanup script still targets a remote final archive")
	}
	if !strings.Contains(script, `rm -rf -- "$PARTIAL_ROOT"`) {
		t.Fatal("cleanup script does not remove the controlled partial root")
	}
}

func TestRuntimeDiagnosticEstimateV2Protocol(t *testing.T) {
	raw := "AIFAR_DIAG_SERVICE_V2\tgateway\t2\t1024\n" +
		"AIFAR_DIAG_SERVICE_V2\toauth\t1\t2048\n" +
		"AIFAR_DIAG_TOTAL_V2\t3\t3072\tAsia/Shanghai\t-\n"
	got, err := parseRuntimeDiagnosticEstimate(raw, []string{"gateway", "oauth"})
	if err != nil {
		t.Fatal(err)
	}
	if got.CandidateFiles != 3 || got.CandidateScanBytes != 3072 || got.ServerTimezone != "Asia/Shanghai" || got.BlockReason != "" {
		t.Fatalf("unexpected V2 total: %+v", got)
	}
	if len(got.Services) != 2 || got.Services[0].CandidateFiles != 2 || got.Services[0].CandidateScanBytes != 1024 {
		t.Fatalf("unexpected V2 services: %+v", got.Services)
	}
}

func TestRuntimeDiagnosticStreamHeaderProtocol(t *testing.T) {
	line := "AIFAR_DIAG_STREAM_V1\taifar-diagnostics-instance-1-20260727T080000Z.tar.gz\t4096\t2\tAsia/Shanghai\n"
	got, err := parseRuntimeDiagnosticStreamHeader(line)
	if err != nil {
		t.Fatal(err)
	}
	if got.ArchiveName != "aifar-diagnostics-instance-1-20260727T080000Z.tar.gz" || got.UncompressedBytes != 4096 || got.WarningCount != 2 || got.ServerTimezone != "Asia/Shanghai" {
		t.Fatalf("unexpected stream header: %+v", got)
	}
	for _, invalid := range []string{
		strings.TrimSuffix(line, "\n"),
		"AIFAR_DIAG_STREAM_V2\taifar-diagnostics-instance-1-20260727T080000Z.tar.gz\t4096\t2\tAsia/Shanghai\n",
		"AIFAR_DIAG_STREAM_V1\t../archive.tar.gz\t4096\t2\tAsia/Shanghai\n",
		"AIFAR_DIAG_STREAM_V1\taifar-diagnostics-instance-1-20260727T080000Z.tar.gz\t-1\t2\tAsia/Shanghai\n",
		"AIFAR_DIAG_STREAM_V1\taifar-diagnostics-instance-1-20260727T080000Z.tar.gz\t4096\t-1\tAsia/Shanghai\n",
		"AIFAR_DIAG_STREAM_V1\taifar-diagnostics-instance-1-20260727T080000Z.tar.gz\t4096\t2\t../../UTC\n",
		"AIFAR_DIAG_STREAM_V1\taifar-diagnostics-instance-1-20260727T080000Z.tar.gz\t4096\t2\tAsia/Shanghai\textra\n",
	} {
		if _, err := parseRuntimeDiagnosticStreamHeader(invalid); err == nil {
			t.Fatalf("invalid stream header accepted: %q", invalid)
		}
	}
}

func TestRuntimeDiagnosticTimestampFilterFixtures(t *testing.T) {
	awkCommand := findRuntimeDiagnosticGNUAwk(t)
	program, err := renderRuntimeDiagnosticFilterProgram()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name                   string
		fixture                string
		want                   string
		wantParser             string
		wantRecords            int
		wantWarnings           int
		unterminatedActiveTail bool
	}{
		{
			name: "spring", fixture: "spring.log", wantParser: "spring", wantRecords: 2,
			want: "2026-07-27 16:00:00,000 ERROR start-boundary\n" +
				"    at example.Main.run(Main.java:10)\n" +
				"Caused by: java.lang.IllegalStateException: boom\n" +
				"    Suppressed: java.lang.RuntimeException: suppressed\n" +
				"    ... 1 more\n" +
				"2026-07-27 16:09:59.999 INFO inside-window\n",
		},
		{
			name: "iso-json", fixture: "iso-json.log", wantParser: "mixed", wantRecords: 6,
			want: "2026-07-27T08:00:00Z iso-z-boundary\n" +
				"2026-07-27T16:01:00+08:00 iso-offset\n" +
				"{\"timestamp\":\"2026-07-27T08:02:00Z\",\"message\":\"timestamp\"}\n" +
				"{\"time\":\"2026-07-27T16:03:00+08:00\",\"message\":\"time\"}\n" +
				"{\"@timestamp\":\"2026-07-27T08:04:00Z\",\"message\":\"at-timestamp\"}\n" +
				"{\"ts\":\"2026-07-27T16:05:00+08:00\",\"message\":\"ts\"}\n",
		},
		{
			name: "nginx-access", fixture: "nginx-access.log", wantParser: "nginx-access", wantRecords: 2,
			want: "127.0.0.1 - - [27/Jul/2026:16:00:00 +0800] \"GET /start HTTP/1.1\" 200 1\n" +
				"127.0.0.1 - - [27/Jul/2026:16:09:59 +0800] \"GET /inside HTTP/1.1\" 200 1\n",
		},
		{
			name: "nginx-error", fixture: "nginx-error.log", wantParser: "nginx-error", wantRecords: 2,
			want: "2026/07/27 16:00:00 [error] start-boundary\n" +
				"2026/07/27 16:09:59 [warn] inside-window\n",
		},
		{
			name: "unknown", fixture: "unknown.log", wantParser: "spring", wantRecords: 1, wantWarnings: 4,
			want: "2026-07-27 16:00:00,000 INFO retained-record\n", unterminatedActiveTail: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixturePath := filepath.Join("testdata", "runtime-diagnostics", test.fixture)
			input, err := os.ReadFile(fixturePath)
			if err != nil {
				t.Fatal(err)
			}
			if test.unterminatedActiveTail {
				input = bytes.TrimSuffix(input, []byte("\n"))
			}
			tempDir := t.TempDir()
			programPath := filepath.Join(tempDir, "filter.awk")
			inputPath := filepath.Join(tempDir, "input.log")
			summaryPath := filepath.Join(tempDir, "summary.tsv")
			if err := os.WriteFile(programPath, []byte(program), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(inputPath, input, 0o600); err != nil {
				t.Fatal(err)
			}
			endedNewline := "1"
			if test.unterminatedActiveTail {
				endedNewline = "0"
			}
			arguments := []string{
				"-v", "since_epoch=1785139200",
				"-v", "until_epoch=1785139800",
				"-v", "server_tz=Asia/Shanghai",
				"-v", "initial_ended_newline=" + endedNewline,
				"-v", "summary_path=summary.tsv",
				"-v", "warning_path=warnings.tsv",
			}
			if runtime.GOOS == "windows" {
				arguments = append(arguments, "-v", "server_utc_offset_seconds=28800")
			}
			arguments = append(arguments, "-f", "filter.awk", "input.log")
			command := exec.Command(awkCommand, arguments...)
			command.Dir = tempDir
			command.Env = append(os.Environ(), "TZ=Asia/Shanghai")
			var stderr bytes.Buffer
			command.Stderr = &stderr
			filtered, err := command.Output()
			if err != nil {
				t.Fatalf("GNU awk filter failed: %v stderr=%s", err, stderr.String())
			}
			if stderr.Len() > 0 {
				t.Fatalf("GNU awk filter wrote stderr: %s", stderr.String())
			}
			summary, err := os.ReadFile(summaryPath)
			if err != nil {
				t.Fatal(err)
			}
			if string(filtered) != test.want {
				t.Fatalf("filtered output mismatch:\ngot:  %q\nwant: %q\nsummary: %q", filtered, test.want, summary)
			}
			wantSummary := fmt.Sprintf("AIFAR_DIAG_FILTER_V1\t%s\t%d\t%d\t%d\t%d\n",
				test.wantParser, len(input), len(test.want), test.wantRecords, test.wantWarnings)
			if string(summary) != wantSummary {
				t.Fatalf("summary=%q want=%q", summary, wantSummary)
			}
		})
	}
}

func findRuntimeDiagnosticGNUAwk(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"gawk", "awk"} {
		path, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		output, err := exec.Command(path, "--version").CombinedOutput()
		if err == nil && strings.Contains(strings.ToLower(string(output)), "gnu awk") {
			return path
		}
	}
	t.Skip("GNU awk is not available")
	return ""
}

func findRuntimeDiagnosticBash(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		for _, candidate := range []string{`D:\tools\Git\bin\bash.exe`, `D:\tools\Git\usr\bin\bash.exe`} {
			if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
				return candidate
			}
		}
	}
	path, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not available")
	}
	return path
}

func legacyRemovedRuntimeDiagnosticExportRedactionFailsWhenIntermediateCommandFails(t *testing.T) {
	sh := runtimeDiagnosticTestShell(t)
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	helper := shellFunction(t, script, "redact_file")
	root := t.TempDir()
	inputNative := filepath.Join(root, "missing-input.log")
	outputNative := filepath.Join(root, "output.log")
	inputPath := runtimeDiagnosticShellPath(inputNative)
	outputPath := runtimeDiagnosticShellPath(outputNative)
	cmd := exec.Command(sh, "-c", helper+"\nredact_file \"$AIFAR_REDACT_INPUT\" \"$AIFAR_REDACT_OUTPUT\" \"$AIFAR_REDACT_SCRATCH\"")
	cmd.Env = append(os.Environ(),
		"AIFAR_REDACT_INPUT="+inputPath,
		"AIFAR_REDACT_OUTPUT="+outputPath,
		"AIFAR_REDACT_SCRATCH="+outputPath+".scratch",
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("redact_file hid an intermediate command failure: %s", output)
	}
	if _, err := os.Stat(outputNative); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed redaction left an output eligible for archiving: %v", err)
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

func TestRuntimeDiagnosticStreamDoesNotResurrectDeletedRecord(t *testing.T) {
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
	remote.beforeStreamReturn = func() {
		concurrent := db.exports[export.ID]
		concurrent.Status = "deleted"
		concurrent.DeletedAt = now
		concurrent.CleanupStatus = "complete"
		db.exports[export.ID] = concurrent
	}
	_, err := NewService(db, remote).StreamRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticStreamRequest{
		Instance: instance, Server: server, Export: export, Language: "en",
	}, io.Discard)
	if err == nil {
		t.Fatal("expected stale download completion to be rejected")
	}
	got, _ := db.GetDiagnosticExport(export.ID)
	if got.Status != "deleted" || got.DeletedAt.IsZero() || !got.DownloadedAt.IsZero() {
		t.Fatalf("stale stream completion resurrected or mutated deleted record: %+v", got)
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

func TestRuntimeDiagnosticDeleteRejectsConcurrentDeletionBeforeCleanup(t *testing.T) {
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
	db.beforeMarkCleanupPending = func() {
		concurrent := db.exports[export.ID]
		concurrent.Status = "deleted"
		concurrent.DeletedAt = now
		concurrent.CleanupStatus = "complete"
		db.exports[export.ID] = concurrent
	}
	remote := &runtimeDiagnosticRemote{}
	err := NewService(db, remote).DeleteRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticDeleteRequest{
		Instance: instance, Server: server, Export: export, Language: "en",
	}, fakeLogger{})
	if err == nil {
		t.Fatal("expected stale cleanup transition to be rejected")
	}
	if remote.calls != 0 {
		t.Fatalf("cleanup ran after a concurrent delete, calls=%d", remote.calls)
	}
	got, _ := db.GetDiagnosticExport(export.ID)
	if got.Status != "deleted" || got.DeletedAt.IsZero() {
		t.Fatalf("concurrent deleted state was overwritten: %+v", got)
	}
}

func TestRuntimeDiagnosticDeleteRetriesFailedAndCancelledCleanup(t *testing.T) {
	for _, status := range []string{"failed", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			now := time.Now().UTC()
			db, instance, server, export := runtimeDiagnosticFixture(now)
			export.Status = status
			export.CleanupStatus = "failed"
			export.CleanupError = "previous cleanup failed"
			export.RemoteRelativePath = ""
			export.ArchiveName = ""
			export.SHA256 = ""
			db.exports[export.ID] = export
			remote := &runtimeDiagnosticRemote{}
			if err := NewService(db, remote).DeleteRuntimeDiagnosticExport(context.Background(), RuntimeDiagnosticDeleteRequest{
				Instance: instance, Server: server, Export: export, Language: "en",
			}, fakeLogger{}); err != nil {
				t.Fatal(err)
			}
			got, _ := db.GetDiagnosticExport(export.ID)
			if remote.calls != 1 || got.Status != "deleted" || got.CleanupStatus != "complete" {
				t.Fatalf("cleanup retry did not complete: calls=%d export=%+v", remote.calls, got)
			}
		})
	}
}

func legacyRemovedExportRuntimeDiagnosticsDoesNotClaimUnexecutedRemoteSteps(t *testing.T) {
	now := time.Now().UTC().Add(-time.Minute)
	db, instance, _, export := runtimeDiagnosticFixture(now)
	remote := &runtimeDiagnosticRemote{}
	remote.run = func(_ context.Context, command string) (adapter.CommandResult, error) {
		switch {
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE"):
			return adapter.CommandResult{Stdout: runtimeDiagnosticEstimateOutput()}, nil
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_EXPORT"):
			return adapter.CommandResult{}, errors.New("collector failed before container logs")
		case strings.Contains(command, "AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP"):
			return adapter.CommandResult{}, nil
		default:
			return adapter.CommandResult{}, errors.New("unexpected remote command")
		}
	}
	log := &recordingStepLogger{}
	err := NewService(db, remote).ExportRuntimeDiagnostics(context.Background(), RuntimeDiagnosticRequest{
		ExportID: export.ID, Instance: instance, Language: "en", Actor: "owner",
	}, log, nil)
	if err == nil {
		t.Fatal("expected collector failure")
	}
	steps, _ := log.snapshot()
	if !containsString(steps, "estimate-size=success") || !containsString(steps, "collect-file-logs=failed") {
		t.Fatalf("executed boundary was not recorded: %v", steps)
	}
	for _, step := range []string{"collect-container-logs", "collect-diagnostics", "redact-and-manifest", "create-archive"} {
		if containsString(steps, step+"=success") {
			t.Fatalf("unexecuted step %q was reported successful: %v", step, steps)
		}
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

func runtimeDiagnosticShellPath(value string) string {
	value = filepath.ToSlash(value)
	if runtime.GOOS == "windows" && len(value) >= 3 && value[1] == ':' {
		return "/" + strings.ToLower(value[:1]) + value[2:]
	}
	return value
}

type runtimeDiagnosticExportShellFixture struct {
	t             *testing.T
	sh            string
	rootNative    string
	installNative string
	binNative     string
	procNative    string
	installShell  string
	binShell      string
	procShell     string
	exportID      string
	archiveBase   string
	fileLimit     string
	maxStagedSize int64
}

func newRuntimeDiagnosticExportShellFixture(t *testing.T) *runtimeDiagnosticExportShellFixture {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(workingDir, ".runtime-diagnostic-shell-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	install := filepath.Join(root, "install")
	bin := filepath.Join(root, "bin")
	procRoot := filepath.Join(root, "proc")
	if err := os.MkdirAll(filepath.Join(install, "runtime", "logs", "gateway"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(procRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{
		"docker":    "exit 0",
		"systemctl": "printf '%s\n' 'agent active'",
		"uptime":    "printf '%s\n' 'up 1 hour'",
		"free":      "printf '%s\n' 'memory ok'",
		"setsid":    "exit 0",
	} {
		writeRuntimeDiagnosticShellCommand(t, bin, name, body)
	}
	return &runtimeDiagnosticExportShellFixture{
		t: t, sh: runtimeDiagnosticTestShell(t), rootNative: root, installNative: install, binNative: bin, procNative: procRoot,
		installShell: runtimeDiagnosticShellPath(install), binShell: runtimeDiagnosticShellPath(bin), procShell: runtimeDiagnosticShellPath(procRoot),
		exportID: "diag_1234567890abcdef12345678", archiveBase: "aifar-diagnostics-instance-1-20260727T080000Z",
	}
}

func (f *runtimeDiagnosticExportShellFixture) render() string {
	f.t.Helper()
	fileLimit := f.fileLimit
	if fileLimit == "" {
		fileLimit = "unlimited"
	}
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{
		InstallRoot:     installerkit.ShellQuote(f.installShell),
		ExportID:        installerkit.ShellQuote(f.exportID),
		InstanceID:      installerkit.ShellQuote("instance-1"),
		Services:        installerkit.ShellQuote("gateway"),
		Since:           installerkit.ShellQuote("2020-01-01T00:00:00Z"),
		Until:           installerkit.ShellQuote("2035-01-01T00:00:00Z"),
		ArchiveBase:     installerkit.ShellQuote(f.archiveBase),
		RuntimeSummary:  installerkit.ShellQuote(`{"instanceId":"instance-1"}`),
		Deployments:     installerkit.ShellQuote(`[]`),
		Pods:            installerkit.ShellQuote(`[]`),
		ReleaseSummary:  installerkit.ShellQuote(`[]`),
		Readme:          installerkit.ShellQuote("diagnostic readme"),
		ProcRoot:        installerkit.ShellQuote(f.procShell),
		FileLimitBlocks: installerkit.ShellQuote(fileLimit),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	if f.maxStagedSize > 0 {
		const productionLimit = "MAX_UNCOMPRESSED=3221225472"
		if strings.Count(script, productionLimit) != 1 {
			f.t.Fatalf("rendered script no longer exposes one fixed staged-data limit")
		}
		script = strings.Replace(script, productionLimit, "MAX_UNCOMPRESSED="+strconv.FormatInt(f.maxStagedSize, 10), 1)
	}
	return script
}

func (f *runtimeDiagnosticExportShellFixture) run(extraEnv ...string) ([]byte, error) {
	f.t.Helper()
	command := strings.Join([]string{
		`PATH="$AIFAR_FAKE_BIN:/usr/bin:/bin"; export PATH`,
		`mkdir -p "$AIFAR_PROC_ROOT/$$"`,
		`{ printf '%s (sh) S 1 %s' "$$" "$$"; field=6; while [ "$field" -le 21 ]; do printf ' 0'; field=$((field + 1)); done; printf ' 777\n'; } > "$AIFAR_PROC_ROOT/$$/stat"`,
		strings.ReplaceAll(f.render(), "\r\n", "\n"),
	}, "\n")
	cmd := exec.Command(f.sh, "-s")
	cmd.Stdin = strings.NewReader(command)
	cmd.Env = append(os.Environ(), append([]string{
		"AIFAR_FAKE_BIN=" + f.binShell,
		"AIFAR_PROC_ROOT=" + f.procShell,
	}, extraEnv...)...)
	return cmd.CombinedOutput()
}

func (f *runtimeDiagnosticExportShellFixture) archiveNative() string {
	return filepath.Join(f.installNative, "runtime", "diagnostics", f.exportID, f.archiveBase+".tar.gz")
}

func writeRuntimeDiagnosticShellCommand(t *testing.T, dir, name, body string) {
	t.Helper()
	content := "#!/bin/sh\nset -eu\n" + body + "\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

func runtimeDiagnosticRealShellCommand(t *testing.T, sh, name string) string {
	t.Helper()
	cmd := exec.Command(sh, "-c", "PATH=/usr/bin:/bin; command -v \"$AIFAR_COMMAND\"")
	cmd.Env = append(os.Environ(), "AIFAR_COMMAND="+name)
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("locate shell command %s: %v", name, err)
	}
	return strings.TrimSpace(string(output))
}

func runtimeDiagnosticArchiveList(t *testing.T, sh, archiveNative string) string {
	t.Helper()
	cmd := exec.Command(sh, "-c", "tar -tzf \"$AIFAR_ARCHIVE\"")
	cmd.Env = append(os.Environ(), "AIFAR_ARCHIVE="+runtimeDiagnosticShellPath(archiveNative))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("list diagnostic archive: %v: %s", err, output)
	}
	return string(output)
}

func runtimeDiagnosticArchiveFile(t *testing.T, sh, archiveNative, relative string) string {
	t.Helper()
	cmd := exec.Command(sh, "-c", "tar -xOzf \"$AIFAR_ARCHIVE\" \"$AIFAR_ENTRY\"")
	cmd.Env = append(os.Environ(),
		"AIFAR_ARCHIVE="+runtimeDiagnosticShellPath(archiveNative),
		"AIFAR_ENTRY="+relative,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("read diagnostic archive entry %s: %v: %s", relative, err, output)
	}
	return string(output)
}

func legacyRemovedRuntimeDiagnosticExportFailsClosedWhenFileLimitCannotBeSet(t *testing.T) {
	sh := runtimeDiagnosticTestShell(t)
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	helper := shellFunction(t, script, "run_limited")
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(workingDir, ".runtime-limit-setup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	markerNative := filepath.Join(root, "command-ran")
	setupMarkerRoot := runtimeDiagnosticShellPath(filepath.Join(root, "limit-markers"))
	cmd := exec.Command(sh, "-c", strings.Join([]string{
		helper,
		`ulimit() { return 99; }`,
		`mkdir -p "$AIFAR_LIMIT_SETUP_ROOT"`,
		`LIMIT_MARKER_ROOT=$AIFAR_LIMIT_SETUP_ROOT`,
		`run_limited unlimited sh -c 'touch "$AIFAR_LIMIT_MARKER"'`,
		`command_status=$?`,
		`printf '%s:%s\n' "$command_status" "${LIMIT_SETUP_FAILED:-unset}"`,
	}, "\n"))
	cmd.Env = append(os.Environ(),
		"AIFAR_LIMIT_MARKER="+runtimeDiagnosticShellPath(markerNative),
		"AIFAR_LIMIT_SETUP_ROOT="+setupMarkerRoot,
	)
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("inspect deterministic ulimit setup failure: %v: %s", err, output)
	}
	if !strings.HasSuffix(strings.TrimSpace(string(output)), ":1") {
		t.Fatalf("ulimit setup failure did not use an independent side channel: %s", output)
	}
	if _, err := os.Stat(markerNative); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("command ran after ulimit failure: %v", err)
	}
}

func TestRuntimeDiagnosticExportRejectsSymlinkDiagnosticsRoot(t *testing.T) {
	fixture := newRuntimeDiagnosticExportShellFixture(t)
	outside := filepath.Join(fixture.rootNative, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	diagnostics := filepath.Join(fixture.installNative, "runtime", "diagnostics")
	if err := os.Symlink(outside, diagnostics); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	if output, err := fixture.run(); err == nil {
		t.Fatalf("symlink diagnostics root was accepted: %s", output)
	}
	if _, err := os.Stat(filepath.Join(outside, fixture.exportID+".partial")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("export wrote through diagnostics symlink: %v", err)
	}
}

func TestRuntimeDiagnosticExportRejectsFinalDirectoryCreatedDuringPromotion(t *testing.T) {
	fixture := newRuntimeDiagnosticExportShellFixture(t)
	realMV := runtimeDiagnosticRealShellCommand(t, fixture.sh, "mv")
	writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "mv", strings.Join([]string{
		`last=""`,
		`for value do last=$value; done`,
		`if [ "$last" = "$AIFAR_FINAL_ROOT" ]; then mkdir -p "$AIFAR_FINAL_ROOT"; fi`,
		`exec "$AIFAR_REAL_MV" "$@"`,
	}, "\n"))
	finalRoot := runtimeDiagnosticShellPath(filepath.Join(fixture.installNative, "runtime", "diagnostics", fixture.exportID))
	if output, err := fixture.run("AIFAR_REAL_MV="+realMV, "AIFAR_FINAL_ROOT="+finalRoot); err == nil {
		t.Fatalf("promotion nested into a concurrently-created final directory: %s", output)
	}
}

func TestRuntimeDiagnosticExportRejectsSourceSwappedToSymlinkBeforeRead(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git for Windows sh cannot apply the numeric snapshot ulimit; exercised on Linux")
	}
	fixture := newRuntimeDiagnosticExportShellFixture(t)
	logPath := filepath.Join(fixture.installNative, "runtime", "logs", "gateway", "swap.log")
	secretPath := filepath.Join(fixture.rootNative, "outside-secret")
	if err := os.WriteFile(logPath, []byte("safe log"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secretPath, []byte("TOP_SECRET_PAYLOAD"), 0o600); err != nil {
		t.Fatal(err)
	}
	realReadlink := runtimeDiagnosticRealShellCommand(t, fixture.sh, "readlink")
	realCP := runtimeDiagnosticRealShellCommand(t, fixture.sh, "cp")
	writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "readlink", strings.Join([]string{
		`last=""`,
		`for value do last=$value; done`,
		`result=$("$AIFAR_REAL_READLINK" "$@")`,
		`case "$last" in`,
		`  *swap.log)`,
		`    count=0`,
		`    [ ! -f "$AIFAR_READLINK_COUNT" ] || count=$(cat "$AIFAR_READLINK_COUNT")`,
		`    count=$((count + 1))`,
		`    printf '%s\n' "$count" > "$AIFAR_READLINK_COUNT"`,
		`    if [ "$count" -eq 3 ]; then rm -f -- "$last"; ln -s "$AIFAR_SECRET_PATH" "$last"; fi`,
		`    ;;`,
		`esac`,
		`printf '%s\n' "$result"`,
	}, "\n"))
	writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "cp", strings.Join([]string{
		`case "$3" in *swap.log) exit 75 ;; esac`,
		`exec "$AIFAR_REAL_CP" "$@"`,
	}, "\n"))
	output, err := fixture.run(
		"AIFAR_REAL_READLINK="+realReadlink,
		"AIFAR_REAL_CP="+realCP,
		"AIFAR_READLINK_COUNT="+runtimeDiagnosticShellPath(filepath.Join(fixture.rootNative, "readlink-count")),
		"AIFAR_SECRET_PATH="+runtimeDiagnosticShellPath(secretPath),
	)
	if err != nil {
		t.Fatalf("source-swap export should skip the changed source, not fail globally: %v: %s", err, output)
	}
	listing := runtimeDiagnosticArchiveList(t, fixture.sh, fixture.archiveNative())
	if strings.Contains(listing, "swap.log") {
		content := runtimeDiagnosticArchiveFile(t, fixture.sh, fixture.archiveNative(), fixture.archiveBase+"/services/gateway/file-logs/swap.log")
		t.Fatalf("source swapped to symlink entered archive with %q", content)
	}
}

func TestRuntimeDiagnosticExportFailsWhenSHA256CommandFails(t *testing.T) {
	fixture := newRuntimeDiagnosticExportShellFixture(t)
	writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "sha256sum", "printf '%064d  %s\n' 0 \"$1\"\nexit 74")
	if output, err := fixture.run(); err == nil {
		t.Fatalf("sha256sum failure was hidden by a pipeline: %s", output)
	}
	if _, err := os.Stat(fixture.archiveNative()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("archive was promoted after sha256sum failure: %v", err)
	}
}

func TestRuntimeDiagnosticExportExcludesSensitiveNamesAndIntermediateFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Git for Windows sh cannot apply the numeric snapshot ulimit; exercised on Linux")
	}
	fixture := newRuntimeDiagnosticExportShellFixture(t)
	logRoot := filepath.Join(fixture.installNative, "runtime", "logs", "gateway")
	files := []struct {
		name    string
		content string
	}{
		{name: "app.log", content: "safe content"},
		{name: "app.log.1", content: "safe rotated content"},
		{name: "dump.rdb", content: "redis secret"},
		{name: "table.IBD", content: "mysql secret"},
		{name: "access.PPK", content: "ssh secret"},
		{name: "credentials.txt", content: "credential secret"},
		{name: "id_ecdsa", content: "private key"},
		{name: "appendonly.aof", content: "redis data"},
		{name: "ibdata1", content: "mysql data"},
		{name: filepath.Join(".hidden", "app.log"), content: "hidden path"},
		{name: filepath.Join("credentials", "app.log"), content: "sensitive path"},
		{name: filepath.Join("data", "app.log"), content: "sensitive data path"},
		{name: filepath.Join("config", "credentials"), content: "credential secret"},
	}
	if runtime.GOOS != "windows" {
		files = append(files, struct {
			name    string
			content string
		}{name: "app.log\nforged.log", content: "newline bypass"})
	}
	for _, file := range files {
		fullPath := filepath.Join(logRoot, file.name)
		if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(fullPath, []byte(file.content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	output, err := fixture.run()
	if err != nil {
		t.Fatalf("diagnostic export failed: %v: %s", err, output)
	}
	listing := runtimeDiagnosticArchiveList(t, fixture.sh, fixture.archiveNative())
	for _, allowed := range []string{
		fixture.archiveBase + "/services/gateway/file-logs/app.log",
		fixture.archiveBase + "/services/gateway/file-logs/app.log.1",
	} {
		if !strings.Contains(listing, allowed) {
			t.Fatalf("archive omitted allowlisted log %q:\n%s", allowed, listing)
		}
	}
	for _, forbidden := range []string{
		"dump.rdb", "table.IBD", "access.PPK", "credentials.txt", "id_ecdsa", "appendonly.aof", "ibdata1",
		"forged.log", ".hidden/app.log", "credentials/app.log", "data/app.log", "config/credentials", ".partial", ".raw", ".work",
	} {
		if strings.Contains(listing, forbidden) {
			t.Fatalf("archive contains forbidden entry %q:\n%s", forbidden, listing)
		}
	}
	errorsText := runtimeDiagnosticArchiveFile(t, fixture.sh, fixture.archiveNative(), fixture.archiveBase+"/collection-errors.txt")
	for _, sensitiveName := range []string{
		"dump.rdb", "table.IBD", "access.PPK", "credentials.txt", "id_ecdsa", "appendonly.aof", "ibdata1",
		"forged.log", ".hidden/app.log", "credentials/app.log", "data/app.log", "config/credentials",
	} {
		if strings.Contains(errorsText, sensitiveName) {
			t.Fatalf("collection errors leaked rejected path %q: %s", sensitiveName, errorsText)
		}
	}
}

func legacyRemovedRuntimeDiagnosticExportLogAllowlistRejectsUnsafeRelativePaths(t *testing.T) {
	sh := runtimeDiagnosticTestShell(t)
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{})
	if err != nil {
		t.Fatal(err)
	}
	helper := shellFunction(t, script, "is_safe_log_relative")
	for _, relative := range []string{
		"credentials.txt", "id_ecdsa", "appendonly.aof", "ibdata1", "app.log\nforged.log",
		".hidden/app.log", "nested/.hidden/app.log", "credentials/app.log", "config/app.log", "data/app.log", "key/app.log",
	} {
		cmd := exec.Command(sh, "-c", helper+"\nis_safe_log_relative \"$AIFAR_RELATIVE\"")
		cmd.Env = append(os.Environ(), "AIFAR_RELATIVE="+relative)
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("unsafe relative log path %q was accepted: %s", relative, output)
		}
	}
	for _, relative := range []string{"app.log", "app.log.1", "nested/app.log", "nested/app.log.42"} {
		cmd := exec.Command(sh, "-c", helper+"\nis_safe_log_relative \"$AIFAR_RELATIVE\"")
		cmd.Env = append(os.Environ(), "AIFAR_RELATIVE="+relative)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("allowlisted relative log path %q was rejected: %v: %s", relative, err, output)
		}
	}
}

func legacyRemovedRuntimeDiagnosticExportContinuesAfterIndividualDiagnosticCommandFailure(t *testing.T) {
	tests := []struct {
		name             string
		configure        func(*runtimeDiagnosticExportShellFixture)
		wantErrorCode    string
		absentEntry      string
		inspectEntry     string
		forbiddenContent string
	}{
		{
			name: "container log",
			configure: func(fixture *runtimeDiagnosticExportShellFixture) {
				containerID := strings.Repeat("b", 64)
				writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "docker", strings.Join([]string{
					`case "${1:-}" in`,
					`  ps)`,
					`    for value do case "$value" in *ID*) printf '%s\n' '` + containerID + `'; exit 0 ;; esac; done`,
					`    printf '%s\n' 'container table'`,
					`    ;;`,
					`  logs) printf '%s\n' 'CONTAINER_FAILURE_SHOULD_NOT_ARCHIVE'; exit 42 ;;`,
					`  inspect)`,
					`    case "$*" in *'.Config.Labels'*) printf 'instance-1\tgateway\t/container-1\n' ;; *) printf '%s\n' '{"Status":"healthy"}' ;; esac`,
					`    ;;`,
					`esac`,
				}, "\n"))
			},
			wantErrorCode: "container-log-failed\tgateway\t-",
			absentEntry:   "/services/gateway/container-logs/container-1.log",
		},
		{
			name: "systemctl status",
			configure: func(fixture *runtimeDiagnosticExportShellFixture) {
				writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "systemctl", "printf '%s\\n' 'SYSTEMCTL_FAILURE_SHOULD_NOT_ARCHIVE'\nexit 70")
			},
			wantErrorCode: "agent-status-failed\t-\t-",
			absentEntry:   "/diagnostics/agent-status.txt",
		},
		{
			name: "container health inspect",
			configure: func(fixture *runtimeDiagnosticExportShellFixture) {
				containerID := strings.Repeat("c", 64)
				writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "docker", strings.Join([]string{
					`case "${1:-}" in`,
					`  ps)`,
					`    for value do case "$value" in *ID*) printf '%s\n' '` + containerID + `'; exit 0 ;; esac; done`,
					`    printf '%s\n' 'container table'`,
					`    ;;`,
					`  logs) printf '%s\n' 'safe container log' ;;`,
					`  inspect)`,
					`    case "$*" in *'.Config.Labels'*) printf 'instance-1\tgateway\t/container-1\n' ;; *) printf '%s\n' 'INSPECT_FAILURE_SHOULD_NOT_ARCHIVE'; exit 45 ;; esac`,
					`    ;;`,
					`esac`,
				}, "\n"))
			},
			wantErrorCode:    "container-health-failed\tgateway\t-",
			inspectEntry:     "/diagnostics/health-checks.txt",
			forbiddenContent: "INSPECT_FAILURE_SHOULD_NOT_ARCHIVE",
		},
		{
			name: "host uptime",
			configure: func(fixture *runtimeDiagnosticExportShellFixture) {
				writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "uptime", "printf '%s\\n' 'HOST_FAILURE_SHOULD_NOT_ARCHIVE'\nexit 44")
			},
			wantErrorCode:    "host-uptime-failed\t-\t-",
			inspectEntry:     "/diagnostics/host-resources.txt",
			forbiddenContent: "HOST_FAILURE_SHOULD_NOT_ARCHIVE",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fixture := newRuntimeDiagnosticExportShellFixture(t)
			tt.configure(fixture)
			output, err := fixture.run()
			if err != nil {
				t.Fatalf("single diagnostic command failure aborted export: %v: %s", err, output)
			}
			result, err := parseRuntimeDiagnosticExportResult(string(output), fixture.exportID)
			if err != nil {
				t.Fatalf("successful export omitted RESULT: %v: %s", err, output)
			}
			if result.WarningCount < 1 {
				t.Fatalf("single diagnostic command failure did not increment warnings: %+v", result)
			}
			errorsText := runtimeDiagnosticArchiveFile(t, fixture.sh, fixture.archiveNative(), fixture.archiveBase+"/collection-errors.txt")
			if !strings.Contains(errorsText, tt.wantErrorCode) {
				t.Fatalf("collection errors omitted stable code %q: %s", tt.wantErrorCode, errorsText)
			}
			listing := runtimeDiagnosticArchiveList(t, fixture.sh, fixture.archiveNative())
			if tt.absentEntry != "" && strings.Contains(listing, fixture.archiveBase+tt.absentEntry) {
				t.Fatalf("failed diagnostic output entered archive at %q:\n%s", tt.absentEntry, listing)
			}
			if tt.inspectEntry != "" {
				content := runtimeDiagnosticArchiveFile(t, fixture.sh, fixture.archiveNative(), fixture.archiveBase+tt.inspectEntry)
				if strings.Contains(content, tt.forbiddenContent) {
					t.Fatalf("failed diagnostic output entered archive: %s", content)
				}
			}
		})
	}
}

func legacyRemovedRuntimeDiagnosticExportBoundsFileSnapshotByRemainingBytes(t *testing.T) {
	t.Run("oversized source is rejected before copy", func(t *testing.T) {
		fixture := newRuntimeDiagnosticExportShellFixture(t)
		fixture.maxStagedSize = 8192
		logPath := filepath.Join(fixture.installNative, "runtime", "logs", "gateway", "oversized.log")
		if err := os.WriteFile(logPath, []byte(strings.Repeat("O", 16384)), 0o600); err != nil {
			t.Fatal(err)
		}
		realCP := runtimeDiagnosticRealShellCommand(t, fixture.sh, "cp")
		copyMarkerNative := filepath.Join(fixture.rootNative, "copy-invoked")
		copyMarker := runtimeDiagnosticShellPath(copyMarkerNative)
		writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "cp", strings.Join([]string{
			`: > "$AIFAR_COPY_MARKER"`,
			`exec "$AIFAR_REAL_CP" "$@"`,
		}, "\n"))
		output, err := fixture.run("AIFAR_REAL_CP="+realCP, "AIFAR_COPY_MARKER="+copyMarker)
		if err != nil {
			t.Fatalf("oversized source should be skipped without aborting export: %v: %s", err, output)
		}
		if _, err := os.Stat(copyMarkerNative); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("oversized source was copied before conservative admission: %v", err)
		}
		listing := runtimeDiagnosticArchiveList(t, fixture.sh, fixture.archiveNative())
		if strings.Contains(listing, "oversized.log") {
			t.Fatalf("oversized source entered archive:\n%s", listing)
		}
	})

	t.Run("growing source snapshot cannot exceed remaining bytes", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Git for Windows sh cannot apply a numeric ulimit -f; exercised on Linux")
		}
		fixture := newRuntimeDiagnosticExportShellFixture(t)
		fixture.maxStagedSize = 8192
		logPath := filepath.Join(fixture.installNative, "runtime", "logs", "gateway", "growing.log")
		if err := os.WriteFile(logPath, []byte(strings.Repeat("G", 1024)), 0o600); err != nil {
			t.Fatal(err)
		}
		realStat := runtimeDiagnosticRealShellCommand(t, fixture.sh, "stat")
		realCP := runtimeDiagnosticRealShellCommand(t, fixture.sh, "cp")
		growthMarker := runtimeDiagnosticShellPath(filepath.Join(fixture.rootNative, "source-grew"))
		observedSizeNative := filepath.Join(fixture.rootNative, "snapshot-size")
		observedSize := runtimeDiagnosticShellPath(observedSizeNative)
		writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "stat", strings.Join([]string{
			`last=""`,
			`for value do last=$value; done`,
			`result=$("$AIFAR_REAL_STAT" "$@")`,
			`case "$last" in`,
			`  "$AIFAR_GROW_SOURCE")`,
			`    if [ ! -e "$AIFAR_GROWTH_MARKER" ]; then`,
			`      printf '%016384d' 0 > "$last"`,
			`      : > "$AIFAR_GROWTH_MARKER"`,
			`    fi`,
			`    ;;`,
			`esac`,
			`printf '%s\n' "$result"`,
		}, "\n"))
		writeRuntimeDiagnosticShellCommand(t, fixture.binNative, "cp", strings.Join([]string{
			`last=""`,
			`for value do last=$value; done`,
			`set +e`,
			`"$AIFAR_REAL_CP" "$@"`,
			`status=$?`,
			`set -e`,
			`if [ -e "$last" ]; then "$AIFAR_REAL_STAT" -c '%s' "$last" > "$AIFAR_SNAPSHOT_SIZE"; fi`,
			`exit "$status"`,
		}, "\n"))
		output, err := fixture.run(
			"AIFAR_REAL_STAT="+realStat,
			"AIFAR_REAL_CP="+realCP,
			"AIFAR_GROW_SOURCE="+runtimeDiagnosticShellPath(logPath),
			"AIFAR_GROWTH_MARKER="+growthMarker,
			"AIFAR_SNAPSHOT_SIZE="+observedSize,
		)
		if err != nil {
			t.Fatalf("growing source should be bounded and skipped without aborting export: %v: %s", err, output)
		}
		observed, err := os.ReadFile(observedSizeNative)
		if err != nil {
			t.Fatalf("read observed snapshot size: %v", err)
		}
		size, err := strconv.ParseInt(strings.TrimSpace(string(observed)), 10, 64)
		if err != nil {
			t.Fatalf("parse observed snapshot size %q: %v", observed, err)
		}
		if size > fixture.maxStagedSize {
			t.Fatalf("snapshot wrote %d bytes with only %d bytes remaining", size, fixture.maxStagedSize)
		}
		listing := runtimeDiagnosticArchiveList(t, fixture.sh, fixture.archiveNative())
		if strings.Contains(listing, "growing.log") {
			t.Fatalf("growing source entered archive:\n%s", listing)
		}
		result, err := parseRuntimeDiagnosticExportResult(string(output), fixture.exportID)
		if err != nil || result.WarningCount < 1 {
			t.Fatalf("bounded growing source did not produce RESULT with warning: result=%+v err=%v output=%s", result, err, output)
		}
	})
}

type runtimeDiagnosticCleanupShellFixture struct {
	t             *testing.T
	sh            string
	installNative string
	partialNative string
	procNative    string
	killNative    string
	killLogNative string
	exportID      string
	groupAlive    bool
}

func newRuntimeDiagnosticCleanupShellFixture(t *testing.T, pidRecord string) *runtimeDiagnosticCleanupShellFixture {
	t.Helper()
	workingDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp(workingDir, ".runtime-diagnostic-cleanup-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	install := filepath.Join(root, "install")
	exportID := "diag_1234567890abcdef12345678"
	partial := filepath.Join(install, "runtime", "diagnostics", exportID+".partial")
	procRoot := filepath.Join(root, "proc")
	if err := os.MkdirAll(partial, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(procRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, ".collector.pid"), []byte(pidRecord), 0o600); err != nil {
		t.Fatal(err)
	}
	killPath := filepath.Join(root, "kill")
	killLog := filepath.Join(root, "kill.log")
	writeRuntimeDiagnosticShellCommand(t, root, "kill", strings.Join([]string{
		`printf '%s\n' "$*" >> "$AIFAR_KILL_LOG"`,
		`if [ "$1" = "-0" ] && [ "$AIFAR_GROUP_ALIVE" = "1" ]; then exit 0; fi`,
		`exit 1`,
	}, "\n"))
	return &runtimeDiagnosticCleanupShellFixture{
		t: t, sh: runtimeDiagnosticTestShell(t), installNative: install, partialNative: partial,
		procNative: procRoot, killNative: killPath, killLogNative: killLog, exportID: exportID,
	}
}

func (f *runtimeDiagnosticCleanupShellFixture) writeProcStat(pid, starttime, pgid string) {
	f.t.Helper()
	dir := filepath.Join(f.procNative, pid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		f.t.Fatal(err)
	}
	fields := []string{pid, "(sh)", "S", "1", pgid}
	for len(fields) < 22 {
		fields = append(fields, "0")
	}
	fields[21] = starttime
	if err := os.WriteFile(filepath.Join(dir, "stat"), []byte(strings.Join(fields, " ")+"\n"), 0o600); err != nil {
		f.t.Fatal(err)
	}
}

func (f *runtimeDiagnosticCleanupShellFixture) run() ([]byte, error) {
	f.t.Helper()
	script, err := renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{
		InstallRoot: installerkit.ShellQuote(runtimeDiagnosticShellPath(f.installNative)),
		ExportID:    installerkit.ShellQuote(f.exportID),
		ProcRoot:    installerkit.ShellQuote(runtimeDiagnosticShellPath(f.procNative)),
		KillCommand: installerkit.ShellQuote(runtimeDiagnosticShellPath(f.killNative)),
	})
	if err != nil {
		f.t.Fatal(err)
	}
	cmd := exec.Command(f.sh, "-s")
	cmd.Stdin = strings.NewReader(strings.ReplaceAll(script, "\r\n", "\n"))
	alive := "0"
	if f.groupAlive {
		alive = "1"
	}
	cmd.Env = append(os.Environ(),
		"AIFAR_KILL_LOG="+runtimeDiagnosticShellPath(f.killLogNative),
		"AIFAR_GROUP_ALIVE="+alive,
	)
	return cmd.CombinedOutput()
}

func TestRuntimeDiagnosticCleanupRejectsPIDReuse(t *testing.T) {
	fixture := newRuntimeDiagnosticCleanupShellFixture(t, "123\t111\t123\n")
	fixture.writeProcStat("123", "222", "123")
	if output, err := fixture.run(); err == nil {
		t.Fatalf("cleanup accepted reused PID: %s", output)
	}
	if _, err := os.Stat(fixture.partialNative); err != nil {
		t.Fatalf("cleanup removed partial directory for reused PID: %v", err)
	}
	if calls, _ := os.ReadFile(fixture.killLogNative); len(calls) != 0 {
		t.Fatalf("cleanup signalled reused process: %s", calls)
	}
}

func TestRuntimeDiagnosticCleanupKeepsPartialWhenLeaderExitedButGroupLives(t *testing.T) {
	fixture := newRuntimeDiagnosticCleanupShellFixture(t, "123\t111\t123\n")
	fixture.groupAlive = true
	if output, err := fixture.run(); err == nil {
		t.Fatalf("cleanup reported complete while process group lived: %s", output)
	}
	if _, err := os.Stat(fixture.partialNative); err != nil {
		t.Fatalf("cleanup removed partial directory while child group lived: %v", err)
	}
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

func legacyRemovedEstimateRuntimeDiagnosticsRendersTrustedSelectionAndComputesAllowed(t *testing.T) {
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
		"sizes=$(find \"$service_root\" 2>/dev/null",
		"docker ps",
		"docker inspect",
	} {
		if strings.Contains(script, forbidden) {
			t.Fatalf("estimate script must not swallow candidate discovery failure with %q:\n%s", forbidden, script)
		}
	}
	for _, required := range []string{
		"sizes=$(find \"$service_root\"",
		"-printf '%s\\n') || exit 21",
		"MAX_FILE_SCAN=1073741824",
		"MAX_TOTAL_SCAN=2147483648",
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
