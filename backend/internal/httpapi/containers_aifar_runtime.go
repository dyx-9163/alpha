package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

const aifarK8sLikeModel = "agent-runtime-v2"

type aifarRuntimeServiceCatalogEntry struct {
	ServiceName string
	AppName     string
	Port        int
}

type aifarRuntimeServiceCatalog struct {
	entries map[string]aifarRuntimeServiceCatalogEntry
}

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
	IncludePods             bool
	IncludeStats            bool
	DockerUnavailable       bool
	DockerUnavailableReason string
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

type discoveredAIFARPod struct {
	ServiceName   string
	Revision      string
	ReplicaID     int
	PodID         string
	ContainerName string
	Image         string
	Port          int
	Status        string
	Ready         bool
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

func aifarRuntimeCleanupSteps() []simpleTaskStep {
	return []simpleTaskStep{
		{"validate-runtime-cleanup", "validate AIFAR runtime cleanup"},
		{"scan-pod-containers", "scan existing AIFAR Pod containers"},
		{"prune-control-plane", "prune stale AIFAR Pod control-plane records"},
	}
}

func aifarRuntimeRestartSteps(lang string) []simpleTaskStep {
	return []simpleTaskStep{
		{"load-instance", i18n.Text(lang, "aifar.runtimeRestart.stepLoadInstance")},
		{"preflight-runtime", i18n.Text(lang, "aifar.runtimeRestart.stepPreflight")},
		{"stop-all-pods", i18n.Text(lang, "aifar.runtimeRestart.stepStopAll")},
		{"start-all-pods", i18n.Text(lang, "aifar.runtimeRestart.stepStartAll")},
		{"verify-runtime", i18n.Text(lang, "aifar.runtimeRestart.stepVerify")},
	}
}

func aifarRuntimeAgentUninstallSteps() []simpleTaskStep {
	return []simpleTaskStep{
		{"validate-agent-uninstall", "validate AIFAR agent uninstall"},
		{"remove-agent-runtime", "deregister Nacos proxies and remove aifar-agent"},
		{"record-agent-uninstall", "record AIFAR agent uninstall"},
	}
}

func aifarRuntimeConfigSteps() []simpleTaskStep {
	return []simpleTaskStep{
		{"save-desired-config", "save desired runtime config"},
		{"render-config", "render runtime config script"},
		{"apply-runtime-config", "apply Docker resources and JVM options"},
		{"record-applied-config", "record runtime config status"},
	}
}

func aifarRuntimeServiceInstallSteps() []simpleTaskStep {
	return []simpleTaskStep{
		{"validate-service-install", "validate AIFAR service installation"},
		{"render-service-install", "render AIFAR service install script"},
		{"apply-service-install", "build and start missing AIFAR services"},
		{"record-service-install", "record AIFAR service control plane"},
	}
}

func aifarRuntimeScaleSteps() []simpleTaskStep {
	return []simpleTaskStep{
		{"validate-service", "validate AIFAR service scale request"},
		{"render-scale-spec", "render AIFAR runtime scale script"},
		{"apply-scale", "apply desired replicas through aifar-agent"},
		{"record-scale", "record AIFAR scale result"},
	}
}

func (a *aifarRuntimeController) runtime(w http.ResponseWriter, r *http.Request) {
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

func (a *aifarRuntimeController) logs(w http.ResponseWriter, r *http.Request) {
	server, instance, query, ok := a.resolveAIFARRuntimeLogQuery(w, r)
	if !ok {
		return
	}
	response, err := a.collectAIFARRuntimeLogs(r.Context(), server, instance, query)
	respond(w, response, err)
}

func (a *aifarRuntimeController) logEvents(w http.ResponseWriter, r *http.Request) {
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
	emitError := func(err error) {
		writeSSE(w, "runtime-logs-error", map[string]any{"message": err.Error()})
		if flusher != nil {
			flusher.Flush()
		}
	}
	states := map[string]runtimeLogStreamState{}
	snapshotOptions := adapter.DockerLogOptions{Tail: query.Tail, Since: query.Since, Timestamps: true}
	if query.FromEnd {
		snapshotOptions = adapter.DockerLogOptions{Tail: query.BatchSize, Timestamps: true}
	}
	seedSnapshot, err := a.collectAIFARRuntimeLogsForPods(r.Context(), server, instance, query, pods, snapshotOptions, "snapshot")
	if err != nil {
		emitError(err)
	} else {
		snapshot := seedSnapshot
		if query.FromEnd {
			snapshot.Pods = make([]aifarRuntimeLogPod, len(seedSnapshot.Pods))
			for i := range snapshot.Pods {
				snapshot.Pods[i] = seedSnapshot.Pods[i]
				snapshot.Pods[i].Logs = nil
				snapshot.Pods[i].LineCount = 0
			}
		}
		snapshot.Warnings = append(snapshot.Warnings, warnings...)
		writeSSE(w, "runtime-logs-snapshot", snapshot)
		if flusher != nil {
			flusher.Flush()
		}
		for _, item := range seedSnapshot.Pods {
			initialSince := runtimeLogNextSince(item.Logs, runtimeLogInitialSince(query.Since), query.Since)
			states[item.ContainerName] = runtimeLogStreamState{
				Since:           initialSince,
				LastFetchCounts: runtimeLogLineCounts(runtimeLogLinesSince(item.Logs, initialSince)),
			}
		}
	}
	for _, pod := range pods {
		if _, ok := states[pod.ContainerName]; ok {
			continue
		}
		states[pod.ContainerName] = runtimeLogStreamState{
			Since:           runtimeLogInitialSince(query.Since),
			LastFetchCounts: map[string]int{},
		}
	}
	bufferSize := len(pods) * query.BatchSize
	if bufferSize < 128 {
		bufferSize = 128
	}
	if bufferSize > 4096 {
		bufferSize = 4096
	}
	events := make(chan runtimeLogStreamEvent, bufferSize)
	streamCtx, cancelStreams := context.WithCancel(r.Context())
	defer cancelStreams()
	done := make(chan struct{})
	if len(pods) > 0 {
		var wg sync.WaitGroup
		for _, pod := range pods {
			pod := pod
			state := states[pod.ContainerName]
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := adapter.StreamDockerContainerLogsForServerWithOptions(streamCtx, server, pod.ContainerName, adapter.DockerLogOptions{
					Tail:       query.BatchSize,
					Since:      state.Since,
					Timestamps: true,
				}, func(line string) {
					select {
					case events <- runtimeLogStreamEvent{Pod: pod, Line: line}:
					case <-streamCtx.Done():
					}
				})
				if err != nil && streamCtx.Err() == nil {
					select {
					case events <- runtimeLogStreamEvent{Pod: pod, Err: err}:
					case <-streamCtx.Done():
					}
				}
			}()
		}
		go func() {
			wg.Wait()
			close(done)
		}()
	} else {
		done = nil
	}
	skipCounts := map[string]map[string]int{}
	for name, state := range states {
		skipCounts[name] = state.LastFetchCounts
	}
	pending := map[string][]string{}
	pendingErrors := map[string]string{}
	pendingTotal := 0
	flushBatch := func() {
		if pendingTotal == 0 && len(pendingErrors) == 0 {
			return
		}
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
		for _, pod := range pods {
			logs := pending[pod.ContainerName]
			errText := pendingErrors[pod.ContainerName]
			if len(logs) == 0 && errText == "" {
				continue
			}
			item := runtimeLogPodResponse(pod, logs)
			if errText != "" {
				item.CollectionError = errText
				response.Warnings = append(response.Warnings, pod.ContainerName+": "+errText)
			}
			response.Pods = append(response.Pods, item)
		}
		pending = map[string][]string{}
		pendingErrors = map[string]string{}
		pendingTotal = 0
		writeSSE(w, "runtime-logs-batch", response)
		if flusher != nil {
			flusher.Flush()
		}
	}
	flushTicker := time.NewTicker(500 * time.Millisecond)
	defer flushTicker.Stop()
	heartbeat := time.NewTicker(25 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-r.Context().Done():
			flushBatch()
			return
		case event := <-events:
			name := event.Pod.ContainerName
			if event.Err != nil {
				pendingErrors[name] = event.Err.Error()
				flushBatch()
				continue
			}
			counts := skipCounts[name]
			if counts != nil && counts[event.Line] > 0 {
				counts[event.Line]--
				continue
			}
			pending[name] = append(pending[name], event.Line)
			pendingTotal++
			if pendingTotal >= query.BatchSize {
				flushBatch()
			}
		case <-flushTicker.C:
			flushBatch()
		case <-done:
			flushBatch()
			return
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
	FromEnd      bool
}

type runtimeLogStreamState struct {
	Since           time.Time
	LastFetchCounts map[string]int
}

type runtimeLogStreamEvent struct {
	Pod  store.AIFARPod
	Line string
	Err  error
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
		FromEnd:      queryBool(r, "fromEnd", false),
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

func (a *aifarRuntimeController) reconcile(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	server, instance, ok := a.resolveAIFARRuntimeActionTarget(w, r)
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.reconcile", instance.ID, actor, lang, server.ID, nil, func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.reconcile", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) restartAll(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, req.InstanceID, true)
	if !ok {
		return
	}
	module, ok := a.apps.Get(instance.App)
	if !ok {
		writeError(w, http.StatusNotFound, "APP_BACKEND_MODULE_MISSING", i18n.Text(lang, "api.appBackendMissing"), map[string]any{"app": instance.App})
		return
	}
	restart, ok := module.(registry.RuntimeRestartModule)
	if !ok {
		writeError(w, http.StatusConflict, "AIFAR_RUNTIME_RESTART_UNSUPPORTED", i18n.Text(lang, "api.aifarRuntimeRestartUnsupported"), map[string]any{"app": instance.App})
		return
	}
	actor := currentUser(r).Username
	task, err, started := a.startSimplePlannedTask(w, "aifar.runtime.restart-all", instance.ID, actor, lang, server.ID, aifarRuntimeRestartSteps(lang), func(ctx context.Context, log worker.Logger) error {
		current, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		server, err := a.store.GetServer(current.ServerID, true)
		if err != nil {
			return err
		}
		log.Info(i18n.Text(lang, "aifar.runtimeRestart.started"), current.ID)
		err = restart.RestartRuntime(ctx, registry.RuntimeRestartRequest{
			Instance: current,
			Server:   server,
			Language: lang,
			Actor:    actor,
			Reason:   strings.TrimSpace(req.Reason),
		}, registry.RunContext{
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
		if err != nil {
			log.Error(i18n.Text(lang, "aifar.runtimeRestart.failed"), current.ID, err)
			return err
		}
		log.Info(i18n.Text(lang, "aifar.runtimeRestart.completed"), current.ID)
		return nil
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "containers.aifar.runtime.restart-all", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) cleanupStale(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, req.InstanceID, false)
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.runtime.cleanup", instance.ID, actor, lang, server.ID, aifarRuntimeCleanupSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.runtime.cleanup", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) uninstallAgent(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	req := aifarRuntimeActionRequest{}
	if r.Body != nil && r.ContentLength != 0 {
		if !decode(w, r, &req) {
			return
		}
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTargetForInstanceWithAgent(w, r, req.InstanceID, false)
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.agent.uninstall", instance.ID, actor, lang, server.ID, aifarRuntimeAgentUninstallSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.agent.uninstall", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) configure(w http.ResponseWriter, r *http.Request) {
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.runtime.config", instance.ID, actor, lang, server.ID, aifarRuntimeConfigSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.runtime.config", instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) installServices(w http.ResponseWriter, r *http.Request) {
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
	server, err := a.store.GetServer(instance.ServerID, true)
	if err != nil {
		respond(w, nil, err)
		return
	}
	task, err, started := a.startSimplePlannedTask(w, "aifar.services.install", target, actor, lang, server.ID, aifarRuntimeServiceInstallSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.services.install", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) scaleOut(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	service := strings.TrimSpace(chi.URLParam(r, "service"))
	if service == "" {
		writeError(w, http.StatusBadRequest, "AIFAR_SERVICE_REQUIRED", "AIFAR service is required", nil)
		return
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTarget(w, r)
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.scale.out", target, actor, lang, server.ID, aifarRuntimeScaleSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.scale.out", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) scaleIn(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	service := strings.TrimSpace(chi.URLParam(r, "service"))
	if service == "" {
		writeError(w, http.StatusBadRequest, "AIFAR_SERVICE_REQUIRED", "AIFAR service is required", nil)
		return
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTarget(w, r)
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.scale.in", target, actor, lang, server.ID, aifarRuntimeScaleSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
	if err == nil {
		a.audit(r, "aifar.scale.in", target, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (a *aifarRuntimeController) offlineService(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	service := strings.TrimSpace(chi.URLParam(r, "service"))
	if service == "" {
		writeError(w, http.StatusBadRequest, "AIFAR_SERVICE_REQUIRED", "AIFAR service is required", nil)
		return
	}
	server, instance, ok := a.resolveAIFARRuntimeActionTarget(w, r)
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
	task, err, started := a.startSimplePlannedTask(w, "aifar.scale.offline", target, actor, lang, server.ID, aifarRuntimeScaleSteps(), func(ctx context.Context, log worker.Logger) error {
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
			TaskID: log.TaskID(),
			Log:    log,
			TargetLog: func(target string) registry.Logger {
				return log.Target(target)
			},
		})
	})
	if !started {
		return
	}
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
	if strings.TrimSpace(server.DockerHost) != "" {
		if err := adapter.DockerPingForServer(ctx, server); err != nil {
			options.DockerUnavailable = true
			options.DockerUnavailableReason = err.Error()
			response.RuntimeStatus = "degraded"
			response.Warnings = append(response.Warnings, "Docker host is not available: "+err.Error())
		}
	}
	if (options.IncludePods || options.IncludeStats) && !options.DockerUnavailable {
		var err error
		containers, err = adapter.DockerContainersForServer(ctx, server)
		if err != nil {
			options.DockerUnavailable = true
			options.DockerUnavailableReason = err.Error()
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
	if options.IncludePods && !options.DockerUnavailable {
		if a.reconcileAIFARRuntimeControlPlane(instance, metadata, deployments, pods, endpoints, containersByName) {
			if saved, err := a.store.GetAppInstance(instance.ID); err == nil {
				instance = saved
				metadata = runtimeMetadata(instance.Metadata)
			}
			deployments, _ = a.store.ListAIFARDeployments(instance.ID)
			replicasets, _ = a.store.ListAIFARReplicaSets(instance.ID)
			pods, _ = a.store.ListAIFARPods(instance.ID)
			endpoints, _ = a.store.ListAIFARServiceEndpoints(instance.ID)
		}
	}
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
	catalog := newAIFARRuntimeServiceCatalog(metadata)
	seenServices := map[string]bool{}
	for _, deployment := range deployments {
		seenServices[deployment.ServiceName] = true
		ready := readyByService[deployment.ServiceName]
		if ready == 0 && len(podsByService[deployment.ServiceName]) == 0 && !options.DockerUnavailable {
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
		appName := catalog.AppName(deployment.ServiceName)
		failureReason := runtimeString(runtimeMetadata(deployment.MetadataJSON), "failureReason", "")
		if hasAgentDeployment && cleanRuntimeText(agentDeployment.LastError) != "" {
			failureReason = cleanRuntimeText(agentDeployment.LastError)
		}
		if options.DockerUnavailable && failureReason == "" {
			failureReason = options.DockerUnavailableReason
		}
		currentReplicas := agentDeployment.CurrentReplicas
		updatedReplicas := agentDeployment.UpdatedReplicas
		availableReplicas := agentDeployment.AvailableReplicas
		if options.DockerUnavailable {
			ready = 0
			currentReplicas = 0
			updatedReplicas = 0
			availableReplicas = 0
			if deployment.DesiredReplicas == 0 || strings.EqualFold(deployment.Status, "offline") {
				status = "offline"
			} else {
				status = "no-endpoints"
			}
		}
		response.Deployments = append(response.Deployments, aifarRuntimeDeployment{
			InstanceID:          instance.ID,
			DeploymentName:      appName,
			ServiceName:         deployment.ServiceName,
			AppName:             appName,
			DesiredReplicas:     deployment.DesiredReplicas,
			CurrentReplicas:     currentReplicas,
			ReadyReplicas:       ready,
			UpdatedReplicas:     updatedReplicas,
			AvailableReplicas:   availableReplicas,
			PodRevision:         effectiveServiceRevision(deployment.CurrentRevision, podsByService[deployment.ServiceName]),
			UpdatingPodRevision: cleanRuntimeText(deployment.UpdatingRevision),
			Image:               image,
			Status:              status,
			UpdatedAt:           deployment.UpdatedAt.Format("2006-01-02T15:04:05Z07:00"),
			FailureReason:       failureReason,
		})
		serviceRow := runtimeServiceFromDeployment(instance.ID, deployment, podsByService[deployment.ServiceName], ready, image, status, appName)
		applyAgentStatusToRuntimeService(&serviceRow, agentServices[deployment.ServiceName], agentDeployment)
		applyDockerUnavailableToRuntimeService(&serviceRow, options)
		response.Services = append(response.Services, serviceRow)
	}
	for service, servicePods := range podsByService {
		if seenServices[service] {
			continue
		}
		serviceRow := runtimeServiceFromPods(instance.ID, service, servicePods, readyByService[service], replicaImage[service], catalog.AppName(service))
		applyAgentStatusToRuntimeService(&serviceRow, agentServices[service], agentDeployments[service])
		applyDockerUnavailableToRuntimeService(&serviceRow, options)
		response.Services = append(response.Services, serviceRow)
	}
	response.Ingress = append(response.Ingress, runtimeIngressFromMetadata(instance.ID, metadata, response.Agent))
}

func (a *API) collectAIFARAgentStatus(ctx context.Context, server store.Server) aifarRuntimeAgent {
	if a.aifarAgentStatus != nil {
		return a.aifarAgentStatus(ctx, server)
	}
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

func runtimeServiceFromDeployment(instanceID string, deployment store.AIFARDeployment, pods []aifarRuntimePod, ready int, image, status, appName string) aifarRuntimeService {
	cpu, mem := averagePodLoad(pods)
	if image == "" {
		image = firstRuntimePodImage(pods)
	}
	if appName == "" {
		appName = deployment.ServiceName
	}
	return aifarRuntimeService{
		InstanceID:         instanceID,
		ServiceName:        deployment.ServiceName,
		AppName:            appName,
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

func runtimeServiceFromPods(instanceID, service string, pods []aifarRuntimePod, ready int, image, appName string) aifarRuntimeService {
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
	if appName == "" {
		appName = service
	}
	return aifarRuntimeService{
		InstanceID:         instanceID,
		ServiceName:        service,
		AppName:            appName,
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

func applyDockerUnavailableToRuntimeService(row *aifarRuntimeService, options aifarRuntimeBuildOptions) {
	if row == nil || !options.DockerUnavailable {
		return
	}
	row.ReadyReplicas = 0
	row.ActiveEndpoints = 0
	row.EndpointCount = 0
	row.ReadyEndpointCount = 0
	if row.DesiredReplicas == 0 || row.Status == "offline" {
		return
	}
	row.Status = "no-endpoints"
	if row.LastError == "" {
		row.LastError = options.DockerUnavailableReason
	}
	if row.FailureReason == "" {
		row.FailureReason = options.DockerUnavailableReason
	}
}

func (a *API) reconcileAIFARRuntimeControlPlane(instance store.AppInstance, metadata map[string]any, deployments []store.AIFARDeployment, pods []store.AIFARPod, endpoints []store.AIFARServiceEndpoint, containersByName map[string]adapter.DockerContainer) bool {
	catalog := newAIFARRuntimeServiceCatalog(metadata)
	discovered := discoverAIFARPodsFromDocker(metadata, containersByName, catalog)
	changed := a.pruneAIFARRuntimeResidualRecords(instance.ID, discovered)
	if len(discovered) == 0 {
		return changed
	}
	deploymentsByService := make(map[string]store.AIFARDeployment, len(deployments))
	for _, deployment := range deployments {
		deploymentsByService[deployment.ServiceName] = deployment
	}
	replicaSets, _ := a.store.ListAIFARReplicaSets(instance.ID)
	replicaSetsByKey := make(map[string]store.AIFARReplicaSet, len(replicaSets))
	for _, item := range replicaSets {
		replicaSetsByKey[runtimeServiceRevisionKey(item.ServiceName, item.Revision)] = item
	}
	podsByKey := make(map[string]store.AIFARPod, len(pods))
	for _, pod := range pods {
		podsByKey[runtimeServicePodKey(pod.ServiceName, pod.PodID)] = pod
	}
	endpointsByService := map[string][]store.AIFARServiceEndpoint{}
	for _, endpoint := range endpoints {
		endpointsByService[endpoint.ServiceName] = append(endpointsByService[endpoint.ServiceName], endpoint)
	}
	explicitDesiredFromMetadata := runtimeDesiredReplicasFromMetadata(metadata)
	desiredFromMetadata := make(map[string]int, len(explicitDesiredFromMetadata)+len(deployments))
	for service, replicas := range explicitDesiredFromMetadata {
		desiredFromMetadata[service] = replicas
	}
	for _, deployment := range deployments {
		if _, ok := desiredFromMetadata[deployment.ServiceName]; !ok {
			desiredFromMetadata[deployment.ServiceName] = deployment.DesiredReplicas
		}
	}
	grouped := map[string][]discoveredAIFARPod{}
	for _, pod := range discovered {
		grouped[pod.ServiceName] = append(grouped[pod.ServiceName], pod)
	}
	services := runtimeMergeServices(runtimeServicesFromMetadata(metadata), runtimeDeploymentServiceNames(deployments), runtimeDiscoveredServiceNames(discovered))
	nextMetadata := copyRuntimeMetadata(metadata)
	metadataChanged := false
	if !runtimeJSONEqual(runtimeDesiredReplicasFromMetadata(nextMetadata), desiredFromMetadata) {
		nextMetadata["desiredReplicas"] = desiredFromMetadata
		metadataChanged = true
	}
	for service, servicePods := range grouped {
		sort.Slice(servicePods, func(i, j int) bool {
			if servicePods[i].ReplicaID == servicePods[j].ReplicaID {
				return servicePods[i].ContainerName < servicePods[j].ContainerName
			}
			return servicePods[i].ReplicaID < servicePods[j].ReplicaID
		})
		desired := runtimeDiscoveredDesiredReplicas(servicePods)
		if explicit, ok := explicitDesiredFromMetadata[service]; ok {
			desired = explicit
		} else {
			if existing := deploymentsByService[service]; existing.DesiredReplicas > desired {
				desired = existing.DesiredReplicas
			}
			if existing := desiredFromMetadata[service]; existing > desired {
				desired = existing
			}
		}
		ready := runtimeDiscoveredReadyReplicas(servicePods)
		revision := runtimeDiscoveredRevision(servicePods)
		image := runtimeDiscoveredImage(servicePods)
		status := runtimeDiscoveredServiceStatus(desired, ready)
		existingDeployment, hasDeployment := deploymentsByService[service]
		strategyJSON := cleanRuntimeText(existingDeployment.StrategyJSON)
		if strategyJSON == "" {
			strategyJSON = runtimeMarshalJSON(map[string]any{"type": "DockerReconcile", "source": "docker"})
		}
		deploymentMetadata := runtimeMarshalJSON(map[string]any{"operation": "docker-reconcile", "source": "docker"})
		nextDeployment := store.AIFARDeployment{
			ID:               existingDeployment.ID,
			InstanceID:       instance.ID,
			ServiceName:      service,
			DesiredReplicas:  desired,
			CurrentRevision:  revision,
			UpdatingRevision: "",
			StrategyJSON:     strategyJSON,
			Status:           status,
			MetadataJSON:     deploymentMetadata,
			CreatedAt:        existingDeployment.CreatedAt,
		}
		if !hasDeployment || !runtimeDeploymentEqual(existingDeployment, nextDeployment) {
			if _, err := a.store.SaveAIFARDeployment(nextDeployment); err == nil {
				changed = true
			}
		}
		if revision != "" {
			existingReplicaSet := replicaSetsByKey[runtimeServiceRevisionKey(service, revision)]
			artifactHash := cleanRuntimeText(existingReplicaSet.ArtifactHash)
			if image == "" {
				image = cleanRuntimeText(existingReplicaSet.Image)
			}
			nextReplicaSet := store.AIFARReplicaSet{
				ID:           existingReplicaSet.ID,
				InstanceID:   instance.ID,
				ServiceName:  service,
				Revision:     revision,
				Image:        image,
				ArtifactHash: artifactHash,
				DesiredPods:  desired,
				ReadyPods:    ready,
				Status:       status,
				MetadataJSON: runtimeMarshalJSON(map[string]any{"operation": "docker-reconcile", "source": "docker"}),
				CreatedAt:    existingReplicaSet.CreatedAt,
			}
			if existingReplicaSet.ID == "" || !runtimeReplicaSetEqual(existingReplicaSet, nextReplicaSet) {
				if _, err := a.store.SaveAIFARReplicaSet(nextReplicaSet); err == nil {
					changed = true
				}
			}
		}
		nextEndpoints := make([]store.AIFARServiceEndpoint, 0, ready)
		for _, pod := range servicePods {
			port := pod.Port
			if port <= 0 {
				port = catalog.Port(service)
			}
			existingPod := podsByKey[runtimeServicePodKey(service, pod.PodID)]
			nextPod := store.AIFARPod{
				ID:            existingPod.ID,
				InstanceID:    instance.ID,
				ServiceName:   service,
				Revision:      pod.Revision,
				PodID:         pod.PodID,
				ContainerName: pod.ContainerName,
				Port:          port,
				Status:        pod.Status,
				Ready:         pod.Ready,
				MetadataJSON:  runtimeMarshalJSON(aifarRuntimeEndpointMetadata(pod, port)),
				CreatedAt:     existingPod.CreatedAt,
			}
			if existingPod.ID == "" || !runtimePodEqual(existingPod, nextPod) {
				if _, err := a.store.SaveAIFARPod(nextPod); err == nil {
					changed = true
				}
			}
			if pod.Ready {
				nextEndpoints = append(nextEndpoints, store.AIFARServiceEndpoint{
					InstanceID:    instance.ID,
					ServiceName:   service,
					PodID:         pod.PodID,
					ContainerName: pod.ContainerName,
					Revision:      pod.Revision,
					Port:          port,
					State:         "active",
					Ready:         true,
					MetadataJSON:  runtimeMarshalJSON(aifarRuntimeEndpointMetadata(pod, port)),
				})
			}
		}
		if !runtimeEndpointsEqual(endpointsByService[service], nextEndpoints) {
			if err := a.store.ReplaceAIFARServiceEndpoints(instance.ID, service, nextEndpoints); err == nil {
				changed = true
			}
		}
		if runtimeApplyDiscoveredServiceMetadata(nextMetadata, service, desired, servicePods, nextEndpoints, services) {
			metadataChanged = true
		}
	}
	if metadataChanged {
		nextMetadata["lastDockerReconcileAt"] = time.Now().UTC().Format(time.RFC3339)
		if raw, err := json.Marshal(nextMetadata); err == nil {
			instance.Metadata = string(raw)
			if _, err := a.store.SaveAppInstance(instance); err == nil {
				changed = true
			}
		}
	}
	return changed
}

func (a *API) pruneAIFARRuntimeResidualRecords(instanceID string, discovered []discoveredAIFARPod) bool {
	existingContainers := make([]string, 0, len(discovered))
	for _, pod := range discovered {
		if name := cleanRuntimeText(pod.ContainerName); name != "" {
			existingContainers = append(existingContainers, name)
		}
	}
	changed := false
	if pruned, err := a.store.PruneAIFARPodRecords(instanceID, existingContainers); err == nil && pruned > 0 {
		changed = true
	}
	if pruned, err := a.store.PruneAIFARServiceEndpointRecords(instanceID, existingContainers); err == nil && pruned > 0 {
		changed = true
	}
	return changed
}

func newAIFARRuntimeServiceCatalog(metadata map[string]any) aifarRuntimeServiceCatalog {
	catalog := aifarRuntimeServiceCatalog{entries: map[string]aifarRuntimeServiceCatalogEntry{}}
	for _, entry := range runtimeServiceCatalogFromMetadata(metadata) {
		catalog.Upsert(entry)
	}
	return catalog
}

func runtimeServiceCatalogFromMetadata(metadata map[string]any) []aifarRuntimeServiceCatalogEntry {
	rawCatalog, ok := metadata["serviceCatalog"]
	if !ok {
		return nil
	}
	raw, err := json.Marshal(rawCatalog)
	if err != nil {
		return nil
	}
	var items []map[string]any
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil
	}
	out := make([]aifarRuntimeServiceCatalogEntry, 0, len(items))
	for _, item := range items {
		service := cleanRuntimeText(strings.ToLower(runtimeTextFromAny(item["name"])))
		if service == "" {
			continue
		}
		out = append(out, aifarRuntimeServiceCatalogEntry{
			ServiceName: service,
			AppName:     cleanRuntimeText(runtimeTextFromAny(item["applicationName"])),
			Port:        runtimeIntFromAny(item["port"], 0),
		})
	}
	return out
}

func (c aifarRuntimeServiceCatalog) Allows(service string) bool {
	service = cleanRuntimeText(strings.ToLower(service))
	if service == "" {
		return false
	}
	_, ok := c.entries[service]
	return ok
}

func (c aifarRuntimeServiceCatalog) Upsert(entry aifarRuntimeServiceCatalogEntry) {
	service := cleanRuntimeText(strings.ToLower(entry.ServiceName))
	if service == "" {
		return
	}
	current := c.entries[service]
	current.ServiceName = service
	if appName := cleanRuntimeText(entry.AppName); appName != "" {
		current.AppName = appName
	}
	if entry.Port > 0 {
		current.Port = entry.Port
	}
	c.entries[service] = current
}

func (c aifarRuntimeServiceCatalog) Port(service string) int {
	service = cleanRuntimeText(strings.ToLower(service))
	if entry, ok := c.entries[service]; ok && entry.Port > 0 {
		return entry.Port
	}
	return 0
}

func (c aifarRuntimeServiceCatalog) AppName(service string) string {
	service = cleanRuntimeText(strings.ToLower(service))
	if entry, ok := c.entries[service]; ok && cleanRuntimeText(entry.AppName) != "" {
		return cleanRuntimeText(entry.AppName)
	}
	return service
}

func (c aifarRuntimeServiceCatalog) PortOrDocker(service, dockerPorts string) int {
	if port := c.Port(service); port > 0 {
		return port
	}
	return runtimePortFromDockerPorts(dockerPorts)
}

func (c aifarRuntimeServiceCatalog) ServiceNames() []string {
	out := make([]string, 0, len(c.entries))
	for service := range c.entries {
		out = append(out, service)
	}
	return runtimeMergeServices(out)
}

func runtimePortFromDockerPorts(ports string) int {
	for _, part := range strings.Split(ports, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if index := strings.LastIndex(part, "->"); index >= 0 {
			part = strings.TrimSpace(part[index+2:])
		}
		if index := strings.Index(part, "/"); index >= 0 {
			part = strings.TrimSpace(part[:index])
		}
		if index := strings.LastIndex(part, ":"); index >= 0 {
			part = strings.TrimSpace(part[index+1:])
		}
		if port := runtimePositiveInt(part, 0); port > 0 {
			return port
		}
	}
	return 0
}

func discoverAIFARPodsFromDocker(metadata map[string]any, containersByName map[string]adapter.DockerContainer, catalog aifarRuntimeServiceCatalog) []discoveredAIFARPod {
	installRoot := normalizeRuntimeInstallRoot(runtimeString(metadata, "installRoot", ""))
	out := make([]discoveredAIFARPod, 0, len(containersByName))
	for _, row := range containersByName {
		pod, ok := discoveredAIFARPodFromDocker(row, installRoot, catalog)
		if ok {
			out = append(out, pod)
		}
	}
	return out
}

func discoveredAIFARPodFromDocker(row adapter.DockerContainer, installRoot string, catalog aifarRuntimeServiceCatalog) (discoveredAIFARPod, bool) {
	labels := row.Labels
	labelApp := cleanRuntimeText(labels["aifar.app"])
	labelComponent := cleanRuntimeText(labels["aifar.component"])
	if labelApp != "" || labelComponent != "" {
		if labelApp != "aifar" || labelComponent != "pod" {
			return discoveredAIFARPod{}, false
		}
	}
	if labelRoot := normalizeRuntimeInstallRoot(labels["aifar.install-root"]); labelRoot != "" && installRoot != "" && labelRoot != installRoot {
		return discoveredAIFARPod{}, false
	}
	parsedInstance, parsedService, parsedRevision, parsedReplica, parsedOK := parseAIFARPodContainerNameWithServices(row.Name, catalog.ServiceNames())
	if !parsedOK && labelApp == "" && labelComponent == "" {
		return discoveredAIFARPod{}, false
	}
	if parsedOK && labels["aifar.install-root"] == "" {
		if base := runtimeInstallRootBase(installRoot); base != "" && parsedInstance != "" && parsedInstance != base {
			return discoveredAIFARPod{}, false
		}
	}
	service := cleanRuntimeText(labels["aifar.service"])
	if service == "" {
		service = parsedService
	}
	service = cleanRuntimeText(strings.ToLower(service))
	if !catalog.Allows(service) {
		return discoveredAIFARPod{}, false
	}
	revision := cleanRuntimeText(labels["aifar.revision"])
	if revision == "" {
		revision = cleanRuntimeText(labels["aifar.release"])
	}
	if revision == "" {
		revision = parsedRevision
	}
	if revision == "" {
		revision = runtimeRevisionFromImage(row.Image)
	}
	replica := runtimePositiveInt(labels["aifar.replica"], 0)
	if replica <= 0 {
		replica = parsedReplica
	}
	if replica <= 0 {
		replica = 1
	}
	status, ready := aifarRuntimeDockerPodStatus(row)
	if status == "stale" {
		return discoveredAIFARPod{}, false
	}
	podID := sanitizeRuntimeIdentifier(fmt.Sprintf("%s-%s-r%d", service, revision, replica))
	return discoveredAIFARPod{
		ServiceName:   service,
		Revision:      revision,
		ReplicaID:     replica,
		PodID:         podID,
		ContainerName: strings.TrimPrefix(cleanRuntimeText(row.Name), "/"),
		Image:         cleanRuntimeText(row.Image),
		Port:          catalog.PortOrDocker(service, row.Ports),
		Status:        status,
		Ready:         ready,
	}, true
}

func parseAIFARPodContainerNameWithServices(name string, services []string) (string, string, string, int, bool) {
	name = strings.TrimPrefix(cleanRuntimeText(name), "/")
	if !strings.HasPrefix(name, "aifar-pod-") {
		return "", "", "", 0, false
	}
	body := strings.TrimPrefix(name, "aifar-pod-")
	replicaIndex := strings.LastIndex(body, "-r")
	if replicaIndex <= 0 || replicaIndex+2 >= len(body) {
		return "", "", "", 0, false
	}
	replica, err := strconv.Atoi(body[replicaIndex+2:])
	if err != nil || replica <= 0 {
		return "", "", "", 0, false
	}
	body = body[:replicaIndex]
	bestIndex := -1
	bestService := ""
	for _, service := range services {
		service = cleanRuntimeText(strings.ToLower(service))
		if service == "" {
			continue
		}
		marker := "-" + service + "-"
		index := strings.LastIndex(body, marker)
		if index > bestIndex {
			bestIndex = index
			bestService = service
		}
	}
	if bestIndex <= 0 || bestService == "" {
		return "", "", "", 0, false
	}
	marker := "-" + bestService + "-"
	instance := body[:bestIndex]
	revision := body[bestIndex+len(marker):]
	if instance == "" || revision == "" {
		return "", "", "", 0, false
	}
	return instance, bestService, revision, replica, true
}

func aifarRuntimeDockerPodStatus(row adapter.DockerContainer) (string, bool) {
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
	case state == "running":
		return "ready", true
	case state == "restarting" || state == "created":
		return "starting", false
	default:
		return "stale", false
	}
}

func runtimeApplyDiscoveredServiceMetadata(metadata map[string]any, service string, desired int, pods []discoveredAIFARPod, endpoints []store.AIFARServiceEndpoint, services []string) bool {
	changed := false
	services = runtimeMergeServices(services, []string{service})
	if !runtimeStringSliceEqual(runtimeServicesFromMetadata(metadata), services) {
		metadata["services"] = services
		changed = true
	}
	desiredMap := runtimeDesiredReplicasFromMetadata(metadata)
	if desiredMap[service] != desired {
		desiredMap[service] = desired
		metadata["desiredReplicas"] = desiredMap
		changed = true
	}
	endpointItems := runtimeEndpointMetadataFromStore(endpoints)
	activeEndpoints := runtimeMapFromAny(metadata["activeEndpoints"])
	if !runtimeJSONEqual(activeEndpoints[service], endpointItems) {
		activeEndpoints[service] = endpointItems
		metadata["activeEndpoints"] = activeEndpoints
		changed = true
	}
	containers := runtimeMapFromAny(metadata["containers"])
	if first := runtimeFirstDiscoveredContainer(pods); first != "" && cleanRuntimeText(fmt.Sprint(containers[service])) != first {
		containers[service] = first
		metadata["containers"] = containers
		changed = true
	}
	revisions := runtimeMapFromAny(metadata["serviceRevisions"])
	if revision := runtimeDiscoveredRevision(pods); revision != "" && cleanRuntimeText(fmt.Sprint(revisions[service])) != revision {
		revisions[service] = revision
		metadata["serviceRevisions"] = revisions
		changed = true
	}
	activeServices := runtimeActiveServicesFromEndpointsForServices(desiredMap, activeEndpoints, services)
	if !runtimeJSONEqual(metadata["activeServices"], activeServices) {
		metadata["activeServices"] = activeServices
		changed = true
	}
	return changed
}

func runtimeEndpointMetadataFromStore(endpoints []store.AIFARServiceEndpoint) []map[string]any {
	out := make([]map[string]any, 0, len(endpoints))
	for _, endpoint := range endpoints {
		out = append(out, map[string]any{
			"container": endpoint.ContainerName,
			"releaseId": endpoint.Revision,
			"revision":  endpoint.Revision,
			"podId":     endpoint.PodID,
			"replicaId": runtimeReplicaIDFromPodID(endpoint.PodID),
			"port":      endpoint.Port,
			"state":     endpoint.State,
		})
	}
	return out
}

func aifarRuntimeEndpointMetadata(pod discoveredAIFARPod, port int) map[string]any {
	return map[string]any{
		"container": pod.ContainerName,
		"releaseId": pod.Revision,
		"revision":  pod.Revision,
		"podId":     pod.PodID,
		"replicaId": pod.ReplicaID,
		"port":      port,
		"state":     "active",
	}
}

func runtimeActiveServicesFromEndpointsForServices(desired map[string]int, endpoints map[string]any, services []string) map[string]any {
	services = runtimeMergeServices(services)
	out := make(map[string]any, len(services))
	for _, service := range services {
		out[service] = map[string]any{
			"desiredReplicas": desired[service],
			"activeEndpoints": endpoints[service],
		}
	}
	return out
}

func runtimeDiscoveredServiceStatus(desired, ready int) string {
	if desired <= 0 {
		return "offline"
	}
	if ready <= 0 {
		return "no-endpoints"
	}
	if ready < desired {
		return "degraded"
	}
	return "ready"
}

func runtimeDiscoveredDesiredReplicas(pods []discoveredAIFARPod) int {
	desired := len(pods)
	for _, pod := range pods {
		if pod.ReplicaID > desired {
			desired = pod.ReplicaID
		}
	}
	return desired
}

func runtimeDiscoveredReadyReplicas(pods []discoveredAIFARPod) int {
	ready := 0
	for _, pod := range pods {
		if pod.Ready {
			ready++
		}
	}
	return ready
}

func runtimeDiscoveredRevision(pods []discoveredAIFARPod) string {
	for _, pod := range pods {
		if pod.Ready {
			if revision := cleanRuntimeText(pod.Revision); revision != "" {
				return revision
			}
		}
	}
	for _, pod := range pods {
		if revision := cleanRuntimeText(pod.Revision); revision != "" {
			return revision
		}
	}
	return ""
}

func runtimeDiscoveredImage(pods []discoveredAIFARPod) string {
	for _, pod := range pods {
		if pod.Ready {
			if image := cleanRuntimeText(pod.Image); image != "" {
				return image
			}
		}
	}
	for _, pod := range pods {
		if image := cleanRuntimeText(pod.Image); image != "" {
			return image
		}
	}
	return ""
}

func runtimeFirstDiscoveredContainer(pods []discoveredAIFARPod) string {
	for _, pod := range pods {
		if pod.Ready {
			if name := cleanRuntimeText(pod.ContainerName); name != "" {
				return name
			}
		}
	}
	for _, pod := range pods {
		if name := cleanRuntimeText(pod.ContainerName); name != "" {
			return name
		}
	}
	return ""
}

func runtimeServicesFromMetadata(metadata map[string]any) []string {
	switch raw := metadata["services"].(type) {
	case []string:
		return runtimeMergeServices(raw)
	case []any:
		values := make([]string, 0, len(raw))
		for _, item := range raw {
			values = append(values, fmt.Sprint(item))
		}
		return runtimeMergeServices(values)
	}
	return nil
}

func runtimeDeploymentServiceNames(deployments []store.AIFARDeployment) []string {
	out := make([]string, 0, len(deployments))
	for _, deployment := range deployments {
		out = append(out, deployment.ServiceName)
	}
	return out
}

func runtimeDiscoveredServiceNames(pods []discoveredAIFARPod) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, pod := range pods {
		if pod.ServiceName == "" || seen[pod.ServiceName] {
			continue
		}
		seen[pod.ServiceName] = true
		out = append(out, pod.ServiceName)
	}
	return out
}

func runtimeMergeServices(groups ...[]string) []string {
	seen := map[string]bool{}
	for _, group := range groups {
		for _, service := range group {
			service = cleanRuntimeText(service)
			if service != "" {
				seen[service] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for service := range seen {
		out = append(out, service)
	}
	sort.Strings(out)
	return out
}

func runtimeDesiredReplicasFromMetadata(metadata map[string]any) map[string]int {
	out := map[string]int{}
	switch raw := metadata["desiredReplicas"].(type) {
	case map[string]int:
		for key, value := range raw {
			if value < 0 {
				value = 0
			}
			out[key] = value
		}
	case map[string]any:
		for key, value := range raw {
			n := runtimeIntFromAny(value, 0)
			if n < 0 {
				n = 0
			}
			out[key] = n
		}
	}
	return out
}

func runtimeMapFromAny(value any) map[string]any {
	out := map[string]any{}
	if raw, ok := value.(map[string]any); ok {
		for key, item := range raw {
			out[key] = item
		}
	}
	return out
}

func copyRuntimeMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return map[string]any{}
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		out := map[string]any{}
		for key, value := range metadata {
			out[key] = value
		}
		return out
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return map[string]any{}
	}
	return out
}

func runtimeDeploymentEqual(a, b store.AIFARDeployment) bool {
	return a.DesiredReplicas == b.DesiredReplicas &&
		cleanRuntimeText(a.CurrentRevision) == cleanRuntimeText(b.CurrentRevision) &&
		cleanRuntimeText(a.UpdatingRevision) == cleanRuntimeText(b.UpdatingRevision) &&
		cleanRuntimeText(a.StrategyJSON) == cleanRuntimeText(b.StrategyJSON) &&
		cleanRuntimeText(a.Status) == cleanRuntimeText(b.Status) &&
		cleanRuntimeText(a.MetadataJSON) == cleanRuntimeText(b.MetadataJSON)
}

func runtimeReplicaSetEqual(a, b store.AIFARReplicaSet) bool {
	return cleanRuntimeText(a.Image) == cleanRuntimeText(b.Image) &&
		cleanRuntimeText(a.ArtifactHash) == cleanRuntimeText(b.ArtifactHash) &&
		a.DesiredPods == b.DesiredPods &&
		a.ReadyPods == b.ReadyPods &&
		cleanRuntimeText(a.Status) == cleanRuntimeText(b.Status) &&
		cleanRuntimeText(a.MetadataJSON) == cleanRuntimeText(b.MetadataJSON)
}

func runtimePodEqual(a, b store.AIFARPod) bool {
	return cleanRuntimeText(a.Revision) == cleanRuntimeText(b.Revision) &&
		cleanRuntimeText(a.ContainerName) == cleanRuntimeText(b.ContainerName) &&
		a.Port == b.Port &&
		cleanRuntimeText(a.Status) == cleanRuntimeText(b.Status) &&
		a.Ready == b.Ready &&
		cleanRuntimeText(a.MetadataJSON) == cleanRuntimeText(b.MetadataJSON)
}

func runtimeEndpointsEqual(a, b []store.AIFARServiceEndpoint) bool {
	if len(a) != len(b) {
		return false
	}
	aByKey := map[string]store.AIFARServiceEndpoint{}
	for _, item := range a {
		aByKey[runtimeServicePodKey(item.ServiceName, item.PodID)] = item
	}
	for _, next := range b {
		current, ok := aByKey[runtimeServicePodKey(next.ServiceName, next.PodID)]
		if !ok {
			return false
		}
		if cleanRuntimeText(current.ContainerName) != cleanRuntimeText(next.ContainerName) ||
			cleanRuntimeText(current.Revision) != cleanRuntimeText(next.Revision) ||
			current.Port != next.Port ||
			cleanRuntimeText(current.State) != cleanRuntimeText(next.State) ||
			current.Ready != next.Ready ||
			cleanRuntimeText(current.MetadataJSON) != cleanRuntimeText(next.MetadataJSON) {
			return false
		}
	}
	return true
}

func runtimeServiceRevisionKey(service, revision string) string {
	return service + "\x00" + revision
}

func runtimeServicePodKey(service, podID string) string {
	return service + "\x00" + podID
}

func runtimeInstallRootBase(installRoot string) string {
	installRoot = normalizeRuntimeInstallRoot(installRoot)
	if installRoot == "" {
		return ""
	}
	if index := strings.LastIndex(installRoot, "/"); index >= 0 {
		return installRoot[index+1:]
	}
	return installRoot
}

func runtimeRevisionFromImage(image string) string {
	image = cleanRuntimeText(image)
	if image == "" {
		return ""
	}
	if index := strings.LastIndex(image, "@"); index >= 0 {
		return cleanRuntimeText(image[index+1:])
	}
	slash := strings.LastIndex(image, "/")
	colon := strings.LastIndex(image, ":")
	if colon > slash {
		return cleanRuntimeText(image[colon+1:])
	}
	return ""
}

func runtimeReplicaIDFromPodID(podID string) int {
	index := strings.LastIndex(podID, "-r")
	if index < 0 || index+2 >= len(podID) {
		return 0
	}
	n, _ := strconv.Atoi(podID[index+2:])
	return n
}

func runtimePositiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n <= 0 {
		return fallback
	}
	return n
}

func runtimeIntFromAny(value any, fallback int) int {
	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return fallback
}

func runtimeTextFromAny(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return cleanRuntimeText(v)
	case fmt.Stringer:
		return cleanRuntimeText(v.String())
	default:
		return cleanRuntimeText(fmt.Sprint(v))
	}
}

func runtimeMarshalJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}

func runtimeJSONEqual(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	if err != nil {
		return false
	}
	return string(left) == string(right)
}

func runtimeStringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sanitizeRuntimeIdentifier(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "aifar"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-_")
	if out == "" {
		return "aifar"
	}
	return out
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
		if isAIFARRuntimePodContainer(row) {
			out = append(out, row.Name)
		}
	}
	return out
}

func isAIFARRuntimePodContainer(row adapter.DockerContainer) bool {
	if row.Labels["aifar.app"] == "aifar" && row.Labels["aifar.component"] == "pod" {
		return true
	}
	return false
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
	return time.Time{}
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

func runtimeLogNextSince(lines []string, fallback, floor time.Time) time.Time {
	latest := time.Time{}
	for _, line := range lines {
		stamp, ok := runtimeLogLineTime(line)
		if !ok {
			continue
		}
		if latest.IsZero() || stamp.After(latest) {
			latest = stamp
		}
	}
	if latest.IsZero() {
		return fallback
	}
	next := latest.Add(-3 * time.Second)
	if !floor.IsZero() && next.Before(floor) {
		return floor
	}
	return next
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
