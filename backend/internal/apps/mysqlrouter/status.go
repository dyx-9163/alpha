package mysqlrouter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

func (s Service) checkRuntime(ctx context.Context, server store.Server, instance store.AppInstance, log Logger) error {
	basePort := instanceBasePort(instance)
	installRoot := remoteInstallRoot(server, "mysql-router", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "mysql-router", instance.Version)
	cmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/mysql-router/bin/mysqlrouter\" ] && [ -x \"$LEGACY_ROOT/mysql-router/bin/mysqlrouter\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\n(systemctl is-active --quiet %s || systemctl is-active --quiet %s) && \"$INSTALL_ROOT/mysql-router/bin/mysqlrouter\" --version",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(routerServiceName(basePort)),
		installerkit.ShellQuote(fmt.Sprintf("aifar-mysql-router-%d", basePort)),
	)
	_, err := installerkit.Run(ctx, s.remote, server, cmd, log, "mysql router remote command failed")
	return err
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

func innoDBClusters(instances []store.AppInstance) []clusterInfo {
	type clusterAccumulator struct {
		info clusterInfo
	}
	groups := map[string]*clusterAccumulator{}
	var order []string
	for _, instance := range instances {
		if instance.App != "mysql" || instanceTopology(instance) != "innodb-cluster" {
			continue
		}
		metadata := appMetadata(instance)
		clusterID := metadataString(metadata, "clusterId")
		clusterName := metadataString(metadata, "clusterName")
		if clusterID == "" {
			clusterID = clusterName
		}
		if clusterID == "" {
			clusterID = instance.ID
		}
		acc := groups[clusterID]
		if acc == nil {
			acc = &clusterAccumulator{info: clusterInfo{
				ID:   clusterID,
				Name: firstNonEmpty(clusterName, clusterID),
			}}
			groups[clusterID] = acc
			order = append(order, clusterID)
		}
		acc.info.NodeCount++
		if clusterName != "" {
			acc.info.Name = clusterName
		}
		if rootUser := metadataString(metadata, "rootUser"); rootUser != "" && acc.info.RootUser == "" {
			acc.info.RootUser = rootUser
		}
		if detectedAt := metadataString(metadata, "primaryDetectedAt"); detectedAt != "" {
			acc.info.PrimaryDetectedAt = detectedAt
		}
		if primary := firstNonEmpty(metadataString(metadata, "currentPrimaryEndpoint"), metadataString(metadata, "primaryEndpoint")); primary != "" {
			acc.info.CurrentPrimary = primary
			acc.info.Endpoint = primary
			continue
		}
		if endpoint := metadataString(metadata, "endpoint"); endpoint != "" && acc.info.Endpoint == "" {
			acc.info.Endpoint = endpoint
		}
	}
	out := make([]clusterInfo, 0, len(order))
	for _, key := range order {
		out = append(out, groups[key].info)
	}
	return out
}

func instanceBasePort(instance store.AppInstance) int {
	return normalizePort(metadataInt(appMetadata(instance), "basePort"), 6446)
}

func instanceTopology(instance store.AppInstance) string {
	if strings.TrimSpace(instance.Topology) != "" {
		return normalizeRouterTopology(instance.Topology)
	}
	return normalizeRouterTopology(metadataString(appMetadata(instance), "topology"))
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
	if !ok || value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func metadataInt(metadata map[string]any, key string) int {
	value, ok := metadata[key]
	if !ok {
		return 0
	}
	return intValue(value)
}

func routerServiceName(basePort int) string {
	return "aifar-mysql-router"
}

func remoteInstallRoot(server store.Server, app, version string) string {
	return installerkit.InstallRoot(server.DeployDir, app)
}

func remoteLegacyInstallRoot(server store.Server, app, version string) string {
	return installerkit.LegacyInstallRoot(server.DeployDir, app, version)
}
