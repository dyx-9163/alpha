package minio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type CheckRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
}

type CheckResult struct {
	Status  string
	Message string
	Details map[string]any
}

type minioRuntimeProbe struct {
	RuntimeStatus string
	HealthStatus  string
	ServiceStatus string
	PortStatus    string
	RuntimeSource string
}

func (p minioRuntimeProbe) details() map[string]any {
	return map[string]any{
		"runtimeStatus":      normalizeMinioProbeStatus(p.RuntimeStatus),
		"minioHealthStatus":  normalizeMinioProbeStatus(p.HealthStatus),
		"minioServiceStatus": normalizeMinioProbeStatus(p.ServiceStatus),
		"minioPortStatus":    normalizeMinioProbeStatus(p.PortStatus),
		"minioRuntimeSource": strings.TrimSpace(p.RuntimeSource),
	}
}

func (p minioRuntimeProbe) available() bool {
	return normalizeMinioProbeStatus(p.RuntimeStatus) == "available"
}

func (s Service) Check(ctx context.Context, req CheckRequest, log Logger, targetLog targetLogger) (CheckResult, error) {
	copy := CheckCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newCheckStepRunner(logForServer, recorder, target, copy)
	details := map[string]any{
		"checkedAt": time.Now().UTC().Format(time.RFC3339),
		"topology":  normalizeTopology(req.Instance.Topology),
		"apiPort":   instanceAPIPort(req.Instance),
		"endpoint":  metadataString(appMetadata(req.Instance), "endpoint"),
	}

	fail := func(err error) (CheckResult, error) {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		failureDetails := copyDetails(details)
		failureDetails["error"] = err.Error()
		_ = s.markInstanceStatus(req.Instance, "unavailable", failureDetails)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "unavailable", Message: msg, Details: failureDetails}, err
	}

	var runtime minioRuntimeProbe
	if err := step(1, "check-runtime", copy.CheckRuntime, func() error {
		var checkErr error
		runtime, checkErr = s.probeMinIORuntime(ctx, req.Server, req.Instance, logForServer)
		mergeMinioDetails(details, runtime.details())
		return checkErr
	}); err != nil {
		return fail(err)
	}
	if !runtime.available() {
		return fail(errors.New("MinIO health endpoint is not available"))
	}

	if err := step(2, "update-instance", copy.UpdateInstance, func() error {
		return s.markInstanceStatus(req.Instance, "available", details)
	}); err != nil {
		return fail(err)
	}

	msg := fmt.Sprintf(copy.Checked, "available")
	logForServer.Info("%s", msg)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: "available", Message: msg, Details: details}, nil
}

func (s Service) probeMinIORuntime(ctx context.Context, server store.Server, instance store.AppInstance, log Logger) (minioRuntimeProbe, error) {
	apiPort := instanceAPIPort(instance)
	metadata := appMetadata(instance)
	serviceName := metadataString(metadata, "serviceName")
	if serviceName == "" {
		serviceName = "aifar-minio"
	}
	legacyServiceName := fmt.Sprintf("aifar-minio-%d", apiPort)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", apiPort)
	cmd := fmt.Sprintf(`AIFAR_MINIO_RUNTIME_PROBE=1
API_PORT=%d
SERVICE_NAME=%s
LEGACY_SERVICE_NAME=%s
HEALTH_URL=%s
runtime_status="unavailable"
health_status="failed"
service_status="unknown"
port_status="unknown"
runtime_source="none"
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null || systemctl is-active --quiet "$LEGACY_SERVICE_NAME" 2>/dev/null; then
    service_status="running"
  else
    service_status="offline"
  fi
fi
if command -v ss >/dev/null 2>&1; then
  if ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq '(^|[^0-9])'"$API_PORT"'$'; then
    port_status="listening"
  else
    port_status="closed"
  fi
fi
if command -v curl >/dev/null 2>&1; then
  runtime_source="curl"
  if curl -fsS --max-time 5 "$HEALTH_URL" >/dev/null 2>&1; then
    health_status="running"
  fi
elif command -v wget >/dev/null 2>&1; then
  runtime_source="wget"
  if wget -T 5 -qO- "$HEALTH_URL" >/dev/null 2>&1; then
    health_status="running"
  fi
else
  health_status="missing"
fi
if [ "$health_status" = "running" ]; then
  runtime_status="available"
elif [ "$health_status" = "missing" ] && [ "$service_status" = "running" ] && [ "$port_status" = "listening" ]; then
  runtime_status="available"
  runtime_source="systemd-port"
fi
printf 'runtimeStatus=%%s\n' "$runtime_status"
printf 'minioHealthStatus=%%s\n' "$health_status"
printf 'minioServiceStatus=%%s\n' "$service_status"
printf 'minioPortStatus=%%s\n' "$port_status"
printf 'minioRuntimeSource=%%s\n' "$runtime_source"
if [ "$runtime_status" = "available" ]; then exit 0; fi
exit 1`,
		apiPort,
		installerkit.ShellQuote(serviceName),
		installerkit.ShellQuote(legacyServiceName),
		installerkit.ShellQuote(healthURL),
	)
	result, err := s.remote.Run(ctx, server, cmd)
	installerkit.LogCommandResult(result, err, log)
	probe := parseMinIORuntimeProbe(result.Stdout)
	if err != nil {
		return probe, fmt.Errorf("minio remote command failed: %w", err)
	}
	if !probe.available() {
		return probe, errors.New("MinIO health endpoint is not available")
	}
	return probe, nil
}

func parseMinIORuntimeProbe(stdout string) minioRuntimeProbe {
	values := parseMinIOShellKeyValues(stdout)
	probe := minioRuntimeProbe{
		RuntimeStatus: values["runtimeStatus"],
		HealthStatus:  values["minioHealthStatus"],
		ServiceStatus: values["minioServiceStatus"],
		PortStatus:    values["minioPortStatus"],
		RuntimeSource: values["minioRuntimeSource"],
	}
	if probe.RuntimeStatus == "" && strings.TrimSpace(stdout) != "" {
		probe.RuntimeStatus = "available"
		probe.HealthStatus = "running"
		probe.RuntimeSource = "command"
	}
	if probe.RuntimeStatus == "" {
		probe.RuntimeStatus = "unavailable"
	}
	return probe
}

func parseMinIOShellKeyValues(stdout string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		values[key] = strings.TrimSpace(value)
	}
	return values
}

func normalizeMinioProbeStatus(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "ok", "success", "active", "available", "running":
		return "available"
	case "listening":
		return "listening"
	case "failed", "error", "missing", "stopped", "offline", "unavailable", "closed":
		return "unavailable"
	default:
		return strings.TrimSpace(value)
	}
}

func minioCheckSteps(copy CheckCopy) []stepDef {
	return []stepDef{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}

func newCheckStepRunner(log Logger, recorder stepRecorder, target string, copy CheckCopy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := minioCheckSteps(copy)
	return func(stepIndex int, stepName, label string, fn func() error) error {
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(copy.StepStart, stepIndex, len(steps), label)
		if err := fn(); err != nil {
			log.Error(copy.StepFailed, stepIndex, len(steps), label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(copy.StepDone, stepIndex, len(steps), label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	metadata := appMetadata(instance)
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	if details == nil {
		details = map[string]any{}
	}
	if _, ok := details["checkedAt"]; !ok {
		details["checkedAt"] = checkedAt
	}
	metadata["lastCheck"] = map[string]any{
		"status":    status,
		"checkedAt": checkedAt,
		"details":   details,
	}
	data, _ := json.Marshal(metadata)
	instance.Metadata = string(data)
	instance.Status = status
	_, err := s.store.SaveAppInstance(instance)
	return err
}

func mergeMinioDetails(dst map[string]any, src map[string]any) {
	for key, value := range src {
		if fmt.Sprint(value) != "" {
			dst[key] = value
		}
	}
}

func copyDetails(src map[string]any) map[string]any {
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
