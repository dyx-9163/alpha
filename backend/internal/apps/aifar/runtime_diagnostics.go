package aifar

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"
	"text/template"
	"time"
	"unicode"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

const (
	runtimeDiagnosticMaxUncompressed = int64(3 * 1024 * 1024 * 1024)
	runtimeDiagnosticMaxArchive      = int64(1 * 1024 * 1024 * 1024)
	runtimeDiagnosticRetention       = 24 * time.Hour
)

type runtimeDiagnosticEstimateScriptData struct {
	InstallRoot string
	InstanceID  string
	Services    string
	SinceUnix   string
	UntilUnix   string
}

type runtimeDiagnosticExportScriptData struct {
	InstallRoot     string
	ExportID        string
	InstanceID      string
	Services        string
	Since           string
	Until           string
	ArchiveBase     string
	RuntimeSummary  string
	Deployments     string
	Pods            string
	ReleaseSummary  string
	Readme          string
	ProcRoot        string
	FileLimitBlocks string
}

type runtimeDiagnosticCleanupScriptData struct {
	InstallRoot string
	ExportID    string
	ProcRoot    string
	KillCommand string
}

type diagnosticFileStreamer interface {
	StreamFile(context.Context, store.Server, string, io.Writer) (int64, error)
}

type runtimeDiagnosticSummaryPayload struct {
	InstanceID string    `json:"instanceId"`
	ServerID   string    `json:"serverId"`
	App        string    `json:"app"`
	Version    string    `json:"version"`
	Topology   string    `json:"topology"`
	Status     string    `json:"status"`
	Services   []string  `json:"services"`
	SinceAt    time.Time `json:"sinceAt"`
	UntilAt    time.Time `json:"untilAt"`
}

type runtimeDiagnosticDeploymentPayload struct {
	ServiceName      string    `json:"serviceName"`
	DesiredReplicas  int       `json:"desiredReplicas"`
	CurrentRevision  string    `json:"currentRevision"`
	UpdatingRevision string    `json:"updatingRevision,omitempty"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

type runtimeDiagnosticPodPayload struct {
	ServiceName   string    `json:"serviceName"`
	Revision      string    `json:"revision"`
	PodID         string    `json:"podId"`
	ContainerName string    `json:"containerName"`
	Port          int       `json:"port"`
	Status        string    `json:"status"`
	Ready         bool      `json:"ready"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

type runtimeDiagnosticReleasePayload struct {
	ReleaseID   string    `json:"releaseId"`
	Version     string    `json:"version"`
	Status      string    `json:"status"`
	ConfigHash  string    `json:"configHash,omitempty"`
	CreatedAt   time.Time `json:"createdAt"`
	ActivatedAt time.Time `json:"activatedAt,omitempty"`
}

var runtimeDiagnosticSteps = []string{
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

func (s Service) EstimateRuntimeDiagnostics(ctx context.Context, req RuntimeDiagnosticRequest, log Logger) (registry.RuntimeDiagnosticEstimateResult, error) {
	if err := ctx.Err(); err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, err
	}
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return registry.RuntimeDiagnosticEstimateResult{}, errors.New(i18n.Text(req.Language, "aifar.diag.storeMissing"))
	}
	installRoot, services, err := validateRuntimeDiagnosticEstimateRequest(req, diagnostics, time.Now().UTC())
	if err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, err
	}
	script, err := renderRuntimeDiagnosticEstimateScript(runtimeDiagnosticEstimateScriptData{
		InstallRoot: installerkit.ShellQuote(installRoot),
		InstanceID:  installerkit.ShellQuote(req.Instance.ID),
		Services:    installerkit.ShellQuote(strings.Join(services, " ")),
		SinceUnix:   installerkit.ShellQuote(fmt.Sprint(req.SinceAt.Unix())),
		UntilUnix:   installerkit.ShellQuote(fmt.Sprint(req.UntilAt.Unix())),
	})
	if err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, errors.New(i18n.Text(req.Language, "aifar.diag.estimateFailed"))
	}
	if log != nil {
		log.Info("%s", i18n.Text(req.Language, "aifar.diag.estimateStarted", req.Instance.ID))
	}
	result, runErr := s.remote.Run(ctx, req.Server, "sh -s <<'AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE'\n"+script+"\nAIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE")
	if runErr != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, errors.New(i18n.Text(req.Language, "aifar.diag.estimateFailed"))
	}
	estimate, err := parseRuntimeDiagnosticEstimate(result.Stdout, services)
	if err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, errors.New(i18n.Text(req.Language, "aifar.diag.protocolInvalid"))
	}
	estimate.Allowed = estimate.TotalBytes <= runtimeDiagnosticMaxUncompressed && estimate.RequiredBytes <= estimate.AvailableBytes
	if log != nil {
		log.Info("%s", i18n.Text(req.Language, "aifar.diag.estimateCompleted", estimate.TotalBytes))
	}
	return estimate, nil
}

func (s Service) ExportRuntimeDiagnostics(ctx context.Context, req RuntimeDiagnosticRequest, log Logger, targetLog targetLogger) (err error) {
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return errors.New(i18n.Text(req.Language, "aifar.diag.storeMissing"))
	}
	target := strings.TrimSpace(req.Instance.ServerID)
	if target == "" {
		target = strings.TrimSpace(req.Server.ID)
	}
	if target == "" {
		target = strings.TrimSpace(req.ExportID)
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	activeStep := ""
	stepTitles := runtimeDiagnosticStepTitles(req.Language)
	startStep := func(index int) {
		activeStep = runtimeDiagnosticSteps[index]
		if recorder != nil {
			recorder.StartStep(target, activeStep, stepTitles[index], index+1)
		}
	}
	finishStep := func(status, errText string) {
		if recorder != nil && activeStep != "" {
			recorder.FinishStep(target, activeStep, status, errText)
		}
		activeStep = ""
	}

	var exportRecord store.DiagnosticExport
	var current store.AppInstance
	var server store.Server
	var installRoot string
	loaded := false
	failureRecordOwned := false
	cleanupReady := false
	fail := func(cause error) error {
		if cause == nil {
			cause = errors.New(i18n.Text(req.Language, "aifar.diag.exportFailed"))
		}
		status := "failed"
		if errors.Is(cause, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
			cause = context.Canceled
		}
		finishStep(status, cause.Error())
		finishTarget(recorder, target, status, cause.Error())
		if !loaded || !failureRecordOwned {
			return cause
		}
		exportRecord.Status = status
		exportRecord.ErrorText = cause.Error()
		exportRecord.RemoteRelativePath = ""
		exportRecord.ArchiveName = ""
		exportRecord.ArchiveBytes = 0
		exportRecord.UncompressedBytes = 0
		exportRecord.SHA256 = ""
		exportRecord.WarningCount = 0
		exportRecord.Warnings = []string{}
		if cleanupReady {
			exportRecord.CleanupAttemptedAt = time.Now().UTC()
			exportRecord.CleanupStatus = "pending"
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			cleanupErr := s.cleanupRuntimeDiagnosticExport(cleanupCtx, server, installRoot, exportRecord.ID)
			cancel()
			if cleanupErr != nil {
				exportRecord.CleanupStatus = "failed"
				exportRecord.CleanupError = i18n.Text(req.Language, "aifar.diag.cleanupFailed")
			} else {
				exportRecord.CleanupStatus = "complete"
				exportRecord.CleanupError = ""
			}
		}
		if _, saveErr := diagnostics.SaveDiagnosticExport(exportRecord); saveErr != nil && logForServer != nil {
			logForServer.Error("%s", i18n.Text(req.Language, "aifar.diag.recordFailed"))
		}
		return cause
	}

	startStep(0)
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	exportID := strings.TrimSpace(req.ExportID)
	if !runtimeDiagnosticExportIDPattern.MatchString(exportID) {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportInvalid")))
	}
	exportRecord, err = diagnostics.GetDiagnosticExport(exportID)
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportNotFound")))
	}
	loaded = true
	failureRecordOwned = exportRecord.Status == "pending" || exportRecord.Status == "building"
	current, err = s.store.GetAppInstance(exportRecord.InstanceID)
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.instanceLoadFailed")))
	}
	server, err = s.store.GetServer(exportRecord.ServerID, true)
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.serverLoadFailed")))
	}
	finishStep("success", "")

	startStep(1)
	if exportRecord.Status != "pending" && exportRecord.Status != "building" {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportStateInvalid")))
	}
	if current.ID != exportRecord.InstanceID || current.ServerID != exportRecord.ServerID || server.ID != exportRecord.ServerID {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.serverMismatch")))
	}
	if req.Instance.ID != "" && req.Instance.ID != current.ID {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.serverMismatch")))
	}
	if req.Server.ID != "" && req.Server.ID != server.ID {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.serverMismatch")))
	}
	normalizedRequest := RuntimeDiagnosticRequest{
		ExportID: exportRecord.ID,
		Instance: current,
		Server:   server,
		Language: req.Language,
		Actor:    exportRecord.CreatedBy,
		Services: append([]string(nil), exportRecord.Services...),
		SinceAt:  exportRecord.SinceAt,
		UntilAt:  exportRecord.UntilAt,
	}
	installRoot, normalizedRequest.Services, err = validateRuntimeDiagnosticEstimateRequest(normalizedRequest, diagnostics, time.Now().UTC())
	if err != nil {
		return fail(err)
	}
	exportRecord.Status = "building"
	exportRecord.ErrorText = ""
	exportRecord.CleanupStatus = "none"
	exportRecord.CleanupError = ""
	savedRecord, saveErr := diagnostics.SaveDiagnosticExport(exportRecord)
	if saveErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed")))
	}
	exportRecord = savedRecord
	cleanupReady = true
	finishStep("success", "")

	startStep(2)
	estimate, err := s.EstimateRuntimeDiagnostics(ctx, normalizedRequest, logForServer)
	if err != nil {
		return fail(err)
	}
	if !estimate.Allowed {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.estimateRejected")))
	}

	runtimeSummaryJSON, deploymentsJSON, podsJSON, releaseSummaryJSON, err := buildRuntimeDiagnosticPayloads(diagnostics, current, exportRecord)
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.payloadFailed")))
	}
	createdAt := exportRecord.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	archiveBase := "aifar-diagnostics-" + runtimeDiagnosticSafeSegment(current.ID) + "-" + createdAt.Format("20060102T150405Z")
	script, err := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{
		InstallRoot:    installerkit.ShellQuote(installRoot),
		ExportID:       installerkit.ShellQuote(exportRecord.ID),
		InstanceID:     installerkit.ShellQuote(current.ID),
		Services:       installerkit.ShellQuote(strings.Join(normalizedRequest.Services, " ")),
		Since:          installerkit.ShellQuote(exportRecord.SinceAt.UTC().Format(time.RFC3339)),
		Until:          installerkit.ShellQuote(exportRecord.UntilAt.UTC().Format(time.RFC3339)),
		ArchiveBase:    installerkit.ShellQuote(archiveBase),
		RuntimeSummary: installerkit.ShellQuote(runtimeSummaryJSON),
		Deployments:    installerkit.ShellQuote(deploymentsJSON),
		Pods:           installerkit.ShellQuote(podsJSON),
		ReleaseSummary: installerkit.ShellQuote(releaseSummaryJSON),
		Readme:         installerkit.ShellQuote(i18n.Text(req.Language, "aifar.diag.readme")),
	})
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportFailed")))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	finishStep("success", "")

	startStep(3)
	if logForServer != nil {
		logForServer.Info("%s", i18n.Text(req.Language, "aifar.diag.exportStarted", current.ID))
	}
	result, runErr := s.remote.Run(ctx, server, "setsid sh -s <<'AIFAR_RUNTIME_DIAGNOSTIC_EXPORT'\n"+script+"\nAIFAR_RUNTIME_DIAGNOSTIC_EXPORT")
	if runErr != nil {
		if ctx.Err() != nil {
			return fail(ctx.Err())
		}
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportFailed")))
	}
	parsed, err := parseRuntimeDiagnosticExportResult(result.Stdout, exportRecord.ID)
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.protocolInvalid")))
	}
	finishStep("success", "")
	for index := 4; index <= 7; index++ {
		startStep(index)
		finishStep("success", "")
	}

	// Promotion and SHA validation have completed remotely. Do not observe cancellation
	// inside this short record section; a ready file must always have a ready row.
	startStep(8)
	readyAt := time.Now().UTC()
	exportRecord.Status = "ready"
	exportRecord.RemoteRelativePath = parsed.RemoteRelativePath
	exportRecord.ArchiveName = parsed.ArchiveName
	exportRecord.ArchiveBytes = parsed.ArchiveBytes
	exportRecord.UncompressedBytes = parsed.UncompressedBytes
	exportRecord.SHA256 = parsed.SHA256
	exportRecord.Warnings = runtimeDiagnosticWarningPlaceholders(parsed.WarningCount)
	exportRecord.WarningCount = parsed.WarningCount
	exportRecord.ErrorText = ""
	exportRecord.ReadyAt = readyAt
	exportRecord.ExpiresAt = readyAt.Add(runtimeDiagnosticRetention)
	exportRecord.CleanupStatus = "none"
	exportRecord.CleanupError = ""
	savedRecord, saveErr = diagnostics.SaveDiagnosticExport(exportRecord)
	if saveErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed")))
	}
	exportRecord = savedRecord
	cleanupReady = false
	finishStep("success", "")
	finishTarget(recorder, target, "success", "")
	if logForServer != nil {
		logForServer.Info("%s", i18n.Text(req.Language, "aifar.diag.exportCompleted", exportRecord.ArchiveName))
	}
	return nil
}

func (s Service) StreamRuntimeDiagnosticExport(ctx context.Context, req RuntimeDiagnosticStreamRequest, dst io.Writer) (int64, error) {
	if dst == nil {
		return 0, errors.New(i18n.Text(req.Language, "aifar.diag.streamFailed"))
	}
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return 0, errors.New(i18n.Text(req.Language, "aifar.diag.storeMissing"))
	}
	streamer, ok := s.remote.(diagnosticFileStreamer)
	if !ok {
		return 0, errors.New(i18n.Text(req.Language, "aifar.diag.streamUnsupported"))
	}
	exportRecord, _, server, installRoot, err := s.loadRuntimeDiagnosticArtifact(req.Export.ID, req.Instance.ID, req.Server.ID, req.Language, false)
	if err != nil {
		return 0, err
	}
	absolutePath := path.Join(installRoot, "runtime", "diagnostics", exportRecord.RemoteRelativePath)
	n, err := streamer.StreamFile(ctx, server, absolutePath, dst)
	if err != nil {
		if ctx.Err() != nil {
			return n, ctx.Err()
		}
		return n, errors.New(i18n.Text(req.Language, "aifar.diag.streamFailed"))
	}
	if n != exportRecord.ArchiveBytes {
		return n, errors.New(i18n.Text(req.Language, "aifar.diag.streamSizeMismatch"))
	}
	updated, err := diagnostics.MarkDiagnosticExportDownloaded(exportRecord.ID, time.Now().UTC())
	if err != nil || !updated {
		return n, errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed"))
	}
	return n, nil
}

func (s Service) DeleteRuntimeDiagnosticExport(ctx context.Context, req RuntimeDiagnosticDeleteRequest, log Logger) error {
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return errors.New(i18n.Text(req.Language, "aifar.diag.storeMissing"))
	}
	exportRecord, _, server, installRoot, err := s.loadRuntimeDiagnosticArtifact(req.Export.ID, req.Instance.ID, req.Server.ID, req.Language, true)
	if err != nil {
		return err
	}
	attemptedAt := time.Now().UTC()
	updated, updateErr := diagnostics.MarkDiagnosticExportCleanupPending(exportRecord.ID, attemptedAt)
	if updateErr != nil || !updated {
		return errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed"))
	}
	if log != nil {
		log.Info("%s", i18n.Text(req.Language, "aifar.diag.deleteStarted", exportRecord.ArchiveName))
	}
	if err := s.cleanupRuntimeDiagnosticExport(ctx, server, installRoot, exportRecord.ID); err != nil {
		_, _ = diagnostics.MarkDiagnosticExportCleanupFailed(exportRecord.ID, i18n.Text(req.Language, "aifar.diag.cleanupFailed"))
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New(i18n.Text(req.Language, "aifar.diag.deleteFailed"))
	}
	updated, updateErr = diagnostics.MarkDiagnosticExportDeleted(exportRecord.ID, time.Now().UTC())
	if updateErr != nil || !updated {
		return errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed"))
	}
	if log != nil {
		log.Info("%s", i18n.Text(req.Language, "aifar.diag.deleteCompleted", exportRecord.ArchiveName))
	}
	return nil
}

func validateRuntimeDiagnosticEstimateRequest(req RuntimeDiagnosticRequest, diagnostics runtimeDiagnosticsStore, now time.Time) (string, []string, error) {
	if req.Instance.App != AppName || stringFromMetadata(metadataFromInstance(req.Instance), "orchestrationModel", "") != orchestrationModelK8sLikeV1 {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.instanceUnsupported"))
	}
	if strings.TrimSpace(req.Instance.ID) == "" || strings.TrimSpace(req.Server.ID) == "" || req.Instance.ServerID != req.Server.ID {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.serverMismatch"))
	}
	if containsRuntimeDiagnosticControl(req.Instance.ID) {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.inputUnsafe"))
	}
	if req.SinceAt.IsZero() || req.UntilAt.IsZero() || !req.SinceAt.Before(req.UntilAt) {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.windowInvalid"))
	}
	if req.UntilAt.After(now) {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.windowFuture"))
	}
	if req.UntilAt.Sub(req.SinceAt) > runtimeDiagnosticRetention {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.windowTooLarge"))
	}
	if len(req.Services) == 0 {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.servicesRequired"))
	}
	metadata := metadataFromInstance(req.Instance)
	installRoot := stringFromMetadata(metadata, "installRoot", "")
	if strings.TrimSpace(installRoot) == "" || !strings.HasPrefix(path.Clean(installRoot), "/") || path.Clean(installRoot) == "/" {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.installRootMissing"))
	}
	if containsRuntimeDiagnosticControl(installRoot) {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.inputUnsafe"))
	}
	deployments, err := diagnostics.ListAIFARDeployments(req.Instance.ID)
	if err != nil {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.selectionFailed"))
	}
	enabled := make(map[string]bool, len(deployments))
	for _, deployment := range deployments {
		if deployment.InstanceID == req.Instance.ID && deployment.DesiredReplicas > 0 {
			enabled[deployment.ServiceName] = true
		}
	}
	services := make([]string, 0, len(req.Services))
	seen := map[string]bool{}
	for _, service := range req.Services {
		if !runtimeDiagnosticNamePattern.MatchString(service) || seen[service] {
			return "", nil, fmt.Errorf(i18n.Text(req.Language, "aifar.diag.serviceInvalid"), service)
		}
		if !enabled[service] {
			return "", nil, fmt.Errorf(i18n.Text(req.Language, "aifar.diag.serviceUnavailable"), service)
		}
		seen[service] = true
		services = append(services, service)
	}
	return path.Clean(installRoot), services, nil
}

func containsRuntimeDiagnosticControl(value string) bool {
	for _, r := range value {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}

func renderRuntimeDiagnosticEstimateScript(data runtimeDiagnosticEstimateScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-diagnostics-estimate.sh")
	if err != nil {
		return "", err
	}
	return renderEmbeddedRuntimeDiagnosticScript("aifar-runtime-diagnostics-estimate", content, data)
}

func renderRuntimeDiagnosticExportScript(data runtimeDiagnosticExportScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-diagnostics-export.sh")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(data.ProcRoot) == "" {
		data.ProcRoot = installerkit.ShellQuote("/proc")
	}
	if strings.TrimSpace(data.FileLimitBlocks) == "" {
		data.FileLimitBlocks = "1048576"
	}
	return renderEmbeddedRuntimeDiagnosticScript("aifar-runtime-diagnostics-export", content, data)
}

func renderRuntimeDiagnosticCleanupScript(data runtimeDiagnosticCleanupScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-diagnostics-cleanup.sh")
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(data.ProcRoot) == "" {
		data.ProcRoot = installerkit.ShellQuote("/proc")
	}
	if strings.TrimSpace(data.KillCommand) == "" {
		data.KillCommand = installerkit.ShellQuote("/bin/kill")
	}
	return renderEmbeddedRuntimeDiagnosticScript("aifar-runtime-diagnostics-cleanup", content, data)
}

func renderEmbeddedRuntimeDiagnosticScript(name string, content []byte, data any) (string, error) {
	tpl, err := template.New(name).Option("missingkey=error").Parse(string(content))
	if err != nil {
		return "", fmt.Errorf("parse embedded runtime diagnostic script %s: %w", name, err)
	}
	var rendered bytes.Buffer
	if err := tpl.Execute(&rendered, data); err != nil {
		return "", fmt.Errorf("render embedded runtime diagnostic script %s: %w", name, err)
	}
	return rendered.String(), nil
}

func (s Service) cleanupRuntimeDiagnosticExport(ctx context.Context, server store.Server, installRoot, exportID string) error {
	if !runtimeDiagnosticExportIDPattern.MatchString(exportID) || strings.TrimSpace(installRoot) == "" || path.Clean(installRoot) == "/" || !path.IsAbs(path.Clean(installRoot)) || containsRuntimeDiagnosticControl(installRoot) {
		return errors.New("runtime diagnostic cleanup identity is invalid")
	}
	script, err := renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{
		InstallRoot: installerkit.ShellQuote(path.Clean(installRoot)),
		ExportID:    installerkit.ShellQuote(exportID),
	})
	if err != nil {
		return err
	}
	_, err = s.remote.Run(ctx, server, "sh -s <<'AIFAR_RUNTIME_DIAGNOSTIC_CLEANUP'\n"+script+"\nAIFAR_RUNTIME_DIAGNOSTIC_CLEANUP")
	return err
}

func (s Service) loadRuntimeDiagnosticArtifact(exportID, requestInstanceID, requestServerID, lang string, allowExpired bool) (store.DiagnosticExport, store.AppInstance, store.Server, string, error) {
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.storeMissing"))
	}
	exportID = strings.TrimSpace(exportID)
	if !runtimeDiagnosticExportIDPattern.MatchString(exportID) {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportInvalid"))
	}
	exportRecord, err := diagnostics.GetDiagnosticExport(exportID)
	if err != nil {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportNotFound"))
	}
	instance, err := s.store.GetAppInstance(exportRecord.InstanceID)
	if err != nil {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.instanceLoadFailed"))
	}
	server, err := s.store.GetServer(exportRecord.ServerID, true)
	if err != nil {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.serverLoadFailed"))
	}
	if instance.ID != exportRecord.InstanceID || instance.ServerID != exportRecord.ServerID || server.ID != exportRecord.ServerID ||
		(requestInstanceID != "" && requestInstanceID != instance.ID) || (requestServerID != "" && requestServerID != server.ID) {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.serverMismatch"))
	}
	metadata := metadataFromInstance(instance)
	if instance.App != AppName || stringFromMetadata(metadata, "orchestrationModel", "") != orchestrationModelK8sLikeV1 {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.instanceUnsupported"))
	}
	installRoot := path.Clean(stringFromMetadata(metadata, "installRoot", ""))
	if installRoot == "." || installRoot == "/" || !path.IsAbs(installRoot) || containsRuntimeDiagnosticControl(installRoot) {
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.installRootMissing"))
	}
	now := time.Now().UTC()
	switch exportRecord.Status {
	case "ready":
		if _, _, err := validateRuntimeDiagnosticRelativePath(exportRecord.ID, exportRecord.RemoteRelativePath, exportRecord.ArchiveName); err != nil ||
			!runtimeDiagnosticSHA256Pattern.MatchString(exportRecord.SHA256) || exportRecord.ArchiveBytes < 0 || exportRecord.ArchiveBytes > runtimeDiagnosticMaxArchive {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.pathInvalid"))
		}
		if !allowExpired && !exportRecord.ExpiresAt.After(now) {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportExpired"))
		}
	case "expired":
		if !allowExpired {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportExpired"))
		}
		if _, _, err := validateRuntimeDiagnosticRelativePath(exportRecord.ID, exportRecord.RemoteRelativePath, exportRecord.ArchiveName); err != nil ||
			!runtimeDiagnosticSHA256Pattern.MatchString(exportRecord.SHA256) || exportRecord.ArchiveBytes < 0 || exportRecord.ArchiveBytes > runtimeDiagnosticMaxArchive {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.pathInvalid"))
		}
	case "failed", "cancelled":
		if !allowExpired || exportRecord.CleanupStatus == "complete" {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportStateInvalid"))
		}
	default:
		return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportStateInvalid"))
	}
	return exportRecord, instance, server, installRoot, nil
}

func runtimeDiagnosticStepTitles(lang string) []string {
	titles := make([]string, len(runtimeDiagnosticSteps))
	for index, step := range runtimeDiagnosticSteps {
		titles[index] = i18n.Text(lang, "aifar.diag.step."+step)
	}
	return titles
}

func runtimeDiagnosticWarningPlaceholders(count int) []string {
	if count <= 0 {
		return []string{}
	}
	warnings := make([]string, count)
	for index := range warnings {
		warnings[index] = fmt.Sprintf("collection-warning-%d", index+1)
	}
	return warnings
}

func runtimeDiagnosticSafeSegment(value string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(value) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	result := strings.Trim(b.String(), ".-_")
	if result == "" {
		return "instance"
	}
	return result
}

func buildRuntimeDiagnosticPayloads(diagnostics runtimeDiagnosticsStore, instance store.AppInstance, exportRecord store.DiagnosticExport) (string, string, string, string, error) {
	selected := make(map[string]bool, len(exportRecord.Services))
	for _, service := range exportRecord.Services {
		selected[service] = true
	}
	services := append([]string(nil), exportRecord.Services...)
	sort.Strings(services)
	runtimeSummary := runtimeDiagnosticSummaryPayload{
		InstanceID: instance.ID,
		ServerID:   instance.ServerID,
		App:        instance.App,
		Version:    instance.Version,
		Topology:   instance.Topology,
		Status:     instance.Status,
		Services:   services,
		SinceAt:    exportRecord.SinceAt.UTC(),
		UntilAt:    exportRecord.UntilAt.UTC(),
	}

	storedDeployments, err := diagnostics.ListAIFARDeployments(instance.ID)
	if err != nil {
		return "", "", "", "", err
	}
	deployments := make([]runtimeDiagnosticDeploymentPayload, 0, len(storedDeployments))
	for _, item := range storedDeployments {
		if item.InstanceID != instance.ID || !selected[item.ServiceName] {
			continue
		}
		deployments = append(deployments, runtimeDiagnosticDeploymentPayload{
			ServiceName:      item.ServiceName,
			DesiredReplicas:  item.DesiredReplicas,
			CurrentRevision:  item.CurrentRevision,
			UpdatingRevision: item.UpdatingRevision,
			Status:           item.Status,
			CreatedAt:        item.CreatedAt.UTC(),
			UpdatedAt:        item.UpdatedAt.UTC(),
		})
	}
	sort.Slice(deployments, func(i, j int) bool { return deployments[i].ServiceName < deployments[j].ServiceName })

	storedPods, err := diagnostics.ListAIFARPods(instance.ID)
	if err != nil {
		return "", "", "", "", err
	}
	pods := make([]runtimeDiagnosticPodPayload, 0, len(storedPods))
	for _, item := range storedPods {
		if item.InstanceID != instance.ID || !selected[item.ServiceName] {
			continue
		}
		pods = append(pods, runtimeDiagnosticPodPayload{
			ServiceName:   item.ServiceName,
			Revision:      item.Revision,
			PodID:         item.PodID,
			ContainerName: item.ContainerName,
			Port:          item.Port,
			Status:        item.Status,
			Ready:         item.Ready,
			CreatedAt:     item.CreatedAt.UTC(),
			UpdatedAt:     item.UpdatedAt.UTC(),
		})
	}
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].ServiceName == pods[j].ServiceName {
			return pods[i].PodID < pods[j].PodID
		}
		return pods[i].ServiceName < pods[j].ServiceName
	})

	storedReleases, err := diagnostics.ListAppReleases(instance.ID)
	if err != nil {
		return "", "", "", "", err
	}
	releases := make([]runtimeDiagnosticReleasePayload, 0, len(storedReleases))
	for _, item := range storedReleases {
		if item.InstanceID != instance.ID {
			continue
		}
		releases = append(releases, runtimeDiagnosticReleasePayload{
			ReleaseID:   item.ReleaseID,
			Version:     item.Version,
			Status:      item.Status,
			ConfigHash:  item.ConfigHash,
			CreatedAt:   item.CreatedAt.UTC(),
			ActivatedAt: item.ActivatedAt.UTC(),
		})
	}
	sort.Slice(releases, func(i, j int) bool { return releases[i].CreatedAt.Before(releases[j].CreatedAt) })

	values := []any{runtimeSummary, deployments, pods, releases}
	encoded := make([]string, len(values))
	for index, value := range values {
		content, err := json.Marshal(value)
		if err != nil {
			return "", "", "", "", err
		}
		encoded[index] = string(content)
	}
	return encoded[0], encoded[1], encoded[2], encoded[3], nil
}
