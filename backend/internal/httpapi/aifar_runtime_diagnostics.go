package httpapi

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

const (
	runtimeDiagnosticEstimateTimeout = 30 * time.Second
	runtimeDiagnosticRetention       = 24 * time.Hour
	runtimeDiagnosticMaxArchiveBytes = int64(1 << 30)

	runtimeDiagnosticExportTaskType = "aifar.runtime.diagnostics.export"
	runtimeDiagnosticDeleteTaskType = "aifar.runtime.diagnostics.delete"

	runtimeDiagnosticExportAuditAction   = "containers.aifar.runtime.diagnostics.export"
	runtimeDiagnosticDownloadAuditAction = "containers.aifar.runtime.diagnostics.download"
	runtimeDiagnosticDeleteAuditAction   = "containers.aifar.runtime.diagnostics.delete"
)

var (
	runtimeDiagnosticHTTPExportIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
	runtimeDiagnosticHTTPArchivePattern  = regexp.MustCompile(`^aifar-diagnostics-[A-Za-z0-9._-]+-[0-9]{8}T[0-9]{6}Z\.tar\.gz$`)
	runtimeDiagnosticHTTPSHA256Pattern   = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)
)

type runtimeDiagnosticRequestPayload struct {
	InstanceID string   `json:"instanceId"`
	SinceAt    string   `json:"sinceAt"`
	UntilAt    string   `json:"untilAt"`
	Services   []string `json:"services"`
}

type runtimeDiagnosticTaskResponse struct {
	TaskID   string `json:"taskId"`
	ExportID string `json:"exportId"`
	Status   string `json:"status"`
}

type runtimeDiagnosticDeleteTaskResponse struct {
	TaskID string `json:"taskId"`
	Status string `json:"status"`
}

var runtimeDiagnosticExportStepNames = []string{
	"load-instance",
	"validate-request",
	"estimate-size",
	"collect-file-logs",
	"collect-container-logs",
	"collect-diagnostics",
	"redact-and-manifest",
	"create-archive",
	"record-export",
}

var runtimeDiagnosticDeleteStepNames = []string{
	"validate-export",
	"delete-remote-archive",
	"record-deletion",
}

func (a *aifarRuntimeController) estimateDiagnostics(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	payload, sinceAt, untilAt, ok := decodeRuntimeDiagnosticPayload(w, r)
	if !ok {
		return
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, payload.InstanceID, false)
	if !ok {
		return
	}
	diagnostics, ok := a.runtimeDiagnosticsModule(w, lang, instance)
	if !ok {
		return
	}
	actor := currentUser(r).Username
	estimate, err := estimateRuntimeDiagnosticsWithTimeout(r.Context(), diagnostics, registry.RuntimeDiagnosticRequest{
		Instance: instance,
		Server:   server,
		Language: lang,
		Actor:    actor,
		Services: append([]string(nil), payload.Services...),
		SinceAt:  sinceAt,
		UntilAt:  untilAt,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "RUNTIME_DIAGNOSTIC_ESTIMATE_FAILED", err.Error(), map[string]any{"instanceId": instance.ID})
		return
	}
	writeJSON(w, http.StatusOK, estimate)
}

func (a *aifarRuntimeController) createDiagnosticExport(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	payload, sinceAt, untilAt, ok := decodeRuntimeDiagnosticPayload(w, r)
	if !ok {
		return
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, payload.InstanceID, false)
	if !ok {
		return
	}
	diagnostics, ok := a.runtimeDiagnosticsModule(w, lang, instance)
	if !ok {
		return
	}
	actor := currentUser(r).Username
	diagnosticReq := registry.RuntimeDiagnosticRequest{
		Instance: instance,
		Server:   server,
		Language: lang,
		Actor:    actor,
		Services: append([]string(nil), payload.Services...),
		SinceAt:  sinceAt,
		UntilAt:  untilAt,
	}
	estimate, err := estimateRuntimeDiagnosticsWithTimeout(r.Context(), diagnostics, diagnosticReq)
	if err != nil {
		writeError(w, http.StatusBadRequest, "RUNTIME_DIAGNOSTIC_ESTIMATE_FAILED", err.Error(), map[string]any{"instanceId": instance.ID})
		return
	}
	if !estimate.Allowed {
		writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_ESTIMATE_REJECTED", i18n.Text(lang, "aifar.diag.estimateRejected"), map[string]any{
			"instanceId":     instance.ID,
			"totalBytes":     estimate.TotalBytes,
			"requiredBytes":  estimate.RequiredBytes,
			"availableBytes": estimate.AvailableBytes,
		})
		return
	}

	now := time.Now().UTC()
	exportID := store.NewID("diag")
	task, err := a.store.CreateTask(store.Task{Type: runtimeDiagnosticExportTaskType, Target: instance.ID, Status: "pending", CreatedBy: actor})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_TASK_CREATE_FAILED", i18n.Text(lang, "api.runtimeDiagnosticTaskCreateFailed"), map[string]any{"instanceId": instance.ID})
		return
	}
	if err := a.storeDiagnosticPlanOrDelete(task.ID, server.ID, runtimeDiagnosticExportSteps(lang)); err != nil {
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_TASK_PLAN_FAILED", i18n.Text(lang, "api.runtimeDiagnosticTaskPlanFailed"), map[string]any{"instanceId": instance.ID})
		return
	}
	locks, ok := a.acquireTaskOperationLocks(w, lang, task, []operationLockSpec{{
		Scope:      "runtime-diagnostics",
		ResourceID: instance.ID,
		Operation:  "export",
		Metadata: operationLockMetadata(map[string]any{
			"action":     "export",
			"instanceId": instance.ID,
			"serverId":   server.ID,
			"exportId":   exportID,
		}),
	}})
	if !ok {
		return
	}
	export, err := a.store.SaveDiagnosticExport(store.DiagnosticExport{
		ID:            exportID,
		TaskID:        task.ID,
		InstanceID:    instance.ID,
		ServerID:      server.ID,
		Status:        "pending",
		Services:      append([]string(nil), payload.Services...),
		SinceAt:       sinceAt,
		UntilAt:       untilAt,
		CreatedBy:     actor,
		CreatedAt:     now,
		ExpiresAt:     now.Add(runtimeDiagnosticRetention),
		CleanupStatus: "none",
	})
	if err != nil {
		a.releaseOperationLocks(locks)
		_ = a.store.DeleteTask(task.ID)
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_RECORD_FAILED", i18n.Text(lang, "aifar.diag.recordFailed"), map[string]any{"instanceId": instance.ID})
		return
	}
	task, err = a.startExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		currentExport, getErr := a.store.GetDiagnosticExport(export.ID)
		if getErr != nil {
			return getErr
		}
		currentInstance, getErr := a.store.GetAppInstance(currentExport.InstanceID)
		if getErr != nil {
			return getErr
		}
		currentServer, getErr := a.store.GetServer(currentExport.ServerID, true)
		if getErr != nil {
			return getErr
		}
		return diagnostics.ExportRuntimeDiagnostics(ctx, registry.RuntimeDiagnosticRequest{
			ExportID: currentExport.ID,
			Instance: currentInstance,
			Server:   currentServer,
			Language: lang,
			Actor:    actor,
			Services: append([]string(nil), currentExport.Services...),
			SinceAt:  currentExport.SinceAt,
			UntilAt:  currentExport.UntilAt,
		}, runtimeDiagnosticRunContext(log))
	})
	if err != nil {
		a.releaseOperationLocks(locks)
		export.TaskID = ""
		export.Status = "failed"
		export.ErrorText = i18n.Text(lang, "api.runtimeDiagnosticTaskStartFailed")
		export.CleanupStatus = "complete"
		if _, saveErr := a.store.SaveDiagnosticExport(export); saveErr != nil {
			_ = a.store.UpdateTaskStatus(task.ID, "failed", i18n.Text(lang, "api.runtimeDiagnosticTaskStartFailed"))
		} else {
			_ = a.store.DeleteTask(task.ID)
		}
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_TASK_START_FAILED", i18n.Text(lang, "api.runtimeDiagnosticTaskStartFailed"), map[string]any{"exportId": export.ID})
		return
	}
	a.audit(r, runtimeDiagnosticExportAuditAction, instance.ID, "running", task.ID)
	writeJSON(w, http.StatusAccepted, runtimeDiagnosticTaskResponse{TaskID: task.ID, ExportID: export.ID, Status: task.Status})
}

func (a *aifarRuntimeController) listDiagnosticExports(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	instanceID := strings.TrimSpace(r.URL.Query().Get("instanceId"))
	_, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, instanceID, false)
	if !ok {
		return
	}
	page, err := a.store.ListDiagnosticExports(instance.ID, queryInt(r, "page", 1), queryInt(r, "pageSize", 20))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_LIST_FAILED", i18n.Text(lang, "api.runtimeDiagnosticListFailed"), map[string]any{"instanceId": instance.ID})
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (a *aifarRuntimeController) downloadDiagnosticExport(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	export, server, instance, ok := a.resolveRuntimeDiagnosticExportTarget(w, r)
	if !ok {
		return
	}
	diagnostics, ok := a.runtimeDiagnosticsModule(w, lang, instance)
	if !ok {
		return
	}
	if !validateRuntimeDiagnosticDownload(w, lang, export, server, instance, time.Now().UTC()) {
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", contentDispositionAttachment(export.ArchiveName))
	w.Header().Set("Content-Length", strconv.FormatInt(export.ArchiveBytes, 10))
	w.Header().Set("X-AIFAR-Diagnostic-SHA256", export.SHA256)
	w.WriteHeader(http.StatusOK)
	actor := currentUser(r).Username
	copied, streamErr := diagnostics.StreamRuntimeDiagnosticExport(r.Context(), registry.RuntimeDiagnosticStreamRequest{
		Instance: instance,
		Server:   server,
		Export:   export,
		Language: lang,
		Actor:    actor,
	}, w)
	if streamErr != nil || copied != export.ArchiveBytes {
		a.audit(r, runtimeDiagnosticDownloadAuditAction, export.ID, "failed", fmt.Sprintf("%s: copied=%d expected=%d", i18n.Text(lang, "api.runtimeDiagnosticDownloadFailed"), copied, export.ArchiveBytes))
		return
	}
	a.audit(r, runtimeDiagnosticDownloadAuditAction, export.ID, "success", fmt.Sprintf("%s: bytes=%d", i18n.Text(lang, "api.runtimeDiagnosticDownloadCompleted"), copied))
	if !queryBool(r, "deleteAfterDownload", false) {
		return
	}
	if _, err := a.enqueueRuntimeDiagnosticDelete(r, diagnostics, export, server, instance, lang, actor); err != nil {
		target := runtimeDiagnosticDeleteTarget(instance.ID, export.ID)
		a.audit(r, runtimeDiagnosticDeleteAuditAction, target, "failed", i18n.Text(lang, "api.runtimeDiagnosticDeleteQueueFailed"))
	}
}

func (a *aifarRuntimeController) deleteDiagnosticExport(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	export, server, instance, ok := a.resolveRuntimeDiagnosticExportTarget(w, r)
	if !ok {
		return
	}
	diagnostics, ok := a.runtimeDiagnosticsModule(w, lang, instance)
	if !ok {
		return
	}
	if !validateRuntimeDiagnosticDelete(w, lang, export, server, instance) {
		return
	}
	task, err := a.enqueueRuntimeDiagnosticDelete(r, diagnostics, export, server, instance, lang, currentUser(r).Username)
	if err != nil {
		var conflict store.OperationLockConflict
		if errors.As(err, &conflict) {
			writeError(w, http.StatusConflict, "OPERATION_LOCKED", i18n.Text(lang, "api.operationLocked", conflict.Lock.ResourceID), map[string]any{
				"scope":       conflict.Lock.Scope,
				"resourceId":  conflict.Lock.ResourceID,
				"operation":   conflict.Lock.Operation,
				"ownerTaskId": conflict.Lock.OwnerTaskID,
				"expiresAt":   conflict.Lock.ExpiresAt,
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_DELETE_QUEUE_FAILED", i18n.Text(lang, "api.runtimeDiagnosticDeleteQueueFailed"), map[string]any{"exportId": export.ID})
		return
	}
	writeJSON(w, http.StatusAccepted, runtimeDiagnosticDeleteTaskResponse{TaskID: task.ID, Status: task.Status})
}

func (a *aifarRuntimeController) runtimeDiagnosticsModule(w http.ResponseWriter, lang string, instance store.AppInstance) (registry.RuntimeDiagnosticsModule, bool) {
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return nil, false
	}
	diagnostics, ok := module.(registry.RuntimeDiagnosticsModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_DIAGNOSTICS_UNSUPPORTED", i18n.Text(lang, "api.aifarRuntimeDiagnosticsUnsupported"), map[string]any{"app": instance.App})
		return nil, false
	}
	return diagnostics, true
}

func decodeRuntimeDiagnosticPayload(w http.ResponseWriter, r *http.Request) (runtimeDiagnosticRequestPayload, time.Time, time.Time, bool) {
	lang := languageFromRequest(r)
	var payload runtimeDiagnosticRequestPayload
	if !decode(w, r, &payload) {
		return payload, time.Time{}, time.Time{}, false
	}
	payload.InstanceID = strings.TrimSpace(payload.InstanceID)
	sinceAt, sinceErr := time.Parse(time.RFC3339, strings.TrimSpace(payload.SinceAt))
	untilAt, untilErr := time.Parse(time.RFC3339, strings.TrimSpace(payload.UntilAt))
	if payload.InstanceID == "" || sinceErr != nil || untilErr != nil || !sinceAt.Before(untilAt) {
		writeError(w, http.StatusBadRequest, "RUNTIME_DIAGNOSTIC_REQUEST_INVALID", i18n.Text(lang, "aifar.diag.windowInvalid"), map[string]any{"instanceId": payload.InstanceID})
		return payload, time.Time{}, time.Time{}, false
	}
	return payload, sinceAt.UTC(), untilAt.UTC(), true
}

func estimateRuntimeDiagnosticsWithTimeout(parent context.Context, diagnostics registry.RuntimeDiagnosticsModule, req registry.RuntimeDiagnosticRequest) (registry.RuntimeDiagnosticEstimateResult, error) {
	ctx, cancel := context.WithTimeout(parent, runtimeDiagnosticEstimateTimeout)
	defer cancel()
	return diagnostics.EstimateRuntimeDiagnostics(ctx, req, registry.RunContext{})
}

func (a *aifarRuntimeController) resolveRuntimeDiagnosticExportTarget(w http.ResponseWriter, r *http.Request) (store.DiagnosticExport, store.Server, store.AppInstance, bool) {
	lang := languageFromRequest(r)
	exportID := strings.TrimSpace(chi.URLParam(r, "id"))
	if !runtimeDiagnosticHTTPExportIDPattern.MatchString(exportID) {
		writeError(w, http.StatusBadRequest, "RUNTIME_DIAGNOSTIC_EXPORT_INVALID", i18n.Text(lang, "aifar.diag.exportInvalid"), nil)
		return store.DiagnosticExport{}, store.Server{}, store.AppInstance{}, false
	}
	export, err := a.store.GetDiagnosticExport(exportID)
	if err != nil {
		if store.IsNotFound(err) {
			writeError(w, http.StatusNotFound, "RUNTIME_DIAGNOSTIC_EXPORT_NOT_FOUND", i18n.Text(lang, "aifar.diag.exportNotFound"), map[string]any{"exportId": exportID})
			return store.DiagnosticExport{}, store.Server{}, store.AppInstance{}, false
		}
		writeError(w, http.StatusInternalServerError, "RUNTIME_DIAGNOSTIC_RECORD_FAILED", i18n.Text(lang, "aifar.diag.recordFailed"), nil)
		return store.DiagnosticExport{}, store.Server{}, store.AppInstance{}, false
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, export.InstanceID, false)
	if !ok {
		return store.DiagnosticExport{}, store.Server{}, store.AppInstance{}, false
	}
	if export.InstanceID != instance.ID || export.ServerID != server.ID || instance.ServerID != server.ID {
		writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_TARGET_MISMATCH", i18n.Text(lang, "aifar.diag.serverMismatch"), map[string]any{"exportId": export.ID})
		return store.DiagnosticExport{}, store.Server{}, store.AppInstance{}, false
	}
	return export, server, instance, true
}

func validateRuntimeDiagnosticDownload(w http.ResponseWriter, lang string, export store.DiagnosticExport, server store.Server, instance store.AppInstance, now time.Time) bool {
	if export.InstanceID != instance.ID || export.ServerID != server.ID || instance.ServerID != server.ID {
		writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_TARGET_MISMATCH", i18n.Text(lang, "aifar.diag.serverMismatch"), map[string]any{"exportId": export.ID})
		return false
	}
	if export.Status == "expired" || (export.Status == "ready" && !export.ExpiresAt.After(now)) {
		writeError(w, http.StatusGone, "RUNTIME_DIAGNOSTIC_EXPORT_EXPIRED", i18n.Text(lang, "aifar.diag.exportExpired"), map[string]any{"exportId": export.ID})
		return false
	}
	if export.Status != "ready" {
		writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_EXPORT_NOT_READY", i18n.Text(lang, "aifar.diag.exportStateInvalid"), map[string]any{"exportId": export.ID, "status": export.Status})
		return false
	}
	if !validRuntimeDiagnosticArchiveMetadata(export) {
		writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_ARCHIVE_INVALID", i18n.Text(lang, "aifar.diag.pathInvalid"), map[string]any{"exportId": export.ID})
		return false
	}
	return true
}

func validateRuntimeDiagnosticDelete(w http.ResponseWriter, lang string, export store.DiagnosticExport, server store.Server, instance store.AppInstance) bool {
	if export.InstanceID != instance.ID || export.ServerID != server.ID || instance.ServerID != server.ID {
		writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_TARGET_MISMATCH", i18n.Text(lang, "aifar.diag.serverMismatch"), map[string]any{"exportId": export.ID})
		return false
	}
	if export.DeletedAt.IsZero() && export.CleanupStatus != "complete" {
		switch export.Status {
		case "ready", "expired":
			if validRuntimeDiagnosticArchiveMetadata(export) {
				return true
			}
		case "failed", "cancelled":
			return true
		}
	}
	writeError(w, http.StatusConflict, "RUNTIME_DIAGNOSTIC_EXPORT_DELETE_INVALID", i18n.Text(lang, "aifar.diag.exportStateInvalid"), map[string]any{"exportId": export.ID, "status": export.Status})
	return false
}

func validRuntimeDiagnosticArchiveMetadata(export store.DiagnosticExport) bool {
	if !runtimeDiagnosticHTTPExportIDPattern.MatchString(export.ID) || !runtimeDiagnosticHTTPArchivePattern.MatchString(export.ArchiveName) ||
		!runtimeDiagnosticHTTPSHA256Pattern.MatchString(export.SHA256) || export.ArchiveBytes <= 0 || export.ArchiveBytes > runtimeDiagnosticMaxArchiveBytes {
		return false
	}
	if containsRuntimeDiagnosticHTTPControl(export.ArchiveName) || containsRuntimeDiagnosticHTTPControl(export.RemoteRelativePath) || strings.Contains(export.RemoteRelativePath, "\\") {
		return false
	}
	return path.Clean(export.RemoteRelativePath) == path.Join(export.ID, export.ArchiveName)
}

func containsRuntimeDiagnosticHTTPControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func contentDispositionAttachment(filename string) string {
	return mime.FormatMediaType("attachment", map[string]string{"filename": filename})
}

func runtimeDiagnosticExportSteps(lang string) []simpleTaskStep {
	steps := make([]simpleTaskStep, 0, len(runtimeDiagnosticExportStepNames))
	for _, name := range runtimeDiagnosticExportStepNames {
		steps = append(steps, simpleTaskStep{name: name, title: i18n.Text(lang, "aifar.diag.step."+name)})
	}
	return steps
}

func runtimeDiagnosticDeleteSteps(lang string) []simpleTaskStep {
	steps := make([]simpleTaskStep, 0, len(runtimeDiagnosticDeleteStepNames))
	for _, name := range runtimeDiagnosticDeleteStepNames {
		steps = append(steps, simpleTaskStep{name: name, title: i18n.Text(lang, "aifar.diag.delete.step."+name)})
	}
	return steps
}

func runtimeDiagnosticRunContext(log worker.Logger) registry.RunContext {
	return registry.RunContext{
		TaskID: log.TaskID(),
		Log:    log,
		TargetLog: func(target string) registry.Logger {
			return log.Target(target)
		},
	}
}

func runtimeDiagnosticDeleteTarget(instanceID, exportID string) string {
	return strings.TrimSpace(instanceID) + ":" + strings.TrimSpace(exportID)
}

func (a *aifarRuntimeController) enqueueRuntimeDiagnosticDelete(r *http.Request, diagnostics registry.RuntimeDiagnosticsModule, export store.DiagnosticExport, server store.Server, instance store.AppInstance, lang, actor string) (store.Task, error) {
	target := runtimeDiagnosticDeleteTarget(instance.ID, export.ID)
	task, err := a.store.CreateTask(store.Task{Type: runtimeDiagnosticDeleteTaskType, Target: target, Status: "pending", CreatedBy: actor})
	if err != nil {
		return task, err
	}
	if err := a.storeDiagnosticPlanOrDelete(task.ID, target, runtimeDiagnosticDeleteSteps(lang)); err != nil {
		return task, err
	}
	lock, err := a.store.AcquireOperationLock(store.OperationLock{
		Scope:       "runtime-diagnostics",
		ResourceID:  export.ID,
		Operation:   "delete",
		OwnerTaskID: task.ID,
		Owner:       actor,
		ExpiresAt:   time.Now().UTC().Add(operationLockTTL),
		Metadata: operationLockMetadata(map[string]any{
			"action":     "delete",
			"instanceId": instance.ID,
			"serverId":   server.ID,
			"exportId":   export.ID,
		}),
	})
	if err != nil {
		_ = a.store.DeleteTask(task.ID)
		return task, err
	}
	task, err = a.startExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		activeStep := ""
		fail := func(cause error) error {
			if cause == nil {
				cause = errors.New(i18n.Text(lang, "aifar.diag.deleteFailed"))
			}
			if activeStep != "" {
				log.FinishStep(target, activeStep, "failed", cause.Error())
			}
			log.FinishTarget(target, "failed", cause.Error())
			return cause
		}
		start := func(index int) {
			activeStep = runtimeDiagnosticDeleteStepNames[index]
			log.StartStep(target, activeStep, i18n.Text(lang, "aifar.diag.delete.step."+activeStep), index+1)
		}
		finish := func() {
			log.FinishStep(target, activeStep, "success", "")
			activeStep = ""
		}

		log.StartTarget(target)
		start(0)
		currentExport, getErr := a.store.GetDiagnosticExport(export.ID)
		if getErr != nil {
			return fail(errors.New(i18n.Text(lang, "aifar.diag.exportNotFound")))
		}
		currentInstance, getErr := a.store.GetAppInstance(currentExport.InstanceID)
		if getErr != nil {
			return fail(errors.New(i18n.Text(lang, "aifar.diag.instanceLoadFailed")))
		}
		currentServer, getErr := a.store.GetServer(currentExport.ServerID, true)
		if getErr != nil {
			return fail(errors.New(i18n.Text(lang, "aifar.diag.serverLoadFailed")))
		}
		if currentExport.InstanceID != currentInstance.ID || currentExport.ServerID != currentServer.ID || currentInstance.ServerID != currentServer.ID {
			return fail(errors.New(i18n.Text(lang, "aifar.diag.serverMismatch")))
		}
		finish()

		start(1)
		if deleteErr := diagnostics.DeleteRuntimeDiagnosticExport(ctx, registry.RuntimeDiagnosticDeleteRequest{
			Instance: currentInstance,
			Server:   currentServer,
			Export:   currentExport,
			Language: lang,
			Actor:    actor,
		}, runtimeDiagnosticRunContext(log)); deleteErr != nil {
			return fail(deleteErr)
		}
		finish()

		start(2)
		deleted, getErr := a.store.GetDiagnosticExport(export.ID)
		if getErr != nil || deleted.Status != "deleted" || deleted.CleanupStatus != "complete" || deleted.DeletedAt.IsZero() {
			return fail(errors.New(i18n.Text(lang, "aifar.diag.recordFailed")))
		}
		finish()
		log.FinishTarget(target, "success", "")
		return nil
	})
	if err != nil {
		_, _ = a.store.ReleaseOperationLock(lock.ID)
		_ = a.store.DeleteTask(task.ID)
		return task, err
	}
	a.audit(r, runtimeDiagnosticDeleteAuditAction, target, "running", task.ID)
	return task, nil
}
