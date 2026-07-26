package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

type aifarReleaseRollbackRequest struct {
	TargetReleaseID string   `json:"targetReleaseId"`
	Services        []string `json:"services"`
	Reason          string   `json:"reason"`
	Force           bool     `json:"force"`
}

type aifarReleaseDeleteBlock struct {
	Code       string
	MessageKey string
	Details    map[string]any
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
		items = append(items, aifarReleaseResponseItem(release, manifest))
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func aifarReleaseResponseItem(release store.AppRelease, manifest map[string]any) map[string]any {
	changed := stringsFromAny(manifest["changedServices"])
	artifacts := mapFromAny(manifest["artifacts"])
	item := map[string]any{
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
		"changedServices":   changed,
		"rollbackAvailable": release.Status == "success" && len(changed) > 0 && len(artifacts) > 0,
		"manifest":          manifest,
	}
	if !release.ActivatedAt.IsZero() {
		item["activatedAt"] = release.ActivatedAt
	}
	return item
}

func (a *API) deleteAIFARRelease(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	instanceID := strings.TrimSpace(chi.URLParam(r, "id"))
	releaseID := strings.TrimSpace(chi.URLParam(r, "releaseId"))
	instance, err := a.store.GetAppInstance(instanceID)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !strings.EqualFold(instance.App, "aifar") {
		writeError(w, http.StatusBadRequest, "AIFAR_INSTANCE_REQUIRED", i18n.Text(lang, "api.aifarInstanceRequired"), map[string]any{"instanceId": instanceID})
		return
	}
	releases, err := a.store.ListAppReleases(instanceID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "AIFAR_RELEASE_LIST_FAILED", err.Error(), nil)
		return
	}
	var target *store.AppRelease
	for index := range releases {
		if releases[index].ReleaseID == releaseID {
			target = &releases[index]
			break
		}
	}
	if target == nil {
		writeError(w, http.StatusNotFound, "AIFAR_RELEASE_NOT_FOUND", i18n.Text(lang, "api.aifarReleaseNotFound"), map[string]any{"instanceId": instanceID, "releaseId": releaseID})
		return
	}
	if block := aifarReleaseDeleteBlockReason(instance, *target, releases); block != nil {
		writeError(w, http.StatusConflict, block.Code, i18n.Text(lang, block.MessageKey), block.Details)
		return
	}
	if err := a.store.DeleteAppRelease(instanceID, releaseID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusNotFound, "AIFAR_RELEASE_NOT_FOUND", i18n.Text(lang, "api.aifarReleaseNotFound"), map[string]any{"instanceId": instanceID, "releaseId": releaseID})
			return
		}
		writeError(w, http.StatusInternalServerError, "AIFAR_RELEASE_DELETE_FAILED", i18n.Text(lang, "api.aifarReleaseDeleteFailed"), map[string]any{"instanceId": instanceID, "releaseId": releaseID})
		return
	}
	a.audit(r, "aifar.release.delete", instanceID+":"+releaseID, "success", releaseID)
	writeJSON(w, http.StatusOK, map[string]any{"releaseId": releaseID})
}

func aifarReleaseDeleteBlockReason(instance store.AppInstance, target store.AppRelease, releases []store.AppRelease) *aifarReleaseDeleteBlock {
	metadata := map[string]any{}
	if strings.TrimSpace(instance.Metadata) != "" {
		_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	}
	currentReleaseID := stringFromAny(metadata["currentRevision"], stringFromAny(metadata["releaseId"], ""))
	if currentReleaseID != "" && target.ReleaseID == currentReleaseID {
		return &aifarReleaseDeleteBlock{
			Code:       "AIFAR_RELEASE_DELETE_CURRENT",
			MessageKey: "api.aifarReleaseDeleteCurrent",
			Details:    map[string]any{"releaseId": target.ReleaseID},
		}
	}
	if target.Status == "pending" || target.Status == "running" {
		return &aifarReleaseDeleteBlock{
			Code:       "AIFAR_RELEASE_DELETE_ACTIVE",
			MessageKey: "api.aifarReleaseDeleteActive",
			Details:    map[string]any{"releaseId": target.ReleaseID, "status": target.Status},
		}
	}
	for _, release := range releases {
		if release.ReleaseID == target.ReleaseID {
			continue
		}
		manifest := map[string]any{}
		if strings.TrimSpace(release.ManifestJSON) == "" || json.Unmarshal([]byte(release.ManifestJSON), &manifest) != nil {
			continue
		}
		for _, field := range []string{"baseReleaseId", "rollbackTo"} {
			if stringFromAny(manifest[field], "") == target.ReleaseID {
				return &aifarReleaseDeleteBlock{
					Code:       "AIFAR_RELEASE_DELETE_REFERENCED",
					MessageKey: "api.aifarReleaseDeleteReferenced",
					Details:    map[string]any{"releaseId": target.ReleaseID, "referencedBy": release.ReleaseID, "field": field},
				}
			}
		}
	}
	return nil
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
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "ARTIFACT_ROLLBACK_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App, "releaseId": req.TargetReleaseID})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, appInstanceOperationLockSpecs("artifact-rollback", []store.AppInstance{instance}))
	if !ok {
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
	if err != nil {
		a.releaseOperationLocks(locks)
	}
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
