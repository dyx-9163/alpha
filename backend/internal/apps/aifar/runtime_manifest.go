package aifar

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"regexp"
	"strings"

	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

var (
	runtimeHealthPathPattern = regexp.MustCompile(`^/[A-Za-z0-9._~/%-]*$`)
	runtimeCPUPattern        = regexp.MustCompile(`^(?:[0-9]+(?:\.[0-9]+)?|\.[0-9]+)$`)
	runtimeMemoryPattern     = regexp.MustCompile(`^[0-9]+(?:[bBkKmMgGtTpP](?:i?[bB])?)?$`)
)

func buildRuntimeManifest(instance store.AppInstance, current store.AIFARDeployment, generation int64) (runtimeagent.DeploymentManifest, error) {
	metadata := metadataFromInstance(instance)
	installRoot := path.Clean(stringFromMetadata(metadata, "installRoot", ""))
	serviceName := cleanAIFARServiceName(current.ServiceName)
	definition, ok := catalogDefinition(serviceDefinitionsFromMetadata(metadata), serviceName)
	if !ok {
		return runtimeagent.DeploymentManifest{}, fmt.Errorf("AIFAR service %s is not defined", serviceName)
	}
	if generation <= 0 {
		return runtimeagent.DeploymentManifest{}, errors.New("AIFAR deployment generation must be positive")
	}
	if !runtimeHealthPathPattern.MatchString(definition.HealthPath) {
		return runtimeagent.DeploymentManifest{}, errors.New("AIFAR service health path is invalid")
	}
	if (definition.Resources.CPUs != "" && !runtimeCPUPattern.MatchString(definition.Resources.CPUs)) ||
		(definition.Resources.Memory != "" && !runtimeMemoryPattern.MatchString(definition.Resources.Memory)) {
		return runtimeagent.DeploymentManifest{}, errors.New("AIFAR service resources are invalid")
	}

	manifest := runtimeManifestDefaults(instance.ID, installRoot, definition, current, generation, metadata)
	if strings.TrimSpace(current.SpecJSON) != "" {
		var persisted runtimeagent.DeploymentManifest
		decoder := json.NewDecoder(strings.NewReader(current.SpecJSON))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&persisted); err != nil {
			return runtimeagent.DeploymentManifest{}, errors.New("stored AIFAR deployment manifest is invalid")
		}
		var trailing any
		if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
			return runtimeagent.DeploymentManifest{}, errors.New("stored AIFAR deployment manifest contains trailing data")
		}
		persisted = runtimeagent.NormalizeDeploymentManifest(persisted)
		manifest.Spec = persisted.Spec
		manifest.Service = persisted.Service
		manifest.APIVersion = runtimeagent.ManifestAPIVersion
		manifest.Kind = runtimeagent.DeploymentManifestKind
		manifest.Metadata = runtimeagent.DeploymentMetadata{InstanceID: instance.ID, Name: serviceName, Generation: generation}
		manifest.Spec.ServiceName = serviceName
		manifest.Service.Name = serviceName
		manifest.Spec.Replicas = current.DesiredReplicas
		if revision := strings.TrimSpace(current.CurrentRevision); revision != "" {
			manifest.Spec.PodRevision = revision
		}
	}
	manifest = runtimeagent.NormalizeDeploymentManifest(manifest)
	config := runtimeInstanceConfig(instance, metadata, installRoot)
	if err := runtimeagent.ValidateDeploymentManifest(config, manifest); err != nil {
		return runtimeagent.DeploymentManifest{}, fmt.Errorf("AIFAR deployment manifest is invalid: %w", err)
	}
	return manifest, nil
}

func runtimeManifestDefaults(instanceID, installRoot string, definition serviceDefinition, current store.AIFARDeployment, generation int64, metadata map[string]any) runtimeagent.DeploymentManifest {
	serviceName := definition.Name
	revision := strings.TrimSpace(current.CurrentRevision)
	envDir := path.Join(installRoot, "runtime", releaseEnvDirName)
	logDir := path.Join(installRoot, "logs", serviceName)
	timezone := stringFromMetadata(metadata, "timezone", "system")
	port := definition.Port
	if definition.Role == "gateway" {
		port = intFromMetadata(metadata, "gatewayPort", port)
	}
	if definition.Role == "web" {
		port = intFromMetadata(metadata, "webPort", port)
	}

	spec := runtimeagent.DeploymentSpec{
		ServiceName:    serviceName,
		DeploymentName: definition.ApplicationName,
		Image:          "aifar-" + serviceName + ":" + revision,
		PodRevision:    revision,
		Replicas:       current.DesiredReplicas,
		Strategy:       runtimeagent.NormalizeDeploymentStrategy(runtimeagent.DeploymentStrategySpec{}),
		Ports:          []runtimeagent.ContainerPort{{Name: "http", ContainerPort: port}},
		Resources:      definition.Resources,
		HealthCheck: runtimeagent.HealthCheckSpec{
			Command:  runtimeHealthCommand(definition.Kind, definition.HealthPath, port),
			Interval: "15s", Timeout: "5s", Retries: 3, StartPeriod: "30s",
		},
		Environment: map[string]string{
			"APP_CONTAINER_NAME": "${containerName}",
			"AIFAR_LOG_DIR":      "/opt/aifar/logs",
			"LOG_DIR":            "/opt/aifar/logs",
			"TZ":                 timezone,
		},
	}
	if strings.TrimSpace(spec.DeploymentName) == "" {
		spec.DeploymentName = "aifar-" + serviceName
	}
	if definition.Kind == "web" {
		spec.EnvFiles = []string{path.Join(envDir, serviceName+".env")}
		spec.Volumes = []runtimeagent.VolumeMount{
			{Source: logDir, Target: "/opt/aifar/logs"},
			{Source: logDir, Target: "/var/log/nginx"},
		}
	} else {
		spec.EnvFiles = []string{path.Join(envDir, "java-common.env"), path.Join(envDir, "java-secrets.env"), path.Join(envDir, serviceName+".env")}
		spec.Volumes = []runtimeagent.VolumeMount{
			{Source: envDir, Target: "/opt/aifar/runtime/env", ReadOnly: true},
			{Source: logDir, Target: "/opt/aifar/logs"},
			{Source: logDir, Target: "/data/aifarsoft/javaApi/aifar-" + serviceName + "/log"},
		}
		spec.Entrypoint = []string{"/bin/sh"}
		spec.Command = []string{"/opt/aifar/runtime/env/java-entrypoint.sh"}
		spec.Environment["AIFAR_SERVICE_NAME"] = serviceName
	}
	return runtimeagent.DeploymentManifest{
		APIVersion: runtimeagent.ManifestAPIVersion,
		Kind:       runtimeagent.DeploymentManifestKind,
		Metadata:   runtimeagent.DeploymentMetadata{InstanceID: instanceID, Name: serviceName, Generation: generation},
		Spec:       spec,
		Service: runtimeagent.ServiceSpec{
			Name: serviceName, AppName: definition.ApplicationName,
			ListenPort: port, TargetPort: port, AffinityPolicy: definition.AffinityPolicy,
		},
	}
}

func runtimeInstanceConfig(instance store.AppInstance, metadata map[string]any, installRoot string) runtimeagent.InstanceConfig {
	return runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion,
		InstanceID: instance.ID, InstallRoot: installRoot,
		Network: stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName)),
		Ingress: runtimeagent.IngressSpec{
			Mode:           runtimeagent.DefaultIngressMode,
			GatewayService: serviceNameForRole(serviceDefinitionsFromMetadata(metadata), "gateway"),
			WebService:     serviceNameForRole(serviceDefinitionsFromMetadata(metadata), "web"),
			GatewayPort:    intFromMetadata(metadata, "gatewayPort", defaultGatewayPort),
			WebPort:        intFromMetadata(metadata, "webPort", defaultWebPort),
		},
	})
}

func runtimeHealthCommand(kind, healthPath string, port int) string {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", port, healthPath)
	if kind == "web" {
		return "wget -q -T 3 -O /dev/null '" + url + "' || exit 1"
	}
	return "curl -fsS --connect-timeout 3 '" + url + "' >/dev/null || exit 1"
}
