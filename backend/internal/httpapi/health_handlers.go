package httpapi

import (
	"net/http"
	"os"
	"strings"
	"time"
)

func (a *API) healthLive(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "checkedAt": time.Now()})
}

func (a *API) healthReady(w http.ResponseWriter, r *http.Request) {
	report := a.healthReport()
	code := http.StatusOK
	if report["status"] != "ok" {
		code = http.StatusServiceUnavailable
	}
	writeJSON(w, code, report)
}

func (a *API) healthDetail(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.healthReport())
}

func (a *API) healthReport() map[string]any {
	components := map[string]map[string]any{}
	components["database"] = healthComponent(a.store.Ping(), map[string]any{"path": a.cfg.DatabasePath})
	components["resources"] = pathComponent(a.cfg.ResourceDir, false)
	components["static"] = pathComponent(a.cfg.StaticDir, true)
	components["databaseBackups"] = pathComponent(a.cfg.DatabaseBackupDir, false)
	status := "ok"
	for _, component := range components {
		if component["status"] != "ok" {
			status = "degraded"
			break
		}
	}
	return map[string]any{
		"status":     status,
		"checkedAt":  time.Now(),
		"components": components,
	}
}

func healthComponent(err error, details map[string]any) map[string]any {
	status := "ok"
	message := ""
	if err != nil {
		status = "error"
		message = err.Error()
	}
	return map[string]any{"status": status, "message": message, "details": details}
}

func pathComponent(path string, mustExist bool) map[string]any {
	details := map[string]any{"path": path}
	if strings.TrimSpace(path) == "" {
		return map[string]any{"status": "error", "message": "path is empty", "details": details}
	}
	info, err := os.Stat(path)
	if err == nil {
		details["isDir"] = info.IsDir()
		return map[string]any{"status": "ok", "message": "", "details": details}
	}
	if os.IsNotExist(err) && !mustExist {
		return map[string]any{"status": "ok", "message": "directory will be created on demand", "details": details}
	}
	return map[string]any{"status": "error", "message": err.Error(), "details": details}
}
