package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
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
	isAIFARSingleUpdate := strings.EqualFold(strings.TrimSpace(instance.App), "aifar")
	var expectedGeneration int64
	if isAIFARSingleUpdate {
		serviceName = strings.ToLower(serviceName)
		var valid bool
		expectedGeneration, valid = strictPositiveMultipartInt64(r.FormValue("expectedGeneration"))
		if !valid {
			writeError(w, http.StatusBadRequest, "INVALID_AIFAR_ARTIFACT_EXPECTED_GENERATION", i18n.Text(lang, "api.aifarRuntimeDeploymentInvalid"), nil)
			return
		}
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
	if isAIFARSingleUpdate {
		target = instance.ID + ":" + serviceName
	}
	req := registry.ArtifactUpdateRequest{
		Instance:           instance,
		Server:             server,
		Language:           lang,
		Actor:              actor,
		ServiceName:        serviceName,
		ExpectedGeneration: expectedGeneration,
		ArtifactLocalPath:  artifactPath,
		ArtifactFileName:   header.Filename,
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
	taskType := artifactUpdateTaskType(instance.App)
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "ARTIFACT_UPDATE_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App, "service": serviceName})
		return
	}
	var (
		operationLocks []store.OperationLock
		runtimeLock    store.AIFAROrchestrationLock
	)
	if isAIFARSingleUpdate {
		runtimeLock, err = a.store.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
			InstanceID: instance.ID, ServiceName: serviceName, Operation: "update-artifact",
			Actor: actor, TaskID: task.ID, ExpiresAt: time.Now().UTC().Add(time.Hour),
		})
		if err != nil {
			if cleanupErr := a.runtime.cleanupUnstartedRuntimeMutation(task.ID, "", lang); cleanupErr != nil {
				a.runtime.writeRuntimePrestartCleanupError(w, lang)
				return
			}
			var conflict store.AIFAROrchestrationLockConflict
			if errors.As(err, &conflict) {
				writeError(w, http.StatusConflict, "AIFAR_RUNTIME_SERVICE_LOCKED", i18n.Text(lang, "api.aifarRuntimeServiceLocked"), map[string]any{"ownerTaskId": conflict.Lock.TaskID})
				return
			}
			writeError(w, http.StatusInternalServerError, "AIFAR_RUNTIME_SERVICE_LOCK_FAILED", i18n.Text(lang, "api.aifarRuntimeDeploymentTaskFailed"), nil)
			return
		}
		freshDeployment, freshErr := a.currentAIFARDeployment(instance.ID, serviceName)
		if freshErr != nil || freshDeployment.Generation != expectedGeneration {
			if cleanupErr := a.runtime.cleanupUnstartedRuntimeMutation(task.ID, runtimeLock.ID, lang); cleanupErr != nil {
				a.runtime.writeRuntimePrestartCleanupError(w, lang)
				return
			}
			if freshErr != nil {
				writeError(w, http.StatusBadRequest, "AIFAR_RUNTIME_DEPLOYMENT_NOT_FOUND", i18n.Text(lang, "api.aifarRuntimeDeploymentNotFound"), nil)
				return
			}
			writeError(w, http.StatusConflict, "AIFAR_RUNTIME_DEPLOYMENT_GENERATION_CONFLICT", i18n.Text(lang, "aifar.deploymentControl.generationConflict"), map[string]any{"currentGeneration": freshDeployment.Generation})
			return
		}
	} else {
		var ok bool
		operationLocks, ok = a.acquireTaskOperationLocks(w, lang, task, appInstanceOperationLockSpecs("artifact-update", []store.AppInstance{instance}))
		if !ok {
			return
		}
	}
	removeArtifact = false
	taskID := task.ID
	job := func(ctx context.Context, log worker.Logger) error {
		if !isAIFARSingleUpdate {
			defer os.Remove(artifactPath)
		}
		log.Info(i18n.Text(lang, "api.artifactUpdateRequested"), instance.App, instance.ID, serviceName)
		if err := updateModule.UpdateArtifact(ctx, req, registry.RunContext{
			TaskID:   taskID,
			Language: lang,
			Actor:    actor,
			LockID:   runtimeLock.ID,
			Log:      log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		}); err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "api.artifactUpdateCompleted"), instance.App, instance.ID, serviceName)
		return nil
	}
	var runtimeLifecycle worker.TaskLifecycle
	if isAIFARSingleUpdate {
		runtimeLifecycle = a.runtime.runtimeDeploymentLockLifecycle(runtimeLock, lang, artifactPath)
		task, err = a.runtime.startExistingWithLanguageAndLifecycle(task, lang, job, runtimeLifecycle)
	} else {
		task, err = a.tasks.StartExistingWithLanguage(task, lang, job)
	}
	if err != nil {
		if isAIFARSingleUpdate {
			if finishErr := runtimeLifecycle.Finish(); finishErr != nil {
				a.runtime.recordRuntimePrestartCleanupFailure(taskID, errors.New(i18n.Text(lang, "api.aifarRuntimePrestartCleanupFailed")))
				a.runtime.writeRuntimePrestartCleanupError(w, lang)
				return
			}
			if cleanupErr := a.runtime.cleanupUnstartedRuntimeMutation(taskID, "", lang); cleanupErr != nil {
				a.runtime.writeRuntimePrestartCleanupError(w, lang)
				return
			}
		} else {
			a.releaseOperationLocks(operationLocks)
			_ = os.Remove(artifactPath)
		}
	}
	if err == nil {
		a.audit(r, taskType, target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func strictPositiveMultipartInt64(value string) (int64, bool) {
	if value == "" || value != strings.TrimSpace(value) {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	return parsed, err == nil && parsed > 0
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
	taskType := artifactBundleUpdateTaskType(instance.App)
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		writeError(w, http.StatusInternalServerError, "ARTIFACT_BUNDLE_UPDATE_PLAN_STORE_FAILED", err.Error(), map[string]any{"app": instance.App})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, appInstanceOperationLockSpecs("artifact-bundle-update", []store.AppInstance{instance}))
	if !ok {
		return
	}
	removeBundle = false
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		defer os.Remove(bundlePath)
		log.Info(i18n.Text(lang, "api.artifactBundleUpdateRequested"), instance.App, instance.ID, header.Filename)
		if err := updateModule.UpdateArtifactBundle(ctx, req, registry.RunContext{
			TaskID:      log.TaskID(),
			Log:         log,
			Concurrency: a.store.DeploymentConcurrency(a.cfg.DeploymentConcurrency),
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
		a.releaseOperationLocks(locks)
		_ = os.Remove(bundlePath)
	}
	if err == nil {
		a.audit(r, taskType, target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func artifactUpdateTaskType(app string) string {
	if strings.EqualFold(strings.TrimSpace(app), "aifar") {
		return "aifar.rollout"
	}
	return "apps." + app + ".update-artifact"
}

func artifactBundleUpdateTaskType(app string) string {
	if strings.EqualFold(strings.TrimSpace(app), "aifar") {
		return "aifar.rollout.bundle"
	}
	return "apps." + app + ".update-artifact-bundle"
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
