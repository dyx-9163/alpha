package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsMySQLBackupSettings(t *testing.T) {
	root := t.TempDir()
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(root); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	t.Setenv("AIFAR_MYSQL_BACKUP_DIR", "")
	t.Setenv("AIFAR_MYSQL_BACKUP_KEEP_LAST", "")
	t.Setenv("AIFAR_DATABASE_BACKUP_DIR", filepath.Join(root, "control-plane-backups"))

	got := Load()
	if got.MySQLBackupDir != filepath.Join(root, "data", "mysql-backups") {
		t.Fatalf("MySQLBackupDir = %q, want independent default", got.MySQLBackupDir)
	}
	if got.MySQLBackupKeepLast != 5 {
		t.Fatalf("MySQLBackupKeepLast = %d, want 5", got.MySQLBackupKeepLast)
	}
}

func TestLoadHonorsMySQLBackupSettings(t *testing.T) {
	t.Setenv("AIFAR_MYSQL_BACKUP_DIR", "/mnt/aifar-mysql-backups")
	t.Setenv("AIFAR_MYSQL_BACKUP_KEEP_LAST", "8")

	got := Load()
	if got.MySQLBackupDir != "/mnt/aifar-mysql-backups" {
		t.Fatalf("MySQLBackupDir = %q, want configured directory", got.MySQLBackupDir)
	}
	if got.MySQLBackupKeepLast != 8 {
		t.Fatalf("MySQLBackupKeepLast = %d, want 8", got.MySQLBackupKeepLast)
	}
}

func TestLoadFallsBackForInvalidMySQLBackupKeepLast(t *testing.T) {
	for _, value := range []string{"0", "-1", "invalid"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv("AIFAR_MYSQL_BACKUP_KEEP_LAST", value)
			if got := Load().MySQLBackupKeepLast; got != 5 {
				t.Fatalf("MySQLBackupKeepLast = %d, want 5 for %q", got, value)
			}
		})
	}
}

func TestLoadDefaultsUnifiedSQLiteLogRetention(t *testing.T) {
	t.Setenv("AIFAR_LOG_RETENTION_DAYS", "")
	t.Setenv("AIFAR_AUDIT_RETENTION_DAYS", "")
	t.Setenv("AIFAR_TASK_RETENTION_DAYS", "")

	got := Load()
	if got.LogRetentionDays != 90 {
		t.Fatalf("LogRetentionDays = %d, want unified default 90", got.LogRetentionDays)
	}
}

func TestLoadHonorsUnifiedSQLiteLogRetention(t *testing.T) {
	t.Setenv("AIFAR_LOG_RETENTION_DAYS", "45")
	t.Setenv("AIFAR_AUDIT_RETENTION_DAYS", "180")
	t.Setenv("AIFAR_TASK_RETENTION_DAYS", "90")

	got := Load()
	if got.LogRetentionDays != 45 {
		t.Fatalf("LogRetentionDays = %d, want configured unified retention", got.LogRetentionDays)
	}
}
