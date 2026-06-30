package docker

import (
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	DefaultBridgeCIDR    = "172.17.0.1/16"
	DefaultRemoteAPIPort = 2375
)

type InstallOptions struct {
	BridgeCIDR    string
	RemoteAPIPort int
}

func NormalizeInstallOptions(options InstallOptions) InstallOptions {
	options.BridgeCIDR = strings.TrimSpace(options.BridgeCIDR)
	if options.BridgeCIDR == "" {
		options.BridgeCIDR = DefaultBridgeCIDR
	}
	if options.RemoteAPIPort <= 0 || options.RemoteAPIPort > 65535 {
		options.RemoteAPIPort = DefaultRemoteAPIPort
	}
	return options
}

func RemoteAPIHost(host string, port int) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	return "tcp://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func InstallOptionSummary(options InstallOptions) string {
	options = NormalizeInstallOptions(options)
	return fmt.Sprintf("bridge=%s apiPort=%d", options.BridgeCIDR, options.RemoteAPIPort)
}

func dockerInstallOptions(parameters map[string]any) InstallOptions {
	return NormalizeInstallOptions(InstallOptions{
		BridgeCIDR:    stringParam(parameters, "dockerBridgeCIDR"),
		RemoteAPIPort: intParam(parameters, "remoteAPIPort"),
	})
}

func stringParam(parameters map[string]any, key string) string {
	if parameters == nil {
		return ""
	}
	value, ok := parameters[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func intParam(parameters map[string]any, key string) int {
	if parameters == nil {
		return 0
	}
	switch value := parameters[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case json.Number:
		parsed, _ := value.Int64()
		return int(parsed)
	case string:
		var parsed int
		_, _ = fmt.Sscanf(strings.TrimSpace(value), "%d", &parsed)
		return parsed
	default:
		return 0
	}
}
