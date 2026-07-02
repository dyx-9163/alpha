package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskplan"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

func (a *API) updateAppInstanceArtifact(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeMultipartParseError(w, r, "ARTIFACT_UPLOAD_INVALID", err)
		return
	}
	if value := strings.TrimSpace(r.FormValue("language")); value != "" {
		lang = value
	}
	serviceName := strings.TrimSpace(r.FormValue("service"))
	file, header, err := r.FormFile("artifact")
	if err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_REQUIRED", i18n.Text(lang, "api.artifactRequired"), nil)
		return
	}
	defer file.Close()

	artifactPath, err := saveMultipartArtifact(file, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ARTIFACT_SAVE_FAILED", err.Error(), nil)
		return
	}
	removeArtifact := true
	defer func() {
		if removeArtifact {
			_ = os.Remove(artifactPath)
		}
	}()

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
	updateModule, ok := module.(registry.ArtifactUpdateModule)
	if !ok {
		writeError(w, http.StatusConflict, "APP_ARTIFACT_UPDATE_UNSUPPORTED", i18n.Text(lang, "api.appArtifactUpdateUnsupported"), map[string]any{"app": instance.App})
		return
	}

	actor := currentUser(r).Username
	target := instance.ServerID
	req := registry.ArtifactUpdateRequest{
		Instance:          instance,
		Server:            server,
		Language:          lang,
		Actor:             actor,
		ServiceName:       serviceName,
		ArtifactLocalPath: artifactPath,
		ArtifactFileName:  header.Filename,
	}
	if err := updateModule.ValidateArtifactUpdate(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_UPDATE_VALIDATE_FAILED", err.Error(), map[string]any{"app": instance.App, "service": serviceName})
		return
	}
	plan, err := updateModule.PlanArtifactUpdate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_UPDATE_PLAN_FAILED", err.Error(), map[string]any{"app": instance.App, "service": serviceName})
		return
	}
	task, err := a.store.CreateTask(store.Task{Type: "apps." + instance.App + ".update-artifact", Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := taskplan.StorePlan(a.store, task.ID, installPlanSteps(plan)); err != nil {
		_ = a.store.DeleteTask(task.ID)
		writeError(w, http.StatusInternalServerError, "ARTIFACT_UPDATE_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App, "service": serviceName})
		return
	}
	removeArtifact = false
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		defer os.Remove(artifactPath)
		log.Info(i18n.Text(lang, "api.artifactUpdateRequested"), instance.App, instance.ID, serviceName)
		if err := updateModule.UpdateArtifact(ctx, req, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.artifactUpdateCompleted"), instance.App, instance.ID, serviceName)
		return nil
	})
	if err != nil {
		_ = os.Remove(artifactPath)
	}
	if err == nil {
		a.audit(r, "apps."+instance.App+".update-artifact", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) updateAppInstanceArtifactBundle(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeMultipartParseError(w, r, "ARTIFACT_BUNDLE_UPLOAD_INVALID", err)
		return
	}
	if value := strings.TrimSpace(r.FormValue("language")); value != "" {
		lang = value
	}
	file, header, err := r.FormFile("bundle")
	if err != nil {
		file, header, err = r.FormFile("artifact")
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_BUNDLE_REQUIRED", i18n.Text(lang, "api.artifactBundleRequired"), nil)
		return
	}
	defer file.Close()

	bundlePath, err := saveMultipartArtifact(file, header.Filename)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ARTIFACT_BUNDLE_SAVE_FAILED", err.Error(), nil)
		return
	}
	removeBundle := true
	defer func() {
		if removeBundle {
			_ = os.Remove(bundlePath)
		}
	}()

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
	updateModule, ok := module.(registry.ArtifactBundleUpdateModule)
	if !ok {
		writeError(w, http.StatusConflict, "APP_ARTIFACT_UPDATE_UNSUPPORTED", i18n.Text(lang, "api.appArtifactUpdateUnsupported"), map[string]any{"app": instance.App})
		return
	}

	actor := currentUser(r).Username
	target := instance.ServerID
	req := registry.ArtifactBundleUpdateRequest{
		Instance:        instance,
		Server:          server,
		Language:        lang,
		Actor:           actor,
		BundleLocalPath: bundlePath,
		BundleFileName:  header.Filename,
	}
	if err := updateModule.ValidateArtifactBundleUpdate(r.Context(), req); err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_BUNDLE_UPDATE_VALIDATE_FAILED", err.Error(), map[string]any{"app": instance.App})
		return
	}
	plan, err := updateModule.PlanArtifactBundleUpdate(r.Context(), req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "ARTIFACT_BUNDLE_UPDATE_PLAN_FAILED", err.Error(), map[string]any{"app": instance.App})
		return
	}
	task, err := a.store.CreateTask(store.Task{Type: "apps." + instance.App + ".update-artifact-bundle", Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := taskplan.StorePlan(a.store, task.ID, installPlanSteps(plan)); err != nil {
		_ = a.store.DeleteTask(task.ID)
		writeError(w, http.StatusInternalServerError, "ARTIFACT_BUNDLE_UPDATE_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App})
		return
	}
	removeBundle = false
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		defer os.Remove(bundlePath)
		log.Info(i18n.Text(lang, "api.artifactBundleUpdateRequested"), instance.App, instance.ID, header.Filename)
		if err := updateModule.UpdateArtifactBundle(ctx, req, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.artifactBundleUpdateCompleted"), instance.App, instance.ID, header.Filename)
		return nil
	})
	if err != nil {
		_ = os.Remove(bundlePath)
	}
	if err == nil {
		a.audit(r, "apps."+instance.App+".update-artifact-bundle", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func writeMultipartParseError(w http.ResponseWriter, r *http.Request, errCode string, err error) {
	var maxBytesErr *http.MaxBytesError
	if errors.As(err, &maxBytesErr) {
		writeError(w, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", i18n.Text(languageFromRequest(r), "api.requestBodyTooLarge"), map[string]any{"limit": maxBytesErr.Limit})
		return
	}
	writeError(w, http.StatusBadRequest, errCode, err.Error(), nil)
}

func saveMultipartArtifact(file io.Reader, originalName string) (string, error) {
	ext := filepath.Ext(filepath.Base(originalName))
	tmp, err := os.CreateTemp("", "aifar-artifact-*"+ext)
	if err != nil {
		return "", err
	}
	defer tmp.Close()
	if _, err := io.Copy(tmp, file); err != nil {
		_ = os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}
