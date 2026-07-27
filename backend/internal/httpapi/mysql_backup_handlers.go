package httpapi

import (
	"context"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

const mysqlBackupTaskType = "apps.mysql.backup"

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
		writeError(w, http.StatusBadRequest, "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY", mysqlBackupHandlerText(lang, "unsupported"), map[string]any{"instanceId": instanceID})
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
		writeError(w, http.StatusConflict, "MYSQL_BACKUP_UNSUPPORTED", mysqlBackupHandlerText(lang, "unsupported"), map[string]any{"instanceId": instanceID})
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
		writeError(w, http.StatusBadRequest, "MYSQL_BACKUP_PLAN_FAILED", err.Error(), map[string]any{"instanceId": instanceID})
		return
	}
	task, err := a.store.CreateTask(store.Task{Type: mysqlBackupTaskType, Target: instance.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_BACKUP_PLAN_STORE_FAILED", err.Error(), map[string]any{"instanceId": instanceID})
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, mysqlBackupOperationLockSpecs(instance))
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info("%s", mysqlBackupHandlerText(lang, "started"))
		if err := backupModule.Backup(ctx, backupRequest.Clone(), registry.RunContext{
			TaskID:      log.TaskID(),
			Log:         log,
			TargetLog:   func(target string) registry.Logger { return log.Target(target) },
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
		}); err != nil {
			return err
		}
		log.Info("%s", mysqlBackupHandlerText(lang, "completed"))
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

func mysqlBackupHandlerText(lang, kind string) string {
	english := strings.HasPrefix(strings.ToLower(strings.TrimSpace(lang)), "en")
	switch kind {
	case "started":
		if english {
			return "MySQL backup task started"
		}
		return "MySQL 备份任务已启动"
	case "completed":
		if english {
			return "MySQL backup task completed"
		}
		return "MySQL 备份任务已完成"
	default:
		if english {
			return "MySQL backup topology is unsupported"
		}
		return "不支持该 MySQL 备份拓扑"
	}
}
