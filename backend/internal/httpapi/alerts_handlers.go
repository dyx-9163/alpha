package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

type alertMuteRequest struct {
	MutedUntil string `json:"mutedUntil"`
	Minutes    int    `json:"minutes"`
}

type alertResolveRequest struct {
	Message string `json:"message"`
}

func (a *API) listAlerts(w http.ResponseWriter, r *http.Request) {
	status := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("status")))
	if status == "" {
		status = "open"
	}
	alerts, err := a.store.ListAlerts(store.AlertQuery{
		Status:   status,
		Severity: r.URL.Query().Get("severity"),
		Scope:    r.URL.Query().Get("scope"),
	})
	if err != nil {
		respond(w, nil, err)
		return
	}
	user := currentUser(r)
	out := make([]map[string]any, 0, len(alerts))
	for _, alert := range alerts {
		if a.canViewAlert(user.Role, alert) {
			out = append(out, alertResponse(alert))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": out})
}

func (a *API) ackAlert(w http.ResponseWriter, r *http.Request) {
	alert, ok := a.visibleAlertForRequest(w, r)
	if !ok {
		return
	}
	saved, err := a.store.AcknowledgeAlert(alert.ID, currentUser(r).Username)
	if err != nil {
		respond(w, nil, err)
		return
	}
	a.publishAlert("alert.updated", saved)
	a.audit(r, "alerts.acknowledge", saved.ID, "success", saved.Fingerprint)
	writeJSON(w, http.StatusOK, alertResponse(saved))
}

func (a *API) muteAlert(w http.ResponseWriter, r *http.Request) {
	alert, ok := a.visibleAlertForRequest(w, r)
	if !ok {
		return
	}
	var req alertMuteRequest
	if !decode(w, r, &req) {
		return
	}
	until, err := muteUntil(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_MUTE_UNTIL", err.Error(), nil)
		return
	}
	saved, err := a.store.MuteAlert(alert.ID, currentUser(r).Username, until)
	if err != nil {
		respond(w, nil, err)
		return
	}
	a.publishAlert("alert.updated", saved)
	a.audit(r, "alerts.mute", saved.ID, "success", saved.Fingerprint)
	writeJSON(w, http.StatusOK, alertResponse(saved))
}

func (a *API) resolveAlert(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	var req alertResolveRequest
	if !decode(w, r, &req) {
		return
	}
	saved, err := a.store.ResolveAlert(id, currentUser(r).Username, req.Message)
	if err != nil {
		respond(w, nil, err)
		return
	}
	a.publishAlert("alert.resolved", saved)
	a.audit(r, "alerts.resolve", saved.ID, "success", saved.Fingerprint)
	writeJSON(w, http.StatusOK, alertResponse(saved))
}

func (a *API) visibleAlertForRequest(w http.ResponseWriter, r *http.Request) (store.Alert, bool) {
	alert, err := a.store.GetAlert(strings.TrimSpace(chi.URLParam(r, "id")))
	if err != nil {
		respond(w, nil, err)
		return store.Alert{}, false
	}
	if !a.canViewAlert(currentUser(r).Role, alert) {
		writeError(w, http.StatusForbidden, "FORBIDDEN", i18n.Text(languageFromRequest(r), "api.permissionDenied"), map[string]any{"permission": alert.RequiredPermission})
		return store.Alert{}, false
	}
	return alert, true
}

func (a *API) canViewAlert(role string, alert store.Alert) bool {
	if rbac.Allows(role, rbac.AlertsManage) {
		return true
	}
	if !rbac.Allows(role, rbac.AlertsView) {
		return false
	}
	if strings.TrimSpace(alert.RequiredPermission) == "" {
		return true
	}
	return rbac.Allows(role, rbac.Permission(alert.RequiredPermission))
}

func (a *API) publishAlert(eventType string, alert store.Alert) {
	if a.realtime == nil {
		return
	}
	a.realtime.Publish(realtime.Event{
		Type:       eventType,
		Resource:   "alert",
		ResourceID: alert.ID,
		ServerID:   alert.ServerID,
		InstanceID: alert.InstanceID,
		Status:     alert.Status,
		Payload:    map[string]any{"alert": alertResponse(alert)},
	})
}

func muteUntil(req alertMuteRequest) (time.Time, error) {
	if strings.TrimSpace(req.MutedUntil) != "" {
		return time.Parse(time.RFC3339, strings.TrimSpace(req.MutedUntil))
	}
	minutes := req.Minutes
	if minutes == 0 {
		minutes = 60
	}
	if minutes > 24*60 {
		minutes = 24 * 60
	}
	if minutes < 0 {
		minutes = 0
	}
	return time.Now().Add(time.Duration(minutes) * time.Minute), nil
}

func alertResponse(alert store.Alert) map[string]any {
	evidence := map[string]any{}
	if strings.TrimSpace(alert.EvidenceJSON) != "" {
		_ = json.Unmarshal([]byte(alert.EvidenceJSON), &evidence)
	}
	return map[string]any{
		"id":                 alert.ID,
		"fingerprint":        alert.Fingerprint,
		"severity":           alert.Severity,
		"scope":              alert.Scope,
		"resourceId":         alert.ResourceID,
		"serverId":           alert.ServerID,
		"app":                alert.App,
		"instanceId":         alert.InstanceID,
		"status":             alert.Status,
		"title":              alert.Title,
		"message":            alert.Message,
		"evidence":           evidence,
		"evidenceJson":       alert.EvidenceJSON,
		"requiredPermission": alert.RequiredPermission,
		"firstSeenAt":        alert.FirstSeenAt,
		"lastSeenAt":         alert.LastSeenAt,
		"resolvedAt":         alert.ResolvedAt,
		"mutedUntil":         alert.MutedUntil,
		"acknowledgedBy":     alert.AcknowledgedBy,
		"acknowledgedAt":     alert.AcknowledgedAt,
		"updatedAt":          alert.UpdatedAt,
	}
}
