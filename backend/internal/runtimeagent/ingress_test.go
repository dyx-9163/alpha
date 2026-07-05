package runtimeagent

import (
	"context"
	"errors"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeRunner struct {
	calls      []string
	endpointIP string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if strings.Contains(call, " ps ") {
		return CommandResult{Stdout: "aifar-pod-admin-gateway-r1\n"}, nil
	}
	if strings.Contains(call, " inspect ") {
		ip := f.endpointIP
		if ip == "" {
			ip = "172.20.0.10"
		}
		return CommandResult{Stdout: "true|healthy|" + ip + "\n"}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func TestManagerAppliesRuntimeSpecAsHostListeners(t *testing.T) {
	gatewayPort := freePort(t)
	webPort := freePort(t)
	runner := &fakeRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "gateway", AppName: "alpha-gateway", Port: gatewayPort},
			{Name: "web-vue3", Port: webPort},
		},
		Ingress: IngressSpec{
			GatewayService: "gateway",
			WebService:     "web-vue3",
			GatewayPort:    gatewayPort,
			WebPort:        webPort,
		},
	}
	if err := manager.Apply(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	listeners, _ := status["listeners"].([]int)
	if len(listeners) != 2 || listeners[0] != gatewayPort || listeners[1] != webPort {
		t.Fatalf("expected host listeners on gateway/web ports, got %#v", status["listeners"])
	}
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerDiscoversReadyDockerPodEndpoints(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "gateway", Port: 38000},
			{Name: "web-vue3", Port: 8080},
		},
	})
	endpoints, err := manager.discoverEndpoints(context.Background(), spec, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].Address != "172.20.0.10:38000" {
		t.Fatalf("unexpected endpoints: %#v", endpoints)
	}
	calls := strings.Join(runner.calls, "\n")
	for _, want := range []string{
		"docker ps --filter label=aifar.app=aifar",
		"--filter label=aifar.component=pod",
		"--filter label=aifar.service=gateway",
		"docker inspect -f",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("expected docker call containing %q, got:\n%s", want, calls)
		}
	}
}

func TestManagerResyncRefreshesEndpointCache(t *testing.T) {
	gatewayPort := freePort(t)
	webPort := freePort(t)
	runner := &fakeRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "gateway", AppName: "alpha-gateway", Port: gatewayPort},
			{Name: "web-vue3", Port: webPort},
		},
		Ingress: IngressSpec{
			GatewayService: "gateway",
			WebService:     "web-vue3",
			GatewayPort:    gatewayPort,
			WebPort:        webPort,
		},
	}
	if err := manager.Apply(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	runner.endpointIP = "172.20.0.11"
	if err := manager.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	endpoints := manager.cachedEndpoints("admin", "gateway")
	if len(endpoints) != 1 || endpoints[0].Address != "172.20.0.11:"+strconv.Itoa(gatewayPort) {
		t.Fatalf("expected resync to refresh endpoint cache, got %#v", endpoints)
	}
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerKeepsFileUploadChunksOnSameEndpoint(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	endpoints := []endpoint{
		{Container: "aifar-pod-admin-file-b", Address: "172.20.0.12:38005"},
		{Container: "aifar-pod-admin-file-a", Address: "172.20.0.11:38005"},
	}
	first := httptest.NewRequest("POST", "http://alpha-file/upload?identifier=upload-1&chunkNumber=1", nil)
	second := httptest.NewRequest("POST", "http://alpha-file/upload?identifier=upload-1&chunkNumber=2", nil)
	gotFirst := manager.selectEndpoint(first, "admin", "file", endpoints)
	gotSecond := manager.selectEndpoint(second, "admin", "file", endpoints)
	if gotFirst != gotSecond {
		t.Fatalf("expected chunks with same upload identifier to use same file endpoint, got %#v and %#v", gotFirst, gotSecond)
	}
}

func TestManagerUsesGatewayAffinityForSameClient(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	endpoints := []endpoint{
		{Container: "aifar-pod-admin-gateway-a", Address: "172.20.0.21:38000"},
		{Container: "aifar-pod-admin-gateway-b", Address: "172.20.0.22:38000"},
	}
	first := httptest.NewRequest("GET", "http://aifar/api/users", nil)
	first.Header.Set("Authorization", "Bearer user-token")
	second := httptest.NewRequest("GET", "http://aifar/api/files", nil)
	second.Header.Set("Authorization", "Bearer user-token")
	gotFirst := manager.selectEndpoint(first, "admin", "gateway", endpoints)
	gotSecond := manager.selectEndpoint(second, "admin", "gateway", endpoints)
	if gotFirst != gotSecond {
		t.Fatalf("expected same authenticated client to use same gateway endpoint, got %#v and %#v", gotFirst, gotSecond)
	}
}

func TestManagerSerializesApplyAndResyncDuringScaleOut(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "file",
		Image:       "aifar-file:rev-1",
		Revision:    "rev-1",
		Replicas:    2,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38005}},
	}
	runner := newScaleOutRaceRunner(deploymentSpecHash(deployment))
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	oldSpec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{{
			ServiceName: "file",
			Image:       "aifar-file:rev-1",
			Revision:    "rev-1",
			Replicas:    1,
			Ports:       []ContainerPort{{Name: "http", ContainerPort: 38005}},
		}},
		Services: []ServiceSpec{{Name: "file", AppName: "alpha-file", Port: 38005, TargetPort: 38005}},
	})
	newSpec := oldSpec
	newSpec.Deployments = []DeploymentSpec{deployment}

	manager.mu.Lock()
	manager.specs[oldSpec.InstanceID] = oldSpec
	manager.mu.Unlock()

	applyDone := make(chan error, 1)
	go func() {
		applyDone <- manager.Apply(context.Background(), newSpec)
	}()

	select {
	case <-runner.runStarted:
	case err := <-applyDone:
		t.Fatalf("apply finished before replica 2 started: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("replica 2 was not started")
	}

	resyncDone := make(chan error, 1)
	go func() {
		resyncDone <- manager.Resync(context.Background())
	}()

	select {
	case err := <-resyncDone:
		t.Fatalf("resync was not serialized with apply, err=%v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(runner.ready)
	if err := <-applyDone; err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if err := <-resyncDone; err != nil {
		t.Fatalf("resync failed: %v", err)
	}
	if runner.removedReplica2() {
		t.Fatal("resync removed replica 2 while scale-out apply was in progress")
	}
}

func TestManagerContainerReadyDiagnosticsIncludesInspectAndLogs(t *testing.T) {
	runner := &diagnosticRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	got := manager.containerReadyDiagnostics(context.Background(), "aifar-pod-admin-file-rev-r2", "false|unhealthy")
	for _, want := range []string{
		"last inspect: false|unhealthy",
		"inspect: status=exited",
		"health log:",
		"connection refused",
		"logs:",
		"application failed to start",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected diagnostics to contain %q, got:\n%s", want, got)
		}
	}
}

type scaleOutRaceRunner struct {
	mu         sync.Mutex
	hash       string
	ready      chan struct{}
	runStarted chan struct{}
	runOnce    sync.Once
	removed    []string
}

func newScaleOutRaceRunner(hash string) *scaleOutRaceRunner {
	return &scaleOutRaceRunner{
		hash:       hash,
		ready:      make(chan struct{}),
		runStarted: make(chan struct{}),
	}
}

func (r *scaleOutRaceRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		if strings.HasSuffix(call, "r2") {
			return CommandResult{}, errors.New("not found")
		}
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(call, `{{index .Config.Labels "aifar.spec-hash"}}`):
		return CommandResult{Stdout: r.hash + "\n"}, nil
	case strings.Contains(call, "docker run "):
		if strings.Contains(call, "r2") {
			r.runOnce.Do(func() { close(r.runStarted) })
		}
		return CommandResult{Stdout: "new-container\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|") && strings.Contains(call, "NetworkSettings"):
		return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		if strings.HasSuffix(call, "r2") {
			select {
			case <-r.ready:
			case <-ctx.Done():
				return CommandResult{}, ctx.Err()
			}
		}
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-file-rev-1-r1|1|rev-1\naifar-pod-admin-file-rev-1-r2|2|rev-1\n"}, nil
	case strings.Contains(call, "docker ps "):
		return CommandResult{Stdout: "aifar-pod-admin-file-rev-1-r1\naifar-pod-admin-file-rev-1-r2\n"}, nil
	case strings.Contains(call, "docker rm -f"):
		r.mu.Lock()
		r.removed = append(r.removed, call)
		r.mu.Unlock()
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *scaleOutRaceRunner) removedReplica2() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, call := range r.removed {
		if strings.Contains(call, "r2") {
			return true
		}
	}
	return false
}

type diagnosticRunner struct{}

func (diagnosticRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "status={{.State.Status}}"):
		return CommandResult{Stdout: "status=exited running=false exitCode=1 error= oomKilled=false health=unhealthy\n"}, nil
	case strings.Contains(call, "State.Health.Log"):
		return CommandResult{Stdout: "2026-07-05 exit= 1 output= connection refused\n"}, nil
	case strings.Contains(call, "docker logs"):
		return CommandResult{Stdout: "application failed to start\n"}, nil
	default:
		return CommandResult{}, nil
	}
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
