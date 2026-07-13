package minio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installflow"
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

type CleanupEstimateRequest struct {
	Instance      store.AppInstance
	Server        store.Server
	RetentionDays int
}

type CleanupEstimateResult struct {
	Status        string         `json:"status"`
	RetentionDays int            `json:"retentionDays"`
	ObjectCount   int64          `json:"objectCount"`
	Bytes         int64          `json:"bytes"`
	Source        string         `json:"source"`
	Details       map[string]any `json:"details,omitempty"`
}

type CleanupPolicyRequest struct {
	Instance       store.AppInstance
	Server         store.Server
	Bucket         string
	Prefix         string
	RetentionDays  int
	Enabled        bool
	ExistingRuleID string
}

type CleanupPolicyResult struct {
	Status        string         `json:"status"`
	Enabled       bool           `json:"enabled"`
	Bucket        string         `json:"bucket"`
	Prefix        string         `json:"prefix,omitempty"`
	RetentionDays int            `json:"retentionDays"`
	RuleID        string         `json:"ruleId,omitempty"`
	Source        string         `json:"source"`
	Details       map[string]any `json:"details,omitempty"`
}

const defaultMinioCleanupRetentionDays = 30

type minioRuntimeProbe struct {
	RuntimeStatus                string
	HealthStatus                 string
	ServiceStatus                string
	PortStatus                   string
	RuntimeSource                string
	StorageTotalBytes            int64
	StorageUsedBytes             int64
	StorageAvailableBytes        int64
	StorageUsagePercent          int64
	StoragePathCount             int64
	StorageDisks                 []minioRuntimeDisk
	CleanupEstimateStatus        string
	CleanupEstimateRetentionDays int64
	CleanupEstimateObjectCount   int64
	CleanupEstimateBytes         int64
	CleanupEstimateSource        string
}

type minioRuntimeDisk struct {
	Index          int64
	Path           string
	Device         string
	MountPoint     string
	TotalBytes     int64
	UsedBytes      int64
	AvailableBytes int64
	UsagePercent   int64
}

func (p minioRuntimeProbe) details() map[string]any {
	details := map[string]any{
		"runtimeStatus":      normalizeMinioProbeStatus(p.RuntimeStatus),
		"minioHealthStatus":  normalizeMinioProbeStatus(p.HealthStatus),
		"minioServiceStatus": normalizeMinioProbeStatus(p.ServiceStatus),
		"minioPortStatus":    normalizeMinioProbeStatus(p.PortStatus),
		"minioRuntimeSource": strings.TrimSpace(p.RuntimeSource),
	}
	if p.StoragePathCount > 0 || p.StorageTotalBytes > 0 {
		details["minioStorageTotalBytes"] = p.StorageTotalBytes
		details["minioStorageUsedBytes"] = p.StorageUsedBytes
		details["minioStorageAvailableBytes"] = p.StorageAvailableBytes
		details["minioStorageUsagePercent"] = p.StorageUsagePercent
		details["minioStoragePathCount"] = p.StoragePathCount
	}
	if len(p.StorageDisks) > 0 {
		disks := make([]map[string]any, 0, len(p.StorageDisks))
		for _, disk := range p.StorageDisks {
			disks = append(disks, map[string]any{
				"index":          disk.Index,
				"path":           strings.TrimSpace(disk.Path),
				"device":         strings.TrimSpace(disk.Device),
				"mountPoint":     strings.TrimSpace(disk.MountPoint),
				"totalBytes":     disk.TotalBytes,
				"usedBytes":      disk.UsedBytes,
				"availableBytes": disk.AvailableBytes,
				"usagePercent":   disk.UsagePercent,
			})
		}
		details["minioStorageDisks"] = disks
	}
	if strings.TrimSpace(p.CleanupEstimateStatus) != "" {
		details["cleanupEstimateStatus"] = normalizeMinioProbeStatus(p.CleanupEstimateStatus)
		details["cleanupEstimateRetentionDays"] = p.CleanupEstimateRetentionDays
		details["cleanupEstimateObjectCount"] = p.CleanupEstimateObjectCount
		details["cleanupEstimateBytes"] = p.CleanupEstimateBytes
		details["cleanupEstimateSource"] = strings.TrimSpace(p.CleanupEstimateSource)
	}
	return details
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
	installflow.StartTarget(recorder, target)
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
	return s.probeMinIORuntimeWithRetention(ctx, server, instance, defaultMinioCleanupRetentionDays, log)
}

func (s Service) EstimateCleanup(ctx context.Context, req CleanupEstimateRequest, log Logger) (CleanupEstimateResult, error) {
	retentionDays := normalizeCleanupRetentionDays(req.RetentionDays)
	probe, err := s.probeMinIORuntimeWithRetention(ctx, req.Server, req.Instance, retentionDays, log)
	status := normalizeMinioProbeStatus(probe.CleanupEstimateStatus)
	if status == "" {
		status = "unavailable"
	}
	result := CleanupEstimateResult{
		Status:        status,
		RetentionDays: int(probe.CleanupEstimateRetentionDays),
		ObjectCount:   probe.CleanupEstimateObjectCount,
		Bytes:         probe.CleanupEstimateBytes,
		Source:        strings.TrimSpace(probe.CleanupEstimateSource),
		Details:       probe.details(),
	}
	if result.RetentionDays <= 0 {
		result.RetentionDays = retentionDays
	}
	return result, err
}

func (s Service) ApplyCleanupPolicy(ctx context.Context, req CleanupPolicyRequest, log Logger) (CleanupPolicyResult, error) {
	bucket := normalizeCleanupPolicyBucket(req.Bucket)
	if bucket == "" {
		bucket = "aifar"
	}
	prefix := normalizeCleanupPolicyPrefix(req.Prefix)
	retentionDays := normalizeCleanupRetentionDays(req.RetentionDays)
	apiPort := instanceAPIPort(req.Instance)
	installRoot := installerkit.InstallRoot(installerkit.RemoteDeployDir(req.Server.DeployDir), "minio")
	enabled := "false"
	if req.Enabled {
		enabled = "true"
	}
	cmd := fmt.Sprintf(`AIFAR_MINIO_CLEANUP_POLICY=1
API_PORT=%d
INSTALL_ROOT=%s
BUCKET=%s
PREFIX=%s
RETENTION_DAYS=%d
ENABLED=%s
PREVIOUS_RULE_ID=%s
MC="$INSTALL_ROOT/bin/mc"
ENV_FILE="$INSTALL_ROOT/conf/minio.env"
cleanup_status="unavailable"
rule_id=""
cleanup_source="mc-ilm"
TARGET="aifar-local/$BUCKET"
fail_policy() {
  printf 'cleanupPolicyStatus=unavailable\n'
  printf 'cleanupPolicyBucket=%%s\n' "$BUCKET"
  printf 'cleanupPolicyPrefix=%%s\n' "$PREFIX"
  printf 'cleanupPolicyRetentionDays=%%s\n' "$RETENTION_DAYS"
  printf 'cleanupPolicyRuleID=%%s\n' "$rule_id"
  printf 'cleanupPolicySource=%%s\n' "$cleanup_source"
  exit 1
}
[ -x "$MC" ] || { echo "MinIO client not found: $MC"; fail_policy; }
[ -r "$ENV_FILE" ] || { echo "MinIO env file not found: $ENV_FILE"; fail_policy; }
# shellcheck disable=SC1090
. "$ENV_FILE"
[ -n "${MINIO_ROOT_USER:-}" ] || { echo "MINIO_ROOT_USER is empty"; fail_policy; }
[ -n "${MINIO_ROOT_PASSWORD:-}" ] || { echo "MINIO_ROOT_PASSWORD is empty"; fail_policy; }
MC_CONFIG_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t aifar-minio-mc)"
export MC_CONFIG_DIR
cleanup_mc_config() { rm -rf "$MC_CONFIG_DIR" >/dev/null 2>&1 || true; }
trap cleanup_mc_config EXIT
"$MC" alias set aifar-local "http://127.0.0.1:$API_PORT" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" --api S3v4 >/dev/null 2>&1 || fail_policy
if [ -n "$PREVIOUS_RULE_ID" ]; then
  "$MC" ilm rule rm --id "$PREVIOUS_RULE_ID" "$TARGET" >/dev/null 2>&1 || true
fi
if [ "$ENABLED" = "true" ]; then
  if [ -n "$PREFIX" ]; then
    add_output="$("$MC" --json ilm rule add --prefix "$PREFIX" --expire-days "$RETENTION_DAYS" "$TARGET" 2>&1)" || { printf '%%s\n' "$add_output"; fail_policy; }
  else
    add_output="$("$MC" --json ilm rule add --expire-days "$RETENTION_DAYS" "$TARGET" 2>&1)" || { printf '%%s\n' "$add_output"; fail_policy; }
  fi
  rule_id="$(printf '%%s\n' "$add_output" | sed -n 's/.*"ruleId"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p; s/.*"ruleID"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p; s/.*"id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
  cleanup_status="enabled"
else
  cleanup_status="disabled"
fi
printf 'cleanupPolicyStatus=%%s\n' "$cleanup_status"
printf 'cleanupPolicyBucket=%%s\n' "$BUCKET"
printf 'cleanupPolicyPrefix=%%s\n' "$PREFIX"
printf 'cleanupPolicyRetentionDays=%%s\n' "$RETENTION_DAYS"
printf 'cleanupPolicyRuleID=%%s\n' "$rule_id"
printf 'cleanupPolicySource=%%s\n' "$cleanup_source"`,
		apiPort,
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(bucket),
		installerkit.ShellQuote(prefix),
		retentionDays,
		enabled,
		installerkit.ShellQuote(strings.TrimSpace(req.ExistingRuleID)),
	)
	result, err := s.remote.Run(ctx, req.Server, cmd)
	installerkit.LogCommandResult(result, err, log)
	policy := parseMinIOCleanupPolicyResult(result.Stdout)
	if policy.Bucket == "" {
		policy.Bucket = bucket
	}
	if policy.Prefix == "" {
		policy.Prefix = prefix
	}
	if policy.RetentionDays <= 0 {
		policy.RetentionDays = retentionDays
	}
	if policy.Source == "" {
		policy.Source = "mc-ilm"
	}
	policy.Enabled = policy.Status == "enabled"
	if err != nil {
		return policy, fmt.Errorf("minio cleanup policy command failed: %w", err)
	}
	if policy.Status != "enabled" && policy.Status != "disabled" {
		if req.Enabled {
			policy.Status = "enabled"
			policy.Enabled = true
		} else {
			policy.Status = "disabled"
			policy.Enabled = false
		}
	}
	return policy, nil
}

func (s Service) probeMinIORuntimeWithRetention(ctx context.Context, server store.Server, instance store.AppInstance, retentionDays int, log Logger) (minioRuntimeProbe, error) {
	retentionDays = normalizeCleanupRetentionDays(retentionDays)
	apiPort := instanceAPIPort(instance)
	metadata := appMetadata(instance)
	serviceName := metadataString(metadata, "serviceName")
	if serviceName == "" {
		serviceName = "aifar-minio"
	}
	installRoot := installerkit.InstallRoot(installerkit.RemoteDeployDir(server.DeployDir), "minio")
	legacyServiceName := fmt.Sprintf("aifar-minio-%d", apiPort)
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/minio/health/live", apiPort)
	dataDirs := minioProbeDataDirs(instance, installRoot)
	cmd := fmt.Sprintf(`AIFAR_MINIO_RUNTIME_PROBE=1
API_PORT=%d
SERVICE_NAME=%s
LEGACY_SERVICE_NAME=%s
HEALTH_URL=%s
INSTALL_ROOT=%s
DATA_DIRS=%s
RETENTION_DAYS=%d
runtime_status="unavailable"
health_status="failed"
service_status="unknown"
port_status="unknown"
runtime_source="none"
storage_total=0
storage_used=0
storage_available=0
storage_paths=0
cleanup_status="unavailable"
cleanup_objects=0
cleanup_bytes=0
cleanup_source="none"
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
if command -v df >/dev/null 2>&1; then
  seen_mounts="|"
  for data_dir in $DATA_DIRS; do
    [ -n "$data_dir" ] || continue
    if [ ! -d "$data_dir" ]; then
      continue
    fi
    df_line="$(df -PB1 "$data_dir" 2>/dev/null | awk 'NR==2 {print $1 "|" $2 "|" $3 "|" $4 "|" $6}')"
    [ -n "$df_line" ] || continue
    fs="$(printf '%%s' "$df_line" | cut -d'|' -f1)"
    total="$(printf '%%s' "$df_line" | cut -d'|' -f2)"
    used="$(printf '%%s' "$df_line" | cut -d'|' -f3)"
    available="$(printf '%%s' "$df_line" | cut -d'|' -f4)"
    mount_point="$(printf '%%s' "$df_line" | cut -d'|' -f5)"
    mount_key="$fs@$mount_point"
    case "$seen_mounts" in
      *"|$mount_key|"*) continue ;;
    esac
    seen_mounts="$seen_mounts$mount_key|"
    case "$total:$used:$available" in
      *[!0-9:]*|"") continue ;;
    esac
    disk_index=$((storage_paths + 1))
    disk_percent=0
    if [ "$total" -gt 0 ]; then
      disk_percent=$((used * 100 / total))
    fi
    printf 'minioStorageDisk%%sPath=%%s\n' "$disk_index" "$data_dir"
    printf 'minioStorageDisk%%sDevice=%%s\n' "$disk_index" "$fs"
    printf 'minioStorageDisk%%sMountPoint=%%s\n' "$disk_index" "$mount_point"
    printf 'minioStorageDisk%%sTotalBytes=%%s\n' "$disk_index" "$total"
    printf 'minioStorageDisk%%sUsedBytes=%%s\n' "$disk_index" "$used"
    printf 'minioStorageDisk%%sAvailableBytes=%%s\n' "$disk_index" "$available"
    printf 'minioStorageDisk%%sUsagePercent=%%s\n' "$disk_index" "$disk_percent"
    storage_total=$((storage_total + total))
    storage_used=$((storage_used + used))
    storage_available=$((storage_available + available))
    storage_paths=$((storage_paths + 1))
  done
fi
storage_percent=0
if [ "$storage_total" -gt 0 ]; then
  storage_percent=$((storage_used * 100 / storage_total))
fi
MC="$INSTALL_ROOT/bin/mc"
ENV_FILE="$INSTALL_ROOT/conf/minio.env"
if [ "$runtime_status" = "available" ] && [ -x "$MC" ] && [ -r "$ENV_FILE" ]; then
  cleanup_source="mc"
  # shellcheck disable=SC1090
  . "$ENV_FILE"
  if [ -n "${MINIO_ROOT_USER:-}" ] && [ -n "${MINIO_ROOT_PASSWORD:-}" ]; then
    MC_CONFIG_DIR="$(mktemp -d 2>/dev/null || mktemp -d -t aifar-minio-mc)"
    export MC_CONFIG_DIR
    if "$MC" alias set aifar-local "http://127.0.0.1:$API_PORT" "$MINIO_ROOT_USER" "$MINIO_ROOT_PASSWORD" --api S3v4 >/dev/null 2>&1; then
      estimate_output="$("$MC" find aifar-local --older-than "${RETENTION_DAYS}d" --json 2>/dev/null | awk '
        BEGIN { count=0; bytes=0 }
        {
          if (match($0, /"size"[[:space:]]*:[[:space:]]*[0-9]+/)) {
            text=substr($0, RSTART, RLENGTH)
            sub(/.*:/, "", text)
            bytes += text + 0
            count += 1
          }
        }
        END { printf "objects=%%d\nbytes=%%d\n", count, bytes }
      ')"
      cleanup_objects="$(printf '%%s\n' "$estimate_output" | awk -F= '$1=="objects"{print $2}' | tail -n 1)"
      cleanup_bytes="$(printf '%%s\n' "$estimate_output" | awk -F= '$1=="bytes"{print $2}' | tail -n 1)"
      case "$cleanup_objects:$cleanup_bytes" in
        *[!0-9:]*|"") cleanup_status="unavailable" ;;
        *) cleanup_status="available" ;;
      esac
    fi
    rm -rf "$MC_CONFIG_DIR" >/dev/null 2>&1 || true
  fi
fi
printf 'runtimeStatus=%%s\n' "$runtime_status"
printf 'minioHealthStatus=%%s\n' "$health_status"
printf 'minioServiceStatus=%%s\n' "$service_status"
printf 'minioPortStatus=%%s\n' "$port_status"
printf 'minioRuntimeSource=%%s\n' "$runtime_source"
printf 'minioStorageTotalBytes=%%s\n' "$storage_total"
printf 'minioStorageUsedBytes=%%s\n' "$storage_used"
printf 'minioStorageAvailableBytes=%%s\n' "$storage_available"
printf 'minioStorageUsagePercent=%%s\n' "$storage_percent"
printf 'minioStoragePathCount=%%s\n' "$storage_paths"
printf 'minioStorageDiskCount=%%s\n' "$storage_paths"
printf 'cleanupEstimateStatus=%%s\n' "$cleanup_status"
printf 'cleanupEstimateRetentionDays=%%s\n' "$RETENTION_DAYS"
printf 'cleanupEstimateObjectCount=%%s\n' "${cleanup_objects:-0}"
printf 'cleanupEstimateBytes=%%s\n' "${cleanup_bytes:-0}"
printf 'cleanupEstimateSource=%%s\n' "$cleanup_source"
if [ "$runtime_status" = "available" ]; then exit 0; fi
exit 1`,
		apiPort,
		installerkit.ShellQuote(serviceName),
		installerkit.ShellQuote(legacyServiceName),
		installerkit.ShellQuote(healthURL),
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(strings.Join(dataDirs, " ")),
		retentionDays,
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

func normalizeCleanupRetentionDays(days int) int {
	if days <= 0 {
		return defaultMinioCleanupRetentionDays
	}
	if days > 3650 {
		return 3650
	}
	return days
}

func normalizeCleanupPolicyBucket(bucket string) string {
	bucket = strings.ToLower(strings.TrimSpace(bucket))
	if bucket == "" {
		return ""
	}
	if strings.ContainsAny(bucket, `/\`) || strings.IndexFunc(bucket, func(r rune) bool { return r <= ' ' }) >= 0 {
		return ""
	}
	return bucket
}

func normalizeCleanupPolicyPrefix(prefix string) string {
	prefix = strings.TrimSpace(prefix)
	prefix = strings.TrimLeft(prefix, "/")
	if strings.IndexFunc(prefix, func(r rune) bool { return r == '\n' || r == '\r' || r == 0 }) >= 0 {
		return ""
	}
	return prefix
}

func parseMinIORuntimeProbe(stdout string) minioRuntimeProbe {
	values := parseMinIOShellKeyValues(stdout)
	probe := minioRuntimeProbe{
		RuntimeStatus:                values["runtimeStatus"],
		HealthStatus:                 values["minioHealthStatus"],
		ServiceStatus:                values["minioServiceStatus"],
		PortStatus:                   values["minioPortStatus"],
		RuntimeSource:                values["minioRuntimeSource"],
		StorageTotalBytes:            int64Value(values["minioStorageTotalBytes"]),
		StorageUsedBytes:             int64Value(values["minioStorageUsedBytes"]),
		StorageAvailableBytes:        int64Value(values["minioStorageAvailableBytes"]),
		StorageUsagePercent:          int64Value(values["minioStorageUsagePercent"]),
		StoragePathCount:             int64Value(values["minioStoragePathCount"]),
		StorageDisks:                 parseMinIOStorageDisks(values),
		CleanupEstimateStatus:        values["cleanupEstimateStatus"],
		CleanupEstimateRetentionDays: int64Value(values["cleanupEstimateRetentionDays"]),
		CleanupEstimateObjectCount:   int64Value(values["cleanupEstimateObjectCount"]),
		CleanupEstimateBytes:         int64Value(values["cleanupEstimateBytes"]),
		CleanupEstimateSource:        values["cleanupEstimateSource"],
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

func parseMinIOCleanupPolicyResult(stdout string) CleanupPolicyResult {
	values := parseMinIOShellKeyValues(stdout)
	status := strings.ToLower(strings.TrimSpace(values["cleanupPolicyStatus"]))
	if status == "" {
		status = "unavailable"
	}
	return CleanupPolicyResult{
		Status:        status,
		Enabled:       status == "enabled",
		Bucket:        strings.TrimSpace(values["cleanupPolicyBucket"]),
		Prefix:        strings.TrimSpace(values["cleanupPolicyPrefix"]),
		RetentionDays: int(int64Value(values["cleanupPolicyRetentionDays"])),
		RuleID:        strings.TrimSpace(values["cleanupPolicyRuleID"]),
		Source:        strings.TrimSpace(values["cleanupPolicySource"]),
		Details: map[string]any{
			"rawOutput": strings.TrimSpace(stdout),
		},
	}
}

func parseMinIOStorageDisks(values map[string]string) []minioRuntimeDisk {
	count := int(int64Value(values["minioStorageDiskCount"]))
	if count <= 0 {
		return nil
	}
	disks := make([]minioRuntimeDisk, 0, count)
	for index := 1; index <= count; index++ {
		prefix := fmt.Sprintf("minioStorageDisk%d", index)
		path := strings.TrimSpace(values[prefix+"Path"])
		totalBytes := int64Value(values[prefix+"TotalBytes"])
		if path == "" || totalBytes <= 0 {
			continue
		}
		disks = append(disks, minioRuntimeDisk{
			Index:          int64(index),
			Path:           path,
			Device:         strings.TrimSpace(values[prefix+"Device"]),
			MountPoint:     strings.TrimSpace(values[prefix+"MountPoint"]),
			TotalBytes:     totalBytes,
			UsedBytes:      int64Value(values[prefix+"UsedBytes"]),
			AvailableBytes: int64Value(values[prefix+"AvailableBytes"]),
			UsagePercent:   int64Value(values[prefix+"UsagePercent"]),
		})
	}
	return disks
}

func minioProbeDataDirs(instance store.AppInstance, installRoot string) []string {
	metadata := appMetadata(instance)
	dirs := stringSliceMetadata(metadata, "dataDirs")
	if len(dirs) == 0 {
		dirs = cleanStrings([]string{metadataString(metadata, "dataDir")})
	}
	if len(dirs) == 0 {
		dirs = []string{installerkit.InstallRoot(installerkit.RemoteDeployDir(""), "minio") + "/data"}
		if strings.TrimSpace(installRoot) != "" {
			dirs = []string{strings.TrimRight(installRoot, "/") + "/data"}
		}
	}
	out := make([]string, 0, len(dirs))
	seen := map[string]bool{}
	for _, dir := range dirs {
		dir = strings.TrimSpace(dir)
		if dir == "" || !strings.HasPrefix(dir, "/") || strings.IndexFunc(dir, func(r rune) bool { return r <= ' ' }) >= 0 {
			continue
		}
		dir = "/" + strings.Trim(dir, "/")
		if !seen[dir] {
			seen[dir] = true
			out = append(out, dir)
		}
	}
	if len(out) == 0 && strings.TrimSpace(installRoot) != "" {
		out = append(out, strings.TrimRight(installRoot, "/")+"/data")
	}
	return out
}

func int64Value(value string) int64 {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0
	}
	return n
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
	runner := installflow.Runner{
		Log:      log,
		Recorder: recorder,
		Target:   target,
		Steps:    steps,
		Messages: installflow.Messages{
			StepStart:  copy.StepStart,
			StepDone:   copy.StepDone,
			StepFailed: copy.StepFailed,
		},
	}
	return func(stepIndex int, stepName, label string, fn func() error) error {
		return runner.Run(stepIndex, stepName, label, fn)
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
