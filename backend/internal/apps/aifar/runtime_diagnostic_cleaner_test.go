package aifar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
	if len(steps) != 3 || steps[0].Name != "mark-expired" || steps[1].Name != "delete-remote-artifacts" || steps[2].Name != "record-cleanup" {
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
	case <-time.After(2 * time.Second):
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

func waitForRuntimeDiagnosticCleanerTask(t *testing.T, db *store.Store) store.Task {
	t.Helper()
	return waitForRuntimeDiagnosticCleanerTaskCount(t, db, 1)
}

func waitForRuntimeDiagnosticCleanerTaskCount(t *testing.T, db *store.Store, count int) store.Task {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
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
