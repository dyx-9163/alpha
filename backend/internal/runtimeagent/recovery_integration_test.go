package runtimeagent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestAgentStatusOmitsNacosWhileDiscoveryStillRegistersAndHeartbeats(t *testing.T) {
	runner := newControllerTestRunner()
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{
		Syncer: syncer, HeartbeatInterval: 5 * time.Millisecond,
	})
	t.Cleanup(discovery.Stop)
	stateDir := t.TempDir()
	manifestStore := &ManifestStore{StateDir: stateDir}
	manager := NewManager(ManagerOptions{
		StateDir: stateDir, Runner: runner, ManifestStore: manifestStore, discoveryController: discovery,
	})
	t.Cleanup(func() { _ = manager.Remove(context.Background(), "admin") })
	manifest := controllerTestManifest("permission", 1, 1)
	servicePort := freePort(t)
	manifest.Service.Port = servicePort
	manifest.Service.ListenPort = servicePort
	manifest.Service.TargetPort = servicePort
	legacy := runtimeSpecForDeployment(controllerTestConfig(), manifest)
	legacy.Ingress.GatewayPort = freePort(t)
	legacy.Ingress.WebPort = freePort(t)
	if err := manager.Apply(context.Background(), legacy); err != nil {
		t.Fatal(err)
	}
	if err := manifestStore.PutInstance(NormalizeInstanceConfig(InstanceConfig{
		APIVersion: ManifestAPIVersion, InstanceID: legacy.InstanceID, InstallRoot: legacy.InstallRoot, Network: legacy.Network, Ingress: legacy.Ingress,
	})); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.AcceptDeployment(context.Background(), manifest); err != nil {
		t.Fatal(err)
	}
	waitForDiscovery(t, time.Second, func() bool {
		return syncer.registered("permission") && syncer.count("heartbeat", "permission") > 0
	})
	statusJSON, err := json.Marshal(manager.Status())
	if err != nil {
		t.Fatal(err)
	}
	status := string(statusJSON)
	for _, forbidden := range []string{"nacosRegistered", "nacosReady", "lastNacosHeartbeatAt", "lastNacosError", `"nacos"`} {
		if strings.Contains(status, forbidden) {
			t.Fatalf("ordinary Agent status leaked Nacos runtime field %q: %s", forbidden, status)
		}
	}
	if !strings.Contains(status, `"serviceName":"permission"`) {
		t.Fatalf("ordinary Agent status lost accepted service shape: %s", status)
	}
}

func TestFiveMinuteFileFailureDoesNotDelayPermissionAvailabilityOrDiscovery(t *testing.T) {
	runner := newControllerTestRunner()
	runner.blockedService = "file"
	clock := newFakeControllerClock()
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{
		Syncer:            syncer,
		HeartbeatInterval: 5 * time.Millisecond,
	})
	t.Cleanup(discovery.Stop)
	manager := newControllerTestManager(t, runner, func(options *ManagerOptions) {
		options.controllerClock = clock
		options.discoveryController = discovery
	})
	t.Cleanup(func() { manager.removeServiceControllers("admin") })

	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("file", 1, 1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-runner.blocked:
	case <-time.After(time.Second):
		t.Fatal("file did not enter its unready reconcile")
	}
	fileProgressing := waitForDeploymentCondition(t, manager, "file", deploymentConditionProgressing, time.Second)
	fileTransition := currentCondition(fileProgressing).LastTransitionTime

	clock.mu.Lock()
	clock.now = clock.now.Add(5 * time.Minute)
	clock.mu.Unlock()
	if _, err := manager.AcceptDeployment(context.Background(), controllerTestManifest("permission", 1, 1)); err != nil {
		t.Fatal(err)
	}
	permission := waitForDeploymentCondition(t, manager, "permission", deploymentConditionAvailable, time.Second)
	permissionTransition := currentCondition(permission).LastTransitionTime
	if permissionTransition.Sub(fileTransition) < 5*time.Minute {
		t.Fatalf("permission did not converge after the simulated five-minute file failure: file=%s permission=%s", fileTransition, permissionTransition)
	}
	file, ok := manager.DeploymentState("admin", "file")
	if !ok {
		t.Fatal("file state disappeared")
	}
	if condition := currentCondition(file); condition.Type == deploymentConditionAvailable {
		t.Fatalf("blocked file unexpectedly became available: %+v", file)
	}
	waitForDiscovery(t, time.Second, func() bool {
		return syncer.registered("permission") && syncer.count("heartbeat", "permission") > 0
	})
}

func TestRecoveryIsolatesCorruptManifestAndKeepsOfflineAcrossResyncAndDockerEvent(t *testing.T) {
	stateDir := t.TempDir()
	manifestStore := &ManifestStore{StateDir: stateDir}
	config := controllerTestConfig()
	if err := manifestStore.PutInstance(config); err != nil {
		t.Fatal(err)
	}
	for _, manifest := range []DeploymentManifest{
		controllerTestManifest("file", 1, 0),
		controllerTestManifest("permission", 1, 1),
	} {
		if _, err := manifestStore.Put(manifest); err != nil {
			t.Fatal(err)
		}
	}
	deploymentsDir := filepath.Join(stateDir, "admin", "deployments")
	if err := os.WriteFile(filepath.Join(deploymentsDir, "message.json"), []byte("{corrupt-secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	runner := newControllerTestRunner()
	staleFilePod := "aifar-pod-admin-file-rev-old-r1"
	runner.pods[staleFilePod] = true
	syncer := &fakeDiscoverySyncer{}
	discovery := newDiscoveryController(discoveryControllerOptions{
		Syncer:            syncer,
		HeartbeatInterval: 5 * time.Millisecond,
	})
	t.Cleanup(discovery.Stop)
	var logs lockedBuffer
	manager := NewManager(ManagerOptions{
		StateDir:            stateDir,
		Runner:              runner,
		Log:                 &logs,
		ManifestStore:       manifestStore,
		discoveryController: discovery,
	})
	t.Cleanup(func() { manager.removeServiceControllers("admin") })

	// A stale aggregate snapshot deliberately asks for file=1. The instance
	// marker and per-service Manifest must keep that retired authority inert.
	staleAggregate := runtimeSpecForDeployment(config, controllerTestManifest("file", 1, 1))
	manager.mu.Lock()
	manager.specs[config.InstanceID] = staleAggregate
	manager.mu.Unlock()

	if err := manager.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForDeploymentCondition(t, manager, "permission", deploymentConditionAvailable, time.Second)
	waitForDeploymentCondition(t, manager, "file", deploymentConditionOffline, time.Second)
	rejected := waitForDeploymentCondition(t, manager, "message", deploymentConditionDegraded, time.Second)
	if condition := currentCondition(rejected); condition.Reason != "SpecRejected" {
		t.Fatalf("corrupt Manifest condition=%+v", condition)
	}
	if strings.Contains(logs.String(), "corrupt-secret") {
		t.Fatalf("corrupt Manifest payload leaked to Agent logs: %s", logs.String())
	}
	waitForDiscovery(t, time.Second, func() bool {
		return syncer.registered("permission") && syncer.count("heartbeat", "permission") > 0 && syncer.count("deregister", "file") > 0
	})
	waitForIntegrationPodAbsent(t, runner, staleFilePod)

	runner.mu.Lock()
	runner.pods[staleFilePod] = true
	runner.mu.Unlock()
	if err := manager.Resync(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForIntegrationPodAbsent(t, runner, staleFilePod)

	runner.mu.Lock()
	runner.pods[staleFilePod] = true
	runner.mu.Unlock()
	if runtime.GOOS == "windows" {
		// The watcher uses a real executable boundary. Windows executes the
		// identical event consequence directly; Linux CI exercises the process.
		if err := manager.Resync(context.Background()); err != nil {
			t.Fatal(err)
		}
	} else {
		installFakeDockerEventCommand(t)
		if err := manager.watchDockerEvents(context.Background(), time.Millisecond); err != nil {
			t.Fatal(err)
		}
	}
	waitForIntegrationPodAbsent(t, runner, staleFilePod)
	file := waitForDeploymentCondition(t, manager, "file", deploymentConditionOffline, time.Second)
	if file.DesiredReplicas != 0 || file.ReadyReplicas != 0 {
		t.Fatalf("offline file was resurrected: %+v", file)
	}
}

func waitForIntegrationPodAbsent(t *testing.T, runner *controllerTestRunner, pod string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runner.mu.Lock()
		_, exists := runner.pods[pod]
		runner.mu.Unlock()
		if !exists {
			return
		}
		time.Sleep(time.Millisecond)
	}
	runner.mu.Lock()
	defer runner.mu.Unlock()
	t.Fatalf("offline pod %s still exists: %+v", pod, runner.pods)
}

func installFakeDockerEventCommand(t *testing.T) {
	t.Helper()
	directory := t.TempDir()
	name := "docker"
	body := "#!/bin/sh\nprintf '1 start file\\n'\n"
	if runtime.GOOS == "windows" {
		name = "docker.cmd"
		body = "@echo off\r\necho 1 start file\r\n"
	}
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
}
