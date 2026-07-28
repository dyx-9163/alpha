package httpapi

import (
	"context"
	"encoding/json"
	"errors"
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

const mysqlReconciliationTaskType = "apps.mysql.reconciliation.run"

func (a *API) runMySQLReconciliation(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	if err := decodeExactReconciliationConfirmation(r); err != nil {
		writeError(w, http.StatusBadRequest, mysqlapp.MySQLReconciliationConfirmationRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationConfirmationRequired), nil)
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
	plan, planErr := mysqlapp.BuildReconciliationPlan(a.store, instance)
	if planErr != nil {
		writeError(w, http.StatusConflict, mysqlapp.MySQLReconciliationRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationRequired), nil)
		return
	}
	instance = plan.Instance
	if strings.EqualFold(strings.TrimSpace(instance.Topology), "innodb-cluster") && !validMySQLMaintenanceClusterID(mysqlClusterID(instance)) {
		writeError(w, http.StatusConflict, mysqlapp.MySQLReconciliationRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationRequired), nil)
		return
	}
	present, markerErr := mysqlapp.ReconciliationMarkerState(instance.Metadata)
	if markerErr != nil {
		writeError(w, http.StatusConflict, mysqlapp.MySQLReconciliationRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationRequired), nil)
		return
	}
	if !present {
		writeError(w, http.StatusConflict, mysqlapp.MySQLReconciliationNotRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationNotRequired), nil)
		return
	}
	module, ok := a.apps.Get("mysql")
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), nil)
		return
	}
	reconciler, ok := module.(interface {
		Reconcile(context.Context, mysqlapp.ReconciliationPlan, string, string, mysqlapp.Logger) error
	})
	if !ok {
		writeError(w, http.StatusConflict, mysqlapp.MySQLReconciliationRequired, i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationRequired), nil)
		return
	}
	actor := currentUser(r).Username
	target := instance.ServerID
	if strings.EqualFold(strings.TrimSpace(instance.Topology), "innodb-cluster") {
		target = mysqlClusterID(instance)
	}
	task, err := a.store.CreateTask(store.Task{Type: mysqlReconciliationTaskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	stepTitle := i18n.Text(lang, "mysql.reconciliation.step")
	taskPlan := []registry.InstallStepPlan{{Target: target, Name: "reconcile-local-infile", Title: stepTitle, Order: 1}}
	if err := a.storeInstallPlanOrDelete(task.ID, taskPlan); err != nil {
		writeError(w, http.StatusInternalServerError, "MYSQL_RECONCILIATION_PLAN_STORE_FAILED", i18n.MySQLBackupErrorText(lang, mysqlapp.MySQLReconciliationRequired), nil)
		return
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, mysqlClusterOperationLockSpecs("mysql-reconciliation", instance))
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.StartTarget(target)
		log.StartStep(target, "reconcile-local-infile", stepTitle, 1)
		log.Info("%s", i18n.Text(lang, "mysql.reconciliation.started"))
		runErr := reconciler.Reconcile(ctx, plan, lang, log.TaskID(), log)
		if runErr != nil {
			log.FinishStep(target, "reconcile-local-infile", "failed", runErr.Error())
			log.FinishTarget(target, "failed", runErr.Error())
			return runErr
		}
		log.Info("%s", i18n.Text(lang, "mysql.reconciliation.completed"))
		log.FinishStep(target, "reconcile-local-infile", "success", "")
		log.FinishTarget(target, "success", "")
		return nil
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	} else {
		a.audit(r, mysqlReconciliationTaskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func decodeExactReconciliationConfirmation(r *http.Request) error {
	decoder := json.NewDecoder(r.Body)
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("invalid reconciliation confirmation")
	}
	seen := false
	confirmed := false
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		key, ok := keyToken.(string)
		if keyErr != nil || !ok || key != "reconciliationConfirmed" || seen {
			return errors.New("invalid reconciliation confirmation")
		}
		seen = true
		if err := decoder.Decode(&confirmed); err != nil {
			return errors.New("invalid reconciliation confirmation")
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || !seen || !confirmed {
		return errors.New("invalid reconciliation confirmation")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("invalid reconciliation confirmation")
	}
	return nil
}
