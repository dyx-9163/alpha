package runtimeagent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"
)

const (
	ManifestAPIVersion      = "aifar.io/v1alpha1"
	DeploymentManifestKind  = "Deployment"
	LegacyBootstrapMaxBytes = 4 << 20
)

var (
	ErrStaleDeploymentGeneration    = errors.New("stale deployment generation")
	ErrDeploymentGenerationConflict = errors.New("deployment generation conflict")

	serviceManifestNamePattern  = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	instanceManifestNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	networkManifestNamePattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`)
	revisionManifestPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	dockerImageManifestPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/:@-]{0,511}$`)
	dockerLabelManifestPattern  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
)

type DeploymentManifest struct {
	APIVersion string             `json:"apiVersion"`
	Kind       string             `json:"kind"`
	Metadata   DeploymentMetadata `json:"metadata"`
	Spec       DeploymentSpec     `json:"spec"`
	Service    ServiceSpec        `json:"service"`
}

type DeploymentMetadata struct {
	InstanceID string `json:"instanceId"`
	Name       string `json:"name"`
	Generation int64  `json:"generation"`
}

type DeploymentAcceptance struct {
	Accepted   bool   `json:"accepted"`
	Generation int64  `json:"generation"`
	SpecHash   string `json:"specHash"`
}

type InstanceConfig struct {
	APIVersion  string      `json:"apiVersion"`
	InstanceID  string      `json:"instanceId"`
	InstallRoot string      `json:"installRoot"`
	Network     string      `json:"network"`
	Ingress     IngressSpec `json:"ingress"`
}

type DeploymentCondition struct {
	Type               string    `json:"type"`
	Status             bool      `json:"status"`
	Reason             string    `json:"reason"`
	Message            string    `json:"message,omitempty"`
	Generation         int64     `json:"generation"`
	LastTransitionTime time.Time `json:"lastTransitionTime"`
}

type DeploymentState struct {
	InstanceID         string                `json:"instanceId"`
	ServiceName        string                `json:"serviceName"`
	Generation         int64                 `json:"generation"`
	ObservedGeneration int64                 `json:"observedGeneration"`
	SpecHash           string                `json:"specHash"`
	DesiredReplicas    int                   `json:"desiredReplicas"`
	CurrentReplicas    int                   `json:"currentReplicas"`
	ReadyReplicas      int                   `json:"readyReplicas"`
	Conditions         []DeploymentCondition `json:"conditions"`
}

// RuntimeDeploymentSnapshot exposes only identity, generation/hash proof and
// runtime observation. It deliberately omits the manifest body, including env
// file paths and other potentially sensitive desired-state fields.
type RuntimeDeploymentSnapshot struct {
	ServiceName        string `json:"serviceName"`
	ManifestGeneration int64  `json:"manifestGeneration"`
	ManifestSpecHash   string `json:"manifestSpecHash"`
	StateGeneration    int64  `json:"stateGeneration"`
	ObservedGeneration int64  `json:"observedGeneration"`
	StateSpecHash      string `json:"stateSpecHash"`
	DesiredReplicas    int    `json:"desiredReplicas"`
}

type RuntimeInstanceSnapshot struct {
	Instance    InstanceConfig              `json:"instance"`
	Deployments []RuntimeDeploymentSnapshot `json:"deployments"`
}

func NormalizeInstanceConfig(config InstanceConfig) InstanceConfig {
	config.APIVersion = strings.TrimSpace(config.APIVersion)
	if config.APIVersion == "" {
		config.APIVersion = ManifestAPIVersion
	}
	config.InstanceID = strings.TrimSpace(config.InstanceID)
	config.InstallRoot = strings.TrimSpace(config.InstallRoot)
	config.Network = strings.TrimSpace(config.Network)
	config.Ingress.Mode = strings.TrimSpace(config.Ingress.Mode)
	if config.Ingress.Mode == "" {
		config.Ingress.Mode = DefaultIngressMode
	}
	config.Ingress.GatewayService = normalizeServiceManifestName(config.Ingress.GatewayService)
	if config.Ingress.GatewayService == "" {
		config.Ingress.GatewayService = "gateway"
	}
	config.Ingress.WebService = normalizeServiceManifestName(config.Ingress.WebService)
	if config.Ingress.WebService == "" {
		config.Ingress.WebService = "web-vue3"
	}
	if config.Ingress.GatewayPort == 0 {
		config.Ingress.GatewayPort = DefaultGatewayPort
	}
	if config.Ingress.WebPort == 0 {
		config.Ingress.WebPort = DefaultWebPort
	}
	return config
}

func NormalizeDeploymentManifest(manifest DeploymentManifest) DeploymentManifest {
	manifest.Spec.EnvFiles = append([]string(nil), manifest.Spec.EnvFiles...)
	manifest.Spec.Volumes = append([]VolumeMount(nil), manifest.Spec.Volumes...)
	manifest.APIVersion = strings.TrimSpace(manifest.APIVersion)
	if manifest.APIVersion == "" {
		manifest.APIVersion = ManifestAPIVersion
	}
	manifest.Kind = strings.TrimSpace(manifest.Kind)
	if manifest.Kind == "" {
		manifest.Kind = DeploymentManifestKind
	}
	manifest.Metadata.InstanceID = strings.TrimSpace(manifest.Metadata.InstanceID)
	manifest.Metadata.Name = normalizeServiceManifestName(manifest.Metadata.Name)
	manifest.Spec.ServiceName = normalizeServiceManifestName(manifest.Spec.ServiceName)
	manifest.Service.Name = normalizeServiceManifestName(manifest.Service.Name)

	serviceName := manifest.Metadata.Name
	if serviceName == "" {
		serviceName = manifest.Spec.ServiceName
	}
	if serviceName == "" {
		serviceName = manifest.Service.Name
	}
	if manifest.Metadata.Name == "" {
		manifest.Metadata.Name = serviceName
	}
	if manifest.Spec.ServiceName == "" {
		manifest.Spec.ServiceName = serviceName
	}
	if manifest.Service.Name == "" {
		manifest.Service.Name = serviceName
	}

	manifest.Spec.DeploymentName = strings.TrimSpace(manifest.Spec.DeploymentName)
	manifest.Spec.Image = strings.TrimSpace(manifest.Spec.Image)
	manifest.Spec.PodRevision = strings.TrimSpace(manifest.Spec.PodRevision)
	if manifest.Spec.DeploymentName == "" && serviceName != "" {
		manifest.Spec.DeploymentName = serviceAppName(ServiceSpec{Name: serviceName})
	}
	manifest.Spec.Strategy = NormalizeDeploymentStrategy(manifest.Spec.Strategy)
	for index := range manifest.Spec.EnvFiles {
		manifest.Spec.EnvFiles[index] = strings.TrimSpace(manifest.Spec.EnvFiles[index])
	}
	for index := range manifest.Spec.Volumes {
		manifest.Spec.Volumes[index].Source = strings.TrimSpace(manifest.Spec.Volumes[index].Source)
		manifest.Spec.Volumes[index].Target = strings.TrimSpace(manifest.Spec.Volumes[index].Target)
	}

	manifest.Service.AppName = strings.TrimSpace(manifest.Service.AppName)
	if manifest.Service.AppName == "" && serviceName != "" {
		manifest.Service.AppName = serviceAppName(ServiceSpec{Name: serviceName})
	}
	manifest.Service.AffinityPolicy = strings.ToLower(strings.TrimSpace(manifest.Service.AffinityPolicy))
	if manifest.Service.ListenPort == 0 {
		manifest.Service.ListenPort = manifest.Service.Port
	}
	if manifest.Service.TargetPort == 0 {
		manifest.Service.TargetPort = manifest.Service.Port
	}
	if manifest.Service.Port == 0 {
		manifest.Service.Port = manifest.Service.ListenPort
	}
	if len(manifest.Spec.Ports) == 0 && manifest.Service.TargetPort > 0 {
		manifest.Spec.Ports = []ContainerPort{{Name: "http", ContainerPort: manifest.Service.TargetPort}}
	}
	return manifest
}

func ValidateInstanceConfig(config InstanceConfig) error {
	if config.APIVersion != ManifestAPIVersion {
		return fmt.Errorf("instance apiVersion must be %s", ManifestAPIVersion)
	}
	if err := validateInstanceManifestName(config.InstanceID); err != nil {
		return err
	}
	if !validManifestRoot(config.InstallRoot) {
		return errors.New("instance installRoot must be a clean absolute non-root path")
	}
	if !networkManifestNamePattern.MatchString(config.Network) {
		return errors.New("instance network is invalid")
	}
	if config.Ingress.Mode != DefaultIngressMode {
		return fmt.Errorf("instance ingress mode must be %s", DefaultIngressMode)
	}
	if err := validateServiceManifestName(config.Ingress.GatewayService); err != nil {
		return fmt.Errorf("invalid gateway service: %w", err)
	}
	if err := validateServiceManifestName(config.Ingress.WebService); err != nil {
		return fmt.Errorf("invalid web service: %w", err)
	}
	if !validManifestPort(config.Ingress.GatewayPort) || !validManifestPort(config.Ingress.WebPort) {
		return errors.New("instance ingress ports must be between 1 and 65535")
	}
	if config.Ingress.GatewayPort == config.Ingress.WebPort {
		return errors.New("instance ingress ports must be different")
	}
	return nil
}

func ValidateDeploymentManifest(config InstanceConfig, manifest DeploymentManifest) error {
	if err := ValidateInstanceConfig(config); err != nil {
		return fmt.Errorf("invalid instance config: %w", err)
	}
	if manifest.APIVersion != ManifestAPIVersion {
		return fmt.Errorf("deployment apiVersion must be %s", ManifestAPIVersion)
	}
	if manifest.Kind != DeploymentManifestKind {
		return fmt.Errorf("deployment kind must be %s", DeploymentManifestKind)
	}
	if err := validateInstanceManifestName(manifest.Metadata.InstanceID); err != nil {
		return err
	}
	if manifest.Metadata.InstanceID != config.InstanceID {
		return errors.New("deployment instance identity does not match instance config")
	}
	if err := validateServiceManifestName(manifest.Metadata.Name); err != nil {
		return err
	}
	if manifest.Spec.ServiceName != manifest.Metadata.Name || manifest.Service.Name != manifest.Metadata.Name {
		return errors.New("deployment service identities must match")
	}
	if manifest.Metadata.Generation <= 0 {
		return errors.New("deployment generation must be positive")
	}
	if manifest.Spec.RestartGeneration < 0 {
		return errors.New("deployment restart generation must not be negative")
	}
	if manifest.Spec.Replicas < 0 {
		return errors.New("deployment replicas must not be negative")
	}
	if !dockerImageManifestPattern.MatchString(manifest.Spec.Image) || strings.HasPrefix(manifest.Spec.Image, "-") || strings.Contains(manifest.Spec.Image, "://") {
		return errors.New("deployment image is invalid")
	}
	if !revisionManifestPattern.MatchString(manifest.Spec.PodRevision) {
		return errors.New("deployment pod revision is invalid")
	}
	if manifest.Spec.DeploymentName == "" || containsManifestControl(manifest.Spec.DeploymentName) {
		return errors.New("deployment name is invalid")
	}
	if !validManifestPort(manifest.Service.ListenPort) || !validManifestPort(manifest.Service.TargetPort) {
		return errors.New("service ports must be between 1 and 65535")
	}
	if manifest.Service.Port != 0 && !validManifestPort(manifest.Service.Port) {
		return errors.New("service compatibility port must be between 1 and 65535")
	}
	for _, port := range manifest.Spec.Ports {
		if !validManifestPort(port.ContainerPort) {
			return errors.New("deployment container ports must be between 1 and 65535")
		}
	}
	for key, value := range manifest.Spec.Labels {
		if !dockerLabelManifestPattern.MatchString(key) || strings.HasPrefix(strings.ToLower(key), "aifar.") {
			return errors.New("deployment label key is invalid or reserved")
		}
		if len(value) > 4096 || containsManifestControl(value) {
			return errors.New("deployment label value is invalid")
		}
	}
	for index, envFile := range manifest.Spec.EnvFiles {
		if !manifestPathUnderRoot(config.InstallRoot, envFile) {
			return fmt.Errorf("deployment envFiles[%d] must be a clean path under installRoot", index)
		}
	}
	for index, volume := range manifest.Spec.Volumes {
		if !manifestPathUnderRoot(config.InstallRoot, volume.Source) {
			return fmt.Errorf("deployment volumes[%d] source must be a clean path under installRoot", index)
		}
		if !validManifestContainerPath(volume.Target) {
			return fmt.Errorf("deployment volumes[%d] target must be a clean absolute non-root path", index)
		}
	}
	return nil
}

func DeploymentManifestSpecHash(manifest DeploymentManifest) (string, error) {
	manifest = NormalizeDeploymentManifest(manifest)
	payload := struct {
		Spec    DeploymentSpec `json:"spec"`
		Service ServiceSpec    `json:"service"`
	}{Spec: manifest.Spec, Service: manifest.Service}
	data, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func normalizeServiceManifestName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func validateInstanceManifestName(value string) error {
	if !instanceManifestNamePattern.MatchString(value) || value == "." || value == ".." {
		return errors.New("deployment instance id is invalid")
	}
	return nil
}

func validateServiceManifestName(value string) error {
	if !serviceManifestNamePattern.MatchString(value) {
		return errors.New("deployment service name is invalid")
	}
	return nil
}

func validManifestPort(value int) bool {
	return value >= 1 && value <= 65535
}

func validManifestRoot(value string) bool {
	return value != "" && value != "/" && path.IsAbs(value) && path.Clean(value) == value && !strings.Contains(value, `\`) && !containsManifestControl(value)
}

func manifestPathUnderRoot(root, value string) bool {
	if !validManifestRoot(root) || value == "" || !path.IsAbs(value) || path.Clean(value) != value || strings.Contains(value, `\`) || containsManifestControl(value) {
		return false
	}
	return strings.HasPrefix(value, root+"/")
}

func validManifestContainerPath(value string) bool {
	return value != "" && value != "/" && path.IsAbs(value) && path.Clean(value) == value && !strings.Contains(value, `\`) && !containsManifestControl(value)
}

func containsManifestControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}
