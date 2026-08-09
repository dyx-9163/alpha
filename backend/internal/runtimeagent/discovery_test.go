package runtimeagent

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestReadyServiceQueuesRegistrationWithoutWaitingForPeers(t *testing.T) {
	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"file": errors.New("nacos down")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)

	discovery.EndpointChanged(readyDiscoveryEvent("file", "file-1"))
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))

	waitForDiscovery(t, time.Second, func() bool { return syncer.registered("permission") })
}

func TestNacosFailureDoesNotDegradeDeployment(t *testing.T) {
	manager := newControllerTestManager(t, newControllerTestRunner())
	manifest := controllerTestManifest("permission", 1, 0)
	if _, err := manager.AcceptDeployment(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	waitForDeploymentCondition(t, manager, "permission", deploymentConditionOffline, time.Second)
	manager.setControllerCondition(manifest, "stable-hash", deploymentConditionAvailable, "MinimumReplicasAvailable", true)

	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"permission": errors.New("nacos unavailable")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.attempts("permission") > 0 })

	state, ok := manager.DeploymentState("admin", "permission")
	if !ok || len(state.Conditions) != 1 || state.Conditions[0].Type != deploymentConditionAvailable {
		t.Fatalf("Nacos failure changed deployment state: %+v", state)
	}
}

func TestDiscoveryTransitionsAndCanonicalHashDedupe(t *testing.T) {
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)

	offline := readyDiscoveryEvent("permission")
	discovery.EndpointChanged(offline)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("deregister", "permission") == 1 })

	first := readyDiscoveryEvent("permission", "permission-2", "permission-1")
	discovery.EndpointChanged(first)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("register", "permission") == 1 })

	reordered := readyDiscoveryEvent("permission", "permission-1", "permission-2")
	discovery.EndpointChanged(reordered)
	duplicated := readyDiscoveryEvent("permission", "permission-1", "permission-2", "permission-1")
	discovery.EndpointChanged(duplicated)
	time.Sleep(20 * time.Millisecond)
	if got := syncer.count("register", "permission"); got != 1 {
		t.Fatalf("same canonical endpoint set registered %d times, want 1", got)
	}

	changed := readyDiscoveryEvent("permission", "permission-1")
	discovery.EndpointChanged(changed)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("register", "permission") == 2 })
	discovery.EndpointChanged(offline)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("deregister", "permission") == 2 })

	if got := syncer.count("refresh", "permission"); got != 4 {
		t.Fatalf("route refresh count=%d, want one for each distinct transition", got)
	}
}

func TestDiscoveryLatestEndpointInterruptsBackoff(t *testing.T) {
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"permission": errors.New("secret=top-secret")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer, Clock: clock})
	t.Cleanup(discovery.Stop)

	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-old"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.attempts("permission") == 1 })
	syncer.setFailure("permission", nil)
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-new"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.registeredPod("permission", "permission-new") })

	if syncer.registeredPod("permission", "permission-old") && syncer.lastRegisteredPod("permission") != "permission-new" {
		t.Fatalf("latest endpoint did not win: %+v", syncer.snapshotCalls())
	}
}

func TestDiscoveryEndpointChangeAndStopCancelInFlightSync(t *testing.T) {
	syncer := &cancellingDiscoverySyncer{started: make(chan string, 4), cancelled: make(chan string, 4)}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-old"))
	select {
	case pod := <-syncer.started:
		if pod != "permission-old" {
			t.Fatalf("first sync pod=%s", pod)
		}
	case <-time.After(time.Second):
		t.Fatal("first discovery sync did not start")
	}

	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-new"))
	select {
	case pod := <-syncer.cancelled:
		if pod != "permission-old" {
			t.Fatalf("cancelled pod=%s", pod)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("new endpoint did not cancel stale in-flight sync")
	}
	select {
	case pod := <-syncer.started:
		if pod != "permission-new" {
			t.Fatalf("replacement sync pod=%s", pod)
		}
	case <-time.After(time.Second):
		t.Fatal("latest discovery sync did not start")
	}
	discovery.StopInstance("admin")
	select {
	case pod := <-syncer.cancelled:
		if pod != "permission-new" {
			t.Fatalf("stop cancelled pod=%s", pod)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("StopInstance did not cancel active sync")
	}
}

func TestDiscoveryRetryBackoffResetsAfterSuccess(t *testing.T) {
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"permission": errors.New("unavailable")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer, Clock: clock})
	t.Cleanup(discovery.Stop)

	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 1 })
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 2 })
	syncer.setFailure("permission", nil)
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return discoveryAppliedPod(discovery, "permission") == "permission-1" })

	syncer.setFailure("permission", errors.New("unavailable again"))
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-2"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 3 })
	if got, want := clock.snapshotDurations(), []time.Duration{time.Second, 2 * time.Second, time.Second}; !reflect.DeepEqual(got, want) {
		t.Fatalf("retry durations=%v, want %v", got, want)
	}
}

func TestDiscoveryNacosRetryDoesNotRefreshUnchangedRoutes(t *testing.T) {
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"permission": errors.New("nacos down")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer, Clock: clock})
	t.Cleanup(discovery.Stop)
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 1 })
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 2 })
	if got := syncer.count("refresh", "permission"); got != 1 {
		t.Fatalf("unchanged endpoint route refreshed %d times during Nacos retry, want 1", got)
	}
}

func TestDiscoveryHeartbeatRunsPerServiceAndReplaysAfterFailure(t *testing.T) {
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{
		Syncer:            syncer,
		Clock:             clock,
		HeartbeatInterval: 5 * time.Second,
	})
	t.Cleanup(discovery.Stop)
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 1 })
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("heartbeat", "permission") == 1 })

	syncer.setFailure("permission", errors.New("heartbeat rejected token=secret"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 2 })
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 3 })
	syncer.setFailure("permission", nil)
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("register", "permission") >= 2 })
}

func TestDiscoveryHeartbeatPanicKeepsOwnerAndPeerAlive(t *testing.T) {
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{panicHeartbeatFor: map[string]int{"file": 1}}
	discovery := newDiscoveryController(discoveryControllerOptions{
		Syncer:            syncer,
		Clock:             clock,
		HeartbeatInterval: 5 * time.Second,
	})
	t.Cleanup(discovery.Stop)
	discovery.EndpointChanged(readyDiscoveryEvent("file", "file-1"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 1 })
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 2 })
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) >= 3 })

	discovery.EndpointChanged(readyDiscoveryEvent("file", "file-2"))
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-2"))
	waitForDiscovery(t, time.Second, func() bool {
		return syncer.registeredPod("file", "file-2") && syncer.registeredPod("permission", "permission-2")
	})
}

func TestDiscoveryWorkerPanicDoesNotKillPeersOrOwner(t *testing.T) {
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{panicFor: map[string]int{"file": 1}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer, Clock: clock})
	t.Cleanup(discovery.Stop)

	discovery.EndpointChanged(readyDiscoveryEvent("file", "file-1"))
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.registered("permission") })
	waitForDiscovery(t, time.Second, func() bool { return len(clock.snapshotDurations()) == 1 })
	clock.fireNext(t)
	waitForDiscovery(t, time.Second, func() bool { return syncer.registered("file") })
}

func TestDiscoveryLogsStableReasonWithoutSecrets(t *testing.T) {
	var log bytes.Buffer
	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"permission": errors.New("url=http://user:password@host token=secret")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer, Log: &log})
	t.Cleanup(discovery.Stop)
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.attempts("permission") == 1 })

	got := log.String()
	if got != "AIFAR discovery sync failed service=permission reason=RegistrationFailed\n" {
		t.Fatalf("unexpected discovery log: %q", got)
	}
}

func TestDiscoveryStopInstanceRemovesWorkers(t *testing.T) {
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)
	discovery.EndpointChanged(readyDiscoveryEvent("permission", "permission-1"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.registered("permission") })

	discovery.StopInstance("admin")
	discovery.mu.Lock()
	workerCount := len(discovery.workers)
	discovery.mu.Unlock()
	if workerCount != 0 {
		t.Fatalf("workers remain after stop: %d", workerCount)
	}
	discovery.EndpointChanged(readyDiscoveryEvent("file", "file-1"))
	waitForDiscovery(t, time.Second, func() bool { return syncer.registered("file") })
}

func TestServiceControllerPublishesDiscoveryWithoutWaitingForNacos(t *testing.T) {
	syncer := &blockingDiscoverySyncer{started: make(chan struct{}, 1), release: make(chan struct{})}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, newControllerTestRunner(), func(options *ManagerOptions) {
		options.discoveryController = discovery
	})

	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-syncer.started:
	case <-time.After(time.Second):
		t.Fatal("discovery registration was not started")
	}
	waitForDeploymentCondition(t, manager, "permission", deploymentConditionAvailable, 250*time.Millisecond)
	close(syncer.release)
}

func TestManagerLoadReplaysEachPersistedDeploymentIntoSharedDiscovery(t *testing.T) {
	stateDir := t.TempDir()
	store := &ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(controllerTestConfig()); err != nil {
		t.Fatal(err)
	}
	for _, service := range []string{"file", "permission"} {
		if _, err := store.Put(controllerTestManifest(service, 1, 0)); err != nil {
			t.Fatal(err)
		}
	}
	syncer := &fakeDiscoverySyncer{failFor: map[string]error{"file": errors.New("nacos down")}}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)
	manager := NewManager(ManagerOptions{
		StateDir:            stateDir,
		Runner:              newControllerTestRunner(),
		ManifestStore:       store,
		discoveryController: discovery,
	})
	if err := manager.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("deregister", "permission") == 1 })
	if syncer.attempts("file") == 0 {
		t.Fatal("file discovery replay was not attempted")
	}
}

func TestManagerRemoveStopsSharedDiscoveryInstanceWorkers(t *testing.T) {
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, newControllerTestRunner(), func(options *ManagerOptions) {
		options.discoveryController = discovery
	})
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 0)); err != nil {
		t.Fatal(err)
	}
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("deregister", "permission") == 1 })
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	discovery.mu.Lock()
	workerCount := len(discovery.workers)
	discovery.mu.Unlock()
	if workerCount != 0 {
		t.Fatalf("discovery workers leaked after remove: %d", workerCount)
	}
}

func TestManagerRemoveCleansDiscoveryPublishedByCancelledReconcile(t *testing.T) {
	runner := newControllerTestRunner()
	runner.blockedService = "permission"
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, runner, func(options *ManagerOptions) {
		options.discoveryController = discovery
	})
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.blocked:
	case <-time.After(time.Second):
		t.Fatal("service reconcile did not block")
	}
	if err := manager.Remove(context.Background(), "admin"); err != nil {
		t.Fatal(err)
	}
	discovery.mu.Lock()
	workerCount := len(discovery.workers)
	discovery.mu.Unlock()
	if workerCount != 0 {
		t.Fatalf("cancelled reconcile republished discovery worker after remove: %d", workerCount)
	}
}

type blockingDiscoverySyncer struct {
	started chan struct{}
	release chan struct{}
}

func (s *blockingDiscoverySyncer) RefreshRoutes(context.Context, EndpointEvent) error { return nil }
func (s *blockingDiscoverySyncer) Register(ctx context.Context, _ EndpointEvent) error {
	select {
	case s.started <- struct{}{}:
	default:
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-s.release:
		return errors.New("nacos down")
	}
}
func (s *blockingDiscoverySyncer) Deregister(ctx context.Context, event EndpointEvent) error {
	return s.Register(ctx, event)
}
func (s *blockingDiscoverySyncer) Heartbeat(context.Context, EndpointEvent) error { return nil }

type cancellingDiscoverySyncer struct {
	started   chan string
	cancelled chan string
}

func (s *cancellingDiscoverySyncer) RefreshRoutes(context.Context, EndpointEvent) error { return nil }
func (s *cancellingDiscoverySyncer) Register(ctx context.Context, event EndpointEvent) error {
	pod := ""
	if len(event.Ready) > 0 {
		pod = event.Ready[0].PodID
	}
	s.started <- pod
	<-ctx.Done()
	s.cancelled <- pod
	return ctx.Err()
}
func (s *cancellingDiscoverySyncer) Deregister(ctx context.Context, event EndpointEvent) error {
	return s.Register(ctx, event)
}
func (s *cancellingDiscoverySyncer) Heartbeat(ctx context.Context, event EndpointEvent) error {
	return s.Register(ctx, event)
}

type fakeDiscoverySyncer struct {
	mu                sync.Mutex
	failFor           map[string]error
	panicFor          map[string]int
	panicHeartbeatFor map[string]int
	calls             []fakeDiscoveryCall
}

type fakeDiscoveryCall struct {
	action  string
	service string
	event   EndpointEvent
}

func (f *fakeDiscoverySyncer) RefreshRoutes(_ context.Context, event EndpointEvent) error {
	f.record("refresh", event)
	return nil
}

func (f *fakeDiscoverySyncer) Register(_ context.Context, event EndpointEvent) error {
	f.maybePanic(event.ServiceName)
	f.record("register", event)
	return f.failure(event.ServiceName)
}

func (f *fakeDiscoverySyncer) Deregister(_ context.Context, event EndpointEvent) error {
	f.record("deregister", event)
	return f.failure(event.ServiceName)
}

func (f *fakeDiscoverySyncer) Heartbeat(_ context.Context, event EndpointEvent) error {
	f.mu.Lock()
	if f.panicHeartbeatFor[event.ServiceName] > 0 {
		f.panicHeartbeatFor[event.ServiceName]--
		f.mu.Unlock()
		panic("heartbeat secret panic payload")
	}
	f.mu.Unlock()
	f.record("heartbeat", event)
	return f.failure(event.ServiceName)
}

func (f *fakeDiscoverySyncer) record(action string, event EndpointEvent) {
	f.mu.Lock()
	f.calls = append(f.calls, fakeDiscoveryCall{action: action, service: event.ServiceName, event: event})
	f.mu.Unlock()
}

func (f *fakeDiscoverySyncer) maybePanic(service string) {
	f.mu.Lock()
	if f.panicFor[service] > 0 {
		f.panicFor[service]--
		f.mu.Unlock()
		panic("secret panic payload")
	}
	f.mu.Unlock()
}

func (f *fakeDiscoverySyncer) failure(service string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.failFor[service]
}

func (f *fakeDiscoverySyncer) setFailure(service string, err error) {
	f.mu.Lock()
	if f.failFor == nil {
		f.failFor = map[string]error{}
	}
	f.failFor[service] = err
	f.mu.Unlock()
}

func (f *fakeDiscoverySyncer) count(action, service string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if call.action == action && call.service == service {
			count++
		}
	}
	return count
}

func (f *fakeDiscoverySyncer) registeredPod(service, pod string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.action != "register" || call.service != service {
			continue
		}
		for _, endpoint := range call.event.Ready {
			if endpoint.PodID == pod {
				return true
			}
		}
	}
	return false
}

func (f *fakeDiscoverySyncer) lastRegisteredPod(service string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	for index := len(f.calls) - 1; index >= 0; index-- {
		call := f.calls[index]
		if call.action == "register" && call.service == service && len(call.event.Ready) > 0 {
			return call.event.Ready[0].PodID
		}
	}
	return ""
}

func (f *fakeDiscoverySyncer) snapshotCalls() []fakeDiscoveryCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeDiscoveryCall(nil), f.calls...)
}

func (f *fakeDiscoverySyncer) registered(service string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, call := range f.calls {
		if call.action == "register" && call.service == service {
			return true
		}
	}
	return false
}

func (f *fakeDiscoverySyncer) attempts(service string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, call := range f.calls {
		if call.service == service && call.action != "refresh" {
			count++
		}
	}
	return count
}

func readyDiscoveryEvent(service string, pods ...string) EndpointEvent {
	ready := make([]ReadyEndpoint, 0, len(pods))
	for _, pod := range pods {
		ready = append(ready, ReadyEndpoint{PodID: pod, ContainerName: pod, Revision: "rev-1", Port: 8080})
	}
	return EndpointEvent{
		InstanceID:  "admin",
		ServiceName: service,
		AppName:     "alpha-" + service,
		ListenPort:  30000,
		Ready:       ready,
	}
}

func waitForDiscovery(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for discovery condition")
}

func discoveryAppliedPod(controller *DiscoveryController, service string) string {
	controller.mu.Lock()
	worker := controller.workers[endpointKey("admin", service)]
	controller.mu.Unlock()
	if worker == nil {
		return ""
	}
	worker.mu.Lock()
	defer worker.mu.Unlock()
	if len(worker.applied.Ready) == 0 {
		return ""
	}
	return worker.applied.Ready[0].PodID
}
