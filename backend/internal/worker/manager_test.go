package worker

import (
	"context"
	"errors"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

type recordingPublisher struct {
	mu     sync.Mutex
	events []realtime.Event
}

func (p *recordingPublisher) Publish(event realtime.Event) {
	p.mu.Lock()
	p.events = append(p.events, event)
	p.mu.Unlock()
}

func (p *recordingPublisher) taskEvents(taskID string) []realtime.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	events := make([]realtime.Event, 0, len(p.events))
	for _, event := range p.events {
		if event.TaskID == taskID {
			events = append(events, event)
		}
	}
	return events
}

func TestManagerUsesDeploymentConcurrencySetting(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSetting("deploymentConcurrency", "1"); err != nil {
		t.Fatal(err)
	}

	manager := NewManagerWithConcurrency(db, 2)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})

	firstTask, err := manager.Start("test.first", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(firstStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-firstRelease:
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not start")
	}

	secondTask, err := manager.Start("test.second", "srv-2", "tester", func(ctx context.Context, log Logger) error {
		close(secondStarted)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
		t.Fatal("second task started before concurrency setting allowed it")
	case <-time.After(250 * time.Millisecond):
	}

	if err := db.SetSetting("deploymentConcurrency", "2"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second task did not start after concurrency setting increased")
	}
	close(firstRelease)
	waitForTaskStatus(t, db, firstTask.ID, "success")
	waitForTaskStatus(t, db, secondTask.ID, "success")
}

func TestManagerRecoversJobPanicAndContinues(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	panickingTask, err := db.CreateTask(store.Task{Type: "test.panic", Target: "srv-1", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.AcquireOperationLock(store.OperationLock{
		Scope:       "app-instance",
		ResourceID:  "app-1",
		Operation:   "update",
		OwnerTaskID: panickingTask.ID,
		Owner:       "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartExistingWithLanguage(panickingTask, "en", func(ctx context.Context, log Logger) error {
		panic("boom")
	}); err != nil {
		t.Fatal(err)
	}

	failed := waitForTaskStatus(t, db, panickingTask.ID, "failed")
	if failed.Error != "task panicked" {
		t.Fatalf("expected generic panic error to be persisted, got %q", failed.Error)
	}
	waitForOperationLockStatus(t, db, lock.ID, "released")

	nextTask, err := manager.Start("test.after-panic", "srv-2", "tester", func(ctx context.Context, log Logger) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, db, nextTask.ID, "success")
}

func TestManagerPersistsNormalJobErrorAndReleasesResources(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	task, err := db.CreateTask(store.Task{Type: "test.error", Target: "srv-1", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.AcquireOperationLock(store.OperationLock{
		Scope:       "app-instance",
		ResourceID:  "app-error",
		Operation:   "update",
		OwnerTaskID: task.ID,
		Owner:       "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	jobErr := errors.New("expected job failure")
	if _, err := manager.StartExistingWithLanguage(task, "en", func(ctx context.Context, log Logger) error {
		return jobErr
	}); err != nil {
		t.Fatal(err)
	}

	failed := waitForTaskStatus(t, db, task.ID, "failed")
	waitForManagerTaskCleanup(t, manager, task.ID)
	if failed.Error != jobErr.Error() {
		t.Fatalf("expected persisted error %q, got %q", jobErr.Error(), failed.Error)
	}
	waitForOperationLockStatus(t, db, lock.ID, "released")
	assertManagerActiveCount(t, manager, 0)
	assertTerminalEventPair(t, events, task.ID, "failed")

	nextTask, err := manager.Start("test.after-error", "srv-2", "tester", func(ctx context.Context, log Logger) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, db, nextTask.ID, "success")
	waitForManagerTaskCleanup(t, manager, nextTask.ID)
}

func TestManagerRejectsConcurrentStartForSameTask(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	task, err := db.CreateTask(store.Task{Type: "test.duplicate", Target: "srv-1", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	release := make(chan struct{})
	if _, err := manager.StartExistingWithLanguage(task, "en", func(ctx context.Context, log Logger) error {
		close(started)
		<-release
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("first task execution did not start")
	}

	duplicateStarted := make(chan struct{})
	_, duplicateErr := manager.StartExistingWithLanguage(task, "en", func(ctx context.Context, log Logger) error {
		close(duplicateStarted)
		return nil
	})
	close(release)
	if duplicateErr == nil {
		select {
		case <-duplicateStarted:
		case <-time.After(2 * time.Second):
			t.Fatal("duplicate task execution did not drain during test cleanup")
		}
		waitForManagerIdle(t, manager)
		t.Fatal("expected concurrent start for the same task to be rejected")
	}
	if duplicateErr.Error() != "task has already started" {
		t.Fatalf("expected localized duplicate-start error, got %q", duplicateErr.Error())
	}

	waitForTaskStatus(t, db, task.ID, "success")
	waitForManagerTaskCleanup(t, manager, task.ID)
	assertTerminalEventPair(t, events, task.ID, "success")
	select {
	case <-duplicateStarted:
		t.Fatal("rejected duplicate task execution ran")
	default:
	}
}

func TestManagerRejectsRestartForCompletedTask(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	task, err := db.CreateTask(store.Task{Type: "test.completed-restart", Target: "srv-1", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.StartExistingWithLanguage(task, "en", func(ctx context.Context, log Logger) error {
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, db, task.ID, "success")
	waitForManagerTaskCleanup(t, manager, task.ID)

	restarted := make(chan struct{})
	_, restartErr := manager.StartExistingWithLanguage(task, "en", func(ctx context.Context, log Logger) error {
		close(restarted)
		return nil
	})
	if restartErr == nil {
		select {
		case <-restarted:
		case <-time.After(2 * time.Second):
			t.Fatal("restarted task execution did not drain during test cleanup")
		}
		waitForManagerTaskCleanup(t, manager, task.ID)
		t.Fatal("expected completed task restart to be rejected")
	}
	if restartErr.Error() != "task has already started" {
		t.Fatalf("expected localized completed-task restart error, got %q", restartErr.Error())
	}

	persisted, _, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != "success" {
		t.Fatalf("expected completed task to remain successful, got %q", persisted.Status)
	}
	assertTerminalEventPair(t, events, task.ID, "success")
	select {
	case <-restarted:
		t.Fatal("rejected completed task execution ran")
	default:
	}
}

func TestManagerDoesNotPublishTerminalEventsWhenPersistenceFails(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	manager := NewManager(db)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	started := make(chan struct{})
	release := make(chan struct{})
	task, err := manager.Start("test.persistence-failure", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(started)
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForManagerTaskCleanup(t, manager, task.ID)

	terminalEvents := make([]realtime.Event, 0, 2)
	for _, event := range events.taskEvents(task.ID) {
		if isTerminalTaskStatus(event.Status) {
			terminalEvents = append(terminalEvents, event)
		}
	}
	if len(terminalEvents) != 0 {
		t.Fatalf("expected no terminal event when terminal persistence failed, got %#v", terminalEvents)
	}
}

func TestManagerCancelsTaskWhileWaitingForSlot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	var releaseFirst sync.Once
	release := func() { releaseFirst.Do(func() { close(firstRelease) }) }
	t.Cleanup(release)
	firstTask, err := manager.Start("test.slot-holder", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(firstStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-firstRelease:
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slot holder did not start")
	}

	queuedTask, err := db.CreateTask(store.Task{Type: "test.slot-waiter", Target: "srv-2", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := db.AcquireOperationLock(store.OperationLock{
		Scope:       "app-instance",
		ResourceID:  "app-queued",
		Operation:   "update",
		OwnerTaskID: queuedTask.ID,
		Owner:       "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
	queuedStarted := make(chan struct{})
	if _, err := manager.StartExistingWithLanguage(queuedTask, "en", func(ctx context.Context, log Logger) error {
		close(queuedStarted)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if !manager.Cancel(queuedTask.ID) {
		t.Fatal("expected waiting task cancellation to be accepted")
	}

	cancelled := waitForTaskStatus(t, db, queuedTask.ID, "cancelled")
	waitForManagerTaskCleanup(t, manager, queuedTask.ID)
	if cancelled.Error != context.Canceled.Error() {
		t.Fatalf("expected cancellation error %q, got %q", context.Canceled.Error(), cancelled.Error)
	}
	select {
	case <-queuedStarted:
		t.Fatal("cancelled task started after waiting for a slot")
	default:
	}
	waitForOperationLockStatus(t, db, lock.ID, "released")
	assertTerminalEventPair(t, events, queuedTask.ID, "cancelled")

	release()
	waitForTaskStatus(t, db, firstTask.ID, "success")
	waitForManagerTaskCleanup(t, manager, firstTask.ID)
	assertManagerActiveCount(t, manager, 0)
}

func TestManagerStartsLifecycleBeforeSlotAndFinishesItOnQueuedCancellation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManagerWithConcurrency(db, 1)
	holderStarted := make(chan struct{})
	holderRelease := make(chan struct{})
	var releaseHolder sync.Once
	t.Cleanup(func() { releaseHolder.Do(func() { close(holderRelease) }) })
	if _, err := manager.Start("test.lifecycle-holder", "holder", "tester", func(ctx context.Context, log Logger) error {
		close(holderStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-holderRelease:
			return nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-holderStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slot holder did not start")
	}
	queuedTask, err := db.CreateTask(store.Task{Type: "test.lifecycle-queued", Target: "queued", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	lifecycleStarted := make(chan struct{})
	lifecycleFinished := make(chan struct{})
	jobStarted := make(chan struct{})
	if _, err := manager.StartExistingWithLanguageAndLifecycle(queuedTask, "en", func(ctx context.Context, log Logger) error {
		close(jobStarted)
		return nil
	}, TaskLifecycle{
		Start: func(context.Context, context.CancelFunc) { close(lifecycleStarted) },
		Finish: func() error {
			close(lifecycleFinished)
			return nil
		},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-lifecycleStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("task lifecycle did not start while queued")
	}
	if !manager.Cancel(queuedTask.ID) {
		t.Fatal("expected queued task cancellation")
	}
	waitForTaskStatus(t, db, queuedTask.ID, "cancelled")
	select {
	case <-lifecycleFinished:
	case <-time.After(2 * time.Second):
		t.Fatal("queued cancellation did not finish task lifecycle")
	}
	select {
	case <-jobStarted:
		t.Fatal("cancelled queued task entered job body")
	default:
	}
	releaseHolder.Do(func() { close(holderRelease) })
}

func TestManagerCancellationSignalAndQueuedSlotAdmissionAreAtomic(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	holderStarted := make(chan struct{})
	holderRelease := make(chan struct{})
	var releaseHolderOnce sync.Once
	releaseHolder := func() { releaseHolderOnce.Do(func() { close(holderRelease) }) }
	t.Cleanup(releaseHolder)
	holderTask, err := manager.Start("test.atomic-slot-holder", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(holderStarted)
		<-holderRelease
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-holderStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slot holder did not start")
	}

	queuedTask, err := db.CreateTask(store.Task{Type: "test.atomic-slot-waiter", Target: "srv-2", Status: "pending", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	queuedStarted := make(chan struct{})
	if _, err := manager.StartExistingWithLanguage(queuedTask, "en", func(ctx context.Context, log Logger) error {
		close(queuedStarted)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	manager.mu.Lock()
	originalCancel := manager.cancels[queuedTask.ID]
	signalHeldManagerMu := false
	manager.cancels[queuedTask.ID] = func() {
		signalHeldManagerMu = true
		deadline := time.Now().Add(50 * time.Millisecond)
		for time.Now().Before(deadline) {
			if manager.mu.TryLock() {
				manager.mu.Unlock()
				signalHeldManagerMu = false
				break
			}
			runtime.Gosched()
		}
		originalCancel()
	}
	manager.mu.Unlock()
	if originalCancel == nil {
		releaseHolder()
		t.Fatal("queued task did not register a cancellation function")
	}

	if !manager.Cancel(queuedTask.ID) {
		releaseHolder()
		t.Fatal("expected queued task cancellation to be accepted")
	}
	releaseHolder()

	cancelled := waitForTaskStatus(t, db, queuedTask.ID, "cancelled")
	waitForManagerTaskCleanup(t, manager, queuedTask.ID)
	waitForTaskStatus(t, db, holderTask.ID, "success")
	waitForManagerTaskCleanup(t, manager, holderTask.ID)
	if !signalHeldManagerMu {
		t.Error("expected cancellation claim and context signal to share Manager.mu")
	}
	select {
	case <-queuedStarted:
		t.Error("queued task ran after cancellation was accepted")
	default:
	}
	if cancelled.Error != context.Canceled.Error() {
		t.Errorf("expected cancellation error %q, got %q", context.Canceled.Error(), cancelled.Error)
	}
	assertTerminalEventPair(t, events, queuedTask.ID, "cancelled")
	assertManagerActiveCount(t, manager, 0)

	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if manager.acquireSlot(cancelledCtx) {
		manager.releaseSlot()
		t.Error("cancelled context acquired a free worker slot")
	}
}

func TestManagerPanicWinsAfterAcceptedCancellation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	started := make(chan struct{})
	task, err := manager.StartWithLanguage("test.cancel-panic", "srv-1", "tester", "en", func(ctx context.Context, log Logger) error {
		close(started)
		<-ctx.Done()
		panic("boom")
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}
	if !manager.Cancel(task.ID) {
		t.Fatal("expected running task cancellation to be accepted")
	}

	waitForTaskStatus(t, db, task.ID, "failed")
	waitForManagerTaskCleanup(t, manager, task.ID)
	assertManagerActiveCount(t, manager, 0)
	assertTerminalEventPair(t, events, task.ID, "failed")
	if manager.Cancel(task.ID) {
		t.Fatal("expected terminal task to reject cancellation")
	}
}

func TestManagerMasksRecoveredPanicValue(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	const sensitiveValue = "sensitive-panic-payload-do-not-expose"
	task, err := manager.StartWithLanguage("test.panic-mask", "srv-1", "tester", "en", func(ctx context.Context, log Logger) error {
		panic(sensitiveValue)
	})
	if err != nil {
		t.Fatal(err)
	}

	failed := waitForTaskStatus(t, db, task.ID, "failed")
	waitForManagerTaskCleanup(t, manager, task.ID)
	if failed.Error != "task panicked" {
		t.Fatalf("expected generic panic error without recovered value, got %q", failed.Error)
	}
	if strings.Contains(failed.Error, sensitiveValue) {
		t.Fatalf("persisted task error exposed recovered panic value %q", sensitiveValue)
	}
	_, logs, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range logs {
		if strings.Contains(entry.Message, sensitiveValue) {
			t.Fatalf("task log exposed recovered panic value %q: %q", sensitiveValue, entry.Message)
		}
	}
}

func TestManagerMarksCancelledWhenJobReturnsNilAfterCancellation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	started := make(chan struct{})
	task, err := manager.Start("test.cancel", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(started)
		<-ctx.Done()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}
	if !manager.Cancel(task.ID) {
		t.Fatal("expected running task cancellation to be accepted")
	}

	cancelled := waitForTaskStatus(t, db, task.ID, "cancelled")
	if cancelled.Error != context.Canceled.Error() {
		t.Fatalf("expected cancellation error %q, got %q", context.Canceled.Error(), cancelled.Error)
	}
}

func TestManagerRepeatedCancelRemainsAcceptedWhileCancellationUnwinds(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	events := &recordingPublisher{}
	manager.SetEventPublisher(events)
	started := make(chan struct{})
	cancelObserved := make(chan struct{})
	finish := make(chan struct{})
	var finishOnce sync.Once
	release := func() { finishOnce.Do(func() { close(finish) }) }
	t.Cleanup(release)
	task, err := manager.Start("test.repeated-cancel", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(started)
		<-ctx.Done()
		close(cancelObserved)
		<-finish
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}
	if !manager.Cancel(task.ID) {
		t.Fatal("expected first cancellation request to be accepted")
	}
	select {
	case <-cancelObserved:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not observe cancellation")
	}

	repeatedAccepted := manager.Cancel(task.ID)
	release()
	if !repeatedAccepted {
		waitForTaskStatus(t, db, task.ID, "cancelled")
		waitForManagerTaskCleanup(t, manager, task.ID)
		t.Fatal("expected repeated cancellation request to remain accepted while cancellation unwinds")
	}

	cancelled := waitForTaskStatus(t, db, task.ID, "cancelled")
	waitForManagerTaskCleanup(t, manager, task.ID)
	if cancelled.Error != context.Canceled.Error() {
		t.Fatalf("expected cancellation error %q, got %q", context.Canceled.Error(), cancelled.Error)
	}
	assertTerminalEventPair(t, events, task.ID, "cancelled")
	if manager.Cancel(task.ID) {
		t.Fatal("expected cancellation request after cleanup to be rejected")
	}
}

func TestManagerCompletesSuccessfulJob(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	task, err := manager.Start("test.success", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	succeeded := waitForTaskStatus(t, db, task.ID, "success")
	if succeeded.Error != "" {
		t.Fatalf("expected successful task without an error, got %q", succeeded.Error)
	}
	if succeeded.StartedAt.IsZero() || succeeded.FinishedAt.IsZero() {
		t.Fatalf("expected successful task timestamps to be populated: %#v", succeeded)
	}
	if manager.Cancel(task.ID) {
		t.Fatal("expected completed task to reject cancellation")
	}
}

func TestManagerRejectsCancellationAfterCommitStarts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	committed := make(chan struct{})
	finish := make(chan struct{})
	task, err := manager.Start("test.commit-boundary", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		if !log.TryEnterCommit() {
			return errors.New("commit boundary was rejected")
		}
		close(committed)
		<-finish
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-committed:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not enter commit")
	}
	if manager.Cancel(task.ID) {
		t.Fatal("cancellation must be rejected after commit starts")
	}
	close(finish)
	waitForTaskStatus(t, db, task.ID, "success")
	waitForManagerTaskCleanup(t, manager, task.ID)
}

func TestManagerCancellationWinsBeforeCommitStarts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManager(db)
	started := make(chan struct{})
	tryCommit := make(chan struct{})
	commitAccepted := make(chan bool, 1)
	task, err := manager.Start("test.cancel-before-commit", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(started)
		<-tryCommit
		commitAccepted <- log.TryEnterCommit()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("task did not start")
	}
	if !manager.Cancel(task.ID) {
		t.Fatal("cancellation before commit must be accepted")
	}
	close(tryCommit)
	if <-commitAccepted {
		t.Fatal("commit must be rejected after cancellation wins")
	}
	waitForTaskStatus(t, db, task.ID, "cancelled")
	waitForManagerTaskCleanup(t, manager, task.ID)
}

func TestManagerCommitAndCancellationRaceHasOneWinner(t *testing.T) {
	for iteration := 0; iteration < 25; iteration++ {
		db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
		if err != nil {
			t.Fatal(err)
		}
		manager := NewManager(db)
		started := make(chan struct{})
		startRace := make(chan struct{})
		finish := make(chan struct{})
		commitAccepted := make(chan bool, 1)
		task, err := manager.Start("test.commit-cancel-race", "srv-1", "tester", func(ctx context.Context, log Logger) error {
			close(started)
			<-startRace
			commitAccepted <- log.TryEnterCommit()
			<-finish
			return nil
		})
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			db.Close()
			t.Fatalf("iteration %d: task did not start", iteration)
		}
		cancelAccepted := make(chan bool, 1)
		go func() {
			<-startRace
			cancelAccepted <- manager.Cancel(task.ID)
		}()
		close(startRace)
		commitWon := <-commitAccepted
		cancelWon := <-cancelAccepted
		if commitWon == cancelWon {
			close(finish)
			db.Close()
			t.Fatalf("iteration %d: expected exactly one winner, commit=%v cancel=%v", iteration, commitWon, cancelWon)
		}
		close(finish)
		want := "cancelled"
		if commitWon {
			want = "success"
		}
		waitForTaskStatus(t, db, task.ID, want)
		waitForManagerTaskCleanup(t, manager, task.ID)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestManagerCancelAndSuccessClaimAreAtomic(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	manager := NewManagerWithConcurrency(db, 1)
	for iteration := 0; iteration < 50; iteration++ {
		started := make(chan struct{})
		finish := make(chan struct{})
		task, err := manager.Start("test.cancel-success-race", "srv-1", "tester", func(ctx context.Context, log Logger) error {
			close(started)
			<-finish
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			t.Fatalf("iteration %d: task did not start", iteration)
		}

		cancelled := make(chan bool, 1)
		startRace := make(chan struct{})
		go func() {
			<-startRace
			cancelled <- manager.Cancel(task.ID)
		}()
		close(startRace)
		close(finish)
		cancelAccepted := <-cancelled
		want := "success"
		if cancelAccepted {
			want = "cancelled"
		}
		finished := waitForTaskStatus(t, db, task.ID, want)
		if cancelAccepted && finished.Status == "success" {
			t.Fatalf("iteration %d: accepted cancellation ended in success", iteration)
		}
		if !cancelAccepted && finished.Status != "success" {
			t.Fatalf("iteration %d: rejected cancellation ended in %q", iteration, finished.Status)
		}
	}
}

func waitForTaskStatus(t *testing.T, db *store.Store, taskID, want string) store.Task {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		task, _, err := db.GetTask(taskID)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status == want {
			return task
		}
		time.Sleep(10 * time.Millisecond)
	}
	task, _, err := db.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("task %s did not reach status %q; current status is %q", taskID, want, task.Status)
	return store.Task{}
}

func waitForOperationLockStatus(t *testing.T, db *store.Store, lockID, want string) store.OperationLock {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		lock, err := db.GetOperationLock(lockID)
		if err != nil {
			t.Fatal(err)
		}
		if lock.Status == want {
			return lock
		}
		time.Sleep(10 * time.Millisecond)
	}
	lock, err := db.GetOperationLock(lockID)
	if err != nil {
		t.Fatal(err)
	}
	t.Fatalf("operation lock %s did not reach status %q; current status is %q", lockID, want, lock.Status)
	return store.OperationLock{}
}

func waitForManagerTaskCleanup(t *testing.T, manager *Manager, taskID string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		_, hasCancel := manager.cancels[taskID]
		_, hasCancelRequest := manager.cancelRequested[taskID]
		_, hasCommit := manager.committing[taskID]
		_, hasLanguage := manager.languages[taskID]
		manager.mu.Unlock()
		if !hasCancel && !hasCancelRequest && !hasCommit && !hasLanguage {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task %s worker state was not cleaned up", taskID)
}

func assertManagerActiveCount(t *testing.T, manager *Manager, want int) {
	t.Helper()
	manager.mu.Lock()
	got := manager.active
	manager.mu.Unlock()
	if got != want {
		t.Fatalf("expected %d active worker slot(s), got %d", want, got)
	}
}

func waitForManagerIdle(t *testing.T, manager *Manager) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		manager.mu.Lock()
		active := manager.active
		languages := len(manager.languages)
		manager.mu.Unlock()
		if active == 0 && languages == 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("worker manager did not become idle")
}

func assertTerminalEventPair(t *testing.T, publisher *recordingPublisher, taskID, wantStatus string) {
	t.Helper()
	terminalEvents := make([]realtime.Event, 0, 2)
	for _, event := range publisher.taskEvents(taskID) {
		if isTerminalTaskStatus(event.Status) {
			terminalEvents = append(terminalEvents, event)
		}
	}
	if len(terminalEvents) != 2 {
		t.Fatalf("expected exactly two terminal events for task %s, got %#v", taskID, terminalEvents)
	}
	wantTypes := []string{"task.updated", "task.finished"}
	for idx, event := range terminalEvents {
		if event.Type != wantTypes[idx] {
			t.Fatalf("terminal event %d: expected type %q, got %q", idx, wantTypes[idx], event.Type)
		}
		if event.Status != wantStatus {
			t.Fatalf("terminal event %d: expected status %q, got %q", idx, wantStatus, event.Status)
		}
		if event.Payload["taskId"] != taskID || event.Payload["status"] != wantStatus {
			t.Fatalf("terminal event %d payload does not match persisted task outcome: %#v", idx, event.Payload)
		}
	}
}

func isTerminalTaskStatus(status string) bool {
	switch status {
	case "success", "failed", "cancelled", "timeout":
		return true
	default:
		return false
	}
}
