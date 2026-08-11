package runtimeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

var (
	ErrLegacyRuntimeSpecDisabled = errors.New("legacy runtime spec is disabled")
	ErrInvalidLegacyRuntimeSpec  = errors.New("invalid legacy runtime spec")
	ErrInvalidDeploymentManifest = errors.New("invalid deployment manifest")
	errAgentStatePersistence     = errors.New("agent state persistence failed")
	legacyRuntimeBootstrapMu     sync.Mutex
)

const (
	legacyBootstrapStageDirectory  = "deployments.bootstrap-staging"
	legacyBootstrapBackupDirectory = "deployments.bootstrap-backup"
	legacyBootstrapMarkerFile      = "instance.bootstrap.json"
)

// LegacyRuntimeSpec is the former instance-wide desired-state resource. It is
// parsed only by the one-way bootstrap and the pre-switch compatibility API.
// Once instance.json exists, every legacy writer fails closed.
type LegacyRuntimeSpec struct {
	Version     string           `json:"version,omitempty"`
	InstanceID  string           `json:"instanceId,omitempty"`
	InstallRoot string           `json:"installRoot,omitempty"`
	Network     string           `json:"network,omitempty"`
	Deployments []DeploymentSpec `json:"deployments,omitempty"`
	Services    []ServiceSpec    `json:"services,omitempty"`
	Ingress     IngressSpec      `json:"ingress"`
	Nacos       NacosSpec        `json:"nacos,omitempty"`
}

// RuntimeSpec keeps the pre-switch compatibility implementation source
// compatible. New desired state must use per-service DeploymentManifest rows.
type RuntimeSpec = LegacyRuntimeSpec

type ServiceSpec struct {
	Name           string `json:"name"`
	AppName        string `json:"appName,omitempty"`
	Port           int    `json:"port,omitempty"`
	ListenPort     int    `json:"listenPort,omitempty"`
	TargetPort     int    `json:"targetPort,omitempty"`
	AffinityPolicy string `json:"affinityPolicy,omitempty"`
}

type DeploymentSpec struct {
	ServiceName       string                 `json:"serviceName"`
	DeploymentName    string                 `json:"deploymentName,omitempty"`
	Image             string                 `json:"image,omitempty"`
	PodRevision       string                 `json:"podRevision,omitempty"`
	RestartGeneration int64                  `json:"restartGeneration,omitempty"`
	Replicas          int                    `json:"replicas,omitempty"`
	Strategy          DeploymentStrategySpec `json:"strategy,omitempty"`
	Ports             []ContainerPort        `json:"ports,omitempty"`
	EnvFiles          []string               `json:"envFiles,omitempty"`
	Volumes           []VolumeMount          `json:"volumes,omitempty"`
	Resources         ResourceSpec           `json:"resources,omitempty"`
	HealthCheck       HealthCheckSpec        `json:"healthCheck,omitempty"`
	Entrypoint        []string               `json:"entrypoint,omitempty"`
	Command           []string               `json:"command,omitempty"`
	Environment       map[string]string      `json:"environment,omitempty"`
	Labels            map[string]string      `json:"labels,omitempty"`
}

type NacosSpec struct {
	Namespace       string `json:"namespace,omitempty"`
	Group           string `json:"group,omitempty"`
	Ephemeral       *bool  `json:"ephemeral,omitempty"`
	AgentIPStrategy string `json:"agentIPStrategy,omitempty"`
}

func NormalizeSpec(spec RuntimeSpec) RuntimeSpec {
	if spec.Version == "" {
		spec.Version = DefaultAgentVersion
	}
	if spec.InstanceID == "" {
		spec.InstanceID = "admin"
	}
	if spec.Network == "" {
		spec.Network = DefaultNetwork
	}
	if spec.Ingress.GatewayService == "" {
		spec.Ingress.GatewayService = "gateway"
	}
	if spec.Ingress.WebService == "" {
		spec.Ingress.WebService = "web-vue3"
	}
	if spec.Ingress.Mode == "" {
		spec.Ingress.Mode = DefaultIngressMode
	}
	if spec.Ingress.GatewayPort == 0 {
		spec.Ingress.GatewayPort = DefaultGatewayPort
	}
	if spec.Ingress.WebPort == 0 {
		spec.Ingress.WebPort = DefaultWebPort
	}
	if spec.Nacos.Namespace == "" {
		spec.Nacos.Namespace = "prod"
	}
	if spec.Nacos.AgentIPStrategy == "" {
		spec.Nacos.AgentIPStrategy = "auto"
	}
	if spec.Nacos.Ephemeral == nil {
		value := true
		spec.Nacos.Ephemeral = &value
	}
	seen := map[string]bool{}
	services := make([]ServiceSpec, 0, len(spec.Services)+2)
	for _, service := range spec.Services {
		if service.Name == "" || seen[service.Name] {
			continue
		}
		if service.AppName == "" {
			service.AppName = serviceAppName(service)
		}
		if service.ListenPort == 0 {
			service.ListenPort = service.Port
		}
		if service.TargetPort == 0 {
			service.TargetPort = service.Port
		}
		if service.Port == 0 {
			service.Port = service.ListenPort
		}
		if service.ListenPort <= 0 || service.TargetPort <= 0 {
			continue
		}
		seen[service.Name] = true
		services = append(services, service)
	}
	if !seen[spec.Ingress.GatewayService] {
		services = append(services, ServiceSpec{Name: spec.Ingress.GatewayService, AppName: "alpha-gateway", Port: spec.Ingress.GatewayPort, ListenPort: spec.Ingress.GatewayPort, TargetPort: spec.Ingress.GatewayPort})
	}
	if !seen[spec.Ingress.WebService] {
		services = append(services, ServiceSpec{Name: spec.Ingress.WebService, AppName: "web-vue3", Port: spec.Ingress.WebPort, ListenPort: spec.Ingress.WebPort, TargetPort: spec.Ingress.WebPort})
	}
	spec.Services = services
	deploymentSeen := map[string]bool{}
	deployments := make([]DeploymentSpec, 0, len(spec.Deployments))
	for _, deployment := range spec.Deployments {
		if deployment.ServiceName == "" || deploymentSeen[deployment.ServiceName] {
			continue
		}
		if deployment.DeploymentName == "" {
			if service, ok := serviceByName(spec, deployment.ServiceName); ok && service.AppName != "" {
				deployment.DeploymentName = service.AppName
			} else {
				deployment.DeploymentName = serviceAppName(ServiceSpec{Name: deployment.ServiceName})
			}
		}
		if deployment.Replicas < 0 {
			deployment.Replicas = 0
		}
		deployment.Strategy = NormalizeDeploymentStrategy(deployment.Strategy)
		if len(deployment.Ports) == 0 {
			if service, ok := serviceByName(spec, deployment.ServiceName); ok {
				deployment.Ports = []ContainerPort{{Name: "http", ContainerPort: service.TargetPort}}
			}
		}
		deploymentSeen[deployment.ServiceName] = true
		deployments = append(deployments, deployment)
	}
	spec.Deployments = deployments
	return spec
}

// LegacyBootstrapAcceptance records the complete marker-last conversion of a
// legacy instance-wide spec into the node's per-service execution cache.
type LegacyBootstrapAcceptance struct {
	Accepted    bool                   `json:"accepted"`
	InstanceID  string                 `json:"instanceId"`
	Deployments []DeploymentAcceptance `json:"deployments"`
}

// ClassifyDeploymentAcceptanceError keeps validation errors client-visible
// without ever treating state read/write failures as a bad Manifest.
func (m *Manager) ClassifyDeploymentAcceptanceError(manifest DeploymentManifest, acceptErr error) error {
	if acceptErr == nil || errors.Is(acceptErr, ErrStaleDeploymentGeneration) || errors.Is(acceptErr, ErrDeploymentGenerationConflict) {
		return acceptErr
	}
	manifest = NormalizeDeploymentManifest(manifest)
	config, err := m.manifestStore.GetInstance(manifest.Metadata.InstanceID)
	if err != nil {
		if errors.Is(err, errUnsafeManifestFilesystemShape) {
			return errors.Join(ErrInvalidDeploymentManifest, err)
		}
		return errors.Join(errAgentStatePersistence, acceptErr)
	}
	if err := ValidateDeploymentManifest(config, manifest); err != nil {
		return errors.Join(ErrInvalidDeploymentManifest, err)
	}
	if err := m.manifestStore.validateDeploymentFilesystem(config, manifest); err != nil {
		if errors.Is(err, errUnsafeManifestFilesystemShape) {
			return errors.Join(ErrInvalidDeploymentManifest, err)
		}
		return errors.Join(errAgentStatePersistence, acceptErr)
	}
	return errors.Join(errAgentStatePersistence, acceptErr)
}

// EnsureLegacyRuntimeSpecEnabled fails closed once instance.json exists. The
// marker is intentionally the final file written by BootstrapLegacyRuntime.
func (m *Manager) EnsureLegacyRuntimeSpecEnabled(instanceID string) error {
	legacyRuntimeBootstrapMu.Lock()
	defer legacyRuntimeBootstrapMu.Unlock()
	return m.ensureLegacyRuntimeSpecEnabledLocked(instanceID)
}

// ApplyLegacyRuntimeSpec serializes the legacy compatibility writer with the
// one-time model switch, preventing a check/apply race across instance.json.
func (m *Manager) ApplyLegacyRuntimeSpec(ctx context.Context, spec LegacyRuntimeSpec) error {
	legacyRuntimeBootstrapMu.Lock()
	defer legacyRuntimeBootstrapMu.Unlock()
	spec = NormalizeSpec(spec)
	if err := m.ensureLegacyRuntimeSpecEnabledLocked(spec.InstanceID); err != nil {
		return err
	}
	if err := validateRuntimeSpec(spec); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidLegacyRuntimeSpec, err)
	}
	return m.Apply(ctx, spec)
}

func (m *Manager) ensureLegacyRuntimeSpecEnabledLocked(instanceID string) error {
	instanceID = strings.TrimSpace(instanceID)
	if err := validateInstanceManifestName(instanceID); err != nil {
		return err
	}
	marker := filepath.Join(m.stateDir, instanceID, "instance.json")
	info, err := os.Lstat(marker)
	switch {
	case err == nil:
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: new-model marker is invalid", ErrLegacyRuntimeSpecDisabled)
		}
		return ErrLegacyRuntimeSpecDisabled
	case errors.Is(err, os.ErrNotExist):
		return nil
	default:
		return fmt.Errorf("inspect new-model marker: %w", err)
	}
}

// BootstrapLegacyRuntime validates and stages the complete split before it
// changes the live model. Deployment files are installed first and
// instance.json is installed last, so any pre-marker failure leaves the old
// runtime-spec writer enabled and safely retryable.
func (m *Manager) BootstrapLegacyRuntime(ctx context.Context, legacy LegacyRuntimeSpec) (LegacyBootstrapAcceptance, error) {
	legacyRuntimeBootstrapMu.Lock()
	defer legacyRuntimeBootstrapMu.Unlock()

	if err := ctx.Err(); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	if err := validateLegacyBootstrapInput(legacy); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("%w: %v", ErrInvalidLegacyRuntimeSpec, err)
	}
	legacy = NormalizeSpec(legacy)
	if err := validateRuntimeSpec(legacy); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("%w: %v", ErrInvalidLegacyRuntimeSpec, err)
	}
	config := NormalizeInstanceConfig(InstanceConfig{
		APIVersion:  ManifestAPIVersion,
		InstanceID:  legacy.InstanceID,
		InstallRoot: legacy.InstallRoot,
		Network:     legacy.Network,
		Ingress:     legacy.Ingress,
	})
	if err := ValidateInstanceConfig(config); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("%w: %v", ErrInvalidLegacyRuntimeSpec, err)
	}
	manifests, err := splitLegacyRuntimeSpec(config, legacy)
	if err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("%w: %v", ErrInvalidLegacyRuntimeSpec, err)
	}
	if err := m.manifestStore.ensureDirectory(m.stateDir); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("prepare runtime state root: %w", err)
	}
	instanceDir := filepath.Join(m.stateDir, config.InstanceID)
	if err := m.manifestStore.ensureDirectory(instanceDir); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("prepare instance state directory: %w", err)
	}
	paths := newLegacyBootstrapPaths(instanceDir)
	markerExists, err := regularBootstrapMarkerExists(paths.marker)
	if err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	if markerExists {
		cleanupErr := cleanupCompletedLegacyBootstrap(paths, m.manifestStore)
		if cleanupErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("%w: cleanup completed bootstrap: %v", ErrLegacyRuntimeSpecDisabled, cleanupErr)
		}
		return LegacyBootstrapAcceptance{}, ErrLegacyRuntimeSpecDisabled
	}
	if err := recoverLegacyBootstrapSwap(paths, m.manifestStore); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	if err := prepareLegacyBootstrapStage(paths, m.manifestStore); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	acceptances := make([]DeploymentAcceptance, 0, len(manifests))
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return LegacyBootstrapAcceptance{}, err
		}
		if err := m.manifestStore.validateDeploymentFilesystem(config, manifest); err != nil {
			if errors.Is(err, errUnsafeManifestFilesystemShape) {
				return LegacyBootstrapAcceptance{}, fmt.Errorf("%w: %v", ErrInvalidLegacyRuntimeSpec, err)
			}
			return LegacyBootstrapAcceptance{}, fmt.Errorf("observe deployment filesystem: %w", err)
		}
		data, marshalErr := json.MarshalIndent(manifest, "", "  ")
		if marshalErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("marshal staged deployment manifest: %w", marshalErr)
		}
		if writeErr := m.manifestStore.atomicWrite(filepath.Join(paths.stage, manifest.Metadata.Name+".json"), append(data, '\n')); writeErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("stage deployment manifest: %w", writeErr)
		}
		hash, hashErr := DeploymentManifestSpecHash(manifest)
		if hashErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("hash staged deployment manifest: %w", hashErr)
		}
		acceptances = append(acceptances, DeploymentAcceptance{Accepted: true, Generation: manifest.Metadata.Generation, SpecHash: hash})
	}
	markerData, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("marshal staged instance marker: %w", err)
	}
	if err := m.manifestStore.atomicWrite(paths.stagedMarker, append(markerData, '\n')); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("stage instance marker: %w", err)
	}
	if err := m.manifestStore.directorySync(instanceDir); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("sync complete bootstrap stage: %w", err)
	}

	maintenance := m.instanceMaintenanceLock(config.InstanceID)
	maintenance.Lock()
	defer maintenance.Unlock()
	if err := ctx.Err(); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	hadFinal, err := installLegacyBootstrapStage(paths, m.manifestStore)
	if err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	if err := m.manifestStore.fileRename(paths.stagedMarker, paths.marker); err != nil {
		restoreErr := restoreLegacyBootstrapSwap(paths, m.manifestStore, hadFinal)
		return LegacyBootstrapAcceptance{}, errors.Join(fmt.Errorf("switch runtime orchestration model: %w", err), restoreErr)
	}
	var completionErrors []error
	if err := m.manifestStore.directorySync(instanceDir); err != nil {
		completionErrors = append(completionErrors, fmt.Errorf("sync runtime orchestration marker: %w", err))
	}
	for _, manifest := range manifests {
		if err := m.enqueuePersistedDeployment(manifest); err != nil {
			completionErrors = append(completionErrors, fmt.Errorf("start deployment controller: %w", err))
		}
	}
	if err := cleanupCompletedLegacyBootstrap(paths, m.manifestStore); err != nil {
		completionErrors = append(completionErrors, err)
	}
	if err := errors.Join(completionErrors...); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	return LegacyBootstrapAcceptance{Accepted: true, InstanceID: config.InstanceID, Deployments: acceptances}, nil
}

func validateLegacyBootstrapInput(legacy LegacyRuntimeSpec) error {
	if strings.TrimSpace(legacy.InstanceID) != legacy.InstanceID {
		return errors.New("legacy instance identity is not canonical")
	}
	if err := validateInstanceManifestName(legacy.InstanceID); err != nil {
		return fmt.Errorf("validate legacy instance identity: %w", err)
	}
	serviceNames := make(map[string]bool, len(legacy.Services))
	for _, service := range legacy.Services {
		name := normalizeServiceManifestName(service.Name)
		if err := validateServiceManifestName(name); err != nil {
			return fmt.Errorf("validate legacy service identity: %w", err)
		}
		if serviceNames[name] {
			return errors.New("legacy runtime spec contains duplicate services")
		}
		serviceNames[name] = true
	}
	deploymentNames := make(map[string]bool, len(legacy.Deployments))
	if len(legacy.Deployments) == 0 {
		return errors.New("legacy runtime spec contains no deployments")
	}
	for _, deployment := range legacy.Deployments {
		name := normalizeServiceManifestName(deployment.ServiceName)
		if err := validateServiceManifestName(name); err != nil {
			return fmt.Errorf("validate legacy deployment identity: %w", err)
		}
		if deploymentNames[name] {
			return errors.New("legacy runtime spec contains duplicate deployments")
		}
		if !serviceNames[name] {
			return errors.New("legacy deployment service is missing")
		}
		if deployment.Replicas < 0 {
			return errors.New("legacy deployment replicas must not be negative")
		}
		deploymentNames[name] = true
	}
	return nil
}

func splitLegacyRuntimeSpec(config InstanceConfig, legacy LegacyRuntimeSpec) ([]DeploymentManifest, error) {
	services := make(map[string]ServiceSpec, len(legacy.Services))
	for _, service := range legacy.Services {
		services[service.Name] = service
	}
	manifests := make([]DeploymentManifest, 0, len(legacy.Deployments))
	for _, deployment := range legacy.Deployments {
		service, ok := services[deployment.ServiceName]
		if !ok {
			return nil, fmt.Errorf("legacy deployment service is missing")
		}
		manifest := NormalizeDeploymentManifest(DeploymentManifest{
			APIVersion: ManifestAPIVersion,
			Kind:       DeploymentManifestKind,
			Metadata: DeploymentMetadata{
				InstanceID: config.InstanceID,
				Name:       deployment.ServiceName,
				Generation: 1,
			},
			Spec:    deployment,
			Service: service,
		})
		if err := ValidateDeploymentManifest(config, manifest); err != nil {
			return nil, fmt.Errorf("validate split deployment manifest: %w", err)
		}
		manifests = append(manifests, manifest)
	}
	sort.Slice(manifests, func(i, j int) bool { return manifests[i].Metadata.Name < manifests[j].Metadata.Name })
	return manifests, nil
}

type legacyBootstrapPaths struct {
	instanceDir  string
	final        string
	stage        string
	backup       string
	marker       string
	stagedMarker string
}

func newLegacyBootstrapPaths(instanceDir string) legacyBootstrapPaths {
	return legacyBootstrapPaths{
		instanceDir:  instanceDir,
		final:        filepath.Join(instanceDir, "deployments"),
		stage:        filepath.Join(instanceDir, legacyBootstrapStageDirectory),
		backup:       filepath.Join(instanceDir, legacyBootstrapBackupDirectory),
		marker:       filepath.Join(instanceDir, "instance.json"),
		stagedMarker: filepath.Join(instanceDir, legacyBootstrapMarkerFile),
	}
}

func regularBootstrapMarkerExists(marker string) (bool, error) {
	info, err := os.Lstat(marker)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return false, errors.New("new-model marker is not a regular file")
		}
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("inspect new-model marker: %w", err)
	}
}

func prepareLegacyBootstrapStage(paths legacyBootstrapPaths, store ManifestStore) error {
	if err := removeAgentBootstrapDirectory(paths.stage, paths.instanceDir, store); err != nil {
		return err
	}
	if err := removeAgentBootstrapFile(paths.stagedMarker, paths.instanceDir, store); err != nil {
		return err
	}
	if err := store.ensureDirectory(paths.stage); err != nil {
		return fmt.Errorf("create deployment bootstrap stage: %w", err)
	}
	return nil
}

// recoverLegacyBootstrapSwap repairs only the fixed Agent-owned transaction
// paths. A quarantined legacy directory is never deleted before instance.json
// exists; it is restored first after any interrupted pre-marker switch.
func recoverLegacyBootstrapSwap(paths legacyBootstrapPaths, store ManifestStore) error {
	backupExists, err := bootstrapDirectoryExists(paths.backup)
	if err != nil {
		return err
	}
	if backupExists {
		finalExists, finalErr := bootstrapDirectoryExists(paths.final)
		if finalErr != nil {
			return finalErr
		}
		if finalExists {
			if err := removeAgentBootstrapDirectory(paths.stage, paths.instanceDir, store); err != nil {
				return err
			}
			if err := store.fileRename(paths.final, paths.stage); err != nil {
				return fmt.Errorf("quarantine interrupted new deployment directory: %w", err)
			}
		}
		if err := store.fileRename(paths.backup, paths.final); err != nil {
			return fmt.Errorf("restore quarantined legacy deployment directory: %w", err)
		}
		if err := store.directorySync(paths.instanceDir); err != nil {
			return fmt.Errorf("sync restored legacy deployment directory: %w", err)
		}
		if err := removeAgentBootstrapDirectory(paths.stage, paths.instanceDir, store); err != nil {
			return err
		}
	}
	if err := removeAgentBootstrapFile(paths.stagedMarker, paths.instanceDir, store); err != nil {
		return err
	}
	return nil
}

func installLegacyBootstrapStage(paths legacyBootstrapPaths, store ManifestStore) (bool, error) {
	if markerExists, err := regularBootstrapMarkerExists(paths.marker); err != nil {
		return false, err
	} else if markerExists {
		return false, ErrLegacyRuntimeSpecDisabled
	}
	if backupExists, err := bootstrapDirectoryExists(paths.backup); err != nil {
		return false, err
	} else if backupExists {
		return false, errors.New("legacy deployment quarantine was not recovered")
	}
	hadFinal, err := bootstrapDirectoryExists(paths.final)
	if err != nil {
		return false, err
	}
	if hadFinal {
		if err := store.fileRename(paths.final, paths.backup); err != nil {
			return false, fmt.Errorf("quarantine legacy deployment directory: %w", err)
		}
		if err := store.directorySync(paths.instanceDir); err != nil {
			restoreErr := restoreLegacyBootstrapSwap(paths, store, true)
			return false, errors.Join(fmt.Errorf("sync legacy deployment quarantine: %w", err), restoreErr)
		}
	}
	if err := store.fileRename(paths.stage, paths.final); err != nil {
		restoreErr := restoreLegacyBootstrapSwap(paths, store, hadFinal)
		return false, errors.Join(fmt.Errorf("install staged deployment directory: %w", err), restoreErr)
	}
	if err := store.directorySync(paths.instanceDir); err != nil {
		restoreErr := restoreLegacyBootstrapSwap(paths, store, hadFinal)
		return false, errors.Join(fmt.Errorf("sync installed deployment directory: %w", err), restoreErr)
	}
	return hadFinal, nil
}

func restoreLegacyBootstrapSwap(paths legacyBootstrapPaths, store ManifestStore, hadFinal bool) error {
	markerExists, err := regularBootstrapMarkerExists(paths.marker)
	if err != nil || markerExists {
		return err
	}
	finalExists, err := bootstrapDirectoryExists(paths.final)
	if err != nil {
		return err
	}
	if finalExists {
		if err := removeAgentBootstrapDirectory(paths.stage, paths.instanceDir, store); err != nil {
			return err
		}
		if err := store.fileRename(paths.final, paths.stage); err != nil {
			return fmt.Errorf("preserve failed staged deployment directory: %w", err)
		}
	}
	if hadFinal {
		backupExists, backupErr := bootstrapDirectoryExists(paths.backup)
		if backupErr != nil {
			return backupErr
		}
		if !backupExists {
			return errors.New("legacy deployment quarantine is missing")
		}
		if err := store.fileRename(paths.backup, paths.final); err != nil {
			return fmt.Errorf("restore legacy deployment directory: %w", err)
		}
	}
	if err := store.directorySync(paths.instanceDir); err != nil {
		return fmt.Errorf("sync restored bootstrap state: %w", err)
	}
	if err := removeAgentBootstrapDirectory(paths.stage, paths.instanceDir, store); err != nil {
		return err
	}
	if err := removeAgentBootstrapFile(paths.stagedMarker, paths.instanceDir, store); err != nil {
		return err
	}
	return nil
}

func cleanupCompletedLegacyBootstrap(paths legacyBootstrapPaths, store ManifestStore) error {
	markerExists, err := regularBootstrapMarkerExists(paths.marker)
	if err != nil {
		return err
	}
	if !markerExists {
		return errors.New("cannot clean bootstrap quarantine before new-model marker")
	}
	if err := removeAgentBootstrapDirectory(paths.backup, paths.instanceDir, store); err != nil {
		return err
	}
	if err := removeAgentBootstrapDirectory(paths.stage, paths.instanceDir, store); err != nil {
		return err
	}
	return removeAgentBootstrapFile(paths.stagedMarker, paths.instanceDir, store)
}

func bootstrapDirectoryExists(directory string) (bool, error) {
	info, err := os.Lstat(directory)
	switch {
	case err == nil:
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return false, errors.New("bootstrap deployment path is not a directory")
		}
		return true, nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("inspect bootstrap deployment directory: %w", err)
	}
}

func removeAgentBootstrapDirectory(target, instanceDir string, store ManifestStore) error {
	base := filepath.Base(target)
	if filepath.Dir(target) != instanceDir || (base != legacyBootstrapStageDirectory && base != legacyBootstrapBackupDirectory) {
		return errors.New("refuse to remove non-bootstrap directory")
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Agent bootstrap directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("Agent bootstrap directory is not a safe directory")
	}
	if err := os.RemoveAll(target); err != nil {
		return fmt.Errorf("remove Agent bootstrap directory: %w", err)
	}
	if err := store.directorySync(instanceDir); err != nil {
		return fmt.Errorf("sync removed Agent bootstrap directory: %w", err)
	}
	return nil
}

func removeAgentBootstrapFile(target, instanceDir string, store ManifestStore) error {
	if filepath.Dir(target) != instanceDir || filepath.Base(target) != legacyBootstrapMarkerFile {
		return errors.New("refuse to remove non-bootstrap file")
	}
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect Agent bootstrap file: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("Agent bootstrap file is not a safe regular file")
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("remove Agent bootstrap file: %w", err)
	}
	if err := store.directorySync(instanceDir); err != nil {
		return fmt.Errorf("sync removed Agent bootstrap file: %w", err)
	}
	return nil
}
