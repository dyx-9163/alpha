package alerts

import (
	"context"
	"path/filepath"
	"strings"
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
	if len(alerts) != 2 {
		t.Fatalf("expected docker and task alerts, got %+v", alerts)
	}
	if len(publisher.events) != 2 {
		t.Fatalf("expected two alert events, got %+v", publisher.events)
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

func TestManagerDoesNotCreateSystemAlertsForCollectorBatchFailures(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	now := time.Now()
	if err := db.UpsertCollectorRun(store.CollectorRun{Name: "app.instances", Status: "failed", LastError: "batch timeout", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertCollectorRun(store.CollectorRun{Name: "docker.summary", Status: "failed", LastError: "docker timeout", UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertAlert(store.Alert{
		Fingerprint: "collector:app.instances:failed",
		Severity:    "warning",
		Scope:       "collector",
		ResourceID:  "app.instances",
		Status:      "open",
		Title:       "Collector app.instances failed",
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
	if len(open) != 0 {
		t.Fatalf("collector batch failures should not appear in system alerts, got %+v", open)
	}
	old, err := db.GetAlertByFingerprint("collector:app.instances:failed")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "resolved" {
		t.Fatalf("old collector alert status=%q, want resolved", old.Status)
	}
}

func TestManagerCreatesRuntimeAndInstanceAlerts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "app-aifar",
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "running",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "aifar.runtime",
		ResourceID: "app-aifar",
		ServerID:   "srv-1",
		Status:     "no-endpoints",
		Payload:    `{"readyPods":0,"desiredReplicas":3}`,
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
	if len(alerts) != 1 {
		t.Fatalf("expected runtime alert only, got %+v", alerts)
	}
	seen := map[string]string{}
	for _, alert := range alerts {
		seen[alert.Scope] = alert.RequiredPermission
	}
	if seen["aifar.runtime"] != "apps.manage" {
		t.Fatalf("unexpected required permissions: %+v", seen)
	}
}

func TestManagerCreatesRuntimeAlertsForInstalledServiceSnapshots(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	cases := []struct {
		id         string
		app        string
		serverID   string
		status     string
		permission string
	}{
		{id: "app-mysql", app: "mysql", serverID: "srv-mysql", status: "failed", permission: "database.manage"},
		{id: "app-router", app: "mysql-router", serverID: "srv-router", status: "unavailable", permission: "database.manage"},
		{id: "app-redis", app: "redis", serverID: "srv-redis", status: "unhealthy", permission: "database.manage"},
		{id: "app-minio", app: "minio", serverID: "srv-minio", status: "down", permission: "storage.manage"},
		{id: "app-nacos", app: "nacos", serverID: "srv-nacos", status: "offline", permission: "apps.manage"},
	}
	for _, tc := range cases {
		if _, err := db.SaveAppInstance(store.AppInstance{ID: tc.id, App: tc.app, Version: "1.0", ServerID: tc.serverID, Status: "installed", Metadata: `{"installState":"installed"}`}); err != nil {
			t.Fatal(err)
		}
		if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
			Scope:      "app.instance",
			ResourceID: tc.id,
			ServerID:   tc.serverID,
			Status:     tc.status,
			LastError:  tc.app + " health probe failed",
			Payload:    `{"status":"` + tc.status + `"}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveAppInstance(store.AppInstance{ID: "app-docker", App: "docker", Version: "24.0.9", ServerID: "srv-docker", Status: "installed"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{Scope: "app.instance", ResourceID: "app-docker", ServerID: "srv-docker", Status: "failed", LastError: "docker app instance failed"}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != len(cases) {
		t.Fatalf("expected installed service runtime alerts, got %+v", alerts)
	}
	seen := map[string]store.Alert{}
	for _, alert := range alerts {
		seen[alert.ResourceID] = alert
		if alert.Scope != "app.instance" {
			t.Fatalf("expected app.instance runtime alert, got %+v", alert)
		}
		if !strings.Contains(strings.ToLower(alert.Title), "service is unavailable") {
			t.Fatalf("expected service unavailable title, got %+v", alert)
		}
		if strings.Contains(strings.ToLower(alert.Title), "installation failed") {
			t.Fatalf("runtime alert must not use install failure wording: %+v", alert)
		}
	}
	for _, tc := range cases {
		alert, ok := seen[tc.id]
		if !ok {
			t.Fatalf("missing runtime alert for %s: %+v", tc.id, alerts)
		}
		if alert.App != tc.app || alert.InstanceID != tc.id || alert.ServerID != tc.serverID || alert.RequiredPermission != tc.permission {
			t.Fatalf("unexpected alert metadata for %s: %+v", tc.id, alert)
		}
		if !strings.Contains(alert.Message, "health probe failed") {
			t.Fatalf("expected snapshot error in message, got %+v", alert)
		}
	}
	if _, ok := seen["app-docker"]; ok {
		t.Fatalf("docker app.instance snapshot must not duplicate docker.summary alert: %+v", alerts)
	}
}

func TestManagerIgnoresInstalledRuntimeFailuresAndKeepsInstallFailureAlerts(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "mysql-runtime-failed",
		App:      "mysql",
		Version:  "8.0",
		ServerID: "srv-1",
		Status:   "failed",
		Metadata: `{"installState":"installed","lastCheck":{"status":"failed","error":"ssh timeout"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "redis-runtime-unavailable",
		App:      "redis",
		Version:  "7.2",
		ServerID: "srv-2",
		Status:   "unavailable",
		Metadata: `{"installState":"installed","lastCheck":{"status":"unavailable","error":"redis unavailable"}}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "mysql-install-failed",
		App:      "mysql",
		Version:  "8.0",
		ServerID: "srv-3",
		Status:   "failed",
		Metadata: `{"installFailed":true,"error":"install command failed"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "docker-install-failed",
		App:      "docker",
		Version:  "24.0.9",
		ServerID: "srv-4",
		Status:   "install_failed",
		Metadata: `{"installState":"install_failed","message":"package missing"}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertAlert(store.Alert{
		Fingerprint: "app.instance:mysql-runtime-failed:failed",
		Severity:    "critical",
		Scope:       "app.instance",
		ResourceID:  "mysql-runtime-failed",
		InstanceID:  "mysql-runtime-failed",
		App:         "mysql",
		Status:      "open",
		Title:       "MYSQL instance is unavailable",
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
		t.Fatalf("expected only explicit install failure alerts, got %+v", alerts)
	}
	for _, alert := range alerts {
		if !strings.Contains(strings.ToLower(alert.Title), "installation failed") {
			t.Fatalf("expected install failure title, got %+v", alert)
		}
		if alert.ResourceID == "mysql-runtime-failed" || alert.ResourceID == "redis-runtime-unavailable" {
			t.Fatalf("runtime health failure must not create app.instance alert: %+v", alert)
		}
	}
	old, err := db.GetAlertByFingerprint("app.instance:mysql-runtime-failed:failed")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "resolved" {
		t.Fatalf("old runtime-derived app.instance alert status=%q, want resolved", old.Status)
	}
}

func TestManagerSuppressesAIFARRuntimeNoEndpointsWhenSnapshotShowsAllPodsReady(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	if _, err := db.SaveAppInstance(store.AppInstance{
		ID:       "app-aifar-ready",
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "running",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "aifar.runtime",
		ResourceID: "app-aifar-ready",
		ServerID:   "srv-1",
		Status:     "no-endpoints",
		Payload:    `{"readyPods":10,"desiredReplicas":10}`,
	}); err != nil {
		t.Fatal(err)
	}
	const fingerprint = "aifar.runtime:app-aifar-ready:no-endpoints"
	if _, _, err := db.UpsertAlert(store.Alert{
		Fingerprint: fingerprint,
		Severity:    "critical",
		Scope:       "aifar.runtime",
		ResourceID:  "app-aifar-ready",
		App:         "aifar",
		InstanceID:  "app-aifar-ready",
		Status:      "open",
		Title:       "AIFAR Runtime is unavailable",
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
	if len(open) != 0 {
		t.Fatalf("expected inconsistent no-endpoints alert to be suppressed, got %+v", open)
	}
	old, err := db.GetAlertByFingerprint(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "resolved" {
		t.Fatalf("old inconsistent runtime alert status=%q, want resolved", old.Status)
	}
}

func TestManagerIgnoresOrphanAIFARRuntimeSnapshotAndResolvesExistingAlert(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil)
	const resourceID = "app-orphan"
	const fingerprint = "aifar.runtime:" + resourceID + ":degraded"
	if _, _, err := db.UpsertStatusSnapshot(store.StatusSnapshot{
		Scope:      "aifar.runtime",
		ResourceID: resourceID,
		ServerID:   "srv-1",
		Status:     "degraded",
		Payload:    `{"readyPods":1,"desiredReplicas":3}`,
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.UpsertAlert(store.Alert{
		Fingerprint: fingerprint,
		Severity:    "warning",
		Scope:       "aifar.runtime",
		ResourceID:  resourceID,
		App:         "aifar",
		InstanceID:  resourceID,
		Status:      "open",
		Title:       "AIFAR Runtime is degraded",
	}); err != nil {
		t.Fatal(err)
	}

	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alert, err := db.GetAlertByFingerprint(fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	if alert.Status != "resolved" {
		t.Fatalf("orphan runtime alert status = %q, want resolved", alert.Status)
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
	if err := manager.Evaluate(context.Background()); err != nil {
		t.Fatal(err)
	}
	alerts, err := db.ListAlerts(store.AlertQuery{Status: "open"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 2 {
		t.Fatalf("expected two critical alerts, got %+v", alerts)
	}
	for _, alert := range alerts {
		if alert.Severity != "critical" {
			t.Fatalf("expected unavailable alert to be critical, got %+v", alert)
		}
		title := strings.ToLower(alert.Title)
		if !strings.Contains(title, "unavailable") || strings.Contains(title, "failed") {
			t.Fatalf("expected unavailable title without failed wording, got %+v", alert)
		}
	}
}
