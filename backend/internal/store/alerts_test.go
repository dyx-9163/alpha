package store

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAlertLifecycleAndMasking(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, action, err := db.UpsertAlert(Alert{
		Fingerprint:        "docker.summary:srv-1:failed",
		Severity:           "warning",
		Scope:              "docker.summary",
		ResourceID:         "srv-1",
		ServerID:           "srv-1",
		Title:              "Docker failed",
		Message:            "password=docker-secret",
		EvidenceJSON:       `{"token":"evidence-secret","line":"boom"}`,
		RequiredPermission: "containers.manage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if action != "created" || first.ID == "" || first.Status != "open" {
		t.Fatalf("unexpected first alert action=%s alert=%+v", action, first)
	}
	if strings.Contains(first.Message+first.EvidenceJSON, "docker-secret") || strings.Contains(first.EvidenceJSON, "evidence-secret") {
		t.Fatalf("expected alert fields to be masked: %+v", first)
	}

	second, action, err := db.UpsertAlert(Alert{
		Fingerprint:        first.Fingerprint,
		Severity:           "warning",
		Scope:              "docker.summary",
		ResourceID:         "srv-1",
		ServerID:           "srv-1",
		Title:              "Docker failed",
		Message:            "password=docker-secret",
		EvidenceJSON:       `{"token":"evidence-secret","line":"boom"}`,
		RequiredPermission: "containers.manage",
	})
	if err != nil {
		t.Fatal(err)
	}
	if action != "" || second.ID != first.ID {
		t.Fatalf("expected duplicate upsert without visible update, action=%s second=%+v first=%+v", action, second, first)
	}

	acked, err := db.AcknowledgeAlert(first.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if acked.AcknowledgedBy != "operator" || acked.AcknowledgedAt.IsZero() {
		t.Fatalf("expected alert to be acknowledged: %+v", acked)
	}
	mutedUntil := time.Now().Add(time.Hour)
	muted, err := db.MuteAlert(first.ID, "operator", mutedUntil)
	if err != nil {
		t.Fatal(err)
	}
	if muted.MutedUntil.IsZero() {
		t.Fatalf("expected alert to be muted: %+v", muted)
	}
	resolved, err := db.ResolveAlert(first.ID, "owner", "fixed")
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Status != "resolved" || resolved.ResolvedAt.IsZero() {
		t.Fatalf("expected alert to be resolved: %+v", resolved)
	}

	alerts, err := db.ListAlerts(AlertQuery{Status: "all"})
	if err != nil {
		t.Fatal(err)
	}
	if len(alerts) != 1 || alerts[0].ID != first.ID {
		t.Fatalf("expected one alert, got %+v", alerts)
	}
	eventCount, err := db.CountRows("alert_events")
	if err != nil {
		t.Fatal(err)
	}
	if eventCount < 4 {
		t.Fatalf("expected lifecycle events, got %d", eventCount)
	}
}

func TestResolveMissingSystemAlertsKeepsTaskAlerts(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	resourceAlert, _, err := db.UpsertAlert(Alert{Fingerprint: "collector:docker.summary:failed", Severity: "warning", Scope: "collector", ResourceID: "docker.summary", Title: "collector failed"})
	if err != nil {
		t.Fatal(err)
	}
	taskAlert, _, err := db.UpsertAlert(Alert{Fingerprint: "task:tsk-1:failed", Severity: "warning", Scope: "task", ResourceID: "tsk-1", Title: "task failed"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := db.ResolveMissingSystemAlerts(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].ID != resourceAlert.ID {
		t.Fatalf("expected only resource alert resolved, got %+v", resolved)
	}
	gotResource, err := db.GetAlert(resourceAlert.ID)
	if err != nil {
		t.Fatal(err)
	}
	gotTask, err := db.GetAlert(taskAlert.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotResource.Status != "resolved" {
		t.Fatalf("expected resource alert resolved: %+v", gotResource)
	}
	if gotTask.Status != "open" {
		t.Fatalf("expected task alert to remain open: %+v", gotTask)
	}
}
