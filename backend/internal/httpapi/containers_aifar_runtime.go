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

const aifarK8sLikeModel = "agent-runtime-v2"

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
	ID                 string         `json:"id"`
	Version            string         `json:"version"`
	Status             string         `json:"status"`
	OrchestrationModel string         `json:"orchestrationModel,omitempty"`
	Legacy             bool           `json:"legacy"`
	InstallRoot        string         `json:"installRoot,omitempty"`
	Endpoint           string         `json:"endpoint,omitempty"`
	GatewayEndpoint    string         `json:"gatewayEndpoint,omitempty"`
	RuntimeConfig      map[string]any `json:"runtimeConfig,omitempty"`
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

type aifarRuntimeServiceInstallRequest struct {
	InstanceID string   `json:"instanceId"`
	Services   []string `json:"services"`
	Reason     string   `json:"reason"`
}

type aifarRuntimeConfigApplyRequest struct {
	InstanceID string                                  `json:"instanceId"`
	Reason     string                                  `json:"reason"`
	Global     registry.RuntimeConfigValues            `json:"global"`
	Services   map[string]registry.RuntimeConfigValues `json:"services,omitempty"`
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

func (a *API) aifarRuntimeCleanupStale(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	_, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, req.InstanceID, false)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	cleanup, ok := module.(registry.RuntimeCleanupModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_CLEANUP_UNSUPPORTED", "AIFAR runtime cleanup is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	task, err := a.tasks.StartWithLanguage("aifar.runtime.cleanup", instance.ID, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("cleaning stale AIFAR runtime Pod records for instance %s", current.ID)
		return cleanup.CleanupRuntimeStalePods(ctx, registry.RuntimeCleanupRequest{
			Instance: current,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Reason:   strings.TrimSpace(req.Reason),
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.runtime.cleanup", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) aifarRuntimeUninstallAgent(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	_, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, req.InstanceID, false)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	uninstaller, ok := module.(registry.RuntimeAgentUninstallModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_AGENT_UNINSTALL_UNSUPPORTED", "AIFAR agent uninstall is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	task, err := a.tasks.StartWithLanguage("aifar.agent.uninstall", instance.ID, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("uninstalling aifar-agent for instance %s", current.ID)
		return uninstaller.UninstallRuntimeAgent(ctx, registry.RuntimeAgentUninstallRequest{
			Instance: current,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Reason:   strings.TrimSpace(req.Reason),
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.agent.uninstall", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) aifarRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeConfigApplyRequest{}
	if !decode(w, r, &req) {
		return
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstance(w, r, req.InstanceID)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	configModule, ok := module.(registry.RuntimeConfigModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_CONFIG_UNSUPPORTED", "AIFAR runtime config is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	configReq := registry.RuntimeConfigRequest{
		Instance: instance,
		Server:   server,
		Language: lang,
		Actor:    actor,
		Reason:   strings.TrimSpace(req.Reason),
		Config: registry.RuntimeConfigPayload{
			Global:   req.Global,
			Services: req.Services,
		},
	}
	if err := configModule.ValidateRuntimeConfig(r.Context(), configReq); err != nil {
		writeError(w, http.StatusBadRequest, "AIFAR_RUNTIME_CONFIG_INVALID", err.Error(), nil)
		return
	}
	task, err := a.tasks.StartWithLanguage("aifar.runtime.config", instance.ID, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("applying AIFAR runtime config for instance %s", current.ID)
		return configModule.ApplyRuntimeConfig(ctx, registry.RuntimeConfigRequest{
			Instance: current,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Reason:   strings.TrimSpace(req.Reason),
			Config: registry.RuntimeConfigPayload{
				Global:   req.Global,
				Services: req.Services,
			},
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.runtime.config", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) aifarRuntimeInstallServices(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeServiceInstallRequest{}
	if !decode(w, r, &req) {
		return
	}
	if len(req.Services) == 0 {
		writeError(w, http.StatusBadRequest, "AIFAR_SERVICES_REQUIRED", "select at least one AIFAR service", nil)
		return
	}
	_, instance, ok := a.resolveAIFARRuntimeActionTargetForInstance(w, r, req.InstanceID)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	installer, ok := module.(registry.ServiceInstallModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_SERVICE_INSTALL_UNSUPPORTED", "AIFAR service installation is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	services := append([]string(nil), req.Services...)
	target := instance.ID + ":" + strings.Join(services, ",")
	task, err := a.tasks.StartWithLanguage("aifar.services.install", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("installing AIFAR services %s for instance %s", strings.Join(services, ", "), current.ID)
		return installer.InstallServices(ctx, registry.ServiceInstallRequest{
			Instance: current,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Services: services,
			Reason:   strings.TrimSpace(req.Reason),
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.services.install", target, "running", task.ID)
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
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return store.Server{}, store.AppInstance{}, false
		}
	}
	return a.resolveAIFARRuntimeActionTargetForInstance(w, r, req.InstanceID)
}

func (a *API) resolveAIFARRuntimeActionTargetForInstance(w http.ResponseWriter, r *http.Request, requestedID string) (store.Server, store.AppInstance, bool) {
	return a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, requestedID, true)
}

func (a *API) resolveAIFARRuntimeActionTargetForInstanceWithAgent(w http.ResponseWriter, r *http.Request, requestedID string, requireAgent bool) (store.Server, store.AppInstance, bool) {
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
	instance, err := a.findAIFARInstanceForRuntimeAction(server.ID, strings.TrimSpace(requestedID))
	if err != nil {
		writeError(w, http.StatusBadRequest, "AIFAR_INSTANCE_REQUIRED", err.Error(), nil)
		return store.Server{}, store.AppInstance{}, false
	}
	metadata := runtimeMetadata(instance.Metadata)
	if strings.TrimSpace(runtimeString(metadata, "orchestrationModel", "")) != aifarK8sLikeModel {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_REINSTALL_REQUIRED", "legacy AIFAR orchestration model does not support this runtime action; reinstall with agent-runtime-v2", map[string]any{"instanceId": instance.ID})
		return store.Server{}, store.AppInstance{}, false
	}
	if requireAgent {
		agent := a.collectAIFARAgentStatus(r.Context(), server)
		if agent.Status != "running" {
			writeError(w, http.StatusConflict, "AIFAR_AGENT_REQUIRED", "aifar-agent is required before running this runtime action", map[string]any{"status": agent.Status, "error": agent.Error})
			return store.Server{}, store.AppInstance{}, false
		}
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
		return store.AppInstance{}, fmt.Errorf("no agent-runtime-v2 AIFAR instance was found on this server")
	}
	return store.AppInstance{}, fmt.Errorf("multiple agent-runtime-v2 AIFAR instances were found; instanceId is required")
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
		a.appendAIFARInstanceRuntime(&response, instance, containersByName, statsByName)
	}
	if len(response.Instances) == 0 {
		response.RuntimeStatus = degradedIfReady(response.RuntimeStatus)
		response.Warnings = append(response.Warnings, "no AIFAR instance was found on this server")
	}
	sortRuntimeResponse(&response)
	return response, nil
}

func (a *API) appendAIFARInstanceRuntime(response *aifarRuntimeResponse, instance store.AppInstance, containersByName map[string]adapter.DockerContainer, statsByName map[string]adapter.DockerContainerStat) {
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
		RuntimeConfig:      runtimeConfigFromRuntimeMetadata(metadata),
	})
	if legacy {
		response.RuntimeStatus = degradedIfReady(response.RuntimeStatus)
		response.Warnings = append(response.Warnings, "legacy AIFAR instance "+instance.ID+" requires reinstall with agent-runtime-v2")
		return
	}
	deployments, _ := a.store.ListAIFARDeployments(instance.ID)
	replicasets, _ := a.store.ListAIFARReplicaSets(instance.ID)
	pods, _ := a.store.ListAIFARPods(instance.ID)
	endpoints, _ := a.store.ListAIFARServiceEndpoints(instance.ID)
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
	command := "command -v aifar-agent >/dev/null 2>&1 || exit 127; aifar-agent status"
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
		ProxyName:       "aifar-agent:" + deployment.ServiceName,
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
		ProxyName:       "aifar-agent:" + service,
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
	_ = containersByName
	container := runtimeString(metadata, "runtimeService", "aifar-agent")
	status := "running"
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

func runtimeConfigFromRuntimeMetadata(metadata map[string]any) map[string]any {
	if raw, ok := metadata["runtimeConfig"].(map[string]any); ok && len(raw) > 0 {
		return raw
	}
	return map[string]any{
		"configVersion":   1,
		"appliedVersion":  1,
		"lastApplyStatus": "applied",
		"global": map[string]any{
			"appCPUs":                 runtimeString(metadata, "appCPUs", "2.0"),
			"appMemoryLimit":          runtimeString(metadata, "appMemoryLimit", "2GB"),
			"jvmInitialRAMPercentage": 20,
			"jvmMaxRAMPercentage":     70,
		},
		"services": map[string]any{},
	}
}

func normalizeRuntimeInstallRoot(value string) string {
	value = strings.TrimSpace(value)
	for len(value) > 1 && strings.HasSuffix(value, "/") {
		value = strings.TrimSuffix(value, "/")
	}
	return value
}

func degradedIfReady(status string) string {
	if status == "ready" {
		return "degraded"
	}
	return status
}
