package aifar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

const (
	releaseLayout      = "release-v1"
	releasePhaseActive = "active"
	releaseKeepCount   = 3
	releasesDirName    = "releases"
	currentLinkName    = "current"
	releaseEnvDirName  = "env"
	ingressDirName     = "ingress"
	ingressConfigName  = "nginx.conf"

	defaultOauthPort      = 38001
	defaultPermissionPort = 38010
	defaultSystemPort     = 38002
	defaultFilePort       = 38005
	defaultMessagePort    = 38008
	defaultIMPort         = 38031
	defaultContactsPort   = 38032
	defaultMeetingPort    = 38033
)

func newReleaseID(version string, t time.Time) string {
	t = t.UTC()
	return t.Format("20060102T150405.000000000Z") + "-" + sanitizeReleasePart(version)
}

func sanitizeReleasePart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "release"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		return "release"
	}
	return out
}

func composeProjectName(releaseID string) string {
	return sanitizeComposeName("aifar_admin_" + releaseID)
}

func releaseContainerName(serviceName, releaseID string) string {
	return sanitizeContainerName("aifar-" + serviceName + "-" + releaseID)
}

func releaseReplicaContainerName(serviceName, releaseID string, replicaID int) string {
	base := releaseContainerName(serviceName, releaseID)
	if replicaID <= 1 {
		return base
	}
	return sanitizeContainerName(fmt.Sprintf("%s-r%d", base, replicaID))
}

func releaseInternalNetworkName(releaseID string) string {
	return sanitizeContainerName("aifar-admin-" + releaseID + "-internal")
}

func ingressContainerName() string {
	return "aifar-admin-ingress"
}

func ingressConfigPath(installRoot string) string {
	return strings.TrimRight(installRoot, "/") + "/" + ingressDirName + "/" + ingressConfigName
}

func sanitizeComposeName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "aifar"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case r == '.':
			b.WriteByte('-')
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "aifar"
	}
	if out[0] < 'a' || out[0] > 'z' {
		return "aifar_" + out
	}
	return out
}

func sanitizeContainerName(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "aifar"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		return "aifar"
	}
	return out
}

func releaseContainers(releaseID string) map[string]string {
	out := make(map[string]string, len(serviceOrder))
	for _, service := range serviceOrder {
		out[service] = releaseContainerName(service, releaseID)
	}
	return out
}

func defaultDesiredReplicas() map[string]int {
	out := make(map[string]int, len(serviceOrder))
	for _, service := range serviceOrder {
		out[service] = 1
	}
	return out
}

func serviceDefaultPort(service string, gatewayPort, webPort int) int {
	switch service {
	case "gateway":
		return gatewayPort
	case "web-vue3":
		return webPort
	case "oauth":
		return defaultOauthPort
	case "permission":
		return defaultPermissionPort
	case "system":
		return defaultSystemPort
	case "file":
		return defaultFilePort
	case "message":
		return defaultMessagePort
	case "im":
		return defaultIMPort
	case "contacts":
		return defaultContactsPort
	case "meeting":
		return defaultMeetingPort
	default:
		return 0
	}
}

func releaseEndpoint(service, releaseID string, replicaID, port int) map[string]any {
	return map[string]any{
		"container": releaseReplicaContainerName(service, releaseID, replicaID),
		"releaseId": releaseID,
		"replicaId": replicaID,
		"port":      port,
		"state":     "active",
	}
}

func releaseEndpointsForService(service, releaseID string, replicas, gatewayPort, webPort int) []map[string]any {
	if replicas < 1 {
		replicas = 1
	}
	port := serviceDefaultPort(service, gatewayPort, webPort)
	out := make([]map[string]any, 0, replicas)
	for replicaID := 1; replicaID <= replicas; replicaID++ {
		out = append(out, releaseEndpoint(service, releaseID, replicaID, port))
	}
	return out
}

func releaseActiveEndpoints(releaseID string, gatewayPort, webPort int) map[string]any {
	out := make(map[string]any, len(serviceOrder))
	for _, service := range serviceOrder {
		out[service] = releaseEndpointsForService(service, releaseID, 1, gatewayPort, webPort)
	}
	return out
}

func activeServicesFromEndpoints(desired map[string]int, endpoints map[string]any) map[string]any {
	out := make(map[string]any, len(serviceOrder))
	for _, service := range serviceOrder {
		out[service] = map[string]any{
			"desiredReplicas": desired[service],
			"activeEndpoints": endpoints[service],
		}
	}
	return out
}

func desiredReplicasFromMetadata(metadata map[string]any) map[string]int {
	out := defaultDesiredReplicas()
	switch raw := metadata["desiredReplicas"].(type) {
	case map[string]int:
		for key, value := range raw {
			if value < 1 {
				value = 1
			}
			out[key] = value
		}
		return out
	case map[string]any:
		for key, value := range raw {
			n := intFromAny(value, 1)
			if n < 1 {
				n = 1
			}
			out[key] = n
		}
		return out
	}
	return out
}

func desiredReplicasFromAny(value any) map[string]int {
	return desiredReplicasFromMetadata(map[string]any{"desiredReplicas": value})
}

func intFromAny(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		var n int
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
			return n
		}
	}
	return fallback
}

func activeEndpointsFromMetadata(metadata map[string]any) map[string]any {
	if raw, ok := metadata["activeEndpoints"].(map[string]any); ok && len(raw) > 0 {
		out := make(map[string]any, len(raw))
		for key, value := range raw {
			out[key] = value
		}
		return out
	}
	releaseID := strings.TrimSpace(fmt.Sprint(metadata["releaseId"]))
	gatewayPort := intFromAny(metadata["gatewayPort"], defaultGatewayPort)
	webPort := intFromAny(metadata["webPort"], defaultWebPort)
	if releaseID == "" {
		return map[string]any{}
	}
	return releaseActiveEndpoints(releaseID, gatewayPort, webPort)
}

func releaseRoutes(releaseID string, gatewayPort, webPort int) map[string]any {
	return map[string]any{
		"gateway": map[string]any{
			"container": releaseContainerName("gateway", releaseID),
			"port":      gatewayPort,
		},
		"web-vue3": map[string]any{
			"container": releaseContainerName("web-vue3", releaseID),
			"port":      webPort,
		},
	}
}

func releaseOrchestrationMetadata(installRoot, releaseID, ingressNetwork string, gatewayPort, webPort int) map[string]any {
	desired := defaultDesiredReplicas()
	activeEndpoints := releaseActiveEndpoints(releaseID, gatewayPort, webPort)
	return map[string]any{
		"composeProject":    composeProjectName(releaseID),
		"ingressNetwork":    ingressNetwork,
		"internalNetwork":   releaseInternalNetworkName(releaseID),
		"ingressContainer":  ingressContainerName(),
		"ingressConfigPath": ingressConfigPath(installRoot),
		"activeRoutes":      releaseRoutes(releaseID, gatewayPort, webPort),
		"containers":        releaseContainers(releaseID),
		"desiredReplicas":   desired,
		"activeEndpoints":   activeEndpoints,
		"activeServices":    activeServicesFromEndpoints(desired, activeEndpoints),
		"autoscalePolicy":   defaultAutoscalePolicy().metadata(),
		"releasePhase":      releasePhaseActive,
	}
}

func releaseManifestFields(releaseID, ingressNetwork string, gatewayPort, webPort int) map[string]any {
	desired := defaultDesiredReplicas()
	endpoints := releaseActiveEndpoints(releaseID, gatewayPort, webPort)
	return map[string]any{
		"composeProject":  composeProjectName(releaseID),
		"ingressNetwork":  ingressNetwork,
		"internalNetwork": releaseInternalNetworkName(releaseID),
		"containers":      releaseContainers(releaseID),
		"routes":          releaseRoutes(releaseID, gatewayPort, webPort),
		"desiredReplicas": desired,
		"endpoints":       endpoints,
		"activeServices":  activeServicesFromEndpoints(desired, endpoints),
	}
}

func installConfigHash(options InstallOptions) string {
	data, _ := json.Marshal(map[string]any{
		"timezone":        options.Timezone,
		"networkName":     options.NetworkName,
		"appCPUs":         options.AppCPUs,
		"appMemoryLimit":  options.AppMemoryLimit,
		"gatewayPort":     options.GatewayPort,
		"webPort":         options.WebPort,
		"nacosWebPort":    options.NacosWebPort,
		"nacosAPIPort":    options.NacosAPIPort,
		"nacosSource":     options.NacosSource,
		"nacosInstanceId": options.NacosInstanceID,
		"nacosHost":       options.NacosHost,
		"nacosUser":       options.NacosUser,
		"nacosNamespace":  options.NacosNamespace,
		"services":        serviceOrder,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func partialUpdateConfigHash(baseHash, serviceName, artifactName, artifactSHA256 string) string {
	data, _ := json.Marshal(map[string]any{
		"baseConfigHash": baseHash,
		"service":        serviceName,
		"artifact":       artifactName,
		"artifactSHA256": artifactSHA256,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func partialBundleUpdateConfigHash(baseHash string, artifacts []artifactInfo) string {
	data, _ := json.Marshal(map[string]any{
		"baseConfigHash": baseHash,
		"artifacts":      artifacts,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
