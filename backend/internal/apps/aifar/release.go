package aifar

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

const (
	releaseLayout     = "release-v1"
	releaseKeepCount  = 3
	releasesDirName   = "releases"
	currentLinkName   = "current"
	releaseEnvDirName = "env"
	ingressDirName    = "ingress"
	ingressConfigName = "nginx.conf"
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
	return map[string]any{
		"composeProject":    composeProjectName(releaseID),
		"ingressNetwork":    ingressNetwork,
		"internalNetwork":   releaseInternalNetworkName(releaseID),
		"ingressContainer":  ingressContainerName(),
		"ingressConfigPath": ingressConfigPath(installRoot),
		"activeRoutes":      releaseRoutes(releaseID, gatewayPort, webPort),
		"containers":        releaseContainers(releaseID),
	}
}

func releaseManifestFields(releaseID, ingressNetwork string, gatewayPort, webPort int) map[string]any {
	return map[string]any{
		"composeProject":  composeProjectName(releaseID),
		"ingressNetwork":  ingressNetwork,
		"internalNetwork": releaseInternalNetworkName(releaseID),
		"containers":      releaseContainers(releaseID),
		"routes":          releaseRoutes(releaseID, gatewayPort, webPort),
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
