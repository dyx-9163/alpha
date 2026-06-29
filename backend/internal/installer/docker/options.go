package docker

import (
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
