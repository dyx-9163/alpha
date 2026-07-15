package redis

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

const redisCollectorCheckMarker = "AIFAR_REDIS_CHECK_V1"

func (s Service) checkRedisCollector(ctx context.Context, req CheckRequest, password string) (CheckResult, error) {
	copy := CopyFor(req.Language)
	details := map[string]any{
		"checkedAt": time.Now().UTC().Format(time.RFC3339),
		"topology":  instanceTopology(req.Instance),
		"collector": true,
	}
	expectsData, expectsSentinel := redisCollectorExpectedComponents(req.Instance)
	fail := func(err error, runtime redisRuntimeCheckResult) (CheckResult, error) {
		details["error"] = err.Error()
		runtime.applyDetails(details)
		if instanceTopology(req.Instance) == "sentinel" && (runtime.Data.Checked || runtime.Sentinel.Checked) {
			_ = s.markRedisSentinelRuntimeStatus(req.Instance, instanceRole(req.Instance), details, runtime)
		} else {
			_ = s.markInstanceStatus(req.Instance, "failed", details)
		}
		message := fmt.Sprintf(copyWithFallback(copy.CheckFailed, "Redis check failed: %s"), err)
		return CheckResult{Status: "failed", Message: message, Details: details}, err
	}
	if !expectsData && !expectsSentinel {
		return fail(errors.New("redis collector check has no service to verify"), redisRuntimeCheckResult{})
	}

	result, err := s.remote.Run(ctx, req.Server, redisCollectorCheckCommand(req.Server, req.Instance, password))
	if err != nil {
		return fail(err, redisRuntimeCheckResult{})
	}
	runtime, role, err := parseRedisCollectorCheckOutput(result.Stdout, expectsData, expectsSentinel)
	if err != nil {
		return fail(err, runtime)
	}
	runtime.applyDetails(details)
	if role == "unknown" {
		role = instanceRole(req.Instance)
	}
	status := runtime.aggregateStatus()
	if instanceTopology(req.Instance) == "sentinel" {
		err = s.markRedisSentinelRuntimeStatus(req.Instance, role, details, runtime)
	} else {
		err = s.markRedisInstanceStatus(req.Instance, status, role, details)
	}
	if err != nil {
		return fail(err, runtime)
	}
	message := fmt.Sprintf(copyWithFallback(copy.Checked, "Redis instance checked: %s"), status)
	if runtime.allCheckedServicesFailed() {
		return CheckResult{Status: status, Message: message, Details: details}, runtime.error()
	}
	return CheckResult{Status: status, Message: message, Details: details}, nil
}

func redisCollectorExpectedComponents(instance store.AppInstance) (bool, bool) {
	metadata := appMetadata(instance)
	role := instanceRole(instance)
	expectsData := role != "sentinel"
	expectsSentinel := instanceTopology(instance) == "sentinel" && (metadataBool(metadata, "sentinel") || role == "sentinel")
	return expectsData, expectsSentinel
}

func redisCollectorCheckCommand(server store.Server, instance store.AppInstance, password string) string {
	expectsData, expectsSentinel := redisCollectorExpectedComponents(instance)
	installRoot := remoteInstallRoot(server, "redis", instance.Version)
	legacyInstallRoot := remoteLegacyInstallRoot(server, "redis", instance.Version)
	lines := []string{
		"INSTALL_ROOT=" + installerkit.ShellQuote(installRoot),
		"LEGACY_ROOT=" + installerkit.ShellQuote(legacyInstallRoot),
		`if [ ! -x "$INSTALL_ROOT/bin/redis-cli" ] && [ -x "$LEGACY_ROOT/bin/redis-cli" ]; then INSTALL_ROOT="$LEGACY_ROOT"; fi`,
		"REDIS_PASSWORD=" + installerkit.ShellQuote(password),
		"ROLE=unknown",
		"printf '%s\\n' " + installerkit.ShellQuote(redisCollectorCheckMarker),
	}
	if expectsData {
		port := instancePort(instance)
		lines = append(lines, fmt.Sprintf(`if (systemctl is-active --quiet %s || systemctl is-active --quiet %s) && REDISCLI_AUTH="$REDIS_PASSWORD" "$INSTALL_ROOT/bin/redis-cli" -p %d --no-auth-warning PING >/dev/null 2>&1; then
  DATA_STATUS=running
  ROLE_OUTPUT=$(REDISCLI_AUTH="$REDIS_PASSWORD" "$INSTALL_ROOT/bin/redis-cli" -p %d --raw --no-auth-warning ROLE 2>/dev/null | sed -n '1p' || true)
  case "$ROLE_OUTPUT" in
    master) ROLE=master ;;
    slave|replica) ROLE=replica ;;
  esac
else
  DATA_STATUS=failed
fi
printf 'data=%%s\n' "$DATA_STATUS"`,
			installerkit.ShellQuote("aifar-redis"),
			installerkit.ShellQuote(fmt.Sprintf("aifar-redis-%d", port)),
			port,
			port,
		))
	}
	if expectsSentinel {
		port := instanceSentinelPort(instance)
		lines = append(lines, fmt.Sprintf(`if (systemctl is-active --quiet %s || systemctl is-active --quiet %s) && REDISCLI_AUTH="$REDIS_PASSWORD" "$INSTALL_ROOT/bin/redis-cli" -p %d --no-auth-warning PING >/dev/null 2>&1; then
  SENTINEL_STATUS=running
else
  SENTINEL_STATUS=failed
fi
printf 'sentinel=%%s\n' "$SENTINEL_STATUS"`,
			installerkit.ShellQuote("aifar-redis-sentinel"),
			installerkit.ShellQuote(fmt.Sprintf("aifar-redis-sentinel-%d", port)),
			port,
		))
	}
	lines = append(lines, `printf 'role=%s\n' "$ROLE"`)
	return strings.Join(lines, "\n")
}

func parseRedisCollectorCheckOutput(stdout string, expectsData, expectsSentinel bool) (redisRuntimeCheckResult, string, error) {
	values := map[string]string{}
	markerFound := false
	for _, line := range nonEmptyLines(stdout) {
		if line == redisCollectorCheckMarker {
			markerFound = true
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			values[strings.TrimSpace(parts[0])] = strings.ToLower(strings.TrimSpace(parts[1]))
		}
	}
	if !markerFound {
		return redisRuntimeCheckResult{}, "", errors.New("Redis collector check output marker is missing")
	}
	result := redisRuntimeCheckResult{}
	parseComponent := func(name string, expected bool) (redisRuntimeComponentStatus, error) {
		if !expected {
			return redisRuntimeComponentStatus{}, nil
		}
		status, ok := values[name]
		if !ok {
			return redisRuntimeComponentStatus{}, fmt.Errorf("Redis collector check output is missing %s status", name)
		}
		if status != "running" && status != "failed" {
			return redisRuntimeComponentStatus{}, fmt.Errorf("Redis collector check returned invalid %s status %q", name, status)
		}
		return redisRuntimeComponentStatus{Checked: true, Status: status}, nil
	}
	var err error
	if result.Data, err = parseComponent("data", expectsData); err != nil {
		return result, "", err
	}
	if result.Sentinel, err = parseComponent("sentinel", expectsSentinel); err != nil {
		return result, "", err
	}
	role := values["role"]
	switch role {
	case "master":
	case "slave", "replica":
		role = "replica"
	case "", "unknown":
		role = "unknown"
	default:
		return result, "", fmt.Errorf("Redis collector check returned invalid role %q", role)
	}
	return result, role, nil
}
