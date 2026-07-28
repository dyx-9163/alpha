package httpapi

import (
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskplan"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

type aifarRuntimeController struct {
	*API
	startExistingTask         func(store.Task, string, worker.Job) (store.Task, error)
	storeDiagnosticTaskPlan   func(string, string, []simpleTaskStep) error
	deleteDiagnosticTask      func(string) error
	terminalizeDiagnosticTask func(string, string, string) error
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

func (a *aifarRuntimeController) storeDiagnosticPlan(taskID, target string, steps []simpleTaskStep) error {
	if a.storeDiagnosticTaskPlan != nil {
		return a.storeDiagnosticTaskPlan(taskID, target, steps)
	}
	return taskplan.StorePlan(a.store, taskID, simpleTaskPlan(target, steps))
}

func (a *aifarRuntimeController) deleteDiagnosticTaskByID(taskID string) error {
	if a.deleteDiagnosticTask != nil {
		return a.deleteDiagnosticTask(taskID)
	}
	return a.store.DeleteTask(taskID)
}

func (a *aifarRuntimeController) terminalizeDiagnosticTaskByID(taskID, status, errText string) error {
	if a.terminalizeDiagnosticTask != nil {
		return a.terminalizeDiagnosticTask(taskID, status, errText)
	}
	return a.store.UpdateTaskStatus(taskID, status, errText)
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
	r.Post("/containers/aifar/services/batch-offline", a.requirePermission(rbac.AppsManage, a.batchOfflineServices))
	r.Post("/containers/aifar/services/{service}/scale-out", a.requirePermission(rbac.AppsManage, a.scaleOut))
	r.Post("/containers/aifar/services/{service}/scale-in", a.requirePermission(rbac.AppsManage, a.scaleIn))
	r.Post("/containers/aifar/services/{service}/offline", a.requirePermission(rbac.AppsManage, a.offlineService))
}
