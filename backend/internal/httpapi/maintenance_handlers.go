package httpapi

import (
	"context"
	"fmt"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/maintenance"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

func (a *API) maintenanceService() maintenance.Service {
	return maintenance.NewService(a.store, maintenance.RetentionConfig{
		AuditRetentionDays: a.cfg.AuditRetentionDays,
		TaskRetentionDays:  a.cfg.TaskRetentionDays,
	})
}

func (a *API) listDatabaseBackups(w http.ResponseWriter, r *http.Request) {
	backups, err := a.maintenanceService().ListDatabaseBackups(a.cfg.DatabaseBackupDir)
	respond(w, map[string]any{"items": backups, "backupDir": a.cfg.DatabaseBackupDir}, err)
}

func (a *API) downloadDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	backup, err := a.maintenanceService().GetDatabaseBackup(a.cfg.DatabaseBackupDir, chi.URLParam(r, "name"))
	if err != nil && isInvalidBackupNameError(err) {
		writeError(w, http.StatusBadRequest, "INVALID_BACKUP_NAME", i18n.Text(lang, "api.invalidDatabaseBackupName"), map[string]any{"error": err.Error()})
		return
	}
	if err != nil {
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "BACKUP_NOT_FOUND", i18n.Text(lang, "api.databaseBackupNotFound"), nil)
			return
		}
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": backup.Name}))
	w.Header().Set("X-AIFAR-Backup-SHA256", backup.SHA256)
	w.Header().Set("X-AIFAR-Backup-Size", strconv.FormatInt(backup.Size, 10))
	http.ServeFile(w, r, backup.Path)
}

func (a *API) verifyDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	name := chi.URLParam(r, "name")
	service := a.maintenanceService()
	if _, err := service.GetDatabaseBackup(a.cfg.DatabaseBackupDir, name); err != nil {
		if isInvalidBackupNameError(err) {
			writeError(w, http.StatusBadRequest, "INVALID_BACKUP_NAME", i18n.Text(lang, "api.invalidDatabaseBackupName"), map[string]any{"error": err.Error()})
			return
		}
		if os.IsNotExist(err) {
			writeError(w, http.StatusNotFound, "BACKUP_NOT_FOUND", i18n.Text(lang, "api.databaseBackupNotFound"), nil)
			return
		}
		respond(w, nil, err)
		return
	}
	actor := currentUser(r).Username
	taskType := "maintenance.database.backup.verify"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: name, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan(name, databaseBackupVerifySteps(lang))); err != nil {
		writeError(w, http.StatusInternalServerError, "MAINTENANCE_PLAN_STORE_FAILED", err.Error(), map[string]any{"target": name})
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.StartTarget(name)

		log.StartStep(name, "locate-backup", i18n.Text(lang, "maintenance.locateBackupStep"), 1)
		backup, err := service.GetDatabaseBackup(a.cfg.DatabaseBackupDir, name)
		if err != nil {
			log.FinishStep(name, "locate-backup", "failed", err.Error())
			log.FinishTarget(name, "failed", err.Error())
			return err
		}
		log.Info(i18n.Text(lang, "maintenance.backupLocated"), backup.Name, backup.Size, backup.SHA256)
		log.FinishStep(name, "locate-backup", "success", "")

		log.StartStep(name, "integrity-check", i18n.Text(lang, "maintenance.integrityCheckStep"), 2)
		if err := ctx.Err(); err != nil {
			log.FinishStep(name, "integrity-check", "cancelled", err.Error())
			log.FinishTarget(name, "cancelled", err.Error())
			return err
		}
		verification, err := service.VerifyDatabaseBackup(a.cfg.DatabaseBackupDir, name)
		if err != nil {
			log.FinishStep(name, "integrity-check", "failed", err.Error())
			log.FinishTarget(name, "failed", err.Error())
			return err
		}
		log.Info(i18n.Text(lang, "maintenance.integrityCheckResult"), verification.IntegrityCheck)
		if verification.IntegrityCheck != "ok" {
			err := fmt.Errorf("%s", i18n.Text(lang, "maintenance.backupVerificationFailed"))
			log.FinishStep(name, "integrity-check", "failed", verification.IntegrityCheck)
			log.FinishTarget(name, "failed", verification.IntegrityCheck)
			return err
		}
		log.FinishStep(name, "integrity-check", "success", "")

		log.StartStep(name, "schema-check", i18n.Text(lang, "maintenance.schemaCheckStep"), 3)
		if len(verification.MissingTables) > 0 {
			message := i18n.Text(lang, "maintenance.schemaCheckMissing", strings.Join(verification.MissingTables, ", "))
			log.Error("%s", message)
			log.FinishStep(name, "schema-check", "failed", message)
			log.FinishTarget(name, "failed", message)
			return fmt.Errorf("%s", message)
		}
		log.Info(i18n.Text(lang, "maintenance.schemaCheckOK"), len(verification.RequiredTables))
		log.FinishStep(name, "schema-check", "success", "")
		log.Info(i18n.Text(lang, "maintenance.backupVerificationCompleted"), verification.Backup.Name)
		log.FinishTarget(name, "success", "")
		return nil
	})
	if err == nil {
		a.audit(r, taskType, name, "running", i18n.Text(lang, "api.databaseBackupVerifyStarted"))
	}
	respondTask(w, task, err)
}

func (a *API) deleteDatabaseBackups(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req deleteDatabaseBackupsRequest
	if !decode(w, r, &req) {
		return
	}
	names := cleanStringIDs(req.Names)
	if len(names) == 0 {
		writeError(w, http.StatusBadRequest, "NAMES_REQUIRED", i18n.Text(lang, "api.databaseBackupNamesRequired"), nil)
		return
	}
	deleted, deletedNames, err := a.maintenanceService().DeleteDatabaseBackups(a.cfg.DatabaseBackupDir, names)
	if err != nil && isInvalidBackupNameError(err) {
		writeError(w, http.StatusBadRequest, "INVALID_BACKUP_NAME", i18n.Text(lang, "api.invalidDatabaseBackupName"), map[string]any{"error": err.Error()})
		return
	}
	if err == nil {
		a.audit(r, "maintenance.database.backup.delete", strings.Join(deletedNames, ","), "success", i18n.Text(lang, "api.databaseBackupsDeleted", deleted))
	}
	respond(w, map[string]any{"deleted": deleted, "names": deletedNames}, err)
}

func (a *API) runDatabaseBackup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	target := "control-plane"
	service := a.maintenanceService()
	actor := currentUser(r).Username
	taskType := "maintenance.database.backup"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan(target, databaseBackupSteps(lang))); err != nil {
		writeError(w, http.StatusInternalServerError, "MAINTENANCE_PLAN_STORE_FAILED", err.Error(), map[string]any{"target": target})
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.StartTarget(target)

		log.StartStep(target, "prepare-backup", i18n.Text(lang, "maintenance.prepareBackupStep"), 1)
		if err := ctx.Err(); err != nil {
			log.FinishStep(target, "prepare-backup", "cancelled", err.Error())
			log.FinishTarget(target, "cancelled", err.Error())
			return err
		}
		log.Info(i18n.Text(lang, "maintenance.backupDir"), a.cfg.DatabaseBackupDir)
		log.FinishStep(target, "prepare-backup", "success", "")

		log.StartStep(target, "backup-database", i18n.Text(lang, "maintenance.backupDatabaseStep"), 2)
		backup, err := service.BackupDatabase(a.cfg.DatabaseBackupDir, time.Now())
		if err != nil {
			log.FinishStep(target, "backup-database", "failed", err.Error())
			log.FinishTarget(target, "failed", err.Error())
			return err
		}
		log.Info(i18n.Text(lang, "maintenance.backupCreated"), backup.Path)
		log.FinishStep(target, "backup-database", "success", "")

		log.StartStep(target, "verify-backup", i18n.Text(lang, "maintenance.verifyBackupStep"), 3)
		log.Info(i18n.Text(lang, "maintenance.backupVerified"), backup.Size, backup.SHA256)
		log.FinishStep(target, "verify-backup", "success", "")
		log.FinishTarget(target, "success", "")
		return nil
	})
	if err == nil {
		a.audit(r, taskType, target, "running", i18n.Text(lang, "api.databaseBackupStarted"))
	}
	respondTask(w, task, err)
}

func (a *API) runRetentionCleanup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	target := "control-plane"
	service := a.maintenanceService()
	actor := currentUser(r).Username
	taskType := "maintenance.retention.run"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeTaskPlanOrDelete(task.ID, simpleTaskPlan(target, retentionCleanupSteps(lang))); err != nil {
		writeError(w, http.StatusInternalServerError, "MAINTENANCE_PLAN_STORE_FAILED", err.Error(), map[string]any{"target": target})
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		plan := service.Plan(time.Now())
		log.StartTarget(target)

		log.StartStep(target, "cleanup-audit", i18n.Text(lang, "maintenance.cleanupAuditStep"), 1)
		if err := ctx.Err(); err != nil {
			log.FinishStep(target, "cleanup-audit", "cancelled", err.Error())
			log.FinishTarget(target, "cancelled", err.Error())
			return err
		}
		auditDeleted, err := service.CleanupAudit(plan)
		if err != nil {
			log.FinishStep(target, "cleanup-audit", "failed", err.Error())
			log.FinishTarget(target, "failed", err.Error())
			return err
		}
		log.Info(i18n.Text(lang, "maintenance.auditDeleted"), auditDeleted, formatRetentionCutoff(plan.AuditCutoff))
		log.FinishStep(target, "cleanup-audit", "success", "")

		log.StartStep(target, "cleanup-tasks", i18n.Text(lang, "maintenance.cleanupTasksStep"), 2)
		if err := ctx.Err(); err != nil {
			log.FinishStep(target, "cleanup-tasks", "cancelled", err.Error())
			log.FinishTarget(target, "cancelled", err.Error())
			return err
		}
		tasksDeleted, err := service.CleanupTasks(plan)
		if err != nil {
			log.FinishStep(target, "cleanup-tasks", "failed", err.Error())
			log.FinishTarget(target, "failed", err.Error())
			return err
		}
		log.Info(i18n.Text(lang, "maintenance.tasksDeleted"), tasksDeleted, formatRetentionCutoff(plan.TaskCutoff))
		log.FinishStep(target, "cleanup-tasks", "success", "")
		log.FinishTarget(target, "success", "")
		return nil
	})
	if err == nil {
		a.audit(r, taskType, target, "running", i18n.Text(lang, "api.retentionCleanupStarted"))
	}
	respondTask(w, task, err)
}

func databaseBackupVerifySteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{"locate-backup", i18n.Text(lang, "maintenance.locateBackupStep")},
		{"integrity-check", i18n.Text(lang, "maintenance.integrityCheckStep")},
		{"schema-check", i18n.Text(lang, "maintenance.schemaCheckStep")},
	}
}

func databaseBackupSteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{"prepare-backup", i18n.Text(lang, "maintenance.prepareBackupStep")},
		{"backup-database", i18n.Text(lang, "maintenance.backupDatabaseStep")},
		{"verify-backup", i18n.Text(lang, "maintenance.verifyBackupStep")},
	}
}

func retentionCleanupSteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{"cleanup-audit", i18n.Text(lang, "maintenance.cleanupAuditStep")},
		{"cleanup-tasks", i18n.Text(lang, "maintenance.cleanupTasksStep")},
	}
}

func formatRetentionCutoff(cutoff time.Time) string {
	if cutoff.IsZero() {
		return "-"
	}
	return cutoff.Format(time.RFC3339)
}

func isInvalidBackupNameError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "backup name is required") ||
		strings.Contains(msg, "invalid backup name") ||
		strings.Contains(msg, "escapes backup directory")
}
