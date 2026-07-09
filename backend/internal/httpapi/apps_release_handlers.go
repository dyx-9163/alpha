package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskplan"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

type aifarReleaseRollbackRequest struct {
	TargetReleaseID string   `json:"targetReleaseId"`
	Services        []string `json:"services"`
	Reason          string   `json:"reason"`
	Force           bool     `json:"force"`
}

func (a *API) listAIFARReleases(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !strings.EqualFold(instance.App, "aifar") {
		writeError(w, http.StatusBadRequest, "AIFAR_INSTANCE_REQUIRED", "AIFAR instance is required", map[string]any{"instanceId": id})
		return
	}
	releases, err := a.store.ListAppReleases(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AIFAR_RELEASE_LIST_FAILED", err.Error(), nil)
		return
	}
	items := make([]map[string]any, 0, len(releases))
	for _, release := range releases {
		manifest := map[string]any{}
		if strings.TrimSpace(release.ManifestJSON) != "" {
			_ = json.Unmarshal([]byte(release.ManifestJSON), &manifest)
		}
		changed := stringsFromAny(manifest["changedServices"])
		artifacts := mapFromAny(manifest["artifacts"])
		items = append(items, map[string]any{
			"id":                release.ID,
			"instanceId":        release.InstanceID,
			"releaseId":         release.ReleaseID,
			"kind":              stringFromAny(manifest["kind"], ""),
			"status":            release.Status,
			"manifestStatus":    stringFromAny(manifest["status"], ""),
			"version":           release.Version,
			"serverId":          release.ServerID,
			"configHash":        release.ConfigHash,
			"createdAt":         release.CreatedAt,
			"activatedAt":       release.ActivatedAt,
			"changedServices":   changed,
			"rollbackAvailable": release.Status == "success" && len(changed) > 0 && len(artifacts) > 0,
			"manifest":          manifest,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (a *API) rollbackAIFARRelease(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var body aifarReleaseRollbackRequest
	if !decode(w, r, &body) {
		return
	}
	id := chi.URLParam(r, "id")
	instance, err := a.store.GetAppInstance(id)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if strings.TrimSpace(instance.ServerID) == "" {
		writeError(w, http.StatusBadRequest, "INSTANCE_SERVER_REQUIRED", i18n.Text(lang, "api.instanceServerRequired"), map[string]any{"instanceId": id})
		return
	}
	server, err := a.store.GetServer(instance.ServerID, true)
	if err != nil {
		respond(w, nil, err)
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	rollbackModule, ok := module.(registry.ArtifactRollbackModule)
	if !ok {
		writeError(w, http.StatusConflict, "APP_ARTIFACT_ROLLBACK_UNSUPPORTED", "artifact rollback is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	req := registry.ArtifactRollbackRequest{
		Instance:        instance,
		Server:          server,
		Language:        lang,
		Actor:           actor,
		TargetReleaseID: strings.TrimSpace(body.TargetReleaseID),
		Services:        body.Services,
		Reason:          strings.TrimSpace(body.Reason),
		Force:           body.Force,
	}
	if err := rollbackModule.ValidateArtifactRollback(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_ROLLBACK_VALIDATE_FAILED", err.Error(), map[string]any{"app": instance.App, "releaseId": req.TargetReleaseID})
		return
	}
	plan, err := rollbackModule.PlanArtifactRollback(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_ROLLBACK_PLAN_FAILED", err.Error(), map[string]any{"app": instance.App, "releaseId": req.TargetReleaseID})
		return
	}
	target := instance.ServerID
	taskType := "aifar.rollback"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := taskplan.StorePlan(a.store, task.ID, installPlanSteps(plan)); err != nil {
		_ = a.store.DeleteTask(task.ID)
		writeError(w, http.StatusInternalServerError, "ARTIFACT_ROLLBACK_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App, "releaseId": req.TargetReleaseID})
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		log.Info(i18n.Text(lang, "api.artifactRollbackRequested"), instance.App, instance.ID, req.TargetReleaseID)
		if err := rollbackModule.RollbackArtifact(ctx, req, registry.RunContext{
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.artifactRollbackCompleted"), instance.App, instance.ID, req.TargetReleaseID)
		return nil
	})
	if err == nil {
		a.audit(r, taskType, target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func stringFromAny(value any, fallback string) string {
	text := strings.TrimSpace(strings.Trim(strings.TrimSpace(jsonScalarString(value)), `"`))
	if text == "" || text == "<nil>" {
		return fallback
	}
	return text
}

func jsonScalarString(value any) string {
	if value == nil {
		return ""
	}
	switch v := value.(type) {
	case string:
		return v
	default:
		return strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(toJSONText(v)), "\n", ""), "\r", ""))
	}
}

func toJSONText(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(data)
}

func stringsFromAny(value any) []string {
	switch raw := value.(type) {
	case []string:
		return raw
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			text := strings.TrimSpace(jsonScalarString(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func mapFromAny(value any) map[string]any {
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return nil
}
