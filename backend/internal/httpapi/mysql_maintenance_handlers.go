package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	mysqlapp "aifar-deployment/backend/internal/apps/mysql"
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
	task, err := a.store.CreateTask(store.Task{Type: mysqlMaintenanceClearTaskType, Target: instance.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, mysqlClusterOperationLockSpecs("mysql-maintenance-clear", instance))
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		return clearer.ClearMaintenance(ctx, instance, lang, log.TaskID(), log)
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	} else {
		a.audit(r, mysqlMaintenanceClearTaskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}
