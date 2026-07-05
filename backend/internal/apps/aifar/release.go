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
	releasePhaseActive          = "active"
	orchestrationModelK8sLikeV1 = "k8s-like-v1"
	legacyOrchestrationModel    = "legacy-release-v1"
	releaseKeepCount            = 3
	releaseEnvDirName           = "env"
	runtimeSpecDirName          = "agent"

	defaultOauthPort      = 38001
	defaultPermissionPort = 38010
	defaultSystemPort     = 38002
	defaultFilePort       = 38005
	defaultMessagePort    = 38008
	defaultIMPort         = 38031
	defaultContactsPort   = 38032
	defaultMeetingPort    = 38033
)

func runtimeSpecPath(installRoot string) string {
	return strings.TrimRight(installRoot, "/") + "/runtime/" + runtimeSpecDirName + "/runtime-spec.json"
}

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

func releaseContainerName(serviceName, releaseID string) string {
	return podContainerName(serviceName, releaseID, 1)
}

func releaseReplicaContainerName(serviceName, releaseID string, replicaID int) string {
	return podContainerName(serviceName, releaseID, replicaID)
}

func podContainerName(serviceName, revision string, replicaID int) string {
	if replicaID < 1 {
		replicaID = 1
	}
	return sanitizeContainerName(fmt.Sprintf("aifar-pod-admin-%s-%s-r%d", serviceName, revision, replicaID))
}

func podID(serviceName, revision string, replicaID int) string {
	if replicaID < 1 {
		replicaID = 1
	}
	return sanitizeContainerName(fmt.Sprintf("%s-%s-r%d", serviceName, revision, replicaID))
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
	return releaseContainersForServices(releaseID, serviceOrder)
}

func releaseContainersForServices(releaseID string, services []string) map[string]string {
	services = serviceListOrDefault(services)
	out := make(map[string]string, len(services))
	for _, service := range services {
		out[service] = podContainerName(service, releaseID, 1)
	}
	return out
}

func defaultDesiredReplicas() map[string]int {
	return desiredReplicasForServices(serviceOrder)
}

func desiredReplicasForServices(services []string) map[string]int {
	services = serviceListOrDefault(services)
	out := make(map[string]int, len(services))
	for _, service := range services {
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
		"container": podContainerName(service, releaseID, replicaID),
		"releaseId": releaseID,
		"revision":  releaseID,
		"podId":     podID(service, releaseID, replicaID),
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
	return releaseActiveEndpointsForServices(releaseID, gatewayPort, webPort, serviceOrder)
}

func releaseActiveEndpointsForServices(releaseID string, gatewayPort, webPort int, services []string) map[string]any {
	services = serviceListOrDefault(services)
	out := make(map[string]any, len(services))
	for _, service := range services {
		out[service] = releaseEndpointsForService(service, releaseID, 1, gatewayPort, webPort)
	}
	return out
}

func activeServicesFromEndpoints(desired map[string]int, endpoints map[string]any) map[string]any {
	return activeServicesFromEndpointsForServices(desired, endpoints, serviceOrder)
}

func activeServicesFromEndpointsForServices(desired map[string]int, endpoints map[string]any, services []string) map[string]any {
	services = serviceListOrDefault(services)
	out := make(map[string]any, len(services))
	for _, service := range services {
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

func releaseRoutes(gatewayPort, webPort int) map[string]any {
	return map[string]any{
		"gateway": map[string]any{
			"systemdService": "aifar-agent",
			"service":        "gateway",
			"port":           gatewayPort,
		},
		"web-vue3": map[string]any{
			"systemdService": "aifar-agent",
			"service":        "web-vue3",
			"port":           webPort,
		},
	}
}

func releaseOrchestrationMetadata(installRoot, releaseID, ingressNetwork string, gatewayPort, webPort int, services []string) map[string]any {
	_ = installRoot
	services = serviceListOrDefault(services)
	desired := desiredReplicasForServices(services)
	activeEndpoints := releaseActiveEndpointsForServices(releaseID, gatewayPort, webPort, services)
	return map[string]any{
		"orchestrationModel": orchestrationModelK8sLikeV1,
		"ingressNetwork":     ingressNetwork,
		"runtimeService":     "aifar-agent",
		"activeRoutes":       releaseRoutes(gatewayPort, webPort),
		"containers":         releaseContainersForServices(releaseID, services),
		"desiredReplicas":    desired,
		"activeEndpoints":    activeEndpoints,
		"activeServices":     activeServicesFromEndpointsForServices(desired, activeEndpoints, services),
		"autoscalePolicy":    defaultAutoscalePolicy().metadata(),
		"releasePhase":       releasePhaseActive,
	}
}

func releaseManifestFields(releaseID, ingressNetwork string, gatewayPort, webPort int, services []string) map[string]any {
	services = serviceListOrDefault(services)
	desired := desiredReplicasForServices(services)
	endpoints := releaseActiveEndpointsForServices(releaseID, gatewayPort, webPort, services)
	return map[string]any{
		"orchestrationModel": orchestrationModelK8sLikeV1,
		"ingressNetwork":     ingressNetwork,
		"containers":         releaseContainersForServices(releaseID, services),
		"routes":             releaseRoutes(gatewayPort, webPort),
		"desiredReplicas":    desired,
		"endpoints":          endpoints,
		"activeServices":     activeServicesFromEndpointsForServices(desired, endpoints, services),
	}
}

func serviceListOrDefault(services []string) []string {
	if len(services) == 0 {
		return serviceOrder
	}
	selected := make(map[string]bool, len(services))
	out := make([]string, 0, len(services))
	for _, service := range services {
		service = cleanAIFARServiceName(service)
		if !aifarServiceSupported(service) || selected[service] {
			continue
		}
		selected[service] = true
	}
	for _, service := range serviceOrder {
		if selected[service] {
			out = append(out, service)
		}
	}
	if len(out) == 0 {
		return serviceOrder
	}
	return out
}

func servicesFromMetadata(metadata map[string]any) []string {
	raw, ok := metadata["services"]
	if !ok {
		return serviceOrder
	}
	switch items := raw.(type) {
	case []string:
		return serviceListOrDefault(items)
	case []any:
		values := make([]string, 0, len(items))
		for _, item := range items {
			values = append(values, fmt.Sprint(item))
		}
		return serviceListOrDefault(values)
	}
	return serviceOrder
}

func installConfigHash(options InstallOptions) string {
	data, _ := json.Marshal(map[string]any{
		"timezone":                options.Timezone,
		"networkName":             options.NetworkName,
		"appCPUs":                 options.AppCPUs,
		"appMemoryLimit":          options.AppMemoryLimit,
		"jvmInitialRAMPercentage": options.JVMInitialRAMPercentage,
		"jvmMaxRAMPercentage":     options.JVMMaxRAMPercentage,
		"gatewayPort":             options.GatewayPort,
		"webPort":                 options.WebPort,
		"nacosWebPort":            options.NacosWebPort,
		"nacosAPIPort":            options.NacosAPIPort,
		"nacosSource":             options.NacosSource,
		"nacosInstanceId":         options.NacosInstanceID,
		"nacosHost":               options.NacosHost,
		"nacosUser":               options.NacosUser,
		"nacosNamespace":          options.NacosNamespace,
		"services":                options.SelectedServices,
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
