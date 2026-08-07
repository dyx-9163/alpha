package aifar

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

const (
	autoscaleTaskType          = "aifar.scale.out"
	autoscaleActor             = "system"
	autoscaleDefaultInterval   = time.Minute
	autoscaleDefaultThreshold  = 80
	autoscaleDefaultSustain    = 5 * time.Minute
	autoscaleDefaultCooldown   = 10 * time.Minute
	autoscaleDefaultMaxReplica = 3
	orchestrationLockTTL       = time.Hour
)

type AutoscalePolicy struct {
	Enabled         bool
	MemoryThreshold float64
	Sustain         time.Duration
	Cooldown        time.Duration
	MaxReplicas     int
	ScaleIn         bool
}

type autoscaleMetric struct {
	Service          string
	Container        string
	ReleaseID        string
	ReplicaID        int
	Port             int
	Running          bool
	Health           string
	MemoryPercent    float64
	MemoryLimitBytes int64
}

type autoscaleStatus struct {
	Endpoints               []autoscaleMetric
	HostMemoryAvailableByte int64
	Deployments             map[string]autoscaleDeploymentStatus
}

type autoscaleDeploymentStatus struct {
	ServiceName     string `json:"serviceName"`
	DesiredReplicas int    `json:"desiredReplicas"`
	CurrentReplicas int    `json:"currentReplicas"`
	ReadyReplicas   int    `json:"readyReplicas"`
	Status          string `json:"status"`
}

type autoscaleSignal struct {
	Since        string `json:"since,omitempty"`
	LastScaledAt string `json:"lastScaledAt,omitempty"`
}

type Autoscaler struct {
	store    *store.Store
	tasks    *worker.Manager
	remote   Remote
	interval time.Duration
}

func NewAutoscaler(s *store.Store, tasks *worker.Manager, remote Remote) *Autoscaler {
	return &Autoscaler{store: s, tasks: tasks, remote: remote, interval: autoscaleDefaultInterval}
}

func (a *Autoscaler) Start(ctx context.Context) {
	if a == nil || a.store == nil || a.tasks == nil || a.remote == nil {
		return
	}
	ticker := time.NewTicker(a.interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				a.tick(ctx, time.Now().UTC())
			}
		}
	}()
}

func (a *Autoscaler) tick(ctx context.Context, now time.Time) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return
	}
	for _, instance := range instances {
		if ctx.Err() != nil {
			return
		}
		if instance.App != AppName || strings.TrimSpace(instance.ServerID) == "" {
			continue
		}
		if !strings.EqualFold(instance.Status, "installed") && !strings.EqualFold(instance.Status, "running") {
			continue
		}
		metadata := metadataFromInstance(instance)
		policy := autoscalePolicyFromMetadata(metadata)
		if !policy.Enabled {
			continue
		}
		if orchestrationLocked(metadata, now) || anyServiceOrchestrationLocked(metadata, now) || a.orchestrationLocked(instance.ID, "", now) {
			continue
		}
		server, err := a.store.GetServer(instance.ServerID, true)
		if err != nil {
			continue
		}
		status, err := collectAutoscaleStatus(ctx, a.remote, server, stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(server.DeployDir)))
		if err != nil {
			a.recordScaleEvent(instance, "collect-failed", "", err.Error(), now)
			continue
		}
		next, decision := evaluateAutoscale(instance, metadata, status, policy, now)
		if decision.Service == "" {
			_ = saveMetadata(a.store, instance, next)
			continue
		}
		if serviceOrchestrationLocked(metadata, decision.Service, now) || a.orchestrationLocked(instance.ID, decision.Service, now) {
			continue
		}
		task, err := a.tasks.StartWithLanguage(autoscaleTaskType, instance.ID+":"+decision.Service, autoscaleActor, "", func(taskCtx context.Context, log worker.Logger) error {
			current, err := a.store.GetAppInstance(instance.ID)
			if err != nil {
				return err
			}
			server, err := a.store.GetServer(current.ServerID, true)
			if err != nil {
				return err
			}
			return NewService(a.store, a.remote).ScaleOut(taskCtx, ScaleOutRequest{
				Instance:    current,
				Server:      server,
				Actor:       autoscaleActor,
				ServiceName: decision.Service,
				Reason:      decision.Reason,
			}, log, func(target string) Logger { return log.Target(target) })
		})
		if err != nil {
			next = recordAutoscaleEvent(next, "task-failed", decision.Service, err.Error(), now)
			_ = saveMetadata(a.store, instance, next)
			continue
		}
		signals := autoscaleSignalsFromMetadata(next)
		signal := signals[decision.Service]
		signal.LastScaledAt = now.Format(time.RFC3339)
		signals[decision.Service] = signal
		next["autoscaleSignals"] = signals
		next = recordAutoscaleEvent(next, "task-started", decision.Service, decision.Reason, now)
		_ = saveMetadata(a.store, instance, next)
		_ = a.store.AddAudit(autoscaleActor, autoscaleTaskType, instance.ID+":"+decision.Service, "running", task.ID)
	}
}

type autoscaleDecision struct {
	Service string
	Reason  string
}

func evaluateAutoscale(instance store.AppInstance, metadata map[string]any, status autoscaleStatus, policy AutoscalePolicy, now time.Time) (map[string]any, autoscaleDecision) {
	next := copyMetadata(metadata)
	signals := autoscaleSignalsFromMetadata(metadata)
	activeEndpoints := activeEndpointsFromMetrics(status.Endpoints)
	desired := desiredReplicasFromMetadata(metadata)
	for service, endpoints := range activeEndpoints {
		if count := endpointCount(endpoints); count > 0 {
			desired[service] = count
		}
	}
	next["desiredReplicas"] = desired
	next["activeEndpoints"] = activeEndpoints
	next["activeServices"] = activeServicesFromEndpoints(desired, activeEndpoints)
	next["autoscalePolicy"] = policy.metadata()
	next["autoscaleMetrics"] = metricsMetadata(status.Endpoints, now)

	byService := metricsByService(status.Endpoints)
	for _, service := range serviceOrder {
		metrics := byService[service]
		if len(metrics) == 0 {
			delete(signals, service)
			continue
		}
		if reachedMaxReplica(len(metrics), policy.MaxReplicas) {
			continue
		}
		if anyEndpointWithoutMemoryLimit(metrics) {
			delete(signals, service)
			continue
		}
		high := maxMemoryPercent(metrics)
		if high < policy.MemoryThreshold {
			delete(signals, service)
			continue
		}
		signal := signals[service]
		if signal.Since == "" {
			signal.Since = now.Format(time.RFC3339)
			signals[service] = signal
			continue
		}
		since, err := time.Parse(time.RFC3339, signal.Since)
		if err != nil {
			signal.Since = now.Format(time.RFC3339)
			signals[service] = signal
			continue
		}
		if now.Sub(since) < policy.Sustain {
			continue
		}
		if signal.LastScaledAt != "" {
			if last, err := time.Parse(time.RFC3339, signal.LastScaledAt); err == nil && now.Sub(last) < policy.Cooldown {
				continue
			}
		}
		next["autoscaleSignals"] = signals
		return next, autoscaleDecision{
			Service: service,
			Reason:  fmt.Sprintf("memory %.1f%% >= %.1f%% for %s", high, policy.MemoryThreshold, policy.Sustain),
		}
	}
	next["autoscaleSignals"] = signals
	return next, autoscaleDecision{}
}

func (s Service) ScaleOut(ctx context.Context, req ScaleOutRequest, log Logger, targetLog targetLogger) error {
	service := strings.TrimSpace(req.ServiceName)
	if service == "" || !isAIFARService(service) {
		return fmt.Errorf("unsupported AIFAR service for autoscale: %s", service)
	}
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	current, err := s.acquireOrchestrationLock(req.Instance.ID, "autoscale", service, req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(req.Instance.ID, "autoscale", service)

	metadata := metadataFromInstance(current)
	if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support autoscale; reinstall with k8s-like orchestration first"}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	policy := autoscalePolicyFromMetadata(metadata)
	if !policy.Enabled {
		err := errors.New("AIFAR autoscale is disabled")
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
	releaseID := currentRevisionForService(metadata, service)
	if releaseID == "" {
		err := errors.New("AIFAR service revision is missing")
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	status, err := collectAutoscaleStatus(ctx, s.remote, req.Server, installRoot)
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	currentReplicas := len(metricsByService(status.Endpoints)[service])
	if currentReplicas >= policy.MaxReplicas {
		err := fmt.Errorf("AIFAR service %s already reached max replicas: %d", service, policy.MaxReplicas)
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	replicaID := nextReplicaID(status.Endpoints, service)
	if replicaID < 2 {
		replicaID = currentReplicas + 1
	}
	desiredBeforeScale := desiredReplicasFromMetadata(metadata)
	desiredBeforeScale[service] = replicaID
	containerName := podContainerName(service, releaseID, replicaID)
	workDir := installerkit.WorkDir(req.Server.DeployDir, AppName+"-agent", "runtime-v2", time.Now().UTC())
	script, err := renderAutoscaleOutScript(autoscaleOutScriptData{
		InstallRoot:     installRoot,
		ServiceName:     service,
		ReleaseID:       releaseID,
		ReplicaID:       replicaID,
		ContainerName:   containerName,
		IngressNetwork:  stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName)),
		MaxReplicas:     policy.MaxReplicas,
		DesiredReplicas: replicaAssignments(desiredBeforeScale),
	})
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	if err := s.ensureRuntimeAgent(ctx, req.Server, workDir, req.Language, logForServer); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	logForServer.Info("scaling out AIFAR service %s: %s", service, req.Reason)
	if _, err := installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_AUTOSCALE_OUT'\n"+script+"\nAIFAR_AUTOSCALE_OUT", logForServer, "AIFAR autoscale command failed"); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	status, err = collectAutoscaleStatus(ctx, s.remote, req.Server, installRoot)
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	nextMetadata := metadataFromInstance(current)
	desired := desiredReplicasFromMetadata(nextMetadata)
	activeEndpoints := activeEndpointsFromMetrics(status.Endpoints)
	for svc, endpoints := range activeEndpoints {
		if svc != service && desired[svc] == 0 {
			continue
		}
		if count := endpointCount(endpoints); count > 0 {
			desired[svc] = count
		}
	}
	for svc, replicas := range desired {
		if svc != service && replicas == 0 {
			delete(activeEndpoints, svc)
		}
	}
	now := time.Now().UTC()
	nextMetadata["desiredReplicas"] = desired
	nextMetadata["activeEndpoints"] = activeEndpoints
	nextMetadata["activeServices"] = activeServicesFromEndpoints(desired, activeEndpoints)
	nextMetadata["serviceRevisions"] = serviceRevisionsFromMetadata(nextMetadata)
	nextMetadata["autoscalePolicy"] = policy.metadata()
	nextMetadata["autoscaleMetrics"] = metricsMetadata(status.Endpoints, now)
	nextMetadata = recordAutoscaleEvent(nextMetadata, "scaled-out", service, containerName, now)
	signals := autoscaleSignalsFromMetadata(nextMetadata)
	signal := signals[service]
	signal.LastScaledAt = now.Format(time.RFC3339)
	signal.Since = ""
	signals[service] = signal
	nextMetadata["autoscaleSignals"] = signals
	delete(nextMetadata, "orchestrationLock")
	if err := saveMetadata(s.store, current, nextMetadata); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	if err := s.saveRolloutControlPlane(current.ID, current.Version, releaseID, service, "", desired[service], intFromMetadata(nextMetadata, "gatewayPort", defaultGatewayPort), intFromMetadata(nextMetadata, "webPort", defaultWebPort), now); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	logForServer.Info("AIFAR service %s scaled out with replica %s", service, containerName)
	finishTarget(recorder, target, "success", "")
	return nil
}

func collectAutoscaleStatus(ctx context.Context, remote Remote, server store.Server, installRoot string) (autoscaleStatus, error) {
	result, err := remote.Run(ctx, server, autoscaleStatusCommand(installRoot))
	if err != nil {
		return autoscaleStatus{}, err
	}
	return parseAutoscaleStatus(result.Stdout), nil
}

func parseAutoscaleStatus(output string) autoscaleStatus {
	status := autoscaleStatus{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "endpoint":
			parts := strings.Split(value, "|")
			if len(parts) < 9 {
				continue
			}
			service := metadataText(parts[0])
			container := metadataText(parts[1])
			if service == "" || container == "" {
				continue
			}
			status.Endpoints = append(status.Endpoints, autoscaleMetric{
				Service:          service,
				Container:        container,
				ReleaseID:        metadataText(parts[2]),
				ReplicaID:        atoiDefault(parts[3], 1),
				Port:             atoiDefault(parts[4], 0),
				Running:          strings.EqualFold(parts[5], "true"),
				Health:           strings.TrimSpace(parts[6]),
				MemoryPercent:    atofDefault(parts[7], 0),
				MemoryLimitBytes: int64(atoiDefault(parts[8], 0)),
			})
		case "hostMemoryAvailableBytes":
			status.HostMemoryAvailableByte = int64(atoiDefault(value, 0))
		case "agentStatus":
			var payload struct {
				Instances []struct {
					DeploymentStatus []autoscaleDeploymentStatus `json:"deploymentStatus"`
				} `json:"instances"`
			}
			if json.Unmarshal([]byte(value), &payload) != nil {
				continue
			}
			if status.Deployments == nil {
				status.Deployments = map[string]autoscaleDeploymentStatus{}
			}
			for _, instance := range payload.Instances {
				for _, deployment := range instance.DeploymentStatus {
					service := cleanAIFARServiceName(deployment.ServiceName)
					if service == "" {
						continue
					}
					deployment.ServiceName = service
					status.Deployments[service] = deployment
				}
			}
		}
	}
	return status
}

func autoscaleStatusCommand(installRoot string) string {
	return "sh -s <<'AIFAR_AUTOSCALE_STATUS'\n" + `#!/usr/bin/env sh
set -u
INSTALL_ROOT=` + installerkit.ShellQuote(installRoot) + `
ENV_DIR="$INSTALL_ROOT/runtime/` + releaseEnvDirName + `"

read_env_value() {
  rev_file="$1"
  rev_key="$2"
  rev_default="${3:-}"
  if [ -f "$rev_file" ]; then
    rev_value="$(awk -F= -v key="$rev_key" '$1==key {print substr($0, index($0, "=")+1)}' "$rev_file" | tail -n 1)"
    if [ -n "$rev_value" ]; then
      printf "%s" "$rev_value"
      return 0
    fi
  fi
  printf "%s" "$rev_default"
}

service_port_var() {
  case "$1" in
    gateway) printf "GATEWAY_PORT" ;;
    oauth) printf "OAUTH_PORT" ;;
    permission) printf "PERMISSION_PORT" ;;
    system) printf "SYSTEM_PORT" ;;
    file) printf "FILE_PORT" ;;
    message) printf "MESSAGE_PORT" ;;
    im) printf "IM_PORT" ;;
    contacts) printf "CONTACTS_PORT" ;;
    meeting) printf "MEETING_PORT" ;;
    web-vue3) printf "WEB_VUE3_PORT" ;;
    *) printf "" ;;
  esac
}

if command -v docker >/dev/null 2>&1 && [ -d "$ENV_DIR" ]; then
  for service in ` + serviceOrderText() + `; do
    port_var="$(service_port_var "$service")"
    port="$(read_env_value "$ENV_DIR/compose.env" "$port_var" 0)"
    names="$(docker ps -a --filter "label=aifar.app=aifar" --filter "label=aifar.install-root=$INSTALL_ROOT" --filter "label=aifar.component=pod" --filter "label=aifar.service=$service" --format '{{.Names}}' 2>/dev/null || true)"
    for name in $names; do
      running="$(docker inspect -f '{{.State.Running}}' "$name" 2>/dev/null || echo false)"
      health="$(docker inspect -f '{{if .State.Health}}{{.State.Health.Status}}{{end}}' "$name" 2>/dev/null || true)"
      release="$(docker inspect -f '{{index .Config.Labels "aifar.revision"}}' "$name" 2>/dev/null || true)"
      replica="$(docker inspect -f '{{index .Config.Labels "aifar.replica"}}' "$name" 2>/dev/null || true)"
      [ -n "$replica" ] || replica=1
      mem="$(docker stats --no-stream --format '{{.MemPerc}}' "$name" 2>/dev/null | tr -d ' %' || true)"
      [ -n "$mem" ] || mem=0
      limit="$(docker inspect -f '{{.HostConfig.Memory}}' "$name" 2>/dev/null || echo 0)"
      case "$limit" in ""|*[!0-9]*) limit=0 ;; esac
      printf "endpoint=%s|%s|%s|%s|%s|%s|%s|%s|%s\n" "$service" "$name" "$release" "$replica" "$port" "$running" "$health" "$mem" "$limit"
    done
  done
fi
if command -v aifar-agent >/dev/null 2>&1; then
  agent_status="$(aifar-agent status 2>/dev/null | tr -d '\r\n' || true)"
  [ -z "$agent_status" ] || printf "agentStatus=%s\n" "$agent_status"
fi
available="$(awk '/MemAvailable/ {print $2 * 1024}' /proc/meminfo 2>/dev/null | cut -d. -f1)"
[ -n "$available" ] || available=0
echo "hostMemoryAvailableBytes=$available"
` + "\nAIFAR_AUTOSCALE_STATUS"
}

func autoscalePolicyFromMetadata(metadata map[string]any) AutoscalePolicy {
	policy := defaultAutoscalePolicy()
	raw, _ := metadata["autoscalePolicy"].(map[string]any)
	if raw == nil {
		return policy
	}
	if value, ok := raw["enabled"]; ok {
		policy.Enabled = boolFromAny(value, policy.Enabled)
	}
	if value, ok := raw["memoryThreshold"]; ok {
		policy.MemoryThreshold = floatFromAny(value, policy.MemoryThreshold)
	}
	if value, ok := raw["sustainSeconds"]; ok {
		policy.Sustain = time.Duration(intFromAny(value, int(policy.Sustain.Seconds()))) * time.Second
	}
	if value, ok := raw["cooldownSeconds"]; ok {
		policy.Cooldown = time.Duration(intFromAny(value, int(policy.Cooldown.Seconds()))) * time.Second
	}
	if value, ok := raw["maxReplicas"]; ok {
		policy.MaxReplicas = intFromAny(value, policy.MaxReplicas)
	}
	if value, ok := raw["scaleIn"]; ok {
		policy.ScaleIn = boolFromAny(value, policy.ScaleIn)
	}
	if policy.MemoryThreshold <= 0 {
		policy.MemoryThreshold = autoscaleDefaultThreshold
	}
	if policy.Sustain <= 0 {
		policy.Sustain = autoscaleDefaultSustain
	}
	if policy.Cooldown <= 0 {
		policy.Cooldown = autoscaleDefaultCooldown
	}
	if policy.MaxReplicas < 1 {
		policy.MaxReplicas = autoscaleDefaultMaxReplica
	}
	return policy
}

func defaultAutoscalePolicy() AutoscalePolicy {
	return AutoscalePolicy{
		Enabled:         true,
		MemoryThreshold: autoscaleDefaultThreshold,
		Sustain:         autoscaleDefaultSustain,
		Cooldown:        autoscaleDefaultCooldown,
		MaxReplicas:     autoscaleDefaultMaxReplica,
		ScaleIn:         false,
	}
}

func (p AutoscalePolicy) metadata() map[string]any {
	return map[string]any{
		"enabled":         p.Enabled,
		"memoryThreshold": p.MemoryThreshold,
		"sustainSeconds":  int(p.Sustain.Seconds()),
		"cooldownSeconds": int(p.Cooldown.Seconds()),
		"maxReplicas":     p.MaxReplicas,
		"scaleIn":         p.ScaleIn,
	}
}

func applyEffectiveServiceFields(manifest map[string]any, orchestration map[string]any) {
	if desired, ok := orchestration["desiredReplicas"]; ok {
		manifest["desiredReplicas"] = desired
	}
	if endpoints, ok := orchestration["activeEndpoints"]; ok {
		manifest["endpoints"] = endpoints
	}
	if services, ok := orchestration["activeServices"]; ok {
		manifest["activeServices"] = services
	}
	if policy, ok := orchestration["autoscalePolicy"]; ok {
		manifest["autoscalePolicy"] = policy
	}
}

func metricsByService(metrics []autoscaleMetric) map[string][]autoscaleMetric {
	out := map[string][]autoscaleMetric{}
	for _, metric := range metrics {
		if !metric.Running {
			continue
		}
		out[metric.Service] = append(out[metric.Service], metric)
	}
	return out
}

func activeEndpointsFromMetrics(metrics []autoscaleMetric) map[string]any {
	grouped := metricsByService(metrics)
	out := map[string]any{}
	for _, service := range serviceOrder {
		items := grouped[service]
		endpoints := make([]map[string]any, 0, len(items))
		for _, item := range items {
			endpoints = append(endpoints, map[string]any{
				"container":        item.Container,
				"releaseId":        item.ReleaseID,
				"replicaId":        item.ReplicaID,
				"port":             item.Port,
				"state":            "active",
				"health":           item.Health,
				"memoryPercent":    item.MemoryPercent,
				"memoryLimitBytes": item.MemoryLimitBytes,
			})
		}
		if len(endpoints) > 0 {
			out[service] = endpoints
		}
	}
	return out
}

func endpointCount(value any) int {
	switch typed := value.(type) {
	case []map[string]any:
		return len(typed)
	case []any:
		return len(typed)
	default:
		return 0
	}
}

func metricsMetadata(metrics []autoscaleMetric, now time.Time) map[string]any {
	out := map[string]any{}
	for _, metric := range metrics {
		out[metric.Container] = map[string]any{
			"service":          metric.Service,
			"releaseId":        metric.ReleaseID,
			"replicaId":        metric.ReplicaID,
			"memoryPercent":    metric.MemoryPercent,
			"memoryLimitBytes": metric.MemoryLimitBytes,
			"running":          metric.Running,
			"health":           metric.Health,
			"checkedAt":        now.Format(time.RFC3339),
		}
	}
	return out
}

func autoscaleSignalsFromMetadata(metadata map[string]any) map[string]autoscaleSignal {
	out := map[string]autoscaleSignal{}
	raw, _ := metadata["autoscaleSignals"].(map[string]any)
	for service, value := range raw {
		data, _ := json.Marshal(value)
		var signal autoscaleSignal
		_ = json.Unmarshal(data, &signal)
		out[service] = signal
	}
	return out
}

func recordAutoscaleEvent(metadata map[string]any, status, service, message string, now time.Time) map[string]any {
	next := copyMetadata(metadata)
	events := []any{}
	if raw, ok := next["lastScaleEvents"].([]any); ok {
		events = append(events, raw...)
	}
	events = append(events, map[string]any{
		"status":    status,
		"service":   service,
		"message":   message,
		"createdAt": now.Format(time.RFC3339),
	})
	if len(events) > 20 {
		events = events[len(events)-20:]
	}
	next["lastScaleEvents"] = events
	return next
}

func (a *Autoscaler) recordScaleEvent(instance store.AppInstance, status, service, message string, now time.Time) {
	metadata := recordAutoscaleEvent(metadataFromInstance(instance), status, service, message, now)
	_ = saveMetadata(a.store, instance, metadata)
}

func saveMetadata(s interface {
	SaveAppInstance(store.AppInstance) (store.AppInstance, error)
}, instance store.AppInstance, metadata map[string]any) error {
	data, _ := json.Marshal(metadata)
	instance.Metadata = string(data)
	_, err := s.SaveAppInstance(instance)
	return err
}

func copyMetadata(metadata map[string]any) map[string]any {
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func activeOrchestrationLock(lock map[string]any, now time.Time) bool {
	if len(lock) == 0 {
		return false
	}
	startedAt := strings.TrimSpace(fmt.Sprint(lock["startedAt"]))
	if startedAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return true
	}
	return now.Sub(t) < orchestrationLockTTL
}

func orchestrationLocked(metadata map[string]any, now time.Time) bool {
	lock, _ := metadata["orchestrationLock"].(map[string]any)
	return activeOrchestrationLock(lock, now)
}

func serviceOrchestrationLocksFromMetadata(metadata map[string]any) map[string]map[string]any {
	locks := map[string]map[string]any{}
	if typed, ok := metadata["orchestrationLocks"].(map[string]map[string]any); ok {
		for service, lock := range typed {
			if strings.TrimSpace(service) != "" && len(lock) > 0 {
				locks[service] = lock
			}
		}
		return locks
	}
	raw, _ := metadata["orchestrationLocks"].(map[string]any)
	for service, value := range raw {
		if strings.TrimSpace(service) == "" {
			continue
		}
		if lock, ok := value.(map[string]any); ok {
			locks[service] = lock
			continue
		}
		data, err := json.Marshal(value)
		if err != nil {
			continue
		}
		var lock map[string]any
		if err := json.Unmarshal(data, &lock); err == nil && len(lock) > 0 {
			locks[service] = lock
		}
	}
	return locks
}

func serviceOrchestrationLocked(metadata map[string]any, service string, now time.Time) bool {
	locks := serviceOrchestrationLocksFromMetadata(metadata)
	return activeOrchestrationLock(locks[strings.TrimSpace(service)], now)
}

func pruneExpiredOrchestrationLocks(metadata map[string]any, now time.Time) {
	lock, _ := metadata["orchestrationLock"].(map[string]any)
	if len(lock) > 0 && !activeOrchestrationLock(lock, now) {
		delete(metadata, "orchestrationLock")
	}

	locks := serviceOrchestrationLocksFromMetadata(metadata)
	changed := false
	for service, lock := range locks {
		if !activeOrchestrationLock(lock, now) {
			delete(locks, service)
			changed = true
		}
	}
	if !changed {
		return
	}
	if len(locks) == 0 {
		delete(metadata, "orchestrationLocks")
		return
	}
	metadata["orchestrationLocks"] = locks
}

func anyServiceOrchestrationLocked(metadata map[string]any, now time.Time) bool {
	for _, lock := range serviceOrchestrationLocksFromMetadata(metadata) {
		if activeOrchestrationLock(lock, now) {
			return true
		}
	}
	return false
}

func (a *Autoscaler) orchestrationLocked(instanceID, serviceName string, now time.Time) bool {
	locks, err := a.store.ListAIFAROrchestrationLocks(instanceID, true)
	if err != nil {
		return false
	}
	serviceName = strings.TrimSpace(serviceName)
	for _, lock := range locks {
		if !lock.ExpiresAt.IsZero() && !now.Before(lock.ExpiresAt) {
			continue
		}
		if serviceName == "" || strings.TrimSpace(lock.ServiceName) == "" || strings.TrimSpace(lock.ServiceName) == serviceName {
			return true
		}
	}
	return false
}

func (s Service) acquireOrchestrationLock(instanceID, operation, serviceName, actor, taskID string) (store.AppInstance, error) {
	instance, err := s.store.GetAppInstance(instanceID)
	if err != nil {
		return instance, err
	}
	metadata := metadataFromInstance(instance)
	now := time.Now().UTC()
	if lockStore, ok := s.store.(aifarOrchestrationLockStore); ok {
		migrated, err := s.migrateLegacyOrchestrationLocks(lockStore, instance, metadata, now)
		if err != nil {
			return migrated, err
		}
		instance = migrated
		lock := store.AIFAROrchestrationLock{
			InstanceID:  instance.ID,
			ServiceName: strings.TrimSpace(serviceName),
			Operation:   strings.TrimSpace(operation),
			Actor:       strings.TrimSpace(actor),
			TaskID:      strings.TrimSpace(taskID),
			StartedAt:   now,
			ExpiresAt:   now.Add(orchestrationLockTTL),
		}
		if _, err := lockStore.AcquireAIFAROrchestrationLock(lock); err != nil {
			var conflict store.AIFAROrchestrationLockConflict
			if errors.As(err, &conflict) {
				return instance, orchestrationConflictError(serviceName, conflict.Lock.ServiceName)
			}
			return instance, err
		}
		return instance, nil
	}
	pruneExpiredOrchestrationLocks(metadata, now)
	serviceName = strings.TrimSpace(serviceName)
	if orchestrationLocked(metadata, now) ||
		(serviceName == "" && anyServiceOrchestrationLocked(metadata, now)) ||
		(serviceName != "" && serviceOrchestrationLocked(metadata, serviceName, now)) {
		return instance, fmt.Errorf("AIFAR instance orchestration is locked")
	}
	lock := map[string]any{
		"operation": operation,
		"service":   serviceName,
		"actor":     actor,
		"taskId":    taskID,
		"startedAt": now.Format(time.RFC3339),
	}
	if strings.TrimSpace(serviceName) == "" {
		if anyServiceOrchestrationLocked(metadata, now) {
			return instance, fmt.Errorf("AIFAR instance orchestration is locked")
		}
		metadata["orchestrationLock"] = lock
	} else {
		locks := serviceOrchestrationLocksFromMetadata(metadata)
		locks[serviceName] = lock
		metadata["orchestrationLocks"] = locks
	}
	if err := saveMetadata(s.store, instance, metadata); err != nil {
		return instance, err
	}
	instance.Metadata = mustMarshalMetadata(metadata)
	return instance, nil
}

func (s Service) releaseOrchestrationLock(instanceID, operation, serviceName string) {
	if lockStore, ok := s.store.(aifarOrchestrationLockStore); ok {
		if released, err := lockStore.ReleaseAIFAROrchestrationLock(instanceID, operation, serviceName); err == nil && released {
			return
		}
	}
	instance, err := s.store.GetAppInstance(instanceID)
	if err != nil {
		return
	}
	metadata := metadataFromInstance(instance)
	if strings.TrimSpace(serviceName) != "" {
		locks := serviceOrchestrationLocksFromMetadata(metadata)
		lock := locks[serviceName]
		if len(lock) > 0 {
			if strings.TrimSpace(fmt.Sprint(lock["operation"])) != operation {
				return
			}
			delete(locks, serviceName)
			if len(locks) == 0 {
				delete(metadata, "orchestrationLocks")
			} else {
				metadata["orchestrationLocks"] = locks
			}
			_ = saveMetadata(s.store, instance, metadata)
			return
		}

		legacyLock, _ := metadata["orchestrationLock"].(map[string]any)
		if strings.TrimSpace(fmt.Sprint(legacyLock["operation"])) != operation ||
			strings.TrimSpace(fmt.Sprint(legacyLock["service"])) != serviceName {
			return
		}
		delete(metadata, "orchestrationLock")
		_ = saveMetadata(s.store, instance, metadata)
		return
	}

	lock, _ := metadata["orchestrationLock"].(map[string]any)
	if strings.TrimSpace(fmt.Sprint(lock["operation"])) != operation {
		return
	}
	delete(metadata, "orchestrationLock")
	_ = saveMetadata(s.store, instance, metadata)
}

func (s Service) migrateLegacyOrchestrationLocks(lockStore aifarOrchestrationLockStore, instance store.AppInstance, metadata map[string]any, now time.Time) (store.AppInstance, error) {
	globalLock, hasGlobal := metadata["orchestrationLock"].(map[string]any)
	serviceLocks := serviceOrchestrationLocksFromMetadata(metadata)
	if !hasGlobal && len(serviceLocks) == 0 {
		return instance, nil
	}
	legacy := make([]store.AIFAROrchestrationLock, 0, len(serviceLocks)+1)
	if hasGlobal && activeOrchestrationLock(globalLock, now) {
		legacy = append(legacy, legacyOrchestrationLock(instance.ID, "", globalLock, now))
	}
	for serviceName, lock := range serviceLocks {
		if activeOrchestrationLock(lock, now) {
			legacy = append(legacy, legacyOrchestrationLock(instance.ID, serviceName, lock, now))
		}
	}
	for _, lock := range legacy {
		if _, err := lockStore.AcquireAIFAROrchestrationLock(lock); err != nil {
			var conflict store.AIFAROrchestrationLockConflict
			if !errors.As(err, &conflict) {
				return instance, err
			}
		}
	}
	delete(metadata, "orchestrationLock")
	delete(metadata, "orchestrationLocks")
	metadata["lastOrchestrationLockMigration"] = map[string]any{
		"migratedAt": now.Format(time.RFC3339),
		"count":      len(legacy),
	}
	if err := saveMetadata(s.store, instance, metadata); err != nil {
		return instance, err
	}
	instance.Metadata = mustMarshalMetadata(metadata)
	return instance, nil
}

func legacyOrchestrationLock(instanceID, serviceName string, lock map[string]any, now time.Time) store.AIFAROrchestrationLock {
	startedAt := orchestrationLockStartedAt(lock, now)
	operation := strings.TrimSpace(fmt.Sprint(lock["operation"]))
	if operation == "" {
		operation = "legacy"
	}
	return store.AIFAROrchestrationLock{
		InstanceID:  instanceID,
		ServiceName: strings.TrimSpace(serviceName),
		Operation:   operation,
		Actor:       strings.TrimSpace(fmt.Sprint(lock["actor"])),
		TaskID:      strings.TrimSpace(fmt.Sprint(lock["taskId"])),
		StartedAt:   startedAt,
		ExpiresAt:   startedAt.Add(orchestrationLockTTL),
	}
}

func orchestrationLockStartedAt(lock map[string]any, fallback time.Time) time.Time {
	startedAt := strings.TrimSpace(fmt.Sprint(lock["startedAt"]))
	if startedAt == "" {
		return fallback
	}
	t, err := time.Parse(time.RFC3339, startedAt)
	if err != nil {
		return fallback
	}
	return t.UTC()
}

func orchestrationConflictError(requestService, conflictService string) error {
	requestService = strings.TrimSpace(requestService)
	conflictService = strings.TrimSpace(conflictService)
	if requestService != "" && conflictService == requestService {
		return fmt.Errorf("AIFAR service %s orchestration is locked", requestService)
	}
	return fmt.Errorf("AIFAR instance orchestration is locked")
}

func mustMarshalMetadata(metadata map[string]any) string {
	data, _ := json.Marshal(metadata)
	return string(data)
}

func reachedMaxReplica(current, max int) bool {
	if max < 1 {
		max = autoscaleDefaultMaxReplica
	}
	return current >= max
}

func anyEndpointWithoutMemoryLimit(metrics []autoscaleMetric) bool {
	for _, metric := range metrics {
		if metric.MemoryLimitBytes <= 0 {
			return true
		}
	}
	return false
}

func maxMemoryPercent(metrics []autoscaleMetric) float64 {
	var out float64
	for _, metric := range metrics {
		if metric.MemoryPercent > out {
			out = metric.MemoryPercent
		}
	}
	return out
}

func nextReplicaID(metrics []autoscaleMetric, service string) int {
	maxID := 0
	for _, metric := range metrics {
		if metric.Service == service && metric.ReplicaID > maxID {
			maxID = metric.ReplicaID
		}
	}
	return maxID + 1
}

func autoscaleReplicaContainerName(service, releaseID string, replicaID int) string {
	return releaseReplicaContainerName(service, releaseID, replicaID)
}

func isAIFARService(service string) bool {
	for _, candidate := range serviceOrder {
		if candidate == service {
			return true
		}
	}
	return false
}

func atoiDefault(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return n
}

func atofDefault(value string, fallback float64) float64 {
	n, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return n
}

func floatFromAny(value any, fallback float64) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case string:
		return atofDefault(v, fallback)
	default:
		return fallback
	}
}

func boolFromAny(value any, fallback bool) bool {
	switch v := value.(type) {
	case bool:
		return v
	case string:
		if parsed, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return parsed
		}
	}
	return fallback
}
