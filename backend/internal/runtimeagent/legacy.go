package runtimeagent

import (
	"context"
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
	legacyRuntimeBootstrapMu     sync.Mutex
)

// LegacyBootstrapAcceptance records the complete marker-last conversion of a
// legacy instance-wide spec into the node's per-service execution cache.
type LegacyBootstrapAcceptance struct {
	Accepted    bool                   `json:"accepted"`
	InstanceID  string                 `json:"instanceId"`
	Deployments []DeploymentAcceptance `json:"deployments"`
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
	if err := m.ensureLegacyRuntimeSpecEnabledLocked(legacy.InstanceID); err != nil {
		return LegacyBootstrapAcceptance{}, err
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
	stageRoot, err := os.MkdirTemp(m.stateDir, ".legacy-bootstrap-")
	if err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("create bootstrap staging root: %w", err)
	}
	defer os.RemoveAll(stageRoot)
	stage := ManifestStore{StateDir: stageRoot, manifestPathLstat: m.manifestStore.manifestPathLstat}
	if err := stage.PutInstance(config); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("stage instance config: %w", err)
	}
	acceptances := make([]DeploymentAcceptance, 0, len(manifests))
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return LegacyBootstrapAcceptance{}, err
		}
		acceptance, putErr := stage.Put(manifest)
		if putErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("stage deployment manifest: %w", putErr)
		}
		acceptances = append(acceptances, acceptance)
	}

	instanceDir := filepath.Join(m.stateDir, config.InstanceID)
	deploymentsDir := filepath.Join(instanceDir, "deployments")
	if err := m.manifestStore.ensureDirectory(instanceDir); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("prepare instance state directory: %w", err)
	}
	if err := m.manifestStore.ensureDirectory(deploymentsDir); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("prepare deployment state directory: %w", err)
	}
	if err := clearBootstrapDeploymentFiles(deploymentsDir, m.manifestStore); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	for _, manifest := range manifests {
		if err := ctx.Err(); err != nil {
			return LegacyBootstrapAcceptance{}, err
		}
		data, readErr := os.ReadFile(filepath.Join(stageRoot, config.InstanceID, "deployments", manifest.Metadata.Name+".json"))
		if readErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("read staged deployment manifest: %w", readErr)
		}
		if writeErr := m.manifestStore.atomicWrite(filepath.Join(deploymentsDir, manifest.Metadata.Name+".json"), data); writeErr != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("install deployment manifest: %w", writeErr)
		}
	}
	if err := ctx.Err(); err != nil {
		return LegacyBootstrapAcceptance{}, err
	}
	markerData, err := os.ReadFile(filepath.Join(stageRoot, config.InstanceID, "instance.json"))
	if err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("read staged instance marker: %w", err)
	}
	if err := m.manifestStore.atomicWrite(filepath.Join(instanceDir, "instance.json"), markerData); err != nil {
		return LegacyBootstrapAcceptance{}, fmt.Errorf("switch runtime orchestration model: %w", err)
	}

	for _, manifest := range manifests {
		if err := m.enqueuePersistedDeployment(manifest); err != nil {
			return LegacyBootstrapAcceptance{}, fmt.Errorf("start deployment controller: %w", err)
		}
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

func clearBootstrapDeploymentFiles(directory string, store ManifestStore) error {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return fmt.Errorf("read deployment state directory: %w", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		if !entry.Type().IsRegular() {
			return errors.New("deployment state entry is not a regular file")
		}
		if err := os.Remove(filepath.Join(directory, entry.Name())); err != nil {
			return fmt.Errorf("clear incomplete deployment manifest: %w", err)
		}
	}
	if err := store.directorySync(directory); err != nil {
		return fmt.Errorf("sync cleared deployment state directory: %w", err)
	}
	return nil
}
