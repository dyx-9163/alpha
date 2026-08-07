package aifar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/installer/installerkit"
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
	AllowedServices []string                       `json:"-"`
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
	Restart                 bool
}

type runtimeConfigScriptData struct {
	InstallRoot                   string
	ConfigVersion                 int
	GlobalAppCPUs                 string
	GlobalAppMemoryLimit          string
	GlobalJVMInitialRAMPercentage string
	GlobalJVMMaxRAMPercentage     string
	NacosEphemeral                string
	Services                      []runtimeConfigScriptService
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
	return state
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
	return !strings.EqualFold(strings.TrimSpace(oldValues.AppMemoryLimit), strings.TrimSpace(newValues.AppMemoryLimit)) ||
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
	if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support runtime config; reinstall with k8s-like orchestration first"}); err != nil {
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

	var metadata map[string]any
	var previous RuntimeConfigState
	var next RuntimeConfigState
	var installRoot string
	var script string
	now := time.Now().UTC()

	if err := step(1, func() error {
		metadata = metadataFromInstance(current)
		if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support runtime config; reinstall with k8s-like orchestration first"}); err != nil {
			return err
		}
		previous = runtimeConfigFromMetadata(metadata)
		var normalizeErr error
		next, normalizeErr = normalizeRuntimeConfigPayload(req.Config, previous)
		if normalizeErr != nil {
			return normalizeErr
		}
		next.ConfigVersion = previous.ConfigVersion + 1
		next.UpdatedAt = now.Format(time.RFC3339)
		next.UpdatedBy = strings.TrimSpace(req.Actor)
		next.LastApplyStatus = runtimeConfigStatusPending
		next.LastApplyError = ""
		metadata["runtimeConfig"] = next
		return saveMetadata(s.store, current, metadata)
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(2, func() error {
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		if strings.TrimSpace(installRoot) == "" {
			return errors.New("AIFAR install root is missing")
		}
		data := runtimeConfigScriptDataFromState(installRoot, previous, next, servicesFromMetadata(metadata))
		var renderErr error
		script, renderErr = renderRuntimeConfigScript(data)
		return renderErr
	}); err != nil {
		_ = s.markRuntimeConfigApplyFailed(current.ID, next, err)
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(3, func() error {
		_, runErr := installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_RUNTIME_CONFIG'\n"+script+"\nAIFAR_RUNTIME_CONFIG", logForServer, "AIFAR runtime config apply failed")
		return runErr
	}); err != nil {
		_ = s.markRuntimeConfigApplyFailed(current.ID, next, err)
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(4, func() error {
		saved, err := s.store.GetAppInstance(current.ID)
		if err != nil {
			return err
		}
		metadata = metadataFromInstance(saved)
		state := runtimeConfigFromMetadata(metadata)
		state.AppliedVersion = next.ConfigVersion
		state.LastAppliedAt = time.Now().UTC().Format(time.RFC3339)
		state.LastApplyStatus = runtimeConfigStatusApplied
		state.LastApplyError = ""
		metadata["runtimeConfig"] = state
		delete(metadata, "orchestrationLock")
		return saveMetadata(s.store, saved, metadata)
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	logForServer.Info("AIFAR runtime config applied, version %d", next.ConfigVersion)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) markRuntimeConfigApplyFailed(instanceID string, state RuntimeConfigState, applyErr error) error {
	instance, err := s.store.GetAppInstance(instanceID)
	if err != nil {
		return err
	}
	metadata := metadataFromInstance(instance)
	state.LastApplyStatus = runtimeConfigStatusFailed
	state.LastApplyError = applyErr.Error()
	metadata["runtimeConfig"] = state
	delete(metadata, "orchestrationLock")
	return saveMetadata(s.store, instance, metadata)
}

func runtimeConfigScriptDataFromState(installRoot string, previous, next RuntimeConfigState, services []string) runtimeConfigScriptData {
	global := normalizeRuntimeConfigValues(next.Global, defaultRuntimeConfigValues())
	out := runtimeConfigScriptData{
		InstallRoot:                   installRoot,
		ConfigVersion:                 next.ConfigVersion,
		GlobalAppCPUs:                 global.AppCPUs,
		GlobalAppMemoryLimit:          global.AppMemoryLimit,
		GlobalJVMInitialRAMPercentage: formatRuntimePercent(global.JVMInitialRAMPercentage),
		GlobalJVMMaxRAMPercentage:     formatRuntimePercent(global.JVMMaxRAMPercentage),
		NacosEphemeral:                strconv.FormatBool(next.NacosEphemeral),
	}
	for _, service := range serviceListOrDefault(services) {
		values := effectiveRuntimeConfigForService(next, service)
		out.Services = append(out.Services, runtimeConfigScriptService{
			Name:                    service,
			Java:                    service != "web-vue3",
			AppCPUs:                 values.AppCPUs,
			AppMemoryLimit:          values.AppMemoryLimit,
			JVMInitialRAMPercentage: formatRuntimePercent(values.JVMInitialRAMPercentage),
			JVMMaxRAMPercentage:     formatRuntimePercent(values.JVMMaxRAMPercentage),
			Restart:                 service != "web-vue3" && runtimeConfigChangedForService(previous, next, service),
		})
	}
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
