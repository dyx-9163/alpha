package maintenance

import (
	"aifar-deployment/backend/internal/store"

	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeStore struct {
	auditCutoff         time.Time
	taskCutoff          time.Time
	statusHistoryCutoff time.Time
	alertEventCutoff    time.Time
}

func (fakeStore) BackupDatabase(path string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(path, []byte("backup"), 0o644); err != nil {
		return 0, "", err
	}
	return 6, strings.Repeat("a", 64), nil
}

func (s *fakeStore) DeleteAuditLogsBefore(cutoff time.Time) (int, error) {
	s.auditCutoff = cutoff
	return 0, nil
}

func (s *fakeStore) DeleteFinishedTasksBefore(cutoff time.Time) (int, error) {
	s.taskCutoff = cutoff
	return 0, nil
}

func (s *fakeStore) DeleteStatusSnapshotHistoryBefore(cutoff time.Time) (int, error) {
	s.statusHistoryCutoff = cutoff
	return 0, nil
}

func (s *fakeStore) DeleteAlertEventsBefore(cutoff time.Time) (int, error) {
	s.alertEventCutoff = cutoff
	return 0, nil
}

func TestPlanUsesOneLogRetentionCutoffForAllSQLiteLogHistory(t *testing.T) {
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	svc := NewService(&fakeStore{}, RetentionConfig{LogRetentionDays: 30, AuditRetentionDays: 180, TaskRetentionDays: 90})

	plan := svc.Plan(now)
	want := now.AddDate(0, 0, -30)
	for name, got := range map[string]time.Time{
		"audit":          plan.AuditCutoff,
		"tasks":          plan.TaskCutoff,
		"statusHistory":  plan.StatusHistoryCutoff,
		"alertEvents":    plan.AlertEventCutoff,
		"unifiedLogRule": plan.LogCutoff,
	} {
		if !got.Equal(want) {
			t.Fatalf("%s cutoff = %s, want unified cutoff %s", name, got, want)
		}
	}
}

func TestCleanupLogsUsesUnifiedRetentionForAllSQLiteLogHistory(t *testing.T) {
	now := time.Date(2026, 8, 12, 15, 0, 0, 0, time.UTC)
	fake := &fakeStore{}
	svc := NewService(fake, RetentionConfig{LogRetentionDays: 7})
	plan := svc.Plan(now)

	if _, err := svc.CleanupAudit(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CleanupTasks(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CleanupStatusHistory(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CleanupAlertEvents(plan); err != nil {
		t.Fatal(err)
	}

	want := now.AddDate(0, 0, -7)
	for name, got := range map[string]time.Time{
		"audit":         fake.auditCutoff,
		"tasks":         fake.taskCutoff,
		"statusHistory": fake.statusHistoryCutoff,
		"alertEvents":   fake.alertEventCutoff,
	} {
		if !got.Equal(want) {
			t.Fatalf("%s cleanup cutoff = %s, want %s", name, got, want)
		}
	}
}

func TestListAndDeleteDatabaseBackups(t *testing.T) {
	dir := t.TempDir()
	goodName := "aifar-control-plane-20260629-120000-1.db"
	if err := os.WriteFile(filepath.Join(dir, goodName), []byte("snapshot"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("ignore"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&fakeStore{}, RetentionConfig{})

	backups, err := svc.ListDatabaseBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].Name != goodName || backups[0].SHA256 == "" {
		t.Fatalf("unexpected backups: %+v", backups)
	}
	got, err := svc.GetDatabaseBackup(dir, goodName)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != goodName || got.Size <= 0 || got.Path == "" {
		t.Fatalf("unexpected backup detail: %+v", got)
	}

	deleted, names, err := svc.DeleteDatabaseBackups(dir, []string{goodName})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 || len(names) != 1 || names[0] != goodName {
		t.Fatalf("unexpected delete result deleted=%d names=%+v", deleted, names)
	}
	if _, err := os.Stat(filepath.Join(dir, goodName)); !os.IsNotExist(err) {
		t.Fatalf("expected backup to be deleted, got %v", err)
	}
}

func TestVerifyDatabaseBackup(t *testing.T) {
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.BootstrapUser("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "backups")
	name := "aifar-control-plane-20260629-120000-1.db"
	if _, _, err := db.BackupDatabase(filepath.Join(dir, name)); err != nil {
		t.Fatal(err)
	}
	svc := NewService(&fakeStore{}, RetentionConfig{})

	verification, err := svc.VerifyDatabaseBackup(dir, name)
	if err != nil {
		t.Fatal(err)
	}
	if !verification.OK || verification.IntegrityCheck != "ok" || len(verification.MissingTables) != 0 {
		t.Fatalf("expected backup verification to pass, got %+v", verification)
	}
	if len(verification.RequiredTables) == 0 || verification.Backup.Name != name {
		t.Fatalf("expected verification detail, got %+v", verification)
	}
}

func TestDeleteDatabaseBackupsRejectsUnsafeNames(t *testing.T) {
	svc := NewService(&fakeStore{}, RetentionConfig{})
	for _, name := range []string{"../aifar-control-plane-bad.db", "C:\\tmp\\aifar-control-plane-bad.db", "other.db"} {
		if _, _, err := svc.DeleteDatabaseBackups(t.TempDir(), []string{name}); err == nil {
			t.Fatalf("expected unsafe backup name %q to be rejected", name)
		}
	}
}
