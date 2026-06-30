package mysql

import (
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

func normalizePort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}
