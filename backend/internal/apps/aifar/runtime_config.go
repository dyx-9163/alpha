package aifar

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

const (
	defaultJVMInitialRAMPercentage = 20.0
	defaultJVMMaxRAMPercentage     = 70.0
	runtimeConfigStatusApplied     = "applied"
	runtimeConfigStatusFailed      = "failed"
	runtimeConfigStatusPending     = "pending"
)

var (
	cpuLimitPattern    = regexp.MustCompile(`^[0-9]+(\.[0-9]+)?$`)
	memoryLimitPattern = regexp.MustCompile(`^[1-9][0-9]*(b|k|m|g|kb|mb|gb|kib|mib|gib)?$`)
)

type RuntimeConfigValues = registry.RuntimeConfigValues
type RuntimeConfigPayload = registry.RuntimeConfigPayload

type RuntimeConfigState struct {
	ConfigVersion   int                            `json:"configVersion"`
	UpdatedAt       string                         `json:"updatedAt,omitempty"`
	UpdatedBy       string                         `json:"updatedBy,omitempty"`
	Global          RuntimeConfigValues            `json:"global"`
	Services        map[string]RuntimeConfigValues `json:"services,omitempty"`
	NacosEphemeral  bool                           `json:"nacosEphemeral"`
	AppliedVersion  int                            `json:"appliedVersion,omitempty"`
	LastAppliedAt   string                         `json:"lastAppliedAt,omitempty"`
	LastApplyStatus string                         `json:"lastApplyStatus,omitempty"`
	LastApplyError  string                         `json:"lastApplyError,omitempty"`
	AppliedSnapshot *RuntimeConfigAppliedSnapshot  `json:"appliedSnapshot,omitempty"`
	AllowedServices []string                       `json:"-"`
}

type RuntimeConfigAppliedSnapshot struct {
	ConfigVersion   int                            `json:"configVersion"`
	Global          RuntimeConfigValues            `json:"global"`
	Services        map[string]RuntimeConfigValues `json:"services,omitempty"`
	NacosEphemeral  bool                           `json:"nacosEphemeral"`
	ServiceHashes   map[string]string              `json:"serviceHashes,omitempty"`
	ServiceVersions map[string]int                 `json:"serviceVersions,omitempty"`
	Immutable       bool                           `json:"immutable,omitempty"`
}

type RuntimeConfigRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	TaskID   string
	Reason   string
	Config   RuntimeConfigPayload
}

type runtimeConfigScriptService struct {
	Name                    string
	Java                    bool
	AppCPUs                 string
	AppMemoryLimit          string
	JVMInitialRAMPercentage string
	JVMMaxRAMPercentage     string
	NacosEphemeral          string
	ConfigHash              string
	ConfigDir               string
}

type runtimeConfigScriptData struct {
	InstallRoot   string
	ConfigVersion int
	Services      []runtimeConfigScriptService
}

type runtimeConfigTarget struct {
	ServiceName    string
	ConfigVersion  int
	ConfigHash     string
	ConfigDir      string
	Values         RuntimeConfigValues
	NacosEphemeral bool
	Java           bool
}

func runtimeConfigFromOptions(options InstallOptions, actor string, now time.Time) RuntimeConfigState {
	state := RuntimeConfigState{
		ConfigVersion: 1,
		UpdatedAt:     now.Format(time.RFC3339),
		UpdatedBy:     actor,
		Global: normalizeRuntimeConfigValues(RuntimeConfigValues{
			AppCPUs:                 options.AppCPUs,
			AppMemoryLimit:          options.AppMemoryLimit,
			JVMInitialRAMPercentage: options.JVMInitialRAMPercentage,
			JVMMaxRAMPercentage:     options.JVMMaxRAMPercentage,
		}, defaultRuntimeConfigValues()),
		NacosEphemeral:  true,
		Services:        map[string]RuntimeConfigValues{},
		AppliedVersion:  1,
		LastAppliedAt:   now.Format(time.RFC3339),
		LastApplyStatus: runtimeConfigStatusApplied,
		AllowedServices: append([]string(nil), options.SelectedServices...),
	}
	snapshot := runtimeConfigAppliedSnapshotFromState(state, state.AllowedServices, false)
	state.AppliedSnapshot = &snapshot
	return state
}

func defaultRuntimeConfigValues() RuntimeConfigValues {
	return RuntimeConfigValues{
		AppCPUs:                 defaultAppCPUs,
		AppMemoryLimit:          defaultMemoryLimit,
		JVMInitialRAMPercentage: defaultJVMInitialRAMPercentage,
		JVMMaxRAMPercentage:     defaultJVMMaxRAMPercentage,
	}
}

func runtimeConfigFromMetadata(metadata map[string]any) RuntimeConfigState {
	state := RuntimeConfigState{
		Global:          defaultRuntimeConfigValues(),
		Services:        map[string]RuntimeConfigValues{},
		NacosEphemeral:  true,
		AllowedServices: servicesFromMetadata(metadata),
	}
	if raw, ok := metadata["runtimeConfig"]; ok {
		hasNacosEphemeral := false
		if rawMap, ok := raw.(map[string]any); ok {
			_, hasNacosEphemeral = rawMap["nacosEphemeral"]
		}
		data, _ := json.Marshal(raw)
		_ = json.Unmarshal(data, &state)
		if !hasNacosEphemeral {
			state.NacosEphemeral = true
		}
	}
	state.Global = normalizeRuntimeConfigValues(state.Global, defaultRuntimeConfigValues())
	if state.Services == nil {
		state.Services = map[string]RuntimeConfigValues{}
	}
	if state.ConfigVersion < 1 {
		state.ConfigVersion = 1
	}
	if state.AppliedSnapshot != nil {
		normalizeRuntimeConfigAppliedSnapshot(state.AppliedSnapshot, state.AllowedServices)
	} else if state.LastApplyStatus == runtimeConfigStatusApplied && state.AppliedVersion == state.ConfigVersion {
		snapshot := runtimeConfigAppliedSnapshotFromState(state, state.AllowedServices, false)
		state.AppliedSnapshot = &snapshot
	}
	return state
}

func normalizeRuntimeConfigAppliedSnapshot(snapshot *RuntimeConfigAppliedSnapshot, services []string) {
	if snapshot == nil {
		return
	}
	snapshot.Global = normalizeRuntimeConfigValues(snapshot.Global, defaultRuntimeConfigValues())
	if snapshot.Services == nil {
		snapshot.Services = map[string]RuntimeConfigValues{}
	}
	for serviceName, values := range snapshot.Services {
		snapshot.Services[serviceName] = normalizeRuntimeConfigValues(values, snapshot.Global)
	}
	if snapshot.ConfigVersion < 1 {
		snapshot.ConfigVersion = 1
	}
	if snapshot.ServiceHashes == nil {
		snapshot.ServiceHashes = map[string]string{}
	}
	if snapshot.ServiceVersions == nil {
		snapshot.ServiceVersions = map[string]int{}
	}
	for _, serviceName := range serviceListOrDefault(services) {
		hash := strings.TrimSpace(snapshot.ServiceHashes[serviceName])
		if !deploymentSpecHashPattern.MatchString(hash) {
			snapshot.ServiceHashes[serviceName] = runtimeConfigServiceHashValues(
				effectiveRuntimeConfigForAppliedSnapshot(*snapshot, serviceName), snapshot.NacosEphemeral,
			)
		}
		if snapshot.Immutable && snapshot.ServiceVersions[serviceName] < 1 {
			snapshot.ServiceVersions[serviceName] = snapshot.ConfigVersion
		}
	}
}

func runtimeConfigAppliedSnapshotFromState(state RuntimeConfigState, services []string, immutable bool) RuntimeConfigAppliedSnapshot {
	snapshot := RuntimeConfigAppliedSnapshot{
		ConfigVersion: state.ConfigVersion,
		Global:        normalizeRuntimeConfigValues(state.Global, defaultRuntimeConfigValues()),
		Services:      map[string]RuntimeConfigValues{}, NacosEphemeral: state.NacosEphemeral,
		ServiceHashes: map[string]string{}, ServiceVersions: map[string]int{}, Immutable: immutable,
	}
	for serviceName, values := range state.Services {
		snapshot.Services[serviceName] = normalizeRuntimeConfigValues(values, snapshot.Global)
	}
	for _, serviceName := range serviceListOrDefault(services) {
		snapshot.ServiceHashes[serviceName] = runtimeConfigServiceHashValues(
			effectiveRuntimeConfigForAppliedSnapshot(snapshot, serviceName), snapshot.NacosEphemeral,
		)
		if immutable {
			snapshot.ServiceVersions[serviceName] = state.ConfigVersion
		}
	}
	return snapshot
}

func effectiveRuntimeConfigForAppliedSnapshot(snapshot RuntimeConfigAppliedSnapshot, service string) RuntimeConfigValues {
	global := normalizeRuntimeConfigValues(snapshot.Global, defaultRuntimeConfigValues())
	if override, ok := snapshot.Services[service]; ok {
		return normalizeRuntimeConfigValues(override, global)
	}
	return global
}

func runtimeConfigServiceHash(state RuntimeConfigState, service string) string {
	return runtimeConfigServiceHashValues(effectiveRuntimeConfigForService(state, service), state.NacosEphemeral)
}

func runtimeConfigServiceHashValues(values RuntimeConfigValues, nacosEphemeral bool) string {
	payload := struct {
		Values         RuntimeConfigValues `json:"values"`
		NacosEphemeral bool                `json:"nacosEphemeral"`
	}{Values: normalizeRuntimeConfigValues(values, defaultRuntimeConfigValues()), NacosEphemeral: nacosEphemeral}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return fmt.Sprintf("%x", sum[:])
}

func runtimeConfigVersionDir(installRoot, service string, version int, configHash string) string {
	return path.Join(installRoot, "runtime", "config", "versions", cleanAIFARServiceName(service), fmt.Sprintf("v%d-%s", version, configHash))
}

func runtimeConfigIntentEqual(a, b RuntimeConfigState, services []string) bool {
	if a.NacosEphemeral != b.NacosEphemeral || !runtimeConfigValuesEqual(a.Global, b.Global) {
		return false
	}
	for _, serviceName := range serviceListOrDefault(services) {
		if !runtimeConfigValuesEqual(effectiveRuntimeConfigForService(a, serviceName), effectiveRuntimeConfigForService(b, serviceName)) {
			return false
		}
	}
	return true
}

func runtimeConfigTargetFromState(installRoot string, state RuntimeConfigState, serviceName string) runtimeConfigTarget {
	return runtimeConfigTargetFromStateVersion(installRoot, state, serviceName, state.ConfigVersion)
}

func runtimeConfigTargetFromStateVersion(installRoot string, state RuntimeConfigState, serviceName string, version int) runtimeConfigTarget {
	serviceName = cleanAIFARServiceName(serviceName)
	configHash := runtimeConfigServiceHash(state, serviceName)
	return runtimeConfigTarget{
		ServiceName: serviceName, ConfigVersion: version, ConfigHash: configHash,
		ConfigDir: runtimeConfigVersionDir(installRoot, serviceName, version, configHash),
		Values:    effectiveRuntimeConfigForService(state, serviceName), NacosEphemeral: state.NacosEphemeral,
		Java: serviceName != "web-vue3",
	}
}

func runtimeConfigAppliedServiceVersion(snapshot RuntimeConfigAppliedSnapshot, serviceName string) (int, bool) {
	if version := snapshot.ServiceVersions[serviceName]; version > 0 {
		return version, true
	}
	if snapshot.Immutable && snapshot.ConfigVersion > 0 {
		return snapshot.ConfigVersion, true
	}
	return 0, false
}

func runtimeConfigTargetsForApply(installRoot string, previous, next RuntimeConfigState, services []string) map[string]runtimeConfigTarget {
	targets := map[string]runtimeConfigTarget{}
	for _, serviceName := range serviceListOrDefault(services) {
		nextHash := runtimeConfigServiceHash(next, serviceName)
		if previous.AppliedSnapshot == nil {
			if runtimeConfigChangedForService(previous, next, serviceName) || previous.NacosEphemeral != next.NacosEphemeral {
				targets[serviceName] = runtimeConfigTargetFromState(installRoot, next, serviceName)
			}
			continue
		} else {
			snapshot := *previous.AppliedSnapshot
			if snapshot.ServiceHashes[serviceName] == nextHash {
				if version, immutable := runtimeConfigAppliedServiceVersion(snapshot, serviceName); immutable {
					targets[serviceName] = runtimeConfigTargetFromStateVersion(installRoot, next, serviceName, version)
				}
				continue
			}
		}
		targets[serviceName] = runtimeConfigTargetFromState(installRoot, next, serviceName)
	}
	return targets
}

func runtimeConfigAppliedSnapshotAfterAcceptance(previous, next RuntimeConfigState, services []string, targets map[string]runtimeConfigTarget) RuntimeConfigAppliedSnapshot {
	snapshot := runtimeConfigAppliedSnapshotFromState(next, services, false)
	for _, serviceName := range serviceListOrDefault(services) {
		if target, ok := targets[serviceName]; ok {
			snapshot.ServiceVersions[serviceName] = target.ConfigVersion
			continue
		}
		if previous.AppliedSnapshot != nil {
			if version, immutable := runtimeConfigAppliedServiceVersion(*previous.AppliedSnapshot, serviceName); immutable {
				snapshot.ServiceVersions[serviceName] = version
			}
		}
	}
	snapshot.Immutable = len(snapshot.ServiceVersions) == len(serviceListOrDefault(services))
	return snapshot
}

func applyRuntimeConfigTarget(manifest *runtimeagent.DeploymentManifest, target runtimeConfigTarget) error {
	if manifest == nil || cleanAIFARServiceName(manifest.Spec.ServiceName) != target.ServiceName {
		return errors.New("AIFAR runtime config target does not match deployment manifest")
	}
	manifest.Spec.Resources.CPUs = target.Values.AppCPUs
	manifest.Spec.Resources.Memory = target.Values.AppMemoryLimit
	if manifest.Spec.Environment == nil {
		manifest.Spec.Environment = map[string]string{}
	}
	manifest.Spec.Environment["AIFAR_RUNTIME_CONFIG_VERSION"] = strconv.Itoa(target.ConfigVersion)
	manifest.Spec.Environment["AIFAR_RUNTIME_CONFIG_HASH"] = target.ConfigHash
	manifest.Spec.Environment["AIFAR_NACOS_EPHEMERAL"] = strconv.FormatBool(target.NacosEphemeral)
	if !target.Java {
		return nil
	}
	found := false
	for idx := range manifest.Spec.Volumes {
		if manifest.Spec.Volumes[idx].Target == "/opt/aifar/runtime/env" {
			manifest.Spec.Volumes[idx].Source = target.ConfigDir
			manifest.Spec.Volumes[idx].ReadOnly = true
			found = true
		}
	}
	if !found {
		return errors.New("AIFAR Java deployment manifest is missing its runtime config volume")
	}
	return nil
}

func runtimeConfigDeploymentProvesTarget(deployment store.AIFARDeployment, target runtimeConfigTarget) bool {
	accepted := strings.EqualFold(deployment.Status, "Accepted") ||
		deployment.ObservedGeneration >= deployment.Generation && runtimeConfigObservedDeploymentStatus(deployment.Status)
	if !accepted || deployment.Generation <= 0 || strings.TrimSpace(deployment.SpecJSON) == "" {
		return false
	}
	var manifest runtimeagent.DeploymentManifest
	if err := json.Unmarshal([]byte(deployment.SpecJSON), &manifest); err != nil {
		return false
	}
	manifest = runtimeagent.NormalizeDeploymentManifest(manifest)
	if manifest.Metadata.InstanceID != deployment.InstanceID || manifest.Metadata.Generation != deployment.Generation ||
		cleanAIFARServiceName(manifest.Metadata.Name) != target.ServiceName || cleanAIFARServiceName(manifest.Spec.ServiceName) != target.ServiceName ||
		manifest.Spec.Resources.CPUs != target.Values.AppCPUs || !strings.EqualFold(manifest.Spec.Resources.Memory, target.Values.AppMemoryLimit) ||
		manifest.Spec.Environment["AIFAR_RUNTIME_CONFIG_VERSION"] != strconv.Itoa(target.ConfigVersion) ||
		manifest.Spec.Environment["AIFAR_RUNTIME_CONFIG_HASH"] != target.ConfigHash ||
		manifest.Spec.Environment["AIFAR_NACOS_EPHEMERAL"] != strconv.FormatBool(target.NacosEphemeral) {
		return false
	}
	if target.Java {
		found := false
		for _, volume := range manifest.Spec.Volumes {
			if volume.Target == "/opt/aifar/runtime/env" {
				if volume.Source != target.ConfigDir || !volume.ReadOnly {
					return false
				}
				found = true
			}
		}
		if !found {
			return false
		}
	}
	specHash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
	return err == nil && deploymentSpecHashPattern.MatchString(specHash)
}

func runtimeConfigObservedDeploymentStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "progressing", "available", "degraded", "offline":
		return true
	default:
		return false
	}
}

func verifyRuntimeConfigTargets(control aifarDeploymentControlStore, instanceID string, targets map[string]runtimeConfigTarget) error {
	deployments, err := control.ListAIFARDeployments(instanceID)
	if err != nil {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_READ_FAILED", err)
	}
	proved := make(map[string]bool, len(targets))
	for _, deployment := range deployments {
		target, ok := targets[cleanAIFARServiceName(deployment.ServiceName)]
		if ok && runtimeConfigDeploymentProvesTarget(deployment, target) {
			proved[target.ServiceName] = true
		}
	}
	for serviceName := range targets {
		if !proved[serviceName] {
			return repairRequired("AIFAR_RUNTIME_CONFIG_ACCEPTANCE_INCOMPLETE", store.ErrAIFARDeploymentGenerationConflict)
		}
	}
	return nil
}

func normalizeRuntimeConfigValues(values, fallback RuntimeConfigValues) RuntimeConfigValues {
	values.AppCPUs = strings.TrimSpace(values.AppCPUs)
	values.AppMemoryLimit = strings.TrimSpace(values.AppMemoryLimit)
	if values.AppCPUs == "" {
		values.AppCPUs = fallback.AppCPUs
	}
	if values.AppMemoryLimit == "" {
		values.AppMemoryLimit = fallback.AppMemoryLimit
	}
	if values.JVMInitialRAMPercentage <= 0 {
		values.JVMInitialRAMPercentage = fallback.JVMInitialRAMPercentage
	}
	if values.JVMMaxRAMPercentage <= 0 {
		values.JVMMaxRAMPercentage = fallback.JVMMaxRAMPercentage
	}
	return values
}

func normalizeRuntimeConfigPayload(payload RuntimeConfigPayload, base RuntimeConfigState) (RuntimeConfigState, error) {
	global := normalizeRuntimeConfigValues(payload.Global, base.Global)
	if err := validateRuntimeConfigValues(global, true); err != nil {
		return RuntimeConfigState{}, err
	}
	services := map[string]RuntimeConfigValues{}
	allowed := map[string]bool{}
	for _, service := range base.AllowedServices {
		allowed[service] = true
	}
	if len(allowed) == 0 {
		for _, service := range serviceOrder {
			allowed[service] = true
		}
	}
	for name, values := range payload.Services {
		service := cleanAIFARServiceName(name)
		if !allowed[service] {
			return RuntimeConfigState{}, fmt.Errorf("unsupported AIFAR service: %s", name)
		}
		values = normalizeRuntimeConfigValues(values, global)
		if err := validateRuntimeConfigValues(values, true); err != nil {
			return RuntimeConfigState{}, fmt.Errorf("%s: %w", service, err)
		}
		if !runtimeConfigValuesEqual(values, global) {
			services[service] = values
		}
	}
	next := base
	next.Global = global
	next.Services = services
	if payload.NacosEphemeral != nil {
		next.NacosEphemeral = *payload.NacosEphemeral
	}
	return next, nil
}

func validateRuntimeConfigValues(values RuntimeConfigValues, requireAll bool) error {
	if requireAll || strings.TrimSpace(values.AppCPUs) != "" {
		if !cpuLimitPattern.MatchString(strings.TrimSpace(values.AppCPUs)) {
			return fmt.Errorf("CPU limit must be a positive decimal")
		}
		cpus, err := strconv.ParseFloat(strings.TrimSpace(values.AppCPUs), 64)
		if err != nil || cpus <= 0 {
			return fmt.Errorf("CPU limit must be greater than 0")
		}
	}
	if requireAll || strings.TrimSpace(values.AppMemoryLimit) != "" {
		if !memoryLimitPattern.MatchString(strings.ToLower(strings.TrimSpace(values.AppMemoryLimit))) {
			return fmt.Errorf("memory limit must be a Docker-compatible size such as 2GB or 2048m")
		}
	}
	if requireAll || values.JVMInitialRAMPercentage > 0 || values.JVMMaxRAMPercentage > 0 {
		if values.JVMInitialRAMPercentage <= 0 || values.JVMMaxRAMPercentage <= 0 {
			return fmt.Errorf("JVM RAM percentages must be greater than 0")
		}
		if values.JVMInitialRAMPercentage > values.JVMMaxRAMPercentage {
			return fmt.Errorf("JVM initial RAM percentage must be less than or equal to max RAM percentage")
		}
		if values.JVMMaxRAMPercentage > 90 {
			return fmt.Errorf("JVM max RAM percentage must be less than or equal to 90")
		}
	}
	return nil
}

func runtimeConfigValuesEqual(a, b RuntimeConfigValues) bool {
	return strings.TrimSpace(a.AppCPUs) == strings.TrimSpace(b.AppCPUs) &&
		strings.EqualFold(strings.TrimSpace(a.AppMemoryLimit), strings.TrimSpace(b.AppMemoryLimit)) &&
		a.JVMInitialRAMPercentage == b.JVMInitialRAMPercentage &&
		a.JVMMaxRAMPercentage == b.JVMMaxRAMPercentage
}

func effectiveRuntimeConfigForService(state RuntimeConfigState, service string) RuntimeConfigValues {
	global := normalizeRuntimeConfigValues(state.Global, defaultRuntimeConfigValues())
	if override, ok := state.Services[service]; ok {
		return normalizeRuntimeConfigValues(override, global)
	}
	return global
}

func runtimeConfigChangedForService(oldState, newState RuntimeConfigState, service string) bool {
	oldValues := effectiveRuntimeConfigForService(oldState, service)
	newValues := effectiveRuntimeConfigForService(newState, service)
	return strings.TrimSpace(oldValues.AppCPUs) != strings.TrimSpace(newValues.AppCPUs) ||
		!strings.EqualFold(strings.TrimSpace(oldValues.AppMemoryLimit), strings.TrimSpace(newValues.AppMemoryLimit)) ||
		oldValues.JVMInitialRAMPercentage != newValues.JVMInitialRAMPercentage ||
		oldValues.JVMMaxRAMPercentage != newValues.JVMMaxRAMPercentage
}

func (s Service) ValidateRuntimeConfig(ctx context.Context, req RuntimeConfigRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if req.Instance.App != AppName {
		return errors.New("only AIFAR service instances support runtime config")
	}
	metadata := metadataFromInstance(req.Instance)
	if err := ensureServiceControllerMetadata(metadata); err != nil {
		return err
	}
	_, err := normalizeRuntimeConfigPayload(req.Config, runtimeConfigFromMetadata(metadata))
	return err
}

func (s Service) ApplyRuntimeConfig(ctx context.Context, req RuntimeConfigRequest, log Logger, targetLog targetLogger) error {
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, runtimeConfigSteps(), "AIFAR runtime config step %d/%d started: %s", "AIFAR runtime config step %d/%d completed: %s", "AIFAR runtime config step %d/%d failed: %s: %v")

	current, lock, err := s.acquireOrchestrationLock(req.Instance.ID, "runtime-config", "", req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(lock)
	lockedCtx, stopHeartbeat := s.startAIFAROrchestrationLockHeartbeat(ctx, lock)
	defer stopHeartbeat()
	ctx = lockedCtx

	var metadata map[string]any
	var previous RuntimeConfigState
	var next RuntimeConfigState
	var installRoot string
	var script string
	var configTargets map[string]runtimeConfigTarget
	now := time.Now().UTC()

	if err := step(1, func() error {
		saved, saveErr := s.updateAppInstanceMetadataWithLock(ctx, lock, current.ID, "AIFAR_RUNTIME_CONFIG_METADATA_REPAIR_REQUIRED", func(freshMetadata map[string]any) error {
			if err := ensureServiceControllerMetadata(freshMetadata); err != nil {
				return err
			}
			previous = runtimeConfigFromMetadata(freshMetadata)
			var normalizeErr error
			next, normalizeErr = normalizeRuntimeConfigPayload(req.Config, previous)
			if normalizeErr != nil {
				return normalizeErr
			}
			services := servicesFromMetadata(freshMetadata)
			if runtimeConfigIntentEqual(previous, next, services) {
				next.ConfigVersion = previous.ConfigVersion
			} else {
				next.ConfigVersion = previous.ConfigVersion + 1
			}
			next.UpdatedAt = now.Format(time.RFC3339)
			next.UpdatedBy = strings.TrimSpace(req.Actor)
			next.LastApplyStatus = runtimeConfigStatusPending
			next.LastApplyError = ""
			freshMetadata["runtimeConfig"] = next
			metadata = freshMetadata
			return nil
		})
		if saveErr == nil {
			current = saved
		}
		return saveErr
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(2, func() error {
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		if strings.TrimSpace(installRoot) == "" {
			return errors.New("AIFAR install root is missing")
		}
		services := servicesFromMetadata(metadata)
		data := runtimeConfigScriptDataFromState(installRoot, previous, next, services)
		configTargets = runtimeConfigTargetsForApply(installRoot, previous, next, services)
		var renderErr error
		script, renderErr = renderRuntimeConfigScript(data)
		return renderErr
	}); err != nil {
		if ctx.Err() == nil {
			_ = s.markRuntimeConfigApplyFailed(ctx, lock, current.ID, next, err)
		}
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(3, func() error {
		_, runErr := installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_RUNTIME_CONFIG'\n"+script+"\nAIFAR_RUNTIME_CONFIG", logForServer, "AIFAR runtime config apply failed")
		if runErr != nil {
			return runErr
		}
		control, ok := s.store.(aifarDeploymentControlStore)
		if !ok {
			return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
		}
		deployments, listErr := control.ListAIFARDeployments(current.ID)
		if listErr != nil {
			return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_READ_FAILED", listErr)
		}
		byService := make(map[string]store.AIFARDeployment, len(deployments))
		for _, deployment := range deployments {
			byService[cleanAIFARServiceName(deployment.ServiceName)] = deployment
		}
		plans := make([]deploymentMutationPlan, 0, len(configTargets))
		for serviceName, target := range configTargets {
			if runtimeConfigDeploymentProvesTarget(byService[serviceName], target) {
				continue
			}
			target := target
			plans = append(plans, deploymentMutationPlan{
				ServiceName:     serviceName,
				Operation:       "runtime-config",
				LockAlreadyHeld: true,
				Mutate: func(manifest *runtimeagent.DeploymentManifest) error {
					if err := applyRuntimeConfigTarget(manifest, target); err != nil {
						return err
					}
					manifest.Spec.RestartGeneration++
					return nil
				},
			})
		}
		_, mutationErr := s.mutateDeploymentsFanOut(ctx, current, req.Server, req.Actor, fallbackTaskID(req.TaskID, log), req.Language, defaultRuntimeMutationConcurrency, plans, log, targetLog)
		if mutationErr != nil {
			return mutationErr
		}
		return verifyRuntimeConfigTargets(control, current.ID, configTargets)
	}); err != nil {
		if ctx.Err() == nil {
			_ = s.markRuntimeConfigApplyFailed(ctx, lock, current.ID, next, err)
		}
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(4, func() error {
		if err := ctx.Err(); err != nil {
			return repairRequired("AIFAR_RUNTIME_CONFIG_METADATA_REPAIR_REQUIRED", err)
		}
		control, ok := s.store.(aifarDeploymentControlStore)
		if !ok {
			return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
		}
		if err := verifyRuntimeConfigTargets(control, current.ID, configTargets); err != nil {
			return err
		}
		_, saveErr := s.updateAppInstanceMetadataWithLock(ctx, lock, current.ID, "AIFAR_RUNTIME_CONFIG_METADATA_REPAIR_REQUIRED", func(freshMetadata map[string]any) error {
			state := runtimeConfigFromMetadata(freshMetadata)
			services := servicesFromMetadata(freshMetadata)
			if state.ConfigVersion != next.ConfigVersion || !runtimeConfigIntentEqual(state, next, services) {
				return repairRequired("AIFAR_RUNTIME_CONFIG_METADATA_REPAIR_REQUIRED", store.ErrAppInstanceConflict)
			}
			state.AppliedVersion = next.ConfigVersion
			state.LastAppliedAt = time.Now().UTC().Format(time.RFC3339)
			state.LastApplyStatus = runtimeConfigStatusApplied
			state.LastApplyError = ""
			snapshot := runtimeConfigAppliedSnapshotAfterAcceptance(previous, state, services, configTargets)
			state.AppliedSnapshot = &snapshot
			freshMetadata["runtimeConfig"] = state
			delete(freshMetadata, "orchestrationLock")
			return nil
		})
		return saveErr
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	logForServer.Info("AIFAR runtime config applied, version %d", next.ConfigVersion)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) markRuntimeConfigApplyFailed(ctx context.Context, lock store.AIFAROrchestrationLock, instanceID string, state RuntimeConfigState, applyErr error) error {
	_, err := s.updateAppInstanceMetadataWithLock(ctx, lock, instanceID, "AIFAR_RUNTIME_CONFIG_METADATA_REPAIR_REQUIRED", func(metadata map[string]any) error {
		state.LastApplyStatus = runtimeConfigStatusFailed
		state.LastApplyError = applyErr.Error()
		metadata["runtimeConfig"] = state
		delete(metadata, "orchestrationLock")
		return nil
	})
	return err
}

func runtimeConfigScriptDataFromState(installRoot string, previous, next RuntimeConfigState, services []string) runtimeConfigScriptData {
	out := runtimeConfigScriptData{
		InstallRoot: installRoot, ConfigVersion: next.ConfigVersion,
	}
	for _, service := range serviceListOrDefault(services) {
		target := runtimeConfigTargetFromState(installRoot, next, service)
		out.Services = append(out.Services, runtimeConfigScriptService{
			Name: target.ServiceName, Java: target.Java,
			AppCPUs: target.Values.AppCPUs, AppMemoryLimit: target.Values.AppMemoryLimit,
			JVMInitialRAMPercentage: formatRuntimePercent(target.Values.JVMInitialRAMPercentage),
			JVMMaxRAMPercentage:     formatRuntimePercent(target.Values.JVMMaxRAMPercentage),
			NacosEphemeral:          strconv.FormatBool(target.NacosEphemeral),
			ConfigHash:              target.ConfigHash, ConfigDir: target.ConfigDir,
		})
	}
	_ = previous
	return out
}

func formatRuntimePercent(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func runtimeConfigSteps() []installStepDef {
	return []installStepDef{
		{Name: "save-desired-config", Title: "save desired runtime config"},
		{Name: "render-config", Title: "render runtime config script"},
		{Name: "apply-runtime-config", Title: "apply Docker resources and JVM options"},
		{Name: "record-applied-config", Title: "record runtime config status"},
	}
}
