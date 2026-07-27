package aifar

import (
	"context"
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
	store    *store.Store
	tasks    *worker.Manager
	remote   Remote
	interval time.Duration
	running  atomic.Bool
}

func NewRuntimeDiagnosticCleaner(s *store.Store, tasks *worker.Manager, remote Remote) *RuntimeDiagnosticCleaner {
	return &RuntimeDiagnosticCleaner{store: s, tasks: tasks, remote: remote, interval: runtimeDiagnosticCleanupInterval}
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
	due, err := c.store.ListDiagnosticExportsDueForCleanup(now.UTC(), runtimeDiagnosticCleanupBatchSize)
	if err != nil || len(due) == 0 || !c.running.CompareAndSwap(false, true) {
		return
	}
	_, err = c.tasks.Start(runtimeDiagnosticCleanupTaskType, runtimeDiagnosticCleanupTarget, runtimeDiagnosticCleanupActor, func(taskCtx context.Context, log worker.Logger) error {
		defer c.running.Store(false)
		return c.run(taskCtx, log, now.UTC())
	})
	if err != nil {
		c.running.Store(false)
	}
}

func (c *RuntimeDiagnosticCleaner) run(ctx context.Context, log worker.Logger, now time.Time) error {
	log.PlanTarget(runtimeDiagnosticCleanupTarget)
	log.PlanStep(runtimeDiagnosticCleanupTarget, "mark-expired", i18n.Text("", "aifar.diag.clean.mark"), 1)
	log.PlanStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", i18n.Text("", "aifar.diag.clean.delete"), 2)
	log.PlanStep(runtimeDiagnosticCleanupTarget, "record-cleanup", i18n.Text("", "aifar.diag.clean.record"), 3)
	log.StartTarget(runtimeDiagnosticCleanupTarget)

	due, err := c.store.ListDiagnosticExportsDueForCleanup(now, runtimeDiagnosticCleanupBatchSize)
	if err != nil {
		log.FinishTarget(runtimeDiagnosticCleanupTarget, "failed", i18n.Text("", "aifar.diag.cleanupFailed"))
		return err
	}

	log.StartStep(runtimeDiagnosticCleanupTarget, "mark-expired", "", 0)
	pending := make([]store.DiagnosticExport, 0, len(due))
	for _, export := range due {
		if err := ctx.Err(); err != nil {
			log.FinishStep(runtimeDiagnosticCleanupTarget, "mark-expired", "cancelled", err.Error())
			log.FinishTarget(runtimeDiagnosticCleanupTarget, "cancelled", err.Error())
			return err
		}
		updated, markErr := c.store.MarkDiagnosticExportCleanupPending(export.ID, now)
		if markErr != nil {
			log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
			continue
		}
		if updated {
			pending = append(pending, export)
		}
	}
	log.FinishStep(runtimeDiagnosticCleanupTarget, "mark-expired", "success", "")

	log.StartStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", "", 0)
	results := make([]runtimeDiagnosticCleanupResult, 0, len(pending))
	for _, export := range pending {
		if err := ctx.Err(); err != nil {
			log.FinishStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", "cancelled", err.Error())
			log.FinishTarget(runtimeDiagnosticCleanupTarget, "cancelled", err.Error())
			return err
		}
		results = append(results, runtimeDiagnosticCleanupResult{export: export, err: c.deleteRemoteArchive(ctx, export)})
	}
	log.FinishStep(runtimeDiagnosticCleanupTarget, "delete-remote-artifacts", "success", "")

	log.StartStep(runtimeDiagnosticCleanupTarget, "record-cleanup", "", 0)
	for _, result := range results {
		c.recordCleanupResult(log, result, now)
	}
	log.FinishStep(runtimeDiagnosticCleanupTarget, "record-cleanup", "success", "")
	log.FinishTarget(runtimeDiagnosticCleanupTarget, "success", "")
	return nil
}

type runtimeDiagnosticCleanupResult struct {
	export store.DiagnosticExport
	err    error
}

func (c *RuntimeDiagnosticCleaner) deleteRemoteArchive(ctx context.Context, export store.DiagnosticExport) error {
	service := NewService(c.store, c.remote)
	current, _, server, installRoot, err := service.loadRuntimeDiagnosticArtifact(export.ID, "", "", "", true)
	if err == nil {
		err = service.cleanupRuntimeDiagnosticExport(ctx, server, installRoot, current.ID)
	}
	return err
}

func (c *RuntimeDiagnosticCleaner) recordCleanupResult(log worker.Logger, result runtimeDiagnosticCleanupResult, now time.Time) {
	if result.err != nil {
		_, _ = c.store.MarkDiagnosticExportCleanupFailed(result.export.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
		_ = c.store.AddAudit(runtimeDiagnosticCleanupActor, runtimeDiagnosticCleanupAuditAction, result.export.ID, "failed", i18n.Text("", "aifar.diag.cleanupFailed"))
		log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		return
	}
	updated, markErr := c.store.MarkDiagnosticExportDeleted(result.export.ID, now)
	if markErr != nil || !updated {
		_, _ = c.store.MarkDiagnosticExportCleanupFailed(result.export.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
		_ = c.store.AddAudit(runtimeDiagnosticCleanupActor, runtimeDiagnosticCleanupAuditAction, result.export.ID, "failed", i18n.Text("", "aifar.diag.cleanupFailed"))
		log.Error("%s", i18n.Text("", "aifar.diag.cleanupFailed"))
		return
	}
	_ = c.store.AddAudit(runtimeDiagnosticCleanupActor, runtimeDiagnosticCleanupAuditAction, result.export.ID, "success", i18n.Text("", "aifar.diag.deleteCompleted", result.export.ArchiveName))
	log.Info("%s", i18n.Text("", "aifar.diag.deleteCompleted", result.export.ArchiveName))
}
