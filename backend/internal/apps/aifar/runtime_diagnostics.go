package aifar

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
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

func validateRuntimeDiagnosticEstimateRequest(req RuntimeDiagnosticRequest, diagnostics runtimeDiagnosticsStore, now time.Time) (string, []string, error) {
	if req.Instance.App != AppName || stringFromMetadata(metadataFromInstance(req.Instance), "orchestrationModel", "") != orchestrationModelK8sLikeV1 {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.instanceUnsupported"))
	}
	if strings.TrimSpace(req.Instance.ID) == "" || strings.TrimSpace(req.Server.ID) == "" || req.Instance.ServerID != req.Server.ID {
		return "", nil, errors.New(i18n.Text(req.Language, "aifar.diag.serverMismatch"))
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

func renderRuntimeDiagnosticEstimateScript(data runtimeDiagnosticEstimateScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-diagnostics-estimate.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "runtime-diagnostics-estimate.sh", "aifar-runtime-diagnostics-estimate", string(content), nil, data)
}
