package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func (a *API) recordFailedInstallInstances(ctx context.Context, req registry.InstallRequest, startedAt time.Time, taskID string, installErr error) (int, error) {
	targets := req.TargetServerIDs()
	if len(targets) == 0 {
		return 0, nil
	}
	existing, err := a.store.ListAppInstances()
	if err != nil {
		return 0, err
	}
	count := 0
	for _, serverID := range targets {
		if ctx.Err() != nil {
			continue
		}
		if installInstanceRecordedAfter(existing, req.App, serverID, startedAt) || failedInstallInstanceExists(existing, req.App, serverID, taskID) {
			continue
		}
		metadata, err := a.failedInstallMetadata(req, serverID, taskID, installErr)
		if err != nil {
			return count, err
		}
		raw, err := json.Marshal(metadata)
		if err != nil {
			return count, err
		}
		_, err = a.store.SaveAppInstance(store.AppInstance{
			App: req.App, Version: req.Version, ServerID: serverID, Status: failedInstallStatus(req.App),
			Topology: normalizedInstallTopology(req.Topology), Metadata: string(raw),
		})
		if err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

func installInstanceRecordedAfter(instances []store.AppInstance, app, serverID string, startedAt time.Time) bool {
	if startedAt.IsZero() {
		return false
	}
	for _, item := range instances {
		if item.App != app || item.ServerID != serverID {
			continue
		}
		if !item.CreatedAt.Before(startedAt) || !item.UpdatedAt.Before(startedAt) {
			return true
		}
	}
	return false
}

// markRecordedInstallInstancesFailed mutates only rows provably created by
// this install window. It is reserved for post-install finalization failures;
// the general failure recorder above keeps its historical non-mutating rule.
func (a *API) markRecordedInstallInstancesFailed(req registry.InstallRequest, startedAt time.Time, taskID string, installErr error) (int, error) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return 0, err
	}
	updates := make([]store.AppInstance, 0, len(req.TargetServerIDs()))
	for _, serverID := range req.TargetServerIDs() {
		instance, found := uniqueInstallInstanceCreatedAfter(instances, req.App, req.Version, serverID, startedAt)
		if !found {
			continue
		}
		metadata, err := a.failedInstallMetadata(req, serverID, taskID, installErr)
		if err != nil {
			return 0, err
		}
		current := map[string]any{}
		_ = json.Unmarshal([]byte(instance.Metadata), &current)
		for key, value := range metadata {
			if key == "clusterId" && strings.TrimSpace(installMetadataString(current, key)) != "" {
				continue
			}
			current[key] = value
		}
		raw, err := json.Marshal(current)
		if err != nil {
			return 0, err
		}
		instance.Status = failedInstallStatus(req.App)
		instance.Metadata = string(raw)
		updates = append(updates, instance)
	}
	if len(updates) == 0 {
		return 0, nil
	}
	if err := a.store.MarkMySQLInstallInstancesFailed(updates); err != nil {
		return 0, err
	}
	return len(updates), nil
}

func uniqueInstallInstanceCreatedAfter(instances []store.AppInstance, app, version, serverID string, startedAt time.Time) (store.AppInstance, bool) {
	if startedAt.IsZero() {
		return store.AppInstance{}, false
	}
	var selected store.AppInstance
	for _, item := range instances {
		if item.App != app || item.Version != version || item.ServerID != serverID || item.CreatedAt.Before(startedAt) {
			continue
		}
		if selected.ID != "" {
			return store.AppInstance{}, false
		}
		selected = item
	}
	return selected, selected.ID != ""
}

func failedInstallInstanceExists(instances []store.AppInstance, app, serverID, taskID string) bool {
	for _, item := range instances {
		if item.App != app || item.ServerID != serverID || !isFailedInstallStatus(item.Status) {
			continue
		}
		metadata := map[string]any{}
		if err := json.Unmarshal([]byte(item.Metadata), &metadata); err == nil && installMetadataString(metadata, "taskId") == taskID {
			return true
		}
	}
	return false
}

func failedInstallStatus(app string) string {
	if strings.EqualFold(strings.TrimSpace(app), "aifar") {
		return "install_failed"
	}
	return "failed"
}

func isFailedInstallStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "install_failed":
		return true
	default:
		return false
	}
}

func (a *API) failedInstallMetadata(req registry.InstallRequest, serverID, taskID string, installErr error) (map[string]any, error) {
	topology := normalizedInstallTopology(req.Topology)
	metadata := map[string]any{
		"installFailed": true,
		"failedAt":      time.Now().Format(time.RFC3339),
		"taskId":        taskID,
		"error":         truncateInstallError(installErr),
		"topology":      topology,
	}
	if isClusterInstallTopology(topology) {
		metadata["clusterId"] = fmt.Sprintf("%s-failed-%s", req.App, taskID)
	}
	server, err := a.store.GetServer(serverID, false)
	if err != nil {
		return metadata, nil
	}
	host := strings.TrimSpace(server.Host)
	deployDir := strings.TrimSpace(server.DeployDir)
	if deployDir == "" {
		deployDir = a.cfg.DefaultDeployDir
	}
	if deployDir == "" {
		deployDir = "/aifar/apps"
	}
	switch req.App {
	case "aifar":
		webPort := installParameterInt(req.Parameters, "webPort", 8080)
		gatewayPort := installParameterInt(req.Parameters, "gatewayPort", 38000)
		metadata["installRoot"] = strings.TrimRight(deployDir, "/") + "/admin"
		metadata["networkName"] = installParameterString(req.Parameters, "networkName", "aifar-network")
		metadata["webPort"] = webPort
		metadata["gatewayPort"] = gatewayPort
		if host != "" {
			metadata["endpoint"] = fmt.Sprintf("%s:%d", host, webPort)
			metadata["gatewayEndpoint"] = fmt.Sprintf("%s:%d", host, gatewayPort)
		}
	case "docker":
		port := installParameterInt(req.Parameters, "remoteAPIPort", 2375)
		metadata["remoteAPIPort"] = port
		metadata["dockerBridgeCIDR"] = installParameterString(req.Parameters, "dockerBridgeCIDR", "172.17.0.1/16")
		if host != "" {
			metadata["dockerHost"] = fmt.Sprintf("tcp://%s:%d", host, port)
		}
	case "mysql":
		port := installParameterInt(req.Parameters, "port", 3306)
		metadata["port"] = port
		metadata["rootUser"] = installParameterString(req.Parameters, "rootUser", "root")
		metadata["serviceName"] = "aifar-mysql"
		if host != "" {
			metadata["endpoint"] = fmt.Sprintf("%s:%d", host, port)
		}
	case "redis":
		port := installParameterInt(req.Parameters, "port", 6379)
		metadata["port"] = port
		metadata["serviceName"] = "aifar-redis"
		if isRedisSentinelTopology(req.Topology) {
			metadata["sentinelPort"] = installParameterInt(req.Parameters, "sentinelPort", 26379)
			metadata["masterName"] = installParameterString(req.Parameters, "masterName", "mymaster")
		}
		if host != "" {
			metadata["endpoint"] = fmt.Sprintf("%s:%d", host, port)
		}
	case "minio":
		apiPort := installParameterInt(req.Parameters, "apiPort", 9000)
		metadata["apiPort"] = apiPort
		metadata["consolePort"] = installParameterInt(req.Parameters, "consolePort", 9001)
		metadata["rootUser"] = installParameterString(req.Parameters, "rootUser", "admin")
		metadata["serviceName"] = "aifar-minio"
		copyOptionalInstallParameter(metadata, req.Parameters, "storageMode")
		copyOptionalInstallParameter(metadata, req.Parameters, "dataRoot")
		copyOptionalInstallParameter(metadata, req.Parameters, "diskDevice")
		copyOptionalInstallParameter(metadata, req.Parameters, "diskDevices")
		if host != "" {
			metadata["endpoint"] = fmt.Sprintf("http://%s:%d", host, apiPort)
		}
	case "nacos":
		port := installParameterInt(req.Parameters, "port", 8848)
		metadata["port"] = port
		metadata["grpcPort"] = installParameterInt(req.Parameters, "grpcPort", port+1000)
		metadata["grpcRaftPort"] = installParameterInt(req.Parameters, "grpcRaftPort", port+1001)
		metadata["raftPort"] = installParameterInt(req.Parameters, "raftPort", 7848)
		metadata["serviceName"] = "aifar-nacos"
		copyOptionalInstallParameter(metadata, req.Parameters, "dbHost")
		copyOptionalInstallParameter(metadata, req.Parameters, "dbPort")
		copyOptionalInstallParameter(metadata, req.Parameters, "dbName")
		copyOptionalInstallParameter(metadata, req.Parameters, "dbUser")
		if host != "" {
			metadata["endpoint"] = fmt.Sprintf("http://%s:%d/nacos", host, port)
		}
	}
	return metadata, nil
}

func normalizedInstallTopology(topology string) string {
	topology = strings.TrimSpace(topology)
	if topology == "" {
		return "standalone"
	}
	switch strings.ToLower(topology) {
	case "single":
		return "standalone"
	default:
		return topology
	}
}

func isRedisSentinelTopology(topology string) bool {
	return strings.EqualFold(strings.TrimSpace(topology), "sentinel")
}

func isClusterInstallTopology(topology string) bool {
	switch strings.ToLower(strings.TrimSpace(topology)) {
	case "cluster", "distributed", "innodb-cluster", "sentinel":
		return true
	default:
		return false
	}
}

func copyOptionalInstallParameter(metadata map[string]any, params map[string]any, key string) {
	if params == nil {
		return
	}
	value, ok := params[key]
	if !ok || value == nil {
		return
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return
	}
	metadata[key] = value
}

func installParameterString(params map[string]any, key, fallback string) string {
	if params == nil {
		return fallback
	}
	value, ok := params[key]
	if !ok || value == nil {
		return fallback
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "" || text == "<nil>" {
		return fallback
	}
	return text
}

func installParameterInt(params map[string]any, key string, fallback int) int {
	if params == nil {
		return fallback
	}
	value, ok := params[key]
	if !ok || value == nil {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return validInstallPort(v, fallback)
	case int64:
		return validInstallPort(int(v), fallback)
	case float64:
		return validInstallPort(int(v), fallback)
	case json.Number:
		n, _ := v.Int64()
		return validInstallPort(int(n), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return validInstallPort(n, fallback)
	default:
		return fallback
	}
}

func validInstallPort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}

func installMetadataString(metadata map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(metadata[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}

func truncateInstallError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	const max = 500
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}
