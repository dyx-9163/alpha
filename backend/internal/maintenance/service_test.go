package maintenance

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeStore struct{}

func (fakeStore) BackupDatabase(path string) (int64, string, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return 0, "", err
	}
	if err := os.WriteFile(path, []byte("backup"), 0o644); err != nil {
		return 0, "", err
	}
	return 6, strings.Repeat("a", 64), nil
}

func (fakeStore) DeleteAuditLogsBefore(time.Time) (int, error) {
	return 0, nil
}

func (fakeStore) DeleteFinishedTasksBefore(time.Time) (int, error) {
	return 0, nil
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
	svc := NewService(fakeStore{}, RetentionConfig{})

	backups, err := svc.ListDatabaseBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 1 || backups[0].Name != goodName || backups[0].SHA256 == "" {
		t.Fatalf("unexpected backups: %+v", backups)
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

func TestDeleteDatabaseBackupsRejectsUnsafeNames(t *testing.T) {
	svc := NewService(fakeStore{}, RetentionConfig{})
	for _, name := range []string{"../aifar-control-plane-bad.db", "C:\\tmp\\aifar-control-plane-bad.db", "other.db"} {
		if _, _, err := svc.DeleteDatabaseBackups(t.TempDir(), []string{name}); err == nil {
			t.Fatalf("expected unsafe backup name %q to be rejected", name)
		}
	}
}
