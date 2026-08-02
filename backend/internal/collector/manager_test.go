package collector

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

func TestSnapshotEventPayloadIncludesDecodedSnapshotPayload(t *testing.T) {
	collectedAt := time.Date(2026, 7, 7, 10, 0, 0, 0, time.UTC)
	updatedAt := collectedAt.Add(time.Second)
	payload := snapshotEventPayload(store.StatusSnapshot{
		Scope:       "docker.summary",
		ResourceID:  "srv-1",
		ServerID:    "srv-1",
		Status:      "available",
		LastError:   "",
		Version:     3,
		CollectedAt: collectedAt,
		UpdatedAt:   updatedAt,
		Payload:     `{"available":true,"summary":{"running":2}}`,
	})

	if payload["scope"] != "docker.summary" || payload["resourceId"] != "srv-1" || payload["version"] != int64(3) {
		t.Fatalf("unexpected snapshot metadata: %#v", payload)
	}
	decoded, ok := payload["payload"].(map[string]any)
	if !ok || decoded["available"] != true {
		t.Fatalf("expected decoded payload, got %#v", payload["payload"])
	}
	summary, ok := decoded["summary"].(map[string]any)
	if !ok || summary["running"] != float64(2) {
		t.Fatalf("expected decoded nested summary, got %#v", decoded["summary"])
	}
}

func TestAIFARRuntimeSnapshotFollowsDockerSummaryFailure(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(store.Server{Name: "node-1", Host: "10.0.0.10", DockerHost: "tcp://10.0.0.10:2375"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "aifar", Version: "runtime-v2", ServerID: server.ID, Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{InstanceID: instance.ID, ServiceName: "gateway", DesiredReplicas: 1, CurrentRevision: "rev-1", Status: "ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARPod(store.AIFARPod{InstanceID: instance.ID, ServiceName: "gateway", Revision: "rev-1", PodID: "r1", ContainerName: "pod-gateway-r1", Status: "ready", Ready: true}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "gateway", []store.AIFARServiceEndpoint{{InstanceID: instance.ID, ServiceName: "gateway", PodID: "r1", ContainerName: "pod-gateway-r1", Revision: "rev-1", State: "active", Ready: true}}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{Scope: "docker.summary", ResourceID: server.ID, ServerID: server.ID, Status: "failed", LastError: "connection refused", Payload: `{"available":false}`}); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(db, nil, time.Minute)
	if err := manager.collectAIFARRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetStatusSnapshot("aifar.runtime", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "no-endpoints" {
		t.Fatalf("expected runtime snapshot to follow Docker failure, got %+v", snapshot)
	}
}

func TestAppInstanceCollectorWritesFailedSnapshot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(store.Server{Name: "node-1", Host: "10.0.0.10"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "standalone"})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(db, nil, time.Minute)
	manager.SetAppRegistry(registry.New(fakeCheckModule{
		name:   "mysql",
		status: registry.InstanceStatus{Status: "failed", Message: "service down"},
		err:    errors.New("service down"),
	}))

	err = manager.collectAppInstances(context.Background())
	if err == nil {
		t.Fatal("expected collector to report failed app instance check")
	}
	snapshot, err := db.GetStatusSnapshot("app.instance", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "failed" || snapshot.LastError != "service down" {
		t.Fatalf("expected failed app instance snapshot, got %+v", snapshot)
	}
}

func TestAppInstanceCollectorTimeoutDoesNotBlockOtherInstances(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(store.Server{Name: "node-1", Host: "10.0.0.10"})
	if err != nil {
		t.Fatal(err)
	}
	fastInstance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "standalone"})
	if err != nil {
		t.Fatal(err)
	}
	slowInstance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "running", Topology: "standalone"})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(db, nil, time.Minute)
	manager.appInstanceTimeout = 25 * time.Millisecond
	manager.SetAppRegistry(registry.New(fakeCheckModule{
		name: "mysql",
		check: func(ctx context.Context, req registry.CheckRequest) (registry.InstanceStatus, error) {
			if req.Instance.ID == slowInstance.ID {
				time.Sleep(200 * time.Millisecond)
			}
			return registry.InstanceStatus{Status: "running", Message: "ok"}, nil
		},
	}))

	startedAt := time.Now()
	err = manager.collectAppInstances(context.Background())
	elapsed := time.Since(startedAt)

	if err == nil || !strings.Contains(err.Error(), slowInstance.ID) {
		t.Fatalf("expected timeout error for slow instance, got %v", err)
	}
	if elapsed > 150*time.Millisecond {
		t.Fatalf("slow instance blocked collection for %s", elapsed)
	}
	fastSnapshot, err := db.GetStatusSnapshot("app.instance", fastInstance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fastSnapshot.Status != "running" {
		t.Fatalf("expected fast instance snapshot to be running, got %+v", fastSnapshot)
	}
	slowSnapshot, err := db.GetStatusSnapshot("app.instance", slowInstance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if slowSnapshot.Status != "unavailable" || !strings.Contains(slowSnapshot.LastError, "collector timeout") {
		t.Fatalf("expected slow instance timeout snapshot, got %+v", slowSnapshot)
	}
}

func TestDockerSummaryCollectorTimeoutDoesNotBlockOtherHosts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	slowIDs := map[string]bool{}
	for index := 0; index < 4; index++ {
		server, err := db.SaveServer(store.Server{Name: "slow", Host: "10.0.0.10", DockerHost: "tcp://10.0.0.10:2375"})
		if err != nil {
			t.Fatal(err)
		}
		slowIDs[server.ID] = true
	}
	fastServer, err := db.SaveServer(store.Server{Name: "fast", Host: "10.0.0.20", DockerHost: "tcp://10.0.0.20:2375"})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(db, nil, time.Minute)
	manager.dockerSummaryTimeout = 30 * time.Millisecond
	slowStarted := make(chan struct{})
	var slowStartedOnce sync.Once
	manager.dockerSummaryForServer = func(ctx context.Context, server store.Server) (adapter.DockerSummary, error) {
		if slowIDs[server.ID] {
			slowStartedOnce.Do(func() { close(slowStarted) })
			<-ctx.Done()
			return adapter.DockerSummary{}, ctx.Err()
		}
		select {
		case <-slowStarted:
		case <-ctx.Done():
			return adapter.DockerSummary{}, ctx.Err()
		}
		return adapter.DockerSummary{Containers: 2, Images: 3, Endpoint: server.DockerHost}, nil
	}

	err = manager.collectDockerSummaries(context.Background())

	if err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("expected timeout failures for slow Docker hosts, got %v", err)
	}
	fastSnapshot, err := db.GetStatusSnapshot("docker.summary", fastServer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fastSnapshot.Status != "available" {
		t.Fatalf("expected fast Docker host snapshot to be available, got %+v", fastSnapshot)
	}
}

func TestCollectorUsesSeparateRemoteCheckTimeoutBudgets(t *testing.T) {
	manager := NewManager(nil, nil, time.Minute)

	if manager.dockerSummaryTimeout != 5*time.Second {
		t.Fatalf("expected Docker summary timeout to remain 5s, got %s", manager.dockerSummaryTimeout)
	}
	if manager.appInstanceTimeout != 30*time.Second {
		t.Fatalf("expected SSH-backed app check timeout to be 30s, got %s", manager.appInstanceTimeout)
	}
}

func TestSaveSnapshotPublishesEveryCollection(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	events := realtime.NewHub()
	ch, unsubscribe := events.Subscribe()
	defer unsubscribe()
	manager := NewManager(db, events, time.Minute)
	snapshot := store.StatusSnapshot{
		Scope:       "app.instance",
		ResourceID:  "app-1",
		ServerID:    "srv-1",
		Status:      "running",
		Payload:     `{"app":"redis","status":"running"}`,
		CollectedAt: time.Now(),
	}

	if err := manager.saveSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	first := nextEvent(t, ch)
	if first.InstanceID != "app-1" || first.Payload["changed"] != true {
		t.Fatalf("expected first snapshot event to be changed app instance, got %+v", first)
	}
	if err := manager.saveSnapshot(context.Background(), snapshot); err != nil {
		t.Fatal(err)
	}
	second := nextEvent(t, ch)
	if second.InstanceID != "app-1" || second.Payload["changed"] != false {
		t.Fatalf("expected unchanged snapshot to still be published, got %+v", second)
	}
}

func nextEvent(t *testing.T, ch <-chan realtime.Event) realtime.Event {
	t.Helper()
	select {
	case event := <-ch:
		return event
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for realtime event")
		return realtime.Event{}
	}
}

type fakeCheckModule struct {
	name   string
	status registry.InstanceStatus
	err    error
	check  func(context.Context, registry.CheckRequest) (registry.InstanceStatus, error)
}

func (m fakeCheckModule) Name() string { return m.name }

func (m fakeCheckModule) Manifest(string) registry.Manifest { return registry.Manifest{Name: m.name} }

func (m fakeCheckModule) PreflightInstall(context.Context, registry.InstallRequest, []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}

func (m fakeCheckModule) PlanInstall(context.Context, registry.InstallRequest, []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (m fakeCheckModule) ValidateInstall(context.Context, registry.InstallRequest, []store.Resource) error {
	return nil
}

func (m fakeCheckModule) Install(context.Context, registry.InstallRequest, registry.RunContext) error {
	return nil
}

func (m fakeCheckModule) PlanCheck(context.Context, registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (m fakeCheckModule) Check(ctx context.Context, req registry.CheckRequest, run registry.RunContext) (registry.InstanceStatus, error) {
	if m.check != nil {
		return m.check(ctx, req)
	}
	return m.status, m.err
}
