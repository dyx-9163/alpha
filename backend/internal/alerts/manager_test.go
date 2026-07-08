package alerts

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

type fakePublisher struct {
	events []realtime.Event
}

func (f *fakePublisher) Publish(event realtime.Event) {
	f.events = append(f.events, event)
}

func TestManagerCreatesAndResolvesResourceAlerts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	publisher := &fakePublisher{}
	manager := NewManager(db, publisher)
	now := time.Now()
	if err := db.UpsertCollectorRun(store.CollectorRun{Name: "docker.summary", Status: "failed", LastError: "password=collector-secret", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "docker.summary",
		ResourceID: "srv-1",
		ServerID:   "srv-1",
		Status:     "failed",
		LastError:  "token=docker-secret",
		Payload:    `{"available":false}`,
	}); err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(store.Task{Type: "apps.mysql.install", Target: "srv-1", Status: "failed", Error: "password=task-secret", CreatedBy: "tester", FinishedAt: now})
	if err != nil {
		t.Fatal(err)
	}

	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 3 {
		t.Fatalf("expected collector, docker and task alerts, got %+v", alerts)
	}
	if len(publisher.events) != 3 {
		t.Fatalf("expected three alert events, got %+v", publisher.events)
	}

	if err := db.UpsertCollectorRun(store.CollectorRun{Name: "docker.summary", Status: "success", UpdatedAt: now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "docker.summary",
		ResourceID: "srv-1",
		ServerID:   "srv-1",
		Status:     "available",
		Payload:    `{"available":true}`,
	}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	open, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].ResourceID != task.ID || open[0].Scope != "task" {
		t.Fatalf("expected only task alert to remain open, got %+v", open)
	}
}

func TestManagerCreatesRuntimeAndInstanceAlerts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "aifar.runtime",
		ResourceID: "app-aifar",
		ServerID:   "srv-1",
		Status:     "no-endpoints",
		Payload:    `{"readyPods":0,"desiredReplicas":3}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "mysql-1",
		App:      "mysql",
		Version:  "8.0",
		ServerID: "srv-2",
		Status:   "failed",
		Metadata: `{"error":"connection refused"}`,
	}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("expected runtime and instance alerts, got %+v", alerts)
	}
	seen := map[string]string{}
	for _, alert := range alerts {
		seen[alert.Scope] = alert.RequiredPermission
	}
	if seen["aifar.runtime"] != "apps.manage" || seen["app.instance"] != "database.manage" {
		t.Fatalf("unexpected required permissions: %+v", seen)
	}
}

func TestManagerTreatsUnavailableResourcesAsCritical(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "docker.summary",
		ResourceID: "srv-docker",
		ServerID:   "srv-docker",
		Status:     "failed",
		LastError:  "docker api unavailable",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "server",
		ResourceID: "srv-1",
		ServerID:   "srv-1",
		Status:     "unavailable",
		LastError:  "ssh unavailable",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "minio-1",
		App:      "minio",
		Version:  "2025",
		ServerID: "srv-2",
		Status:   "unavailable",
		Metadata: `{"error":"readiness failed"}`,
	}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 3 {
		t.Fatalf("expected three critical alerts, got %+v", alerts)
	}
	for _, alert := range alerts {
		if alert.Severity != "critical" {
			t.Fatalf("expected unavailable alert to be critical, got %+v", alert)
		}
	}
}
