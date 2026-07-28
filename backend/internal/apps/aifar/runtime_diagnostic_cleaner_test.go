package aifar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func TestRuntimeDiagnosticCleanerMarksExpiredAndDeletesReachableArchive(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	expired := saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-expired", now.Add(-time.Second))
	future := saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-future", now.Add(time.Hour))

	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)
	task := waitForRuntimeDiagnosticCleanerTask(t, db)

	gotExpired, err := db.GetDiagnosticExport(expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotExpired.Status != "deleted" || gotExpired.CleanupStatus != "complete" || gotExpired.DeletedAt.IsZero() {
		t.Fatalf("expired archive was not completely deleted: %+v", gotExpired)
	}
	gotFuture, err := db.GetDiagnosticExport(future.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFuture.Status != "ready" || gotFuture.CleanupStatus != "none" {
		t.Fatalf("future archive was unexpectedly cleaned: %+v", gotFuture)
	}
	if remote.CallCount() != 1 {
		t.Fatalf("cleanup remote calls=%d, want 1", remote.CallCount())
	}
	if task.Type != runtimeDiagnosticCleanupTaskType || task.Target != runtimeDiagnosticCleanupTarget || task.CreatedBy != runtimeDiagnosticCleanupActor || task.Status != "success" {
		t.Fatalf("unexpected cleanup task: %+v", task)
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil || len(targets) != 1 || targets[0].Target != runtimeDiagnosticCleanupTarget || targets[0].Status != "success" {
		t.Fatalf("unexpected cleanup targets: %+v err=%v", targets, err)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 3 || steps[0].Name != "mark-expired" || steps[1].Name != "delete-local-or-legacy-artifacts" || steps[2].Name != "record-cleanup" {
		t.Fatalf("unexpected cleanup steps: %+v", steps)
	}
	audits, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 1 || audits[0].Actor != runtimeDiagnosticCleanupActor || audits[0].Action != runtimeDiagnosticCleanupAuditAction || audits[0].Status != "success" {
		t.Fatalf("unexpected cleanup audit: %+v", audits)
	}
}

func TestRuntimeDiagnosticCleanerDeletesExpiredLocalWithoutSSH(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	root := t.TempDir()
	archives := NewRuntimeDiagnosticArchiveStorage(root, 5<<30, runtimeDiagnosticRetention, db)
	expired := saveLocalRuntimeDiagnosticCleanerExport(t, db, archives, now, "diag-local-expired", now.Add(-time.Second))
	future := saveLocalRuntimeDiagnosticCleanerExport(t, db, archives, now, "diag-local-future", now.Add(time.Hour))

	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote, archives)
	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)

	gotExpired, err := db.GetDiagnosticExport(expired.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotExpired.Status != "deleted" || gotExpired.CleanupStatus != "complete" {
		t.Fatalf("expired local archive was not deleted: %+v", gotExpired)
	}
	if _, err := archives.Open(expired.StorageRelativePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired local file still exists: %v", err)
	}
	gotFuture, err := db.GetDiagnosticExport(future.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFuture.Status != "ready" || gotFuture.CleanupStatus != "none" {
		t.Fatalf("unexpired local archive was removed: %+v", gotFuture)
	}
	if remote.CallCount() != 0 {
		t.Fatalf("local cleanup used SSH %d time(s)", remote.CallCount())
	}
}

func TestRuntimeDiagnosticCleanerReconcilesStalePartialsMissingFilesAndReservations(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	root := t.TempDir()
	archives := NewRuntimeDiagnosticArchiveStorage(root, 5<<30, runtimeDiagnosticRetention, db)
	archiveName := "aifar-diagnostics-gateway-20260727T070000Z.tar.gz"
	missing, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: "diag-local-missing", TaskID: "task-missing-ready", InstanceID: "instance-1", ServerID: "server-1",
		Status: "ready", StorageKind: "local", StorageRelativePath: path.Join("diag-local-missing", archiveName),
		ArchiveName: archiveName, ArchiveBytes: 100, SHA256: strings.Repeat("a", 64), Services: []string{"gateway"},
		SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), ReadyAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	interrupted, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: "diag-local-interrupted", TaskID: "task-no-longer-exists", InstanceID: "instance-1", ServerID: "server-1",
		Status: "building", StorageKind: "local", ReservedBytes: 256 << 20, Services: []string{"gateway"},
		SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	activeTask, err := db.CreateTask(store.Task{ID: "task-active-local-export", Type: "aifar.runtime.diagnostics.export", Target: "instance-1", Status: "running", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	active, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: "diag-local-active", TaskID: activeTask.ID, InstanceID: "instance-1", ServerID: "server-1",
		Status: "building", StorageKind: "local", ReservedBytes: 256 << 20, Services: []string{"gateway"},
		SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute), CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	partialDir := filepath.Join(root, "diag-stale-partial")
	if err := os.MkdirAll(partialDir, 0o700); err != nil {
		t.Fatal(err)
	}
	partialPath := filepath.Join(partialDir, archiveName+".partial")
	if err := os.WriteFile(partialPath, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(partialPath, now.Add(-2*time.Hour), now.Add(-2*time.Hour)); err != nil {
		t.Fatal(err)
	}

	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote, archives)
	result, err := cleaner.Reconcile(context.Background(), now)
	if err != nil {
		t.Fatal(err)
	}
	if result.RemovedPartials != 1 || !containsString(result.MissingReadyIDs, missing.ID) {
		t.Fatalf("unexpected reconciliation result: %+v", result)
	}
	gotMissing, _ := db.GetDiagnosticExport(missing.ID)
	if gotMissing.Status != "expired" || gotMissing.CleanupStatus != "failed" || gotMissing.CleanupError == "" {
		t.Fatalf("missing ready archive was not made non-downloadable: %+v", gotMissing)
	}
	gotInterrupted, _ := db.GetDiagnosticExport(interrupted.ID)
	if gotInterrupted.Status != "failed" || gotInterrupted.ReservedBytes != 0 {
		t.Fatalf("interrupted reservation was not released: %+v", gotInterrupted)
	}
	gotActive, _ := db.GetDiagnosticExport(active.ID)
	if gotActive.Status != "building" || gotActive.ReservedBytes == 0 {
		t.Fatalf("active reservation was changed: %+v", gotActive)
	}
}

func TestRuntimeDiagnosticCleanerRetriesOfflineServer(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	export := saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-retry", now.Add(-time.Second))
	remote.SetError(errors.New("offline target password=not-for-logs"))
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)

	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)
	failed, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "expired" || failed.CleanupStatus != "failed" || failed.CleanupAttemptedAt.IsZero() {
		t.Fatalf("offline cleanup was not retained for retry: %+v", failed)
	}
	if failed.CleanupError == "" || failed.CleanupError == "offline target password=not-for-logs" {
		t.Fatalf("offline cleanup leaked or omitted a safe error: %q", failed.CleanupError)
	}

	remote.SetError(nil)
	cleaner.tick(context.Background(), now.Add(time.Hour))
	waitForRuntimeDiagnosticCleanerTaskCount(t, db, 2)
	retried, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != "deleted" || retried.CleanupStatus != "complete" || retried.DeletedAt.IsZero() {
		t.Fatalf("offline cleanup did not retry successfully: %+v", retried)
	}
	if remote.CallCount() != 2 {
		t.Fatalf("cleanup remote calls=%d, want 2", remote.CallCount())
	}
}

func TestRuntimeDiagnosticCleanerReclaimsTerminalAndMissingTaskOrphans(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	terminalTask, err := db.CreateTask(store.Task{ID: "task-terminal-export", Type: "aifar.runtime.diagnostics.export", Target: "instance-1", Status: "failed", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	terminalOrphan := saveRuntimeDiagnosticCleanerExportWithState(t, db, now, "diag-terminal-orphan", "pending", terminalTask.ID, "none")
	missingOrphan := saveRuntimeDiagnosticCleanerExportWithState(t, db, now, "diag-missing-orphan", "building", "task-missing-export", "none")

	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)

	for _, exportID := range []string{terminalOrphan.ID, missingOrphan.ID} {
		got, err := db.GetDiagnosticExport(exportID)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "deleted" || got.CleanupStatus != "complete" || got.DeletedAt.IsZero() {
			t.Fatalf("orphan export %s was not reclaimed: %+v", exportID, got)
		}
	}
	if remote.CallCount() != 2 {
		t.Fatalf("cleanup remote calls=%d, want 2", remote.CallCount())
	}
	audits, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) != 2 {
		t.Fatalf("cleanup attempts must each be audited, got %+v", audits)
	}
}

func TestRuntimeDiagnosticCleanerDoesNotRaceActiveExportTask(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	activeTask, err := db.CreateTask(store.Task{ID: "task-active-export", Type: "aifar.runtime.diagnostics.export", Target: "instance-1", Status: "running", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	export := saveRuntimeDiagnosticCleanerExportWithState(t, db, now, "diag-active-export", "building", activeTask.ID, "none")

	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)

	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "building" || got.CleanupStatus != "none" || remote.CallCount() != 0 {
		t.Fatalf("active export was raced by cleanup: %+v calls=%d", got, remote.CallCount())
	}
	allTasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(allTasks) != 1 || allTasks[0].ID != activeTask.ID {
		t.Fatalf("cleaner created a task for an active export: %+v", allTasks)
	}
}

func TestRuntimeDiagnosticCleanerRetriesFailedExportCleanup(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	export := saveRuntimeDiagnosticCleanerExportWithState(t, db, now, "diag-failed-retry", "failed", "task-missing-export", "failed")
	remote.SetError(errors.New("offline target"))
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)

	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)
	failed, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.CleanupStatus != "failed" || failed.CleanupAttemptedAt.IsZero() {
		t.Fatalf("failed export cleanup was not retained for retry: %+v", failed)
	}

	remote.SetError(nil)
	cleaner.tick(context.Background(), now.Add(time.Hour))
	waitForRuntimeDiagnosticCleanerTaskCount(t, db, 2)
	retried, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != "deleted" || retried.CleanupStatus != "complete" {
		t.Fatalf("failed export cleanup did not retry: %+v", retried)
	}
}

func TestRuntimeDiagnosticCleanerCoalescesOverlappingTicks(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	_ = saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-overlap", now.Add(-time.Second))
	remote.Block()
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)

	cleaner.tick(context.Background(), now)
	remote.WaitStarted(t)
	cleaner.tick(context.Background(), now.Add(time.Second))
	tasksAfterOverlap, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksAfterOverlap) != 1 {
		t.Fatalf("overlapping cleanup ticks created %d tasks, want 1", len(tasksAfterOverlap))
	}
	remote.Release()
	waitForRuntimeDiagnosticCleanerTask(t, db)
}

func TestRuntimeDiagnosticCleanerReleasesRunningAfterQueuedTaskCancellation(t *testing.T) {
	db, _, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	tasks := worker.NewManagerWithConcurrency(db, 1)
	holderStarted := make(chan struct{})
	holderRelease := make(chan struct{})
	if _, err := tasks.Start("holder", "holder", "system", func(context.Context, worker.Logger) error {
		close(holderStarted)
		<-holderRelease
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	<-holderStarted
	_ = saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-queued", now.Add(-time.Second))
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)
	cleanupTask := findRuntimeDiagnosticCleanerTask(t, db)
	if cleanupTask.Status != "pending" {
		t.Fatalf("cleanup task should be queued, got %+v", cleanupTask)
	}
	if !tasks.Cancel(cleanupTask.ID) {
		t.Fatal("expected queued cleanup task cancellation")
	}
	waitForRuntimeDiagnosticCleanerTaskStatus(t, db, cleanupTask.ID, "cancelled")
	waitForRuntimeDiagnosticCleanerIdle(t, cleaner)
	close(holderRelease)
}

func TestRuntimeDiagnosticCleanerReleasesRunningWhenQueuedTaskIsDeleted(t *testing.T) {
	db, tasks, remote, _ := newRuntimeDiagnosticCleanerFixture(t)
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	task, err := db.CreateTask(store.Task{Type: runtimeDiagnosticCleanupTaskType, Target: runtimeDiagnosticCleanupTarget, Status: "cancelled", CreatedBy: runtimeDiagnosticCleanupActor})
	if err != nil {
		t.Fatal(err)
	}
	cleaner.running.Store(true)
	go cleaner.releaseWhenTaskFinishes(task.ID)
	if err := db.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	waitForRuntimeDiagnosticCleanerIdle(t, cleaner)
}

func TestRuntimeDiagnosticCleanerSkipsDownloadDeleteLockConflict(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	export := saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-locked", now.Add(-time.Second))
	if _, err := db.AcquireOperationLock(store.OperationLock{Scope: "runtime-diagnostics", ResourceID: export.ID, Operation: "delete", Owner: "download", ExpiresAt: now.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.CleanupStatus != "none" || remote.CallCount() != 0 {
		t.Fatalf("lock conflict must skip archive safely: %+v calls=%d", got, remote.CallCount())
	}
}

func TestRuntimeDiagnosticCleanerProcessesAllDuePages(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	for i := 0; i < 205; i++ {
		saveRuntimeDiagnosticCleanerExport(t, db, now, fmt.Sprintf("diag-page-%03d", i), now.Add(-time.Second))
	}
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)
	if remote.CallCount() != 205 {
		t.Fatalf("cleanup remote calls=%d, want 205", remote.CallCount())
	}
}

func TestRuntimeDiagnosticCleanerProcessesMixedOrphansAcrossPages(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	for i := 0; i < 205; i++ {
		status := "failed"
		cleanupStatus := "failed"
		if i%2 == 0 {
			status = "building"
			cleanupStatus = "none"
		}
		saveRuntimeDiagnosticCleanerExportWithState(t, db, now, fmt.Sprintf("diag-orphan-page-%03d", i), status, "task-missing", cleanupStatus)
	}
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)
	if remote.CallCount() != 205 {
		t.Fatalf("mixed orphan cleanup remote calls=%d, want 205", remote.CallCount())
	}
}

func TestRuntimeDiagnosticCleanerAuditFailureLeavesArchiveRetryable(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	export := saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-audit", now.Add(-time.Second))
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.addAudit = func(string, string, string, string, string) error { return errors.New("audit unavailable") }
	cleaner.tick(context.Background(), now)
	waitForRuntimeDiagnosticCleanerTask(t, db)
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "expired" || got.CleanupStatus != "failed" {
		t.Fatalf("audit failure must retain retryable archive: %+v", got)
	}
}

func TestRuntimeDiagnosticCleanerStartFailureLeavesNoPendingTask(t *testing.T) {
	db, tasks, remote, now := newRuntimeDiagnosticCleanerFixture(t)
	_ = saveRuntimeDiagnosticCleanerExport(t, db, now, "diag-start-failure", now.Add(-time.Second))
	cleaner := NewRuntimeDiagnosticCleaner(db, tasks, remote)
	cleaner.startExisting = func(store.Task, worker.Job) (store.Task, error) { return store.Task{}, errors.New("start rejected") }
	cleaner.tick(context.Background(), now)
	tasksAfter, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasksAfter) != 0 {
		t.Fatalf("start failure retained task(s): %+v", tasksAfter)
	}
}

type runtimeDiagnosticCleanerRemote struct {
	mu      sync.Mutex
	calls   int
	err     error
	started chan struct{}
	release chan struct{}
}

func (r *runtimeDiagnosticCleanerRemote) Run(ctx context.Context, _ store.Server, _ string) (adapter.CommandResult, error) {
	r.mu.Lock()
	r.calls++
	started, release, err := r.started, r.release, r.err
	r.mu.Unlock()
	if started != nil {
		select {
		case <-started:
		default:
			close(started)
		}
	}
	if release != nil {
		select {
		case <-ctx.Done():
			return adapter.CommandResult{}, ctx.Err()
		case <-release:
		}
	}
	return adapter.CommandResult{}, err
}

func (*runtimeDiagnosticCleanerRemote) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

func (r *runtimeDiagnosticCleanerRemote) SetError(err error) {
	r.mu.Lock()
	r.err = err
	r.mu.Unlock()
}

func (r *runtimeDiagnosticCleanerRemote) CallCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func (r *runtimeDiagnosticCleanerRemote) Block() {
	r.mu.Lock()
	r.started = make(chan struct{})
	r.release = make(chan struct{})
	r.mu.Unlock()
}

func (r *runtimeDiagnosticCleanerRemote) WaitStarted(t *testing.T) {
	t.Helper()
	r.mu.Lock()
	started := r.started
	r.mu.Unlock()
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for cleanup remote call")
	}
}

func (r *runtimeDiagnosticCleanerRemote) Release() {
	r.mu.Lock()
	release := r.release
	r.release = nil
	r.mu.Unlock()
	close(release)
}

func newRuntimeDiagnosticCleanerFixture(t *testing.T) (*store.Store, *worker.Manager, *runtimeDiagnosticCleanerRemote, time.Time) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.SaveServer(store.Server{ID: "server-1", Name: "server", Host: "192.0.2.10", Username: "root", DeployDir: "/aifar/apps"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{ID: "instance-1", App: AppName, Version: "runtime-v2", ServerID: "server-1", Status: "running", Topology: "standalone", Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`}); err != nil {
		t.Fatal(err)
	}
	return db, worker.NewManager(db), &runtimeDiagnosticCleanerRemote{}, time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
}

func saveRuntimeDiagnosticCleanerExport(t *testing.T, db *store.Store, now time.Time, id string, expiresAt time.Time) store.DiagnosticExport {
	t.Helper()
	archiveName := "aifar-diagnostics-gateway-20260727T070000Z.tar.gz"
	export, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: id, TaskID: "task-" + id, InstanceID: "instance-1", ServerID: "server-1", Status: "ready",
		Services: []string{"gateway"}, SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute),
		RemoteRelativePath: id + "/" + archiveName, ArchiveName: archiveName, ArchiveBytes: 1,
		SHA256:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CreatedBy: "owner", CreatedAt: now.Add(-time.Hour), ReadyAt: now.Add(-time.Hour), ExpiresAt: expiresAt, CleanupStatus: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	return export
}

func saveLocalRuntimeDiagnosticCleanerExport(t *testing.T, db *store.Store, archives RuntimeDiagnosticArchiveStorage, now time.Time, id string, expiresAt time.Time) store.DiagnosticExport {
	t.Helper()
	archiveName := "aifar-diagnostics-gateway-20260727T070000Z.tar.gz"
	sink, err := archives.Begin(id, archiveName)
	if err != nil {
		t.Fatal(err)
	}
	archive := runtimeDiagnosticTestArchive(t, strings.TrimSuffix(archiveName, ".tar.gz"))
	if _, err := sink.Write(archive); err != nil {
		t.Fatal(err)
	}
	artifact, err := sink.Commit(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	export, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: id, TaskID: "task-" + id, InstanceID: "instance-1", ServerID: "server-1", Status: "ready", StorageKind: "local",
		Services: []string{"gateway"}, SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute),
		StorageRelativePath: artifact.RelativePath, ArchiveName: artifact.ArchiveName, ArchiveBytes: artifact.Size, SHA256: artifact.SHA256,
		CreatedBy: "owner", CreatedAt: now.Add(-time.Hour), ReadyAt: now.Add(-time.Hour), ExpiresAt: expiresAt, CleanupStatus: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	return export
}

func saveRuntimeDiagnosticCleanerExportWithState(t *testing.T, db *store.Store, now time.Time, id, status, taskID, cleanupStatus string) store.DiagnosticExport {
	t.Helper()
	archiveName := "aifar-diagnostics-gateway-20260727T070000Z.tar.gz"
	export, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: id, TaskID: taskID, InstanceID: "instance-1", ServerID: "server-1", Status: status,
		Services: []string{"gateway"}, SinceAt: now.Add(-time.Hour), UntilAt: now.Add(-time.Minute),
		RemoteRelativePath: id + "/" + archiveName, ArchiveName: archiveName, ArchiveBytes: 1,
		SHA256:    "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		CreatedBy: "owner", CreatedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour), CleanupStatus: cleanupStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	return export
}

func waitForRuntimeDiagnosticCleanerTask(t *testing.T, db *store.Store) store.Task {
	t.Helper()
	return waitForRuntimeDiagnosticCleanerTaskCount(t, db, 1)
}

func waitForRuntimeDiagnosticCleanerTaskCount(t *testing.T, db *store.Store, count int) store.Task {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := db.ListTasks()
		if err != nil {
			t.Fatal(err)
		}
		if len(tasks) >= count {
			task := tasks[0]
			if task.Status == "success" || task.Status == "failed" || task.Status == "cancelled" {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for cleanup task")
	return store.Task{}
}

func findRuntimeDiagnosticCleanerTask(t *testing.T, db *store.Store) store.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := db.ListTasks()
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if task.Type == runtimeDiagnosticCleanupTaskType {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for cleanup task creation")
	return store.Task{}
}

func waitForRuntimeDiagnosticCleanerTaskStatus(t *testing.T, db *store.Store, taskID, want string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		task, _, err := db.GetTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == want {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for task %s status %s", taskID, want)
}

func waitForRuntimeDiagnosticCleanerIdle(t *testing.T, cleaner *RuntimeDiagnosticCleaner) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !cleaner.running.Load() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for cleaner to become idle")
}
