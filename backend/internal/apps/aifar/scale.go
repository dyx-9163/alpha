package aifar

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

const runtimeControlPlaneRepairCode = "AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED"

func (s Service) ScaleService(ctx context.Context, req ScaleRequest, log Logger, targetLog targetLogger) error {
	return s.ScaleServices(ctx, ScaleServicesRequest{
		Instance:        req.Instance,
		Server:          req.Server,
		Language:        req.Language,
		Actor:           req.Actor,
		TaskID:          req.TaskID,
		DesiredReplicas: map[string]int{req.ServiceName: req.Replicas},
		Reason:          req.Reason,
	}, log, targetLog)
}

func (s Service) ScaleServices(ctx context.Context, req ScaleServicesRequest, log Logger, targetLog targetLogger) error {
	desiredTargets, services, err := normalizeScaleTargets(req.DesiredReplicas)
	if err != nil {
		return err
	}
	primaryService := services[0]
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, scaleServiceSteps(), "AIFAR scale step %d/%d started: %s", "AIFAR scale step %d/%d completed: %s", "AIFAR scale step %d/%d failed: %s: %v")

	current, err := s.acquireOrchestrationLock(req.Instance.ID, "scale-service", strings.Join(services, ","), req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(req.Instance.ID, "scale-service", strings.Join(services, ","))

	var metadata map[string]any
	var installRoot string
	revisions := make(map[string]string, len(services))
	var script string
	var status autoscaleStatus
	remoteCommitted := false
	commitCtx := ctx
	var cancelCommit context.CancelFunc
	defer func() {
		if cancelCommit != nil {
			cancelCommit()
		}
	}()
	now := time.Now().UTC()

	if err := step(1, func() error {
		metadata = metadataFromInstance(current)
		if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support service scale; reinstall with k8s-like orchestration first"}); err != nil {
			return err
		}
		installed := servicesFromMetadata(metadata)
		for _, service := range services {
			if !serviceInList(service, installed) {
				return fmt.Errorf("AIFAR service %s is not installed", service)
			}
		}
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		if strings.TrimSpace(installRoot) == "" {
			return errors.New("AIFAR install root is missing")
		}
		for _, service := range services {
			revision := currentRevisionForService(metadata, service)
			if revision == "" {
				return fmt.Errorf("AIFAR service %s revision is missing", service)
			}
			revisions[service] = revision
		}
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(2, func() error {
		var renderErr error
		script, renderErr = renderScaleServiceScript(scaleServiceScriptData{
			InstallRoot:           installRoot,
			ServiceOrder:          strings.Join(servicesFromMetadata(metadata), " "),
			ServiceName:           primaryService,
			Replicas:              desiredTargets[primaryService],
			IngressNetwork:        stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName)),
			TaskID:                fallbackTaskID(req.TaskID, log),
			DesiredReplicas:       replicaAssignments(desiredReplicasFromMetadata(metadata)),
			TargetDesiredReplicas: replicaAssignments(desiredTargets),
		})
		return renderErr
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(3, func() error {
		workDir := installerkit.WorkDir(req.Server.DeployDir, AppName+"-agent", "runtime-v2", time.Now().UTC())
		if err := s.ensureRuntimeAgent(ctx, req.Server, workDir, req.Language, logForServer); err != nil {
			return err
		}
		if boundary, ok := logForServer.(interface{ TryEnterCommit() bool }); ok && !boundary.TryEnterCommit() {
			return context.Canceled
		}
		commitCtx, cancelCommit = context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
		if strings.TrimSpace(req.Reason) != "" {
			logForServer.Info("scaling AIFAR services %s: %s", replicaAssignments(desiredTargets), req.Reason)
		} else {
			logForServer.Info("scaling AIFAR services %s", replicaAssignments(desiredTargets))
		}
		_, runErr := installerkit.Run(commitCtx, s.remote, req.Server, "sh -s <<'AIFAR_SCALE_SERVICE'\n"+script+"\nAIFAR_SCALE_SERVICE", logForServer, "AIFAR service scale failed")
		if runErr == nil {
			remoteCommitted = true
			return nil
		}
		readback, readbackErr := collectAutoscaleStatus(commitCtx, s.remote, req.Server, installRoot)
		if readbackErr != nil || !scaleCommitObservedAll(readback, desiredTargets) {
			return runErr
		}
		if _, finalizeErr := installerkit.Run(commitCtx, s.remote, req.Server, scaleServiceFinalizeCommand(installRoot, fallbackTaskID(req.TaskID, log)), logForServer, "AIFAR service scale finalize failed"); finalizeErr != nil {
			return fmt.Errorf("%s: %w", runtimeControlPlaneRepairCode, finalizeErr)
		}
		status = readback
		remoteCommitted = true
		logForServer.Info("AIFAR services %s commit confirmed from agent state after remote response loss", strings.Join(services, ","))
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(4, func() error {
		if status.Deployments == nil {
			var collectErr error
			status, collectErr = collectAutoscaleStatus(commitCtx, s.remote, req.Server, installRoot)
			if collectErr != nil {
				return collectErr
			}
		}
		saved, err := s.store.GetAppInstance(current.ID)
		if err != nil {
			return err
		}
		nextMetadata := metadataFromInstance(saved)
		for _, service := range services {
			nextMetadata = metadataAfterServiceScale(nextMetadata, status, service, desiredTargets[service], now)
		}
		delete(nextMetadata, "orchestrationLock")
		if err := saveMetadata(s.store, saved, nextMetadata); err != nil {
			return err
		}
		for _, service := range services {
			if err := s.saveServiceScaleControlPlane(saved.ID, saved.Version, revisions[service], service, desiredTargets[service], intFromMetadata(nextMetadata, "gatewayPort", defaultGatewayPort), intFromMetadata(nextMetadata, "webPort", defaultWebPort), status, now); err != nil {
				return err
			}
		}
		if cleanup, ok := s.store.(aifarRuntimeCleanupStore); ok {
			names := containerNamesFromAutoscaleStatus(status)
			if _, err := cleanup.PruneAIFARPodRecords(saved.ID, names); err != nil {
				return err
			}
			if _, err := cleanup.PruneAIFARServiceEndpointRecords(saved.ID, names); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		if remoteCommitted && !strings.HasPrefix(err.Error(), runtimeControlPlaneRepairCode+":") {
			return fmt.Errorf("%s: %w", runtimeControlPlaneRepairCode, err)
		}
		return err
	}

	logForServer.Info("AIFAR services desired replicas set: %s", replicaAssignments(desiredTargets))
	finishTarget(recorder, target, "success", "")
	return nil
}

func normalizeScaleTargets(requested map[string]int) (map[string]int, []string, error) {
	if len(requested) == 0 {
		return nil, nil, errors.New("at least one AIFAR service scale target is required")
	}
	desired := make(map[string]int, len(requested))
	for rawService, replicas := range requested {
		service := cleanAIFARServiceName(rawService)
		if service == "" || !isAIFARService(service) {
			return nil, nil, fmt.Errorf("unsupported AIFAR service for scale: %s", strings.TrimSpace(rawService))
		}
		if replicas < 0 {
			return nil, nil, errors.New("AIFAR service replicas must be greater than or equal to 0")
		}
		desired[service] = replicas
	}
	services := make([]string, 0, len(desired))
	for _, service := range serviceOrder {
		if _, ok := desired[service]; ok {
			services = append(services, service)
		}
	}
	if len(services) != len(desired) {
		return nil, nil, errors.New("unsupported AIFAR service scale target")
	}
	return desired, services, nil
}

func scaleCommitObserved(status autoscaleStatus, service string, replicas int) bool {
	deployment, ok := status.Deployments[cleanAIFARServiceName(service)]
	if !ok {
		return false
	}
	if replicas < 0 {
		replicas = 0
	}
	return deployment.DesiredReplicas == replicas && deployment.CurrentReplicas == replicas && deployment.ReadyReplicas == replicas
}

func scaleCommitObservedAll(status autoscaleStatus, desired map[string]int) bool {
	for service, replicas := range desired {
		if !scaleCommitObserved(status, service, replicas) {
			return false
		}
	}
	return true
}

func scaleServiceFinalizeCommand(installRoot, taskID string) string {
	return "sh -s <<'AIFAR_SCALE_FINALIZE'\n" + `#!/usr/bin/env sh
set -eu
INSTALL_ROOT=` + installerkit.ShellQuote(installRoot) + `
TASK_ID=` + installerkit.ShellQuote(taskID) + `
TASK_TOKEN="$(printf "%s" "${TASK_ID:-manual}" | tr -c 'A-Za-z0-9._-' '_')"
CANONICAL_ENV="$INSTALL_ROOT/runtime/env/compose.env"
CANONICAL_SPEC="$INSTALL_ROOT/runtime/agent/runtime-spec.json"
STAGED_ENV="$CANONICAL_ENV.$TASK_TOKEN.staged"
STAGED_SPEC="$CANONICAL_SPEC.$TASK_TOKEN.staged"
echo AIFAR_SCALE_FINALIZE
if [ -f "$STAGED_ENV" ] && [ -f "$STAGED_SPEC" ]; then
  mv "$STAGED_ENV" "$CANONICAL_ENV"
  mv "$STAGED_SPEC" "$CANONICAL_SPEC"
elif [ -f "$STAGED_ENV" ] || [ -f "$STAGED_SPEC" ]; then
  echo "incomplete AIFAR scale staging files" >&2
  exit 1
fi
rm -f "$CANONICAL_ENV.$TASK_TOKEN.rollback" "$CANONICAL_SPEC.$TASK_TOKEN.rollback"
` + "\nAIFAR_SCALE_FINALIZE"
}

func metadataAfterServiceScale(metadata map[string]any, status autoscaleStatus, service string, replicas int, now time.Time) map[string]any {
	next := copyMetadata(metadata)
	desired := desiredReplicasFromMetadata(next)
	if replicas < 0 {
		replicas = 0
	}
	desired[service] = replicas
	activeEndpoints := activeEndpointsFromMetrics(status.Endpoints)
	if replicas == 0 {
		delete(activeEndpoints, service)
	}
	next["desiredReplicas"] = desired
	next["activeEndpoints"] = activeEndpoints
	next["activeServices"] = activeServicesFromEndpointsForServices(desired, activeEndpoints, servicesFromMetadata(next))
	containers := mapFromMetadataValue(next["containers"])
	if containers == nil {
		containers = map[string]any{}
	}
	if replicas == 0 {
		delete(containers, service)
	} else if first := firstContainerForService(status, service); first != "" {
		containers[service] = first
	}
	next["containers"] = containers
	next["autoscaleMetrics"] = metricsMetadata(status.Endpoints, now)
	next = recordAutoscaleEvent(next, "scaled", service, fmt.Sprintf("desired replicas set to %d", replicas), now)
	return next
}

func (s Service) saveServiceScaleControlPlane(instanceID, version, revision, service string, replicas, gatewayPort, webPort int, status autoscaleStatus, now time.Time) error {
	if replicas < 0 {
		replicas = 0
	}
	orch, ok := s.store.(aifarOrchestrationStore)
	if !ok {
		return nil
	}
	ready := readyMetricsForService(status, service)
	state := "active"
	if replicas == 0 {
		state = "offline"
	} else if len(ready) < replicas {
		state = "degraded"
	}
	port := serviceDefaultPort(service, gatewayPort, webPort)
	if replicas == 0 {
		ready = nil
	}
	if _, err := orch.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instanceID,
		ServiceName:     service,
		DesiredReplicas: replicas,
		CurrentRevision: revision,
		StrategyJSON:    `{"type":"Scale","maxSurge":0,"maxUnavailable":1}`,
		Status:          state,
		MetadataJSON:    fmt.Sprintf(`{"model":%q,"operation":"scale","replicas":%d}`, orchestrationModelK8sLikeV1, replicas),
		CreatedAt:       now,
	}); err != nil {
		return err
	}
	if _, err := orch.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:   instanceID,
		ServiceName:  service,
		Revision:     revision,
		Image:        fmt.Sprintf("aifar-%s:%s", service, revision),
		DesiredPods:  replicas,
		ReadyPods:    len(ready),
		Status:       state,
		MetadataJSON: fmt.Sprintf(`{"version":%q,"operation":"scale"}`, version),
		CreatedAt:    now,
	}); err != nil {
		return err
	}
	endpoints := make([]store.AIFARServiceEndpoint, 0, len(ready))
	for _, metric := range ready {
		podIDValue := podID(service, metric.ReleaseID, metric.ReplicaID)
		if metric.ReleaseID == "" {
			podIDValue = podID(service, revision, metric.ReplicaID)
		}
		pod := store.AIFARPod{
			InstanceID:    instanceID,
			ServiceName:   service,
			Revision:      firstNonEmpty(metric.ReleaseID, revision),
			PodID:         podIDValue,
			ContainerName: metric.Container,
			Port:          port,
			Status:        "ready",
			Ready:         true,
			MetadataJSON:  fmt.Sprintf(`{"replicaId":%d}`, metric.ReplicaID),
			CreatedAt:     now,
		}
		if _, err := orch.SaveAIFARPod(pod); err != nil {
			return err
		}
		endpoints = append(endpoints, store.AIFARServiceEndpoint{
			InstanceID:    instanceID,
			ServiceName:   service,
			PodID:         pod.PodID,
			ContainerName: metric.Container,
			Revision:      pod.Revision,
			Port:          port,
			State:         "active",
			Ready:         true,
			MetadataJSON:  pod.MetadataJSON,
			CreatedAt:     now,
		})
	}
	return orch.ReplaceAIFARServiceEndpoints(instanceID, service, endpoints)
}

func readyMetricsForService(status autoscaleStatus, service string) []autoscaleMetric {
	out := []autoscaleMetric{}
	for _, metric := range status.Endpoints {
		if metric.Service == service && metric.Running {
			out = append(out, metric)
		}
	}
	return out
}

func firstContainerForService(status autoscaleStatus, service string) string {
	for _, metric := range status.Endpoints {
		if metric.Service == service && metric.Running && strings.TrimSpace(metric.Container) != "" {
			return metric.Container
		}
	}
	return ""
}

func containerNamesFromAutoscaleStatus(status autoscaleStatus) []string {
	names := make([]string, 0, len(status.Endpoints))
	for _, metric := range status.Endpoints {
		if strings.TrimSpace(metric.Container) != "" {
			names = append(names, metric.Container)
		}
	}
	return names
}

func serviceInList(service string, services []string) bool {
	for _, item := range services {
		if item == service {
			return true
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func scaleServiceSteps() []installStepDef {
	return []installStepDef{
		{Name: "validate-service", Title: "validate AIFAR service scale request"},
		{Name: "render-scale-spec", Title: "render AIFAR runtime scale script"},
		{Name: "apply-scale", Title: "apply desired replicas through aifar-agent"},
		{Name: "record-scale", Title: "record AIFAR scale result"},
	}
}
