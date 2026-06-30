package redis

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

type redisSentinelTopology struct {
	MasterEndpoint    string
	ReplicaEndpoints  []string
	SentinelEndpoints []string
}

func (s Service) checkRedisRuntime(ctx context.Context, server store.Server, instance store.AppInstance, defaultPassword string, log Logger) error {
	metadata := appMetadata(instance)
	role := metadataString(metadata, "role")
	topology := instanceTopology(instance)
	password := redisPassword(nil, defaultPassword)
	installRoot := remoteInstallRoot(server, "redis", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "redis", instance.Version)
	resolveRoot := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/bin/redis-cli\" ] && [ -x \"$LEGACY_ROOT/bin/redis-cli\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
	)
	var checks []string
	if role != "sentinel" {
		port := instancePort(instance)
		checks = append(checks, fmt.Sprintf("(systemctl is-active --quiet %s || systemctl is-active --quiet %s)",
			installerkit.ShellQuote("aifar-redis"),
			installerkit.ShellQuote(fmt.Sprintf("aifar-redis-%d", port)),
		))
		checks = append(checks, fmt.Sprintf("REDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -p %d --no-auth-warning PING >/dev/null",
			installerkit.ShellQuote(password),
			port,
		))
	}
	if topology == "sentinel" && (metadataBool(metadata, "sentinel") || role == "sentinel") {
		sentinelPort := instanceSentinelPort(instance)
		checks = append(checks, fmt.Sprintf("(systemctl is-active --quiet %s || systemctl is-active --quiet %s)",
			installerkit.ShellQuote("aifar-redis-sentinel"),
			installerkit.ShellQuote(fmt.Sprintf("aifar-redis-sentinel-%d", sentinelPort)),
		))
		checks = append(checks, fmt.Sprintf("REDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -p %d --no-auth-warning PING >/dev/null",
			installerkit.ShellQuote(password),
			sentinelPort,
		))
	}
	if len(checks) == 0 {
		return errors.New("redis runtime check has no service to verify")
	}
	_, err := installerkit.Run(ctx, s.remote, server, resolveRoot+"\n"+strings.Join(checks, " && "), log, "redis remote command failed")
	return err
}

func (s Service) detectRedisRole(ctx context.Context, server store.Server, instance store.AppInstance, defaultPassword string, log Logger) (string, redisSentinelTopology, error) {
	topology := instanceTopology(instance)
	role := instanceRole(instance)
	password := redisPassword(nil, defaultPassword)
	if topology == "sentinel" {
		sentinelTopology, err := s.detectRedisSentinelTopology(ctx, server, instance, password, log)
		if err != nil {
			return role, redisSentinelTopology{}, err
		}
		if detected := s.detectRedisDataRoleQuiet(ctx, server, instance, password); detected != "" {
			role = detected
		}
		instanceEndpoint := s.redisInstanceEndpoint(instance, appMetadata(instance))
		if role != "sentinel" && normalizeEndpoint(instanceEndpoint) == normalizeEndpoint(sentinelTopology.MasterEndpoint) {
			role = "master"
		} else if role != "sentinel" {
			role = "replica"
		}
		return role, sentinelTopology, nil
	}
	if role == "sentinel" {
		return role, redisSentinelTopology{}, nil
	}
	detected, err := s.detectRedisDataRole(ctx, server, instance, password, log)
	if err != nil {
		return role, redisSentinelTopology{}, err
	}
	if detected != "" {
		role = detected
	}
	return role, redisSentinelTopology{}, nil
}

func (s Service) detectRedisDataRole(ctx context.Context, server store.Server, instance store.AppInstance, password string, log Logger) (string, error) {
	cmd := redisDataRoleCommand(server, instance, password)
	result, err := installerkit.Run(ctx, s.remote, server, cmd, log, "redis remote command failed")
	if err != nil {
		return "", err
	}
	return parseRedisDataRole(result.Stdout), nil
}

func (s Service) detectRedisDataRoleQuiet(ctx context.Context, server store.Server, instance store.AppInstance, password string) string {
	result, err := s.remote.Run(ctx, server, redisDataRoleCommand(server, instance, password))
	if err != nil {
		return ""
	}
	return parseRedisDataRole(result.Stdout)
}

func redisDataRoleCommand(server store.Server, instance store.AppInstance, password string) string {
	installRoot := remoteInstallRoot(server, "redis", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "redis", instance.Version)
	port := instancePort(instance)
	return fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/bin/redis-cli\" ] && [ -x \"$LEGACY_ROOT/bin/redis-cli\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nREDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -p %d --raw --no-auth-warning ROLE",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(password),
		port,
	)
}

func parseRedisDataRole(stdout string) string {
	switch strings.ToLower(firstNonEmptyLine(stdout)) {
	case "master":
		return "master"
	case "slave", "replica":
		return "replica"
	default:
		return ""
	}
}

func (s Service) detectRedisSentinelTopology(ctx context.Context, server store.Server, instance store.AppInstance, password string, log Logger) (redisSentinelTopology, error) {
	sentinelInstance := instance
	sentinelServer := server
	if !instanceHasSentinel(instance) {
		peerInstance, peerServer, err := s.findRedisSentinelPeer(instance)
		if err != nil {
			return redisSentinelTopology{}, err
		}
		sentinelInstance = peerInstance
		sentinelServer = peerServer
	}
	metadata := appMetadata(sentinelInstance)
	masterName := metadataString(metadata, "masterName")
	if masterName == "" {
		masterName = "aifar-master"
	}
	sentinelPort := instanceSentinelPort(sentinelInstance)
	installRoot := remoteInstallRoot(sentinelServer, "redis", sentinelInstance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(sentinelServer, "redis", sentinelInstance.Version)
	baseCmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/bin/redis-cli\" ] && [ -x \"$LEGACY_ROOT/bin/redis-cli\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nREDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -p %d --raw --no-auth-warning",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(password),
		sentinelPort,
	)
	masterCmd := fmt.Sprintf("%s SENTINEL get-master-addr-by-name %s",
		baseCmd,
		installerkit.ShellQuote(masterName),
	)
	result, err := installerkit.Run(ctx, s.remote, sentinelServer, masterCmd, log, "redis sentinel remote command failed")
	if err != nil {
		return redisSentinelTopology{}, err
	}
	lines := nonEmptyLines(result.Stdout)
	if len(lines) < 2 {
		return redisSentinelTopology{}, errors.New("Redis Sentinel did not return current master address")
	}
	out := redisSentinelTopology{
		MasterEndpoint: fmt.Sprintf("%s:%s", lines[0], lines[1]),
	}
	replicaCmd := fmt.Sprintf("%s SENTINEL replicas %s",
		baseCmd,
		installerkit.ShellQuote(masterName),
	)
	if result, err := installerkit.Run(ctx, s.remote, sentinelServer, replicaCmd, log, "redis sentinel replicas command failed"); err == nil {
		out.ReplicaEndpoints = parseRedisSentinelEndpointRows(result.Stdout)
	}
	sentinelsCmd := fmt.Sprintf("%s SENTINEL sentinels %s",
		baseCmd,
		installerkit.ShellQuote(masterName),
	)
	if result, err := installerkit.Run(ctx, s.remote, sentinelServer, sentinelsCmd, log, "redis sentinel peers command failed"); err == nil {
		out.SentinelEndpoints = parseRedisSentinelEndpointRows(result.Stdout)
	}
	if strings.TrimSpace(sentinelServer.Host) != "" {
		out.SentinelEndpoints = appendUniqueEndpoint(out.SentinelEndpoints, fmt.Sprintf("%s:%d", sentinelServer.Host, sentinelPort))
	}
	for _, endpoint := range append([]string{out.MasterEndpoint}, out.ReplicaEndpoints...) {
		host, _ := splitEndpoint(endpoint)
		if host != "" && s.probeRedisSentinelEndpoint(ctx, sentinelServer, sentinelInstance, password, host, sentinelPort) {
			out.SentinelEndpoints = appendUniqueEndpoint(out.SentinelEndpoints, fmt.Sprintf("%s:%d", host, sentinelPort))
		}
	}
	return out, nil
}

func (s Service) probeRedisSentinelEndpoint(ctx context.Context, server store.Server, instance store.AppInstance, password, host string, sentinelPort int) bool {
	installRoot := remoteInstallRoot(server, "redis", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "redis", instance.Version)
	cmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/bin/redis-cli\" ] && [ -x \"$LEGACY_ROOT/bin/redis-cli\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nREDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -h %s -p %d --raw --no-auth-warning PING >/dev/null",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(password),
		installerkit.ShellQuote(host),
		sentinelPort,
	)
	_, err := s.remote.Run(ctx, server, cmd)
	return err == nil
}

func (s Service) findRedisSentinelPeer(instance store.AppInstance) (store.AppInstance, store.Server, error) {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return store.AppInstance{}, store.Server{}, err
	}
	for _, candidate := range instances {
		if !sameRedisSentinelGroup(instance, candidate) || !instanceHasSentinel(candidate) {
			continue
		}
		server, err := s.store.GetServer(candidate.ServerID, true)
		if err != nil {
			return store.AppInstance{}, store.Server{}, err
		}
		return candidate, server, nil
	}
	return store.AppInstance{}, store.Server{}, errors.New("Redis Sentinel peer was not found")
}

func (s Service) markRedisSentinelMaster(instance store.AppInstance, checkedRole string, sentinelTopology redisSentinelTopology, details map[string]any) error {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return err
	}
	masterEndpoint := sentinelTopology.MasterEndpoint
	masterHost, masterPort := splitEndpoint(masterEndpoint)
	detectedAt := time.Now().UTC().Format(time.RFC3339)
	for _, candidate := range instances {
		if !sameRedisSentinelGroup(instance, candidate) {
			continue
		}
		metadata := appMetadata(candidate)
		metadata["currentMasterEndpoint"] = masterEndpoint
		metadata["masterHost"] = masterHost
		metadata["masterPort"] = masterPort
		metadata["masterDetectedAt"] = detectedAt
		metadata["replicaEndpoints"] = sentinelTopology.ReplicaEndpoints
		metadata["sentinelEndpoints"] = sentinelTopology.SentinelEndpoints
		role := metadataString(metadata, "role")
		candidateEndpoint := s.redisInstanceEndpoint(candidate, metadata)
		if normalizeEndpoint(candidateEndpoint) == normalizeEndpoint(masterEndpoint) {
			role = "master"
			metadata["role"] = role
		} else if endpointInList(candidateEndpoint, sentinelTopology.ReplicaEndpoints) {
			role = "replica"
			metadata["role"] = role
		} else if role != "sentinel" && strings.TrimSpace(candidateEndpoint) != "" {
			role = "replica"
			metadata["role"] = role
		}
		if candidate.ID == instance.ID {
			metadata["role"] = checkedRole
			metadata["lastCheck"] = map[string]any{
				"status":    "running",
				"checkedAt": detectedAt,
				"details":   details,
			}
			candidate.Status = "running"
		}
		data, _ := json.Marshal(metadata)
		candidate.Metadata = string(data)
		if _, err := s.store.SaveAppInstance(candidate); err != nil {
			return err
		}
	}
	return nil
}

func parseRedisSentinelEndpointRows(stdout string) []string {
	lines := nonEmptyLines(stdout)
	fields := map[string]string{}
	var endpoints []string
	flush := func() {
		ip := strings.TrimSpace(fields["ip"])
		port := strings.TrimSpace(fields["port"])
		if ip != "" && port != "" {
			endpoints = appendUniqueEndpoint(endpoints, fmt.Sprintf("%s:%s", ip, port))
		}
		fields = map[string]string{}
	}
	for idx := 0; idx+1 < len(lines); idx += 2 {
		key := strings.ToLower(strings.TrimSpace(lines[idx]))
		value := strings.TrimSpace(lines[idx+1])
		if key == "name" && len(fields) > 0 {
			flush()
		}
		fields[key] = value
	}
	flush()
	return endpoints
}

func appendUniqueEndpoint(endpoints []string, endpoint string) []string {
	endpoint = normalizeEndpoint(endpoint)
	if endpoint == "" {
		return endpoints
	}
	for _, existing := range endpoints {
		if normalizeEndpoint(existing) == endpoint {
			return endpoints
		}
	}
	return append(endpoints, endpoint)
}

func endpointInList(endpoint string, endpoints []string) bool {
	endpoint = normalizeEndpoint(endpoint)
	if endpoint == "" {
		return false
	}
	for _, candidate := range endpoints {
		if endpoint == normalizeEndpoint(candidate) {
			return true
		}
	}
	return false
}

func (s Service) redisInstanceEndpoint(instance store.AppInstance, metadata map[string]any) string {
	if endpoint := metadataString(metadata, "endpoint"); endpoint != "" {
		return endpoint
	}
	server, err := s.store.GetServer(instance.ServerID, false)
	if err != nil || strings.TrimSpace(server.Host) == "" {
		return ""
	}
	return fmt.Sprintf("%s:%d", server.Host, intParam(metadata, "port", 6379))
}

func (s Service) markRedisInstanceStatus(instance store.AppInstance, status, role string, details map[string]any) error {
	metadata := appMetadata(instance)
	if role != "" {
		metadata["role"] = role
	}
	if !redisStatusHealthy(status) {
		clearRedisDiscoveredTopology(metadata)
	}
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

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	return s.markRedisInstanceStatus(instance, status, instanceRole(instance), details)
}

func redisStatusHealthy(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "success", "running", "available":
		return true
	default:
		return false
	}
}

func clearRedisDiscoveredTopology(metadata map[string]any) {
	delete(metadata, "currentMasterEndpoint")
	delete(metadata, "replicaEndpoints")
	delete(metadata, "sentinelEndpoints")
	delete(metadata, "masterDetectedAt")
}
