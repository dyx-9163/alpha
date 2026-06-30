package httpapi

import (
	"net/http"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/resource"
)

func (a *API) listResources(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListResources()
	respond(w, out, err)
}

func (a *API) rescanResources(w http.ResponseWriter, r *http.Request) {
	if err := resource.ScanAndSave(a.store, a.cfg.ResourceDir); err != nil {
		writeError(w, http.StatusInternalServerError, "RESOURCE_SCAN_FAILED", err.Error(), nil)
		return
	}
	a.audit(r, "resources.scan", a.cfg.ResourceDir, "success", i18n.Text(languageFromRequest(r), "api.resourceScanCompleted"))
	a.listResources(w, r)
}
