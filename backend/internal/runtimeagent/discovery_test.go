package runtimeagent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
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
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("refresh", "permission") == 3 })
	if got := syncer.count("register", "permission"); got != 1 {
		t.Fatalf("positive endpoint churn registered Nacos %d times, want 1", got)
	}
	discovery.EndpointChanged(offline)
	waitForDiscovery(t, time.Second, func() bool { return syncer.count("deregister", "permission") == 2 })

	if got := syncer.count("refresh", "permission"); got != 4 {
		t.Fatalf("route refresh count=%d, want one for each distinct transition", got)
	}
}

func TestDiscoveryRouteRollsBackAndRegistrationRecoversAcrossAtoBtoA(t *testing.T) {
	syncer := newPortTransitionDiscoverySyncer()
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(discovery.Stop)

	a := readyDiscoveryEvent("permission", "permission-a")
	a.ListenPort = 38010
	discovery.EndpointChanged(a)
	waitForDiscovery(t, time.Second, func() bool { return syncer.registerCount(38010) == 1 })

	b := readyDiscoveryEvent("permission", "permission-b")
	b.ListenPort = 38011
	discovery.EndpointChanged(b)
	select {
	case <-syncer.blockedRegister:
	case <-time.After(time.Second):
		t.Fatal("B registration did not block")
	}
	discovery.EndpointChanged(a)
	waitForDiscovery(t, time.Second, func() bool {
		return syncer.refreshCount(38010) >= 2 && syncer.registerCount(38010) >= 2
	})
	if got := syncer.registerCount(38011); got != 0 {
		t.Fatalf("cancelled B registration completed %d times", got)
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
	changedIdentity := readyDiscoveryEvent("permission", "permission-2")
	changedIdentity.ListenPort = 30001
	discovery.EndpointChanged(changedIdentity)
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
		return discoveryAppliedPod(discovery, "file") == "file-2" && discoveryAppliedPod(discovery, "permission") == "permission-2"
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

func TestManagerDiscoveryRouteReplacesListenPortAndAllowsReclaim(t *testing.T) {
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: &fakeDiscoverySyncer{}})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, newControllerTestRunner(), func(options *ManagerOptions) {
		options.discoveryController = discovery
	})
	t.Cleanup(func() { _ = manager.Remove(context.Background(), "admin") })
	syncer := &managerDiscoverySyncer{manager: manager}
	portA, portB := freePort(t), freePort(t)
	if portA == portB {
		t.Fatal("expected distinct free ports")
	}

	applyPort := func(generation int64, port int) {
		t.Helper()
		manifest := controllerTestManifest("permission", generation, 1)
		manifest.Service.Port = port
		manifest.Service.ListenPort = port
		manifest.Service.TargetPort = port
		if _, err := manager.manifestStore.Put(manifest); err != nil {
			t.Fatal(err)
		}
		event := readyDiscoveryEvent("permission", "permission-1")
		event.ListenPort = port
		if err := syncer.RefreshRoutes(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	assertOnlyPort := func(want, old int) {
		t.Helper()
		manager.mu.RLock()
		_, hasWantRoute := manager.routes[want]
		_, hasOldRoute := manager.routes[old]
		_, hasWantServer := manager.servers[want]
		_, hasOldServer := manager.servers[old]
		manager.mu.RUnlock()
		if !hasWantRoute || !hasWantServer || hasOldRoute || hasOldServer {
			t.Fatalf("route replacement want=%d old=%d routes=%t/%t servers=%t/%t", want, old, hasWantRoute, hasOldRoute, hasWantServer, hasOldServer)
		}
	}

	applyPort(1, portA)
	applyPort(2, portB)
	assertOnlyPort(portB, portA)
	applyPort(3, portA)
	assertOnlyPort(portA, portB)
}

func TestManagerDiscoveryRouteRestoresOldPortWhenNewListenerFails(t *testing.T) {
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: &fakeDiscoverySyncer{}})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, newControllerTestRunner(), func(options *ManagerOptions) {
		options.discoveryController = discovery
	})
	t.Cleanup(func() { _ = manager.Remove(context.Background(), "admin") })
	syncer := &managerDiscoverySyncer{manager: manager}
	oldPort := freePort(t)
	occupied, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	newPort := occupied.Addr().(*net.TCPAddr).Port

	putAndRefresh := func(generation int64, port int) error {
		manifest := controllerTestManifest("permission", generation, 1)
		manifest.Service.Port = port
		manifest.Service.ListenPort = port
		manifest.Service.TargetPort = port
		if _, err := manager.manifestStore.Put(manifest); err != nil {
			return err
		}
		event := readyDiscoveryEvent("permission", "permission-1")
		event.ListenPort = port
		return syncer.RefreshRoutes(context.Background(), event)
	}
	if err := putAndRefresh(1, oldPort); err != nil {
		t.Fatal(err)
	}
	if err := putAndRefresh(2, newPort); err == nil {
		t.Fatal("expected occupied replacement port to fail")
	}
	manager.mu.RLock()
	oldRoute := manager.routes[oldPort]
	_, newRoute := manager.routes[newPort]
	_, oldServer := manager.servers[oldPort]
	_, newServer := manager.servers[newPort]
	manager.mu.RUnlock()
	if oldRoute.InstanceID != "admin" || oldRoute.Service != "permission" || newRoute || !oldServer || newServer {
		t.Fatalf("failed replacement did not restore old route: old=%+v newRoute=%t oldServer=%t newServer=%t", oldRoute, newRoute, oldServer, newServer)
	}
}

func TestManagerDiscoveryRouteFailurePreservesConcurrentPeerSpecUpdate(t *testing.T) {
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: &fakeDiscoverySyncer{}})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, newControllerTestRunner(), func(options *ManagerOptions) {
		options.discoveryController = discovery
	})
	t.Cleanup(func() { _ = manager.Remove(context.Background(), "admin") })
	fileOldPort, fileAttemptPort := freePort(t), freePort(t)
	permissionOldPort, permissionNewPort := freePort(t), freePort(t)
	blocked := make(chan struct{})
	release := make(chan struct{})
	syncer := &managerDiscoverySyncer{
		manager: manager,
		startPort: func(port int) error {
			if port == fileAttemptPort {
				close(blocked)
				<-release
				return errors.New("injected listener failure")
			}
			return manager.startPort(port)
		},
	}

	putAndRefresh := func(service string, generation int64, port int) error {
		manifest := controllerTestManifest(service, generation, 1)
		manifest.Spec.Image = fmt.Sprintf("aifar-%s:gen-%d", service, generation)
		manifest.Spec.PodRevision = fmt.Sprintf("gen-%d", generation)
		manifest.Service.Port = port
		manifest.Service.ListenPort = port
		manifest.Service.TargetPort = port
		if _, err := manager.manifestStore.Put(manifest); err != nil {
			return err
		}
		event := readyDiscoveryEvent(service, service+"-1")
		event.ListenPort = port
		return syncer.RefreshRoutes(context.Background(), event)
	}
	if err := putAndRefresh("file", 1, fileOldPort); err != nil {
		t.Fatal(err)
	}
	if err := putAndRefresh("permission", 1, permissionOldPort); err != nil {
		t.Fatal(err)
	}

	fileDone := make(chan error, 1)
	go func() { fileDone <- putAndRefresh("file", 2, fileAttemptPort) }()
	select {
	case <-blocked:
	case <-time.After(time.Second):
		t.Fatal("file listener attempt did not reach the unlocked start window")
	}
	if err := putAndRefresh("permission", 2, permissionNewPort); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-fileDone; err == nil {
		t.Fatal("expected file listener replacement failure")
	}

	manager.mu.RLock()
	spec := manager.specs["admin"]
	fileOldRoute := manager.routes[fileOldPort]
	_, fileAttemptRoute := manager.routes[fileAttemptPort]
	permissionNewRoute := manager.routes[permissionNewPort]
	_, permissionOldRoute := manager.routes[permissionOldPort]
	manager.mu.RUnlock()
	if got := discoverySpecServicePort(spec, "file"); got != fileOldPort {
		t.Fatalf("file rollback port=%d, want %d", got, fileOldPort)
	}
	if got := discoverySpecServicePort(spec, "permission"); got != permissionNewPort {
		t.Fatalf("concurrent permission update rolled back to port=%d, want %d", got, permissionNewPort)
	}
	if got := discoverySpecImage(spec, "permission"); got != "aifar-permission:gen-2" {
		t.Fatalf("concurrent permission image rolled back to %q", got)
	}
	if fileOldRoute.Service != "file" || fileAttemptRoute || permissionNewRoute.Service != "permission" || permissionOldRoute {
		t.Fatalf("unexpected routes after scoped rollback: fileOld=%+v fileAttempt=%t permissionNew=%+v permissionOld=%t", fileOldRoute, fileAttemptRoute, permissionNewRoute, permissionOldRoute)
	}
}

func TestManagerDiscoveryRetiredListenerDoesNotBlockRegistrationOrStop(t *testing.T) {
	manager := newControllerTestManager(t, newControllerTestRunner())
	retirementStarted := make(chan struct{})
	releaseRetirement := make(chan struct{})
	routeSyncer := &managerDiscoverySyncer{
		manager: manager,
		shutdownServer: func(*http.Server) {
			close(retirementStarted)
			<-releaseRetirement
		},
	}
	syncer := &retirementDiscoverySyncer{routes: routeSyncer, registered: make(chan int, 4)}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(func() {
		select {
		case <-releaseRetirement:
		default:
			close(releaseRetirement)
		}
		discovery.Stop()
		_ = manager.Remove(context.Background(), "admin")
	})
	oldPort, newPort := freePort(t), freePort(t)

	publish := func(generation int64, port int) {
		t.Helper()
		manifest := controllerTestManifest("permission", generation, 1)
		manifest.Service.Port = port
		manifest.Service.ListenPort = port
		manifest.Service.TargetPort = port
		if _, err := manager.manifestStore.Put(manifest); err != nil {
			t.Fatal(err)
		}
		event := readyDiscoveryEvent("permission", fmt.Sprintf("permission-%d", generation))
		event.ListenPort = port
		discovery.EndpointChanged(event)
	}
	publish(1, oldPort)
	select {
	case got := <-syncer.registered:
		if got != oldPort {
			t.Fatalf("initial registered port=%d, want %d", got, oldPort)
		}
	case <-time.After(time.Second):
		t.Fatal("initial registration did not finish")
	}

	publish(2, newPort)
	select {
	case <-retirementStarted:
	case <-time.After(time.Second):
		t.Fatal("retired listener shutdown did not start")
	}
	select {
	case got := <-syncer.registered:
		if got != newPort {
			t.Fatalf("replacement registered port=%d, want %d", got, newPort)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("blocked retired-listener shutdown delayed new registration")
	}
	stopped := make(chan struct{})
	go func() {
		discovery.StopInstance("admin")
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(250 * time.Millisecond):
		t.Fatal("blocked retired-listener shutdown delayed StopInstance")
	}
	close(releaseRetirement)
}

func TestManagerDiscoveryStaleFailedAttemptCannotDeleteReplacementRouteOnSamePort(t *testing.T) {
	manager := newControllerTestManager(t, newControllerTestRunner())
	oldPort, port := freePort(t), freePort(t)
	baseline := &managerDiscoverySyncer{manager: manager}
	putManifest := func(generation int64, listenPort int, image string) {
		t.Helper()
		manifest := controllerTestManifest("permission", generation, 1)
		manifest.Spec.Image = image
		manifest.Spec.PodRevision = fmt.Sprintf("rev-%d", generation)
		manifest.Service.Port = listenPort
		manifest.Service.ListenPort = listenPort
		manifest.Service.TargetPort = listenPort
		if _, err := manager.manifestStore.Put(manifest); err != nil {
			t.Fatal(err)
		}
	}
	putManifest(1, oldPort, "permission:baseline")
	baselineEvent := readyDiscoveryEvent("permission", "permission-baseline")
	baselineEvent.ListenPort = oldPort
	if err := baseline.RefreshRoutes(context.Background(), baselineEvent); err != nil {
		t.Fatal(err)
	}

	oldStartBlocked := make(chan struct{})
	releaseOldStart := make(chan struct{})
	var startMu sync.Mutex
	startCalls := 0
	routeSyncer := &managerDiscoverySyncer{
		manager: manager,
		startPort: func(got int) error {
			startMu.Lock()
			startCalls++
			call := startCalls
			startMu.Unlock()
			if call == 1 {
				close(oldStartBlocked)
				<-releaseOldStart
				return errors.New("stale listener start failed")
			}
			return manager.startPort(got)
		},
	}
	syncer := &samePortLifecycleDiscoverySyncer{
		routes:     routeSyncer,
		registered: make(chan EndpointEvent, 2),
		oldDone:    make(chan error, 1),
	}
	discovery := newDiscoveryController(discoveryControllerOptions{Syncer: syncer})
	t.Cleanup(func() {
		select {
		case <-releaseOldStart:
		default:
			close(releaseOldStart)
		}
		discovery.Stop()
		_ = manager.Remove(context.Background(), "admin")
		manager.stopPort(context.Background(), oldPort)
		manager.stopPort(context.Background(), port)
	})

	putManifest(2, port, "permission:old-attempt")
	oldEvent := readyDiscoveryEvent("permission", "permission-old")
	oldEvent.ListenPort = port
	discovery.EndpointChanged(oldEvent)
	select {
	case <-oldStartBlocked:
	case <-time.After(time.Second):
		t.Fatal("old route attempt did not block in startPort")
	}
	discovery.StopInstance("admin")

	putManifest(3, port, "permission:replacement")
	newEvent := readyDiscoveryEvent("permission", "permission-new")
	newEvent.ListenPort = port
	discovery.EndpointChanged(newEvent)
	select {
	case registered := <-syncer.registered:
		if registered.Ready[0].PodID != "permission-new" {
			t.Fatalf("registered stale event: %+v", registered)
		}
	case <-time.After(time.Second):
		t.Fatal("replacement worker did not register same-port route")
	}
	close(releaseOldStart)
	select {
	case err := <-syncer.oldDone:
		if err == nil {
			t.Fatal("expected old route attempt to fail")
		}
	case <-time.After(time.Second):
		t.Fatal("old route attempt did not finish")
	}

	manager.mu.RLock()
	route, hasRoute := manager.routes[port]
	_, restoredOldRoute := manager.routes[oldPort]
	_, hasServer := manager.servers[port]
	spec := manager.specs["admin"]
	manager.mu.RUnlock()
	if !hasRoute || route.InstanceID != "admin" || route.Service != "permission" || !hasServer {
		t.Fatalf("stale failure removed replacement route/server: route=%+v hasRoute=%t hasServer=%t", route, hasRoute, hasServer)
	}
	if restoredOldRoute {
		t.Fatal("stale failure restored old route")
	}
	if got := discoverySpecServicePort(spec, "permission"); got != port {
		t.Fatalf("stale failure restored old service port=%d, want %d", got, port)
	}
	if got := discoverySpecImage(spec, "permission"); got != "permission:replacement" {
		t.Fatalf("stale failure restored old deployment image=%q", got)
	}
}

func discoverySpecServicePort(spec RuntimeSpec, service string) int {
	for _, item := range spec.Services {
		if item.Name == service {
			return item.ListenPort
		}
	}
	return 0
}

func discoverySpecImage(spec RuntimeSpec, service string) string {
	for _, deployment := range spec.Deployments {
		if deployment.ServiceName == service {
			return deployment.Image
		}
	}
	return ""
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

type portTransitionDiscoverySyncer struct {
	mu              sync.Mutex
	refreshes       map[int]int
	registers       map[int]int
	blockedRegister chan struct{}
}

type retirementDiscoverySyncer struct {
	routes     *managerDiscoverySyncer
	registered chan int
}

type samePortLifecycleDiscoverySyncer struct {
	routes     *managerDiscoverySyncer
	registered chan EndpointEvent
	oldDone    chan error
}

func (s *samePortLifecycleDiscoverySyncer) RefreshRoutes(ctx context.Context, event EndpointEvent) error {
	err := s.routes.RefreshRoutes(ctx, event)
	if len(event.Ready) > 0 && event.Ready[0].PodID == "permission-old" {
		s.oldDone <- err
	}
	return err
}

func (s *samePortLifecycleDiscoverySyncer) Register(_ context.Context, event EndpointEvent) error {
	s.registered <- event
	return nil
}

func (s *samePortLifecycleDiscoverySyncer) Deregister(context.Context, EndpointEvent) error {
	return nil
}
func (s *samePortLifecycleDiscoverySyncer) Heartbeat(context.Context, EndpointEvent) error {
	return nil
}

func (s *retirementDiscoverySyncer) RefreshRoutes(ctx context.Context, event EndpointEvent) error {
	return s.routes.RefreshRoutes(ctx, event)
}

func (s *retirementDiscoverySyncer) Register(_ context.Context, event EndpointEvent) error {
	s.registered <- event.ListenPort
	return nil
}

func (s *retirementDiscoverySyncer) Deregister(context.Context, EndpointEvent) error { return nil }
func (s *retirementDiscoverySyncer) Heartbeat(context.Context, EndpointEvent) error  { return nil }

func newPortTransitionDiscoverySyncer() *portTransitionDiscoverySyncer {
	return &portTransitionDiscoverySyncer{
		refreshes:       map[int]int{},
		registers:       map[int]int{},
		blockedRegister: make(chan struct{}, 1),
	}
}

func (s *portTransitionDiscoverySyncer) RefreshRoutes(_ context.Context, event EndpointEvent) error {
	s.mu.Lock()
	s.refreshes[event.ListenPort]++
	s.mu.Unlock()
	return nil
}

func (s *portTransitionDiscoverySyncer) Register(ctx context.Context, event EndpointEvent) error {
	if event.ListenPort == 38011 {
		select {
		case s.blockedRegister <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	s.mu.Lock()
	s.registers[event.ListenPort]++
	s.mu.Unlock()
	return nil
}

func (s *portTransitionDiscoverySyncer) Deregister(context.Context, EndpointEvent) error { return nil }
func (s *portTransitionDiscoverySyncer) Heartbeat(context.Context, EndpointEvent) error  { return nil }

func (s *portTransitionDiscoverySyncer) refreshCount(port int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refreshes[port]
}

func (s *portTransitionDiscoverySyncer) registerCount(port int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.registers[port]
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
