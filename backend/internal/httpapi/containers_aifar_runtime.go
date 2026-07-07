package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

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

type aifarRuntimeBuildOptions struct {
	IncludePods  bool
	IncludeStats bool
}

type aifarRuntimeAgent struct {
	Status    string                     `json:"status"`
	Version   string                     `json:"version,omitempty"`
	Mode      string                     `json:"mode,omitempty"`
	Error     string                     `json:"error,omitempty"`
	Listeners []int                      `json:"listeners,omitempty"`
	Features  []string                   `json:"features,omitempty"`
	Instances []aifarAgentInstanceStatus `json:"instances,omitempty"`
}

type aifarAgentInstanceStatus struct {
	InstanceID       string                       `json:"instanceId"`
	DeploymentStatus []aifarAgentDeploymentStatus `json:"deploymentStatus"`
	ServiceStatus    []aifarAgentServiceStatus    `json:"serviceStatus"`
}

type aifarAgentDeploymentStatus struct {
	InstanceID        string `json:"instanceId"`
	ServiceName       string `json:"serviceName"`
	DeploymentName    string `json:"deploymentName,omitempty"`
	PodRevision       string `json:"podRevision,omitempty"`
	Image             string `json:"image,omitempty"`
	DesiredReplicas   int    `json:"desiredReplicas"`
	CurrentReplicas   int    `json:"currentReplicas"`
	ReadyReplicas     int    `json:"readyReplicas"`
	UpdatedReplicas   int    `json:"updatedReplicas"`
	AvailableReplicas int    `json:"availableReplicas"`
	Status            string `json:"status"`
	LastReconcileAt   string `json:"lastReconcileAt,omitempty"`
	LastError         string `json:"lastError,omitempty"`
}

type aifarAgentServiceStatus struct {
	InstanceID            string `json:"instanceId"`
	ServiceName           string `json:"serviceName"`
	AppName               string `json:"appName,omitempty"`
	EndpointCount         int    `json:"endpointCount"`
	ReadyEndpointCount    int    `json:"readyEndpointCount"`
	NacosRegistered       bool   `json:"nacosRegistered,omitempty"`
	NacosReady            bool   `json:"nacosReady,omitempty"`
	LastNacosHeartbeatAt  string `json:"lastNacosHeartbeatAt,omitempty"`
	LastNacosError        string `json:"lastNacosError,omitempty"`
	Status                string `json:"status"`
	LastEndpointRefreshAt string `json:"lastEndpointRefreshAt,omitempty"`
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
	InstanceID          string `json:"instanceId"`
	DeploymentName      string `json:"deploymentName"`
	ServiceName         string `json:"serviceName"`
	AppName             string `json:"appName"`
	DesiredReplicas     int    `json:"desiredReplicas"`
	CurrentReplicas     int    `json:"currentReplicas,omitempty"`
	ReadyReplicas       int    `json:"readyReplicas"`
	UpdatedReplicas     int    `json:"updatedReplicas,omitempty"`
	AvailableReplicas   int    `json:"availableReplicas,omitempty"`
	PodRevision         string `json:"podRevision,omitempty"`
	UpdatingPodRevision string `json:"updatingPodRevision,omitempty"`
	Image               string `json:"image,omitempty"`
	Status              string `json:"status"`
	UpdatedAt           string `json:"updatedAt,omitempty"`
	FailureReason       string `json:"failureReason,omitempty"`
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
	InstanceID         string  `json:"instanceId"`
	ServiceName        string  `json:"serviceName"`
	AppName            string  `json:"appName"`
	ProxyName          string  `json:"proxyName,omitempty"`
	DesiredReplicas    int     `json:"desiredReplicas"`
	ReadyReplicas      int     `json:"readyReplicas"`
	ActiveEndpoints    int     `json:"activeEndpoints"`
	EndpointCount      int     `json:"endpointCount,omitempty"`
	ReadyEndpointCount int     `json:"readyEndpointCount,omitempty"`
	Image              string  `json:"image,omitempty"`
	Status             string  `json:"status"`
	RolloutStatus      string  `json:"rolloutStatus,omitempty"`
	NacosRegistered    bool    `json:"nacosRegistered,omitempty"`
	NacosReady         bool    `json:"nacosReady,omitempty"`
	LastNacosError     string  `json:"lastNacosError,omitempty"`
	LastError          string  `json:"lastError,omitempty"`
	CPUPercent         float64 `json:"cpuPercent,omitempty"`
	MemoryPercent      float64 `json:"memoryPercent,omitempty"`
	FailureReason      string  `json:"failureReason,omitempty"`
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

type aifarRuntimeLogsResponse struct {
	ServerID   string               `json:"serverId"`
	InstanceID string               `json:"instanceId"`
	Service    string               `json:"service,omitempty"`
	Services   []string             `json:"services,omitempty"`
	PodsFilter []string             `json:"podsFilter,omitempty"`
	Tail       int                  `json:"tail"`
	BatchSize  int                  `json:"batchSize,omitempty"`
	Mode       string               `json:"mode,omitempty"`
	Pods       []aifarRuntimeLogPod `json:"pods"`
	Warnings   []string             `json:"warnings,omitempty"`
}

type aifarRuntimeLogPod struct {
	InstanceID      string   `json:"instanceId"`
	ServiceName     string   `json:"serviceName"`
	PodID           string   `json:"podId,omitempty"`
	ContainerName   string   `json:"containerName"`
	Revision        string   `json:"revision,omitempty"`
	Status          string   `json:"status,omitempty"`
	Ready           bool     `json:"ready"`
	Logs            []string `json:"logs"`
	LineCount       int      `json:"lineCount"`
	CollectionError string   `json:"collectionError,omitempty"`
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
	InstanceID     string                                  `json:"instanceId"`
	Reason         string                                  `json:"reason"`
	Global         registry.RuntimeConfigValues            `json:"global"`
	Services       map[string]registry.RuntimeConfigValues `json:"services,omitempty"`
	NacosEphemeral *bool                                   `json:"nacosEphemeral,omitempty"`
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
	response, err := a.buildAIFARRuntime(r.Context(), server, aifarRuntimeBuildOptions{
		IncludePods:  queryBool(r, "includePods", true),
		IncludeStats: queryBool(r, "includeStats", true),
	})
	respond(w, response, err)
}

func (a *API) aifarRuntimeLogs(w http.ResponseWriter, r *http.Request) {
	server, instance, query, ok := a.resolveAIFARRuntimeLogQuery(w, r)
	if !ok {
		return
	}
	response, err := a.collectAIFARRuntimeLogs(r.Context(), server, instance, query)
	respond(w, response, err)
}

func (a *API) aifarRuntimeLogsEvents(w http.ResponseWriter, r *http.Request) {
	server, instance, query, ok := a.resolveAIFARRuntimeLogQuery(w, r)
	if !ok {
		return
	}
	pods, warnings, err := a.runtimeLogPods(instance.ID, query)
	if err != nil {
		respond(w, nil, err)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	states := map[string]runtimeLogStreamState{}
	emitError := func(err error) {
		writeSSE(w, "runtime-logs-error", map[string]any{"message": err.Error()})
		if flusher != nil {
			flusher.Flush()
		}
	}
	snapshot, err := a.collectAIFARRuntimeLogsForPods(r.Context(), server, instance, query, pods, adapter.DockerLogOptions{Tail: query.Tail, Since: query.Since, Timestamps: true}, "snapshot")
	if err != nil {
		emitError(err)
	} else {
		snapshot.Warnings = append(snapshot.Warnings, warnings...)
		writeSSE(w, "runtime-logs-snapshot", snapshot)
		if flusher != nil {
			flusher.Flush()
		}
		initialSince := runtimeLogInitialSince(query.Since)
		for _, item := range snapshot.Pods {
			states[item.ContainerName] = runtimeLogStreamState{
				Since:           initialSince,
				LastFetchCounts: runtimeLogLineCounts(runtimeLogLinesSince(item.Logs, initialSince)),
			}
		}
	}
	emitBatch := func() {
		response := aifarRuntimeLogsResponse{
			ServerID:   server.ID,
			InstanceID: instance.ID,
			Service:    firstRuntimeValue(query.Services),
			Services:   query.Services,
			PodsFilter: query.PodSelectors,
			Tail:       query.Tail,
			BatchSize:  query.BatchSize,
			Mode:       "append",
			Pods:       []aifarRuntimeLogPod{},
			Warnings:   []string{},
		}
		hasContent := false
		for _, pod := range pods {
			state := states[pod.ContainerName]
			if state.Since.IsZero() {
				state.Since = runtimeLogInitialSince(query.Since)
			}
			startedAt := time.Now()
			logs, logErr := adapter.DockerContainerLogsForServerWithOptions(r.Context(), server, pod.ContainerName, adapter.DockerLogOptions{
				Tail:       query.BatchSize,
				Since:      state.Since,
				Timestamps: true,
			})
			newLogs, _ := runtimeLogNewLines(logs, state.LastFetchCounts)
			nextSince := startedAt.Add(-3 * time.Second)
			if !query.Since.IsZero() && nextSince.Before(query.Since) {
				nextSince = query.Since
			}
			states[pod.ContainerName] = runtimeLogStreamState{
				Since:           nextSince,
				LastFetchCounts: runtimeLogLineCounts(runtimeLogLinesSince(logs, nextSince)),
			}
			if len(newLogs) == 0 && logErr == nil {
				continue
			}
			item := runtimeLogPodResponse(pod, newLogs)
			if logErr != nil {
				item.CollectionError = logErr.Error()
				response.Warnings = append(response.Warnings, pod.ContainerName+": "+logErr.Error())
			}
			response.Pods = append(response.Pods, item)
			hasContent = true
		}
		if hasContent {
			writeSSE(w, "runtime-logs-batch", response)
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			emitBatch()
		case <-heartbeat.C:
			writeSSE(w, "heartbeat", map[string]any{"time": time.Now().UTC()})
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

type aifarRuntimeLogQuery struct {
	Tail         int
	BatchSize    int
	Services     []string
	PodSelectors []string
	Since        time.Time
}

type runtimeLogStreamState struct {
	Since           time.Time
	LastFetchCounts map[string]int
}

func (a *API) resolveAIFARRuntimeLogQuery(w http.ResponseWriter, r *http.Request) (store.Server, store.AppInstance, aifarRuntimeLogQuery, bool) {
	lang := languageFromRequest(r)
	server, useServer, err := a.dockerServerFromRequest(r)
	if err != nil {
		respond(w, nil, err)
		return store.Server{}, store.AppInstance{}, aifarRuntimeLogQuery{}, false
	}
	if !useServer {
		writeError(w, http.StatusBadRequest, "DOCKER_TARGET_REQUIRED", i18n.Text(lang, "api.dockerTargetRequired"), nil)
		return store.Server{}, store.AppInstance{}, aifarRuntimeLogQuery{}, false
	}
	instanceID := strings.TrimSpace(r.URL.Query().Get("instanceId"))
	instance, err := a.findAIFARInstanceForRuntimeAction(server.ID, instanceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "AIFAR_INSTANCE_REQUIRED", err.Error(), nil)
		return store.Server{}, store.AppInstance{}, aifarRuntimeLogQuery{}, false
	}
	if strings.TrimSpace(runtimeString(runtimeMetadata(instance.Metadata), "orchestrationModel", "")) != aifarK8sLikeModel {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_REINSTALL_REQUIRED", "legacy AIFAR orchestration model does not support runtime log aggregation; reinstall with agent-runtime-v2", map[string]any{"instanceId": instance.ID})
		return store.Server{}, store.AppInstance{}, aifarRuntimeLogQuery{}, false
	}
	return server, instance, aifarRuntimeLogQuery{
		Tail:         boundedRuntimeLogTail(queryInt(r, "tail", 200)),
		BatchSize:    boundedRuntimeLogBatch(queryInt(r, "batch", 200)),
		Services:     runtimeQueryValues(r, "service", "services"),
		PodSelectors: runtimeQueryValues(r, "pod", "pods", "container", "containerName"),
		Since:        runtimeLogSinceFromRequest(r),
	}, true
}

func (a *API) collectAIFARRuntimeLogs(ctx context.Context, server store.Server, instance store.AppInstance, query aifarRuntimeLogQuery) (aifarRuntimeLogsResponse, error) {
	pods, warnings, err := a.runtimeLogPods(instance.ID, query)
	if err != nil {
		return aifarRuntimeLogsResponse{}, err
	}
	response, err := a.collectAIFARRuntimeLogsForPods(ctx, server, instance, query, pods, adapter.DockerLogOptions{Tail: query.Tail, Since: query.Since}, "")
	if err != nil {
		return response, err
	}
	response.Warnings = append(response.Warnings, warnings...)
	return response, nil
}

func (a *API) runtimeLogPods(instanceID string, query aifarRuntimeLogQuery) ([]store.AIFARPod, []string, error) {
	pods, err := a.store.ListAIFARPods(instanceID)
	if err != nil {
		return nil, nil, err
	}
	pods = filterRuntimeLogPods(pods, query.Services, query.PodSelectors)
	sort.Slice(pods, func(i, j int) bool {
		if pods[i].ServiceName == pods[j].ServiceName {
			return pods[i].ContainerName < pods[j].ContainerName
		}
		return pods[i].ServiceName < pods[j].ServiceName
	})
	warnings := []string{}
	if len(pods) > 32 {
		warnings = append(warnings, "runtime log query matched more than 32 pods; showing the first 32")
		pods = pods[:32]
	}
	return pods, warnings, nil
}

func (a *API) collectAIFARRuntimeLogsForPods(ctx context.Context, server store.Server, instance store.AppInstance, query aifarRuntimeLogQuery, pods []store.AIFARPod, options adapter.DockerLogOptions, mode string) (aifarRuntimeLogsResponse, error) {
	response := aifarRuntimeLogsResponse{
		ServerID:   server.ID,
		InstanceID: instance.ID,
		Service:    firstRuntimeValue(query.Services),
		Services:   query.Services,
		PodsFilter: query.PodSelectors,
		Tail:       query.Tail,
		BatchSize:  query.BatchSize,
		Mode:       mode,
		Pods:       []aifarRuntimeLogPod{},
	}
	for _, pod := range pods {
		logs, logErr := adapter.DockerContainerLogsForServerWithOptions(ctx, server, pod.ContainerName, options)
		item := runtimeLogPodResponse(pod, logs)
		if logErr != nil {
			item.CollectionError = logErr.Error()
			response.Warnings = append(response.Warnings, pod.ContainerName+": "+logErr.Error())
		}
		response.Pods = append(response.Pods, item)
	}
	return response, nil
}

func runtimeLogPodResponse(pod store.AIFARPod, logs []string) aifarRuntimeLogPod {
	return aifarRuntimeLogPod{
		InstanceID:    pod.InstanceID,
		ServiceName:   pod.ServiceName,
		PodID:         pod.PodID,
		ContainerName: pod.ContainerName,
		Revision:      cleanRuntimeText(pod.Revision),
		Status:        cleanRuntimeText(pod.Status),
		Ready:         pod.Ready,
		Logs:          logs,
		LineCount:     len(logs),
	}
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
			Global:         req.Global,
			Services:       req.Services,
			NacosEphemeral: req.NacosEphemeral,
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
				Global:         req.Global,
				Services:       req.Services,
				NacosEphemeral: req.NacosEphemeral,
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

func (a *API) aifarRuntimeScaleIn(w http.ResponseWriter, r *http.Request) {
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
	scaler, ok := module.(registry.ServiceScaleModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_SCALE_UNSUPPORTED", "AIFAR service scale is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	target := instance.ID + ":" + service
	task, err := a.tasks.StartWithLanguage("aifar.scale.in", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		currentDesired, err := a.currentAIFARServiceDesiredReplicas(current.ID, service)
		if err != nil {
			return err
		}
		if currentDesired <= 1 {
			return fmt.Errorf("AIFAR service %s has %d desired replicas; use offline to scale to 0", service, currentDesired)
		}
		nextReplicas := currentDesired - 1
		log.Info("scaling in AIFAR service %s for instance %s from %d to %d replicas", service, current.ID, currentDesired, nextReplicas)
		return scaler.ScaleService(ctx, registry.ServiceScaleRequest{
			Instance:    current,
			Server:      server,
			Language:    lang,
			Actor:       actor,
			ServiceName: service,
			Replicas:    nextReplicas,
			Reason:      "manual container runtime scale-in",
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.scale.in", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) aifarRuntimeOfflineService(w http.ResponseWriter, r *http.Request) {
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
	scaler, ok := module.(registry.ServiceScaleModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_SCALE_UNSUPPORTED", "AIFAR service scale is not supported", map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	target := instance.ID + ":" + service
	task, err := a.tasks.StartWithLanguage("aifar.scale.offline", target, actor, lang, func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info("offlining AIFAR service %s for instance %s", service, current.ID)
		return scaler.ScaleService(ctx, registry.ServiceScaleRequest{
			Instance:    current,
			Server:      server,
			Language:    lang,
			Actor:       actor,
			ServiceName: service,
			Replicas:    0,
			Reason:      "manual container runtime service offline",
		}, registry.RunContext{
			Log: log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if err == nil {
		a.audit(r, "aifar.scale.offline", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *API) currentAIFARServiceDesiredReplicas(instanceID, service string) (int, error) {
	deployments, err := a.store.ListAIFARDeployments(instanceID)
	if err != nil {
		return 0, err
	}
	service = cleanRuntimeText(service)
	for _, deployment := range deployments {
		if deployment.ServiceName == service {
			return deployment.DesiredReplicas, nil
		}
	}
	return 0, fmt.Errorf("AIFAR service %s deployment was not found", service)
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

func (a *API) buildAIFARRuntime(ctx context.Context, server store.Server, options aifarRuntimeBuildOptions) (aifarRuntimeResponse, error) {
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
	containers := []adapter.DockerContainer{}
	if options.IncludePods || options.IncludeStats {
		var err error
		containers, err = adapter.DockerContainersForServer(ctx, server)
		if err != nil {
			response.RuntimeStatus = "degraded"
			response.Warnings = append(response.Warnings, "failed to read Docker containers: "+err.Error())
		}
	}
	containersByName := mapContainersByName(containers)
	statsByName := map[string]adapter.DockerContainerStat{}
	if options.IncludeStats {
		names := aifarPodContainerNames(containers)
		if len(names) > 0 {
			if stats, err := adapter.DockerContainerStatsForServer(ctx, server, names); err == nil {
				statsByName = mapStatsByName(stats)
			} else {
				response.Warnings = append(response.Warnings, "failed to read Docker stats: "+err.Error())
			}
		}
	}
	for _, instance := range instances {
		if instance.App != "aifar" || instance.ServerID != server.ID {
			continue
		}
		a.appendAIFARInstanceRuntime(&response, instance, containersByName, statsByName, options)
	}
	if len(response.Instances) == 0 {
		response.RuntimeStatus = degradedIfReady(response.RuntimeStatus)
		response.Warnings = append(response.Warnings, "no AIFAR instance was found on this server")
	}
	sortRuntimeResponse(&response)
	return response, nil
}

func (a *API) appendAIFARInstanceRuntime(response *aifarRuntimeResponse, instance store.AppInstance, containersByName map[string]adapter.DockerContainer, statsByName map[string]adapter.DockerContainerStat, options aifarRuntimeBuildOptions) {
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
	endpointReadyByService := readyEndpointCounts(endpoints)
	agentDeployments := response.Agent.deploymentStatusByService(instance.ID)
	agentServices := response.Agent.serviceStatusByService(instance.ID)
	readyByService := map[string]int{}
	podsByService := map[string][]aifarRuntimePod{}
	if options.IncludePods {
		for _, pod := range pods {
			row, ok := containersByName[pod.ContainerName]
			stat := statsByName[pod.ContainerName]
			status, ready := runtimePodStatus(pod, row, ok)
			revision := cleanRuntimeText(pod.Revision)
			image := cleanRuntimeText(row.Image)
			if image == "" {
				image = replicaImage[pod.ServiceName]
			}
			item := aifarRuntimePod{
				InstanceID:    instance.ID,
				ServiceName:   pod.ServiceName,
				PodID:         pod.PodID,
				ContainerName: pod.ContainerName,
				Revision:      revision,
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
			if ready {
				readyByService[pod.ServiceName]++
			}
		}
	}
	seenServices := map[string]bool{}
	for _, deployment := range deployments {
		seenServices[deployment.ServiceName] = true
		ready := readyByService[deployment.ServiceName]
		if ready == 0 && (len(podsByService[deployment.ServiceName]) == 0 || response.Agent.Status != "running") {
			ready = endpointReadyByService[deployment.ServiceName]
		}
		image := replicaImage[deployment.ServiceName]
		status := runtimeServiceStatus(deployment, ready)
		agentDeployment, hasAgentDeployment := agentDeployments[deployment.ServiceName]
		if hasAgentDeployment {
			if agentDeployment.ReadyReplicas > 0 || agentDeployment.DesiredReplicas > 0 {
				ready = agentDeployment.ReadyReplicas
			}
			if cleanRuntimeText(agentDeployment.Image) != "" {
				image = cleanRuntimeText(agentDeployment.Image)
			}
			if cleanRuntimeText(agentDeployment.Status) != "" {
				status = cleanRuntimeText(agentDeployment.Status)
			}
		}
		appName := aifarRuntimeAppName(deployment.ServiceName)
		failureReason := runtimeString(runtimeMetadata(deployment.MetadataJSON), "failureReason", "")
		if hasAgentDeployment && cleanRuntimeText(agentDeployment.LastError) != "" {
			failureReason = cleanRuntimeText(agentDeployment.LastError)
		}
		response.Deployments = append(response.Deployments, aifarRuntimeDeployment{
			InstanceID:          instance.ID,
			DeploymentName:      appName,
			ServiceName:         deployment.ServiceName,
			AppName:             appName,
			DesiredReplicas:     deployment.DesiredReplicas,
			CurrentReplicas:     agentDeployment.CurrentReplicas,
			ReadyReplicas:       ready,
			UpdatedReplicas:     agentDeployment.UpdatedReplicas,
			AvailableReplicas:   agentDeployment.AvailableReplicas,
			PodRevision:         effectiveServiceRevision(deployment.CurrentRevision, podsByService[deployment.ServiceName]),
			UpdatingPodRevision: cleanRuntimeText(deployment.UpdatingRevision),
			Image:               image,
			Status:              status,
			UpdatedAt:           deployment.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			FailureReason:       failureReason,
		})
		serviceRow := runtimeServiceFromDeployment(instance.ID, deployment, podsByService[deployment.ServiceName], ready, image, status)
		applyAgentStatusToRuntimeService(&serviceRow, agentServices[deployment.ServiceName], agentDeployment)
		response.Services = append(response.Services, serviceRow)
	}
	for service, servicePods := range podsByService {
		if seenServices[service] {
			continue
		}
		serviceRow := runtimeServiceFromPods(instance.ID, service, servicePods, readyByService[service], replicaImage[service])
		applyAgentStatusToRuntimeService(&serviceRow, agentServices[service], agentDeployments[service])
		response.Services = append(response.Services, serviceRow)
	}
	response.Ingress = append(response.Ingress, runtimeIngressFromMetadata(instance.ID, metadata, response.Agent))
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
		Status    string                     `json:"status"`
		Version   string                     `json:"version"`
		Listeners []int                      `json:"listeners"`
		Features  []string                   `json:"features"`
		Instances []aifarAgentInstanceStatus `json:"instances"`
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
	return aifarRuntimeAgent{Status: status, Version: parsed.Version, Mode: "systemd", Listeners: parsed.Listeners, Features: parsed.Features, Instances: parsed.Instances}
}

func (agent aifarRuntimeAgent) deploymentStatusByService(instanceID string) map[string]aifarAgentDeploymentStatus {
	out := map[string]aifarAgentDeploymentStatus{}
	for _, instance := range agent.Instances {
		if instance.InstanceID != instanceID {
			continue
		}
		for _, status := range instance.DeploymentStatus {
			if service := cleanRuntimeText(status.ServiceName); service != "" {
				out[service] = status
			}
		}
	}
	return out
}

func (agent aifarRuntimeAgent) serviceStatusByService(instanceID string) map[string]aifarAgentServiceStatus {
	out := map[string]aifarAgentServiceStatus{}
	for _, instance := range agent.Instances {
		if instance.InstanceID != instanceID {
			continue
		}
		for _, status := range instance.ServiceStatus {
			if service := cleanRuntimeText(status.ServiceName); service != "" {
				out[service] = status
			}
		}
	}
	return out
}

func runtimeServiceFromDeployment(instanceID string, deployment store.AIFARDeployment, pods []aifarRuntimePod, ready int, image, status string) aifarRuntimeService {
	cpu, mem := averagePodLoad(pods)
	if image == "" {
		image = firstRuntimePodImage(pods)
	}
	return aifarRuntimeService{
		InstanceID:         instanceID,
		ServiceName:        deployment.ServiceName,
		AppName:            aifarRuntimeAppName(deployment.ServiceName),
		ProxyName:          "aifar-agent:" + deployment.ServiceName,
		DesiredReplicas:    deployment.DesiredReplicas,
		ReadyReplicas:      ready,
		ActiveEndpoints:    ready,
		EndpointCount:      ready,
		ReadyEndpointCount: ready,
		Image:              image,
		Status:             status,
		CPUPercent:         cpu,
		MemoryPercent:      mem,
		FailureReason:      runtimeString(runtimeMetadata(deployment.MetadataJSON), "failureReason", ""),
	}
}

func runtimeServiceFromPods(instanceID, service string, pods []aifarRuntimePod, ready int, image string) aifarRuntimeService {
	cpu, mem := averagePodLoad(pods)
	activePods := activeRuntimePods(pods)
	desired := len(activePods)
	if desired == 0 {
		desired = len(pods)
	}
	status := "ready"
	if ready == 0 {
		status = "no-endpoints"
	} else if ready < desired {
		status = "degraded"
	}
	if image == "" {
		image = firstRuntimePodImage(activePods)
	}
	if image == "" {
		image = firstRuntimePodImage(pods)
	}
	return aifarRuntimeService{
		InstanceID:         instanceID,
		ServiceName:        service,
		AppName:            aifarRuntimeAppName(service),
		ProxyName:          "aifar-agent:" + service,
		DesiredReplicas:    desired,
		ReadyReplicas:      ready,
		ActiveEndpoints:    ready,
		EndpointCount:      ready,
		ReadyEndpointCount: ready,
		Image:              image,
		Status:             status,
		CPUPercent:         cpu,
		MemoryPercent:      mem,
	}
}

func applyAgentStatusToRuntimeService(row *aifarRuntimeService, serviceStatus aifarAgentServiceStatus, deploymentStatus aifarAgentDeploymentStatus) {
	if row == nil {
		return
	}
	if serviceStatus.ServiceName != "" {
		row.EndpointCount = serviceStatus.EndpointCount
		row.ReadyEndpointCount = serviceStatus.ReadyEndpointCount
		row.ActiveEndpoints = serviceStatus.ReadyEndpointCount
		row.NacosRegistered = serviceStatus.NacosRegistered
		row.NacosReady = serviceStatus.NacosReady
		row.LastNacosError = cleanRuntimeText(serviceStatus.LastNacosError)
		if cleanRuntimeText(serviceStatus.Status) != "" {
			row.Status = cleanRuntimeText(serviceStatus.Status)
		}
	}
	if deploymentStatus.ServiceName != "" {
		row.DesiredReplicas = deploymentStatus.DesiredReplicas
		row.ReadyReplicas = deploymentStatus.ReadyReplicas
		row.RolloutStatus = cleanRuntimeText(deploymentStatus.Status)
		row.LastError = cleanRuntimeText(deploymentStatus.LastError)
		if cleanRuntimeText(deploymentStatus.Image) != "" {
			row.Image = cleanRuntimeText(deploymentStatus.Image)
		}
		switch row.RolloutStatus {
		case "failed", "rolling", "degraded", "offline":
			row.Status = row.RolloutStatus
		}
	}
	if row.LastError == "" && row.LastNacosError != "" {
		row.LastError = row.LastNacosError
	}
	if row.LastNacosError != "" && row.Status == "ready" {
		row.Status = "degraded"
	}
}

func aifarRuntimeAppName(service string) string {
	switch strings.TrimSpace(service) {
	case "gateway":
		return "alpha-gateway"
	case "oauth":
		return "alpha-oauth"
	case "permission":
		return "alpha-permission"
	case "system":
		return "alpha-system"
	case "file":
		return "alpha-file"
	case "message":
		return "alpha-message"
	case "im":
		return "alpha-im"
	case "contacts":
		return "alpha-contacts"
	case "meeting":
		return "alpha-meeting"
	case "web-vue3":
		return "web-vue3"
	default:
		return strings.TrimSpace(service)
	}
}

func runtimeIngressFromMetadata(instanceID string, metadata map[string]any, agent aifarRuntimeAgent) aifarRuntimeIngress {
	container := runtimeString(metadata, "runtimeService", "aifar-agent")
	gatewayPort := runtimeInt(metadata, "gatewayPort", 38000)
	webPort := runtimeInt(metadata, "webPort", 8080)
	status, ingressErr := runtimeIngressStatus(agent, gatewayPort, webPort)
	return aifarRuntimeIngress{
		InstanceID:   instanceID,
		Container:    container,
		Status:       status,
		GatewayPort:  gatewayPort,
		WebPort:      webPort,
		GatewayRoute: runtimeString(metadata, "gatewayEndpoint", ""),
		WebRoute:     runtimeString(metadata, "endpoint", ""),
		Error:        ingressErr,
	}
}

func runtimeIngressStatus(agent aifarRuntimeAgent, gatewayPort, webPort int) (string, string) {
	status := cleanRuntimeText(agent.Status)
	if status == "" {
		status = "unknown"
	}
	if status != "running" {
		return status, cleanRuntimeText(agent.Error)
	}
	if len(agent.Listeners) == 0 {
		return "running", ""
	}
	listeners := map[int]bool{}
	for _, port := range agent.Listeners {
		if port > 0 {
			listeners[port] = true
		}
	}
	missing := []string{}
	if gatewayPort > 0 && !listeners[gatewayPort] {
		missing = append(missing, fmt.Sprintf("%d", gatewayPort))
	}
	if webPort > 0 && webPort != gatewayPort && !listeners[webPort] {
		missing = append(missing, fmt.Sprintf("%d", webPort))
	}
	if len(missing) > 0 {
		return "degraded", "missing listener ports: " + strings.Join(missing, ", ")
	}
	return "running", ""
}

func runtimeServiceStatus(deployment store.AIFARDeployment, ready int) string {
	if strings.EqualFold(deployment.Status, "failed") {
		return "failed"
	}
	if deployment.DesiredReplicas == 0 || strings.EqualFold(deployment.Status, "offline") {
		return "offline"
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
		if cleanRuntimeText(item.Revision) == "" {
			continue
		}
		image := cleanRuntimeText(item.Image)
		if image == "" {
			continue
		}
		if out[item.ServiceName] == "" {
			out[item.ServiceName] = image
		}
	}
	return out
}

func readyEndpointCounts(endpoints []store.AIFARServiceEndpoint) map[string]int {
	out := map[string]int{}
	for _, endpoint := range endpoints {
		if endpoint.Ready && strings.EqualFold(endpoint.State, "active") && cleanRuntimeText(endpoint.Revision) != "" {
			out[endpoint.ServiceName]++
		}
	}
	return out
}

func effectiveServiceRevision(revision string, pods []aifarRuntimePod) string {
	if revision = cleanRuntimeText(revision); revision != "" {
		return revision
	}
	return firstRuntimePodRevision(pods)
}

func firstRuntimePodRevision(pods []aifarRuntimePod) string {
	for _, pod := range pods {
		if pod.Status == "stale" {
			continue
		}
		if revision := cleanRuntimeText(pod.Revision); revision != "" {
			return revision
		}
	}
	for _, pod := range pods {
		if revision := cleanRuntimeText(pod.Revision); revision != "" {
			return revision
		}
	}
	return ""
}

func firstRuntimePodImage(pods []aifarRuntimePod) string {
	for _, pod := range pods {
		if pod.Status == "stale" {
			continue
		}
		if image := cleanRuntimeText(pod.Image); image != "" {
			return image
		}
	}
	for _, pod := range pods {
		if image := cleanRuntimeText(pod.Image); image != "" {
			return image
		}
	}
	return ""
}

func activeRuntimePods(pods []aifarRuntimePod) []aifarRuntimePod {
	out := make([]aifarRuntimePod, 0, len(pods))
	for _, pod := range pods {
		if pod.Status != "stale" {
			out = append(out, pod)
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
			if text := cleanRuntimeText(v); text != "" {
				return text
			}
		}
	}
	return fallback
}

func cleanRuntimeText(value string) string {
	text := strings.TrimSpace(value)
	lower := strings.ToLower(text)
	switch {
	case lower == "", lower == "<nil>", lower == "<no value>", lower == "nil", lower == "null":
		return ""
	case strings.Contains(lower, "<nil>"), strings.Contains(lower, "<no value>"):
		return ""
	default:
		return text
	}
}

func boundedRuntimeLogTail(value int) int {
	if value <= 0 {
		return 200
	}
	if value > 1000 {
		return 1000
	}
	return value
}

func boundedRuntimeLogBatch(value int) int {
	if value <= 0 {
		return 200
	}
	if value > 1000 {
		return 1000
	}
	return value
}

func runtimeLogSinceFromRequest(r *http.Request) time.Time {
	raw := cleanRuntimeText(firstRuntimeValue(runtimeQueryValues(r, "since", "from")))
	if raw == "" {
		return time.Time{}
	}
	if strings.EqualFold(raw, "now") {
		return time.Now()
	}
	if unix, err := strconv.ParseInt(raw, 10, 64); err == nil && unix > 0 {
		return time.Unix(unix, 0)
	}
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed
	}
	return time.Time{}
}

func runtimeLogInitialSince(requested time.Time) time.Time {
	if !requested.IsZero() {
		return requested
	}
	return time.Now().Add(-3 * time.Second)
}

func runtimeQueryValues(r *http.Request, keys ...string) []string {
	seen := map[string]bool{}
	out := []string{}
	query := r.URL.Query()
	for _, key := range keys {
		for _, raw := range query[key] {
			for _, part := range strings.Split(raw, ",") {
				value := cleanRuntimeText(part)
				if value == "" || seen[value] {
					continue
				}
				seen[value] = true
				out = append(out, value)
			}
		}
	}
	return out
}

func firstRuntimeValue(values []string) string {
	for _, value := range values {
		if value = cleanRuntimeText(value); value != "" {
			return value
		}
	}
	return ""
}

func filterRuntimeLogPods(pods []store.AIFARPod, services, podSelectors []string) []store.AIFARPod {
	serviceSet := stringSet(services)
	podSet := stringSet(podSelectors)
	out := make([]store.AIFARPod, 0, len(pods))
	for _, pod := range pods {
		if strings.TrimSpace(pod.ContainerName) == "" {
			continue
		}
		if len(serviceSet) > 0 && !serviceSet[pod.ServiceName] {
			continue
		}
		if len(podSet) > 0 && !podSet[pod.PodID] && !podSet[pod.ContainerName] {
			continue
		}
		out = append(out, pod)
	}
	return out
}

func stringSet(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		if value = cleanRuntimeText(value); value != "" {
			out[value] = true
		}
	}
	return out
}

func runtimeLogLineCounts(lines []string) map[string]int {
	out := map[string]int{}
	for _, line := range lines {
		out[line]++
	}
	return out
}

func runtimeLogLinesSince(lines []string, since time.Time) []string {
	if since.IsZero() || len(lines) == 0 {
		return lines
	}
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if stamp, ok := runtimeLogLineTime(line); !ok || !stamp.Before(since) {
			out = append(out, line)
		}
	}
	return out
}

func runtimeLogLineTime(line string) (time.Time, bool) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return time.Time{}, false
	}
	raw := strings.TrimSpace(fields[0])
	if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil {
		return parsed, true
	}
	if parsed, err := time.Parse(time.RFC3339, raw); err == nil {
		return parsed, true
	}
	return time.Time{}, false
}

func runtimeLogNewLines(lines []string, previous map[string]int) ([]string, map[string]int) {
	current := map[string]int{}
	out := []string{}
	for _, line := range lines {
		current[line]++
		if current[line] <= previous[line] {
			continue
		}
		out = append(out, line)
	}
	return out, current
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
		out := map[string]any{}
		for key, value := range raw {
			out[key] = value
		}
		if _, ok := out["nacosEphemeral"]; !ok {
			out["nacosEphemeral"] = true
		}
		return out
	}
	return map[string]any{
		"configVersion":   1,
		"appliedVersion":  1,
		"lastApplyStatus": "applied",
		"nacosEphemeral":  true,
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
