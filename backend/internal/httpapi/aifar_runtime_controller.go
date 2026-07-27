package httpapi

import (
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

type aifarRuntimeController struct {
	*API
	startExistingTask       func(store.Task, string, worker.Job) (store.Task, error)
	storeDiagnosticTaskPlan func(string, string, []simpleTaskStep) error
}

func newAIFARRuntimeController(api *API) *aifarRuntimeController {
	return &aifarRuntimeController{API: api}
}

func (a *aifarRuntimeController) startExistingWithLanguage(task store.Task, lang string, job worker.Job) (store.Task, error) {
	if a.startExistingTask != nil {
		return a.startExistingTask(task, lang, job)
	}
	return a.tasks.StartExistingWithLanguage(task, lang, job)
}

func (a *aifarRuntimeController) storeDiagnosticPlanOrDelete(taskID, target string, steps []simpleTaskStep) error {
	if a.storeDiagnosticTaskPlan != nil {
		if err := a.storeDiagnosticTaskPlan(taskID, target, steps); err != nil {
			_ = a.store.DeleteTask(taskID)
			return err
		}
		return nil
	}
	return a.storeTaskPlanOrDelete(taskID, simpleTaskPlan(target, steps))
}

func (a *aifarRuntimeController) mount(r chi.Router) {
	r.Get("/containers/aifar/runtime", a.runtime)
	r.Get("/containers/aifar/runtime/logs", a.logs)
	r.Get("/containers/aifar/runtime/logs/events", a.logEvents)
	r.Put("/containers/aifar/runtime/config", a.requirePermission(rbac.AppsManage, a.configure))
	r.Post("/containers/aifar/runtime/reconcile", a.requirePermission(rbac.AppsManage, a.reconcile))
	r.Post("/containers/aifar/runtime/restart-all", a.requirePermission(rbac.AppsManage, a.restartAll))
	r.Post("/containers/aifar/runtime/cleanup-stale", a.requirePermission(rbac.AppsManage, a.cleanupStale))
	r.Post("/containers/aifar/runtime/uninstall-agent", a.requirePermission(rbac.AppsManage, a.uninstallAgent))
	r.Post("/containers/aifar/runtime/diagnostics/estimate", a.requirePermission(rbac.AppsManage, a.estimateDiagnostics))
	r.Post("/containers/aifar/runtime/diagnostics/exports", a.requirePermission(rbac.AppsManage, a.createDiagnosticExport))
	r.Get("/containers/aifar/runtime/diagnostics/exports", a.requirePermission(rbac.AppsManage, a.listDiagnosticExports))
	r.Get("/containers/aifar/runtime/diagnostics/exports/{id}/download", a.requirePermission(rbac.AppsManage, a.downloadDiagnosticExport))
	r.Delete("/containers/aifar/runtime/diagnostics/exports/{id}", a.requirePermission(rbac.AppsManage, a.deleteDiagnosticExport))
	r.Post("/containers/aifar/services/install", a.requirePermission(rbac.AppsManage, a.installServices))
	r.Post("/containers/aifar/services/{service}/scale-out", a.requirePermission(rbac.AppsManage, a.scaleOut))
	r.Post("/containers/aifar/services/{service}/scale-in", a.requirePermission(rbac.AppsManage, a.scaleIn))
	r.Post("/containers/aifar/services/{service}/offline", a.requirePermission(rbac.AppsManage, a.offlineService))
}
