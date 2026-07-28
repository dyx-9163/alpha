package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

type mysqlRuntimeProbe struct {
	RuntimeStatus string
	PingStatus    string
	ServiceStatus string
	PortStatus    string
	RuntimeSource string
}

func (p mysqlRuntimeProbe) details() map[string]any {
	return map[string]any{
		"runtimeStatus":      normalizeRuntimeProbeStatus(p.RuntimeStatus),
		"mysqlPingStatus":    normalizeRuntimeProbeStatus(p.PingStatus),
		"mysqlServiceStatus": normalizeRuntimeProbeStatus(p.ServiceStatus),
		"mysqlPortStatus":    normalizeRuntimeProbeStatus(p.PortStatus),
		"mysqlRuntimeSource": strings.TrimSpace(p.RuntimeSource),
	}
}

func (p mysqlRuntimeProbe) runtimeRunning() bool {
	return normalizeRuntimeProbeStatus(p.RuntimeStatus) == "running"
}

func (p mysqlRuntimeProbe) pingRunning() bool {
	return normalizeRuntimeProbeStatus(p.PingStatus) == "running"
}

func (s Service) probeMySQLRuntime(ctx context.Context, server store.Server, instance store.AppInstance, credential store.Credential, log Logger) (mysqlRuntimeProbe, error) {
	port := instancePort(instance)
	installRoot := remoteInstallRoot(server, "mysql", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "mysql", instance.Version)
	metadata := appMetadata(instance)
	serviceName := metadataString(metadata, "serviceName")
	if serviceName == "" {
		serviceName = "aifar-mysql"
	}
	legacyServiceName := fmt.Sprintf("aifar-mysql-%d", port)
	result, err := s.runMySQLCredentialCommand(ctx, server, instance, credential, log, func(work string) string {
		return fmt.Sprintf(`AIFAR_MYSQL_RUNTIME_PROBE=1
INSTALL_ROOT=%s
LEGACY_ROOT=%s
PORT=%d
SECRET_CONTEXT=%s
SERVICE_NAME=%s
LEGACY_SERVICE_NAME=%s
if [ ! -x "$INSTALL_ROOT/mysql/bin/mysqladmin" ] && [ -x "$LEGACY_ROOT/mysql/bin/mysqladmin" ]; then INSTALL_ROOT="$LEGACY_ROOT"; fi
ping_status="offline"
service_status="unknown"
port_status="unknown"
runtime_status="offline"
runtime_source="none"
if command -v systemctl >/dev/null 2>&1; then
  if systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null || systemctl is-active --quiet "$LEGACY_SERVICE_NAME" 2>/dev/null; then
    service_status="running"
  else
    service_status="offline"
  fi
fi
if [ -x "$INSTALL_ROOT/mysql/bin/mysqladmin" ]; then
  if "$INSTALL_ROOT/mysql/bin/mysqladmin" --defaults-file="$SECRET_CONTEXT" --protocol=tcp -h 127.0.0.1 -P "$PORT" ping >/dev/null 2>&1; then
    ping_status="running"
  else
    ping_status="offline"
  fi
else
  ping_status="missing"
fi
if command -v ss >/dev/null 2>&1; then
  if ss -ltn 2>/dev/null | awk '{print $4}' | grep -Eq '(^|[^0-9])'"$PORT"'$'; then
    port_status="listening"
  else
    port_status="closed"
  fi
fi
if [ "$ping_status" = "running" ]; then
  runtime_status="running"
  runtime_source="mysqladmin"
fi
printf 'runtimeStatus=%%s\n' "$runtime_status"
printf 'mysqlPingStatus=%%s\n' "$ping_status"
printf 'mysqlServiceStatus=%%s\n' "$service_status"
printf 'mysqlPortStatus=%%s\n' "$port_status"
printf 'mysqlRuntimeSource=%%s\n' "$runtime_source"
if [ "$runtime_status" = "running" ]; then exit 0; fi
exit 1`,
			installerkit.ShellQuote(installRoot),
			installerkit.ShellQuote(legacyInstallRoot),
			port,
			installerkit.ShellQuote(path.Join(work, "secret-context.cnf")),
			installerkit.ShellQuote(serviceName),
			installerkit.ShellQuote(legacyServiceName),
		)
	})
	probe := parseMySQLRuntimeProbe(result.Stdout)
	if err != nil {
		return probe, fmt.Errorf("mysql remote command failed: %w", err)
	}
	if !probe.runtimeRunning() {
		return probe, errors.New("MySQL runtime is not running")
	}
	return probe, nil
}

func (s Service) detectInnoDBPrimary(ctx context.Context, server store.Server, instance store.AppInstance, credential store.Credential, log Logger) (string, error) {
	port := instancePort(instance)
	installRoot := remoteInstallRoot(server, "mysql", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "mysql", instance.Version)
	query := "SELECT CONCAT(MEMBER_HOST, ':', MEMBER_PORT) FROM performance_schema.replication_group_members WHERE MEMBER_ROLE='PRIMARY' LIMIT 1"
	result, err := s.runMySQLCredentialCommand(ctx, server, instance, credential, log, func(work string) string {
		return fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/mysql/bin/mysql\" ] && [ -x \"$LEGACY_ROOT/mysql/bin/mysql\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\n\"$INSTALL_ROOT/mysql/bin/mysql\" --defaults-file=%s --protocol=tcp -h 127.0.0.1 -P %d --batch --skip-column-names -e %s",
			installerkit.ShellQuote(installRoot),
			installerkit.ShellQuote(legacyInstallRoot),
			installerkit.ShellQuote(path.Join(work, "secret-context.cnf")),
			port,
			installerkit.ShellQuote(query),
		)
	})
	if err != nil {
		return "", err
	}
	primary := firstNonEmptyLine(result.Stdout)
	if primary == "" {
		return "", errors.New("InnoDB Cluster primary was not returned")
	}
	return primary, nil
}

func (s Service) runMySQLCredentialCommand(ctx context.Context, server store.Server, instance store.AppInstance, credential store.Credential, log Logger, command func(work string) string) (result installerkit.CommandResult, retErr error) {
	if strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return result, mysqlOperationError(MySQLCredentialUnavailable)
	}
	work := mysqlBackupWorkDir(store.NewID("credential"))
	if _, err := s.remote.Run(ctx, server, bootstrapBackupWorkCommand(work)); err != nil {
		return result, errors.New("unable to prepare MySQL credential context")
	}
	secretPath, err := writeMySQLSecretContext(credential, instancePort(instance))
	if err != nil {
		cleanupCtx, cancel := mysqlCredentialCleanupContext(ctx)
		_, _ = s.remote.Run(cleanupCtx, server, cleanupBackupCommand(work))
		cancel()
		return result, errors.New("unable to create MySQL credential context")
	}
	defer func() {
		var cleanupErrors []error
		cleanupCtx, cancel := mysqlCredentialCleanupContext(ctx)
		if _, err := s.remote.Run(cleanupCtx, server, cleanupBackupCommand(work)); err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("unable to clean remote MySQL credential context"))
		}
		cancel()
		if err := removeMySQLCredentialContext(secretPath); err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("unable to clean local MySQL credential context"))
		}
		retErr = errors.Join(retErr, errors.Join(cleanupErrors...))
	}()
	if err := s.remote.UploadFile(ctx, server, secretPath, path.Join(work, "secret-context.cnf"), 0o600); err != nil {
		return result, errors.New("unable to upload MySQL credential context")
	}
	remoteSecretPath := path.Join(work, "secret-context.cnf")
	result, err = s.remote.Run(ctx, server, mysqlRemoteCredentialValidationCommand(remoteSecretPath)+"\n"+command(work))
	secret := credential.Secret["password"]
	result.Stdout = sanitizeMySQLCredentialText(result.Stdout, secret)
	result.Stderr = sanitizeMySQLCredentialText(result.Stderr, secret)
	installerkit.LogCommandResult(result, err, mysqlSanitizedLogger{base: log, secrets: []string{secret}})
	if err != nil {
		return result, errors.New("mysql remote command failed")
	}
	return result, nil
}

func (s Service) markInnoDBClusterPrimary(instance store.AppInstance, primaryEndpoint string, details map[string]any) error {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return err
	}
	matched := false
	detectedAt := time.Now().UTC().Format(time.RFC3339)
	for _, candidate := range instances {
		if !sameMySQLCluster(instance, candidate) {
			continue
		}
		matched = true
		metadata := appMetadata(candidate)
		metadata["currentPrimaryEndpoint"] = primaryEndpoint
		metadata["primaryEndpoint"] = primaryEndpoint
		metadata["primaryDetectedAt"] = detectedAt
		if normalizeEndpoint(metadataString(metadata, "endpoint")) == normalizeEndpoint(primaryEndpoint) {
			metadata["role"] = "primary"
		} else {
			metadata["role"] = "secondary"
		}
		if metadataString(metadata, "topology") == "" {
			metadata["topology"] = "innodb-cluster"
		}
		if candidate.ID == instance.ID {
			metadata["lastCheck"] = map[string]any{
				"status":    "running",
				"checkedAt": detectedAt,
				"details":   details,
			}
			clearRecoveredInstallFailure(metadata, "running")
			candidate.Status = "running"
		}
		data, _ := json.Marshal(metadata)
		candidate.Metadata = string(data)
		if candidate.Topology == "" {
			candidate.Topology = "innodb-cluster"
		}
		if _, err := s.store.SaveAppInstance(candidate); err != nil {
			return err
		}
	}
	if !matched {
		return s.markInstanceStatus(instance, "running", details)
	}
	return nil
}

func (s Service) markInnoDBClusterStarted(instance store.AppInstance, primaryEndpoint string, details map[string]any) error {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return err
	}
	matched := false
	detectedAt := time.Now().UTC().Format(time.RFC3339)
	for _, candidate := range instances {
		if !sameMySQLCluster(instance, candidate) {
			continue
		}
		matched = true
		metadata := appMetadata(candidate)
		metadata["currentPrimaryEndpoint"] = primaryEndpoint
		metadata["primaryEndpoint"] = primaryEndpoint
		metadata["primaryDetectedAt"] = detectedAt
		if normalizeEndpoint(metadataString(metadata, "endpoint")) == normalizeEndpoint(primaryEndpoint) {
			metadata["role"] = "primary"
		} else {
			metadata["role"] = "secondary"
		}
		metadata["topology"] = "innodb-cluster"
		metadata["lastCheck"] = map[string]any{
			"status":    "running",
			"checkedAt": detectedAt,
			"details":   details,
		}
		clearRecoveredInstallFailure(metadata, "running")
		data, _ := json.Marshal(metadata)
		candidate.Metadata = string(data)
		candidate.Status = "running"
		if candidate.Topology == "" {
			candidate.Topology = "innodb-cluster"
		}
		if _, err := s.store.SaveAppInstance(candidate); err != nil {
			return err
		}
	}
	if !matched {
		return s.markInstanceStatus(instance, "running", details)
	}
	return nil
}

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	metadata := appMetadata(instance)
	metadata["lastCheck"] = map[string]any{
		"status":    status,
		"checkedAt": time.Now().UTC().Format(time.RFC3339),
		"details":   details,
	}
	clearRecoveredInstallFailure(metadata, status)
	data, _ := json.Marshal(metadata)
	instance.Metadata = string(data)
	instance.Status = status
	_, err := s.store.SaveAppInstance(instance)
	return err
}

func clearRecoveredInstallFailure(metadata map[string]any, status string) {
	if normalizeRuntimeProbeStatus(status) != "running" {
		return
	}
	for _, key := range []string{"installFailed", "failedAt", "taskId", "error"} {
		delete(metadata, key)
	}
}

func instancePort(instance store.AppInstance) int {
	var metadata struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return normalizePort(metadata.Port, 3306)
}

func instanceRootUser(instance store.AppInstance) string {
	return stringParam(appMetadata(instance), "rootUser", "root")
}

func appMetadata(instance store.AppInstance) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func parseMySQLRuntimeProbe(stdout string) mysqlRuntimeProbe {
	values := parseShellKeyValues(stdout)
	probe := mysqlRuntimeProbe{
		RuntimeStatus: values["runtimeStatus"],
		PingStatus:    values["mysqlPingStatus"],
		ServiceStatus: values["mysqlServiceStatus"],
		PortStatus:    values["mysqlPortStatus"],
		RuntimeSource: values["mysqlRuntimeSource"],
	}
	if probe.RuntimeStatus == "" && strings.TrimSpace(stdout) != "" {
		probe.RuntimeStatus = "running"
		probe.PingStatus = "running"
		probe.RuntimeSource = "command"
	}
	if probe.RuntimeStatus == "" {
		probe.RuntimeStatus = "offline"
	}
	return probe
}

func parseShellKeyValues(stdout string) map[string]string {
	values := map[string]string{}
	lines := strings.Split(stdout, "\n")
	for _, line := range lines {
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

func normalizeRuntimeProbeStatus(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	switch value {
	case "ok", "success", "active", "available", "listening":
		return "running"
	case "failed", "error", "missing", "stopped", "offline", "unavailable", "closed":
		return "offline"
	default:
		return value
	}
}

func mergeDetails(dst map[string]any, src map[string]any) {
	keys := make([]string, 0, len(src))
	for key := range src {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if value := src[key]; value != "" {
			dst[key] = value
		}
	}
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func instanceTopology(instance store.AppInstance) string {
	if strings.TrimSpace(instance.Topology) != "" {
		return normalizeTopology(instance.Topology)
	}
	return normalizeTopology(metadataString(appMetadata(instance), "topology"))
}

func sameMySQLCluster(base, candidate store.AppInstance) bool {
	if candidate.App != "mysql" || instanceTopology(candidate) != "innodb-cluster" {
		return false
	}
	baseMetadata := appMetadata(base)
	candidateMetadata := appMetadata(candidate)
	if clusterID := metadataString(baseMetadata, "clusterId"); clusterID != "" {
		return clusterID == metadataString(candidateMetadata, "clusterId")
	}
	if clusterName := metadataString(baseMetadata, "clusterName"); clusterName != "" {
		return strings.EqualFold(clusterName, metadataString(candidateMetadata, "clusterName"))
	}
	return base.ID != "" && base.ID == candidate.ID
}

func normalizeEndpoint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "tcp://")
	value = strings.TrimPrefix(value, "mysql://")
	return value
}

func firstNonEmptyLine(value string) string {
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}
