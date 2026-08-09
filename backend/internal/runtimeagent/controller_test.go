package runtimeagent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type controllerTestRunner struct {
	mu             sync.Mutex
	pods           map[string]bool
	blockedService string
	blocked        chan struct{}
	released       chan struct{}
	cancelled      map[string]int
	runFailures    int
	runError       error
	runAttempts    int
	panicRuns      int
}

func newControllerTestRunner() *controllerTestRunner {
	return &controllerTestRunner{
		pods:      map[string]bool{},
		blocked:   make(chan struct{}),
		released:  make(chan struct{}),
		cancelled: map[string]int{},
	}
}

func (r *controllerTestRunner) Run(ctx context.Context, name string, args ...string) (CommandResult, error) {
	r.mu.Lock()
	if r.panicRuns > 0 {
		r.panicRuns--
		r.mu.Unlock()
		panic("injected runner panic")
	}
	r.mu.Unlock()
	call := name + " " + strings.Join(args, " ")
	service := serviceFromControllerCall(call)
	if len(args) > 0 && args[0] == "run" {
		r.mu.Lock()
		r.runAttempts++
		if r.runFailures > 0 {
			r.runFailures--
			err := r.runError
			r.mu.Unlock()
			return CommandResult{}, err
		}
		blocked := service != "" && service == r.blockedService
		r.mu.Unlock()
		if blocked {
			select {
			case r.blocked <- struct{}{}:
			default:
			}
			select {
			case <-ctx.Done():
				r.mu.Lock()
				r.cancelled[service]++
				r.mu.Unlock()
				return CommandResult{}, ctx.Err()
			case <-r.released:
			}
		}
		container := argumentAfter(args, "--name")
		r.mu.Lock()
		r.pods[container] = true
		r.mu.Unlock()
		return CommandResult{Stdout: container}, nil
	}
	if len(args) > 0 && args[0] == "inspect" {
		container := args[len(args)-1]
		r.mu.Lock()
		exists := r.pods[container]
		r.mu.Unlock()
		if !exists {
			return CommandResult{}, errors.New("No such container")
		}
		format := strings.Join(args, " ")
		switch {
		case strings.Contains(format, ".State.Running") && strings.Contains(format, "NetworkSettings"):
			return CommandResult{Stdout: "true|healthy|172.20.0.10"}, nil
		case strings.Contains(format, ".State.Running"):
			return CommandResult{Stdout: "true|healthy"}, nil
		case strings.Contains(format, "aifar.spec-hash"):
			return CommandResult{Stdout: ""}, nil
		default:
			return CommandResult{Stdout: "container-id"}, nil
		}
	}
	if len(args) > 1 && args[0] == "ps" {
		filterService := ""
		for _, arg := range args {
			if strings.HasPrefix(arg, "label=aifar.service=") {
				filterService = strings.TrimPrefix(arg, "label=aifar.service=")
			}
		}
		r.mu.Lock()
		defer r.mu.Unlock()
		var rows []string
		for pod := range r.pods {
			if filterService != "" && !strings.Contains(pod, "-"+filterService+"-") {
				continue
			}
			if strings.Contains(call, `aifar.replica`) {
				rows = append(rows, pod+"|1|rev-1|")
			} else {
				rows = append(rows, pod+"|"+pod)
			}
		}
		return CommandResult{Stdout: strings.Join(rows, "\n")}, nil
	}
	if len(args) > 0 && args[0] == "rm" {
		container := args[len(args)-1]
		r.mu.Lock()
		delete(r.pods, container)
		r.mu.Unlock()
	}
	return CommandResult{Stdout: "ok"}, nil
}

func argumentAfter(args []string, wanted string) string {
	for index := 0; index+1 < len(args); index++ {
		if args[index] == wanted {
			return args[index+1]
		}
	}
	return ""
}

func serviceFromControllerCall(call string) string {
	marker := "aifar.service="
	index := strings.Index(call, marker)
	if index < 0 {
		return ""
	}
	value := call[index+len(marker):]
	if end := strings.IndexByte(value, ' '); end >= 0 {
		value = value[:end]
	}
	return value
}

func controllerTestConfig() InstanceConfig {
	return NormalizeInstanceConfig(InstanceConfig{
		APIVersion:  ManifestAPIVersion,
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
	})
}

func controllerTestManifest(service string, generation int64, replicas int) DeploymentManifest {
	return NormalizeDeploymentManifest(DeploymentManifest{
		APIVersion: ManifestAPIVersion,
		Kind:       DeploymentManifestKind,
		Metadata: DeploymentMetadata{
			InstanceID: "admin",
			Name:       service,
			Generation: generation,
		},
		Spec: DeploymentSpec{
			ServiceName: service,
			Image:       "aifar-" + service + ":rev-1",
			PodRevision: "rev-1",
			Replicas:    replicas,
		},
		Service: ServiceSpec{Name: service, AppName: "alpha-" + service, Port: 38010, ListenPort: 38010, TargetPort: 38010},
	})
}

func newControllerTestManager(t *testing.T, runner CommandRunner, options ...func(*ManagerOptions)) *Manager {
	t.Helper()
	stateDir := t.TempDir()
	store := &ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(controllerTestConfig()); err != nil {
		t.Fatal(err)
	}
	managerOptions := ManagerOptions{StateDir: stateDir, Runner: runner, ManifestStore: store}
	for _, option := range options {
		option(&managerOptions)
	}
	return NewManager(managerOptions)
}

func waitForDeploymentCondition(t *testing.T, manager *Manager, service, conditionType string, timeout time.Duration) DeploymentState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		state, ok := manager.DeploymentState("admin", service)
		if ok {
			for _, condition := range state.Conditions {
				if condition.Type == conditionType && condition.Status {
					return state
				}
			}
		}
		time.Sleep(time.Millisecond)
	}
	state, _ := manager.DeploymentState("admin", service)
	t.Fatalf("condition %s for %s not reached; state=%+v", conditionType, service, state)
	return DeploymentState{}
}

func currentCondition(state DeploymentState) DeploymentCondition {
	for index := len(state.Conditions) - 1; index >= 0; index-- {
		if state.Conditions[index].Status {
			return state.Conditions[index]
		}
	}
	return DeploymentCondition{}
}

func TestControllersDoNotBlockDifferentServices(t *testing.T) {
	runner := newControllerTestRunner()
	runner.blockedService = "permission"
	manager := newControllerTestManager(t, runner)
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.blocked:
	case <-time.After(time.Second):
		t.Fatal("permission controller did not block")
	}
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("file", 1, 1)); err != nil {
		t.Fatal(err)
	}
	waitForDeploymentCondition(t, manager, "file", "Available", time.Second)
	state, _ := manager.DeploymentState("admin", "permission")
	if condition := currentCondition(state); condition.Type == "Available" {
		t.Fatal("blocked permission unexpectedly became available")
	}
}

func TestNewGenerationSupersedesOnlySameService(t *testing.T) {
	runner := newControllerTestRunner()
	runner.blockedService = "permission"
	manager := newControllerTestManager(t, runner)
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.blocked:
	case <-time.After(time.Second):
		t.Fatal("permission generation 1 did not start")
	}
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("file", 1, 0)); err != nil {
		t.Fatal(err)
	}
	waitForDeploymentCondition(t, manager, "file", "Offline", time.Second)
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 2, 0)); err != nil {
		t.Fatal(err)
	}
	waitForDeploymentCondition(t, manager, "permission", "Offline", time.Second)
	runner.mu.Lock()
	permissionCancelled := runner.cancelled["permission"]
	fileCancelled := runner.cancelled["file"]
	runner.mu.Unlock()
	if permissionCancelled != 1 {
		t.Fatalf("permission cancellation count=%d, want 1", permissionCancelled)
	}
	if fileCancelled != 0 {
		t.Fatalf("file cancellation count=%d, want 0", fileCancelled)
	}
}

func TestHigherGenerationWinsBeforeOldControllerRegistersCancel(t *testing.T) {
	runner := newControllerTestRunner()
	entered := make(chan struct{})
	release := make(chan struct{})
	manager := newControllerTestManager(t, runner, func(options *ManagerOptions) {
		options.controllerBeforeActivate = func(manifest DeploymentManifest) {
			if manifest.Metadata.Generation != 1 {
				return
			}
			close(entered)
			<-release
		}
	})
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("generation 1 did not reach pre-activation window")
	}
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 2, 0)); err != nil {
		t.Fatal(err)
	}
	close(release)
	waitForDeploymentCondition(t, manager, "permission", "Offline", time.Second)
	runner.mu.Lock()
	attempts := runner.runAttempts
	runner.mu.Unlock()
	if attempts != 0 {
		t.Fatalf("superseded generation entered Docker; run attempts=%d", attempts)
	}
}

func TestSupersededGenerationCannotRegressDeploymentState(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	manager := newControllerTestManager(t, newControllerTestRunner(), func(options *ManagerOptions) {
		options.controllerBeforeActivate = func(manifest DeploymentManifest) {
			if manifest.Metadata.Generation == 2 {
				once.Do(func() {
					close(entered)
					<-release
				})
			}
		}
	})
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 2, 0)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("generation 2 did not reach controlled reconcile window")
	}
	stale := controllerTestManifest("permission", 1, 1)
	hash, err := DeploymentManifestSpecHash(stale)
	if err != nil {
		t.Fatal(err)
	}
	manager.setControllerCondition(stale, hash, deploymentConditionAvailable, "MinimumReplicasAvailable", true)
	manager.setControllerPanic("admin", "permission", 1)
	manager.setControllerPanic("admin", "permission", 0)
	state, ok := manager.DeploymentState("admin", "permission")
	if !ok {
		t.Fatal("deployment state missing")
	}
	if state.Generation != 2 {
		t.Fatalf("superseded generation regressed state to %d, want 2", state.Generation)
	}
	if condition := currentCondition(state); condition.Type != deploymentConditionAccepted {
		t.Fatalf("unattributed panic changed current condition to %s, want Accepted", condition.Type)
	}
	close(release)
	waitForDeploymentCondition(t, manager, "permission", "Offline", time.Second)
}

func TestControllerUsesSingleOwnerAndWakeChannelPerService(t *testing.T) {
	runner := newControllerTestRunner()
	runner.blockedService = "permission"
	manager := newControllerTestManager(t, runner)
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.blocked:
	case <-time.After(time.Second):
		t.Fatal("controller did not start")
	}
	for generation := int64(2); generation <= 20; generation++ {
		if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", generation, 1)); err != nil {
			t.Fatal(err)
		}
	}
	manager.controllerMu.Lock()
	controllers := len(manager.controllers)
	entry := manager.controllers[endpointKey("admin", "permission")]
	queued := len(entry.wake)
	manager.controllerMu.Unlock()
	if controllers != 1 {
		t.Fatalf("controllers=%d, want 1", controllers)
	}
	if queued > 1 {
		t.Fatalf("wake queue length=%d, want <=1", queued)
	}
}

func TestControllerSurvivesRunnerPanicAndAcceptsNewGeneration(t *testing.T) {
	runner := newControllerTestRunner()
	runner.panicRuns = 1
	manager := newControllerTestManager(t, runner)
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	degraded := waitForDeploymentCondition(t, manager, "permission", "Degraded", 250*time.Millisecond)
	if condition := currentCondition(degraded); condition.Reason != "ContainerCreateFailed" {
		t.Fatalf("panic reason=%s, want ContainerCreateFailed", condition.Reason)
	}
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 2, 1)); err != nil {
		t.Fatal(err)
	}
	available := waitForDeploymentCondition(t, manager, "permission", "Available", time.Second)
	if available.Generation != 2 || available.ObservedGeneration != 2 {
		t.Fatalf("state after panic recovery=%+v", available)
	}
}

func TestControllerHonorsInstanceMaintenanceWriteLock(t *testing.T) {
	manager := newControllerTestManager(t, newControllerTestRunner())
	lock := manager.instanceMaintenanceLock("admin")
	lock.Lock()
	acceptDone := make(chan error, 1)
	go func() {
		_, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 0))
		acceptDone <- err
	}()
	select {
	case err := <-acceptDone:
		lock.Unlock()
		t.Fatalf("acceptance escaped maintenance lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	lock.Unlock()
	select {
	case err := <-acceptDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("acceptance remained blocked after maintenance")
	}
	waitForDeploymentCondition(t, manager, "permission", "Offline", time.Second)
}

func TestControllerConditionTransitionsAndReasons(t *testing.T) {
	runner := newControllerTestRunner()
	runner.blockedService = "permission"
	manager := newControllerTestManager(t, runner)
	acceptance, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1))
	if err != nil {
		t.Fatal(err)
	}
	if !acceptance.Accepted {
		t.Fatal("manifest was not accepted")
	}
	progressing := waitForDeploymentCondition(t, manager, "permission", "Progressing", time.Second)
	if progressing.ObservedGeneration != 0 {
		t.Fatalf("observed generation while progressing=%d, want 0", progressing.ObservedGeneration)
	}
	close(runner.released)
	available := waitForDeploymentCondition(t, manager, "permission", "Available", time.Second)
	if available.ObservedGeneration != 1 || available.ReadyReplicas != 1 {
		t.Fatalf("available state=%+v", available)
	}
	if condition := currentCondition(available); condition.Reason != "MinimumReplicasAvailable" {
		t.Fatalf("available reason=%s", condition.Reason)
	}

	offlineManifest := controllerTestManifest("permission", 2, 0)
	if _, err := manager.AcceptDeployment(context.Background(), offlineManifest); err != nil {
		t.Fatal(err)
	}
	offline := waitForDeploymentCondition(t, manager, "permission", "Offline", time.Second)
	if condition := currentCondition(offline); condition.Reason != "DesiredReplicasZero" {
		t.Fatalf("offline reason=%s", condition.Reason)
	}
}

type fakeControllerTimer struct {
	ch      chan time.Time
	stopped atomic.Bool
}

func (t *fakeControllerTimer) C() <-chan time.Time { return t.ch }
func (t *fakeControllerTimer) Stop() bool          { return t.stopped.CompareAndSwap(false, true) }

type fakeControllerClock struct {
	mu        sync.Mutex
	now       time.Time
	durations []time.Duration
	timers    []*fakeControllerTimer
	created   chan struct{}
}

func newFakeControllerClock() *fakeControllerClock {
	return &fakeControllerClock{now: time.Unix(1, 0).UTC(), created: make(chan struct{}, 32)}
}

func (c *fakeControllerClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeControllerClock) NewTimer(duration time.Duration) controllerTimer {
	c.mu.Lock()
	timer := &fakeControllerTimer{ch: make(chan time.Time, 1)}
	c.durations = append(c.durations, duration)
	c.timers = append(c.timers, timer)
	c.mu.Unlock()
	c.created <- struct{}{}
	return timer
}

func (c *fakeControllerClock) fireNext(t *testing.T) {
	t.Helper()
	select {
	case <-c.created:
	case <-time.After(time.Second):
		t.Fatal("retry timer was not created")
	}
	c.mu.Lock()
	timer := c.timers[0]
	c.timers = c.timers[1:]
	c.now = c.now.Add(c.durations[len(c.durations)-len(c.timers)-1])
	now := c.now
	c.mu.Unlock()
	timer.ch <- now
}

func (c *fakeControllerClock) snapshotDurations() []time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]time.Duration(nil), c.durations...)
}

func TestControllerRetryBackoffAndAvailableReset(t *testing.T) {
	runner := newControllerTestRunner()
	runner.runFailures = 8
	runner.runError = errors.New("docker create failed")
	clock := newFakeControllerClock()
	manager := newControllerTestManager(t, runner, func(options *ManagerOptions) { options.controllerClock = clock })
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 30 * time.Second, 60 * time.Second, 60 * time.Second}
	for range want {
		waitForDeploymentCondition(t, manager, "permission", "Degraded", time.Second)
		clock.fireNext(t)
	}
	waitForDeploymentCondition(t, manager, "permission", "Available", time.Second)
	got := clock.snapshotDurations()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("backoff=%v, want %v", got, want)
	}

	runner.mu.Lock()
	delete(runner.pods, "aifar-pod-admin-permission-rev-1-r1")
	runner.runFailures = 1
	runner.runError = errors.New("docker create failed")
	runner.mu.Unlock()
	manager.ReconcileDeployment("admin", "permission")
	waitForDeploymentCondition(t, manager, "permission", "Degraded", time.Second)
	select {
	case <-clock.created:
	case <-time.After(time.Second):
		t.Fatal("reset retry timer was not created")
	}
	got = clock.snapshotDurations()
	if got[len(got)-1] != time.Second {
		t.Fatalf("backoff after Available=%s, want 1s", got[len(got)-1])
	}
}

func TestControllerClassifiesStableFailureReasons(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{name: "image", err: errors.New("No such image: missing"), reason: "ImageMissing"},
		{name: "create", err: errors.New("docker create failed"), reason: "ContainerCreateFailed"},
		{name: "start", err: errors.New("start stopped container failed"), reason: "ContainerStartFailed"},
		{name: "readiness", err: errors.New("pod did not become ready"), reason: "ReadinessFailed"},
		{name: "readiness with refused diagnostic", err: errors.New("AIFAR pod did not become ready: permission\nhealth probe: connection refused"), reason: "ReadinessFailed"},
		{name: "crashloop", err: errors.New("CrashLoopBackOff"), reason: "CrashLoopBackOff"},
		{name: "pressure", err: errors.New("no space left on device"), reason: "NodeResourcePressure"},
		{name: "agent", err: errors.New("Cannot connect to the Docker daemon"), reason: "AgentUnavailable"},
		{name: "bare refused is not agent boundary", err: errors.New("connection refused"), reason: "ContainerCreateFailed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if reason := deploymentFailureReason(test.err); reason != test.reason {
				t.Fatalf("reason=%s, want %s", reason, test.reason)
			}
		})
	}
}

type controllerPathInfo struct{ mode os.FileMode }

func (i controllerPathInfo) Name() string       { return "path" }
func (i controllerPathInfo) Size() int64        { return 0 }
func (i controllerPathInfo) Mode() os.FileMode  { return i.mode }
func (i controllerPathInfo) ModTime() time.Time { return time.Time{} }
func (i controllerPathInfo) IsDir() bool        { return i.mode.IsDir() }
func (i controllerPathInfo) Sys() any           { return nil }

func TestControllerSpecRejectedDoesNotRetryUntilNewGeneration(t *testing.T) {
	var reject atomic.Bool
	stateDir := t.TempDir()
	store := &ManifestStore{StateDir: stateDir}
	store.manifestPathLstat = func(path string) (os.FileInfo, error) {
		if reject.Load() && path == "/aifar/apps/admin/env" {
			return controllerPathInfo{mode: os.ModeSymlink}, nil
		}
		if path == "/" || path == "/aifar" || path == "/aifar/apps" || path == "/aifar/apps/admin" || path == "/aifar/apps/admin/env" {
			return controllerPathInfo{mode: os.ModeDir}, nil
		}
		return nil, os.ErrNotExist
	}
	if err := store.PutInstance(controllerTestConfig()); err != nil {
		t.Fatal(err)
	}
	clock := newFakeControllerClock()
	runner := newControllerTestRunner()
	readEntered := make(chan struct{})
	readRelease := make(chan struct{})
	var readOnce sync.Once
	manager := NewManager(ManagerOptions{
		StateDir:        stateDir,
		Runner:          runner,
		ManifestStore:   store,
		controllerClock: clock,
		controllerBeforeRead: func(_, _ string) {
			readOnce.Do(func() {
				close(readEntered)
				<-readRelease
			})
		},
	})
	manifest := controllerTestManifest("permission", 1, 1)
	manifest.Spec.EnvFiles = []string{"/aifar/apps/admin/env/permission.env"}
	if _, err := manager.AcceptDeployment(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	select {
	case <-readEntered:
	case <-time.After(time.Second):
		t.Fatal("controller did not reach pre-read window")
	}
	reject.Store(true)
	close(readRelease)
	state := waitForDeploymentCondition(t, manager, "permission", "Degraded", time.Second)
	if condition := currentCondition(state); condition.Reason != "SpecRejected" {
		t.Fatalf("reason=%s, want SpecRejected", condition.Reason)
	}
	select {
	case <-clock.created:
		t.Fatal("SpecRejected scheduled an automatic retry")
	case <-time.After(30 * time.Millisecond):
	}
	reject.Store(false)
	manager.ReconcileDeployment("admin", "permission")
	time.Sleep(30 * time.Millisecond)
	runner.mu.Lock()
	attemptsBeforeNewGeneration := runner.runAttempts
	runner.mu.Unlock()
	if attemptsBeforeNewGeneration != 0 {
		t.Fatalf("SpecRejected generation was retried manually; attempts=%d", attemptsBeforeNewGeneration)
	}
	manifest.Metadata.Generation = 2
	if _, err := manager.AcceptDeployment(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	waitForDeploymentCondition(t, manager, "permission", "Available", time.Second)
}

func TestManifestStoreConcurrentSameGenerationAcceptsOnlyOneHash(t *testing.T) {
	store := ManifestStore{StateDir: t.TempDir()}
	if err := store.PutInstance(controllerTestConfig()); err != nil {
		t.Fatal(err)
	}
	first := controllerTestManifest("permission", 1, 1)
	second := controllerTestManifest("permission", 1, 2)
	start := make(chan struct{})
	errs := make(chan error, 2)
	for _, manifest := range []DeploymentManifest{first, second} {
		manifest := manifest
		go func() {
			<-start
			_, err := store.Put(manifest)
			errs <- err
		}()
	}
	close(start)
	accepted, conflicts := 0, 0
	for range 2 {
		err := <-errs
		switch {
		case err == nil:
			accepted++
		case errors.Is(err, ErrDeploymentGenerationConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if accepted != 1 || conflicts != 1 {
		t.Fatalf("accepted=%d conflicts=%d", accepted, conflicts)
	}
}

func TestManifestStoreConcurrentGenerationsEndAtHighestGeneration(t *testing.T) {
	store := ManifestStore{StateDir: t.TempDir()}
	if err := store.PutInstance(controllerTestConfig()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for generation := int64(1); generation <= 20; generation++ {
		generation := generation
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := store.Put(controllerTestManifest("permission", generation, int(generation%2)+1))
			if err != nil && !errors.Is(err, ErrStaleDeploymentGeneration) {
				t.Errorf("generation %d: %v", generation, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	manifest, err := store.Get("admin", "permission")
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Metadata.Generation != 20 {
		t.Fatalf("generation=%d, want 20", manifest.Metadata.Generation)
	}
}
