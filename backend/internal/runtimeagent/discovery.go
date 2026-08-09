package runtimeagent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type ReadyEndpoint struct {
	PodID         string `json:"podId"`
	ContainerName string `json:"containerName"`
	Revision      string `json:"revision"`
	Port          int    `json:"port"`
}

type EndpointEvent struct {
	InstanceID  string
	ServiceName string
	AppName     string
	ListenPort  int
	Ready       []ReadyEndpoint
}

var discoveryRetryBackoff = [...]time.Duration{
	time.Second,
	2 * time.Second,
	4 * time.Second,
	8 * time.Second,
	16 * time.Second,
	30 * time.Second,
	60 * time.Second,
}

const discoveryListenerShutdownTimeout = 2 * time.Second

type discoverySyncer interface {
	RefreshRoutes(context.Context, EndpointEvent) error
	Register(context.Context, EndpointEvent) error
	Deregister(context.Context, EndpointEvent) error
	Heartbeat(context.Context, EndpointEvent) error
}

type discoveryControllerOptions struct {
	Syncer            discoverySyncer
	Clock             controllerClock
	Log               io.Writer
	HeartbeatInterval time.Duration
}

type DiscoveryController struct {
	mu                sync.Mutex
	workers           map[string]*discoveryWorker
	syncer            discoverySyncer
	clock             controllerClock
	log               io.Writer
	heartbeatInterval time.Duration
	stopped           bool
}

type discoveryWorker struct {
	controller *DiscoveryController
	key        string
	wake       chan struct{}
	stop       chan struct{}

	mu                 sync.Mutex
	latest             EndpointEvent
	latestHash         string
	routeHash          string
	applied            EndpointEvent
	appliedHash        string
	hasApplied         bool
	registered         EndpointEvent
	registeredIdentity string
	registryKnown      bool
	failures           int
	stopped            bool
	activeHash         string
	cancel             context.CancelFunc
}

type managerDiscoverySyncer struct {
	manager        *Manager
	startPort      func(int) error
	shutdownServer func(*http.Server)
}

type discoveryResult uint8

const (
	discoveryStable discoveryResult = iota
	discoveryRetry
	discoverySuperseded
)

func newDiscoveryController(options discoveryControllerOptions) *DiscoveryController {
	clock := options.Clock
	if clock == nil {
		clock = realControllerClock{}
	}
	return &DiscoveryController{
		workers:           map[string]*discoveryWorker{},
		syncer:            options.Syncer,
		clock:             clock,
		log:               options.Log,
		heartbeatInterval: options.HeartbeatInterval,
	}
}

func (controller *DiscoveryController) EndpointChanged(raw EndpointEvent) {
	event := canonicalEndpointEvent(raw)
	if !validEndpointEvent(event) || controller.syncer == nil {
		return
	}
	hash := endpointEventHash(event)
	key := endpointKey(event.InstanceID, event.ServiceName)

	controller.mu.Lock()
	if controller.stopped {
		controller.mu.Unlock()
		return
	}
	worker := controller.workers[key]
	if worker == nil {
		worker = &discoveryWorker{
			controller: controller,
			key:        key,
			wake:       make(chan struct{}, 1),
			stop:       make(chan struct{}),
		}
		controller.workers[key] = worker
		go worker.run()
	}
	controller.mu.Unlock()

	worker.mu.Lock()
	if worker.stopped || worker.latestHash == hash {
		worker.mu.Unlock()
		return
	}
	worker.latest = event
	worker.latestHash = hash
	if worker.cancel != nil {
		worker.cancel()
	}
	worker.mu.Unlock()
	worker.enqueue()
}

func (controller *DiscoveryController) StopInstance(instanceID string) {
	prefix := strings.TrimSpace(instanceID) + "/"
	controller.mu.Lock()
	workers := make([]*discoveryWorker, 0)
	for key, worker := range controller.workers {
		if strings.HasPrefix(key, prefix) {
			delete(controller.workers, key)
			workers = append(workers, worker)
		}
	}
	controller.mu.Unlock()
	for _, worker := range workers {
		worker.stopWorker()
	}
}

func (controller *DiscoveryController) Stop() {
	controller.mu.Lock()
	if controller.stopped {
		controller.mu.Unlock()
		return
	}
	controller.stopped = true
	workers := make([]*discoveryWorker, 0, len(controller.workers))
	for key, worker := range controller.workers {
		delete(controller.workers, key)
		workers = append(workers, worker)
	}
	controller.mu.Unlock()
	for _, worker := range workers {
		worker.stopWorker()
	}
}

func (worker *discoveryWorker) enqueue() {
	worker.mu.Lock()
	stopped := worker.stopped
	worker.mu.Unlock()
	if stopped {
		return
	}
	select {
	case worker.wake <- struct{}{}:
	default:
	}
}

func (worker *discoveryWorker) stopWorker() {
	worker.mu.Lock()
	if worker.stopped {
		worker.mu.Unlock()
		return
	}
	worker.stopped = true
	if worker.cancel != nil {
		worker.cancel()
	}
	close(worker.stop)
	worker.mu.Unlock()
}

func (worker *discoveryWorker) run() {
	for {
		select {
		case <-worker.stop:
			return
		case <-worker.wake:
			worker.syncUntilStable()
		}
		for worker.shouldHeartbeat() {
			timer := worker.controller.clock.NewTimer(worker.controller.heartbeatInterval)
			select {
			case <-worker.stop:
				timer.Stop()
				return
			case <-worker.wake:
				timer.Stop()
				worker.syncUntilStable()
				continue
			case <-timer.C():
			}
			if worker.heartbeatOnce() {
				continue
			}
			if !worker.replayRegistrationUntilStable() {
				return
			}
		}
	}
}

func (worker *discoveryWorker) syncUntilStable() {
	for {
		switch worker.syncIteration() {
		case discoveryStable:
			return
		case discoverySuperseded:
			continue
		case discoveryRetry:
			timer := worker.controller.clock.NewTimer(worker.nextBackoff())
			select {
			case <-worker.stop:
				timer.Stop()
				return
			case <-worker.wake:
				timer.Stop()
				continue
			case <-timer.C():
				continue
			}
		}
	}
}

func (worker *discoveryWorker) syncIteration() (result discoveryResult) {
	defer func() {
		if recover() != nil {
			worker.logFailure("WorkerPanic")
			result = discoveryRetry
		}
	}()

	event, hash, _, _, stopped := worker.snapshot()
	if stopped || hash == "" {
		return discoveryStable
	}
	if worker.isFullyApplied(hash) {
		return discoveryStable
	}
	ctx, finish, active := worker.activate(hash)
	if !active {
		return discoverySuperseded
	}
	defer finish()
	if worker.needsRouteRefresh(hash) {
		if err := worker.controller.syncer.RefreshRoutes(ctx, event); err != nil {
			if errors.Is(err, context.Canceled) && worker.wasSuperseded(hash) {
				return discoverySuperseded
			}
			worker.logFailure("RouteRefreshFailed")
			return discoveryRetry
		}
		worker.markRouteRefreshed(hash)
	}
	if worker.wasSuperseded(hash) {
		return discoverySuperseded
	}

	registered, registeredIdentity, registryKnown := worker.registrySnapshot()
	desiredIdentity := registrationIdentityHash(event)
	if len(event.Ready) == 0 {
		if !registryKnown || registeredIdentity != "" {
			target := event
			if registeredIdentity != "" {
				target = registered
			}
			if err := worker.controller.syncer.Deregister(ctx, target); err != nil {
				if errors.Is(err, context.Canceled) && worker.wasSuperseded(hash) {
					return discoverySuperseded
				}
				worker.logFailure("DeregistrationFailed")
				return discoveryRetry
			}
			worker.markDeregistered()
		}
	} else if !registryKnown || registeredIdentity != desiredIdentity {
		if registeredIdentity != "" {
			if err := worker.controller.syncer.Deregister(ctx, registered); err != nil {
				if errors.Is(err, context.Canceled) && worker.wasSuperseded(hash) {
					return discoverySuperseded
				}
				worker.logFailure("DeregistrationFailed")
				return discoveryRetry
			}
			worker.markDeregistered()
			if worker.wasSuperseded(hash) {
				return discoverySuperseded
			}
		}
		if err := worker.controller.syncer.Register(ctx, event); err != nil {
			if errors.Is(err, context.Canceled) && worker.wasSuperseded(hash) {
				return discoverySuperseded
			}
			worker.logFailure("RegistrationFailed")
			return discoveryRetry
		}
		worker.markRegistered(event)
	}
	if worker.wasSuperseded(hash) {
		return discoverySuperseded
	}
	worker.markApplied(event, hash)
	return discoveryStable
}

func (worker *discoveryWorker) snapshot() (EndpointEvent, string, EndpointEvent, bool, bool) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.latest, worker.latestHash, worker.applied, worker.hasApplied, worker.stopped
}

func (worker *discoveryWorker) wasSuperseded(hash string) bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.stopped || worker.latestHash != hash
}

func (worker *discoveryWorker) isFullyApplied(hash string) bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.hasApplied && worker.appliedHash == hash && worker.routeHash == hash
}

func (worker *discoveryWorker) registrySnapshot() (EndpointEvent, string, bool) {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.registered, worker.registeredIdentity, worker.registryKnown
}

func (worker *discoveryWorker) markRegistered(event EndpointEvent) {
	worker.mu.Lock()
	worker.registered = event
	worker.registeredIdentity = registrationIdentityHash(event)
	worker.registryKnown = true
	worker.mu.Unlock()
}

func (worker *discoveryWorker) markDeregistered() {
	worker.mu.Lock()
	worker.registered = EndpointEvent{}
	worker.registeredIdentity = ""
	worker.registryKnown = true
	worker.mu.Unlock()
}

func (worker *discoveryWorker) needsRouteRefresh(hash string) bool {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return worker.routeHash != hash
}

func (worker *discoveryWorker) markRouteRefreshed(hash string) {
	worker.mu.Lock()
	if !worker.stopped && worker.latestHash == hash {
		worker.routeHash = hash
	}
	worker.mu.Unlock()
}

func (worker *discoveryWorker) activate(hash string) (context.Context, func(), bool) {
	ctx, cancel := context.WithCancel(context.Background())
	worker.mu.Lock()
	if worker.stopped || worker.latestHash != hash {
		worker.mu.Unlock()
		cancel()
		return ctx, func() {}, false
	}
	worker.activeHash = hash
	worker.cancel = cancel
	worker.mu.Unlock()
	finish := func() {
		cancel()
		worker.mu.Lock()
		if worker.activeHash == hash {
			worker.activeHash = ""
			worker.cancel = nil
		}
		worker.mu.Unlock()
	}
	return ctx, finish, true
}

func (worker *discoveryWorker) markApplied(event EndpointEvent, hash string) {
	worker.mu.Lock()
	if !worker.stopped && worker.latestHash == hash {
		worker.applied = event
		worker.appliedHash = hash
		worker.hasApplied = true
		worker.failures = 0
	}
	worker.mu.Unlock()
}

func (worker *discoveryWorker) nextBackoff() time.Duration {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	index := worker.failures
	if index >= len(discoveryRetryBackoff) {
		index = len(discoveryRetryBackoff) - 1
	}
	worker.failures++
	return discoveryRetryBackoff[index]
}

func (worker *discoveryWorker) resetFailures() {
	worker.mu.Lock()
	worker.failures = 0
	worker.mu.Unlock()
}

func (worker *discoveryWorker) shouldHeartbeat() bool {
	if worker.controller.heartbeatInterval <= 0 {
		return false
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	return !worker.stopped && worker.hasApplied && len(worker.applied.Ready) > 0
}

func (worker *discoveryWorker) heartbeatOnce() (success bool) {
	defer func() {
		if recover() != nil {
			worker.logFailure("WorkerPanic")
			success = false
		}
	}()
	_, _, event, _, stopped := worker.snapshot()
	if stopped || len(event.Ready) == 0 {
		return true
	}
	hash := endpointEventHash(event)
	ctx, finish, active := worker.activate(hash)
	if !active {
		return true
	}
	defer finish()
	if err := worker.controller.syncer.Heartbeat(ctx, event); err != nil {
		if errors.Is(err, context.Canceled) && worker.wasSuperseded(hash) {
			return true
		}
		worker.logFailure("HeartbeatFailed")
		return false
	}
	worker.resetFailures()
	return true
}

func (worker *discoveryWorker) replayRegistrationUntilStable() bool {
	for {
		timer := worker.controller.clock.NewTimer(worker.nextBackoff())
		select {
		case <-worker.stop:
			timer.Stop()
			return false
		case <-worker.wake:
			timer.Stop()
			worker.syncUntilStable()
			return true
		case <-timer.C():
		}
		event, hash, _, _, stopped := worker.snapshot()
		if stopped {
			return false
		}
		if len(event.Ready) == 0 {
			worker.syncUntilStable()
			return true
		}
		ctx, finish, active := worker.activate(hash)
		if !active {
			worker.syncUntilStable()
			return true
		}
		err, panicked := worker.callRegistration(ctx, event)
		finish()
		if err != nil {
			if panicked {
				worker.logFailure("WorkerPanic")
				continue
			}
			if errors.Is(err, context.Canceled) && worker.wasSuperseded(hash) {
				worker.syncUntilStable()
				return true
			}
			worker.logFailure("RegistrationFailed")
			continue
		}
		if worker.wasSuperseded(hash) {
			worker.syncUntilStable()
			return true
		}
		worker.resetFailures()
		worker.markRegistered(event)
		return true
	}
}

func (worker *discoveryWorker) callRegistration(ctx context.Context, event EndpointEvent) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			err = errors.New("discovery registration panicked")
			panicked = true
		}
	}()
	return worker.controller.syncer.Register(ctx, event), false
}

func (worker *discoveryWorker) logFailure(reason string) {
	_, _, event, _, _ := worker.snapshot()
	service := event.ServiceName
	if service == "" {
		worker.mu.Lock()
		service = worker.latest.ServiceName
		worker.mu.Unlock()
	}
	logf(worker.controller.log, "AIFAR discovery sync failed service=%s reason=%s\n", service, reason)
}

func canonicalEndpointEvent(event EndpointEvent) EndpointEvent {
	event.InstanceID = strings.TrimSpace(event.InstanceID)
	event.ServiceName = normalizeServiceManifestName(event.ServiceName)
	event.AppName = strings.TrimSpace(event.AppName)
	ready := append([]ReadyEndpoint(nil), event.Ready...)
	for index := range ready {
		ready[index].PodID = strings.TrimSpace(ready[index].PodID)
		ready[index].ContainerName = strings.TrimSpace(ready[index].ContainerName)
		ready[index].Revision = strings.TrimSpace(ready[index].Revision)
	}
	sort.Slice(ready, func(i, j int) bool {
		left, right := ready[i], ready[j]
		if left.PodID != right.PodID {
			return left.PodID < right.PodID
		}
		if left.ContainerName != right.ContainerName {
			return left.ContainerName < right.ContainerName
		}
		if left.Revision != right.Revision {
			return left.Revision < right.Revision
		}
		return left.Port < right.Port
	})
	deduplicated := ready[:0]
	for _, candidate := range ready {
		if len(deduplicated) > 0 && deduplicated[len(deduplicated)-1] == candidate {
			continue
		}
		deduplicated = append(deduplicated, candidate)
	}
	event.Ready = deduplicated
	return event
}

func validEndpointEvent(event EndpointEvent) bool {
	if validateInstanceManifestName(event.InstanceID) != nil || validateServiceManifestName(event.ServiceName) != nil {
		return false
	}
	if event.AppName == "" || !validManifestPort(event.ListenPort) {
		return false
	}
	for _, endpoint := range event.Ready {
		if endpoint.PodID == "" || endpoint.ContainerName == "" || endpoint.Revision == "" || !validManifestPort(endpoint.Port) {
			return false
		}
	}
	return true
}

func endpointEventHash(event EndpointEvent) string {
	data, _ := json.Marshal(canonicalEndpointEvent(event))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func registrationIdentityHash(event EndpointEvent) string {
	payload := struct {
		InstanceID  string `json:"instanceId"`
		ServiceName string `json:"serviceName"`
		AppName     string `json:"appName"`
		ListenPort  int    `json:"listenPort"`
	}{
		InstanceID:  event.InstanceID,
		ServiceName: event.ServiceName,
		AppName:     event.AppName,
		ListenPort:  event.ListenPort,
	}
	data, _ := json.Marshal(payload)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (m *Manager) publishDeploymentEndpoints(config InstanceConfig, manifest DeploymentManifest) {
	if m.discoveryController == nil {
		return
	}
	key := endpointKey(manifest.Metadata.InstanceID, manifest.Metadata.Name)
	m.mu.RLock()
	items := append([]endpoint(nil), m.endpoints[key]...)
	m.mu.RUnlock()
	ready := make([]ReadyEndpoint, 0, len(items))
	for _, item := range items {
		port := manifest.Service.TargetPort
		if _, rawPort, err := net.SplitHostPort(item.Address); err == nil {
			if parsed, parseErr := strconv.Atoi(rawPort); parseErr == nil {
				port = parsed
			}
		}
		ready = append(ready, ReadyEndpoint{
			PodID:         item.Container,
			ContainerName: item.Container,
			Revision:      manifest.Spec.PodRevision,
			Port:          port,
		})
	}
	m.discoveryController.EndpointChanged(EndpointEvent{
		InstanceID:  config.InstanceID,
		ServiceName: manifest.Metadata.Name,
		AppName:     manifest.Service.AppName,
		ListenPort:  manifest.Service.ListenPort,
		Ready:       ready,
	})
}

func (syncer *managerDiscoverySyncer) RefreshRoutes(ctx context.Context, event EndpointEvent) error {
	manager := syncer.manager
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := manager.manifestStore.GetInstance(event.InstanceID)
	if err != nil {
		return errors.New("instance config unavailable")
	}
	manifest, err := manager.manifestStore.Get(event.InstanceID, event.ServiceName)
	if err != nil {
		return errors.New("deployment manifest unavailable")
	}
	if manifest.Service.ListenPort != event.ListenPort || manifest.Service.AppName != event.AppName {
		return errors.New("endpoint event identity mismatch")
	}

	manager.mu.Lock()
	if current, ok := manager.routes[event.ListenPort]; ok && (current.InstanceID != event.InstanceID || current.Service != event.ServiceName) {
		manager.mu.Unlock()
		return errors.New("proxy route port is already assigned")
	}
	previousSpec, hadPreviousSpec := manager.specs[event.InstanceID]
	previousDeployment, hadPreviousDeployment := discoveryDeployment(previousSpec.Deployments, event.ServiceName)
	previousService, hadPreviousService := discoveryService(previousSpec.Services, event.ServiceName)
	previousRoutes := map[int]proxyRoute{}
	oldPorts := make([]int, 0)
	for port, route := range manager.routes {
		if route.InstanceID != event.InstanceID || route.Service != event.ServiceName {
			continue
		}
		previousRoutes[port] = route
		if port != event.ListenPort {
			delete(manager.routes, port)
			oldPorts = append(oldPorts, port)
		}
	}
	spec := manager.specs[event.InstanceID]
	if spec.InstanceID == "" {
		spec = runtimeSpecForDeployment(config, manifest)
	} else {
		spec.Version = DefaultAgentVersion
		spec.InstanceID = config.InstanceID
		spec.InstallRoot = config.InstallRoot
		spec.Network = config.Network
		spec.Ingress = config.Ingress
		spec.Deployments = replaceDiscoveryDeployment(spec.Deployments, manifest.Spec)
		spec.Services = replaceDiscoveryService(spec.Services, manifest.Service)
	}
	manager.specs[event.InstanceID] = spec
	manager.routes[event.ListenPort] = proxyRoute{
		InstanceID: event.InstanceID,
		Service:    event.ServiceName,
		WebIngress: event.ServiceName == config.Ingress.WebService,
	}
	_, listening := manager.servers[event.ListenPort]
	manager.mu.Unlock()
	if !listening {
		startPort := syncer.startPort
		if startPort == nil {
			startPort = manager.startPort
		}
		if err := startPort(event.ListenPort); err != nil {
			manager.mu.Lock()
			if current, ok := manager.routes[event.ListenPort]; ok && current.InstanceID == event.InstanceID && current.Service == event.ServiceName {
				delete(manager.routes, event.ListenPort)
			}
			newerRoute := false
			for port, route := range manager.routes {
				if route.InstanceID == event.InstanceID && route.Service == event.ServiceName && port != event.ListenPort {
					if _, wasPrevious := previousRoutes[port]; !wasPrevious {
						newerRoute = true
						break
					}
				}
			}
			if !newerRoute {
				for port, route := range previousRoutes {
					if _, claimed := manager.routes[port]; !claimed {
						manager.routes[port] = route
					}
				}
			}
			currentSpec := manager.specs[event.InstanceID]
			currentDeployment, hasCurrentDeployment := discoveryDeployment(currentSpec.Deployments, event.ServiceName)
			if hasCurrentDeployment && sameDiscoveryDeployment(currentDeployment, manifest.Spec) {
				if hadPreviousDeployment {
					currentSpec.Deployments = replaceDiscoveryDeployment(currentSpec.Deployments, previousDeployment)
				} else {
					currentSpec.Deployments = removeDiscoveryDeployment(currentSpec.Deployments, event.ServiceName)
				}
			}
			currentService, hasCurrentService := discoveryService(currentSpec.Services, event.ServiceName)
			if hasCurrentService && currentService == manifest.Service {
				if hadPreviousService {
					currentSpec.Services = replaceDiscoveryService(currentSpec.Services, previousService)
				} else {
					currentSpec.Services = removeDiscoveryService(currentSpec.Services, event.ServiceName)
				}
			}
			if !hadPreviousSpec && len(currentSpec.Deployments) == 0 && len(currentSpec.Services) == 0 {
				delete(manager.specs, event.InstanceID)
			} else {
				manager.specs[event.InstanceID] = currentSpec
			}
			manager.mu.Unlock()
			return errors.New("proxy listener unavailable")
		}
	}
	sort.Ints(oldPorts)
	serversToStop := make([]*http.Server, 0, len(oldPorts))
	manager.mu.Lock()
	for _, port := range oldPorts {
		if _, used := manager.routes[port]; used {
			continue
		}
		if server := manager.servers[port]; server != nil {
			delete(manager.servers, port)
			serversToStop = append(serversToStop, server)
		}
	}
	manager.mu.Unlock()
	for _, server := range serversToStop {
		syncer.retireServer(server)
	}
	return nil
}

func (syncer *managerDiscoverySyncer) retireServer(server *http.Server) {
	shutdownServer := syncer.shutdownServer
	if shutdownServer == nil {
		shutdownServer = shutdownRetiredDiscoveryServer
	}
	go func() {
		defer recoverRuntimeAgentPanic(syncer.manager.log, "retired runtime proxy shutdown")
		shutdownServer(server)
	}()
}

func shutdownRetiredDiscoveryServer(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), discoveryListenerShutdownTimeout)
	err := server.Shutdown(ctx)
	cancel()
	if err != nil {
		_ = server.Close()
	}
}

func (syncer *managerDiscoverySyncer) Register(ctx context.Context, event EndpointEvent) error {
	config, err := syncer.manager.manifestStore.GetInstance(event.InstanceID)
	if err != nil {
		return errors.New("instance config unavailable")
	}
	return syncNacosDiscoveryEvent(ctx, config, event, NacosProxyRegister)
}

func (syncer *managerDiscoverySyncer) Deregister(ctx context.Context, event EndpointEvent) error {
	config, err := syncer.manager.manifestStore.GetInstance(event.InstanceID)
	if err != nil {
		return errors.New("instance config unavailable")
	}
	return syncNacosDiscoveryEvent(ctx, config, event, NacosProxyDeregister)
}

func (syncer *managerDiscoverySyncer) Heartbeat(ctx context.Context, event EndpointEvent) error {
	config, err := syncer.manager.manifestStore.GetInstance(event.InstanceID)
	if err != nil {
		return errors.New("instance config unavailable")
	}
	return heartbeatNacosDiscoveryEvent(ctx, config, event)
}

func replaceDiscoveryDeployment(items []DeploymentSpec, replacement DeploymentSpec) []DeploymentSpec {
	out := make([]DeploymentSpec, 0, len(items)+1)
	replaced := false
	for _, item := range items {
		if item.ServiceName == replacement.ServiceName {
			if !replaced {
				out = append(out, replacement)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}

func discoveryDeployment(items []DeploymentSpec, serviceName string) (DeploymentSpec, bool) {
	for _, item := range items {
		if item.ServiceName == serviceName {
			return item, true
		}
	}
	return DeploymentSpec{}, false
}

func removeDiscoveryDeployment(items []DeploymentSpec, serviceName string) []DeploymentSpec {
	out := make([]DeploymentSpec, 0, len(items))
	for _, item := range items {
		if item.ServiceName != serviceName {
			out = append(out, item)
		}
	}
	return out
}

func sameDiscoveryDeployment(left, right DeploymentSpec) bool {
	leftJSON, leftErr := json.Marshal(left)
	rightJSON, rightErr := json.Marshal(right)
	return leftErr == nil && rightErr == nil && string(leftJSON) == string(rightJSON)
}

func replaceDiscoveryService(items []ServiceSpec, replacement ServiceSpec) []ServiceSpec {
	out := make([]ServiceSpec, 0, len(items)+1)
	replaced := false
	for _, item := range items {
		if item.Name == replacement.Name {
			if !replaced {
				out = append(out, replacement)
				replaced = true
			}
			continue
		}
		out = append(out, item)
	}
	if !replaced {
		out = append(out, replacement)
	}
	return out
}

func discoveryService(items []ServiceSpec, serviceName string) (ServiceSpec, bool) {
	for _, item := range items {
		if item.Name == serviceName {
			return item, true
		}
	}
	return ServiceSpec{}, false
}

func removeDiscoveryService(items []ServiceSpec, serviceName string) []ServiceSpec {
	out := make([]ServiceSpec, 0, len(items))
	for _, item := range items {
		if item.Name != serviceName {
			out = append(out, item)
		}
	}
	return out
}
