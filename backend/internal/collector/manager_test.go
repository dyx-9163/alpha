package collector

import (
	"context"
	"path/filepath"
	"testing"
	"time"

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
