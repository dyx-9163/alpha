package mysql

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

func mysqlOptions(params map[string]any, defaultPassword string) InstallOptions {
	return InstallOptions{
		Port:         intParam(params, "port", 3306),
		RootUser:     stringParam(params, "rootUser", "root"),
		RootPassword: passwordParam(params, defaultPassword),
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

func mysqlClusterName(params map[string]any) string {
	name := stringParam(params, "clusterName", "aifarCluster")
	replacer := strings.NewReplacer(" ", "", "-", "", "_", "")
	name = replacer.Replace(name)
	if name == "" {
		return "aifarCluster"
	}
	return name
}

func mysqlClusterServerIDs(params map[string]any, fallback []string) []string {
	for _, key := range []string{"mysqlServerIds", "mysqlDataServerIds", "clusterServerIds"} {
		if values := stringSliceParam(params, key); len(values) > 0 {
			return values
		}
	}
	return uniqueStrings(fallback)
}

func mysqlRouterEnabled(params map[string]any) bool {
	for _, key := range []string{"installRouter", "deployRouter", "mysqlRouterEnabled"} {
		if value, ok := params[key]; ok {
			return boolParam(value)
		}
	}
	return false
}

func mysqlRouterServerIDs(params map[string]any, fallback []string) []string {
	for _, key := range []string{"routerServerIds", "mysqlRouterServerIds"} {
		if values := stringSliceParam(params, key); len(values) > 0 {
			return values
		}
	}
	return uniqueStrings(fallback)
}

func mysqlRouterBasePort(params map[string]any) int {
	if value := intParam(params, "routerBasePort", 0); value > 0 {
		return value
	}
	return intParam(params, "basePort", 6446)
}

func mysqlRouterBindAddress(params map[string]any) string {
	return stringParam(params, "routerBindAddress", stringParam(params, "bindAddress", "0.0.0.0"))
}

func clusterNodeInstalledMessage(copy Copy) string {
	if strings.TrimSpace(copy.ClusterNodeInstalled) != "" {
		return copy.ClusterNodeInstalled
	}
	return "MySQL InnoDB Cluster 节点已安装，实例已记录：%s"
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
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return normalizePort(v, fallback)
	case int64:
		return normalizePort(int(v), fallback)
	case float64:
		return normalizePort(int(v), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePort(n, fallback)
	default:
		return fallback
	}
}

func boolParam(value any) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", "true", "yes", "y", "on":
			return true
		default:
			return false
		}
	case int:
		return v != 0
	case int64:
		return v != 0
	case float64:
		return v != 0
	case json.Number:
		n, _ := v.Int64()
		return n != 0
	default:
		return false
	}
}

func stringSliceParam(params map[string]any, key string) []string {
	if params == nil {
		return nil
	}
	value, ok := params[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return uniqueStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			out = append(out, fmt.Sprint(item))
		}
		return uniqueStrings(out)
	case string:
		return uniqueStrings(strings.Split(v, ","))
	default:
		return nil
	}
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func normalizePort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
