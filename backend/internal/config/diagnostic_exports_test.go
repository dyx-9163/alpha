package config

import (
	"path/filepath"
	"testing"
)

func TestLoadDiagnosticExportDefaultsFollowDatabaseDirectory(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "control", "aifar.db")
	t.Setenv("AIFAR_DATABASE_PATH", databasePath)
	t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_DIR", "")
	t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS", "")
	t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES", "")

	cfg := Load()
	wantDir := filepath.Join(filepath.Dir(databasePath), "diagnostic-exports")
	if cfg.DiagnosticExportDir != wantDir {
		t.Fatalf("DiagnosticExportDir = %q, want %q", cfg.DiagnosticExportDir, wantDir)
	}
	if cfg.DiagnosticExportRetentionHours != 24 {
		t.Fatalf("DiagnosticExportRetentionHours = %d, want 24", cfg.DiagnosticExportRetentionHours)
	}
	if cfg.DiagnosticExportQuotaBytes != int64(5*1024*1024*1024) {
		t.Fatalf("DiagnosticExportQuotaBytes = %d, want %d", cfg.DiagnosticExportQuotaBytes, int64(5*1024*1024*1024))
	}
}

func TestLoadDiagnosticExportOverrides(t *testing.T) {
	wantDir := filepath.Join(t.TempDir(), "exports")
	t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_DIR", wantDir)
	t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS", "12")
	t.Setenv("AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES", "1073741824")

	cfg := Load()
	if cfg.DiagnosticExportDir != wantDir {
		t.Fatalf("DiagnosticExportDir = %q, want %q", cfg.DiagnosticExportDir, wantDir)
	}
	if cfg.DiagnosticExportRetentionHours != 12 {
		t.Fatalf("DiagnosticExportRetentionHours = %d, want 12", cfg.DiagnosticExportRetentionHours)
	}
	if cfg.DiagnosticExportQuotaBytes != int64(1073741824) {
		t.Fatalf("DiagnosticExportQuotaBytes = %d, want 1073741824", cfg.DiagnosticExportQuotaBytes)
	}
}
