package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
)

func (a *API) listAudit(w http.ResponseWriter, r *http.Request) {
	out, err := a.store.ListAuditPage(store.AuditQuery{
		Page:     queryInt(r, "page", 1),
		PageSize: queryInt(r, "pageSize", 20),
		Module:   strings.TrimSpace(r.URL.Query().Get("module")),
		Status:   strings.TrimSpace(r.URL.Query().Get("status")),
	})
	respond(w, out, err)
}

func (a *API) deleteAudit(w http.ResponseWriter, r *http.Request) {
	var req deleteAuditRequest
	if !decode(w, r, &req) {
		return
	}
	if len(req.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "IDS_REQUIRED", i18n.Text(languageFromRequest(r), "api.idsRequired"), nil)
		return
	}
	deleted, err := a.store.DeleteAuditLogs(req.IDs)
	if err == nil {
		a.audit(r, "audit.delete.batch", strconv.Itoa(deleted), "success", i18n.Text(languageFromRequest(r), "api.auditDeleted", deleted))
	}
	respond(w, map[string]any{"deleted": deleted}, err)
}
