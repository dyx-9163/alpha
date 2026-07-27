package i18n

import (
	"strings"
	"testing"
)

func TestMySQLBackupCatalogContainsEveryTaskStepAndStableErrorMessageInBothLocales(t *testing.T) {
	// Production break caught: a missing catalog entry would expose its machine key in a task plan, task log, or handler response.
	keys := []string{
		"mysql.backup.stepStart", "mysql.backup.stepDone", "mysql.backup.stepFailed",
		"mysql.backup.taskStarted", "mysql.backup.taskCompleted", "mysql.backup.planFailed",
		"mysql.backup.planStoreFailed", "mysql.backup.remoteCleanupFailed", "mysql.backup.retentionSelected",
		"mysql.backup.step.load-instance", "mysql.backup.step.acquire-instance-lock",
		"mysql.backup.step.resolve-credential", "mysql.backup.step.inspect-mysql",
		"mysql.backup.step.check-backup-space", "mysql.backup.step.prepare-workdir",
		"mysql.backup.step.dry-run-dump", "mysql.backup.step.dump-instance",
		"mysql.backup.step.build-manifest", "mysql.backup.step.package-backup",
		"mysql.backup.step.transfer-backup", "mysql.backup.step.verify-checksum",
		"mysql.backup.step.record-backup", "mysql.backup.step.apply-retention",
		"mysql.backup.step.cleanup-workdir",
	}
	stableCodes := []string{
		"MYSQL_CREDENTIAL_UNAVAILABLE", "MYSQL_BACKUP_UNSUPPORTED_TOPOLOGY",
		"MYSQL_BACKUP_CLUSTER_UNHEALTHY", "MYSQL_BACKUP_PRIMARY_NOT_FOUND",
		"MYSQL_BACKUP_SPACE_INSUFFICIENT", "MYSQL_BACKUP_TRANSFER_FAILED",
		"MYSQL_BACKUP_CHECKSUM_MISMATCH", "MYSQL_RESTORE_MAINTENANCE_REQUIRED",
		"MYSQL_RESTORE_VERSION_INCOMPATIBLE", "MYSQL_RESTORE_MANIFEST_INVALID",
		"MYSQL_RESTORE_TARGET_NOT_CLEAN", "MYSQL_RESTORE_PRIMARY_CHANGED",
		"MYSQL_RESTORE_LOCAL_INFILE_RESTORE_FAILED", "MYSQL_RESTORE_INCOMPLETE",
		"MYSQL_REBUILD_CONFIRMATION_REQUIRED", "MYSQL_REBUILD_ROUTER_FAILED",
	}
	for _, locale := range []Locale{Zh, En} {
		catalog, ok := mysqlBackupCatalogs[locale]
		if !ok || catalog == nil {
			t.Fatalf("MySQL backup catalog is missing for locale %q", locale)
		}
		for _, key := range keys {
			if message, exists := catalog[key]; !exists || strings.TrimSpace(message) == "" {
				t.Fatalf("%s has no raw %s catalog message", key, locale)
			}
		}
		for _, code := range stableCodes {
			key, exists := mysqlBackupErrorMessageKeys[code]
			if !exists || strings.TrimSpace(key) == "" {
				t.Fatalf("stable code %s has no catalog key", code)
			}
			if message, exists := catalog[key]; !exists || strings.TrimSpace(message) == "" {
				t.Fatalf("stable code %s key %s has no raw %s catalog message", code, key, locale)
			}
		}
	}
}
