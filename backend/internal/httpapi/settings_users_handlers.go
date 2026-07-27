package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"

	"github.com/go-chi/chi/v5"
)

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"language":                 a.store.GetSetting("language", "zh"),
		"deploymentConcurrency":    a.store.GetSetting("deploymentConcurrency", fmt.Sprintf("%d", a.cfg.DeploymentConcurrency)),
		"providerStatus":           "real",
		"providerMode":             a.cfg.ProviderMode,
		"databasePath":             a.cfg.DatabasePath,
		"databaseBackupDir":        a.cfg.DatabaseBackupDir,
		"mysqlBackupDir":           a.cfg.MySQLBackupDir,
		"mysqlBackupKeepLast":      a.cfg.MySQLBackupKeepLast,
		"resourcePath":             a.cfg.ResourceDir,
		"staticPath":               a.cfg.StaticDir,
		"defaultDeployDir":         a.cfg.DefaultDeployDir,
		"authMaxFailures":          a.cfg.AuthMaxFailures,
		"authLockoutSeconds":       a.cfg.AuthLockoutSeconds,
		"maxRequestBodyBytes":      a.cfg.MaxRequestBodyBytes,
		"auditRetentionDays":       a.cfg.AuditRetentionDays,
		"taskRetentionDays":        a.cfg.TaskRetentionDays,
		"collectorIntervalSeconds": a.cfg.CollectorIntervalSecs,
		"moduleStatus": map[string]string{
			"servers": "available", "apps": "available", "containers": "available",
			"database": "available", "storage": "available", "terminal": "available",
			"audit": "available", "settings": "available", "maintenance": "available", "users": "available",
		},
	})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req map[string]any
	if !decode(w, r, &req) {
		return
	}
	for _, key := range []string{"language", "deploymentConcurrency"} {
		if value, ok := req[key]; ok {
			if key == "deploymentConcurrency" {
				_ = a.store.SetSetting(key, fmt.Sprintf("%d", store.NormalizeDeploymentConcurrency(fmt.Sprint(value), a.cfg.DeploymentConcurrency)))
				continue
			}
			_ = a.store.SetSetting(key, fmt.Sprint(value))
		}
	}
	a.audit(r, "settings.update", "panel", "success", i18n.Text(lang, "api.settingsUpdated"))
	a.getSettings(w, r)
}

func (a *API) listUsers(w http.ResponseWriter, r *http.Request) {
	users, err := a.store.ListUsers()
	respond(w, map[string]any{"items": users}, err)
}

func (a *API) createUser(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	role := rbac.NormalizeRole(req.Role)
	if username == "" || req.Password == "" || role == "" {
		writeError(w, http.StatusBadRequest, "INVALID_USER", i18n.Text(lang, "api.invalidUser"), nil)
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "PASSWORD_TOO_SHORT", i18n.Text(lang, "api.passwordTooShort"), nil)
		return
	}
	if !rbac.ValidRole(role) {
		writeError(w, http.StatusBadRequest, "INVALID_USER_ROLE", i18n.Text(lang, "api.invalidUserRole"), map[string]any{"role": role})
		return
	}
	if _, err := a.store.UserByUsername(username); err == nil {
		writeError(w, http.StatusConflict, "USER_EXISTS", i18n.Text(lang, "api.userAlreadyExists"), map[string]any{"username": username})
		return
	}
	user, err := a.store.CreateUser(username, req.Password, role)
	if err != nil {
		respond(w, nil, err)
		return
	}
	a.audit(r, "users.create", username, "success", i18n.Text(lang, "api.userCreated"))
	writeJSON(w, http.StatusCreated, map[string]any{"user": user})
}

func (a *API) updateUserRole(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	username := strings.TrimSpace(chi.URLParam(r, "username"))
	var req struct {
		Role string `json:"role"`
	}
	if !decode(w, r, &req) {
		return
	}
	role := rbac.NormalizeRole(req.Role)
	if username == "" || role == "" {
		writeError(w, http.StatusBadRequest, "INVALID_USER", i18n.Text(lang, "api.invalidUser"), nil)
		return
	}
	if !rbac.ValidRole(role) {
		writeError(w, http.StatusBadRequest, "INVALID_USER_ROLE", i18n.Text(lang, "api.invalidUserRole"), map[string]any{"role": role})
		return
	}
	current, err := a.store.UserByUsername(username)
	if err != nil {
		writeError(w, http.StatusNotFound, "USER_NOT_FOUND", i18n.Text(lang, "api.userNotFound"), map[string]any{"username": username})
		return
	}
	if rbac.NormalizeRole(current.Role) == "owner" && role != "owner" {
		owners, err := a.store.CountUsersByRole("owner")
		if err != nil {
			respond(w, nil, err)
			return
		}
		if owners <= 1 {
			writeError(w, http.StatusBadRequest, "LAST_OWNER_REQUIRED", i18n.Text(lang, "api.lastOwnerRequired"), nil)
			return
		}
	}
	if err := a.store.SetUserRole(username, role); err != nil {
		respond(w, nil, err)
		return
	}
	user, err := a.store.UserByUsername(username)
	if err != nil {
		respond(w, nil, err)
		return
	}
	a.audit(r, "users.role.update", username, "success", i18n.Text(lang, "api.userRoleUpdated"))
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}

func (a *API) resetUserPassword(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	username := strings.TrimSpace(chi.URLParam(r, "username"))
	var req struct {
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	if username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "INVALID_USER", i18n.Text(lang, "api.invalidUser"), nil)
		return
	}
	if len(req.Password) < 8 {
		writeError(w, http.StatusBadRequest, "PASSWORD_TOO_SHORT", i18n.Text(lang, "api.passwordTooShort"), nil)
		return
	}
	if _, err := a.store.UserByUsername(username); err != nil {
		writeError(w, http.StatusNotFound, "USER_NOT_FOUND", i18n.Text(lang, "api.userNotFound"), map[string]any{"username": username})
		return
	}
	if err := a.store.ResetUserPassword(username, req.Password); err != nil {
		respond(w, nil, err)
		return
	}
	user, err := a.store.UserByUsername(username)
	if err != nil {
		respond(w, nil, err)
		return
	}
	a.audit(r, "users.password.reset", username, "success", i18n.Text(lang, "api.userPasswordReset"))
	writeJSON(w, http.StatusOK, map[string]any{"user": user})
}
