package i18n

import "testing"

func TestMySQLBackupCatalogContainsEveryTaskAndStepMessageInBothLanguages(t *testing.T) {
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
	for _, language := range []string{"zh", "en"} {
		for _, key := range keys {
			if message := Text(language, key); message == "" || message == key {
				t.Fatalf("%s has no %s catalog message", key, language)
			}
		}
	}
}
