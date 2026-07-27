package httpapi

import (
	"context"
	"database/sql"
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
		writeError(w, http.StatusNotFound, mysqlapp.MySQLBackupVerifyNotAllowed, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupVerifyNotAllowed), map[string]any{"backupId": backupID})
		return
	}
	if backup.App != "mysql" || backup.Status != "success" {
		writeError(w, http.StatusConflict, mysqlapp.MySQLBackupVerifyNotAllowed, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupVerifyNotAllowed), map[string]any{"backupId": backupID})
		return
	}
	instance, err := a.store.GetAppInstance(backup.InstanceID)
	if err != nil {
		writeError(w, http.StatusConflict, mysqlapp.MySQLBackupStandaloneRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupStandaloneRequired), map[string]any{"backupId": backupID})
		return
	}
	if instance.App != "mysql" || strings.ToLower(strings.TrimSpace(instance.Topology)) != "standalone" || instance.ID != backup.InstanceID || instance.ServerID != backup.ServerID || backup.BackupType != "logical-full" {
		writeError(w, http.StatusConflict, mysqlapp.MySQLBackupStandaloneRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupStandaloneRequired), map[string]any{"backupId": backupID})
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
		plan[index] = registry.InstallStepPlan{Target: backup.ID, Name: name, Title: i18n.Text(lang, "mysql.backup.verify.step."+name), Order: index + 1}
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_BACKUP_VERIFY_PLAN_STORE_FAILED", i18n.MySQLBackupErrorText(lang, "MYSQL_BACKUP_VERIFY_PLAN_STORE_FAILED"), map[string]any{"backupId": backupID})
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
	auditTarget := backupID
	if !backuprepo.ValidBackupID(backupID) {
		auditTarget = "invalid-backup-id"
	}
	fail := func(status int, code string) {
		a.audit(r, mysqlBackupDeleteAction, auditTarget, "failed", code)
		writeError(w, status, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"backupId": auditTarget})
	}
	if auditTarget == "invalid-backup-id" {
		fail(http.StatusBadRequest, "MYSQL_BACKUP_ID_INVALID")
		return
	}
	backup, err := a.store.GetAppBackup(backupID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			fail(http.StatusNotFound, "MYSQL_BACKUP_DELETE_NOT_FOUND")
		} else {
			fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_LOOKUP_FAILED")
		}
		return
	}
	if backup.App != "mysql" || backup.Status == "pending" || backup.Status == "running" || backup.Status == "deleted" || backup.Status != "success" {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_NOT_ALLOWED")
		return
	}
	instance, err := a.store.GetAppInstance(backup.InstanceID)
	if err != nil {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_NOT_ALLOWED")
		return
	}
	if instance.App != "mysql" || strings.ToLower(strings.TrimSpace(instance.Topology)) != "standalone" || instance.ID != backup.InstanceID || instance.ServerID != backup.ServerID || backup.BackupType != "logical-full" {
		fail(http.StatusConflict, mysqlapp.MySQLBackupStandaloneRequired)
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
			a.audit(r, mysqlBackupDeleteAction, auditTarget, "failed", "OPERATION_LOCKED")
			writeError(w, http.StatusConflict, "OPERATION_LOCKED", i18n.Text(lang, "api.operationLocked", conflict.Lock.ResourceID), map[string]any{"backupId": auditTarget})
			return
		}
		fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_FAILED")
		return
	}
	defer a.store.ReleaseOperationLock(lock.ID)
	repository, err := backuprepo.New(a.cfg.MySQLBackupDir)
	if err != nil {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_FAILED")
		return
	}
	backups, err := a.store.ListAppBackupsForInstances([]string{backup.InstanceID}, false)
	if err != nil {
		fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_FAILED")
		return
	}
	hasOtherVerified := false
	for _, candidate := range backups {
		if candidate.ID == backup.ID || candidate.Status != "success" || candidate.App != "mysql" || candidate.InstanceID != instance.ID || candidate.ServerID != instance.ServerID || candidate.BackupType != "logical-full" {
			continue
		}
		freshCandidate, getErr := a.store.GetAppBackup(candidate.ID)
		if getErr != nil || !sameMySQLBackupRecord(candidate, freshCandidate) {
			continue
		}
		if _, verifyErr := repository.Verify(freshCandidate); verifyErr == nil {
			hasOtherVerified = true
			break
		}
	}
	if !hasOtherVerified {
		fail(http.StatusConflict, "MYSQL_BACKUP_LAST_SUCCESS_PROTECTED")
		return
	}
	fresh, err := a.store.GetAppBackup(backup.ID)
	if err != nil || !sameMySQLBackupRecord(backup, fresh) {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_FAILED")
		return
	}
	deletion, err := repository.BeginDelete(fresh)
	if err != nil {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_FAILED")
		return
	}
	deleted, err := a.store.MarkAppBackupDeleted(fresh.ID, time.Now().UTC())
	if err != nil {
		if rollbackErr := deletion.Rollback(); rollbackErr != nil {
			fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_ROLLBACK_FAILED")
		} else {
			fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_RECORD_FAILED")
		}
		return
	}
	if err := deletion.Finalize(); err != nil {
		fail(http.StatusInternalServerError, "MYSQL_BACKUP_DELETE_FINALIZE_FAILED")
		return
	}
	a.audit(r, mysqlBackupDeleteAction, backup.ID, "success", backup.ID)
	writeJSON(w, http.StatusOK, map[string]any{"backup": deleted})
}

func sameMySQLBackupRecord(left, right store.AppBackup) bool {
	return left.ID == right.ID && left.App == right.App && left.InstanceID == right.InstanceID && left.ServerID == right.ServerID && left.BackupType == right.BackupType && left.Status == right.Status && left.Path == right.Path && left.Checksum == right.Checksum && left.Size == right.Size && left.TaskID == right.TaskID && left.Metadata == right.Metadata && left.CreatedAt.Equal(right.CreatedAt) && left.CompletedAt.Equal(right.CompletedAt)
}
