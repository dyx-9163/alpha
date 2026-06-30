package httpapi

import (
	"context"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/auditkit"
	"aifar-deployment/backend/internal/auth"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
)

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &req) {
		return
	}
	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = "unknown"
	}
	guardKey := loginGuardKey(r, username)
	if lockedUntil, locked := a.auth.LockedUntil(guardKey); locked {
		a.auditLoginLocked(lang, username, lockedUntil)
		writeAuthLocked(w, r, lockedUntil)
		return
	}
	u, err := a.store.UserByUsername(req.Username)
	if err != nil || auth.CheckPassword(u.PasswordHash, req.Password) != nil {
		_ = auditkit.Record(a.store, auditkit.Event{
			Actor:   username,
			Action:  "auth.login",
			Target:  username,
			Status:  "failed",
			Message: i18n.Text(lang, "api.authFailed"),
		})
		if lockedUntil, locked := a.auth.RecordFailure(guardKey); locked {
			a.auditLoginLocked(lang, username, lockedUntil)
			writeAuthLocked(w, r, lockedUntil)
			return
		}
		writeError(w, http.StatusUnauthorized, "AUTH_FAILED", i18n.Text(lang, "api.authFailed"), nil)
		return
	}
	a.auth.RecordSuccess(guardKey)
	token, err := auth.IssueToken(a.cfg.JWTSecret, u)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TOKEN_ERROR", err.Error(), nil)
		return
	}
	_ = auditkit.Record(a.store, auditkit.Event{Actor: u.Username, Action: "auth.login", Target: u.ID, Status: "success", Message: "login"})
	writeJSON(w, http.StatusOK, map[string]any{
		"token": token,
		"user": map[string]any{
			"username":     u.Username,
			"role":         u.Role,
			"tokenVersion": u.TokenVersion,
			"permissions":  rbac.Permissions(u.Role),
		},
	})
}

func (a *API) auditLoginLocked(lang, username string, lockedUntil time.Time) {
	_ = auditkit.Record(a.store, auditkit.Event{
		Actor:   username,
		Action:  "auth.login.locked",
		Target:  username,
		Status:  "failed",
		Message: i18n.Text(lang, "api.authLocked") + " until=" + lockedUntil.UTC().Format(time.RFC3339),
	})
}

func writeAuthLocked(w http.ResponseWriter, r *http.Request, lockedUntil time.Time) {
	retryAfter := int(time.Until(lockedUntil).Seconds())
	if retryAfter < 1 {
		retryAfter = 1
	}
	w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	writeError(w, http.StatusTooManyRequests, "AUTH_LOCKED", i18n.Text(languageFromRequest(r), "api.authLocked"), map[string]any{"retryAfterSeconds": retryAfter})
}

func loginGuardKey(r *http.Request, username string) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if host == "" {
		host = "unknown"
	}
	return strings.ToLower(strings.TrimSpace(username)) + "|" + host
}

func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearerToken(r)
		claims, err := auth.ParseToken(a.cfg.JWTSecret, token)
		if token == "" || err != nil {
			writeError(w, http.StatusUnauthorized, "UNAUTHORIZED", i18n.Text(languageFromRequest(r), "api.authRequired"), nil)
			return
		}
		user, err := a.store.UserByID(claims.UserID)
		if err != nil || user.TokenVersion != claims.TokenVersion {
			writeError(w, http.StatusUnauthorized, "SESSION_INVALID", i18n.Text(languageFromRequest(r), "api.sessionInvalid"), nil)
			return
		}
		claims.Username = user.Username
		claims.Role = user.Role
		claims.TokenVersion = user.TokenVersion
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxClaims{}, claims)))
	})
}
