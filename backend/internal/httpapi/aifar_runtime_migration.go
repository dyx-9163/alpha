package httpapi

import (
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
	"context"
	"database/sql"
	"errors"
	"github.com/go-chi/chi/v5"
	"net/http"
	"strings"
)

const runtimeMigrationTaskType = "aifar.runtime.migrate"

func (a *aifarRuntimeController) runtimeMigrationStatus(w http.ResponseWriter, r *http.Request) {
	instance, err := a.store.GetAppInstance(chi.URLParam(r, "id"))
	if err != nil || instance.App != "aifar" {
		writeError(w, 404, "AIFAR_INSTANCE_NOT_FOUND", i18n.Text(languageFromRequest(r), "aifar.migration.notFound"), nil)
		return
	}
	status := "migration-required"
	if runtimeString(runtimeMetadata(instance.Metadata), "orchestrationModel", "") == "agent-service-controller-v1" {
		status = "complete"
	}
	task, err := a.store.LatestTaskForTarget(runtimeMigrationTaskType, instance.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		respond(w, nil, err)
		return
	}
	var latest *store.Task
	if err == nil {
		decorated := decorateTask(task)
		latest = &decorated
	}
	writeJSON(w, 200, map[string]any{"instanceId": instance.ID, "status": status, "lastTask": latest})
}

func (a *aifarRuntimeController) migrateRuntime(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var body struct {
		Reason       string `json:"reason"`
		ResumeTaskID string `json:"resumeTaskId"`
	}
	if !decodeStrictRuntimeDeploymentBody(w, r, &body) {
		return
	}
	instance, err := a.store.GetAppInstance(chi.URLParam(r, "id"))
	if err != nil || instance.App != "aifar" {
		writeError(w, 404, "AIFAR_INSTANCE_NOT_FOUND", i18n.Text(lang, "aifar.migration.notFound"), nil)
		return
	}
	body.Reason = strings.TrimSpace(body.Reason)
	body.ResumeTaskID = strings.TrimSpace(body.ResumeTaskID)
	if len(body.Reason) > 1024 {
		writeError(w, 400, "INVALID_MIGRATION_REASON", i18n.Text(lang, "aifar.migration.invalid"), nil)
		return
	}
	if body.ResumeTaskID != "" {
		previous, _, err := a.store.GetTask(body.ResumeTaskID)
		if err != nil || previous.Type != runtimeMigrationTaskType || previous.Target != instance.ID {
			writeError(w, 400, "INVALID_MIGRATION_RESUME_TASK", i18n.Text(lang, "aifar.migration.invalid"), nil)
			return
		}
		if previous.Status == "running" || previous.Status == "pending" {
			writeError(w, 409, "MIGRATION_TASK_ACTIVE", i18n.Text(lang, "aifar.migration.active"), map[string]any{"ownerTaskId": previous.ID})
			return
		}
	}
	if runtimeString(runtimeMetadata(instance.Metadata), "orchestrationModel", "") == "agent-service-controller-v1" {
		writeJSON(w, 200, map[string]any{"instanceId": instance.ID, "status": "complete"})
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, 409, "MIGRATION_UNAVAILABLE", i18n.Text(lang, "aifar.migration.unavailable"), nil)
		return
	}
	migration, ok := module.(registry.RuntimeMigrationModule)
	if !ok {
		writeError(w, 409, "MIGRATION_UNAVAILABLE", i18n.Text(lang, "aifar.migration.unavailable"), nil)
		return
	}
	actor := currentUser(r).Username
	task, err := a.store.CreateTask(store.Task{Type: runtimeMigrationTaskType, Target: instance.ID, CreatedBy: actor, CorrelationID: body.ResumeTaskID})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	phases := []string{"preflight", "bootstrap", "readback", "archive", "commit"}
	steps := make([]simpleTaskStep, 0, len(phases))
	for _, name := range phases {
		steps = append(steps, simpleTaskStep{name: name, title: i18n.Text(lang, "aifar.migration."+name)})
	}
	if err = a.storeRuntimePlan(task.ID, simpleTaskPlan(instance.ID, steps)); err != nil {
		_ = a.store.UpdateTaskStatus(task.ID, "failed", i18n.Text(lang, "aifar.migration.failed"))
		respondTask(w, task, err)
		return
	}
	taskID := task.ID
	task, err = a.startExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.StartTarget(instance.ID)
		err := migration.MigrateRuntimeInstance(ctx, instance.ID, body.Reason, registry.RunContext{TaskID: taskID, Actor: actor, Language: lang, Log: log.Target(instance.ID)})
		status, detail := "success", ""
		if err != nil {
			status = "failed"
			detail = i18n.Text(lang, "aifar.migration.failed")
		}
		log.FinishTarget(instance.ID, status, detail)
		return err
	})
	if err != nil {
		_ = a.store.UpdateTaskStatus(taskID, "failed", i18n.Text(lang, "aifar.migration.failed"))
	}
	a.audit(r, runtimeMigrationTaskType, instance.ID, "submitted", taskID)
	respondTask(w, task, err)
}
