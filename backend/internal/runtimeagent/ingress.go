package runtimeagent

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Reconciler struct {
	Manager *Manager
	Log     io.Writer
}

func (r Reconciler) ReconcileRuntime(ctx context.Context, spec RuntimeSpec) error {
	if r.Manager == nil {
		return errors.New("runtime manager is required")
	}
	spec = NormalizeSpec(spec)
	if err := r.Manager.Apply(ctx, spec); err != nil {
		return err
	}
	if err := SyncNacosProxyRegistrations(ctx, NacosProxySyncOptions{
		Specs:             []RuntimeSpec{spec},
		Action:            NacosProxyRegister,
		Log:               r.Log,
		RequireConfigured: true,
	}); err != nil {
		logf(r.Log, "sync AIFAR Nacos proxies after runtime reconcile failed: %v\n", err)
		r.Manager.MarkNacosProxyStatus([]RuntimeSpec{spec}, err)
		return nil
	}
	r.Manager.MarkNacosProxyStatus([]RuntimeSpec{spec}, nil)
	logf(r.Log, "AIFAR agent reconciled runtime instance %s\n", spec.InstanceID)
	return nil
}

func (r Reconciler) ReconcileIngress(ctx context.Context, spec RuntimeSpec) error {
	return r.ReconcileRuntime(ctx, spec)
}

type ManagerOptions struct {
	StateDir string
	Runner   CommandRunner
	Log      io.Writer
}

type Manager struct {
	mu          sync.RWMutex
	reconcileMu sync.Mutex
	stateDir    string
	runner      CommandRunner
	log         io.Writer
	specs       map[string]RuntimeSpec
	routes      map[int]proxyRoute
	servers     map[int]*http.Server
	next        map[string]uint64
	endpoints   map[string][]endpoint
	deployments map[string]deploymentRuntimeStatus
	services    map[string]serviceRuntimeStatus
}

type proxyRoute struct {
	InstanceID string
	Service    string
	WebIngress bool
}

type endpoint struct {
	Container string
	Address   string
}

type deploymentRuntimeStatus struct {
	InstanceID        string                 `json:"instanceId"`
	ServiceName       string                 `json:"serviceName"`
	DeploymentName    string                 `json:"deploymentName,omitempty"`
	PodRevision       string                 `json:"podRevision,omitempty"`
	Image             string                 `json:"image,omitempty"`
	Strategy          DeploymentStrategySpec `json:"strategy"`
	DesiredReplicas   int                    `json:"desiredReplicas"`
	CurrentReplicas   int                    `json:"currentReplicas"`
	ReadyReplicas     int                    `json:"readyReplicas"`
	UpdatedReplicas   int                    `json:"updatedReplicas"`
	AvailableReplicas int                    `json:"availableReplicas"`
	Status            string                 `json:"status"`
	LastReconcileAt   string                 `json:"lastReconcileAt,omitempty"`
	LastError         string                 `json:"lastError,omitempty"`
}

type serviceRuntimeStatus struct {
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

type deploymentPodState struct {
	Name     string
	Replica  int
	Revision string
	SpecHash string
	Running  bool
	Healthy  bool
}

func NewManager(options ManagerOptions) *Manager {
	stateDir := strings.TrimSpace(options.StateDir)
	if stateDir == "" {
		stateDir = DefaultStateDir
	}
	runner := options.Runner
	if runner == nil {
		runner = ExecRunner{}
	}
	return &Manager{
		stateDir:    stateDir,
		runner:      runner,
		log:         options.Log,
		specs:       map[string]RuntimeSpec{},
		routes:      map[int]proxyRoute{},
		servers:     map[int]*http.Server{},
		next:        map[string]uint64{},
		endpoints:   map[string][]endpoint{},
		deployments: map[string]deploymentRuntimeStatus{},
		services:    map[string]serviceRuntimeStatus{},
	}
}

func (m *Manager) Load(ctx context.Context) error {
	entries, err := os.ReadDir(m.stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		spec, err := readSpecFile(filepath.Join(m.stateDir, entry.Name(), "runtime-spec.json"))
		if err != nil {
			logf(m.log, "skip invalid AIFAR runtime spec %s: %v\n", entry.Name(), err)
			continue
		}
		if err := m.Apply(ctx, spec); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) Apply(ctx context.Context, spec RuntimeSpec) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	spec = NormalizeSpec(spec)
	if err := validateRuntimeSpec(spec); err != nil {
		return err
	}
	if err := m.reconcileDeployments(ctx, spec); err != nil {
		return err
	}
	if err := m.refreshInstanceEndpoints(ctx, spec); err != nil {
		return err
	}
	routes := routesForSpec(spec)
	portsToStart := make([]int, 0, len(routes))
	m.mu.Lock()
	m.specs[spec.InstanceID] = spec
	for port, route := range routes {
		m.routes[port] = route
		if _, ok := m.servers[port]; !ok {
			portsToStart = append(portsToStart, port)
		}
	}
	m.mu.Unlock()
	sort.Ints(portsToStart)
	for _, port := range portsToStart {
		if err := m.startPort(port); err != nil {
			return err
		}
	}
	if err := m.writeSpec(spec); err != nil {
		return err
	}
	logf(m.log, "AIFAR runtime applied instance=%s ports=%v\n", spec.InstanceID, sortedRoutePorts(routes))
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (m *Manager) RestartAll(ctx context.Context, spec RuntimeSpec) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	spec = NormalizeSpec(spec)
	if err := validateRuntimeSpec(spec); err != nil {
		return err
	}
	plan, err := m.preflightRestartAll(ctx, spec)
	if err != nil {
		return err
	}
	if err := m.stopAllRuntimePods(ctx, spec); err != nil {
		return err
	}
	if err := m.refreshInstanceEndpoints(ctx, spec); err != nil {
		return fmt.Errorf("refresh AIFAR runtime endpoints after stop-all: %w", err)
	}

	started := make([]restartReplicaPlan, 0, len(plan))
	restartErrs := make([]error, 0)
	for _, deployment := range spec.Deployments {
		status := "pending"
		if deployment.Replicas <= 0 {
			status = "offline"
		}
		m.setDeploymentRestartPhase(spec, deployment, status)
	}
	for _, item := range plan {
		if err := ctx.Err(); err != nil {
			m.refreshRestartAllPartialState(spec)
			return err
		}
		if err := m.runContainerDetached(ctx, spec, item.deployment, item.replica, item.name); err != nil {
			restartErrs = append(restartErrs, fmt.Errorf("start AIFAR deployment %s replica %d: %w", item.deployment.ServiceName, item.replica, err))
			continue
		}
		started = append(started, item)
	}

	restartErrs = append(restartErrs, m.verifyRestartedRuntime(ctx, spec, started)...)
	if err := m.refreshInstanceEndpoints(context.WithoutCancel(ctx), spec); err != nil {
		restartErrs = append(restartErrs, fmt.Errorf("refresh AIFAR runtime endpoints after restart-all: %w", err))
	}
	for _, deployment := range spec.Deployments {
		lastError := ""
		status := "ready"
		if deployment.Replicas <= 0 {
			status = "offline"
		}
		serviceErrs := make([]string, 0)
		needle := "deployment " + deployment.ServiceName
		for _, restartErr := range restartErrs {
			if restartErr != nil && strings.Contains(restartErr.Error(), needle) {
				serviceErrs = append(serviceErrs, restartErr.Error())
			}
		}
		if len(serviceErrs) > 0 {
			status = "failed"
			lastError = strings.Join(serviceErrs, "; ")
		}
		m.setDeploymentStatusFromDocker(context.WithoutCancel(ctx), spec, deployment, status, lastError)
	}
	if len(restartErrs) > 0 {
		return fmt.Errorf("restart all AIFAR runtime pods: %w", errors.Join(restartErrs...))
	}
	return nil
}

func (m *Manager) refreshRestartAllPartialState(spec RuntimeSpec) {
	refreshCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := m.refreshInstanceEndpoints(refreshCtx, spec); err != nil {
		logf(m.log, "AIFAR runtime partial restart endpoint refresh failed: %v\n", err)
	}
	for _, deployment := range spec.Deployments {
		status := "pending"
		if deployment.Replicas <= 0 {
			status = "offline"
		}
		m.setDeploymentStatusFromDocker(refreshCtx, spec, deployment, status, "")
		m.mu.Lock()
		key := endpointKey(spec.InstanceID, deployment.ServiceName)
		current := m.deployments[key]
		if current.LastError == "" {
			current.Status = status
			m.deployments[key] = current
		}
		m.mu.Unlock()
	}
}

func (m *Manager) setDeploymentRestartPhase(spec RuntimeSpec, deployment DeploymentSpec, status string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := endpointKey(spec.InstanceID, deployment.ServiceName)
	current := m.deployments[key]
	current.InstanceID = spec.InstanceID
	current.ServiceName = deployment.ServiceName
	current.DeploymentName = deployment.DeploymentName
	current.PodRevision = deployment.PodRevision
	current.Image = deployment.Image
	current.Strategy = NormalizeDeploymentStrategy(deployment.Strategy)
	current.DesiredReplicas = deployment.Replicas
	current.CurrentReplicas = 0
	current.ReadyReplicas = 0
	current.UpdatedReplicas = 0
	current.AvailableReplicas = 0
	current.Status = status
	current.LastReconcileAt = time.Now().Format(time.RFC3339)
	current.LastError = ""
	m.deployments[key] = current
}

type restartReplicaPlan struct {
	deployment DeploymentSpec
	replica    int
	name       string
}

func (m *Manager) preflightRestartAll(ctx context.Context, spec RuntimeSpec) ([]restartReplicaPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := m.runner.Run(ctx, "docker", "network", "inspect", spec.Network); err != nil {
		return nil, fmt.Errorf("preflight AIFAR runtime network %s: %w", spec.Network, err)
	}
	checkedImages := map[string]bool{}
	plan := make([]restartReplicaPlan, 0)
	for _, raw := range spec.Deployments {
		deployment := raw
		deployment.Strategy = NormalizeDeploymentStrategy(deployment.Strategy)
		if deployment.Replicas <= 0 {
			continue
		}
		if strings.TrimSpace(deployment.Image) == "" {
			return nil, fmt.Errorf("preflight AIFAR deployment %s: image is required", deployment.ServiceName)
		}
		if !checkedImages[deployment.Image] {
			if _, err := m.runner.Run(ctx, "docker", "image", "inspect", deployment.Image); err != nil {
				return nil, fmt.Errorf("preflight AIFAR deployment %s image %s: %w", deployment.ServiceName, deployment.Image, err)
			}
			checkedImages[deployment.Image] = true
		}
		for _, path := range deployment.EnvFiles {
			if path = strings.TrimSpace(path); path != "" {
				if _, err := os.Stat(path); err != nil {
					return nil, fmt.Errorf("preflight AIFAR deployment %s env file %s: %w", deployment.ServiceName, path, err)
				}
			}
		}
		for _, volume := range deployment.Volumes {
			if source := strings.TrimSpace(volume.Source); source != "" {
				if _, err := os.Stat(source); err != nil {
					return nil, fmt.Errorf("preflight AIFAR deployment %s volume source %s: %w", deployment.ServiceName, source, err)
				}
			}
		}
		for replica := 1; replica <= deployment.Replicas; replica++ {
			plan = append(plan, restartReplicaPlan{
				deployment: deployment,
				replica:    replica,
				name:       containerNameForDeployment(spec, deployment, replica),
			})
		}
	}
	return plan, nil
}

func (m *Manager) listInstanceRuntimePods(ctx context.Context, spec RuntimeSpec) ([]string, error) {
	result, err := m.runner.Run(ctx, "docker",
		"ps", "-a",
		"--filter", "label=aifar.app=aifar",
		"--filter", "label=aifar.install-root="+spec.InstallRoot,
		"--filter", "label=aifar.component=pod",
		"--format", `{{.Names}}`,
	)
	if err != nil {
		return nil, fmt.Errorf("list AIFAR runtime pods for instance %s: %w", spec.InstanceID, err)
	}
	names := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names, nil
}

func (m *Manager) stopAllRuntimePods(ctx context.Context, spec RuntimeSpec) error {
	names, err := m.listInstanceRuntimePods(ctx, spec)
	if err != nil {
		return err
	}
	removeErrs := make([]error, 0)
	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return err
		}
		if _, err := m.runner.Run(ctx, "docker", "rm", "-f", name); err != nil {
			removeErrs = append(removeErrs, fmt.Errorf("remove AIFAR runtime pod %s: %w", name, err))
			continue
		}
		logf(m.log, "AIFAR runtime pod removed before restart-all container=%s\n", name)
	}
	remaining, listErr := m.listInstanceRuntimePods(context.WithoutCancel(ctx), spec)
	if listErr != nil {
		removeErrs = append(removeErrs, listErr)
	} else if len(remaining) > 0 {
		removeErrs = append(removeErrs, fmt.Errorf("AIFAR runtime pods remain after stop-all: %s", strings.Join(remaining, ", ")))
	}
	if len(removeErrs) > 0 {
		return fmt.Errorf("stop all AIFAR runtime pods: %w", errors.Join(removeErrs...))
	}
	return nil
}

func (m *Manager) verifyRestartedRuntime(ctx context.Context, spec RuntimeSpec, started []restartReplicaPlan) []error {
	errs := make([]error, 0)
	for _, item := range started {
		deploymentCtx := ctx
		cancel := func() {}
		if item.deployment.Strategy.ProgressDeadlineSeconds > 0 {
			deploymentCtx, cancel = context.WithTimeout(ctx, time.Duration(item.deployment.Strategy.ProgressDeadlineSeconds)*time.Second)
		}
		if err := m.waitContainerReady(deploymentCtx, item.name); err != nil {
			errs = append(errs, fmt.Errorf("verify AIFAR deployment %s replica %d: %w", item.deployment.ServiceName, item.replica, err))
		}
		cancel()
	}
	for _, deployment := range spec.Deployments {
		pods, err := m.listDeploymentPods(context.WithoutCancel(ctx), spec, deployment)
		if err != nil {
			errs = append(errs, fmt.Errorf("verify AIFAR deployment %s: %w", deployment.ServiceName, err))
			continue
		}
		current := len(pods)
		ready := 0
		updated := 0
		desiredHash := deploymentSpecHash(deployment)
		for _, pod := range pods {
			if pod.Healthy {
				ready++
			}
			if pod.Replica > 0 && pod.Replica <= deployment.Replicas && (pod.SpecHash == "" || pod.SpecHash == desiredHash) {
				updated++
			}
		}
		if current != deployment.Replicas || ready != deployment.Replicas || updated != deployment.Replicas {
			errs = append(errs, fmt.Errorf("verify AIFAR deployment %s replicas: desired=%d current=%d ready=%d updated=%d available=%d", deployment.ServiceName, deployment.Replicas, current, ready, updated, ready))
		}
	}
	return errs
}

func (m *Manager) Remove(ctx context.Context, instanceID string) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instanceID = "admin"
	}
	portsToStop := []int{}
	var spec RuntimeSpec
	var hasSpec bool
	m.mu.Lock()
	spec, hasSpec = m.specs[instanceID]
	delete(m.specs, instanceID)
	m.deleteInstanceRuntimeStatusLocked(instanceID)
	for port, route := range m.routes {
		if route.InstanceID != instanceID {
			continue
		}
		delete(m.routes, port)
		if !m.portStillUsedLocked(port) {
			portsToStop = append(portsToStop, port)
		}
	}
	m.mu.Unlock()
	for _, port := range portsToStop {
		m.stopPort(ctx, port)
	}
	if hasSpec {
		if err := SyncNacosProxyRegistrations(ctx, NacosProxySyncOptions{
			Specs:  []RuntimeSpec{spec},
			Action: NacosProxyDeregister,
			Log:    m.log,
		}); err != nil {
			logf(m.log, "sync AIFAR Nacos proxies after runtime remove failed: %v\n", err)
		}
	}
	_ = os.RemoveAll(filepath.Join(m.stateDir, instanceID))
	return nil
}

func (m *Manager) Status() map[string]any {
	m.mu.RLock()
	defer m.mu.RUnlock()
	listeners := make([]int, 0, len(m.servers))
	for port := range m.servers {
		listeners = append(listeners, port)
	}
	sort.Ints(listeners)
	instances := make([]map[string]any, 0, len(m.specs))
	for _, id := range sortedSpecIDs(m.specs) {
		spec := m.specs[id]
		instances = append(instances, map[string]any{
			"instanceId":       spec.InstanceID,
			"installRoot":      spec.InstallRoot,
			"network":          spec.Network,
			"deployments":      spec.Deployments,
			"services":         spec.Services,
			"ingress":          spec.Ingress,
			"endpoints":        m.endpointsForInstanceLocked(spec.InstanceID),
			"deploymentStatus": m.deploymentStatusForInstanceLocked(spec.InstanceID),
			"serviceStatus":    m.serviceStatusForInstanceLocked(spec.InstanceID),
			"nacos":            spec.Nacos,
		})
	}
	return map[string]any{
		"status":    "running",
		"version":   DefaultAgentVersion,
		"listeners": listeners,
		"instances": instances,
		"features": []string{
			"health",
			"host-proxy",
			"local-runtime-controller",
			"nacos-proxy-deregister",
			"nacos-proxy-register",
			"endpoint-cache",
			"docker-events",
			"periodic-resync",
			"reconcile-runtime",
			"restart-runtime",
			"rolling-update",
			"nacos-ready-gate",
			"service-affinity-policy",
			"runtime-status-detail",
			"status",
		},
	}
}

func (m *Manager) Resync(ctx context.Context) error {
	m.reconcileMu.Lock()
	defer m.reconcileMu.Unlock()
	for _, spec := range m.snapshotSpecs() {
		if err := m.reconcileDeployments(ctx, spec); err != nil {
			return err
		}
		if err := m.refreshInstanceEndpoints(ctx, spec); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) StartRuntimeResync(ctx context.Context, interval time.Duration, nacosOptions NacosProxySyncOptions) {
	defer recoverRuntimeAgentPanic(m.log, "periodic resync")
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := m.Resync(ctx); err != nil {
				logf(m.log, "AIFAR runtime periodic resync failed: %v\n", err)
				continue
			}
			specs := m.snapshotSpecs()
			if len(specs) == 0 {
				continue
			}
			options := nacosOptions
			options.Specs = specs
			options.Action = NacosProxyRegister
			if err := SyncNacosProxyRegistrations(ctx, options); err != nil {
				logf(m.log, "AIFAR runtime periodic Nacos replay failed: %v\n", err)
				m.MarkNacosProxyStatus(specs, err)
			} else {
				m.MarkNacosProxyStatus(specs, nil)
			}
		}
	}
}

func (m *Manager) StartDockerEventSync(ctx context.Context, debounce time.Duration) {
	defer recoverRuntimeAgentPanic(m.log, "docker event sync")
	if debounce <= 0 {
		debounce = 2 * time.Second
	}
	for ctx.Err() == nil {
		if err := m.watchDockerEvents(ctx, debounce); err != nil && ctx.Err() == nil {
			logf(m.log, "AIFAR Docker event watcher stopped: %v\n", err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}
}

func (m *Manager) watchDockerEvents(ctx context.Context, debounce time.Duration) error {
	cmd := exec.CommandContext(ctx, "docker", "events",
		"--filter", "type=container",
		"--filter", "label=aifar.app=aifar",
		"--format", "{{.TimeNano}} {{.Action}} {{.Actor.Attributes.aifar.service}}",
	)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		data, _ := io.ReadAll(stderr)
		if len(strings.TrimSpace(string(data))) > 0 {
			logf(m.log, "AIFAR Docker events stderr: %s\n", strings.TrimSpace(string(data)))
		}
	}()
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			_ = cmd.Wait()
			return ctx.Err()
		case <-time.After(debounce):
		}
		if err := m.Resync(ctx); err != nil {
			logf(m.log, "AIFAR runtime Docker event resync failed: %v\n", err)
		}
	}
	if err := scanner.Err(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}

func (m *Manager) startPort(port int) error {
	addr := ":" + strconv.Itoa(port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen runtime proxy port %d: %w", port, err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(m.handleProxy(port)),
		ReadHeaderTimeout: 10 * time.Second,
	}
	m.mu.Lock()
	if _, exists := m.servers[port]; exists {
		m.mu.Unlock()
		_ = ln.Close()
		return nil
	}
	m.servers[port] = server
	m.mu.Unlock()
	go func() {
		if err := server.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logf(m.log, "AIFAR runtime proxy port %d stopped: %v\n", port, err)
		}
	}()
	logf(m.log, "AIFAR runtime proxy listening on %s\n", addr)
	return nil
}

func (m *Manager) stopPort(ctx context.Context, port int) {
	m.mu.Lock()
	server := m.servers[port]
	delete(m.servers, port)
	m.mu.Unlock()
	if server != nil {
		_ = server.Shutdown(ctx)
	}
}

func (m *Manager) handleProxy(port int) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		spec, service, ok := m.resolveRoute(port, r.URL.Path)
		if !ok {
			http.Error(w, "AIFAR runtime route is not configured", http.StatusServiceUnavailable)
			return
		}
		endpoints := m.cachedEndpoints(spec.InstanceID, service)
		if len(endpoints) == 0 {
			http.Error(w, "AIFAR runtime service has no ready endpoints", http.StatusServiceUnavailable)
			return
		}
		ep := m.selectEndpoint(r, spec.InstanceID, service, affinityPolicyForService(spec, service), endpoints)
		target := &url.URL{Scheme: "http", Host: ep.Address}
		proxy := httputil.NewSingleHostReverseProxy(target)
		originalDirector := proxy.Director
		proxy.Director = func(req *http.Request) {
			originalDirector(req)
			req.Host = r.Host
			req.Header.Set("X-AIFAR-Upstream", ep.Container)
		}
		proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, proxyErr error) {
			http.Error(rw, proxyErr.Error(), http.StatusBadGateway)
		}
		proxy.ServeHTTP(w, r)
	}
}

func (m *Manager) resolveRoute(port int, path string) (RuntimeSpec, string, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	route, ok := m.routes[port]
	if !ok {
		return RuntimeSpec{}, "", false
	}
	spec, ok := m.specs[route.InstanceID]
	if !ok {
		return RuntimeSpec{}, "", false
	}
	service := route.Service
	if spec.Ingress.Mode != DefaultIngressMode && route.WebIngress && (strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/im/ws/")) {
		service = spec.Ingress.GatewayService
	}
	return spec, service, true
}

func (m *Manager) reconcileDeployments(ctx context.Context, spec RuntimeSpec) error {
	deployments := make([]DeploymentSpec, 0, len(spec.Deployments))
	for _, deployment := range spec.Deployments {
		if strings.TrimSpace(deployment.Image) == "" {
			continue
		}
		deployments = append(deployments, deployment)
	}
	if len(deployments) == 0 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	var errMu sync.Mutex
	var firstErr error
	recordErr := func(deployment DeploymentSpec, err error) {
		if err == nil {
			return
		}
		deploymentName := strings.TrimSpace(deployment.DeploymentName)
		if deploymentName == "" {
			deploymentName = deployment.ServiceName
		}
		err = fmt.Errorf("reconcile AIFAR deployment %s: %w", deploymentName, err)
		errMu.Lock()
		if firstErr == nil {
			firstErr = err
			cancel()
		}
		errMu.Unlock()
	}
	for _, deployment := range deployments {
		deployment := deployment
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if recovered := recover(); recovered != nil {
					logf(m.log, "AIFAR deployment reconcile panic service=%s: %v\n%s\n", deployment.ServiceName, recovered, debug.Stack())
					recordErr(deployment, fmt.Errorf("panic: %v", recovered))
				}
			}()
			if err := m.ensureDeployment(ctx, spec, deployment); err != nil {
				recordErr(deployment, err)
			}
		}()
	}
	wg.Wait()
	return firstErr
}

func (m *Manager) ensureDeployment(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec) error {
	deployment.Strategy = NormalizeDeploymentStrategy(deployment.Strategy)
	if deployment.Strategy.ProgressDeadlineSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(deployment.Strategy.ProgressDeadlineSeconds)*time.Second)
		defer cancel()
	}
	m.setDeploymentStatusFromDocker(ctx, spec, deployment, "rolling", "")
	created := []string{}
	for replica := 1; replica <= deployment.Replicas; replica++ {
		name := containerNameForDeployment(spec, deployment, replica)
		exists, err := m.containerExists(ctx, name)
		if err != nil {
			m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
			return err
		}
		if exists {
			recreate, err := m.containerNeedsRecreate(ctx, name, deployment)
			if err != nil {
				m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
				return err
			}
			if recreate {
				if err := m.replaceContainer(ctx, spec, deployment, replica, name, func(refreshCtx context.Context) error {
					return m.refreshServiceEndpoint(refreshCtx, spec, deployment.ServiceName)
				}); err != nil {
					m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
					return err
				}
				continue
			}
			if err := m.ensureExistingContainerRunning(ctx, deployment, replica, name); err != nil {
				m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
				return err
			}
		}
		if !exists {
			if err := m.runContainer(ctx, spec, deployment, replica, name); err != nil {
				if deploymentRollbackOnFailure(deployment) {
					m.rollbackCreatedPods(ctx, created)
				}
				m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
				return err
			}
			created = append(created, name)
			if err := m.refreshServiceEndpoint(ctx, spec, deployment.ServiceName); err != nil {
				if deploymentRollbackOnFailure(deployment) {
					m.rollbackCreatedPods(ctx, created)
				}
				m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
				return err
			}
		}
	}
	if err := m.removeExtraReplicas(ctx, spec, deployment); err != nil {
		m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
		return err
	}
	if err := m.refreshServiceEndpoint(ctx, spec, deployment.ServiceName); err != nil {
		m.setDeploymentStatusFromDocker(ctx, spec, deployment, "failed", err.Error())
		return err
	}
	m.setDeploymentStatusFromDocker(ctx, spec, deployment, "ready", "")
	return nil
}

func (m *Manager) replaceContainer(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name string, promote func(context.Context) error) error {
	suffix := shortNameHash(fmt.Sprintf("%s-%d", deploymentSpecHash(deployment), time.Now().UnixNano()))
	replacement := sanitizeDockerName(name + "-next-" + suffix)
	backup := sanitizeDockerName(name + "-old-" + suffix)
	committed := false
	defer func() {
		if committed {
			return
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = m.runner.Run(cleanupCtx, "docker", "rm", "-f", replacement)
	}()
	if err := m.runContainerNamed(ctx, spec, deployment, replica, replacement, name); err != nil {
		return err
	}
	if _, err := m.runner.Run(ctx, "docker", "rename", name, backup); err != nil {
		return fmt.Errorf("backup drifted AIFAR pod %s: %w", name, err)
	}
	if _, err := m.runner.Run(ctx, "docker", "rename", replacement, name); err != nil {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, _ = m.runner.Run(cleanupCtx, "docker", "rename", backup, name)
		return fmt.Errorf("promote replacement AIFAR pod %s: %w", name, err)
	}
	if promote != nil {
		if err := promote(ctx); err != nil {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			defer cancel()
			_, _ = m.runner.Run(cleanupCtx, "docker", "rm", "-f", name)
			if _, restoreErr := m.runner.Run(cleanupCtx, "docker", "rename", backup, name); restoreErr != nil {
				return fmt.Errorf("refresh endpoints after replacing AIFAR pod %s: %w (restore backup: %v)", name, err, restoreErr)
			}
			_ = m.refreshServiceEndpoint(cleanupCtx, spec, deployment.ServiceName)
			return fmt.Errorf("refresh endpoints after replacing AIFAR pod %s: %w", name, err)
		}
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if _, err := m.runner.Run(cleanupCtx, "docker", "rm", "-f", backup); err != nil {
		return fmt.Errorf("remove replaced AIFAR pod %s: %w", backup, err)
	}
	committed = true
	logf(m.log, "AIFAR runtime pod replaced service=%s replica=%d container=%s\n", deployment.ServiceName, replica, name)
	return nil
}

func shortNameHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:10]
}

func deploymentRollbackOnFailure(deployment DeploymentSpec) bool {
	if deployment.Strategy.RollbackOnFailure == nil {
		return DefaultDeploymentRollbackOnError
	}
	return *deployment.Strategy.RollbackOnFailure
}

func (m *Manager) rollbackCreatedPods(ctx context.Context, names []string) {
	if len(names) == 0 {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			continue
		}
		_, _ = m.runner.Run(cleanupCtx, "docker", "rm", "-f", name)
		logf(m.log, "AIFAR runtime rollback removed pod container=%s\n", name)
	}
}

func (m *Manager) containerExists(ctx context.Context, name string) (bool, error) {
	_, err := m.runner.Run(ctx, "docker", "inspect", "-f", "{{.Id}}", name)
	if err == nil {
		return true, nil
	}
	return false, nil
}

func (m *Manager) containerNeedsRecreate(ctx context.Context, name string, deployment DeploymentSpec) (bool, error) {
	result, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{index .Config.Labels "aifar.spec-hash"}}`, name)
	if err != nil {
		return false, fmt.Errorf("inspect AIFAR pod spec hash %s: %w", name, err)
	}
	current := strings.TrimSpace(result.Stdout)
	return current == "" || current != deploymentSpecHash(deployment), nil
}

func (m *Manager) ensureExistingContainerRunning(ctx context.Context, deployment DeploymentSpec, replica int, name string) error {
	if _, err := m.runner.Run(ctx, "docker", "update", "--restart", "unless-stopped", name); err != nil {
		logf(m.log, "AIFAR runtime pod restart policy update failed container=%s error=%v\n", name, err)
	}
	running, err := m.containerRunning(ctx, name)
	if err != nil {
		return err
	}
	if running {
		readyRunning, health, err := m.containerReadiness(ctx, name)
		if err != nil {
			return err
		}
		if readyRunning {
			switch strings.ToLower(strings.TrimSpace(health)) {
			case "", "healthy":
				return nil
			case "starting":
				if err := m.waitContainerReady(ctx, name); err != nil {
					return err
				}
				return nil
			default:
				if _, err := m.runner.Run(ctx, "docker", "restart", name); err != nil {
					return fmt.Errorf("restart unhealthy AIFAR pod %s: %w", name, err)
				}
				if err := m.waitContainerReady(ctx, name); err != nil {
					return err
				}
				logf(m.log, "AIFAR runtime pod restarted unhealthy service=%s replica=%d container=%s health=%s\n", deployment.ServiceName, replica, name, health)
				return nil
			}
		}
		// The pod state changed between inspections. Fall through to the stopped
		// recovery path so reconcile can still restore the desired replica.
		running = false
	}
	if running {
		return nil
	}
	if _, err := m.runner.Run(ctx, "docker", "start", name); err != nil {
		return fmt.Errorf("start stopped AIFAR pod %s: %w", name, err)
	}
	if err := m.waitContainerReady(ctx, name); err != nil {
		return err
	}
	logf(m.log, "AIFAR runtime pod recovered service=%s replica=%d container=%s\n", deployment.ServiceName, replica, name)
	return nil
}

func (m *Manager) containerRunning(ctx context.Context, name string) (bool, error) {
	result, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{.State.Running}}`, name)
	if err != nil {
		return false, fmt.Errorf("inspect AIFAR pod running state %s: %w", name, err)
	}
	state := strings.TrimSpace(result.Stdout)
	if strings.Contains(state, "|") {
		state = strings.TrimSpace(strings.SplitN(state, "|", 2)[0])
	}
	return strings.EqualFold(state, "true"), nil
}

func (m *Manager) containerReadiness(ctx context.Context, name string) (bool, string, error) {
	result, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}`, name)
	if err != nil {
		return false, "", fmt.Errorf("inspect AIFAR pod readiness %s: %w", name, err)
	}
	parts := strings.SplitN(strings.TrimSpace(result.Stdout), "|", 2)
	running := len(parts) > 0 && strings.EqualFold(strings.TrimSpace(parts[0]), "true")
	health := ""
	if len(parts) > 1 {
		health = strings.TrimSpace(parts[1])
	}
	return running, health, nil
}

func (m *Manager) runContainer(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name string) error {
	return m.runContainerNamed(ctx, spec, deployment, replica, name, name)
}

func (m *Manager) runContainerNamed(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name, logicalName string) error {
	if err := m.runContainerDetachedNamed(ctx, spec, deployment, replica, name, logicalName); err != nil {
		return err
	}
	if err := m.waitContainerReady(ctx, name); err != nil {
		return err
	}
	logf(m.log, "AIFAR runtime pod started service=%s replica=%d container=%s\n", deployment.ServiceName, replica, name)
	return nil
}

func (m *Manager) runContainerDetached(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name string) error {
	return m.runContainerDetachedNamed(ctx, spec, deployment, replica, name, name)
}

func (m *Manager) runContainerDetachedNamed(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name, logicalName string) error {
	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
	deploymentName := strings.TrimSpace(deployment.DeploymentName)
	if deploymentName == "" {
		deploymentName = deployment.ServiceName
	}
	args = append(args,
		"--log-driver", "json-file",
		"--log-opt", "max-size=50m",
		"--log-opt", "max-file=5",
		"--label", "aifar.app=aifar",
		"--label", "aifar.runtime-version=2",
		"--label", "aifar.spec-hash="+deploymentSpecHash(deployment),
		"--label", "aifar.install-root="+spec.InstallRoot,
		"--label", "aifar.component=pod",
		"--label", "aifar.instance="+spec.InstanceID,
		"--label", "aifar.deployment="+deploymentName,
		"--label", "aifar.service="+deployment.ServiceName,
		"--label", "aifar.revision="+deployment.PodRevision,
		"--label", "aifar.release="+deployment.PodRevision,
		"--label", fmt.Sprintf("aifar.pod=%s", logicalName),
		"--label", fmt.Sprintf("aifar.replica=%d", replica),
		"--network", spec.Network,
		"--add-host", "host.docker.internal:host-gateway",
	)
	for key, value := range deployment.Labels {
		if strings.TrimSpace(key) != "" {
			args = append(args, "--label", key+"="+value)
		}
	}
	if deployment.Resources.CPUs != "" {
		args = append(args, "--cpus", deployment.Resources.CPUs)
	}
	if deployment.Resources.Memory != "" {
		args = append(args, "--memory", deployment.Resources.Memory, "--memory-swap", deployment.Resources.Memory)
	}
	if deployment.HealthCheck.Command != "" {
		args = append(args, "--health-cmd", deployment.HealthCheck.Command)
		if deployment.HealthCheck.Interval != "" {
			args = append(args, "--health-interval", deployment.HealthCheck.Interval)
		}
		if deployment.HealthCheck.Timeout != "" {
			args = append(args, "--health-timeout", deployment.HealthCheck.Timeout)
		}
		if deployment.HealthCheck.Retries > 0 {
			args = append(args, "--health-retries", strconv.Itoa(deployment.HealthCheck.Retries))
		}
		if deployment.HealthCheck.StartPeriod != "" {
			args = append(args, "--health-start-period", deployment.HealthCheck.StartPeriod)
		}
	}
	for _, envFile := range deployment.EnvFiles {
		if strings.TrimSpace(envFile) != "" {
			args = append(args, "--env-file", envFile)
		}
	}
	for key, value := range deployment.Environment {
		if strings.TrimSpace(key) != "" {
			value = strings.ReplaceAll(value, "${containerName}", logicalName)
			args = append(args, "-e", key+"="+value)
		}
	}
	for _, volume := range deployment.Volumes {
		if volume.Source == "" || volume.Target == "" {
			continue
		}
		mount := volume.Source + ":" + volume.Target
		if volume.ReadOnly {
			mount += ":ro"
		}
		args = append(args, "-v", mount)
	}
	if len(deployment.Entrypoint) > 0 {
		args = append(args, "--entrypoint", strings.Join(deployment.Entrypoint, " "))
	}
	args = append(args, deployment.Image)
	args = append(args, deployment.Command...)
	if _, err := m.runner.Run(ctx, "docker", args...); err != nil {
		return fmt.Errorf("start AIFAR pod %s: %w", name, err)
	}
	logf(m.log, "AIFAR runtime pod start submitted service=%s replica=%d container=%s\n", deployment.ServiceName, replica, name)
	return nil
}

func (m *Manager) waitContainerReady(ctx context.Context, name string) error {
	deadline := time.Now().Add(5 * time.Minute)
	lastInspect := ""
	for {
		result, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}`, name)
		if err == nil {
			lastInspect = strings.TrimSpace(result.Stdout)
			parts := strings.SplitN(strings.TrimSpace(result.Stdout), "|", 2)
			running := len(parts) > 0 && parts[0] == "true"
			health := ""
			if len(parts) > 1 {
				health = parts[1]
			}
			if running && (health == "" || health == "healthy") {
				return nil
			}
		} else {
			lastInspect = strings.TrimSpace(err.Error())
			if strings.TrimSpace(result.Stderr) != "" {
				lastInspect += ": " + strings.TrimSpace(result.Stderr)
			}
		}
		if time.Now().After(deadline) {
			diagnostics := m.containerReadyDiagnostics(ctx, name, lastInspect)
			if diagnostics != "" {
				return fmt.Errorf("AIFAR pod did not become ready: %s\n%s", name, diagnostics)
			}
			return fmt.Errorf("AIFAR pod did not become ready: %s", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

func (m *Manager) containerReadyDiagnostics(ctx context.Context, name, lastInspect string) string {
	var b strings.Builder
	if strings.TrimSpace(lastInspect) != "" {
		fmt.Fprintf(&b, "last inspect: %s\n", trimDiagnosticOutput(lastInspect, 1024))
	}
	inspectFormat := `status={{.State.Status}} running={{.State.Running}} exitCode={{.State.ExitCode}} error={{.State.Error}} oomKilled={{.State.OOMKilled}}{{if .State.Health}} health={{.State.Health.Status}}{{end}}`
	if result, err := m.runner.Run(ctx, "docker", "inspect", "-f", inspectFormat, name); err != nil {
		fmt.Fprintf(&b, "inspect failed: %v", err)
		if strings.TrimSpace(result.Stderr) != "" {
			fmt.Fprintf(&b, ": %s", trimDiagnosticOutput(strings.TrimSpace(result.Stderr), 1024))
		}
		b.WriteString("\n")
	} else if strings.TrimSpace(result.Stdout) != "" {
		fmt.Fprintf(&b, "inspect: %s\n", trimDiagnosticOutput(strings.TrimSpace(result.Stdout), 2048))
	}
	healthFormat := `{{if .State.Health}}{{range .State.Health.Log}}{{println .Start "exit=" .ExitCode "output=" .Output}}{{end}}{{end}}`
	if result, err := m.runner.Run(ctx, "docker", "inspect", "-f", healthFormat, name); err == nil && strings.TrimSpace(result.Stdout) != "" {
		fmt.Fprintf(&b, "health log:\n%s\n", trimDiagnosticOutput(strings.TrimSpace(result.Stdout), 4096))
	}
	if result, err := m.runner.Run(ctx, "docker", "logs", "--tail", "120", name); err != nil {
		fmt.Fprintf(&b, "logs failed: %v", err)
		if strings.TrimSpace(result.Stderr) != "" {
			fmt.Fprintf(&b, ": %s", trimDiagnosticOutput(strings.TrimSpace(result.Stderr), 1024))
		}
		b.WriteString("\n")
	} else {
		logs := strings.TrimSpace(strings.TrimSpace(result.Stdout) + "\n" + strings.TrimSpace(result.Stderr))
		if logs != "" {
			fmt.Fprintf(&b, "logs:\n%s\n", trimDiagnosticOutput(logs, 8192))
		}
	}
	return strings.TrimSpace(b.String())
}

func trimDiagnosticOutput(value string, max int) string {
	value = strings.TrimSpace(value)
	if max <= 0 || len(value) <= max {
		return value
	}
	return value[:max] + "...(truncated)"
}

func (m *Manager) removeExtraReplicas(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec) error {
	pods, err := m.listDeploymentPods(ctx, spec, deployment)
	if err != nil {
		return fmt.Errorf("list AIFAR pods for %s: %w", deployment.ServiceName, err)
	}
	desiredHash := deploymentSpecHash(deployment)
	errs := []string{}
	for _, pod := range pods {
		revisionDrifted := strings.TrimSpace(deployment.PodRevision) != "" && pod.Revision != "" && pod.Revision != strings.TrimSpace(deployment.PodRevision)
		specDrifted := pod.SpecHash != "" && pod.SpecHash != desiredHash && (pod.Revision == "" || pod.Revision == strings.TrimSpace(deployment.PodRevision))
		if deployment.Replicas == 0 || pod.Replica > deployment.Replicas || revisionDrifted || specDrifted {
			if _, err := m.runner.Run(ctx, "docker", "rm", "-f", pod.Name); err != nil {
				errs = append(errs, fmt.Sprintf("%s: %v", pod.Name, err))
				continue
			}
			logf(m.log, "AIFAR runtime pod removed service=%s replica=%d container=%s\n", deployment.ServiceName, pod.Replica, pod.Name)
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("remove drifted AIFAR pods for %s: %s", deployment.ServiceName, strings.Join(errs, "; "))
	}
	return nil
}

func (m *Manager) listDeploymentPods(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec) ([]deploymentPodState, error) {
	result, err := m.runner.Run(ctx, "docker",
		"ps", "-a",
		"--filter", "label=aifar.app=aifar",
		"--filter", "label=aifar.install-root="+spec.InstallRoot,
		"--filter", "label=aifar.component=pod",
		"--filter", "label=aifar.service="+deployment.ServiceName,
		"--format", `{{.Names}}|{{.Label "aifar.replica"}}|{{.Label "aifar.revision"}}|{{.Label "aifar.spec-hash"}}`,
	)
	if err != nil {
		return nil, err
	}
	pods := []deploymentPodState{}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) == 0 {
			continue
		}
		pod := deploymentPodState{Name: strings.TrimSpace(parts[0])}
		if pod.Name == "" {
			continue
		}
		if len(parts) > 1 {
			pod.Replica, _ = strconv.Atoi(strings.TrimSpace(parts[1]))
		}
		if len(parts) > 2 {
			pod.Revision = strings.TrimSpace(parts[2])
		}
		if len(parts) > 3 {
			pod.SpecHash = strings.TrimSpace(parts[3])
		}
		inspect, err := m.runner.Run(ctx, "docker", "inspect", "-f", `{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}`, pod.Name)
		if err == nil {
			state := strings.Split(strings.TrimSpace(inspect.Stdout), "|")
			pod.Running = len(state) > 0 && state[0] == "true"
			health := ""
			if len(state) > 1 {
				health = strings.TrimSpace(state[1])
			}
			pod.Healthy = pod.Running && (health == "" || health == "healthy")
		}
		pods = append(pods, pod)
	}
	sort.Slice(pods, func(i, j int) bool { return pods[i].Name < pods[j].Name })
	return pods, nil
}

func (m *Manager) refreshInstanceEndpoints(ctx context.Context, spec RuntimeSpec) error {
	refreshed := map[string][]endpoint{}
	for _, service := range spec.Services {
		endpoints, err := m.discoverEndpoints(ctx, spec, service.Name)
		if err != nil {
			return err
		}
		refreshed[service.Name] = endpoints
	}
	m.mu.Lock()
	for service, endpoints := range refreshed {
		m.endpoints[endpointKey(spec.InstanceID, service)] = endpoints
		if serviceSpec, ok := serviceByName(spec, service); ok {
			m.setServiceEndpointStatusLocked(spec, serviceSpec, endpoints)
		}
	}
	m.mu.Unlock()
	return nil
}

func (m *Manager) refreshServiceEndpoint(ctx context.Context, spec RuntimeSpec, service string) error {
	serviceSpec, ok := serviceByName(spec, service)
	if !ok {
		return fmt.Errorf("AIFAR runtime service %s is not configured", service)
	}
	endpoints, err := m.discoverEndpoints(ctx, spec, service)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.endpoints[endpointKey(spec.InstanceID, service)] = endpoints
	m.setServiceEndpointStatusLocked(spec, serviceSpec, endpoints)
	m.mu.Unlock()
	return nil
}

func (m *Manager) setServiceEndpointStatusLocked(spec RuntimeSpec, service ServiceSpec, endpoints []endpoint) {
	status := "ready"
	if replicas, ok := serviceDesiredReplicas(spec, service.Name); ok && replicas == 0 {
		status = "offline"
	} else if len(endpoints) == 0 {
		status = "no-endpoints"
	}
	key := endpointKey(spec.InstanceID, service.Name)
	current := m.services[key]
	current.InstanceID = spec.InstanceID
	current.ServiceName = service.Name
	current.AppName = serviceAppName(service)
	current.EndpointCount = len(endpoints)
	current.ReadyEndpointCount = len(endpoints)
	current.Status = status
	current.LastEndpointRefreshAt = time.Now().Format(time.RFC3339)
	m.services[key] = current
}

func (m *Manager) cachedEndpoints(instanceID, service string) []endpoint {
	m.mu.RLock()
	defer m.mu.RUnlock()
	items := m.endpoints[endpointKey(instanceID, service)]
	out := make([]endpoint, len(items))
	copy(out, items)
	return out
}

func (m *Manager) snapshotSpecs() []RuntimeSpec {
	m.mu.RLock()
	defer m.mu.RUnlock()
	specs := make([]RuntimeSpec, 0, len(m.specs))
	for _, id := range sortedSpecIDs(m.specs) {
		specs = append(specs, m.specs[id])
	}
	return specs
}

func (m *Manager) endpointsForInstanceLocked(instanceID string) map[string][]endpoint {
	out := map[string][]endpoint{}
	prefix := instanceID + "/"
	for key, items := range m.endpoints {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		service := strings.TrimPrefix(key, prefix)
		copied := make([]endpoint, len(items))
		copy(copied, items)
		out[service] = copied
	}
	return out
}

func (m *Manager) deploymentStatusForInstanceLocked(instanceID string) []deploymentRuntimeStatus {
	out := []deploymentRuntimeStatus{}
	prefix := instanceID + "/"
	for key, status := range m.deployments {
		if strings.HasPrefix(key, prefix) {
			out = append(out, status)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceName < out[j].ServiceName })
	return out
}

func (m *Manager) serviceStatusForInstanceLocked(instanceID string) []serviceRuntimeStatus {
	out := []serviceRuntimeStatus{}
	prefix := instanceID + "/"
	for key, status := range m.services {
		if strings.HasPrefix(key, prefix) {
			out = append(out, status)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ServiceName < out[j].ServiceName })
	return out
}

func (m *Manager) deleteInstanceRuntimeStatusLocked(instanceID string) {
	prefix := instanceID + "/"
	for key := range m.endpoints {
		if strings.HasPrefix(key, prefix) {
			delete(m.endpoints, key)
		}
	}
	for key := range m.deployments {
		if strings.HasPrefix(key, prefix) {
			delete(m.deployments, key)
		}
	}
	for key := range m.services {
		if strings.HasPrefix(key, prefix) {
			delete(m.services, key)
		}
	}
}

func (m *Manager) setDeploymentStatusFromDocker(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, status, lastError string) {
	statusCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	pods, err := m.listDeploymentPods(statusCtx, spec, deployment)
	if err != nil && strings.TrimSpace(lastError) == "" {
		lastError = err.Error()
	}
	desiredHash := deploymentSpecHash(deployment)
	desiredRevision := strings.TrimSpace(deployment.PodRevision)
	runtimeStatus := deploymentRuntimeStatus{
		InstanceID:      spec.InstanceID,
		ServiceName:     deployment.ServiceName,
		DeploymentName:  deployment.DeploymentName,
		PodRevision:     deployment.PodRevision,
		Image:           deployment.Image,
		Strategy:        NormalizeDeploymentStrategy(deployment.Strategy),
		DesiredReplicas: deployment.Replicas,
		Status:          status,
		LastReconcileAt: time.Now().Format(time.RFC3339),
		LastError:       strings.TrimSpace(lastError),
	}
	for _, pod := range pods {
		runtimeStatus.CurrentReplicas++
		if pod.Healthy {
			runtimeStatus.ReadyReplicas++
			runtimeStatus.AvailableReplicas++
		}
		revisionMatches := desiredRevision == "" || pod.Revision == "" || pod.Revision == desiredRevision
		hashMatches := pod.SpecHash == "" || pod.SpecHash == desiredHash
		if pod.Replica <= deployment.Replicas && revisionMatches && hashMatches {
			runtimeStatus.UpdatedReplicas++
		}
	}
	if runtimeStatus.LastError != "" {
		runtimeStatus.Status = "failed"
	} else if deployment.Replicas == 0 {
		runtimeStatus.Status = "offline"
	} else if runtimeStatus.UpdatedReplicas < deployment.Replicas {
		runtimeStatus.Status = "rolling"
	} else if runtimeStatus.ReadyReplicas < deployment.Replicas {
		runtimeStatus.Status = "degraded"
	} else if runtimeStatus.Status == "" {
		runtimeStatus.Status = "ready"
	}
	m.mu.Lock()
	m.deployments[endpointKey(spec.InstanceID, deployment.ServiceName)] = runtimeStatus
	m.mu.Unlock()
}

func (m *Manager) MarkNacosProxyStatus(specs []RuntimeSpec, syncErr error) {
	now := time.Now().Format(time.RFC3339)
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, raw := range specs {
		spec := NormalizeSpec(raw)
		_, hasEnv := nacosEnvForSpec(spec)
		for _, service := range spec.Services {
			if !serviceRegistersInNacos(spec, service) {
				continue
			}
			key := endpointKey(spec.InstanceID, service.Name)
			status := m.services[key]
			status.InstanceID = spec.InstanceID
			status.ServiceName = service.Name
			status.AppName = serviceAppName(service)
			if replicas, ok := serviceDesiredReplicas(spec, service.Name); ok && replicas == 0 {
				status.NacosRegistered = false
				status.NacosReady = false
				status.LastNacosError = ""
				if status.Status == "" || status.Status == "ready" || status.Status == "no-endpoints" {
					status.Status = "offline"
				}
				m.services[key] = status
				continue
			}
			if !hasEnv {
				status.NacosRegistered = false
				status.NacosReady = false
				status.LastNacosError = "nacos env is not configured"
			} else if syncErr != nil {
				status.NacosRegistered = false
				status.NacosReady = false
				status.LastNacosError = strings.TrimSpace(syncErr.Error())
			} else {
				status.NacosRegistered = true
				status.NacosReady = true
				status.LastNacosHeartbeatAt = now
				status.LastNacosError = ""
			}
			if status.Status == "" {
				if status.EndpointCount == 0 {
					status.Status = "no-endpoints"
				} else {
					status.Status = "ready"
				}
			}
			m.services[key] = status
		}
	}
}

func endpointKey(instanceID, service string) string {
	return instanceID + "/" + service
}

func (m *Manager) discoverEndpoints(ctx context.Context, spec RuntimeSpec, service string) ([]endpoint, error) {
	serviceSpec, ok := serviceByName(spec, service)
	if !ok {
		return nil, fmt.Errorf("AIFAR runtime service %s is not configured", service)
	}
	result, err := m.runner.Run(ctx, "docker",
		"ps",
		"--filter", "label=aifar.app=aifar",
		"--filter", "label=aifar.install-root="+spec.InstallRoot,
		"--filter", "label=aifar.component=pod",
		"--filter", "label=aifar.service="+service,
		"--format", `{{.Names}}|{{.Label "aifar.pod"}}`,
	)
	if err != nil {
		return nil, fmt.Errorf("list AIFAR service pods: %w", err)
	}
	names := make([]string, 0)
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		if len(parts) == 2 {
			logicalName := strings.TrimSpace(parts[1])
			if logicalName != "" && logicalName != name {
				continue
			}
		}
		names = append(names, name)
	}
	endpoints := make([]endpoint, 0, len(names))
	format := fmt.Sprintf(`{{.State.Running}}|{{if .State.Health}}{{.State.Health.Status}}{{end}}|{{with index .NetworkSettings.Networks %q}}{{.IPAddress}}{{else}}{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}{{end}}`, spec.Network)
	for _, name := range names {
		inspect, err := m.runner.Run(ctx, "docker", "inspect", "-f", format, name)
		if err != nil {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(inspect.Stdout), "|", 3)
		if len(parts) != 3 {
			continue
		}
		if parts[0] != "true" {
			continue
		}
		if parts[1] != "" && parts[1] != "healthy" {
			continue
		}
		ip := strings.TrimSpace(parts[2])
		if ip == "" {
			continue
		}
		endpoints = append(endpoints, endpoint{
			Container: name,
			Address:   net.JoinHostPort(ip, strconv.Itoa(serviceSpec.TargetPort)),
		})
	}
	sort.Slice(endpoints, func(i, j int) bool {
		return endpoints[i].Container < endpoints[j].Container
	})
	return endpoints, nil
}

func (m *Manager) selectEndpoint(r *http.Request, instanceID, service, policy string, endpoints []endpoint) endpoint {
	if affinityPolicyUsesStableKey(service, policy) {
		if key := affinityKeyForRequest(r); key != "" {
			return endpoints[int(stableHash(key)%uint64(len(endpoints)))]
		}
	}
	if strings.EqualFold(strings.TrimSpace(policy), "random") {
		return endpoints[int(stableHash(strconv.FormatInt(time.Now().UnixNano(), 10))%uint64(len(endpoints)))]
	}
	return m.pickEndpoint(instanceID, service, endpoints)
}

func affinityPolicyForService(spec RuntimeSpec, service string) string {
	if serviceSpec, ok := serviceByName(spec, service); ok {
		return strings.TrimSpace(serviceSpec.AffinityPolicy)
	}
	return ""
}

func affinityPolicyUsesStableKey(service, policy string) bool {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case "stable", "consistent-hash", "consistenthash", "hash", "ip-hash", "iphash", "sticky":
		return true
	case "round-robin", "roundrobin", "none", "off":
		return false
	}
	return serviceNeedsAffinity(service)
}

func (m *Manager) pickEndpoint(instanceID, service string, endpoints []endpoint) endpoint {
	key := instanceID + "/" + service
	m.mu.Lock()
	index := m.next[key]
	m.next[key] = index + 1
	m.mu.Unlock()
	return endpoints[int(index%uint64(len(endpoints)))]
}

func affinityKeyForRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, header := range []string{
		"X-AIFAR-Affinity",
		"X-Upload-Id",
		"X-File-Md5",
		"X-File-Hash",
		"X-Trace-Id",
		"X-Request-Id",
		"Authorization",
	} {
		if value := strings.TrimSpace(r.Header.Get(header)); value != "" {
			return header + ":" + value
		}
	}
	query := r.URL.Query()
	for _, key := range []string{
		"identifier",
		"uploadId",
		"fileMd5",
		"md5",
		"hash",
		"traceId",
		"guid",
		"uuid",
		"fileId",
	} {
		if value := strings.TrimSpace(query.Get(key)); value != "" {
			return "query:" + key + ":" + value
		}
	}
	for _, name := range []string{"aifar-session-token", "token", "SESSION", "JSESSIONID"} {
		if cookie, err := r.Cookie(name); err == nil && strings.TrimSpace(cookie.Value) != "" {
			return "cookie:" + name + ":" + strings.TrimSpace(cookie.Value)
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	if host != "" {
		return "remote:" + host
	}
	return ""
}

func serviceNeedsAffinity(service string) bool {
	switch strings.TrimSpace(service) {
	case "file", "gateway":
		return true
	default:
		return false
	}
}

func stableHash(value string) uint64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(value))
	return hash.Sum64()
}

func (m *Manager) writeSpec(spec RuntimeSpec) error {
	dir := filepath.Join(m.stateDir, spec.InstanceID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(spec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "runtime-spec.json"), append(data, '\n'), 0o644)
}

func (m *Manager) portStillUsedLocked(port int) bool {
	for _, route := range m.routes {
		if route.Service != "" {
			_ = route
		}
	}
	_, ok := m.routes[port]
	return ok
}

func validateRuntimeSpec(spec RuntimeSpec) error {
	if strings.TrimSpace(spec.InstanceID) == "" {
		return errors.New("runtime instance id is required")
	}
	if strings.TrimSpace(spec.InstallRoot) == "" {
		return errors.New("runtime install root is required")
	}
	if strings.TrimSpace(spec.Network) == "" {
		return errors.New("runtime network is required")
	}
	if spec.Ingress.GatewayPort <= 0 || spec.Ingress.WebPort <= 0 {
		return errors.New("runtime proxy ports must be positive")
	}
	if spec.Ingress.GatewayPort == spec.Ingress.WebPort {
		return errors.New("gateway and web ingress ports must be different")
	}
	if _, ok := serviceByName(spec, spec.Ingress.GatewayService); !ok {
		return errors.New("gateway service is missing from runtime services")
	}
	if _, ok := serviceByName(spec, spec.Ingress.WebService); !ok {
		return errors.New("web service is missing from runtime services")
	}
	ports := map[int]string{}
	for _, service := range spec.Services {
		if strings.TrimSpace(service.Name) == "" {
			return errors.New("runtime service name is required")
		}
		if service.ListenPort <= 0 || service.TargetPort <= 0 {
			return fmt.Errorf("runtime service %s port must be positive", service.Name)
		}
		if previous := ports[service.ListenPort]; previous != "" && previous != service.Name {
			return fmt.Errorf("runtime port %d is used by both %s and %s", service.ListenPort, previous, service.Name)
		}
		ports[service.ListenPort] = service.Name
	}
	return nil
}

func routesForSpec(spec RuntimeSpec) map[int]proxyRoute {
	routes := map[int]proxyRoute{}
	for _, service := range spec.Services {
		routes[service.ListenPort] = proxyRoute{
			InstanceID: spec.InstanceID,
			Service:    service.Name,
			WebIngress: service.Name == spec.Ingress.WebService,
		}
	}
	routes[spec.Ingress.GatewayPort] = proxyRoute{InstanceID: spec.InstanceID, Service: spec.Ingress.GatewayService}
	routes[spec.Ingress.WebPort] = proxyRoute{InstanceID: spec.InstanceID, Service: spec.Ingress.WebService, WebIngress: true}
	return routes
}

func containerNameForDeployment(spec RuntimeSpec, deployment DeploymentSpec, replica int) string {
	revision := strings.TrimSpace(deployment.PodRevision)
	if revision == "" {
		revision = "current"
	}
	return sanitizeDockerName(fmt.Sprintf("aifar-pod-%s-%s-%s-r%d", spec.InstanceID, deployment.ServiceName, revision, replica))
}

func sanitizeDockerName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "aifar-pod"
	}
	var b strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	if out == "" {
		return "aifar-pod"
	}
	return out
}

func deploymentSpecHash(deployment DeploymentSpec) string {
	type hashDeployment struct {
		ServiceName    string            `json:"serviceName"`
		DeploymentName string            `json:"deploymentName,omitempty"`
		Image          string            `json:"image,omitempty"`
		PodRevision    string            `json:"podRevision,omitempty"`
		Ports          []ContainerPort   `json:"ports,omitempty"`
		EnvFiles       []string          `json:"envFiles,omitempty"`
		Volumes        []VolumeMount     `json:"volumes,omitempty"`
		Resources      ResourceSpec      `json:"resources,omitempty"`
		HealthCheck    HealthCheckSpec   `json:"healthCheck,omitempty"`
		Entrypoint     []string          `json:"entrypoint,omitempty"`
		Command        []string          `json:"command,omitempty"`
		Environment    map[string]string `json:"environment,omitempty"`
		Labels         map[string]string `json:"labels,omitempty"`
	}
	data, _ := json.Marshal(hashDeployment{
		ServiceName:    deployment.ServiceName,
		DeploymentName: deployment.DeploymentName,
		Image:          deployment.Image,
		PodRevision:    deployment.PodRevision,
		Ports:          deployment.Ports,
		EnvFiles:       deployment.EnvFiles,
		Volumes:        deployment.Volumes,
		Resources:      deployment.Resources,
		HealthCheck:    deployment.HealthCheck,
		Entrypoint:     deployment.Entrypoint,
		Command:        deployment.Command,
		Environment:    deployment.Environment,
		Labels:         deployment.Labels,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func recoverRuntimeAgentPanic(log io.Writer, component string) {
	if recovered := recover(); recovered != nil {
		logf(log, "AIFAR runtime agent %s panic: %v\n%s\n", component, recovered, debug.Stack())
	}
}

func serviceByName(spec RuntimeSpec, name string) (ServiceSpec, bool) {
	for _, service := range spec.Services {
		if service.Name == name {
			return service, true
		}
	}
	return ServiceSpec{}, false
}

func readSpecFile(path string) (RuntimeSpec, error) {
	var spec RuntimeSpec
	data, err := os.ReadFile(path)
	if err != nil {
		return spec, err
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		return spec, err
	}
	return NormalizeSpec(spec), nil
}

func sortedRoutePorts(routes map[int]proxyRoute) []int {
	ports := make([]int, 0, len(routes))
	for port := range routes {
		ports = append(ports, port)
	}
	sort.Ints(ports)
	return ports
}

func sortedSpecIDs(specs map[string]RuntimeSpec) []string {
	ids := make([]string, 0, len(specs))
	for id := range specs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func logf(w io.Writer, format string, args ...any) {
	if w != nil {
		_, _ = fmt.Fprintf(w, format, args...)
	}
}
