package aifar

import (
	"context"
	"errors"
	"sync/atomic"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

const (
	runtimeDiagnosticCleanupInterval    = time.Hour
	runtimeDiagnosticCleanupTaskType    = "aifar.runtime.diagnostics.cleanup"
	runtimeDiagnosticCleanupTarget      = "runtime-diagnostics"
	runtimeDiagnosticCleanupActor       = "system"
	runtimeDiagnosticCleanupAuditAction = "containers.aifar.runtime.diagnostics.cleanup"
	runtimeDiagnosticCleanupBatchSize   = 100
)

type RuntimeDiagnosticCleaner struct {
	store         *store.Store
	tasks         *worker.Manager
	remote        Remote
	interval      time.Duration
	running       atomic.Bool
	addAudit      func(string, string, string, string, string) error
	startExisting func(store.Task, worker.Job) (store.Task, error)
}

func NewRuntimeDiagnosticCleaner(s *store.Store, tasks *worker.Manager, remote Remote) *RuntimeDiagnosticCleaner {
	c := &RuntimeDiagnosticCleaner{store: s, tasks: tasks, remote: remote, interval: runtimeDiagnosticCleanupInterval, addAudit: s.AddAudit}
	c.startExisting = func(task store.Task, job worker.Job) (store.Task, error) {
		return tasks.StartExistingWithLanguage(task, "", job)
	}
	return c
}

func (c *RuntimeDiagnosticCleaner) Start(ctx context.Context) {
	if c == nil || c.store == nil || c.tasks == nil || c.remote == nil {
		return
	}
	go func() {
		c.tick(ctx, time.Now().UTC())
		ticker := time.NewTicker(c.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				c.tick(ctx, now.UTC())
			}
		}
	}()
}

func (c *RuntimeDiagnosticCleaner) tick(ctx context.Context, now time.Time) {
	if c == nil || c.store == nil || c.tasks == nil || c.remote == nil || ctx.Err() != nil {
		return
	}
	due, err := c.store.ListDiagnosticExportsDueForCleanup(now.UTC(), 1)
	if err != nil || len(due) == 0 || !c.running.CompareAndSwap(false, true) {
		return
	}
	task, err := c.store.CreateTask(store.Task{Type: runtimeDiagnosticCleanupTaskType, Target: runtimeDiagnosticCleanupTarget, Status: "pending", CreatedBy: runtimeDiagnosticCleanupActor})
	if err != nil {
		c.running.Store(false)
		return
	}
	if err := c.storeCleanupPlan(task.ID); err != nil {
		c.cleanupUnstartedTask(task.ID)
		c.running.Store(false)
		return
	}
	startedTask, err := c.startExisting(task, func(taskCtx context.Context, log worker.Logger) error {
		defer c.running.Store(false)
		return c.run(taskCtx, log, now.UTC(), log.TaskID())
	})
	if err != nil {
		c.cleanupUnstartedTask(task.ID)
		c.running.Store(false)
		return
	}
	go c.releaseWhenTaskFinishes(startedTask.ID)
}

func (c *RuntimeDiagnosticCleaner) storeCleanupPlan(taskID string) error {
	if err := c.store.UpsertTaskTarget(taskID, runtimeDiagnosticCleanupTarget, "pending", ""); err != nil {
		return err
	}
	for index, step := range []struct{ name, title string }{{"mark-expired", i18n.Text("", "aifar.diag.clean.mark")}, {"delete-remote-artifacts", i18n.Text("", "aifar.diag.clean.delete")}, {"record-cleanup", i18n.Text("", "aifar.diag.clean.record")}} {
		if err := c.store.UpsertTaskStep(taskID, runtimeDiagnosticCleanupTarget, step.name, step.title, index+1, "pending", ""); err != nil {
			return err
		}
	}
	return nil
}

func (c *RuntimeDiagnosticCleaner) cleanupUnstartedTask(taskID string) {
	if err := c.store.DeleteTask(taskID); err != nil {
		_ = c.store.UpdateTaskStatus(taskID, "failed", i18n.Text("", "aifar.diag.cleanupFailed"))
	}
}

func (c *RuntimeDiagnosticCleaner) releaseWhenTaskFinishes(taskID string) {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		task, _, err := c.store.GetTask(taskID)
		if err != nil {
			return
		}
		switch task.Status {
		case "success", "failed", "cancelled":
			c.running.Store(false)
			return
		}
	}
}

func (c *RuntimeDiagnosticCleaner) run(ctx context.Context, log worker.Logger, now time.Time, taskID string) error {
	log.PlanTarget(runtimeDiagnosticCleanupTarget)
	log.PlanStep(runtimeDiagnosticCleanupTarget, "mark-expired", i18n.Text("", "aifar.diag.clean.mark"), 1)
	log.PlanStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", i18n.Text("", "aifar.diag.clean.delete"), 2)
	log.PlanStep(runtimeDiagnosticCleanupTarget, "record-cleanup", i18n.Text("", "aifar.diag.clean.record"), 3)
	log.StartTarget(runtimeDiagnosticCleanupTarget)
	log.StartStep(runtimeDiagnosticCleanupTarget, "mark-expired", "", 0)
	log.StartStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", "", 0)
	log.StartStep(runtimeDiagnosticCleanupTarget, "record-cleanup", "", 0)
	var afterExpiresAt time.Time
	var afterID string
	for {
		due, err := c.store.ListDiagnosticExportsDueForCleanupAfter(now, afterExpiresAt, afterID, runtimeDiagnosticCleanupBatchSize)
		if err != nil {
			log.FinishTarget(runtimeDiagnosticCleanupTarget, "failed", i18n.Text("", "aifar.diag.cleanupFailed"))
			return err
		}
		if len(due) == 0 {
			break
		}
		for _, export := range due {
			if err := ctx.Err(); err != nil {
				log.FinishTarget(runtimeDiagnosticCleanupTarget, "cancelled", err.Error())
				return err
			}
			c.cleanupOne(ctx, log, export, now, taskID)
		}
		last := due[len(due)-1]
		afterExpiresAt, afterID = last.ExpiresAt, last.ID
	}
	log.FinishStep(runtimeDiagnosticCleanupTarget, "mark-expired", "success", "")
	log.FinishStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", "success", "")
	log.FinishStep(runtimeDiagnosticCleanupTarget, "record-cleanup", "success", "")
	log.FinishTarget(runtimeDiagnosticCleanupTarget, "success", "")
	return nil
}

func (c *RuntimeDiagnosticCleaner) cleanupOne(ctx context.Context, log worker.Logger, export store.DiagnosticExport, now time.Time, taskID string) {
	lock, err := c.store.AcquireOperationLock(store.OperationLock{Scope: "runtime-diagnostics", ResourceID: export.ID, Operation: "delete", OwnerTaskID: taskID, Owner: runtimeDiagnosticCleanupActor, ExpiresAt: time.Now().UTC().Add(time.Hour)})
	if err != nil {
		var conflict store.OperationLockConflict
		if !errors.As(err, &conflict) {
			log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		}
		return
	}
	defer c.store.ReleaseOperationLock(lock.ID)
	updated, err := c.store.MarkDiagnosticExportCleanupPending(export.ID, now)
	if err != nil || !updated {
		return
	}
	service := NewService(c.store, c.remote)
	current, _, server, installRoot, err := service.loadRuntimeDiagnosticArtifact(export.ID, "", "", "", true)
	if err == nil {
		err = service.cleanupRuntimeDiagnosticExport(ctx, server, installRoot, current.ID)
	}
	c.recordCleanupResult(log, export, err, now)
}

func (c *RuntimeDiagnosticCleaner) recordCleanupResult(log worker.Logger, export store.DiagnosticExport, remoteErr error, now time.Time) {
	if remoteErr != nil {
		_, _ = c.store.MarkDiagnosticExportCleanupFailed(export.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
		if err := c.addAudit(runtimeDiagnosticCleanupActor, runtimeDiagnosticCleanupAuditAction, export.ID, "failed", i18n.Text("", "aifar.diag.cleanupFailed")); err != nil {
			log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		}
		log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		return
	}
	if err := c.addAudit(runtimeDiagnosticCleanupActor, runtimeDiagnosticCleanupAuditAction, export.ID, "success", i18n.Text("", "aifar.diag.deleteCompleted", export.ArchiveName)); err != nil {
		_, _ = c.store.MarkDiagnosticExportCleanupFailed(export.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
		log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		return
	}
	updated, err := c.store.MarkDiagnosticExportDeleted(export.ID, now)
	if err != nil || !updated {
		_, _ = c.store.MarkDiagnosticExportCleanupFailed(export.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
		log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		return
	}
	log.Info("%s", i18n.Text("", "aifar.diag.deleteCompleted", export.ArchiveName))
}
