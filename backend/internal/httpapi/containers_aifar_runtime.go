package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

const aifarK8sLikeModel = "k8s-like-v1"

type aifarRuntimeResponse struct {
	ServerID      string                   `json:"serverId"`
	RuntimeStatus string                   `json:"runtimeStatus"`
	Agent         aifarRuntimeAgent        `json:"agent"`
	Instances     []aifarRuntimeInstance   `json:"instances"`
	Deployments   []aifarRuntimeDeployment `json:"deployments"`
	Pods          []aifarRuntimePod        `json:"pods"`
	Services      []aifarRuntimeService    `json:"services"`
	Ingress       []aifarRuntimeIngress    `json:"ingress"`
	Warnings      []string                 `json:"warnings,omitempty"`
}

type aifarRuntimeAgent struct {
	Status   string   `json:"status"`
	Version  string   `json:"version,omitempty"`
	Mode     string   `json:"mode,omitempty"`
	Error    string   `json:"error,omitempty"`
	Features []string `json:"features,omitempty"`
}

type aifarRuntimeInstance struct {
	ID                 string `json:"id"`
	Version            string `json:"version"`
	Status             string `json:"status"`
	OrchestrationModel string `json:"orchestrationModel,omitempty"`
	Legacy             bool   `json:"legacy"`
	InstallRoot        string `json:"installRoot,omitempty"`
	Endpoint           string `json:"endpoint,omitempty"`
	GatewayEndpoint    string `json:"gatewayEndpoint,omitempty"`
}

type aifarRuntimeDeployment struct {
	InstanceID       string `json:"instanceId"`
	ServiceName      string `json:"serviceName"`
	DesiredReplicas  int    `json:"desiredReplicas"`
	ReadyReplicas    int    `json:"readyReplicas"`
	CurrentRevision  string `json:"currentRevision,omitempty"`
	UpdatingRevision string `json:"updatingRevision,omitempty"`
	Image            string `json:"image,omitempty"`
	Status           string `json:"status"`
	UpdatedAt        string `json:"updatedAt,omitempty"`
	FailureReason    string `json:"failureReason,omitempty"`
}

type aifarRuntimePod struct {
	InstanceID    string  `json:"instanceId"`
	ServiceName   string  `json:"serviceName"`
	PodID         string  `json:"podId,omitempty"`
	ContainerName string  `json:"containerName"`
	Revision      string  `json:"revision,omitempty"`
	Image         string  `json:"image,omitempty"`
	Port          int     `json:"port,omitempty"`
	Status        string  `json:"status"`
	Ready         bool    `json:"ready"`
	CPUPercent    float64 `json:"cpuPercent,omitempty"`
	MemoryPercent float64 `json:"memoryPercent,omitempty"`
	MemoryUsage   string  `json:"memoryUsage,omitempty"`
	CreatedAt     string  `json:"createdAt,omitempty"`
	UpdatedAt     string  `json:"updatedAt,omitempty"`
}

type aifarRuntimeService struct {
	InstanceID      string  `json:"instanceId"`
	ServiceName     string  `json:"serviceName"`
	ProxyName       string  `json:"proxyName,omitempty"`
	DesiredReplicas int     `json:"desiredReplicas"`
	ReadyReplicas   int     `json:"readyReplicas"`
	ActiveEndpoints int     `json:"activeEndpoints"`
	CurrentRevision string  `json:"currentRevision,omitempty"`
	Image           string  `json:"image,omitempty"`
	Status          string  `json:"status"`
	CPUPercent      float64 `json:"cpuPercent,omitempty"`
	MemoryPercent   float64 `json:"memoryPercent,omitempty"`
	FailureReason   string  `json:"failureReason,omitempty"`
}

type aifarRuntimeIngress struct {
	InstanceID   string `json:"instanceId"`
	Container    string `json:"container,omitempty"`
	Status       string `json:"status"`
	GatewayPort  int    `json:"gatewayPort,omitempty"`
	WebPort      int    `json:"webPort,omitempty"`
	GatewayRoute string `json:"gatewayRoute,omitempty"`
	WebRoute     string `json:"webRoute,omitempty"`
	Error        string `json:"error,omitempty"`
}

type aifarRuntimeActionRequest struct {
	InstanceID string `json:"instanceId"`
	Reason     string `json:"reason"`
}

func (a *API) aifarRuntime(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	server, useServer, err := a.dockerServerFromRequest(r)
	if err != nil {
		respond(w, nil, err)
		return
	}
	if !useServer {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(lang, "api.dockerTargetRequired"), nil)
		return
	}
	response, err := a.buildAIFARRuntime(r.Context(), server)
	respond(w, response, err)
}

func (a *API) aifarRuntimeReconcile(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	_, instance, ok := a.resolveAIFARRuntimeActionTarget(w, r)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	reconcile, ok := module.(registry.RuntimeReconcileModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_RECONCILE_UNSUPPORTED", "AIFAR runtime reconcile is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	task, err := a.tasks.StartWithLanguage("aifar.reconcile", instance.ID, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("reconciling AIFAR runtime for instance %s", current.ID)
		return reconcile.ReconcileRuntime(ctx, registry.RuntimeReconcileRequest{
			Instance: current,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Reason:   "manual container runtime reconcile",
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.reconcile", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) aifarRuntimeScaleOut(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	service := strings.TrimSpace(chi.URLParam(r, "service"))
	if service == "" {
		writeError(w, http.StatusBadRequest, "AIFAR_SERVICE_REQUIRED", "AIFAR service is required", nil)
		return
	}
	_, instance, ok := a.resolveAIFARRuntimeActionTarget(w, r)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	scaleOut, ok := module.(registry.ServiceScaleOutModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_SCALE_OUT_UNSUPPORTED", "AIFAR service scale-out is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	target := instance.ID + ":" + service
	task, err := a.tasks.StartWithLanguage("aifar.scale.out", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("scaling out AIFAR service %s for instance %s", service, current.ID)
		return scaleOut.ScaleOutService(ctx, registry.ServiceScaleOutRequest{
			Instance:    current,
			Server:      server,
			Language:    lang,
			Actor:       actor,
			ServiceName: service,
			Reason:      "manual container runtime scale-out",
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.scale.out", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) resolveAIFARRuntimeActionTarget(w http.ResponseWriter, r *http.Request) (store.Server, store.AppInstance, bool) {
	lang := languageFromRequest(r)
	server, useServer, err := a.dockerServerFromRequest(r)
	if err != nil {
		respond(w, nil, err)
		return store.Server{}, store.AppInstance{}, false
	}
	if !useServer {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(lang, "api.dockerTargetRequired"), nil)
		return store.Server{}, store.AppInstance{}, false
	}
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return store.Server{}, store.AppInstance{}, false
		}
	}
	instance, err := a.findAIFARInstanceForRuntimeAction(server.ID, req.InstanceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "AIFAR_INSTANCE_REQUIRED", err.Error(), nil)
		return store.Server{}, store.AppInstance{}, false
	}
	metadata := runtimeMetadata(instance.Metadata)
	if strings.TrimSpace(runtimeString(metadata, "orchestrationModel", "")) != aifarK8sLikeModel {
		writeError(w, http.StatusConflict, "AIFAR_LEGACY_RUNTIME", "legacy AIFAR orchestration model does not support this runtime action", map[string]any{"instanceId": instance.ID})
		return store.Server{}, store.AppInstance{}, false
	}
	agent := a.collectAIFARAgentStatus(r.Context(), server)
	if agent.Status != "running" {
		writeError(w, http.StatusConflict, "AIFAR_AGENT_REQUIRED", "aifar-agent is required before running this runtime action", map[string]any{"status": agent.Status, "error": agent.Error})
		return store.Server{}, store.AppInstance{}, false
	}
	return server, instance, true
}

func (a *API) findAIFARInstanceForRuntimeAction(serverID, requestedID string) (store.AppInstance, error) {
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return store.AppInstance{}, err
	}
	candidates := make([]store.AppInstance, 0)
	for _, instance := range instances {
		if instance.App != "aifar" || instance.ServerID != serverID {
			continue
		}
		if requestedID != "" {
			if instance.ID == requestedID {
				return instance, nil
			}
			continue
		}
		if strings.TrimSpace(runtimeString(runtimeMetadata(instance.Metadata), "orchestrationModel", "")) == aifarK8sLikeModel {
			candidates = append(candidates, instance)
		}
	}
	if requestedID != "" {
		return store.AppInstance{}, fmt.Errorf("AIFAR instance %s was not found on this server", requestedID)
	}
	if len(candidates) == 1 {
		return candidates[0], nil
	}
	if len(candidates) == 0 {
		return store.AppInstance{}, fmt.Errorf("no k8s-like AIFAR instance was found on this server")
	}
	return store.AppInstance{}, fmt.Errorf("multiple k8s-like AIFAR instances were found; instanceId is required")
}

func (a *API) buildAIFARRuntime(ctx context.Context, server store.Server) (aifarRuntimeResponse, error) {
	response := aifarRuntimeResponse{
		ServerID:      server.ID,
		RuntimeStatus: "ready",
		Agent:         a.collectAIFARAgentStatus(ctx, server),
	}
	if response.Agent.Status != "running" {
		response.RuntimeStatus = "degraded"
		response.Warnings = append(response.Warnings, "aifar-agent is not available; runtime data is degraded")
	}
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return response, err
	}
	containers, err := adapter.DockerContainersForServer(ctx, server)
	if err != nil {
		response.RuntimeStatus = "degraded"
		response.Warnings = append(response.Warnings, "failed to read Docker containers: "+err.Error())
	}
	containersByName := mapContainersByName(containers)
	statsByName := map[string]adapter.DockerContainerStat{}
	if names := aifarPodContainerNames(containers); len(names) > 0 {
		if stats, err := adapter.DockerContainerStatsForServer(ctx, server, names); err == nil {
			statsByName = mapStatsByName(stats)
		} else {
			response.Warnings = append(response.Warnings, "failed to read Docker stats: "+err.Error())
		}
	}
	for _, instance := range instances {
		if instance.App != "aifar" || instance.ServerID != server.ID {
			continue
		}
		a.appendAIFARInstanceRuntime(&response, instance, containers, containersByName, statsByName)
	}
	if len(response.Instances) == 0 {
		response.RuntimeStatus = degradedIfReady(response.RuntimeStatus)
		response.Warnings = append(response.Warnings, "no AIFAR instance was found on this server")
	}
	sortRuntimeResponse(&response)
	return response, nil
}

func (a *API) appendAIFARInstanceRuntime(response *aifarRuntimeResponse, instance store.AppInstance, containers []adapter.DockerContainer, containersByName map[string]adapter.DockerContainer, statsByName map[string]adapter.DockerContainerStat) {
	metadata := runtimeMetadata(instance.Metadata)
	installRoot := normalizeRuntimeInstallRoot(runtimeString(metadata, "installRoot", ""))
	model := strings.TrimSpace(runtimeString(metadata, "orchestrationModel", ""))
	legacy := model != aifarK8sLikeModel
	response.Instances = append(response.Instances, aifarRuntimeInstance{
		ID:                 instance.ID,
		Version:            instance.Version,
		Status:             instance.Status,
		OrchestrationModel: model,
		Legacy:             legacy,
		InstallRoot:        installRoot,
		Endpoint:           runtimeString(metadata, "endpoint", ""),
		GatewayEndpoint:    runtimeString(metadata, "gatewayEndpoint", ""),
	})
	if legacy {
		response.RuntimeStatus = degradedIfReady(response.RuntimeStatus)
		response.Warnings = append(response.Warnings, "legacy AIFAR instance "+instance.ID+" does not support runtime actions")
		return
	}
	deployments, _ := a.store.ListAIFARDeployments(instance.ID)
	replicasets, _ := a.store.ListAIFARReplicaSets(instance.ID)
	pods, _ := a.store.ListAIFARPods(instance.ID)
	endpoints, _ := a.store.ListAIFARServiceEndpoints(instance.ID)
	if len(pods) == 0 {
		pods = fallbackAIFARPodsFromContainers(instance.ID, installRoot, containers)
	}
	replicaImage := latestReplicaImages(replicasets)
	readyByService := readyEndpointCounts(endpoints)
	podsByService := map[string][]aifarRuntimePod{}
	for _, pod := range pods {
		row, ok := containersByName[pod.ContainerName]
		stat := statsByName[pod.ContainerName]
		status, ready := runtimePodStatus(pod, row, ok)
		image := row.Image
		if image == "" {
			image = replicaImage[pod.ServiceName]
		}
		item := aifarRuntimePod{
			InstanceID:    instance.ID,
			ServiceName:   pod.ServiceName,
			PodID:         pod.PodID,
			ContainerName: pod.ContainerName,
			Revision:      pod.Revision,
			Image:         image,
			Port:          pod.Port,
			Status:        status,
			Ready:         ready,
			CPUPercent:    stat.CPUPerc,
			MemoryPercent: stat.MemPerc,
			MemoryUsage:   stat.MemUsage,
			CreatedAt:     pod.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
			UpdatedAt:     pod.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
		}
		response.Pods = append(response.Pods, item)
		podsByService[pod.ServiceName] = append(podsByService[pod.ServiceName], item)
	}
	seenServices := map[string]bool{}
	for _, deployment := range deployments {
		seenServices[deployment.ServiceName] = true
		image := replicaImage[deployment.ServiceName]
		status := runtimeServiceStatus(deployment, readyByService[deployment.ServiceName])
		response.Deployments = append(response.Deployments, aifarRuntimeDeployment{
			InstanceID:       instance.ID,
			ServiceName:      deployment.ServiceName,
			DesiredReplicas:  deployment.DesiredReplicas,
			ReadyReplicas:    readyByService[deployment.ServiceName],
			CurrentRevision:  deployment.CurrentRevision,
			UpdatingRevision: deployment.UpdatingRevision,
			Image:            image,
			Status:           status,
			UpdatedAt:        deployment.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			FailureReason:    runtimeString(runtimeMetadata(deployment.MetadataJSON), "failureReason", ""),
		})
		response.Services = append(response.Services, runtimeServiceFromDeployment(instance.ID, deployment, podsByService[deployment.ServiceName], readyByService[deployment.ServiceName], image, status))
	}
	for service, servicePods := range podsByService {
		if seenServices[service] {
			continue
		}
		response.Services = append(response.Services, runtimeServiceFromPods(instance.ID, service, servicePods, readyByService[service], replicaImage[service]))
	}
	response.Ingress = append(response.Ingress, runtimeIngressFromMetadata(instance.ID, metadata, containersByName))
}

func (a *API) collectAIFARAgentStatus(ctx context.Context, server store.Server) aifarRuntimeAgent {
	if strings.TrimSpace(server.Username) == "" || (strings.TrimSpace(server.Password) == "" && strings.TrimSpace(server.PrivateKey) == "") {
		return aifarRuntimeAgent{Status: "missing", Error: "ssh credential is not available"}
	}
	command := "command -v aifar-agent >/dev/null 2>&1 || exit 127; aifar-agent status 2>/dev/null || aifar-agent health"
	result, err := adapter.RunSSH(ctx, server, command)
	if err != nil {
		return aifarRuntimeAgent{Status: "missing", Error: strings.TrimSpace(result.Stderr)}
	}
	var parsed struct {
		Status   string   `json:"status"`
		Version  string   `json:"version"`
		Features []string `json:"features"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &parsed); err != nil {
		return aifarRuntimeAgent{Status: "degraded", Error: err.Error()}
	}
	status := strings.TrimSpace(parsed.Status)
	if status == "ok" {
		status = "running"
	}
	if status == "" {
		status = "running"
	}
	return aifarRuntimeAgent{Status: status, Version: parsed.Version, Mode: "systemd", Features: parsed.Features}
}

func runtimeServiceFromDeployment(instanceID string, deployment store.AIFARDeployment, pods []aifarRuntimePod, ready int, image, status string) aifarRuntimeService {
	cpu, mem := averagePodLoad(pods)
	return aifarRuntimeService{
		InstanceID:      instanceID,
		ServiceName:     deployment.ServiceName,
		ProxyName:       "aifar-svc-admin-" + deployment.ServiceName,
		DesiredReplicas: deployment.DesiredReplicas,
		ReadyReplicas:   ready,
		ActiveEndpoints: ready,
		CurrentRevision: deployment.CurrentRevision,
		Image:           image,
		Status:          status,
		CPUPercent:      cpu,
		MemoryPercent:   mem,
		FailureReason:   runtimeString(runtimeMetadata(deployment.MetadataJSON), "failureReason", ""),
	}
}

func runtimeServiceFromPods(instanceID, service string, pods []aifarRuntimePod, ready int, image string) aifarRuntimeService {
	cpu, mem := averagePodLoad(pods)
	desired := len(pods)
	status := "ready"
	if ready == 0 {
		status = "no-endpoints"
	} else if ready < desired {
		status = "degraded"
	}
	revision := ""
	if len(pods) > 0 {
		revision = pods[0].Revision
		if image == "" {
			image = pods[0].Image
		}
	}
	return aifarRuntimeService{
		InstanceID:      instanceID,
		ServiceName:     service,
		ProxyName:       "aifar-svc-admin-" + service,
		DesiredReplicas: desired,
		ReadyReplicas:   ready,
		ActiveEndpoints: ready,
		CurrentRevision: revision,
		Image:           image,
		Status:          status,
		CPUPercent:      cpu,
		MemoryPercent:   mem,
	}
}

func runtimeIngressFromMetadata(instanceID string, metadata map[string]any, containersByName map[string]adapter.DockerContainer) aifarRuntimeIngress {
	container := runtimeString(metadata, "ingressContainer", "aifar-admin-ingress")
	status := "missing"
	if row, ok := containersByName[container]; ok {
		status = "running"
		if strings.EqualFold(strings.TrimSpace(row.State), "exited") || strings.Contains(strings.ToLower(row.Status), "unhealthy") {
			status = "failed"
		}
	}
	return aifarRuntimeIngress{
		InstanceID:   instanceID,
		Container:    container,
		Status:       status,
		GatewayPort:  runtimeInt(metadata, "gatewayPort", 38000),
		WebPort:      runtimeInt(metadata, "webPort", 8080),
		GatewayRoute: runtimeString(metadata, "gatewayEndpoint", ""),
		WebRoute:     runtimeString(metadata, "endpoint", ""),
	}
}

func runtimeServiceStatus(deployment store.AIFARDeployment, ready int) string {
	if strings.EqualFold(deployment.Status, "failed") {
		return "failed"
	}
	if strings.TrimSpace(deployment.UpdatingRevision) != "" {
		return "rolling"
	}
	if ready == 0 {
		return "no-endpoints"
	}
	if deployment.DesiredReplicas > 0 && ready < deployment.DesiredReplicas {
		return "degraded"
	}
	return "ready"
}

func runtimePodStatus(pod store.AIFARPod, row adapter.DockerContainer, found bool) (string, bool) {
	if !found {
		return "stale", false
	}
	state := strings.ToLower(strings.TrimSpace(row.State))
	detail := strings.ToLower(strings.TrimSpace(row.Status))
	if detail == "" {
		detail = state
	}
	switch {
	case state == "running" && strings.Contains(detail, "unhealthy"):
		return "unhealthy", false
	case state == "running" && (strings.Contains(detail, "health: starting") || strings.Contains(detail, "starting")):
		return "starting", false
	case state == "running" && pod.Ready:
		return "ready", true
	case state == "running":
		return "running", pod.Ready
	case state == "restarting" || state == "created":
		return "starting", false
	default:
		return "unhealthy", false
	}
}

func fallbackAIFARPodsFromContainers(instanceID, installRoot string, containers []adapter.DockerContainer) []store.AIFARPod {
	out := []store.AIFARPod{}
	for _, row := range containers {
		labels := row.Labels
		if labels["aifar.app"] != "aifar" || labels["aifar.component"] != "pod" {
			continue
		}
		if installRoot != "" && normalizeRuntimeInstallRoot(labels["aifar.install-root"]) != installRoot {
			continue
		}
		out = append(out, store.AIFARPod{
			InstanceID:    instanceID,
			ServiceName:   labels["aifar.service"],
			Revision:      firstRuntimeValue(labels["aifar.revision"], labels["aifar.release"]),
			PodID:         firstRuntimeValue(labels["aifar.pod"], labels["aifar.replica"]),
			ContainerName: row.Name,
			Status:        row.State,
			Ready:         strings.EqualFold(row.State, "running") && !strings.Contains(strings.ToLower(row.Status), "unhealthy"),
		})
	}
	return out
}

func latestReplicaImages(items []store.AIFARReplicaSet) map[string]string {
	out := map[string]string{}
	for _, item := range items {
		if out[item.ServiceName] == "" {
			out[item.ServiceName] = item.Image
		}
	}
	return out
}

func readyEndpointCounts(endpoints []store.AIFARServiceEndpoint) map[string]int {
	out := map[string]int{}
	for _, endpoint := range endpoints {
		if endpoint.Ready && strings.EqualFold(endpoint.State, "active") {
			out[endpoint.ServiceName]++
		}
	}
	return out
}

func averagePodLoad(pods []aifarRuntimePod) (float64, float64) {
	if len(pods) == 0 {
		return 0, 0
	}
	var cpu, mem float64
	var cpuN, memN int
	for _, pod := range pods {
		if pod.CPUPercent > 0 {
			cpu += pod.CPUPercent
			cpuN++
		}
		if pod.MemoryPercent > 0 {
			mem += pod.MemoryPercent
			memN++
		}
	}
	if cpuN > 0 {
		cpu = cpu / float64(cpuN)
	}
	if memN > 0 {
		mem = mem / float64(memN)
	}
	return cpu, mem
}

func aifarPodContainerNames(containers []adapter.DockerContainer) []string {
	out := []string{}
	for _, row := range containers {
		if row.Labels["aifar.app"] == "aifar" && row.Labels["aifar.component"] == "pod" {
			out = append(out, row.Name)
		}
	}
	return out
}

func mapContainersByName(containers []adapter.DockerContainer) map[string]adapter.DockerContainer {
	out := make(map[string]adapter.DockerContainer, len(containers))
	for _, row := range containers {
		if strings.TrimSpace(row.Name) != "" {
			out[row.Name] = row
		}
	}
	return out
}

func mapStatsByName(stats []adapter.DockerContainerStat) map[string]adapter.DockerContainerStat {
	out := map[string]adapter.DockerContainerStat{}
	for _, item := range stats {
		if strings.TrimSpace(item.Name) != "" {
			out[item.Name] = item
		}
		if strings.TrimSpace(item.ID) != "" {
			out[item.ID] = item
		}
	}
	return out
}

func sortRuntimeResponse(response *aifarRuntimeResponse) {
	sort.Slice(response.Instances, func(i, j int) bool { return response.Instances[i].ID < response.Instances[j].ID })
	sort.Slice(response.Deployments, func(i, j int) bool {
		if response.Deployments[i].InstanceID == response.Deployments[j].InstanceID {
			return response.Deployments[i].ServiceName < response.Deployments[j].ServiceName
		}
		return response.Deployments[i].InstanceID < response.Deployments[j].InstanceID
	})
	sort.Slice(response.Pods, func(i, j int) bool {
		if response.Pods[i].ServiceName == response.Pods[j].ServiceName {
			return response.Pods[i].ContainerName < response.Pods[j].ContainerName
		}
		return response.Pods[i].ServiceName < response.Pods[j].ServiceName
	})
	sort.Slice(response.Services, func(i, j int) bool {
		if response.Services[i].InstanceID == response.Services[j].InstanceID {
			return response.Services[i].ServiceName < response.Services[j].ServiceName
		}
		return response.Services[i].InstanceID < response.Services[j].InstanceID
	})
}

func runtimeMetadata(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func runtimeString(metadata map[string]any, key, fallback string) string {
	if value, ok := metadata[key]; ok {
		switch v := value.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
	}
	return fallback
}

func runtimeInt(metadata map[string]any, key string, fallback int) int {
	if value, ok := metadata[key]; ok {
		switch v := value.(type) {
		case float64:
			return int(v)
		case int:
			return v
		case string:
			var n int
			if _, err := fmt.Sscanf(strings.TrimSpace(v), "%d", &n); err == nil {
				return n
			}
		}
	}
	return fallback
}

func normalizeRuntimeInstallRoot(value string) string {
	value = strings.TrimSpace(value)
	for len(value) > 1 && strings.HasSuffix(value, "/") {
		value = strings.TrimSuffix(value, "/")
	}
	return value
}

func firstRuntimeValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func degradedIfReady(status string) string {
	if status == "ready" {
		return "degraded"
	}
	return status
}
