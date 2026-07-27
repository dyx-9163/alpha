package runtimeagent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http/httptest"
	"slices"
	"sort"
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

func TestManagerRestartAllRestoresMissingDesiredPod(t *testing.T) {
	runner := newStopAllRestartRunner("aifar-pod-admin-gateway-rev-1-r1")
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})

	if err := manager.RestartAll(context.Background(), restartAllSpec()); err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{
		"aifar-pod-admin-contacts-rev-1-r1",
		"aifar-pod-admin-contacts-rev-1-r2",
		"aifar-pod-admin-gateway-rev-1-r1",
	} {
		if !runner.hasContainer(name) {
			t.Fatalf("missing desired pod %s after restart-all: %#v", name, runner.containerNames())
		}
	}
}

func TestManagerRestartAllLeavesZeroReplicaDeploymentOffline(t *testing.T) {
	runner := newStopAllRestartRunner("aifar-pod-admin-message-rev-old-r1")
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})

	if err := manager.RestartAll(context.Background(), restartAllSpec()); err != nil {
		t.Fatal(err)
	}
	if runner.hasServiceContainer("message") {
		t.Fatalf("zero-replica deployment must remain offline: %#v", runner.containerNames())
	}
}

func TestManagerRestartAllStopsEveryPodBeforeStartingAnyPod(t *testing.T) {
	runner := newStopAllRestartRunner(
		"aifar-pod-admin-contacts-rev-1-r1",
		"aifar-pod-admin-contacts-rev-1-r2",
		"aifar-pod-admin-gateway-rev-1-r1",
		"aifar-pod-admin-contacts-rev-old-r3",
	)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})

	if err := manager.RestartAll(context.Background(), restartAllSpec()); err != nil {
		t.Fatal(err)
	}

	calls := runner.callsString()
	lastRemove := strings.LastIndex(calls, "docker rm -f")
	firstRun := strings.Index(calls, "docker run -d")
	if lastRemove < 0 || firstRun < 0 || lastRemove > firstRun {
		t.Fatalf("all old pods must be removed before any new pod starts:\n%s", calls)
	}
	lastRun := strings.LastIndex(calls, "docker run -d")
	firstReadiness := strings.Index(calls, "docker inspect -f {{.State.Running}}|")
	if firstReadiness < 0 || lastRun > firstReadiness {
		t.Fatalf("all pod starts must be submitted before readiness inspection:\n%s", calls)
	}
	for _, forbidden := range []string{"docker rename ", "-next-"} {
		if strings.Contains(calls, forbidden) {
			t.Fatalf("stop-all restart must not use rolling artifact %q:\n%s", forbidden, calls)
		}
	}
}

func TestManagerRestartAllContinuesStartingAfterOnePodFails(t *testing.T) {
	runner := newStopAllRestartRunner("aifar-pod-admin-contacts-rev-1-r1")
	runner.failStartFor = "contacts-rev-1-r2"
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})

	err := manager.RestartAll(context.Background(), restartAllSpec())
	if err == nil || !strings.Contains(err.Error(), "contacts") || !strings.Contains(err.Error(), "replica 2") {
		t.Fatalf("expected contacts replica 2 startup failure, got %v", err)
	}
	if !runner.hasContainer("aifar-pod-admin-gateway-rev-1-r1") {
		t.Fatalf("later deployments must still start after one failure: %#v", runner.containerNames())
	}
}

func TestManagerRestartAllDoesNotStartWhenOldPodRemovalLeavesResidue(t *testing.T) {
	runner := newStopAllRestartRunner("aifar-pod-admin-contacts-rev-old-r1")
	runner.failRemoveFor = "contacts-rev-old-r1"
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})

	err := manager.RestartAll(context.Background(), restartAllSpec())
	if err == nil || !strings.Contains(err.Error(), "remain after stop-all") {
		t.Fatalf("expected residual pod failure, got %v", err)
	}
	if strings.Contains(runner.callsString(), "docker run ") {
		t.Fatalf("new pods must not start while old runtime pods remain:\n%s", runner.callsString())
	}
}

func TestManagerRestartAllAggregatesHealthAndStartFailures(t *testing.T) {
	runner := newStopAllRestartRunner()
	runner.failStartFor = "contacts-rev-1-r2"
	runner.unhealthyFor = "gateway-rev-1-r1"
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := restartAllSpec()
	for index := range spec.Deployments {
		spec.Deployments[index].Strategy.ProgressDeadlineSeconds = 1
	}

	err := manager.RestartAll(context.Background(), spec)
	if err == nil || !strings.Contains(err.Error(), "contacts") || !strings.Contains(err.Error(), "gateway") {
		t.Fatalf("expected aggregate contacts and gateway failures, got %v", err)
	}
	manager.mu.RLock()
	contactsStatus := manager.deployments[endpointKey(spec.InstanceID, "contacts")]
	gatewayStatus := manager.deployments[endpointKey(spec.InstanceID, "gateway")]
	manager.mu.RUnlock()
	for service, status := range map[string]deploymentRuntimeStatus{"contacts": contactsStatus, "gateway": gatewayStatus} {
		if status.Status != "failed" || status.LastError == "" {
			t.Fatalf("failed deployment %s must retain its restart error, got %#v", service, status)
		}
	}
}

func TestManagerRestartAllCancellationKeepsPartialResultWithoutRollback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	runner := newStopAllRestartRunner("aifar-pod-admin-contacts-rev-old-r1")
	runner.cancelAfterStart = "contacts-rev-1-r1"
	runner.cancel = cancel
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := restartAllSpec()

	err := manager.RestartAll(ctx, spec)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
	if !runner.hasContainer("aifar-pod-admin-contacts-rev-1-r1") {
		t.Fatalf("submitted pod must remain after cancellation: %#v", runner.containerNames())
	}
	if runner.hasContainer("aifar-pod-admin-contacts-rev-old-r1") {
		t.Fatalf("removed old pod must not be rolled back: %#v", runner.containerNames())
	}
	if endpoints := manager.cachedEndpoints(spec.InstanceID, "contacts"); len(endpoints) != 1 {
		t.Fatalf("cancellation must refresh endpoints for the submitted pod, got %#v", endpoints)
	}
	manager.mu.RLock()
	status := manager.deployments[endpointKey(spec.InstanceID, "contacts")]
	manager.mu.RUnlock()
	if status.Status != "pending" || status.CurrentReplicas != 1 || status.ReadyReplicas != 1 {
		t.Fatalf("cancellation must retain partial restart status, got %#v", status)
	}
}

func TestManagerRestartAllPreflightFailureKeepsExistingPods(t *testing.T) {
	original := "aifar-pod-admin-contacts-rev-1-r1"
	runner := newStopAllRestartRunner(original)
	runner.failImageFor = "contacts:rev-1"
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})

	err := manager.RestartAll(context.Background(), restartAllSpec())
	if err == nil || !strings.Contains(err.Error(), "image") {
		t.Fatalf("expected image preflight failure, got %v", err)
	}
	if !runner.hasContainer(original) {
		t.Fatalf("preflight failure must keep existing pod: %#v", runner.containerNames())
	}
	if strings.Contains(runner.callsString(), "docker rm -f ") {
		t.Fatalf("preflight failure must happen before mutation:\n%s", runner.callsString())
	}
}

func restartAllSpec() RuntimeSpec {
	return NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "contacts", Port: 38002},
			{Name: "message", Port: 38003},
			{Name: "gateway", Port: 38000},
			{Name: "web-vue3", Port: 8080},
		},
		Deployments: []DeploymentSpec{
			{ServiceName: "contacts", Image: "contacts:rev-1", PodRevision: "rev-1", Replicas: 2},
			{ServiceName: "message", Image: "message:rev-1", PodRevision: "rev-1", Replicas: 0},
			{ServiceName: "gateway", Image: "gateway:rev-1", PodRevision: "rev-1", Replicas: 1},
		},
	})
}

type stopAllRestartRunner struct {
	mu               sync.Mutex
	calls            []string
	containers       map[string]bool
	failImageFor     string
	failStartFor     string
	failRemoveFor    string
	unhealthyFor     string
	cancelAfterStart string
	cancel           context.CancelFunc
}

func newStopAllRestartRunner(names ...string) *stopAllRestartRunner {
	runner := &stopAllRestartRunner{containers: map[string]bool{}}
	for _, name := range names {
		runner.containers[name] = true
	}
	return runner
}

func (r *stopAllRestartRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, call)
	switch {
	case strings.Contains(call, "docker image inspect "):
		if r.failImageFor != "" && strings.Contains(call, r.failImageFor) {
			return CommandResult{}, errors.New("image unavailable")
		}
		return CommandResult{Stdout: "image-id\n"}, nil
	case strings.Contains(call, "docker ps -a"):
		names := make([]string, 0, len(r.containers))
		for container := range r.containers {
			if service := dockerServiceFilter(args); service != "" && !strings.Contains(container, "-"+service+"-") {
				continue
			}
			names = append(names, container)
		}
		sort.Strings(names)
		if strings.Contains(call, `aifar.replica`) {
			for index, container := range names {
				replica := 1
				if marker := strings.LastIndex(container, "-r"); marker >= 0 {
					replica, _ = strconv.Atoi(container[marker+2:])
				}
				names[index] = fmt.Sprintf("%s|%d|rev-1|", container, replica)
			}
		}
		stdout := strings.Join(names, "\n")
		if stdout != "" {
			stdout += "\n"
		}
		return CommandResult{Stdout: stdout}, nil
	case strings.Contains(call, "docker ps "):
		service := dockerServiceFilter(args)
		names := make([]string, 0)
		for container := range r.containers {
			if service == "" || strings.Contains(container, "-"+service+"-") {
				names = append(names, container+"|"+container)
			}
		}
		sort.Strings(names)
		return CommandResult{Stdout: strings.Join(names, "\n")}, nil
	case strings.Contains(call, "docker run "):
		container := containerNameArg(args)
		if r.failStartFor != "" && strings.Contains(container, r.failStartFor) {
			return CommandResult{Stderr: "image start failed\n"}, errors.New("image start failed")
		}
		r.containers[container] = true
		if r.cancelAfterStart != "" && strings.Contains(container, r.cancelAfterStart) && r.cancel != nil {
			r.cancel()
		}
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(call, "docker rm -f "):
		container := args[len(args)-1]
		if r.failRemoveFor != "" && strings.Contains(container, r.failRemoveFor) {
			return CommandResult{}, errors.New("remove failed")
		}
		delete(r.containers, container)
		return CommandResult{}, nil
	case strings.Contains(call, "docker rename "):
		oldName := args[len(args)-2]
		newName := args[len(args)-1]
		delete(r.containers, oldName)
		r.containers[newName] = true
		return CommandResult{}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		container := args[len(args)-1]
		if r.unhealthyFor != "" && strings.Contains(container, r.unhealthyFor) {
			return CommandResult{Stdout: "true|unhealthy\n"}, nil
		}
		if strings.Contains(call, "NetworkSettings") {
			return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
		}
		return CommandResult{Stdout: "true|healthy\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *stopAllRestartRunner) callsString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

func (r *stopAllRestartRunner) hasContainer(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.containers[name]
}

func (r *stopAllRestartRunner) hasServiceContainer(service string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name := range r.containers {
		if strings.Contains(name, "-"+service+"-") {
			return true
		}
	}
	return false
}

func (r *stopAllRestartRunner) containerNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]string, 0, len(r.containers))
	for name := range r.containers {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func dockerServiceFilter(args []string) string {
	for _, arg := range args {
		if strings.HasPrefix(arg, "label=aifar.service=") {
			return strings.TrimPrefix(arg, "label=aifar.service=")
		}
	}
	return ""
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

func TestManagerDiscoverEndpointsExcludesRenamedReplacementArtifacts(t *testing.T) {
	logical := "aifar-pod-admin-gateway-rev-1-r1"
	runner := &replacementEndpointRunner{logical: logical}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Services:    []ServiceSpec{{Name: "gateway", Port: 38000}},
	})

	endpoints, err := manager.discoverEndpoints(context.Background(), spec, "gateway")
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].Container != logical || endpoints[0].Address != "172.20.0.10:38000" {
		t.Fatalf("expected only the promoted logical container endpoint, got %#v", endpoints)
	}
}

type replacementEndpointRunner struct{ logical string }

func (r *replacementEndpointRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	if strings.Contains(call, "docker ps ") {
		return CommandResult{Stdout: strings.Join([]string{
			r.logical + "|" + r.logical,
			r.logical + "-old-deadbeef|" + r.logical,
			r.logical + "-next-deadbeef|" + r.logical,
		}, "\n") + "\n"}, nil
	}
	if strings.Contains(call, "docker inspect -f") {
		container := args[len(args)-1]
		ip := "172.20.0.10"
		if strings.Contains(container, "-old-") {
			ip = "172.20.0.11"
		} else if strings.Contains(container, "-next-") {
			ip = "172.20.0.12"
		}
		return CommandResult{Stdout: "true|healthy|" + ip + "\n"}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func TestManagerRunContainerAddsLoggingLabels(t *testing.T) {
	runner := &loggingLabelRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
	})
	deployment := DeploymentSpec{
		ServiceName:    "oauth",
		DeploymentName: "alpha-oauth",
		Image:          "aifar-oauth:rev-1",
		PodRevision:    "rev-1",
		Ports:          []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	if err := manager.runContainer(context.Background(), spec, deployment, 1, "aifar-pod-admin-oauth-rev-1-r1"); err != nil {
		t.Fatal(err)
	}
	calls := runner.callsString()
	for _, want := range []string{
		"--log-driver json-file",
		"--log-opt max-size=50m",
		"--log-opt max-file=5",
		"--label aifar.instance=admin",
		"--label aifar.deployment=alpha-oauth",
		"--label aifar.service=oauth",
		"--label aifar.pod=aifar-pod-admin-oauth-rev-1-r1",
		"--label aifar.replica=1",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("expected docker run label %q, got:\n%s", want, calls)
		}
	}
}

func TestManagerRunContainerDetachedDoesNotWaitForHealth(t *testing.T) {
	runner := &loggingLabelRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
	})
	deployment := DeploymentSpec{
		ServiceName:    "oauth",
		DeploymentName: "alpha-oauth",
		Image:          "aifar-oauth:rev-1",
		PodRevision:    "rev-1",
		Ports:          []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	name := "aifar-pod-admin-oauth-rev-1-r1"

	if err := manager.runContainerDetached(context.Background(), spec, deployment, 1, name); err != nil {
		t.Fatal(err)
	}
	calls := runner.callsString()
	if strings.Contains(calls, ".State.Health") {
		t.Fatalf("detached start must not wait for readiness:\n%s", calls)
	}
	for _, want := range []string{
		"docker run -d",
		"--label aifar.pod=" + name,
		"--label aifar.replica=1",
		"--network aifar-network",
		"aifar-oauth:rev-1",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("detached start must preserve docker argument %q:\n%s", want, calls)
		}
	}
}

type loggingLabelRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *loggingLabelRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	if strings.Contains(call, "docker inspect -f {{.State.Running}}|") {
		return CommandResult{Stdout: "true|healthy\n"}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func (r *loggingLabelRunner) callsString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
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
		Services: []ServiceSpec{{Name: "file", AppName: "alpha-file", Port: freePort(t), TargetPort: 38005}},
		Ingress:  IngressSpec{GatewayPort: freePort(t), WebPort: freePort(t)},
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

func TestManagerApplyPreemptsBackgroundResync(t *testing.T) {
	runner := newPreemptibleResyncRunner()
	defer runner.releaseBlockedCall()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	webServicePort := freePort(t)
	fileServicePort := freePort(t)
	oldSpec := NormalizeSpec(RuntimeSpec{
		InstanceID: "admin", InstallRoot: "/aifar/apps/admin", Network: "aifar-network",
		Deployments: []DeploymentSpec{{ServiceName: "web-vue3", Image: "web-vue3:rev-1", PodRevision: "rev-1", Replicas: 1, Ports: []ContainerPort{{Name: "http", ContainerPort: 8080}}}},
		Services:    []ServiceSpec{{Name: "web-vue3", AppName: "web-vue3", Port: webServicePort, TargetPort: 8080}},
		Ingress:     IngressSpec{GatewayPort: freePort(t), WebPort: freePort(t)},
	})
	manager.mu.Lock()
	manager.specs[oldSpec.InstanceID] = oldSpec
	manager.mu.Unlock()
	resyncDone := make(chan error, 1)
	go func() { resyncDone <- manager.Resync(context.Background()) }()
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("background resync did not reach the blocked unrelated deployment")
	}

	newSpec := oldSpec
	newSpec.Deployments = []DeploymentSpec{{ServiceName: "file", Image: "aifar-file:rev-1", PodRevision: "rev-1", Replicas: 0, Ports: []ContainerPort{{Name: "http", ContainerPort: 38005}}}}
	newSpec.Services = []ServiceSpec{{Name: "file", AppName: "alpha-file", Port: fileServicePort, TargetPort: 38005}}
	applyDone := make(chan error, 1)
	go func() { applyDone <- manager.Apply(context.Background(), newSpec) }()
	select {
	case err := <-applyDone:
		if err != nil {
			t.Fatalf("interactive apply failed after preemption: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("interactive apply remained blocked behind background resync")
	}
	select {
	case err := <-resyncDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected preempted resync cancellation, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("preempted resync did not exit")
	}
}

func TestManagerApplySkipsUnchangedDeployment(t *testing.T) {
	runner := &unchangedDeploymentFailureRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	webServicePort := freePort(t)
	fileServicePort := freePort(t)
	oldSpec := NormalizeSpec(RuntimeSpec{
		InstanceID: "admin", InstallRoot: "/aifar/apps/admin", Network: "aifar-network",
		Deployments: []DeploymentSpec{
			{ServiceName: "web-vue3", Image: "web-vue3:rev-1", PodRevision: "rev-1", Replicas: 1, Ports: []ContainerPort{{Name: "http", ContainerPort: 8080}}},
			{ServiceName: "file", Image: "aifar-file:rev-1", PodRevision: "rev-1", Replicas: 1, Ports: []ContainerPort{{Name: "http", ContainerPort: 38005}}},
		},
		Services: []ServiceSpec{
			{Name: "web-vue3", AppName: "web-vue3", Port: webServicePort, TargetPort: 8080},
			{Name: "file", AppName: "alpha-file", Port: fileServicePort, TargetPort: 38005},
		},
		Ingress: IngressSpec{GatewayPort: freePort(t), WebPort: freePort(t)},
	})
	manager.mu.Lock()
	manager.specs[oldSpec.InstanceID] = oldSpec
	manager.mu.Unlock()
	newSpec := oldSpec
	newSpec.Deployments = append([]DeploymentSpec(nil), oldSpec.Deployments...)
	newSpec.Deployments[1].Replicas = 0

	if err := manager.Apply(context.Background(), newSpec); err != nil {
		t.Fatalf("unchanged unhealthy deployment blocked target offline: %v", err)
	}
	if runner.touchedUnchanged() {
		t.Fatalf("interactive apply reconciled unchanged web deployment:\n%s", runner.callsString())
	}
}

func TestChangedDeploymentsPrioritizesOfflineDeployment(t *testing.T) {
	current := NormalizeSpec(RuntimeSpec{Deployments: []DeploymentSpec{
		{ServiceName: "oauth", Image: "oauth:rev-1", PodRevision: "rev-1", Replicas: 0},
		{ServiceName: "file", Image: "file:rev-1", PodRevision: "rev-1", Replicas: 1},
	}})
	next := current
	next.Deployments = []DeploymentSpec{
		{ServiceName: "oauth", Image: "oauth:rev-1", PodRevision: "rev-1", Replicas: 1},
		{ServiceName: "file", Image: "file:rev-1", PodRevision: "rev-1", Replicas: 0},
	}
	changed := changedDeployments(current, next)
	if len(changed) != 2 || changed[0].ServiceName != "file" || changed[0].Replicas != 0 || changed[1].ServiceName != "oauth" {
		t.Fatalf("expected offline deployment first, got %+v", changed)
	}
}

func TestManagerApplyPrioritizesOfflineDeployment(t *testing.T) {
	runner := &orderedReconcileRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	current := NormalizeSpec(RuntimeSpec{
		InstanceID: "admin", InstallRoot: "/aifar/apps/admin", Network: "aifar-network",
		Deployments: []DeploymentSpec{
			{ServiceName: "oauth", Image: "oauth:rev-1", PodRevision: "rev-1", Replicas: 0},
			{ServiceName: "file", Image: "file:rev-1", PodRevision: "rev-1", Replicas: 1},
		},
		Services: []ServiceSpec{
			{Name: "oauth", AppName: "alpha-oauth", Port: freePort(t), TargetPort: 8080},
			{Name: "file", AppName: "alpha-file", Port: freePort(t), TargetPort: 38005},
		},
		Ingress: IngressSpec{GatewayPort: freePort(t), WebPort: freePort(t)},
	})
	next := current
	next.Deployments = []DeploymentSpec{
		{ServiceName: "oauth", Image: "oauth:rev-1", PodRevision: "rev-1", Replicas: 1},
		{ServiceName: "file", Image: "file:rev-1", PodRevision: "rev-1", Replicas: 0},
	}
	if err := manager.reconcileDeploymentSet(context.Background(), next, changedDeployments(current, next)); err != nil {
		t.Fatal(err)
	}
	calls := runner.callsString()
	offlineIndex := strings.Index(calls, "aifar.service=file")
	onlineIndex := strings.Index(calls, "aifar-pod-admin-oauth-rev-1-r1")
	if offlineIndex < 0 || onlineIndex < 0 || offlineIndex > onlineIndex {
		t.Fatalf("offline deployment must reconcile before online deployment:\n%s", calls)
	}
}

func TestManagerSerializesInteractiveApplies(t *testing.T) {
	runner := newPreemptibleResyncRunner()
	defer runner.releaseBlockedCall()
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	defer func() { _ = manager.Remove(context.Background(), "admin") }()
	current := NormalizeSpec(RuntimeSpec{
		InstanceID: "admin", InstallRoot: "/aifar/apps/admin", Network: "aifar-network",
		Deployments: []DeploymentSpec{{ServiceName: "web-vue3", Image: "web:rev-1", PodRevision: "rev-1", Replicas: 0}},
		Services:    []ServiceSpec{{Name: "web-vue3", AppName: "web-vue3", Port: freePort(t), TargetPort: 8080}},
		Ingress:     IngressSpec{GatewayPort: freePort(t), WebPort: freePort(t)},
	})
	first := current
	first.Deployments = []DeploymentSpec{{ServiceName: "web-vue3", Image: "web:rev-1", PodRevision: "rev-1", Replicas: 1}}
	second := first
	second.Deployments = []DeploymentSpec{{ServiceName: "web-vue3", Image: "web:rev-1", PodRevision: "rev-1", Replicas: 2}}
	manager.mu.Lock()
	manager.specs[current.InstanceID] = current
	manager.mu.Unlock()

	firstDone := make(chan error, 1)
	go func() { firstDone <- manager.Apply(context.Background(), first) }()
	select {
	case <-runner.started:
	case <-time.After(2 * time.Second):
		t.Fatal("first interactive apply did not start")
	}
	secondDone := make(chan error, 1)
	go func() { secondDone <- manager.Apply(context.Background(), second) }()
	select {
	case err := <-firstDone:
		t.Fatalf("first interactive apply was interrupted before release: %v", err)
	case err := <-secondDone:
		t.Fatalf("second interactive apply bypassed the first: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	runner.releaseBlockedCall()
	if err := <-firstDone; err != nil {
		t.Fatalf("first interactive apply failed: %v", err)
	}
	if err := <-secondDone; err != nil {
		t.Fatalf("second interactive apply failed: %v", err)
	}
}

func TestManagerStatusPublishesInteractiveDeltaFeatures(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	status := manager.Status()
	features, _ := status["features"].([]string)
	for _, want := range []string{"interactive-reconcile-priority", "runtime-delta-apply"} {
		if !slices.Contains(features, want) {
			t.Fatalf("missing feature %q in %+v", want, features)
		}
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

func TestManagerStartsStoppedDesiredPod(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "oauth",
		Image:       "aifar-oauth:rev-1",
		PodRevision: "rev-1",
		Replicas:    1,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	runner := &stoppedDesiredPodRunner{hash: deploymentSpecHash(deployment)}
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
	for _, want := range []string{
		"docker update --restart unless-stopped aifar-pod-admin-oauth-rev-1-r1",
		"docker start aifar-pod-admin-oauth-rev-1-r1",
	} {
		if !strings.Contains(calls, want) {
			t.Fatalf("expected call containing %q, got:\n%s", want, calls)
		}
	}
	if strings.Contains(calls, "docker run -d") {
		t.Fatalf("expected existing stopped pod to be started, not recreated:\n%s", calls)
	}
}

func TestManagerRestartsUnhealthyDesiredPod(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "oauth",
		Image:       "aifar-oauth:rev-1",
		PodRevision: "rev-1",
		Replicas:    1,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	runner := &unhealthyDesiredPodRunner{hash: deploymentSpecHash(deployment)}
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
	if !strings.Contains(calls, "docker restart aifar-pod-admin-oauth-rev-1-r1") {
		t.Fatalf("expected unhealthy existing pod to be restarted, got:\n%s", calls)
	}
	if strings.Contains(calls, "docker start aifar-pod-admin-oauth-rev-1-r1") {
		t.Fatalf("expected running unhealthy pod to be restarted, not started:\n%s", calls)
	}
	if strings.Contains(calls, "docker run -d") {
		t.Fatalf("expected existing unhealthy pod to be restarted, not recreated:\n%s", calls)
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

func TestManagerOfflineDeploymentRemovesUnlabeledPods(t *testing.T) {
	deployment := DeploymentSpec{
		ServiceName: "oauth",
		Image:       "aifar-oauth:rev-1",
		PodRevision: "rev-1",
		Replicas:    0,
		Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
	}
	runner := &offlinePruneRunner{}
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: runner})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
	})

	if err := manager.removeExtraReplicas(context.Background(), spec, deployment); err != nil {
		t.Fatal(err)
	}
	if !runner.removed("aifar-pod-admin-oauth-rev-1-legacy") {
		t.Fatalf("expected offline reconcile to remove unlabeled pod, got:\n%s", strings.Join(runner.removals, "\n"))
	}
}

func TestManagerConvertsDeploymentPanicToApplyError(t *testing.T) {
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: panicDeploymentRunner{}})
	spec := NormalizeSpec(RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Deployments: []DeploymentSpec{{
			ServiceName: "oauth",
			Image:       "aifar-oauth:rev-1",
			PodRevision: "rev-1",
			Replicas:    1,
			Ports:       []ContainerPort{{Name: "http", ContainerPort: 38001}},
		}},
		Services: []ServiceSpec{{Name: "oauth", AppName: "alpha-oauth", Port: 38001, TargetPort: 38001}},
		Ingress: IngressSpec{
			GatewayPort: freePort(t),
			WebPort:     freePort(t),
		},
	})

	err := manager.Apply(context.Background(), spec)

	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected panic to be returned as apply error, got %v", err)
	}
}

func TestReconcileRuntimeKeepsRuntimeAppliedWhenNacosSyncFails(t *testing.T) {
	gatewayPort := freePort(t)
	webPort := freePort(t)
	manager := NewManager(ManagerOptions{StateDir: t.TempDir(), Runner: &fakeRunner{}})
	spec := RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: t.TempDir(),
		Network:     "aifar-network",
		Services: []ServiceSpec{
			{Name: "gateway", AppName: "alpha-gateway", Port: gatewayPort, TargetPort: gatewayPort},
			{Name: "web-vue3", AppName: "web-vue3", Port: webPort, TargetPort: webPort},
		},
		Ingress: IngressSpec{
			GatewayService: "gateway",
			WebService:     "web-vue3",
			GatewayPort:    gatewayPort,
			WebPort:        webPort,
		},
	}

	err := (Reconciler{Manager: manager}).ReconcileRuntime(context.Background(), spec)

	if err != nil {
		t.Fatalf("expected Nacos sync failure to be non-fatal, got %v", err)
	}
	manager.mu.RLock()
	statuses := manager.serviceStatusForInstanceLocked("admin")
	manager.mu.RUnlock()
	var gateway serviceRuntimeStatus
	for _, status := range statuses {
		if status.ServiceName == "gateway" {
			gateway = status
			break
		}
	}
	if gateway.ServiceName == "" || gateway.LastNacosError == "" || gateway.NacosReady {
		t.Fatalf("expected gateway Nacos status to record degraded sync, got %+v from %+v", gateway, statuses)
	}
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
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

type stoppedDesiredPodRunner struct {
	mu      sync.Mutex
	hash    string
	started bool
	calls   []string
}

func (r *stoppedDesiredPodRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	started := r.started
	r.mu.Unlock()
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(call, `{{index .Config.Labels "aifar.spec-hash"}}`):
		return CommandResult{Stdout: r.hash + "\n"}, nil
	case strings.Contains(call, "docker update --restart"):
		return CommandResult{Stdout: "updated\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}} ") && !strings.Contains(call, "|"):
		if started {
			return CommandResult{Stdout: "true\n"}, nil
		}
		return CommandResult{Stdout: "false\n"}, nil
	case strings.Contains(call, "docker start"):
		r.mu.Lock()
		r.started = true
		r.mu.Unlock()
		return CommandResult{Stdout: "started\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|") && strings.Contains(call, "NetworkSettings"):
		return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1|1|rev-1|" + r.hash + "\n"}, nil
	case strings.Contains(call, "docker ps "):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *stoppedDesiredPodRunner) callsString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

type unhealthyDesiredPodRunner struct {
	mu        sync.Mutex
	hash      string
	restarted bool
	calls     []string
}

func (r *unhealthyDesiredPodRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	restarted := r.restarted
	r.mu.Unlock()
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		return CommandResult{Stdout: "container-id\n"}, nil
	case strings.Contains(call, `{{index .Config.Labels "aifar.spec-hash"}}`):
		return CommandResult{Stdout: r.hash + "\n"}, nil
	case strings.Contains(call, "docker update --restart"):
		return CommandResult{Stdout: "updated\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}} ") && !strings.Contains(call, "|"):
		return CommandResult{Stdout: "true\n"}, nil
	case strings.Contains(call, "docker restart"):
		r.mu.Lock()
		r.restarted = true
		r.mu.Unlock()
		return CommandResult{Stdout: "restarted\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|") && strings.Contains(call, "NetworkSettings"):
		if restarted {
			return CommandResult{Stdout: "true|healthy|172.20.0.10\n"}, nil
		}
		return CommandResult{Stdout: "true|unhealthy|\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		if restarted {
			return CommandResult{Stdout: "true|healthy\n"}, nil
		}
		return CommandResult{Stdout: "true|unhealthy\n"}, nil
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1|1|rev-1|" + r.hash + "\n"}, nil
	case strings.Contains(call, "docker ps "):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-r1\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *unhealthyDesiredPodRunner) callsString() string {
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

type offlinePruneRunner struct {
	removals []string
}

func (r *offlinePruneRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "docker ps -a") && strings.Contains(call, `{{.Label "aifar.replica"}}`):
		return CommandResult{Stdout: "aifar-pod-admin-oauth-rev-1-legacy||rev-1|\n"}, nil
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "false|\n"}, nil
	case strings.Contains(call, "docker rm -f"):
		r.removals = append(r.removals, args[len(args)-1])
		return CommandResult{Stdout: "removed\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *offlinePruneRunner) removed(container string) bool {
	for _, item := range r.removals {
		if item == container {
			return true
		}
	}
	return false
}

type panicDeploymentRunner struct{}

func (panicDeploymentRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	switch {
	case strings.Contains(call, "docker ps -a"):
		return CommandResult{}, nil
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		panic("docker inspect crashed")
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
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

type preemptibleResyncRunner struct {
	started     chan struct{}
	release     chan struct{}
	startedOnce sync.Once
	releaseOnce sync.Once
}

func newPreemptibleResyncRunner() *preemptibleResyncRunner {
	return &preemptibleResyncRunner{started: make(chan struct{}), release: make(chan struct{})}
}

func (r *preemptibleResyncRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	if err := ctx.Err(); err != nil {
		return CommandResult{}, err
	}
	call := name + " " + strings.Join(args, " ")
	if strings.Contains(call, "docker inspect -f {{.Id}}") && strings.Contains(call, "web-vue3") {
		r.startedOnce.Do(func() { close(r.started) })
		select {
		case <-ctx.Done():
			return CommandResult{}, ctx.Err()
		case <-r.release:
			return CommandResult{}, errors.New("released blocked resync")
		}
	}
	if strings.Contains(call, "docker inspect -f {{.State.Running}}|") {
		return CommandResult{Stdout: "true|healthy\n"}, nil
	}
	if strings.Contains(call, "docker ps") {
		return CommandResult{}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

func (r *preemptibleResyncRunner) releaseBlockedCall() {
	r.releaseOnce.Do(func() { close(r.release) })
}

type unchangedDeploymentFailureRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *unchangedDeploymentFailureRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	if strings.Contains(call, "docker inspect") && strings.Contains(call, "web-vue3") {
		return CommandResult{}, errors.New("unchanged web deployment is unhealthy")
	}
	if strings.Contains(call, "docker ps") {
		return CommandResult{}, nil
	}
	return CommandResult{Stdout: "ok\n"}, nil
}

type orderedReconcileRunner struct {
	mu    sync.Mutex
	calls []string
}

func (r *orderedReconcileRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	r.mu.Lock()
	r.calls = append(r.calls, call)
	r.mu.Unlock()
	switch {
	case strings.Contains(call, "docker inspect -f {{.Id}}"):
		return CommandResult{}, errors.New("not found")
	case strings.Contains(call, "docker inspect -f {{.State.Running}}|"):
		return CommandResult{Stdout: "true|healthy\n"}, nil
	case strings.Contains(call, "docker ps"):
		return CommandResult{}, nil
	case strings.Contains(call, "docker run "):
		return CommandResult{Stdout: "container-id\n"}, nil
	default:
		return CommandResult{Stdout: "ok\n"}, nil
	}
}

func (r *orderedReconcileRunner) callsString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
}

func (r *unchangedDeploymentFailureRunner) touchedUnchanged() bool {
	return strings.Contains(r.callsString(), "docker inspect") && strings.Contains(r.callsString(), "web-vue3")
}

func (r *unchangedDeploymentFailureRunner) callsString() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return strings.Join(r.calls, "\n")
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
