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
	if err := r.Manager.Apply(ctx, spec); err != nil {
		return err
	}
	if err := SyncNacosProxyRegistrations(ctx, NacosProxySyncOptions{
		Specs:  []RuntimeSpec{NormalizeSpec(spec)},
		Action: NacosProxyRegister,
		Log:    r.Log,
	}); err != nil {
		logf(r.Log, "sync AIFAR Nacos proxies after runtime reconcile failed: %v\n", err)
	}
	logf(r.Log, "AIFAR agent reconciled runtime instance %s\n", NormalizeSpec(spec).InstanceID)
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
		stateDir:  stateDir,
		runner:    runner,
		log:       options.Log,
		specs:     map[string]RuntimeSpec{},
		routes:    map[int]proxyRoute{},
		servers:   map[int]*http.Server{},
		next:      map[string]uint64{},
		endpoints: map[string][]endpoint{},
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
			"instanceId":  spec.InstanceID,
			"installRoot": spec.InstallRoot,
			"network":     spec.Network,
			"deployments": spec.Deployments,
			"services":    spec.Services,
			"ingress":     spec.Ingress,
			"endpoints":   m.endpointsForInstanceLocked(spec.InstanceID),
			"nacos":       spec.Nacos,
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
	if interval <= 0 {
		interval = 30 * time.Second
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
			}
		}
	}
}

func (m *Manager) StartDockerEventSync(ctx context.Context, debounce time.Duration) {
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
		ep := m.selectEndpoint(r, spec.InstanceID, service, endpoints)
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
	for _, deployment := range spec.Deployments {
		if strings.TrimSpace(deployment.Image) == "" {
			continue
		}
		if err := m.ensureDeployment(ctx, spec, deployment); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) ensureDeployment(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec) error {
	for replica := 1; replica <= deployment.Replicas; replica++ {
		name := containerNameForDeployment(spec, deployment, replica)
		exists, err := m.containerExists(ctx, name)
		if err != nil {
			return err
		}
		if exists {
			recreate, err := m.containerNeedsRecreate(ctx, name, deployment)
			if err != nil {
				return err
			}
			if recreate {
				if _, err := m.runner.Run(ctx, "docker", "rm", "-f", name); err != nil {
					return fmt.Errorf("replace drifted AIFAR pod %s: %w", name, err)
				}
				exists = false
			}
		}
		if !exists {
			if err := m.runContainer(ctx, spec, deployment, replica, name); err != nil {
				return err
			}
		}
	}
	return m.removeExtraReplicas(ctx, spec, deployment)
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
		return false, nil
	}
	current := strings.TrimSpace(result.Stdout)
	return current == "" || current != deploymentSpecHash(deployment), nil
}

func (m *Manager) runContainer(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name string) error {
	args := []string{"run", "-d", "--name", name, "--restart", "unless-stopped"}
	args = append(args,
		"--label", "aifar.app=aifar",
		"--label", "aifar.runtime-version=2",
		"--label", "aifar.spec-hash="+deploymentSpecHash(deployment),
		"--label", "aifar.install-root="+spec.InstallRoot,
		"--label", "aifar.component=pod",
		"--label", "aifar.instance="+spec.InstanceID,
		"--label", "aifar.service="+deployment.ServiceName,
		"--label", "aifar.revision="+deployment.Revision,
		"--label", "aifar.release="+deployment.Revision,
		"--label", fmt.Sprintf("aifar.pod=%s", name),
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
			value = strings.ReplaceAll(value, "${containerName}", name)
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
	if err := m.waitContainerReady(ctx, name); err != nil {
		return err
	}
	logf(m.log, "AIFAR runtime pod started service=%s replica=%d container=%s\n", deployment.ServiceName, replica, name)
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
	result, err := m.runner.Run(ctx, "docker",
		"ps", "-a",
		"--filter", "label=aifar.app=aifar",
		"--filter", "label=aifar.install-root="+spec.InstallRoot,
		"--filter", "label=aifar.component=pod",
		"--filter", "label=aifar.service="+deployment.ServiceName,
		"--format", `{{.Names}}|{{.Label "aifar.replica"}}|{{.Label "aifar.revision"}}`,
	)
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(result.Stdout), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		replicaText := strings.TrimSpace(parts[1])
		revision := ""
		if len(parts) == 3 {
			revision = strings.TrimSpace(parts[2])
		}
		replica, _ := strconv.Atoi(strings.TrimSpace(replicaText))
		revisionDrifted := strings.TrimSpace(deployment.Revision) != "" && revision != "" && revision != strings.TrimSpace(deployment.Revision)
		if replica > deployment.Replicas || revisionDrifted {
			_, _ = m.runner.Run(ctx, "docker", "rm", "-f", name)
			logf(m.log, "AIFAR runtime pod removed service=%s replica=%d container=%s\n", deployment.ServiceName, replica, name)
		}
	}
	return nil
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
	}
	m.mu.Unlock()
	return nil
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
		"--format", "{{.Names}}",
	)
	if err != nil {
		return nil, fmt.Errorf("list AIFAR service pods: %w", err)
	}
	names := strings.Fields(result.Stdout)
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

func (m *Manager) selectEndpoint(r *http.Request, instanceID, service string, endpoints []endpoint) endpoint {
	if key := affinityKeyForRequest(service, r); key != "" {
		return endpoints[int(stableHash(key)%uint64(len(endpoints)))]
	}
	return m.pickEndpoint(instanceID, service, endpoints)
}

func (m *Manager) pickEndpoint(instanceID, service string, endpoints []endpoint) endpoint {
	key := instanceID + "/" + service
	m.mu.Lock()
	index := m.next[key]
	m.next[key] = index + 1
	m.mu.Unlock()
	return endpoints[int(index%uint64(len(endpoints)))]
}

func affinityKeyForRequest(service string, r *http.Request) string {
	if !serviceNeedsAffinity(service) || r == nil {
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
	revision := strings.TrimSpace(deployment.Revision)
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
		ServiceName string            `json:"serviceName"`
		Image       string            `json:"image,omitempty"`
		Revision    string            `json:"revision,omitempty"`
		Ports       []ContainerPort   `json:"ports,omitempty"`
		EnvFiles    []string          `json:"envFiles,omitempty"`
		Volumes     []VolumeMount     `json:"volumes,omitempty"`
		Resources   ResourceSpec      `json:"resources,omitempty"`
		HealthCheck HealthCheckSpec   `json:"healthCheck,omitempty"`
		Entrypoint  []string          `json:"entrypoint,omitempty"`
		Command     []string          `json:"command,omitempty"`
		Environment map[string]string `json:"environment,omitempty"`
		Labels      map[string]string `json:"labels,omitempty"`
	}
	data, _ := json.Marshal(hashDeployment{
		ServiceName: deployment.ServiceName,
		Image:       deployment.Image,
		Revision:    deployment.Revision,
		Ports:       deployment.Ports,
		EnvFiles:    deployment.EnvFiles,
		Volumes:     deployment.Volumes,
		Resources:   deployment.Resources,
		HealthCheck: deployment.HealthCheck,
		Entrypoint:  deployment.Entrypoint,
		Command:     deployment.Command,
		Environment: deployment.Environment,
		Labels:      deployment.Labels,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
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
