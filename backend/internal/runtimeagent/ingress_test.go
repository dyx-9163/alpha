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
	mu         sync.Mutex
	calls      []string
	endpointIP string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	f.mu.Lock()
	f.calls = append(f.calls, call)
	endpointIP := f.endpointIP
	f.mu.Unlock()
	if strings.Contains(call, " ps ") {
		return CommandResult{Stdout: "aifar-pod-admin-gateway-r1\n"}, nil
	}
	if strings.Contains(call, " inspect ") {
		ip := endpointIP
		if ip == "" {
			ip = "172.20.0.10"
		}
		return CommandResult{Stdout: "true|healthy|" + ip + "\n"}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func (f *fakeRunner) callsString() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.calls, "\n")
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
	calls := runner.callsString()
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
	gotFirst := manager.selectEndpoint(first, "admin", "file", "stable", endpoints)
	gotSecond := manager.selectEndpoint(second, "admin", "file", "stable", endpoints)
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
	gotFirst := manager.selectEndpoint(first, "admin", "gateway", "stable", endpoints)
	gotSecond := manager.selectEndpoint(second, "admin", "gateway", "stable", endpoints)
	if gotFirst != gotSecond {
		t.Fatalf("expected same authenticated client to use same gateway endpoint, got %#v and %#v", gotFirst, gotSecond)
	}
}

func TestManagerUsesRoundRobinWhenAffinityPolicyDisablesStickyRouting(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	endpoints := []endpoint{
		{Container: "aifar-pod-admin-oauth-a", Address: "172.20.0.31:38001"},
		{Container: "aifar-pod-admin-oauth-b", Address: "172.20.0.32:38001"},
	}
	first := httptest.NewRequest("GET", "http://alpha-oauth/user", nil)
	first.Header.Set("Authorization", "Bearer user-token")
	second := httptest.NewRequest("GET", "http://alpha-oauth/user", nil)
	second.Header.Set("Authorization", "Bearer user-token")
	gotFirst := manager.selectEndpoint(first, "admin", "oauth", "round-robin", endpoints)
	gotSecond := manager.selectEndpoint(second, "admin", "oauth", "round-robin", endpoints)
	if gotFirst == gotSecond {
		t.Fatalf("expected round-robin policy to rotate endpoints, got %#v twice", gotFirst)
	}
}

func TestManagerReconcilesDeploymentsConcurrently(t *testing.T) {
	runner := newConcurrentDeploymentRunner("oauth", "system")
	defer runner.releaseRuns()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	oauthPort := freePort(t)
	systemPort := freePort(t)
	gatewayPort := freePort(t)
	webPort := freePort(t)
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{
			{
				ServiceName: "oauth",
				Image:       "aifar-oauth:rev-1",
				PodRevision: "rev-1",
				Replicas:    1,
				Ports:       []ContainerPort{{Name: "http", ContainerPort: oauthPort}},
			},
			{
				ServiceName: "system",
				Image:       "aifar-system:rev-1",
				PodRevision: "rev-1",
				Replicas:    1,
				Ports:       []ContainerPort{{Name: "http", ContainerPort: systemPort}},
			},
		},
		Services: []ServiceSpec{
			{Name: "oauth", AppName: "alpha-oauth", Port: oauthPort, TargetPort: oauthPort},
			{Name: "system", AppName: "alpha-system", Port: systemPort, TargetPort: systemPort},
		},
		Ingress: IngressSpec{
			GatewayPort: gatewayPort,
			WebPort:     webPort,
		},
	})
	applyDone := make(chan error, 1)
	go func() {
		applyDone <- manager.Apply(context.Background(), spec)
	}()

	select {
	case <-runner.allStarted:
	case err := <-applyDone:
		t.Fatalf("apply finished before both deployments started: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("deployments were not reconciled concurrently")
	}

	runner.releaseRuns()
	if err := <-applyDone; err != nil {
		t.Fatalf("apply failed: %v", err)
	}
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
}

func TestManagerSerializesApplyAndResyncDuringScaleOut(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "file",
		Image:       "aifar-file:rev-1",
		PodRevision: "rev-1",
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
			PodRevision: "rev-1",
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

func TestManagerRollsBackNewPodsWhenRollingUpdateFails(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "oauth",
		Image:       "aifar-oauth:rev-2",
		PodRevision: "rev-2",
		Replicas:    2,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	runner := newRollbackRunner(deploymentSpecHash(deployment))
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{deployment},
		Services:    []ServiceSpec{{Name: "oauth", AppName: "alpha-oauth", Port: 38001, TargetPort: 38001}},
		Ingress: IngressSpec{
			GatewayPort: freePort(t),
			WebPort:     freePort(t),
		},
	})
	if err := manager.Apply(context.Background(), spec); err == nil {
		t.Fatal("expected rolling update failure")
	}
	if !runner.removed("aifar-pod-admin-oauth-rev-2-r1") {
		t.Fatalf("expected new revision replica 1 to be rolled back, removals:\n%s", strings.Join(runner.removedCalls(), "\n"))
	}
	for _, call := range runner.removedCalls() {
		if strings.Contains(call, "rev-1") {
			t.Fatalf("old revision should not be removed during failed rollout, got %s", call)
		}
	}
}

func TestManagerReplacesSameRevisionDriftAfterReplacementIsReady(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "oauth",
		Image:       "aifar-oauth:rev-1",
		PodRevision: "rev-1",
		Replicas:    1,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	runner := &sameRevisionReplaceRunner{hash: deploymentSpecHash(deployment)}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{deployment},
		Services:    []ServiceSpec{{Name: "oauth", AppName: "alpha-oauth", Port: 38001, TargetPort: 38001}},
	})

	if err := manager.ensureDeployment(context.Background(), spec, deployment); err != nil {
		t.Fatal(err)
	}
	calls := runner.callsString()
	runIndex := strings.Index(calls, "docker run -d")
	backupIndex := strings.Index(calls, "docker rename aifar-pod-admin-oauth-rev-1-r1")
	if runIndex < 0 || backupIndex < 0 || runIndex > backupIndex {
		t.Fatalf("expected replacement container to be ready before old pod is renamed, got:\n%s", calls)
	}
	if strings.Contains(calls, "docker rm -f aifar-pod-admin-oauth-rev-1-r1\n") {
		t.Fatalf("old stable pod name should not be removed before replacement promotion, got:\n%s", calls)
	}
}

func TestManagerReturnsErrorWhenRemovingDriftedPodFails(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "oauth",
		Image:       "aifar-oauth:rev-2",
		PodRevision: "rev-2",
		Replicas:    1,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	runner := &removeExtraFailureRunner{hash: deploymentSpecHash(deployment)}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{deployment},
		Services:    []ServiceSpec{{Name: "oauth", AppName: "alpha-oauth", Port: 38001, TargetPort: 38001}},
	})

	err := manager.removeExtraReplicas(context.Background(), spec, deployment)

	if err == nil || !strings.Contains(err.Error(), "remove drifted AIFAR pods for oauth") {
		t.Fatalf("expected drifted pod removal error, got %v", err)
	}
	if !runner.removed("aifar-pod-admin-oauth-rev-1-r1") {
		t.Fatalf("expected old revision removal attempt, got:\n%s", strings.Join(runner.removals, "\n"))
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

type sameRevisionReplaceRunner struct {
	mu    sync.Mutex
	hash  string
	calls []string
}

func (r *sameRevisionReplaceRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(call, `{{index .Config.Labels "aifar.spec-hash"}}`):
		return CommandResult{Stdout: "oldhash\n"}, nil
	case strings.Contains(call, "docker run "):
		return CommandResult{Stdout: "new-container\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|") && strings.Contains(call, "NetworkSettings"):
		return CommandResult{Stdout: "true|healthy|172.20.0.50\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1|1|rev-1|" + r.hash + "\n"}, nil
	case strings.Contains(call, "docker ps "):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1\n"}, nil
	case strings.Contains(call, "docker rename"):
		return CommandResult{Stdout: "renamed\n"}, nil
	case strings.Contains(call, "docker rm -f"):
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *sameRevisionReplaceRunner) callsString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

type removeExtraFailureRunner struct {
	hash     string
	removals []string
}

func (r *removeExtraFailureRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1|1|rev-1|oldhash\naifar-pod-admin-oauth-rev-2-r1|1|rev-2|" + r.hash + "\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker rm -f"):
		r.removals = append(r.removals, call)
		if strings.Contains(call, "rev-1") {
			return CommandResult{Stderr: "permission denied\n"}, errors.New("permission denied")
		}
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *removeExtraFailureRunner) removed(container string) bool {
	for _, call := range r.removals {
		if strings.Contains(call, container) {
			return true
		}
	}
	return false
}

type concurrentDeploymentRunner struct {
	mu             sync.Mutex
	expected       map[string]bool
	started        map[string]bool
	allStarted     chan struct{}
	allStartedOnce sync.Once
	release        chan struct{}
	releaseOnce    sync.Once
}

func newConcurrentDeploymentRunner(services ...string) *concurrentDeploymentRunner {
	expected := map[string]bool{}
	for _, service := range services {
		expected[service] = true
	}
	return &concurrentDeploymentRunner{
		expected:   expected,
		started:    map[string]bool{},
		allStarted: make(chan struct{}),
		release:    make(chan struct{}),
	}
}

func (r *concurrentDeploymentRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		return CommandResult{}, errors.New("not found")
	case strings.Contains(call, "docker run "):
		r.markStarted(containerNameArg(args))
		select {
		case <-r.release:
			return CommandResult{Stdout: "new-container\n"}, nil
		case <-ctx.Done():
			return CommandResult{}, ctx.Err()
		}
	case strings.Contains(call, "NetworkSettings"):
		return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps"):
		return CommandResult{}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *concurrentDeploymentRunner) markStarted(container string) {
	service := ""
	for candidate := range r.expected {
		if strings.Contains(container, "-"+candidate+"-") {
			service = candidate
			break
		}
	}
	if service == "" {
		return
	}
	r.mu.Lock()
	r.started[service] = true
	allStarted := len(r.started) == len(r.expected)
	r.mu.Unlock()
	if allStarted {
		r.allStartedOnce.Do(func() { close(r.allStarted) })
	}
}

func (r *concurrentDeploymentRunner) releaseRuns() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func containerNameArg(args []string) string {
	for i, arg := range args {
		if arg == "--name" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

type scaleOutRaceRunner struct {
	mu         sync.Mutex
	hash       string
	ready      chan struct{}
	runStarted chan struct{}
	runOnce    sync.Once
	removed    []string
	r2Started  bool
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
			r.mu.Lock()
			r.r2Started = true
			r.mu.Unlock()
			r.runOnce.Do(func() { close(r.runStarted) })
		}
		return CommandResult{Stdout: "new-container\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|") && strings.Contains(call, "NetworkSettings"):
		return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		r.mu.Lock()
		r2Started := r.r2Started
		r.mu.Unlock()
		if strings.HasSuffix(call, "r2") && r2Started {
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

type rollbackRunner struct {
	mu       sync.Mutex
	hash     string
	removals []string
}

func newRollbackRunner(hash string) *rollbackRunner {
	return &rollbackRunner{hash: hash}
}

func (r *rollbackRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		if strings.Contains(call, "rev-2") {
			return CommandResult{}, errors.New("not found")
		}
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(call, `{{index .Config.Labels "aifar.spec-hash"}}`):
		return CommandResult{Stdout: r.hash + "\n"}, nil
	case strings.Contains(call, "docker run ") && strings.Contains(call, "rev-2-r2"):
		return CommandResult{}, errors.New("image pull failed")
	case strings.Contains(call, "docker run "):
		return CommandResult{Stdout: "new-container\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|") && strings.Contains(call, "NetworkSettings"):
		return CommandResult{Stdout: "true|healthy|172.20.0.40\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1|1|rev-1|oldhash\naifar-pod-admin-oauth-rev-1-r2|2|rev-1|oldhash\naifar-pod-admin-oauth-rev-2-r1|1|rev-2|" + r.hash + "\n"}, nil
	case strings.Contains(call, "docker ps "):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1\naifar-pod-admin-oauth-rev-1-r2\naifar-pod-admin-oauth-rev-2-r1\n"}, nil
	case strings.Contains(call, "docker rm -f"):
		r.mu.Lock()
		r.removals = append(r.removals, call)
		r.mu.Unlock()
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *rollbackRunner) removed(container string) bool {
	for _, call := range r.removedCalls() {
		if strings.Contains(call, container) {
			return true
		}
	}
	return false
}

func (r *rollbackRunner) removedCalls() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.removals))
	copy(out, r.removals)
	return out
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

var freePortState = struct {
	sync.Mutex
	used map[int]bool
}{used: map[int]bool{}}

func freePort(t *testing.T) int {
	t.Helper()
	for attempts := 0; attempts < 100; attempts++ {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		_, port, err := net.SplitHostPort(listener.Addr().String())
		_ = listener.Close()
		if err != nil {
			t.Fatal(err)
		}
		n, err := strconv.Atoi(port)
		if err != nil {
			t.Fatal(err)
		}
		freePortState.Lock()
		used := freePortState.used[n]
		if !used {
			freePortState.used[n] = true
		}
		freePortState.Unlock()
		if !used {
			return n
		}
	}
	t.Fatal("could not allocate a unique test port")
	return 0
}
