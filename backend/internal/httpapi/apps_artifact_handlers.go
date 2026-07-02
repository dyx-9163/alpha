package httpapi

import (
	"context"
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
		writeError(w, http.StatusBadRequest, "ARTIFACT_UPLOAD_INVALID", err.Error(), nil)
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
