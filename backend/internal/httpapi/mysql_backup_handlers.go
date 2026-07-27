package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

const (
	mysqlBackupTaskType       = "apps.mysql.backup"
	mysqlBackupVerifyTaskType = "apps.mysql.backup.verify"
	mysqlBackupDeleteAction   = "apps.mysql.backup.delete"
)

var mysqlBackupVerificationSteps = []string{"load-backup", "verify-manifest", "verify-checksum", "record-verification"}

func (a *API) listMySQLBackups(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	instance, err := a.store.GetAppInstance(chi.URLParam(r, "id"))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !strings.EqualFold(instance.App, "mysql") {
		writeError(w, http.StatusBadRequest, "MYSQL_INSTANCE_REQUIRED", i18n.Text(lang, "api.mysqlClusterRequired"), map[string]any{"instanceId": instance.ID})
		return
	}
	instanceIDs := []string{instance.ID}
	if appInstanceTopology(instance) == "innodb-cluster" {
		clusterKey := mysqlClusterKey(instance)
		if clusterKey == "" {
			writeError(w, http.StatusBadRequest, "MYSQL_CLUSTER_REQUIRED", i18n.Text(lang, "api.mysqlClusterRequired"), map[string]any{"instanceId": instance.ID})
			return
		}
		instances, err := a.store.ListAppInstances()
		if err != nil {
			respond(w, nil, err)
			return
		}
		instanceIDs = instanceIDs[:0]
		for _, candidate := range instances {
			if candidate.App == "mysql" && appInstanceTopology(candidate) == "innodb-cluster" && mysqlClusterKey(candidate) == clusterKey {
				instanceIDs = append(instanceIDs, candidate.ID)
			}
		}
	}
	backups, err := a.store.ListAppBackupsForInstances(instanceIDs, false)
	if err != nil {
		respond(w, nil, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instanceId": instance.ID,
		"items":      backups,
		"defaults":   map[string]any{"threads": 4, "maxRateMBps": 0, "keepLast": a.cfg.MySQLBackupKeepLast},
	})
}

func (a *API) startMySQLBackup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	payload, ok := decodeMySQLBackupRequest(w, r)
	if !ok {
		return
	}
	instanceID := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(instanceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if instance.App != "mysql" {
		writeError(w, http.StatusBadRequest, "MYSQL_INSTANCE_REQUIRED", i18n.Text(lang, "api.mysqlClusterRequired"), map[string]any{"instanceId": instanceID})
		return
	}
	if appInstanceTopology(instance) != "standalone" {
		writeError(w, http.StatusBadRequest, "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY", i18n.MySQLBackupErrorText(lang, "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY"), map[string]any{"instanceId": instanceID})
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": instanceID})
		return
	}
	server, err := a.store.GetServer(instance.ServerID, false)
	if err != nil {
		respond(w, nil, err)
		return
	}
	module, exists := a.apps.Get("mysql")
	if !exists {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": "mysql"})
		return
	}
	backupModule, supportsBackup := module.(registry.BackupModule)
	if !supportsBackup {
		writeError(w, http.StatusConflict, "MYSQL_BACKUP_UNSUPPORTED", i18n.MySQLBackupErrorText(lang, "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY"), map[string]any{"instanceId": instanceID})
		return
	}
	keepLast := a.cfg.MySQLBackupKeepLast
	if payload.KeepLast != nil {
		keepLast = *payload.KeepLast
	}
	actor := currentUser(r).Username
	backupRequest := registry.BackupRequest{
		Instance:      instance,
		Instances:     []store.AppInstance{instance},
		Servers:       []store.Server{server},
		Language:      lang,
		Actor:         actor,
		RepositoryDir: a.cfg.MySQLBackupDir,
		KeepLast:      keepLast,
		Parameters:    map[string]any{"name": payload.Name, "threads": payload.Threads, "maxRateMBps": payload.MaxRateMBps},
	}
	plan, err := backupModule.PlanBackup(r.Context(), backupRequest)
	if err != nil {
		var stable interface{ StableCode() string }
		if errors.As(err, &stable) && stable.StableCode() != "" {
			code := stable.StableCode()
			writeError(w, http.StatusBadRequest, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instanceID})
			return
		}
		writeError(w, http.StatusBadRequest, "MYSQL_BACKUP_PLAN_FAILED", i18n.Text(lang, "mysql.backup.planFailed"), map[string]any{"instanceId": instanceID})
		return
	}
	task, err := a.store.CreateTask(store.Task{Type: mysqlBackupTaskType, Target: instance.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_BACKUP_PLAN_STORE_FAILED", i18n.Text(lang, "mysql.backup.planStoreFailed"), map[string]any{"instanceId": instanceID})
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, mysqlBackupOperationLockSpecs(instance))
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info("%s", i18n.Text(lang, "mysql.backup.taskStarted"))
		if err := backupModule.Backup(ctx, backupRequest.Clone(), registry.RunContext{
			TaskID:      log.TaskID(),
			Log:         log,
			TargetLog:   func(target string) registry.Logger { return log.Target(target) },
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
		}); err != nil {
			return err
		}
		log.Info("%s", i18n.Text(lang, "mysql.backup.taskCompleted"))
		return nil
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, mysqlBackupTaskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) verifyMySQLBackup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	backupID := strings.TrimSpace(chi.URLParam(r, "backupId"))
	backup, err := a.store.GetAppBackup(backupID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if backup.App != "mysql" || backup.Status != "success" {
		writeError(w, http.StatusConflict, "MYSQL_BACKUP_VERIFY_NOT_ALLOWED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch), map[string]any{"backupId": backupID})
		return
	}
	instance, err := a.store.GetAppInstance(backup.InstanceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	actor := currentUser(r).Username
	task, err := a.store.CreateTask(store.Task{Type: mysqlBackupVerifyTaskType, Target: backup.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	plan := make([]registry.InstallStepPlan, len(mysqlBackupVerificationSteps))
	for index, name := range mysqlBackupVerificationSteps {
		plan[index] = registry.InstallStepPlan{Target: backup.ServerID, Name: name, Title: name, Order: index + 1}
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_BACKUP_VERIFY_PLAN_STORE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch), map[string]any{"backupId": backupID})
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, appInstanceOperationLockSpecs("mysql-backup-verify", []store.AppInstance{instance}))
	if !acquired {
		return
	}
	module := mysqlapp.NewModule(a.store, nil)
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		return module.VerifyBackup(ctx, backup.ID, a.cfg.MySQLBackupDir, lang, registry.RunContext{TaskID: log.TaskID(), Log: log, TargetLog: func(target string) registry.Logger { return log.Target(target) }, Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency)})
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, mysqlBackupVerifyTaskType, backup.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) deleteMySQLBackup(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	backupID := strings.TrimSpace(chi.URLParam(r, "backupId"))
	backup, err := a.store.GetAppBackup(backupID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	fail := func(status int, code, message string) {
		a.audit(r, mysqlBackupDeleteAction, backup.ID, "failed", code)
		writeError(w, status, code, message, map[string]any{"backupId": backup.ID})
	}
	if backup.App != "mysql" || backup.Status == "pending" || backup.Status == "running" || backup.Status == "deleted" || backup.Status != "success" {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_NOT_ALLOWED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	instance, err := a.store.GetAppInstance(backup.InstanceID)
	if err != nil {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_NOT_ALLOWED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	lock, err := a.store.AcquireOperationLock(store.OperationLock{
		Scope: "app-instance", ResourceID: instance.ID, Operation: operationLockMutation,
		Owner: currentUser(r).Username, ExpiresAt: time.Now().UTC().Add(operationLockTTL),
		Metadata: operationLockMetadata(map[string]any{"action": "mysql-backup-delete", "backupId": backup.ID, "instanceId": instance.ID}),
	})
	if err != nil {
		var conflict store.OperationLockConflict
		if errors.As(err, &conflict) {
			fail(http.StatusConflict, "OPERATION_LOCKED", i18n.Text(lang, "api.operationLocked", conflict.Lock.ResourceID))
			return
		}
		fail(http.StatusInternalServerError, "OPERATION_LOCK_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	defer a.store.ReleaseOperationLock(lock.ID)
	backups, err := a.store.ListAppBackupsForInstances([]string{backup.InstanceID}, false)
	if err != nil {
		fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	hasOtherSuccessful := false
	for _, candidate := range backups {
		if candidate.ID != backup.ID && candidate.Status == "success" {
			hasOtherSuccessful = true
			break
		}
	}
	if !hasOtherSuccessful {
		fail(http.StatusConflict, "MYSQL_BACKUP_LAST_SUCCESS_PROTECTED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	repository, err := backuprepo.New(a.cfg.MySQLBackupDir)
	if err != nil {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	fresh, err := a.store.GetAppBackup(backup.ID)
	if err != nil || fresh.Status != "success" || fresh.Path != backup.Path || fresh.Checksum != backup.Checksum || fresh.Size != backup.Size {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	if err := repository.Delete(fresh); err != nil {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	deleted, err := a.store.MarkAppBackupDeleted(fresh.ID, time.Now().UTC())
	if err != nil {
		fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_RECORD_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupChecksumMismatch))
		return
	}
	a.audit(r, mysqlBackupDeleteAction, backup.ID, "success", backup.ID)
	writeJSON(w, http.StatusOK, map[string]any{"backup": deleted})
}
