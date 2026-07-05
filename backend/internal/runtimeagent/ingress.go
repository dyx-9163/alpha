package runtimeagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
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
	mu       sync.RWMutex
	stateDir string
	runner   CommandRunner
	log      io.Writer
	specs    map[string]RuntimeSpec
	routes   map[int]proxyRoute
	servers  map[int]*http.Server
	next     map[string]uint64
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
		stateDir: stateDir,
		runner:   runner,
		log:      options.Log,
		specs:    map[string]RuntimeSpec{},
		routes:   map[int]proxyRoute{},
		servers:  map[int]*http.Server{},
		next:     map[string]uint64{},
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
	spec = NormalizeSpec(spec)
	if err := validateRuntimeSpec(spec); err != nil {
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
	instanceID = strings.TrimSpace(instanceID)
	if instanceID == "" {
		instanceID = "admin"
	}
	portsToStop := []int{}
	m.mu.Lock()
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
			"services":    spec.Services,
			"ingress":     spec.Ingress,
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
			"reconcile-runtime",
			"status",
		},
	}
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
		endpoints, err := m.discoverEndpoints(r.Context(), spec, service)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		if len(endpoints) == 0 {
			http.Error(w, "AIFAR runtime service has no ready endpoints", http.StatusServiceUnavailable)
			return
		}
		ep := m.pickEndpoint(spec.InstanceID, service, endpoints)
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
	if route.WebIngress && (strings.HasPrefix(path, "/api/") || strings.HasPrefix(path, "/im/ws/")) {
		service = spec.Ingress.GatewayService
	}
	return spec, service, true
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
			Address:   net.JoinHostPort(ip, strconv.Itoa(serviceSpec.Port)),
		})
	}
	return endpoints, nil
}

func (m *Manager) pickEndpoint(instanceID, service string, endpoints []endpoint) endpoint {
	key := instanceID + "/" + service
	m.mu.Lock()
	index := m.next[key]
	m.next[key] = index + 1
	m.mu.Unlock()
	return endpoints[int(index%uint64(len(endpoints)))]
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
		if service.Port <= 0 {
			return fmt.Errorf("runtime service %s port must be positive", service.Name)
		}
		if previous := ports[service.Port]; previous != "" && previous != service.Name {
			return fmt.Errorf("runtime port %d is used by both %s and %s", service.Port, previous, service.Name)
		}
		ports[service.Port] = service.Name
	}
	return nil
}

func routesForSpec(spec RuntimeSpec) map[int]proxyRoute {
	routes := map[int]proxyRoute{}
	for _, service := range spec.Services {
		routes[service.Port] = proxyRoute{
			InstanceID: spec.InstanceID,
			Service:    service.Name,
			WebIngress: service.Name == spec.Ingress.WebService,
		}
	}
	routes[spec.Ingress.GatewayPort] = proxyRoute{InstanceID: spec.InstanceID, Service: spec.Ingress.GatewayService}
	routes[spec.Ingress.WebPort] = proxyRoute{InstanceID: spec.InstanceID, Service: spec.Ingress.WebService, WebIngress: true}
	return routes
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
