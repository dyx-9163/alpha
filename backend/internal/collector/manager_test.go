package collector

import (
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
