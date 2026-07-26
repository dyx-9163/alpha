package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDiagnosticExportLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	saved, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-1", TaskID: "task-1", InstanceID: "instance-1", ServerID: "server-1",
		Status: "pending", ServicesJSON: `["gateway","oauth"]`, SinceAt: now.Add(-2 * time.Hour),
		UntilAt: now, CreatedBy: "owner", CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		CleanupStatus: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	saved.Status = "ready"
	saved.ArchiveName = "aifar-diagnostics.tar.gz"
	saved.RemoteRelativePath = "diag-1/aifar-diagnostics.tar.gz"
	saved.ArchiveBytes = 1024
	saved.SHA256 = strings.Repeat("a", 64)
	saved.ReadyAt = now
	if _, err := db.SaveDiagnosticExport(saved); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetDiagnosticExport("diag-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || got.ArchiveBytes != 1024 || got.ReadyAt.IsZero() {
		t.Fatalf("unexpected export: %+v", got)
	}
}

func TestSaveDiagnosticExportNormalizesCollectionsAndRejectsUnknownStatuses(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	saved, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-normalized", InstanceID: "i1", ServerID: "s1", Status: "building",
		Services: []string{" oauth ", "gateway", "gateway", ""},
		Warnings: []string{" slow ", "slow", "truncated"},
		SinceAt:  now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ServicesJSON != `["gateway","oauth"]` || strings.Join(saved.Services, ",") != "gateway,oauth" {
		t.Fatalf("unexpected services: %+v", saved)
	}
	if saved.WarningsJSON != `["slow","truncated"]` || saved.WarningCount != 2 || strings.Join(saved.Warnings, ",") != "slow,truncated" {
		t.Fatalf("unexpected warnings: %+v", saved)
	}
	if saved.CleanupStatus != "none" {
		t.Fatalf("unexpected default cleanup status: %q", saved.CleanupStatus)
	}

	_, err = db.SaveDiagnosticExport(DiagnosticExport{ID: "bad-status", InstanceID: "i1", ServerID: "s1", Status: "unknown", SinceAt: now, UntilAt: now, ExpiresAt: now})
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected unknown status error, got %v", err)
	}
	_, err = db.SaveDiagnosticExport(DiagnosticExport{ID: "bad-cleanup", InstanceID: "i1", ServerID: "s1", Status: "ready", SinceAt: now, UntilAt: now, ExpiresAt: now, CleanupStatus: "unknown"})
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected unknown cleanup status error, got %v", err)
	}
}

func TestListDiagnosticExportsDueForCleanup(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	rows := []DiagnosticExport{
		{ID: "expired", InstanceID: "i1", ServerID: "s1", Status: "ready", SinceAt: now.Add(-3 * time.Hour), UntilAt: now.Add(-time.Hour), ExpiresAt: now.Add(-time.Second)},
		{ID: "future", InstanceID: "i1", ServerID: "s1", Status: "ready", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "failed", InstanceID: "i1", ServerID: "s1", Status: "failed", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(-time.Second)},
		{ID: "deleted", InstanceID: "i1", ServerID: "s1", Status: "deleted", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(-time.Second), CleanupStatus: "complete"},
	}
	for _, row := range rows {
		if _, err := db.SaveDiagnosticExport(row); err != nil {
			t.Fatal(err)
		}
	}
	due, err := db.ListDiagnosticExportsDueForCleanup(now, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 1 || due[0].ID != "expired" {
		t.Fatalf("unexpected due exports: %+v", due)
	}
}
