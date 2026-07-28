package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

const mysqlMaintenanceClearTaskType = "apps.mysql.maintenance.clear"

func (a *API) clearMySQLMaintenance(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var payload struct {
		RecoveryConfirmed bool `json:"recoveryConfirmed"`
	}
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil || !payload.RecoveryConfirmed {
		writeError(w, http.StatusBadRequest, mysqlapp.MySQLMaintenanceRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLMaintenanceRequired), nil)
		return
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		writeError(w, http.StatusBadRequest, mysqlapp.MySQLMaintenanceRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLMaintenanceRequired), nil)
		return
	}
	instance, err := a.store.GetAppInstance(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		respond(w, nil, err)
		return
	}
	if instance.App != "mysql" {
		writeError(w, http.StatusBadRequest, "MYSQL_INSTANCE_REQUIRED", i18n.Text(lang, "api.mysqlClusterRequired"), nil)
		return
	}
	if strings.EqualFold(strings.TrimSpace(instance.Topology), "innodb-cluster") && mysqlClusterID(instance) == "" {
		writeError(w, http.StatusConflict, mysqlapp.MySQLMaintenanceStateInvalid, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLMaintenanceStateInvalid), map[string]any{"instanceId": instance.ID})
		return
	}
	module, ok := a.apps.Get("mysql")
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), nil)
		return
	}
	clearer, ok := module.(interface {
		ClearMaintenance(context.Context, store.AppInstance, string, string, mysqlapp.Logger) error
	})
	if !ok {
		writeError(w, http.StatusConflict, mysqlapp.MySQLMaintenanceStateInvalid, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLMaintenanceStateInvalid), nil)
		return
	}
	actor := currentUser(r).Username
	target := instance.ServerID
	if strings.EqualFold(strings.TrimSpace(instance.Topology), "innodb-cluster") {
		target = mysqlClusterID(instance)
	}
	task, err := a.store.CreateTask(store.Task{Type: mysqlMaintenanceClearTaskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	plan := []registry.InstallStepPlan{{Target: target, Name: "clear-maintenance", Title: "clear MySQL maintenance state", Order: 1}}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_MAINTENANCE_PLAN_STORE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLMaintenanceStatePersistFailed), nil)
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, mysqlClusterOperationLockSpecs("mysql-maintenance-clear", instance))
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.StartTarget(target)
		log.StartStep(target, "clear-maintenance", "clear MySQL maintenance state", 1)
		err := clearer.ClearMaintenance(ctx, instance, lang, log.TaskID(), log)
		if err != nil {
			log.FinishStep(target, "clear-maintenance", "failed", err.Error())
			log.FinishTarget(target, "failed", err.Error())
			return err
		}
		log.FinishStep(target, "clear-maintenance", "success", "")
		log.FinishTarget(target, "success", "")
		return nil
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	} else {
		a.audit(r, mysqlMaintenanceClearTaskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}
