package aifar

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
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

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

const (
	runtimeDiagnosticMaxFileScan       = int64(1 << 30)
	runtimeDiagnosticMaxTotalScan      = int64(2 << 30)
	runtimeDiagnosticMaxFiltered       = int64(500 << 20)
	runtimeDiagnosticMaxArchive        = int64(256 << 20)
	runtimeDiagnosticMaxUncompressed   = int64(3 << 30)
	runtimeDiagnosticLegacyMaxArchive  = int64(1 << 30)
	runtimeDiagnosticRetention         = 24 * time.Hour
	runtimeDiagnosticExportTimeout     = 15 * time.Minute
	runtimeDiagnosticEstimateTimeout   = 30 * time.Second
	runtimeDiagnosticHeaderLimit       = 4096
	runtimeDiagnosticMinBytesPerSecond = int64(2 << 20)
	runtimeDiagnosticMaxBytesPerSecond = int64(32 << 20)
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
	FilterProgram   string
}

type runtimeDiagnosticCleanupScriptData struct {
	InstallRoot string
	ExportID    string
	ProcRoot    string
	KillCommand string
	RemoveFinal bool
}

type diagnosticFileStreamer interface {
	StreamFile(context.Context, store.Server, string, io.Writer) (int64, error)
}

type diagnosticCommandStreamer interface {
	StreamCommand(context.Context, store.Server, string, io.Writer) (adapter.CommandStreamResult, error)
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
	"validate-local-storage",
	"discover-log-files",
	"filter-and-redact",
	"build-manifest",
	"stream-local-archive",
	"verify-local-archive",
	"cleanup-remote",
}

func (s Service) EstimateRuntimeDiagnostics(ctx context.Context, req RuntimeDiagnosticRequest, log Logger) (registry.RuntimeDiagnosticEstimateResult, error) {
	if err := ctx.Err(); err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, err
	}
	if s.archives == nil {
		return registry.RuntimeDiagnosticEstimateResult{}, &registry.RuntimeDiagnosticError{
			Code: "RUNTIME_DIAGNOSTIC_LOCAL_COMMIT_FAILED", Message: i18n.Text(req.Language, "aifar.diag.storageMissing"),
		}
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
	estimateCtx, cancel := context.WithTimeout(ctx, runtimeDiagnosticEstimateTimeout)
	defer cancel()
	result, runErr := s.remote.Run(estimateCtx, req.Server, "sh -s <<'AIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE'\n"+script+"\nAIFAR_RUNTIME_DIAGNOSTIC_ESTIMATE")
	if runErr != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, errors.New(i18n.Text(req.Language, "aifar.diag.estimateFailed"))
	}
	estimate, err := parseRuntimeDiagnosticEstimate(result.Stdout, services)
	if err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, errors.New(i18n.Text(req.Language, "aifar.diag.protocolInvalid"))
	}
	if err := s.cleanupExpiredLocalDiagnosticArchives(ctx, diagnostics, time.Now().UTC(), req.ExportID); err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, &registry.RuntimeDiagnosticError{
			Code: "RUNTIME_DIAGNOSTIC_LOCAL_COMMIT_FAILED", Message: i18n.Text(req.Language, "aifar.diag.localDiskFailed"),
		}
	}
	stats, err := s.archives.Stats(ctx)
	if err != nil {
		return registry.RuntimeDiagnosticEstimateResult{}, &registry.RuntimeDiagnosticError{
			Code: "RUNTIME_DIAGNOSTIC_LOCAL_COMMIT_FAILED", Message: i18n.Text(req.Language, "aifar.diag.localDiskFailed"),
		}
	}
	estimate.LogSource = "host-mounted"
	estimate.EstimatedSecondsMin, estimate.EstimatedSecondsMax = runtimeDiagnosticDurationRange(estimate.CandidateScanBytes)
	estimate.MaxFileScanBytes = runtimeDiagnosticMaxFileScan
	estimate.MaxTotalScanBytes = runtimeDiagnosticMaxTotalScan
	estimate.MaxFilteredBytes = runtimeDiagnosticMaxFiltered
	estimate.MaxArchiveBytes = runtimeDiagnosticMaxArchive
	estimate.TimeoutSeconds = int(runtimeDiagnosticExportTimeout / time.Second)
	estimate.LocalAvailableBytes = stats.RootAvailableBytes
	estimate.LocalReadyBytes = stats.ReadyBytes
	estimate.LocalReservedBytes = stats.ReservedBytes
	estimate.LocalQuotaBytes = stats.QuotaBytes
	estimate.ExpiresAt = time.Now().UTC().Add(s.archives.Retention())
	projectedQuota := stats.ReadyBytes + stats.ReservedBytes + runtimeDiagnosticMaxArchive
	projectedHeadroom := stats.RootAvailableBytes - runtimeDiagnosticMaxArchive
	if estimate.BlockReason == "" && projectedQuota > stats.QuotaBytes {
		estimate.BlockReason = "local-quota-exceeded"
	}
	if estimate.BlockReason == "" && projectedHeadroom < runtimeDiagnosticFilesystemMargin {
		estimate.BlockReason = "local-disk-insufficient"
	}
	estimate.Allowed = estimate.BlockReason == ""
	if log != nil {
		log.Info("%s", i18n.Text(req.Language, "aifar.diag.estimateCompleted", estimate.CandidateScanBytes))
	}
	return estimate, nil
}

func (s Service) cleanupExpiredLocalDiagnosticArchives(ctx context.Context, diagnostics runtimeDiagnosticsStore, now time.Time, ownerTaskID string) error {
	records, err := diagnostics.ListDiagnosticExportsForReconcile()
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return err
		}
		if record.StorageKind != "local" || (record.Status != "ready" && record.Status != "expired") || record.ExpiresAt.IsZero() || record.ExpiresAt.After(now) || record.StorageRelativePath == "" {
			continue
		}
		lock, lockErr := diagnostics.AcquireOperationLock(store.OperationLock{
			Scope: "runtime-diagnostics", ResourceID: record.ID, Operation: "delete",
			OwnerTaskID: strings.TrimSpace(ownerTaskID), Owner: runtimeDiagnosticCleanupActor, ExpiresAt: now.Add(time.Hour),
		})
		if lockErr != nil {
			var conflict store.OperationLockConflict
			if errors.As(lockErr, &conflict) {
				continue
			}
			return lockErr
		}
		func() {
			defer diagnostics.ReleaseOperationLock(lock.ID)
			updated, updateErr := diagnostics.MarkDiagnosticExportCleanupPending(record.ID, now)
			if updateErr != nil || !updated {
				return
			}
			if removeErr := s.archives.Remove(record.StorageRelativePath); removeErr != nil {
				_, _ = diagnostics.MarkDiagnosticExportCleanupFailed(record.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
				return
			}
			if auditErr := diagnostics.AddAudit(runtimeDiagnosticCleanupActor, runtimeDiagnosticCleanupAuditAction, record.ID, "success", i18n.Text("", "aifar.diag.deleteCompleted", record.ArchiveName)); auditErr != nil {
				_, _ = diagnostics.MarkDiagnosticExportCleanupFailed(record.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
				return
			}
			if updated, deleteErr := diagnostics.MarkDiagnosticExportDeleted(record.ID, now); deleteErr != nil || !updated {
				_, _ = diagnostics.MarkDiagnosticExportCleanupFailed(record.ID, i18n.Text("", "aifar.diag.cleanupFailed"))
			}
		}()
	}
	return nil
}

func runtimeDiagnosticDurationRange(candidateBytes int64) (int, int) {
	if candidateBytes < 0 {
		candidateBytes = 0
	}
	minimum := int((candidateBytes + runtimeDiagnosticMaxBytesPerSecond - 1) / runtimeDiagnosticMaxBytesPerSecond)
	maximum := int((candidateBytes + runtimeDiagnosticMinBytesPerSecond - 1) / runtimeDiagnosticMinBytesPerSecond)
	if minimum < 1 {
		minimum = 1
	}
	if maximum < minimum {
		maximum = minimum
	}
	limit := int(runtimeDiagnosticExportTimeout / time.Second)
	if maximum > limit {
		maximum = limit
	}
	if minimum > maximum {
		minimum = maximum
	}
	return minimum, maximum
}

func (s Service) ExportRuntimeDiagnostics(ctx context.Context, req RuntimeDiagnosticRequest, log Logger, targetLog targetLogger) (err error) {
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return errors.New(i18n.Text(req.Language, "aifar.diag.storeMissing"))
	}
	streamer, ok := s.remote.(diagnosticCommandStreamer)
	if !ok || s.archives == nil {
		return errors.New(i18n.Text(req.Language, "aifar.diag.storageMissing"))
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

	exportCtx, cancelExport := context.WithTimeout(ctx, runtimeDiagnosticExportTimeout)
	defer cancelExport()
	var exportRecord store.DiagnosticExport
	var server store.Server
	var installRoot string
	var sink RuntimeDiagnosticArchiveSink
	var finalArtifact RuntimeDiagnosticLocalArtifact
	loaded := false
	collectorStarted := false
	reservationHeld := false
	succeeded := false
	defer func() {
		if sink != nil && !succeeded {
			_ = sink.Abort()
		}
		if !succeeded && finalArtifact.RelativePath != "" {
			_ = s.archives.Remove(finalArtifact.RelativePath)
		}
		if reservationHeld {
			_, _ = diagnostics.ReleaseDiagnosticExportReservation(exportRecord.ID)
		}
		if collectorStarted {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			_ = s.cleanupRuntimeDiagnosticExport(cleanupCtx, server, installRoot, exportRecord.ID)
			cleanupCancel()
		}
		if err != nil && loaded && (exportRecord.Status == "pending" || exportRecord.Status == "building") {
			status := "failed"
			if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				status = "cancelled"
			}
			exportRecord.Status = status
			exportRecord.ErrorText = i18n.Text(req.Language, "aifar.diag.exportFailed")
			exportRecord.ReservedBytes = 0
			_, _ = diagnostics.SaveDiagnosticExport(exportRecord)
		}
	}()
	fail := func(cause error) error {
		if errors.Is(exportCtx.Err(), context.DeadlineExceeded) {
			cause = errors.New(i18n.Text(req.Language, "aifar.diag.timeout"))
		} else if errors.Is(ctx.Err(), context.Canceled) {
			cause = context.Canceled
		} else if cause == nil {
			cause = errors.New(i18n.Text(req.Language, "aifar.diag.exportFailed"))
		}
		finishStep("failed", cause.Error())
		finishTarget(recorder, target, "failed", cause.Error())
		return cause
	}

	startStep(0)
	if err = exportCtx.Err(); err != nil {
		return fail(err)
	}
	if !runtimeDiagnosticExportIDPattern.MatchString(strings.TrimSpace(req.ExportID)) {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportInvalid")))
	}
	exportRecord, err = diagnostics.GetDiagnosticExport(strings.TrimSpace(req.ExportID))
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportNotFound")))
	}
	loaded = true
	if exportRecord.StorageKind != "local" || (exportRecord.Status != "pending" && exportRecord.Status != "building") {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportStateInvalid")))
	}
	current, loadErr := s.store.GetAppInstance(exportRecord.InstanceID)
	if loadErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.instanceLoadFailed")))
	}
	server, loadErr = s.store.GetServer(exportRecord.ServerID, true)
	if loadErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.serverLoadFailed")))
	}
	normalizedRequest := RuntimeDiagnosticRequest{
		ExportID: exportRecord.ID, Instance: current, Server: server, Language: req.Language,
		Actor: exportRecord.CreatedBy, Services: append([]string(nil), exportRecord.Services...),
		SinceAt: exportRecord.SinceAt, UntilAt: exportRecord.UntilAt,
	}
	installRoot, normalizedRequest.Services, err = validateRuntimeDiagnosticEstimateRequest(normalizedRequest, diagnostics, time.Now().UTC())
	if err != nil {
		return fail(err)
	}
	if _, reconcileErr := s.archives.Reconcile(exportCtx, time.Now().UTC()); reconcileErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.localDiskFailed")))
	}
	exportRecord.Status = "building"
	exportRecord.ErrorText = ""
	if exportRecord, err = diagnostics.SaveDiagnosticExport(exportRecord); err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed")))
	}
	stats, statsErr := s.archives.Stats(exportCtx)
	if statsErr != nil || stats.RootAvailableBytes < runtimeDiagnosticMaxArchive+runtimeDiagnosticFilesystemMargin {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.localDiskInsufficient")))
	}
	finishStep("success", "")

	startStep(1)
	estimate, estimateErr := s.EstimateRuntimeDiagnostics(exportCtx, normalizedRequest, logForServer)
	if estimateErr != nil {
		return fail(estimateErr)
	}
	if !estimate.Allowed {
		return fail(runtimeDiagnosticEstimateBlockError(req.Language, estimate.BlockReason))
	}
	if _, err = diagnostics.ReserveDiagnosticExportBytes(exportRecord.ID, runtimeDiagnosticMaxArchive, stats.QuotaBytes); err != nil {
		if errors.Is(err, store.ErrDiagnosticExportQuotaExceeded) {
			return fail(errors.New(i18n.Text(req.Language, "aifar.diag.localQuotaExceeded")))
		}
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.recordFailed")))
	}
	reservationHeld = true
	finishStep("success", "")

	startStep(2)
	runtimeSummaryJSON, deploymentsJSON, podsJSON, releaseSummaryJSON, payloadErr := buildRuntimeDiagnosticPayloads(diagnostics, current, exportRecord)
	if payloadErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.payloadFailed")))
	}
	createdAt := exportRecord.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	archiveBase := "aifar-diagnostics-" + runtimeDiagnosticSafeSegment(current.ID) + "-" + createdAt.Format("20060102T150405Z")
	script, renderErr := renderRuntimeDiagnosticExportScript(runtimeDiagnosticExportScriptData{
		InstallRoot: installerkit.ShellQuote(installRoot), ExportID: installerkit.ShellQuote(exportRecord.ID),
		InstanceID: installerkit.ShellQuote(current.ID), Services: installerkit.ShellQuote(strings.Join(normalizedRequest.Services, " ")),
		Since: installerkit.ShellQuote(exportRecord.SinceAt.UTC().Format(time.RFC3339)), Until: installerkit.ShellQuote(exportRecord.UntilAt.UTC().Format(time.RFC3339)),
		ArchiveBase: installerkit.ShellQuote(archiveBase), RuntimeSummary: installerkit.ShellQuote(runtimeSummaryJSON),
		Deployments: installerkit.ShellQuote(deploymentsJSON), Pods: installerkit.ShellQuote(podsJSON),
		ReleaseSummary: installerkit.ShellQuote(releaseSummaryJSON), Readme: installerkit.ShellQuote(i18n.Text(req.Language, "aifar.diag.readme")),
	})
	if renderErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.exportFailed")))
	}
	finishStep("success", "")
	startStep(3)
	finishStep("success", "")

	startStep(4)
	collectorStarted = true
	command := "setsid sh -s <<'AIFAR_RUNTIME_DIAGNOSTIC_EXPORT'\n" + script + "\nAIFAR_RUNTIME_DIAGNOSTIC_EXPORT"
	header, streamErr := streamRuntimeDiagnosticCommand(exportCtx, streamer, server, command, func(parsed runtimeDiagnosticStreamHeader, src io.Reader) error {
		var beginErr error
		sink, beginErr = s.archives.Begin(exportRecord.ID, parsed.ArchiveName)
		if beginErr != nil {
			return beginErr
		}
		_, copyErr := io.Copy(sink, src)
		return copyErr
	})
	if streamErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.streamFailed")))
	}
	finishStep("success", "")

	startStep(5)
	finalArtifact, err = sink.Commit(exportCtx, header.UncompressedBytes)
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.localCommitFailed")))
	}
	finishStep("success", "")

	startStep(6)
	cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
	cleanupErr := s.cleanupRuntimeDiagnosticExport(cleanupCtx, server, installRoot, exportRecord.ID)
	cleanupCancel()
	if cleanupErr != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.cleanupFailed")))
	}
	collectorStarted = false
	readyAt := time.Now().UTC()
	exportRecord, err = diagnostics.CommitLocalDiagnosticExport(store.LocalDiagnosticExportCommit{
		ID: exportRecord.ID, StorageRelativePath: finalArtifact.RelativePath, ArchiveName: finalArtifact.ArchiveName,
		SHA256: finalArtifact.SHA256, ArchiveBytes: finalArtifact.Size, UncompressedBytes: header.UncompressedBytes,
		WarningCount: header.WarningCount, Warnings: runtimeDiagnosticWarningPlaceholders(header.WarningCount),
		ReadyAt: readyAt, ExpiresAt: readyAt.Add(s.archives.Retention()),
	})
	if err != nil {
		return fail(errors.New(i18n.Text(req.Language, "aifar.diag.localCommitFailed")))
	}
	reservationHeld = false
	finishStep("success", "")
	succeeded = true
	finishTarget(recorder, target, "success", "")
	if logForServer != nil {
		logForServer.Info("%s", i18n.Text(req.Language, "aifar.diag.exportCompleted", exportRecord.ArchiveName))
	}
	return nil
}

func streamRuntimeDiagnosticCommand(
	ctx context.Context,
	streamer diagnosticCommandStreamer,
	server store.Server,
	command string,
	consume func(runtimeDiagnosticStreamHeader, io.Reader) error,
) (runtimeDiagnosticStreamHeader, error) {
	var header runtimeDiagnosticStreamHeader
	reader, writer := io.Pipe()
	type streamResult struct {
		result adapter.CommandStreamResult
		err    error
	}
	resultCh := make(chan streamResult, 1)
	go func() {
		result, err := streamer.StreamCommand(ctx, server, command, writer)
		if err != nil {
			_ = writer.CloseWithError(err)
		} else {
			_ = writer.Close()
		}
		resultCh <- streamResult{result: result, err: err}
	}()

	buffered := bufio.NewReaderSize(reader, runtimeDiagnosticHeaderLimit)
	headerBytes, readErr := buffered.ReadSlice('\n')
	if errors.Is(readErr, bufio.ErrBufferFull) || len(headerBytes) > runtimeDiagnosticHeaderLimit {
		_ = reader.CloseWithError(errors.New("runtime diagnostic stream header exceeds limit"))
		<-resultCh
		return header, errors.New("runtime diagnostic stream header exceeds limit")
	}
	if readErr != nil {
		_ = reader.CloseWithError(readErr)
		streamed := <-resultCh
		if streamed.err != nil {
			return header, streamed.err
		}
		return header, readErr
	}
	parsed, parseErr := parseRuntimeDiagnosticStreamHeader(string(headerBytes))
	if parseErr != nil {
		_ = reader.CloseWithError(parseErr)
		<-resultCh
		return header, parseErr
	}
	if consumeErr := consume(parsed, buffered); consumeErr != nil {
		_ = reader.CloseWithError(consumeErr)
		<-resultCh
		return header, consumeErr
	}
	streamed := <-resultCh
	if streamed.err != nil {
		return header, streamed.err
	}
	if streamed.result.Bytes < int64(len(headerBytes)) {
		return header, errors.New("runtime diagnostic stream byte count is invalid")
	}
	return parsed, nil
}

func runtimeDiagnosticEstimateBlockError(lang, blockReason string) error {
	switch blockReason {
	case "file-scan-limit-exceeded", "total-scan-limit-exceeded":
		return errors.New(i18n.Text(lang, "aifar.diag.scanLimitExceeded"))
	case "local-quota-exceeded":
		return errors.New(i18n.Text(lang, "aifar.diag.localQuotaExceeded"))
	case "local-disk-insufficient":
		return errors.New(i18n.Text(lang, "aifar.diag.localDiskInsufficient"))
	default:
		return errors.New(i18n.Text(lang, "aifar.diag.estimateRejected"))
	}
}

func (s Service) StreamRuntimeDiagnosticExport(ctx context.Context, req RuntimeDiagnosticStreamRequest, dst io.Writer) (int64, error) {
	if dst == nil {
		return 0, errors.New(i18n.Text(req.Language, "aifar.diag.streamFailed"))
	}
	diagnostics, ok := s.store.(runtimeDiagnosticsStore)
	if !ok {
		return 0, errors.New(i18n.Text(req.Language, "aifar.diag.storeMissing"))
	}
	exportRecord, _, server, installRoot, err := s.loadRuntimeDiagnosticArtifact(req.Export.ID, req.Instance.ID, req.Server.ID, req.Language, false)
	if err != nil {
		return 0, err
	}
	var n int64
	if exportRecord.StorageKind == "local" {
		if s.archives == nil {
			return 0, errors.New(i18n.Text(req.Language, "aifar.diag.storageMissing"))
		}
		file, openErr := s.archives.Open(exportRecord.StorageRelativePath)
		if openErr != nil {
			return 0, errors.New(i18n.Text(req.Language, "aifar.diag.streamFailed"))
		}
		defer file.Close()
		info, statErr := file.Stat()
		if statErr != nil || info.Size() != exportRecord.ArchiveBytes {
			return 0, errors.New(i18n.Text(req.Language, "aifar.diag.streamSizeMismatch"))
		}
		hasher := sha256.New()
		n, err = copyExactRuntimeDiagnosticArchive(ctx, io.MultiWriter(dst, hasher), file, exportRecord.ArchiveBytes)
		if err == nil && fmt.Sprintf("%x", hasher.Sum(nil)) != exportRecord.SHA256 {
			return n, errors.New(i18n.Text(req.Language, "aifar.diag.localChecksumFailed"))
		}
	} else {
		streamer, streamOK := s.remote.(diagnosticFileStreamer)
		if !streamOK {
			return 0, errors.New(i18n.Text(req.Language, "aifar.diag.streamUnsupported"))
		}
		absolutePath := path.Join(installRoot, "runtime", "diagnostics", exportRecord.RemoteRelativePath)
		n, err = streamer.StreamFile(ctx, server, absolutePath, dst)
	}
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
	var cleanupErr error
	if exportRecord.StorageKind == "local" {
		if s.archives == nil {
			cleanupErr = errors.New(i18n.Text(req.Language, "aifar.diag.storageMissing"))
		} else if exportRecord.StorageRelativePath != "" {
			cleanupErr = s.archives.Remove(exportRecord.StorageRelativePath)
		} else {
			cleanupErr = s.archives.RemovePartial(exportRecord.ID)
		}
	} else {
		cleanupErr = s.cleanupLegacyRuntimeDiagnosticExport(ctx, server, installRoot, exportRecord.ID)
	}
	if cleanupErr != nil {
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

type runtimeDiagnosticContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func copyExactRuntimeDiagnosticArchive(ctx context.Context, dst io.Writer, src io.Reader, expectedBytes int64) (int64, error) {
	if expectedBytes < 0 {
		return 0, errors.New("archive size mismatch")
	}
	reader := &runtimeDiagnosticContextReader{ctx: ctx, reader: src}
	n, err := io.CopyN(dst, reader, expectedBytes)
	if err != nil {
		return n, err
	}
	var extra [1]byte
	extraBytes, readErr := reader.Read(extra[:])
	if extraBytes != 0 || readErr != io.EOF {
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return n, readErr
		}
		return n, errors.New("archive size mismatch")
	}
	return n, nil
}

func (r *runtimeDiagnosticContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
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
	if strings.TrimSpace(data.FilterProgram) == "" {
		filterProgram, err := renderRuntimeDiagnosticFilterProgram()
		if err != nil {
			return "", err
		}
		data.FilterProgram = filterProgram
	}
	return renderEmbeddedRuntimeDiagnosticScript("aifar-runtime-diagnostics-export", content, data)
}

func renderRuntimeDiagnosticFilterProgram() (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-diagnostics-filter.awk")
	if err != nil {
		return "", err
	}
	return string(content), nil
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
	return s.cleanupRuntimeDiagnosticRemoteArtifacts(ctx, server, installRoot, exportID, false)
}

func (s Service) cleanupLegacyRuntimeDiagnosticExport(ctx context.Context, server store.Server, installRoot, exportID string) error {
	return s.cleanupRuntimeDiagnosticRemoteArtifacts(ctx, server, installRoot, exportID, true)
}

func (s Service) cleanupRuntimeDiagnosticRemoteArtifacts(ctx context.Context, server store.Server, installRoot, exportID string, removeFinal bool) error {
	if !runtimeDiagnosticExportIDPattern.MatchString(exportID) || strings.TrimSpace(installRoot) == "" || path.Clean(installRoot) == "/" || !path.IsAbs(path.Clean(installRoot)) || containsRuntimeDiagnosticControl(installRoot) {
		return errors.New("runtime diagnostic cleanup identity is invalid")
	}
	script, err := renderRuntimeDiagnosticCleanupScript(runtimeDiagnosticCleanupScriptData{
		InstallRoot: installerkit.ShellQuote(path.Clean(installRoot)),
		ExportID:    installerkit.ShellQuote(exportID),
		RemoveFinal: removeFinal,
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
	validateArtifact := func() error {
		if !runtimeDiagnosticSHA256Pattern.MatchString(exportRecord.SHA256) || exportRecord.ArchiveBytes < 0 {
			return errors.New(i18n.Text(lang, "aifar.diag.pathInvalid"))
		}
		if exportRecord.StorageKind == "local" {
			if _, _, err := validateRuntimeDiagnosticRelativePath(exportRecord.ID, exportRecord.StorageRelativePath, exportRecord.ArchiveName); err != nil || exportRecord.ArchiveBytes > runtimeDiagnosticMaxArchive {
				return errors.New(i18n.Text(lang, "aifar.diag.pathInvalid"))
			}
			return nil
		}
		if exportRecord.StorageKind != "remote" {
			return errors.New(i18n.Text(lang, "aifar.diag.pathInvalid"))
		}
		if _, _, err := validateRuntimeDiagnosticRelativePath(exportRecord.ID, exportRecord.RemoteRelativePath, exportRecord.ArchiveName); err != nil || exportRecord.ArchiveBytes > runtimeDiagnosticLegacyMaxArchive {
			return errors.New(i18n.Text(lang, "aifar.diag.pathInvalid"))
		}
		return nil
	}
	switch exportRecord.Status {
	case "ready":
		if err := validateArtifact(); err != nil {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", err
		}
		if !allowExpired && !exportRecord.ExpiresAt.After(now) {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportExpired"))
		}
	case "expired":
		if !allowExpired {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", errors.New(i18n.Text(lang, "aifar.diag.exportExpired"))
		}
		if err := validateArtifact(); err != nil {
			return store.DiagnosticExport{}, store.AppInstance{}, store.Server{}, "", err
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
