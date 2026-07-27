package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
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

func TestDiagnosticExportLegacyMigrationDefaultsStorageToRemote(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aifar.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`create table schema_migrations (
		version integer primary key,
		name text not null,
		applied_at datetime not null
	)`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	var legacyMigration *storeMigration
	for index := range storeMigrations {
		if storeMigrations[index].Version == 2026072701 {
			legacyMigration = &storeMigrations[index]
			break
		}
	}
	if legacyMigration == nil {
		raw.Close()
		t.Fatal("missing diagnostic export legacy migration 2026072701")
	}
	tx, err := raw.Begin()
	if err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := legacyMigration.Up(tx); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	if _, err := tx.Exec(`insert into schema_migrations(version,name,applied_at) values(?,?,?)`,
		legacyMigration.Version, legacyMigration.Name, time.Now().UTC()); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	if _, err := tx.Exec(`insert into diagnostic_exports(
		id,instance_id,server_id,status,since_at,until_at,remote_relative_path,created_at,expires_at
	) values(?,?,?,?,?,?,?,?,?)`,
		"diag-legacy", "instance-1", "server-1", "ready", now.Add(-time.Hour), now,
		"diag-legacy/archive.tar.gz", now, now.Add(24*time.Hour)); err != nil {
		tx.Rollback()
		raw.Close()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err := db.GetDiagnosticExport("diag-legacy")
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageKind != "remote" || got.StorageRelativePath != "" || got.ReservedBytes != 0 || got.RemoteRelativePath != "diag-legacy/archive.tar.gz" {
		t.Fatalf("legacy migration mismatch: %+v", got)
	}
}

func TestDiagnosticExportLocalStorageRoundTrip(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	saved, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-local", InstanceID: "instance-1", ServerID: "server-1", Status: "building",
		StorageKind: "local", StorageRelativePath: "diag-local/aifar-diagnostics-instance-1.tar.gz",
		SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetDiagnosticExport(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.StorageKind != "local" || got.StorageRelativePath != "diag-local/aifar-diagnostics-instance-1.tar.gz" || got.RemoteRelativePath != "" {
		t.Fatalf("local storage round trip mismatch: %+v", got)
	}
}

func TestSaveDiagnosticExportRejectsInvalidLocalStorageMetadata(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*DiagnosticExport)
	}{
		{name: "unsupported storage kind", mutate: func(v *DiagnosticExport) { v.StorageKind = "shared" }},
		{name: "negative archive bytes", mutate: func(v *DiagnosticExport) { v.ArchiveBytes = -1 }},
		{name: "negative uncompressed bytes", mutate: func(v *DiagnosticExport) { v.UncompressedBytes = -1 }},
		{name: "negative reserved bytes", mutate: func(v *DiagnosticExport) { v.ReservedBytes = -1 }},
		{name: "ready local path missing", mutate: func(v *DiagnosticExport) { v.Status = "ready" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := DiagnosticExport{
				ID:         "diag-invalid-" + strings.ReplaceAll(test.name, " ", "-"),
				InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local",
				SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
			}
			test.mutate(&value)
			if _, err := db.SaveDiagnosticExport(value); err == nil {
				t.Fatal("expected invalid local storage metadata to be rejected")
			}
		})
	}
}

func TestCommitLocalDiagnosticExportRejectsIncompleteMetadata(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*LocalDiagnosticExportCommit)
	}{
		{name: "storage path missing", mutate: func(v *LocalDiagnosticExportCommit) { v.StorageRelativePath = "" }},
		{name: "archive name missing", mutate: func(v *LocalDiagnosticExportCommit) { v.ArchiveName = "" }},
		{name: "sha missing", mutate: func(v *LocalDiagnosticExportCommit) { v.SHA256 = "" }},
		{name: "negative archive bytes", mutate: func(v *LocalDiagnosticExportCommit) { v.ArchiveBytes = -1 }},
		{name: "negative uncompressed bytes", mutate: func(v *LocalDiagnosticExportCommit) { v.UncompressedBytes = -1 }},
		{name: "negative warning count", mutate: func(v *LocalDiagnosticExportCommit) { v.WarningCount = -1 }},
		{name: "ready time missing", mutate: func(v *LocalDiagnosticExportCommit) { v.ReadyAt = time.Time{} }},
		{name: "expiry time missing", mutate: func(v *LocalDiagnosticExportCommit) { v.ExpiresAt = time.Time{} }},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			id := fmt.Sprintf("diag-invalid-commit-%d", index)
			if _, err := db.SaveDiagnosticExport(DiagnosticExport{
				ID: id, InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local",
				SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			value := LocalDiagnosticExportCommit{
				ID: id, StorageRelativePath: id + "/archive.tar.gz", ArchiveName: "archive.tar.gz",
				ArchiveBytes: 1024, UncompressedBytes: 4096, SHA256: strings.Repeat("a", 64),
				ReadyAt: now, ExpiresAt: now.Add(24 * time.Hour),
			}
			test.mutate(&value)
			if _, err := db.CommitLocalDiagnosticExport(value); err == nil {
				t.Fatal("expected incomplete local commit metadata to be rejected")
			}
		})
	}
}

func TestDiagnosticExportReservationAndLocalCommit(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	for _, id := range []string{"diag-1", "diag-2"} {
		if _, err := db.SaveDiagnosticExport(DiagnosticExport{
			ID: id, InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local",
			SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	usage, err := db.ReserveDiagnosticExportBytes("diag-1", 256<<20, 5<<30)
	if err != nil || usage.ReadyBytes != 0 || usage.ReservedBytes != int64(256<<20) || usage.QuotaBytes != int64(5<<30) {
		t.Fatalf("reserve: usage=%+v err=%v", usage, err)
	}
	if _, err := db.ReserveDiagnosticExportBytes("diag-2", 256<<20, 300<<20); !errors.Is(err, ErrDiagnosticExportQuotaExceeded) {
		t.Fatalf("reserve over quota error=%v", err)
	}

	ready, err := db.CommitLocalDiagnosticExport(LocalDiagnosticExportCommit{
		ID: "diag-1", StorageRelativePath: "diag-1/aifar-diagnostics-instance-1.tar.gz",
		ArchiveName: "aifar-diagnostics-instance-1.tar.gz", ArchiveBytes: 1024,
		UncompressedBytes: 4096, SHA256: strings.Repeat("a", 64),
		WarningCount: 2, Warnings: []string{"timestamp-unrecognized"},
		ReadyAt: now, ExpiresAt: now.Add(24 * time.Hour),
	})
	if err != nil || ready.ReservedBytes != 0 || ready.Status != "ready" {
		t.Fatalf("commit local export: ready=%+v err=%v", ready, err)
	}
	if ready.StorageRelativePath != "diag-1/aifar-diagnostics-instance-1.tar.gz" || ready.ArchiveBytes != 1024 || ready.WarningCount != 2 {
		t.Fatalf("local commit metadata mismatch: %+v", ready)
	}
}

func TestDiagnosticExportConcurrentReservationsCannotExceedQuota(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	for _, id := range []string{"diag-concurrent-1", "diag-concurrent-2"} {
		if _, err := db.SaveDiagnosticExport(DiagnosticExport{
			ID: id, InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local",
			SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"diag-concurrent-1", "diag-concurrent-2"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			<-start
			_, err := db.ReserveDiagnosticExportBytes(id, 256<<20, 300<<20)
			errs <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)

	succeeded := 0
	quotaRejected := 0
	for err := range errs {
		switch {
		case err == nil:
			succeeded++
		case errors.Is(err, ErrDiagnosticExportQuotaExceeded):
			quotaRejected++
		default:
			t.Fatalf("unexpected concurrent reservation error: %v", err)
		}
	}
	if succeeded != 1 || quotaRejected != 1 {
		t.Fatalf("concurrent reservation result: succeeded=%d quotaRejected=%d", succeeded, quotaRejected)
	}
}

func TestDiagnosticExportTerminalTransitionsReleaseReservations(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)

	if _, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-failed", InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local",
		SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ReserveDiagnosticExportBytes("diag-failed", 128<<20, 5<<30); err != nil {
		t.Fatal(err)
	}
	if updated, err := db.MarkDiagnosticExportFailed("diag-failed", "stream interrupted", now); err != nil || !updated {
		t.Fatalf("mark failed: updated=%v err=%v", updated, err)
	}
	failed, err := db.GetDiagnosticExport("diag-failed")
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != "failed" || failed.ReservedBytes != 0 || failed.ErrorText != "stream interrupted" {
		t.Fatalf("failed transition mismatch: %+v", failed)
	}

	if _, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-deleted", InstanceID: "instance-1", ServerID: "server-1", Status: "failed", StorageKind: "local",
		ReservedBytes: 64 << 20, SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now,
		ExpiresAt: now.Add(24 * time.Hour), CleanupStatus: "pending",
	}); err != nil {
		t.Fatal(err)
	}
	if updated, err := db.MarkDiagnosticExportDeleted("diag-deleted", now); err != nil || !updated {
		t.Fatalf("mark deleted: updated=%v err=%v", updated, err)
	}
	deleted, err := db.GetDiagnosticExport("diag-deleted")
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != "deleted" || deleted.ReservedBytes != 0 {
		t.Fatalf("deleted transition retained reservation: %+v", deleted)
	}
}

func TestReleaseDiagnosticExportReservationClearsOnlyLocalReservation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	if _, err := db.SaveDiagnosticExport(DiagnosticExport{
		ID: "diag-release", InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local",
		ReservedBytes: 64 << 20, SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	updated, err := db.ReleaseDiagnosticExportReservation("diag-release")
	if err != nil || !updated {
		t.Fatalf("release reservation: updated=%v err=%v", updated, err)
	}
	got, err := db.GetDiagnosticExport("diag-release")
	if err != nil {
		t.Fatal(err)
	}
	if got.ReservedBytes != 0 || got.Status != "building" {
		t.Fatalf("reservation release changed unexpected fields: %+v", got)
	}
	updated, err = db.ReleaseDiagnosticExportReservation("diag-release")
	if err != nil || updated {
		t.Fatalf("second reservation release should be a no-op: updated=%v err=%v", updated, err)
	}
}

func TestListDiagnosticExportsForReconcileReturnsOnlyLiveLocalRows(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	rows := []DiagnosticExport{
		{ID: "diag-local-building", InstanceID: "instance-1", ServerID: "server-1", Status: "building", StorageKind: "local", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "diag-local-failed", InstanceID: "instance-1", ServerID: "server-1", Status: "failed", StorageKind: "local", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(time.Minute), ExpiresAt: now.Add(time.Hour)},
		{ID: "diag-remote", InstanceID: "instance-1", ServerID: "server-1", Status: "ready", StorageKind: "remote", RemoteRelativePath: "diag-remote/archive.tar.gz", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(2 * time.Minute), ExpiresAt: now.Add(time.Hour)},
		{ID: "diag-local-deleted", InstanceID: "instance-1", ServerID: "server-1", Status: "deleted", StorageKind: "local", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(3 * time.Minute), ExpiresAt: now.Add(time.Hour), DeletedAt: now.Add(4 * time.Minute), CleanupStatus: "complete"},
	}
	for _, row := range rows {
		if _, err := db.SaveDiagnosticExport(row); err != nil {
			t.Fatal(err)
		}
	}

	got, err := db.ListDiagnosticExportsForReconcile()
	if err != nil {
		t.Fatal(err)
	}
	gotIDs := make([]string, 0, len(got))
	for _, row := range got {
		gotIDs = append(gotIDs, row.ID)
	}
	if strings.Join(gotIDs, ",") != "diag-local-building,diag-local-failed" {
		t.Fatalf("unexpected reconcile rows: %v", gotIDs)
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
	if len(due) != 2 || due[0].ID != "expired" || due[1].ID != "failed" {
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

func TestListDiagnosticExportsDueForCleanupIncludesIncompleteTerminalAndOrphanRecords(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)

	for _, task := range []Task{
		{ID: "task-terminal", Type: "diagnostic", Target: "i1", Status: "failed"},
		{ID: "task-pending", Type: "diagnostic", Target: "i1", Status: "pending"},
		{ID: "task-running", Type: "diagnostic", Target: "i1", Status: "running"},
	} {
		if _, err := db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}

	rows := []DiagnosticExport{
		{ID: "failed-retry", TaskID: "task-terminal", InstanceID: "i1", ServerID: "s1", Status: "failed", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(-5 * time.Minute), ExpiresAt: now.Add(time.Hour), CleanupStatus: "failed"},
		{ID: "cancelled-retry", InstanceID: "i1", ServerID: "s1", Status: "cancelled", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(-4 * time.Minute), ExpiresAt: now.Add(time.Hour), CleanupStatus: "none"},
		{ID: "pending-terminal-task", TaskID: "task-terminal", InstanceID: "i1", ServerID: "s1", Status: "pending", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(-3 * time.Minute), ExpiresAt: now.Add(time.Hour), CleanupStatus: "none"},
		{ID: "building-missing-task", TaskID: "task-missing", InstanceID: "i1", ServerID: "s1", Status: "building", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(-2 * time.Minute), ExpiresAt: now.Add(time.Hour), CleanupStatus: "none"},
		{ID: "pending-active-task", TaskID: "task-pending", InstanceID: "i1", ServerID: "s1", Status: "pending", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), CleanupStatus: "none"},
		{ID: "failed-active-task", TaskID: "task-running", InstanceID: "i1", ServerID: "s1", Status: "failed", SinceAt: now.Add(-time.Hour), UntilAt: now, CreatedAt: now, ExpiresAt: now.Add(time.Hour), CleanupStatus: "failed"},
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
	gotIDs := make([]string, 0, len(due))
	for _, row := range due {
		gotIDs = append(gotIDs, row.ID)
	}
	wantIDs := []string{"building-missing-task", "cancelled-retry", "failed-retry", "pending-terminal-task"}
	sort.Strings(gotIDs)
	if strings.Join(gotIDs, ",") != strings.Join(wantIDs, ",") {
		t.Fatalf("unexpected due records: got=%v want=%v", gotIDs, wantIDs)
	}
}

func TestDiagnosticExportCleanupPendingTransitionsOnlyOrphanBuildsToFailed(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 7, 27, 8, 0, 0, 0, time.UTC)
	if _, err := db.CreateTask(Task{ID: "task-active", Type: "diagnostic", Target: "i1", Status: "running"}); err != nil {
		t.Fatal(err)
	}
	for _, row := range []DiagnosticExport{
		{ID: "orphan-building", TaskID: "task-missing", InstanceID: "i1", ServerID: "s1", Status: "building", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour)},
		{ID: "active-building", TaskID: "task-active", InstanceID: "i1", ServerID: "s1", Status: "building", SinceAt: now.Add(-time.Hour), UntilAt: now, ExpiresAt: now.Add(time.Hour)},
	} {
		if _, err := db.SaveDiagnosticExport(row); err != nil {
			t.Fatal(err)
		}
	}

	updated, err := db.MarkDiagnosticExportCleanupPending("orphan-building", now)
	if err != nil || !updated {
		t.Fatalf("orphan building export did not enter cleanup: updated=%v err=%v", updated, err)
	}
	orphan, err := db.GetDiagnosticExport("orphan-building")
	if err != nil {
		t.Fatal(err)
	}
	if orphan.Status != "failed" || orphan.CleanupStatus != "pending" || !orphan.CleanupAttemptedAt.Equal(now) {
		t.Fatalf("unexpected orphan transition: %+v", orphan)
	}

	updated, err = db.MarkDiagnosticExportCleanupPending("active-building", now)
	if err != nil || updated {
		t.Fatalf("active building export entered cleanup: updated=%v err=%v", updated, err)
	}
}
