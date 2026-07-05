package httpapi

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/apperror"
	"aifar-deployment/backend/internal/auditkit"
	"aifar-deployment/backend/internal/auth"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/store"

	"github.com/gorilla/websocket"
)

type ctxClaims struct{}

func currentUser(r *http.Request) auth.Claims {
	claims, _ := r.Context().Value(ctxClaims{}).(auth.Claims)
	return claims
}

func (a *API) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		next.ServeHTTP(w, r)
	})
}

func (a *API) limitRequestBody(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.cfg.MaxRequestBodyBytes > 0 && r.Body != nil && requestMayHaveBody(r.Method) {
			r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

func requestMayHaveBody(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

func (a *API) requirePermission(permission rbac.Permission, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !rbac.Allows(currentUser(r).Role, permission) {
			_ = auditkit.Record(a.store, auditkit.Event{
				Actor:   currentUser(r).Username,
				Action:  "auth.permission.denied",
				Target:  string(permission),
				Status:  "failed",
				Message: r.Method + " " + r.URL.Path,
			})
			writeError(w, http.StatusForbidden, "FORBIDDEN", i18n.Text(languageFromRequest(r), "api.permissionDenied"), map[string]any{"permission": string(permission)})
			return
		}
		next(w, r)
	}
}

func languageFromRequest(r *http.Request) string {
	if lang := strings.TrimSpace(r.URL.Query().Get("lang")); lang != "" {
		return lang
	}
	if lang := strings.TrimSpace(r.Header.Get("X-AIFAR-Language")); lang != "" {
		return lang
	}
	return r.Header.Get("Accept-Language")
}

func queryInt(r *http.Request, key string, fallback int) int {
	value := strings.TrimSpace(r.URL.Query().Get(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func queryBool(r *http.Request, key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return fallback
	}
}

func bearerToken(r *http.Request) string {
	header := r.Header.Get("Authorization")
	if strings.HasPrefix(header, "Bearer ") {
		return strings.TrimPrefix(header, "Bearer ")
	}
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	return ""
}

func tokenFromWS(r *http.Request) string {
	if token := bearerToken(r); token != "" {
		return token
	}
	for _, proto := range websocket.Subprotocols(r) {
		if strings.HasPrefix(proto, "aifar.auth.") {
			raw := strings.TrimPrefix(proto, "aifar.auth.")
			data, err := base64.RawURLEncoding.DecodeString(raw)
			if err == nil {
				return string(data)
			}
		}
	}
	return r.URL.Query().Get("token")
}

func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer r.Body.Close()
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", i18n.Text(languageFromRequest(r), "api.requestBodyTooLarge"), map[string]any{"limit": maxBytesErr.Limit})
			return false
		}
		writeError(w, http.StatusBadRequest, "INVALID_JSON", i18n.Text(languageFromRequest(r), "api.invalidJSON"), map[string]any{"error": err.Error()})
		return false
	}
	return true
}

func respond(w http.ResponseWriter, value any, err error) {
	if err != nil {
		code := http.StatusInternalServerError
		if store.IsNotFound(err) {
			code = http.StatusNotFound
		}
		writeError(w, code, "REQUEST_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, value)
}

func respondTask(w http.ResponseWriter, task store.Task, err error) {
	if err != nil {
		writeError(w, http.StatusInternalServerError, "TASK_START_FAILED", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"taskId": task.ID, "status": task.Status})
}

func writeJSON(w http.ResponseWriter, code int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, code int, errCode, message string, details any) {
	apiErr := apperror.New(code, errCode, message, details)
	writeJSON(w, apiErr.Status, apiErr.Body())
}

func writeSSE(w http.ResponseWriter, event string, value any) {
	data, _ := json.Marshal(value)
	_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
