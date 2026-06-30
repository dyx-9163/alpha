package mysqlrouter

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

func mysqlRouterOptions(params map[string]any, defaultPassword string, cluster clusterInfo) RouterInstallOptions {
	bindAddress := stringParam(params, "bindAddress", "0.0.0.0")
	return RouterInstallOptions{
		BasePort:          intParam(params, "basePort", 6446),
		BootstrapHost:     cluster.BootstrapHost,
		BootstrapPort:     cluster.BootstrapPort,
		BootstrapUser:     stringParam(params, "rootUser", firstNonEmpty(cluster.RootUser, "root")),
		BootstrapPassword: passwordParam(params, defaultPassword),
		BindAddress:       bindAddress,
	}
}

func passwordParam(params map[string]any, fallback string) string {
	for _, key := range []string{"rootPassword", "password", "mysqlPassword"} {
		if value, ok := params[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return "Oversea.123"
	}
	return fallback
}

func targetServerIDs(req InstallRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(req.ServerID)
	for _, id := range req.ServerIDs {
		add(id)
	}
	return out
}

func normalizeRouterTopology(topology string) string {
	topology = strings.ToLower(strings.TrimSpace(topology))
	if topology == "" {
		return "router"
	}
	return topology
}

func stringParam(params map[string]any, key, fallback string) string {
	if value, ok := params[key]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return fallback
}

func intParam(params map[string]any, key string, fallback int) int {
	if value, ok := params[key]; ok {
		return intValue(value)
	}
	return fallback
}

func intValue(value any) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return n
	default:
		return 0
	}
}

func normalizePort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}

func parseEndpoint(value string, fallbackPort int) (string, int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", 0, errors.New("endpoint is empty")
	}
	if parsed, err := url.Parse(value); err == nil && parsed.Host != "" {
		value = parsed.Host
	}
	host, portText, err := net.SplitHostPort(value)
	if err == nil {
		port, parseErr := strconv.Atoi(portText)
		if parseErr != nil || port <= 0 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port %q", portText)
		}
		return strings.Trim(host, "[]"), port, nil
	}
	if idx := strings.LastIndex(value, ":"); idx > 0 && strings.Count(value, ":") == 1 {
		host = strings.TrimSpace(value[:idx])
		port, parseErr := strconv.Atoi(strings.TrimSpace(value[idx+1:]))
		if parseErr != nil || port <= 0 || port > 65535 {
			return "", 0, fmt.Errorf("invalid port in endpoint %q", value)
		}
		return strings.Trim(host, "[]"), port, nil
	}
	if fallbackPort > 0 {
		return strings.Trim(value, "[]"), fallbackPort, nil
	}
	return "", 0, fmt.Errorf("endpoint %q does not include a port", value)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
