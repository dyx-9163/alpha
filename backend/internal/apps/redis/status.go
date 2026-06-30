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

func (s Service) detectRedisRole(ctx context.Context, server store.Server, instance store.AppInstance, defaultPassword string, log Logger) (string, string, error) {
	topology := instanceTopology(instance)
	role := instanceRole(instance)
	password := redisPassword(nil, defaultPassword)
	if topology == "sentinel" {
		endpoint, err := s.detectRedisSentinelMaster(ctx, server, instance, password, log)
		if err != nil {
			return role, "", err
		}
		if role != "sentinel" && normalizeEndpoint(instanceEndpoint(instance)) == normalizeEndpoint(endpoint) {
			role = "master"
		} else if role != "sentinel" {
			role = "replica"
		}
		return role, endpoint, nil
	}
	if role == "sentinel" {
		return role, "", nil
	}
	detected, err := s.detectRedisDataRole(ctx, server, instance, password, log)
	if err != nil {
		return role, "", err
	}
	if detected != "" {
		role = detected
	}
	return role, "", nil
}

func (s Service) detectRedisDataRole(ctx context.Context, server store.Server, instance store.AppInstance, password string, log Logger) (string, error) {
	installRoot := remoteInstallRoot(server, "redis", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "redis", instance.Version)
	port := instancePort(instance)
	cmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/bin/redis-cli\" ] && [ -x \"$LEGACY_ROOT/bin/redis-cli\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nREDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -p %d --raw --no-auth-warning ROLE",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(password),
		port,
	)
	result, err := installerkit.Run(ctx, s.remote, server, cmd, log, "redis remote command failed")
	if err != nil {
		return "", err
	}
	switch strings.ToLower(firstNonEmptyLine(result.Stdout)) {
	case "master":
		return "master", nil
	case "slave", "replica":
		return "replica", nil
	default:
		return "", nil
	}
}

func (s Service) detectRedisSentinelMaster(ctx context.Context, server store.Server, instance store.AppInstance, password string, log Logger) (string, error) {
	sentinelInstance := instance
	sentinelServer := server
	if !instanceHasSentinel(instance) {
		peerInstance, peerServer, err := s.findRedisSentinelPeer(instance)
		if err != nil {
			return "", err
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
	cmd := fmt.Sprintf("INSTALL_ROOT=%s\nLEGACY_ROOT=%s\nif [ ! -x \"$INSTALL_ROOT/bin/redis-cli\" ] && [ -x \"$LEGACY_ROOT/bin/redis-cli\" ]; then INSTALL_ROOT=\"$LEGACY_ROOT\"; fi\nREDISCLI_AUTH=%s \"$INSTALL_ROOT/bin/redis-cli\" -p %d --raw --no-auth-warning SENTINEL get-master-addr-by-name %s",
		installerkit.ShellQuote(installRoot),
		installerkit.ShellQuote(legacyInstallRoot),
		installerkit.ShellQuote(password),
		sentinelPort,
		installerkit.ShellQuote(masterName),
	)
	result, err := installerkit.Run(ctx, s.remote, sentinelServer, cmd, log, "redis sentinel remote command failed")
	if err != nil {
		return "", err
	}
	lines := nonEmptyLines(result.Stdout)
	if len(lines) < 2 {
		return "", errors.New("Redis Sentinel did not return current master address")
	}
	return fmt.Sprintf("%s:%s", lines[0], lines[1]), nil
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

func (s Service) markRedisSentinelMaster(instance store.AppInstance, checkedRole, masterEndpoint string, details map[string]any) error {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return err
	}
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
		role := metadataString(metadata, "role")
		if role != "sentinel" {
			if normalizeEndpoint(s.redisInstanceEndpoint(candidate, metadata)) == normalizeEndpoint(masterEndpoint) {
				role = "master"
			} else {
				role = "replica"
			}
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
