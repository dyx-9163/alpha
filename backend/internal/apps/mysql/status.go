package mysql

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

func (s Service) checkMySQLRuntime(ctx context.Context, server store.Server, instance store.AppInstance, defaultPassword string, log Logger) error {
	port := instancePort(instance)
	rootUser := instanceRootUser(instance)
	rootPassword := passwordParam(nil, defaultPassword)
	installRoot := remoteInstallRoot(server, "mysql", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "mysql", instance.Version)
	cmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/mysql/bin/mysqladmin\" ] && [ -x \"$LEGACY_ROOT/mysql/bin/mysqladmin\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nMYSQL_PWD=%s \"$INSTALL_ROOT/mysql/bin/mysqladmin\" --protocol=tcp -h 127.0.0.1 -P %d -u %s ping",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(rootPassword),
		port,
		installerkit.ShellQuote(rootUser),
	)
	_, err := installerkit.Run(ctx, s.remote, server, cmd, log, "mysql remote command failed")
	return err
}

func (s Service) detectInnoDBPrimary(ctx context.Context, server store.Server, instance store.AppInstance, defaultPassword string, log Logger) (string, error) {
	port := instancePort(instance)
	rootUser := instanceRootUser(instance)
	rootPassword := passwordParam(nil, defaultPassword)
	installRoot := remoteInstallRoot(server, "mysql", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "mysql", instance.Version)
	query := "SELECT CONCAT(MEMBER_HOST, ':', MEMBER_PORT) FROM performance_schema.replication_group_members WHERE MEMBER_ROLE='PRIMARY' LIMIT 1"
	cmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/mysql/bin/mysql\" ] && [ -x \"$LEGACY_ROOT/mysql/bin/mysql\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nMYSQL_PWD=%s \"$INSTALL_ROOT/mysql/bin/mysql\" --protocol=tcp -h 127.0.0.1 -P %d -u %s --batch --skip-column-names -e %s",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(rootPassword),
		port,
		installerkit.ShellQuote(rootUser),
		installerkit.ShellQuote(query),
	)
	result, err := installerkit.Run(ctx, s.remote, server, cmd, log, "mysql remote command failed")
	if err != nil {
		return "", err
	}
	primary := firstNonEmptyLine(result.Stdout)
	if primary == "" {
		return "", errors.New("InnoDB Cluster primary was not returned")
	}
	return primary, nil
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
	data, _ := json.Marshal(metadata)
	instance.Metadata = string(data)
	instance.Status = status
	_, err := s.store.SaveAppInstance(instance)
	return err
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
