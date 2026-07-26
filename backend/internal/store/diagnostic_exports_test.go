package store

import (
	"database/sql"
	"encoding/json"
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

func TestDiagnosticExportEmptyServicesRoundTripAsJSONArray(t *testing.T) {
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name     string
		services []string
	}{
		{name: "empty", services: []string{}},
		{name: "whitespace", services: []string{" ", "\t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()

			saved, err := db.SaveDiagnosticExport(DiagnosticExport{
				ID: "diag-empty-services", InstanceID: "i1", ServerID: "s1", Status: "pending",
				Services: tc.services, SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour),
			})
			if err != nil {
				t.Fatal(err)
			}
			if saved.Services == nil || saved.ServicesJSON != `[]` {
				t.Fatalf("expected saved empty services array, got %#v (%s)", saved.Services, saved.ServicesJSON)
			}

			got, err := db.GetDiagnosticExport(saved.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Services == nil || got.ServicesJSON != `[]` {
				t.Fatalf("expected loaded empty services array, got %#v (%s)", got.Services, got.ServicesJSON)
			}
			body, err := json.Marshal(got)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(body), `"services":[]`) {
				t.Fatalf("expected JSON array services field, got %s", body)
			}
		})
	}
}

func TestDiagnosticExportConditionalLifecycleUpdatesDoNotReviveTerminalRows(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	export, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-cas", InstanceID: "i1", ServerID: "s1", Status: "ready",
		SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour),
		CleanupStatus: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := db.MarkDiagnosticExportDownloaded(export.ID, now.Add(time.Minute))
	if err != nil || !updated {
		t.Fatalf("ready download timestamp was not recorded: updated=%v err=%v", updated, err)
	}
	export, err = db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	export.Status = "deleted"
	export.DeletedAt = now.Add(2 * time.Minute)
	export.CleanupStatus = "complete"
	if _, err := db.SaveDiagnosticExport(export); err != nil {
		t.Fatal(err)
	}
	updated, err = db.MarkDiagnosticExportDownloaded(export.ID, now.Add(3*time.Minute))
	if err != nil || updated {
		t.Fatalf("deleted row accepted stale download update: updated=%v err=%v", updated, err)
	}
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "deleted" || got.DeletedAt.IsZero() || !got.DownloadedAt.Equal(now.Add(time.Minute)) {
		t.Fatalf("conditional download revived terminal row: %+v", got)
	}
}

func TestDiagnosticExportConditionalCleanupTransitionsSupportRetry(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	_, err = db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-cleanup-retry", InstanceID: "i1", ServerID: "s1", Status: "failed",
		SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour),
		CleanupStatus: "failed", CleanupError: "first attempt failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated, err := db.MarkDiagnosticExportCleanupPending("diag-cleanup-retry", now); err != nil || !updated {
		t.Fatalf("failed cleanup did not enter retry pending: updated=%v err=%v", updated, err)
	}
	if updated, err := db.MarkDiagnosticExportCleanupFailed("diag-cleanup-retry", "second attempt failed"); err != nil || !updated {
		t.Fatalf("pending cleanup did not record retry failure: updated=%v err=%v", updated, err)
	}
	if updated, err := db.MarkDiagnosticExportCleanupPending("diag-cleanup-retry", now.Add(time.Minute)); err != nil || !updated {
		t.Fatalf("cleanup failure was not retryable: updated=%v err=%v", updated, err)
	}
	if updated, err := db.MarkDiagnosticExportDeleted("diag-cleanup-retry", now.Add(2*time.Minute)); err != nil || !updated {
		t.Fatalf("successful retry did not atomically delete: updated=%v err=%v", updated, err)
	}
	got, err := db.GetDiagnosticExport("diag-cleanup-retry")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "deleted" || got.CleanupStatus != "complete" || got.DeletedAt.IsZero() {
		t.Fatalf("unexpected cleanup retry result: %+v", got)
	}
}
