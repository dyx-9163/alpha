package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
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
	mysqlRestoreTaskType      = "apps.mysql.restore"
	mysqlBackupDeleteAction   = "apps.mysql.backup.delete"
)

// mysqlBackupTargets gives the worker a complete stable snapshot for planning;
// the MySQL module re-reads app_clusters and runtime membership before it ever
// chooses a PRIMARY.
func (a *API) mysqlBackupTargets(instance store.AppInstance) ([]store.AppInstance, []store.Server, error) {
	if appInstanceTopology(instance) != "innodb-cluster" {
		server, err := a.store.GetServer(instance.ServerID, false)
		return []store.AppInstance{instance}, []store.Server{server}, err
	}
	clusterID := mysqlClusterID(instance)
	if clusterID == "" {
		return nil, nil, sql.ErrNoRows
	}
	members, err := a.store.ListAppClusterMembers(clusterID)
	if err != nil || len(members) != 3 {
		return nil, nil, sql.ErrNoRows
	}
	instances := make([]store.AppInstance, 0, len(members))
	servers := make([]store.Server, 0, len(members))
	for _, member := range members {
		candidate, getErr := a.store.GetAppInstance(member.InstanceID)
		if getErr != nil || candidate.App != "mysql" || appInstanceTopology(candidate) != "innodb-cluster" || mysqlClusterID(candidate) != clusterID || candidate.ServerID != member.ServerID {
			return nil, nil, sql.ErrNoRows
		}
		server, getErr := a.store.GetServer(candidate.ServerID, false)
		if getErr != nil {
			return nil, nil, getErr
		}
		instances, servers = append(instances, candidate), append(servers, server)
	}
	return instances, servers, nil
}

func mysqlClusterIDFromBackup(backup store.AppBackup) string {
	metadata := map[string]json.RawMessage{}
	if json.Unmarshal([]byte(backup.Metadata), &metadata) != nil {
		return ""
	}
	var clusterID string
	_ = json.Unmarshal(metadata["clusterId"], &clusterID)
	return strings.TrimSpace(clusterID)
}

func mysqlClusterID(instance store.AppInstance) string {
	metadata := map[string]json.RawMessage{}
	if json.Unmarshal([]byte(instance.Metadata), &metadata) != nil {
		return ""
	}
	var clusterID string
	if json.Unmarshal(metadata["clusterId"], &clusterID) != nil {
		return ""
	}
	return strings.TrimSpace(clusterID)
}

func (a *API) startMySQLRestore(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	payload, ok := decodeMySQLRestoreRequest(w, r)
	if !ok {
		return
	}
	instanceID := strings.TrimSpace(chi.URLParam(r, "id"))
	instance, err := a.store.GetAppInstance(instanceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	topology := appInstanceTopology(instance)
	if instance.App != "mysql" || (topology != "standalone" && topology != "innodb-cluster") || strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, mysqlapp.MySQLBackupStandaloneRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupStandaloneRequired), map[string]any{"instanceId": instanceID})
		return
	}
	if payload.Mode == "disaster-rebuild" {
		if topology != "innodb-cluster" {
			writeError(w, http.StatusBadRequest, mysqlapp.MySQLRebuildConfirmationRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLRebuildConfirmationRequired), map[string]any{"instanceId": instance.ID})
			return
		}
		if code := a.mysqlMaintenanceGate(instance); code != mysqlapp.MySQLMaintenanceRequired {
			if code == "" {
				code = mysqlapp.MySQLMaintenanceRequired
			}
			writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instance.ID})
			return
		}
	} else {
		if payload.Mode != topology {
			writeError(w, http.StatusBadRequest, mysqlapp.MySQLRestoreManifestInvalid, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLRestoreManifestInvalid), map[string]any{"instanceId": instance.ID})
			return
		}
		var gateCode string
		if payload.ResumeMaintenance {
			gateCode = a.mysqlStandaloneMaintenanceResumeGate(instance, payload.BackupID)
		} else {
			gateCode = a.mysqlOrdinaryLifecycleGate(instance)
		}
		if code := gateCode; code != "" {
			writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instance.ID})
			return
		}
	}
	backup, err := a.store.GetAppBackup(payload.BackupID)
	if err != nil || backup.App != "mysql" || backup.BackupType != "logical-full" || backup.Status != "success" || (topology == "standalone" && (backup.InstanceID != instance.ID || backup.ServerID != instance.ServerID)) || (topology == "innodb-cluster" && mysqlClusterIDFromBackup(backup) != mysqlClusterID(instance)) {
		writeError(w, http.StatusConflict, mysqlapp.MySQLBackupVerifyNotAllowed, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupVerifyNotAllowed), map[string]any{"backupId": payload.BackupID})
		return
	}
	instances, servers, err := a.mysqlBackupTargets(instance)
	if err != nil {
		respond(w, nil, err)
		return
	}
	targetMapping := map[string]any{}
	serverPasswordsConfirmed := false
	if payload.Mode == "disaster-rebuild" {
		if code := a.validateDisasterRebuildTargets(instances, servers, payload.TargetMapping, payload.ServerPasswords); code != "" {
			status := http.StatusBadRequest
			if code == "SERVER_PASSWORD_INVALID" {
				status = http.StatusForbidden
			} else if code == "SERVER_PASSWORD_NOT_CONFIGURED" {
				status = http.StatusConflict
			}
			message := i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLRebuildConfirmationRequired)
			if strings.HasPrefix(code, "SERVER_PASSWORD_") {
				key := "api.serverPasswordRequired"
				if code == "SERVER_PASSWORD_INVALID" {
					key = "api.serverPasswordInvalid"
				}
				if code == "SERVER_PASSWORD_NOT_CONFIGURED" {
					key = "api.serverPasswordNotConfigured"
				}
				message = i18n.Text(lang, key)
			}
			writeError(w, status, code, message, map[string]any{"instanceId": instance.ID})
			return
		}
		for instanceID, serverID := range payload.TargetMapping {
			targetMapping[instanceID] = serverID
		}
		serverPasswordsConfirmed = true
	}
	module, exists := a.apps.Get("mysql")
	if !exists {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": "mysql"})
		return
	}
	restoreModule, supportsRestore := module.(registry.RestoreModule)
	if !supportsRestore {
		writeError(w, http.StatusConflict, "MYSQL_RESTORE_UNSUPPORTED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLRestoreIncomplete), map[string]any{"instanceId": instance.ID})
		return
	}
	actor := currentUser(r).Username
	restoreRequest := registry.RestoreRequest{
		Instance: instance, Instances: instances, Servers: servers, Backup: backup,
		Language: lang, Actor: actor, RepositoryDir: a.cfg.MySQLBackupDir,
		Parameters: map[string]any{
			"mode": payload.Mode, "maintenanceConfirmed": payload.MaintenanceConfirmed,
			"createPreRestoreBackup": payload.CreatePreRestoreBackup, "disasterConfirmed": payload.DisasterConfirmed, "threads": payload.Threads,
			"resumeMaintenance": payload.ResumeMaintenance,
			"targetMapping":     targetMapping, "serverPasswordsConfirmed": serverPasswordsConfirmed,
		},
	}
	plan, err := restoreModule.PlanRestore(r.Context(), restoreRequest)
	if err != nil {
		var stable interface{ StableCode() string }
		if errors.As(err, &stable) && stable.StableCode() != "" {
			code := stable.StableCode()
			writeError(w, http.StatusBadRequest, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instance.ID, "backupId": backup.ID})
			return
		}
		writeError(w, http.StatusBadRequest, "MYSQL_RESTORE_PLAN_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLRestoreIncomplete), map[string]any{"instanceId": instance.ID})
		return
	}
	task, err := a.store.CreateTask(store.Task{Type: mysqlRestoreTaskType, Target: instance.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_RESTORE_PLAN_STORE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLRestoreIncomplete), map[string]any{"instanceId": instance.ID})
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, mysqlClusterOperationLockSpecs("mysql-restore", instance))
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		return restoreModule.Restore(ctx, restoreRequest.Clone(), registry.RunContext{
			TaskID: log.TaskID(), Log: log, TargetLog: func(target string) registry.Logger { return log.Target(target) },
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
		})
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	}
	if err == nil {
		a.audit(r, mysqlRestoreTaskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) validateDisasterRebuildTargets(instances []store.AppInstance, servers []store.Server, mapping, passwords map[string]string) string {
	if len(instances) != 3 || len(servers) != 3 || len(mapping) != 3 || len(passwords) != 3 {
		return mysqlapp.MySQLRebuildConfirmationRequired
	}
	serverByID := make(map[string]store.Server, len(servers))
	for _, server := range servers {
		serverByID[server.ID] = server
	}
	seenServers := map[string]bool{}
	for _, instance := range instances {
		serverID, present := mapping[instance.ID]
		if !present || serverID != instance.ServerID || seenServers[serverID] || serverByID[serverID].ID == "" {
			return mysqlapp.MySQLRebuildConfirmationRequired
		}
		seenServers[serverID] = true
		confirmed, present := passwords[serverID]
		if !present || strings.TrimSpace(confirmed) == "" {
			return "SERVER_PASSWORD_REQUIRED"
		}
		stored, err := a.store.GetServer(serverID, true)
		if err != nil || strings.TrimSpace(stored.Password) == "" {
			return "SERVER_PASSWORD_NOT_CONFIGURED"
		}
		if strings.TrimSpace(confirmed) != strings.TrimSpace(stored.Password) {
			return "SERVER_PASSWORD_INVALID"
		}
	}
	return ""
}

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
	unique := make([]store.AppBackup, 0, len(backups))
	seenBackupIDs := map[string]bool{}
	for _, backup := range backups {
		if !seenBackupIDs[backup.ID] {
			seenBackupIDs[backup.ID] = true
			unique = append(unique, backup)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"instanceId": instance.ID,
		"items":      unique,
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
	if appInstanceTopology(instance) != "standalone" && appInstanceTopology(instance) != "innodb-cluster" {
		writeError(w, http.StatusBadRequest, "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY", i18n.MySQLBackupErrorText(lang, "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY"), map[string]any{"instanceId": instanceID})
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": instanceID})
		return
	}
	if code := a.mysqlOrdinaryLifecycleGate(instance); code != "" {
		writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instance.ID})
		return
	}
	instances, servers, err := a.mysqlBackupTargets(instance)
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
		Instances:     instances,
		Servers:       servers,
		Language:      lang,
		Actor:         actor,
		RepositoryDir: a.cfg.MySQLBackupDir,
		KeepLast:      keepLast,
		Parameters:    map[string]any{"name": payload.Name, "threads": payload.Threads, "maxRateMBps": payload.MaxRateMBps, "schemas": append([]string(nil), payload.Schemas...)},
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

func (a *API) listMySQLBackupSchemas(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	instanceID := strings.TrimSpace(chi.URLParam(r, "id"))
	instance, err := a.store.GetAppInstance(instanceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	topology := appInstanceTopology(instance)
	if instance.App != "mysql" || (topology != "standalone" && topology != "innodb-cluster") || strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, mysqlapp.MySQLBackupUnsupportedTopology, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupUnsupportedTopology), map[string]any{"instanceId": instanceID})
		return
	}
	if code := a.mysqlOrdinaryLifecycleGate(instance); code != "" {
		writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instanceID})
		return
	}
	instances, servers, err := a.mysqlBackupTargets(instance)
	if err != nil {
		respond(w, nil, err)
		return
	}
	module, exists := a.apps.Get("mysql")
	if !exists {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": "mysql"})
		return
	}
	discovery, ok := module.(registry.BackupSchemaModule)
	if !ok {
		writeError(w, http.StatusConflict, "MYSQL_BACKUP_SCHEMA_DISCOVERY_UNSUPPORTED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupSchemaSelectionInvalid), map[string]any{"instanceId": instanceID})
		return
	}
	catalog, err := discovery.DiscoverBackupSchemas(r.Context(), registry.BackupRequest{
		Instance: instance, Instances: instances, Servers: servers, Language: lang, Actor: currentUser(r).Username,
	})
	if err != nil {
		var stable interface{ StableCode() string }
		if errors.As(err, &stable) && stable.StableCode() != "" {
			code := stable.StableCode()
			writeError(w, http.StatusBadRequest, code, i18n.MySQLBackupErrorText(lang, code), map[string]any{"instanceId": instanceID})
			return
		}
		writeError(w, http.StatusBadGateway, "MYSQL_BACKUP_SCHEMA_DISCOVERY_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupSchemaSelectionInvalid), map[string]any{"instanceId": instanceID})
		return
	}
	writeJSON(w, http.StatusOK, catalog)
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
	topology := appInstanceTopology(instance)
	if instance.App != "mysql" || instance.ID != backup.InstanceID || instance.ServerID != backup.ServerID || backup.BackupType != "logical-full" || (topology != "standalone" && topology != "innodb-cluster") {
		writeError(w, http.StatusConflict, mysqlapp.MySQLBackupVerifyNotAllowed, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupVerifyNotAllowed), map[string]any{"backupId": backupID})
		return
	}
	lockSpecs := appInstanceOperationLockSpecs("mysql-backup-verify", []store.AppInstance{instance})
	if topology == "innodb-cluster" {
		clusterID := mysqlClusterID(instance)
		instances, _, targetErr := a.mysqlBackupTargets(instance)
		if targetErr != nil || len(instances) != 3 || clusterID == "" || mysqlClusterIDFromBackup(backup) != clusterID {
			writeError(w, http.StatusConflict, mysqlapp.MySQLBackupClusterUnhealthy, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLBackupClusterUnhealthy), map[string]any{"backupId": backupID})
			return
		}
		lockSpecs = mysqlClusterOperationLockSpecs("mysql-backup-verify", instance)
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
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, lockSpecs)
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
	freshInstance, err := a.store.GetAppInstance(instance.ID)
	if err != nil || !sameStandaloneMySQLBackupOwner(instance, freshInstance, backup) {
		fail(http.StatusConflict, "MYSQL_BACKUP_DELETE_NOT_ALLOWED")
		return
	}
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

func sameStandaloneMySQLBackupOwner(expected, current store.AppInstance, backup store.AppBackup) bool {
	return current.ID == expected.ID && current.ID == backup.InstanceID && current.App == "mysql" &&
		strings.ToLower(strings.TrimSpace(current.Topology)) == "standalone" && current.ServerID == expected.ServerID && current.ServerID == backup.ServerID
}
