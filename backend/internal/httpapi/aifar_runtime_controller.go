package httpapi

import (
	"aifar-deployment/backend/internal/rbac"

	"github.com/go-chi/chi/v5"
)

type aifarRuntimeController struct {
	*API
}

func newAIFARRuntimeController(api *API) *aifarRuntimeController {
	return &aifarRuntimeController{API: api}
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
	r.Post("/containers/aifar/services/install", a.requirePermission(rbac.AppsManage, a.installServices))
	r.Post("/containers/aifar/services/{service}/scale-out", a.requirePermission(rbac.AppsManage, a.scaleOut))
	r.Post("/containers/aifar/services/{service}/scale-in", a.requirePermission(rbac.AppsManage, a.scaleIn))
	r.Post("/containers/aifar/services/{service}/offline", a.requirePermission(rbac.AppsManage, a.offlineService))
}
