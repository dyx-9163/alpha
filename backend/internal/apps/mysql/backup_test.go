package mysql

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/store"
)

var standaloneBackupSteps = []string{
	"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql",
	"check-backup-space", "prepare-workdir", "dry-run-dump", "dump-instance",
	"build-manifest", "package-backup", "transfer-backup", "verify-checksum",
	"record-backup", "apply-retention", "cleanup-workdir",
}

func TestBackupStandalonePlanUsesExactDesignSequenceAndClampsParameters(t *testing.T) {
	// Production break caught: skipping/reordering a safety phase or forwarding unbounded request numbers would make the worker plan lie about execution.
	module, _, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	request.Parameters = map[string]any{"threads": 999, "maxRateMBps": 99999, "name": "nightly"}
	plan, err := module.PlanBackup(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(plan))
	for index, step := range plan {
		got[index] = step.Name
		if step.Target != request.Instance.ServerID || step.Order != index+1 {
			t.Fatalf("step %d has target/order %+v", index, step)
		}
	}
	if !reflect.DeepEqual(got, standaloneBackupSteps) {
		t.Fatalf("backup plan = %v, want %v", got, standaloneBackupSteps)
	}
	if request.Parameters["threads"] != 999 {
		t.Fatal("PlanBackup must not mutate the caller request")
	}
}

func TestBackupStandaloneCompletesOneDumpTransferCommitRetentionAndCleanup(t *testing.T) {
	// Production break caught: omitting any lifecycle side effect can report a successful backup without a committed, attributable archive.
	module, data, remote := newStandaloneBackupModule(t)
	recorder := &backupRecorder{}
	request := standaloneBackupRequest(t)
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorder.startedSteps, standaloneBackupSteps) {
		t.Fatalf("executed steps = %v, want %v", recorder.startedSteps, standaloneBackupSteps)
	}
	if remote.dryRunRuns != 1 || remote.dumpRuns != 1 || remote.downloads != 1 || remote.packageRuns != 1 || remote.cleanupRuns != 1 {
		t.Fatalf("remote lifecycle counts dry-run=%d dump=%d package=%d download=%d cleanup=%d", remote.dryRunRuns, remote.dumpRuns, remote.packageRuns, remote.downloads, remote.cleanupRuns)
	}
	if data.credentialLoads != 1 {
		t.Fatalf("bound admin credential loads = %d, want 1", data.credentialLoads)
	}
	if len(data.backups) != 1 {
		t.Fatalf("backup records = %+v", data.backups)
	}
	backup := data.backups[0]
	if backup.Status != "success" || backup.TaskID != "tsk_1234567890abcdef12345678" || backup.Checksum != remote.archiveSHA || backup.Size != int64(len(remote.archive)) {
		t.Fatalf("completed backup = %+v", backup)
	}
	var metadata struct {
		ManifestVersion int    `json:"manifestVersion"`
		Topology        string `json:"topology"`
		MySQLVersion    string `json:"mysqlVersion"`
	}
	if err := json.Unmarshal([]byte(backup.Metadata), &metadata); err != nil || metadata.ManifestVersion != 2 || metadata.Topology != "standalone" || metadata.MySQLVersion != "8.0.36" {
		t.Fatalf("successful backup metadata must mirror the verified manifest: metadata=%s parsed=%+v err=%v", backup.Metadata, metadata, err)
	}
	for _, name := range []string{"dump.tar", "backup-manifest.json", "checksums.txt"} {
		if info, err := os.Stat(filepath.Join(filepath.Dir(backup.Path), name)); err != nil || !info.Mode().IsRegular() {
			t.Fatalf("committed %s missing or unsafe: info=%v err=%v", name, info, err)
		}
	}
	if strings.Contains(strings.Join(recorder.messages, "\n"), "top-secret") || strings.Contains(strings.Join(remote.commands, "\n"), "top-secret") {
		t.Fatal("decrypted MySQL password leaked to a command or task log")
	}
	if !remote.secretUploaded || remote.scriptUploads != 1 {
		t.Fatalf("secret/script upload evidence missing: secret=%v scripts=%d", remote.secretUploaded, remote.scriptUploads)
	}
	if data.listCalls == 0 {
		t.Fatal("retention did not inspect successful backups")
	}
}

func TestBackupStandaloneRetentionCountsTheNewCommittedBackup(t *testing.T) {
	// Production break caught: treating the current committed archive as still running makes keepLast=1 retain an older success too.
	module, data, _ := newStandaloneBackupModule(t)
	old := store.AppBackup{
		ID: "backup_aaaaaaaaaaaaaaaaaaaaaaaa", App: "mysql", InstanceID: standaloneBackupRequest(t).Instance.ID,
		Status: "success", CreatedAt: time.Now().UTC().Add(-time.Hour),
	}
	data.backups = append(data.backups, old)
	recorder := &backupRecorder{}
	request := standaloneBackupRequest(t)
	request.KeepLast = 1
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(recorder.messages, "\n"), "retention selected 1 older backup") {
		t.Fatalf("retention log did not select the old success after counting the current backup: %v", recorder.messages)
	}
}

func TestBackupRetentionDeletesOwnedOldArchiveAfterNewRecordIsSuccessful(t *testing.T) {
	// Production break caught: retention that only selects rows, deletes before the new success, or forgets the deleted state leaves storage unbounded or removes the recovery point.
	module, data, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	repository, err := backuprepo.New(request.RepositoryDir)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repository.Prepare("backup_old_retention")
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("old archive")
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if err := repository.Commit(paths, []byte(`{"backupId":"backup_old_retention","app":"mysql"}`), digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	data.backups = append(data.backups, store.AppBackup{ID: "backup_old_retention", App: "mysql", InstanceID: request.Instance.ID, ServerID: request.Instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), CreatedAt: time.Now().UTC().Add(-time.Hour)})
	request.KeepLast = 1
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}}); err != nil {
		t.Fatal(err)
	}
	old, err := data.GetAppBackup("backup_old_retention")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "deleted" {
		t.Fatalf("old backup status=%q, want deleted", old.Status)
	}
	if _, err := os.Stat(paths.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old backup directory remains: %v", err)
	}
	if data.statusAtDelete != "success" {
		t.Fatalf("new backup status at old deletion=%q, want success", data.statusAtDelete)
	}
}

func TestBackupRejectsInstanceOwnershipDriftBeforeRemoteWork(t *testing.T) {
	// Production break caught: the request is the pre-lock snapshot. A changed
	// authoritative owner must fail closed before either dump or retention work.
	tests := []struct {
		name, sensitive string
		mutate          func(*store.AppInstance)
	}{
		{name: "app changed", sensitive: "redis-private-detail", mutate: func(instance *store.AppInstance) { instance.App = "redis-private-detail" }},
		{name: "topology changed", sensitive: "cluster-private-detail", mutate: func(instance *store.AppInstance) { instance.Topology = "cluster-private-detail" }},
		{name: "server changed", sensitive: "server-private-detail", mutate: func(instance *store.AppInstance) { instance.ServerID = "server-private-detail" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, data, _ := newStandaloneBackupModule(t)
			request := standaloneBackupRequest(t)
			repository, err := backuprepo.New(request.RepositoryDir)
			if err != nil {
				t.Fatal(err)
			}
			paths, err := repository.Prepare("backup_stale_owner")
			if err != nil {
				t.Fatal(err)
			}
			archive := []byte("stale owner archive")
			if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(archive))
			if err := repository.Commit(paths, []byte(`{"backupId":"backup_stale_owner","app":"mysql"}`), digest, int64(len(archive))); err != nil {
				t.Fatal(err)
			}
			data.backups = append(data.backups, store.AppBackup{ID: "backup_stale_owner", App: "mysql", InstanceID: request.Instance.ID, ServerID: request.Instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), CreatedAt: time.Now().UTC().Add(-time.Hour)})
			// Simulate the instance changing after HTTP preflight captured request.Instance and before the worker acquired the mutation lock.
			test.mutate(&data.instance)
			request.KeepLast = 1
			recorder := &backupRecorder{}
			err = module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder})
			var operation *MySQLOperationError
			if !errors.As(err, &operation) || operation.Code != MySQLReconciliationRequired {
				t.Fatalf("ownership drift error=%v code=%v", err, operation)
			}
			old, err := data.GetAppBackup("backup_stale_owner")
			if err != nil || old.Status != "success" {
				t.Fatalf("old backup=%+v err=%v", old, err)
			}
			if _, err := repository.Verify(old); err != nil {
				t.Fatalf("old archive changed: %v", err)
			}
			if newest := data.newestBackupExcept(old.ID); newest.ID != "" {
				t.Fatalf("ownership drift created a backup: %+v", newest)
			}
			warning := strings.Join(recorder.messages, "\n")
			if warning != "" || strings.Contains(warning, test.sensitive) {
				t.Fatalf("ownership drift reached remote/log lifecycle: %q", warning)
			}
		})
	}
}

func TestBackupRetentionFailureWarnsWithoutFailingNewSuccessfulBackup(t *testing.T) {
	// Production break caught: an old corrupt/missing candidate must not roll back a newly verified archive.
	module, data, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	data.backups = append(data.backups, store.AppBackup{ID: "backup_missing_old", App: "mysql", InstanceID: request.Instance.ID, ServerID: request.Instance.ServerID, BackupType: "logical-full", Status: "success", Path: filepath.Join(request.RepositoryDir, "backup_missing_old", "dump.tar"), Checksum: strings.Repeat("a", 64), Size: 10, CreatedAt: time.Now().UTC().Add(-time.Hour)})
	request.KeepLast = 1
	recorder := &backupRecorder{}
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}); err != nil {
		t.Fatal(err)
	}
	old, err := data.GetAppBackup("backup_missing_old")
	if err != nil {
		t.Fatal(err)
	}
	if old.Status != "success" {
		t.Fatalf("failed cleanup changed old record: %+v", old)
	}
	if !strings.Contains(strings.ToLower(strings.Join(recorder.messages, "\n")), "retention") {
		t.Fatalf("missing sanitized retention warning: %v", recorder.messages)
	}
	newest := data.newestBackupExcept("backup_missing_old")
	if newest.Status != "success" {
		t.Fatalf("new backup not successful: %+v", newest)
	}
}

func TestBackupRetentionRollsBackQuarantineWhenDeletedRecordCannotBeMarked(t *testing.T) {
	// Production break caught: retention must restore an old verified archive if its database transition fails after quarantine.
	module, data, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	repository, err := backuprepo.New(request.RepositoryDir)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repository.Prepare("backup_retention_rollback")
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("old rollback archive")
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if err := repository.Commit(paths, []byte(`{"backupId":"backup_retention_rollback","app":"mysql"}`), digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	data.backups = append(data.backups, store.AppBackup{ID: "backup_retention_rollback", App: "mysql", InstanceID: request.Instance.ID, ServerID: request.Instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: `{}`, CreatedAt: time.Now().UTC().Add(-time.Hour)})
	data.markDeleteErr = errors.New("injected secret database detail")
	request.KeepLast = 1
	recorder := &backupRecorder{}
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}); err != nil {
		t.Fatal(err)
	}
	old, err := data.GetAppBackup("backup_retention_rollback")
	if err != nil || old.Status != "success" {
		t.Fatalf("old record=%+v err=%v", old, err)
	}
	if _, err := repository.Verify(old); err != nil {
		t.Fatalf("old archive was not restored: %v", err)
	}
	joined := strings.Join(recorder.messages, "\n")
	if !strings.Contains(joined, "MySQL backup retention cleanup failed") || strings.Contains(joined, "secret database detail") {
		t.Fatalf("retention warning=%q", joined)
	}
}

func TestBackupRetentionDoesNotDeleteForeignOrNonStandaloneRecord(t *testing.T) {
	// Production break caught: Task 7 retention must fail closed on a record not owned by the current standalone MySQL instance.
	module, data, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	repository, err := backuprepo.New(request.RepositoryDir)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repository.Prepare("backup_foreign_retention")
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("foreign archive")
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if err := repository.Commit(paths, []byte(`{"backupId":"backup_foreign_retention","app":"mysql"}`), digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	data.backups = append(data.backups, store.AppBackup{ID: "backup_foreign_retention", App: "redis", InstanceID: request.Instance.ID, ServerID: request.Instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: `{}`, CreatedAt: time.Now().UTC().Add(-time.Hour)})
	request.KeepLast = 1
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(paths.Directory); err != nil {
		t.Fatalf("foreign archive changed: %v", err)
	}
}

func TestBackupVerifyServiceValidatesRepositoryBeforeMySQLContactAndPreservesArchiveIdentity(t *testing.T) {
	// Production break caught: verification must use only the controlled repository, persist non-secret result metadata, and never inspect MySQL.
	module, data, remote := newStandaloneBackupModule(t)
	repositoryRoot := filepath.Join(t.TempDir(), "repository")
	repository, err := backuprepo.New(repositoryRoot)
	if err != nil {
		t.Fatal(err)
	}
	paths, err := repository.Prepare("backup_verify_service")
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("verify service archive")
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if err := repository.Commit(paths, []byte(`{"backupId":"backup_verify_service","app":"mysql"}`), digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	original := store.AppBackup{ID: "backup_verify_service", App: "mysql", InstanceID: standaloneBackupRequest(t).Instance.ID, ServerID: standaloneBackupRequest(t).Instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: `{"phase":"success","source":"primary"}`, CreatedAt: time.Now().UTC()}
	data.backups = append(data.backups, original)
	recorder := &backupRecorder{}
	if err := module.VerifyBackup(context.Background(), original.ID, repositoryRoot, "en", registry.RunContext{TaskID: "tsk_verify_1234567890abcdef", Log: recorder}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(recorder.startedSteps, []string{"load-backup", "verify-manifest", "verify-checksum", "record-verification"}) {
		t.Fatalf("verify steps=%v", recorder.startedSteps)
	}
	verified, err := data.GetAppBackup(original.ID)
	if err != nil {
		t.Fatal(err)
	}
	if verified.Path != original.Path || verified.Checksum != original.Checksum || verified.Size != original.Size || verified.Status != original.Status {
		t.Fatalf("archive identity changed: before=%+v after=%+v", original, verified)
	}
	if !strings.Contains(verified.Metadata, `"verificationResult":"success"`) || !strings.Contains(verified.Metadata, `"verifiedAt":`) || !strings.Contains(verified.Metadata, `"source":"primary"`) {
		t.Fatalf("verification metadata=%s", verified.Metadata)
	}
	if data.credentialLoads != 0 || len(remote.commands) != 0 {
		t.Fatalf("verification contacted MySQL: credentials=%d commands=%v", data.credentialLoads, remote.commands)
	}
}

func TestBackupVerifyServiceRejectsInvalidRepositoryBeforeMySQLContact(t *testing.T) {
	// Production break caught: a missing/escaped/tampered artifact must fail before credential resolution or remote execution.
	module, data, remote := newStandaloneBackupModule(t)
	record := store.AppBackup{ID: "backup_verify_missing", App: "mysql", InstanceID: standaloneBackupRequest(t).Instance.ID, ServerID: standaloneBackupRequest(t).Instance.ServerID, BackupType: "logical-full", Status: "success", Path: filepath.Join(t.TempDir(), "outside.tar"), Checksum: strings.Repeat("a", 64), Size: 1, Metadata: `{}`}
	data.backups = append(data.backups, record)
	err := module.VerifyBackup(context.Background(), record.ID, filepath.Join(t.TempDir(), "repository"), "en", registry.RunContext{TaskID: "tsk_verify_1234567890abcdef", Log: &backupRecorder{}})
	if err == nil {
		t.Fatal("VerifyBackup accepted an unmanaged backup")
	}
	if data.credentialLoads != 0 || len(remote.commands) != 0 {
		t.Fatalf("verification contacted MySQL: credentials=%d commands=%v", data.credentialLoads, remote.commands)
	}
	failed, getErr := data.GetAppBackup(record.ID)
	if getErr != nil {
		t.Fatal(getErr)
	}
	if !strings.Contains(failed.Metadata, `"verificationResult":"failed"`) {
		t.Fatalf("failed verification metadata=%s", failed.Metadata)
	}
}

func TestBackupVerifyServiceRejectsMalformedOrNonObjectMetadataWithoutWriting(t *testing.T) {
	// Production break caught: verification must not replace malformed metadata with a new object and silently erase provenance fields.
	for _, metadata := range []string{"{", "null", `{} {}`, `[]`} {
		t.Run(metadata, func(t *testing.T) {
			module, data, _ := newStandaloneBackupModule(t)
			root := filepath.Join(t.TempDir(), "repository")
			repository, err := backuprepo.New(root)
			if err != nil {
				t.Fatal(err)
			}
			id := store.NewID("backup")
			paths, err := repository.Prepare(id)
			if err != nil {
				t.Fatal(err)
			}
			archive := []byte("strict metadata")
			if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
				t.Fatal(err)
			}
			digest := fmt.Sprintf("%x", sha256.Sum256(archive))
			if err := repository.Commit(paths, []byte(`{"backupId":"`+id+`","app":"mysql"}`), digest, int64(len(archive))); err != nil {
				t.Fatal(err)
			}
			data.backups = []store.AppBackup{{ID: id, App: "mysql", InstanceID: data.instance.ID, ServerID: data.instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: metadata}}
			err = module.VerifyBackup(context.Background(), id, root, "en", registry.RunContext{TaskID: "tsk_verify_1234567890abcdef", Log: &backupRecorder{}})
			if err == nil {
				t.Fatalf("metadata %q was accepted", metadata)
			}
			if data.saveCalls != 0 || data.backups[0].Metadata != metadata {
				t.Fatalf("metadata %q was rewritten: calls=%d record=%+v", metadata, data.saveCalls, data.backups[0])
			}
		})
	}
}

func TestBackupVerifyServiceAcceptsMatchingClusterOwnershipWithoutMySQLContact(t *testing.T) {
	module, data, _ := newStandaloneBackupModule(t)
	data.instance.Topology = "innodb-cluster"
	data.instance.Metadata = `{"clusterId":"cluster_verify_1234567890abcdef","port":3306}`
	root := filepath.Join(t.TempDir(), "repository")
	repository, err := backuprepo.New(root)
	if err != nil {
		t.Fatal(err)
	}
	id := store.NewID("backup")
	paths, err := repository.Prepare(id)
	if err != nil {
		t.Fatal(err)
	}
	archive := []byte("cluster archive")
	if err := os.WriteFile(paths.PartialArchive, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive))
	if err := repository.Commit(paths, []byte(`{"backupId":"`+id+`","app":"mysql"}`), digest, int64(len(archive))); err != nil {
		t.Fatal(err)
	}
	data.backups = []store.AppBackup{{ID: id, App: "mysql", InstanceID: data.instance.ID, ServerID: data.instance.ServerID, BackupType: "logical-full", Status: "success", Path: paths.Archive, Checksum: digest, Size: int64(len(archive)), Metadata: `{"phase":"success","topology":"innodb-cluster","clusterId":"cluster_verify_1234567890abcdef"}`}}
	if err := module.VerifyBackup(context.Background(), id, root, "en", registry.RunContext{TaskID: "tsk_verify_1234567890abcdef", Log: &backupRecorder{}}); err != nil {
		t.Fatal(err)
	}
	if data.credentialLoads != 0 {
		t.Fatalf("cluster repository verification contacted MySQL %d time(s)", data.credentialLoads)
	}
}

func TestBackupVerifyServiceRejectsClusterIdentityMismatchBeforeRepositoryContact(t *testing.T) {
	module, data, _ := newStandaloneBackupModule(t)
	data.instance.Topology = "innodb-cluster"
	data.instance.Metadata = `{"clusterId":"cluster_current_1234567890abcdef"}`
	id := store.NewID("backup")
	data.backups = []store.AppBackup{{ID: id, App: "mysql", InstanceID: data.instance.ID, ServerID: data.instance.ServerID, BackupType: "logical-full", Status: "success", Path: filepath.Join(t.TempDir(), "outside.tar"), Checksum: strings.Repeat("a", 64), Size: 1, Metadata: `{"clusterId":"cluster_other_1234567890abcdef"}`}}
	err := module.VerifyBackup(context.Background(), id, filepath.Join(t.TempDir(), "missing-repository"), "en", registry.RunContext{TaskID: "tsk_verify_1234567890abcdef", Log: &backupRecorder{}})
	var operationErr *MySQLOperationError
	if !errors.As(err, &operationErr) || operationErr.Code != MySQLBackupClusterUnhealthy {
		t.Fatalf("error=%v, want %s", err, MySQLBackupClusterUnhealthy)
	}
	if data.saveCalls != 0 {
		t.Fatalf("mismatched cluster verification wrote %d records", data.saveCalls)
	}
}

func TestBackupStandaloneRollsBackCommittedArchiveWhenRecordOrRetentionFails(t *testing.T) {
	// Production break caught: a post-commit control-plane failure must not leave a published archive while the record is failed.
	tests := []struct {
		name   string
		mutate func(*backupFakeStore)
	}{
		{"record committed phase", func(data *backupFakeStore) { data.saveErrPhase = "committed" }},
		{"record success phase", func(data *backupFakeStore) { data.saveErrPhase = "success" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, data, _ := newStandaloneBackupModule(t)
			test.mutate(data)
			err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
			if err == nil {
				t.Fatal("expected post-commit failure")
			}
			if len(data.backups) != 1 || data.backups[0].Status != "failed" {
				t.Fatalf("failed backup record not retained: %+v", data.backups)
			}
			if _, statErr := os.Stat(data.backups[0].Path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("committed archive still exists: %v", statErr)
			}
		})
	}
}

func TestBackupStandaloneRetainsFailedRecordWithoutFinalArchive(t *testing.T) {
	// Production break caught: a failed safety phase must not publish an archive or erase the forensic backup record.
	tests := []struct {
		name   string
		mutate func(*backupFakeStore, *backupFakeRemote)
		code   string
	}{
		{"missing credential", func(s *backupFakeStore, _ *backupFakeRemote) { s.credentialErr = store.ErrBoundCredentialNotFound }, MySQLCredentialUnavailable},
		{"inactive credential", func(s *backupFakeStore, _ *backupFakeRemote) { s.credentialErr = store.ErrBoundCredentialNotFound }, MySQLCredentialUnavailable},
		{"ambiguous credential", func(s *backupFakeStore, _ *backupFakeRemote) { s.credentialErr = store.ErrBoundCredentialAmbiguous }, MySQLCredentialUnavailable},
		{"mysqlsh failure", func(_ *backupFakeStore, r *backupFakeRemote) { r.inspectErr = errors.New("mysqlsh failed") }, ""},
		{"system schema discovery", func(_ *backupFakeStore, r *backupFakeRemote) { r.inspectOutput += "__AIFAR_SCHEMA__\tmysql\n" }, MySQLRestoreManifestInvalid},
		{"insufficient source space", func(_ *backupFakeStore, r *backupFakeRemote) { r.sourceAvailable = 1 }, MySQLBackupSpaceInsufficient},
		{"transfer cancellation", func(_ *backupFakeStore, r *backupFakeRemote) { r.downloadErr = context.Canceled }, MySQLBackupTransferFailed},
		{"checksum mismatch", func(_ *backupFakeStore, r *backupFakeRemote) { r.downloadSHA = strings.Repeat("0", 64) }, MySQLBackupChecksumMismatch},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			module, data, remote := newStandaloneBackupModule(t)
			test.mutate(data, remote)
			err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
			if err == nil {
				t.Fatal("expected backup failure")
			}
			if test.code != "" {
				var operationErr *MySQLOperationError
				if !errors.As(err, &operationErr) || operationErr.Code != test.code {
					t.Fatalf("error = %v, want code %s", err, test.code)
				}
			}
			if len(data.backups) != 1 || data.backups[0].Status != "failed" {
				t.Fatalf("failed backup record not retained: %+v", data.backups)
			}
			if _, statErr := os.Stat(data.backups[0].Path); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("final archive exists after failure: %v", statErr)
			}
		})
	}
}

func TestBackupStandaloneCleanupFailureAfterPublishedSuccessFailsTaskWithoutRemovingRecoveryPoint(t *testing.T) {
	// Production break caught: a published recovery point remains usable, but a credential-bearing workdir cleanup failure must not publish a successful task.
	module, data, remote := newStandaloneBackupModule(t)
	remote.cleanupErr = errors.New("cleanup failed")
	recorder := &backupRecorder{}
	if err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder}); err == nil {
		t.Fatal("post-publication credential cleanup failure published a successful task")
	}
	if len(data.backups) != 1 || data.backups[0].Status != "success" {
		t.Fatalf("published recovery point was not preserved: %+v", data.backups)
	}
	if _, err := os.Stat(data.backups[0].Path); err != nil {
		t.Fatalf("published archive was removed: %v", err)
	}
	if !strings.Contains(strings.Join(recorder.messages, "\n"), BackupCopyFor("en").RemoteCleanupFailed) {
		t.Fatalf("missing sanitized cleanup warning: %v", recorder.messages)
	}
}

func TestBackupStandaloneDoesNotRepeatSuccessPersistenceAfterRetention(t *testing.T) {
	// Production break caught: a redundant second success write can fail after retention and trigger rollback of the only remaining archive.
	module, data, _ := newStandaloneBackupModule(t)
	data.failRepeatedSuccessSave = true
	if err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}}); err != nil {
		t.Fatalf("redundant post-retention persistence failed backup: %v", err)
	}
	if data.successSaveCalls != 1 || len(data.backups) != 1 || data.backups[0].Status != "success" {
		t.Fatalf("success persistence calls=%d backups=%+v", data.successSaveCalls, data.backups)
	}
	if _, err := os.Stat(data.backups[0].Path); err != nil {
		t.Fatalf("published archive was removed: %v", err)
	}
}

func TestBackupStandaloneRejectsRemoteWithoutFileDownloader(t *testing.T) {
	// Production break caught: a remote that cannot stream to the controlled panel partial must fail before publishing anything.
	data := newBackupFakeStore(t)
	module := NewModule(data, backupRemoteWithoutDownloader{})
	err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
	var operationErr *MySQLOperationError
	if !errors.As(err, &operationErr) || operationErr.Code != MySQLBackupTransferFailed {
		t.Fatalf("error = %v, want %s", err, MySQLBackupTransferFailed)
	}
	if len(data.backups) != 1 || data.backups[0].Status != "failed" {
		t.Fatalf("missing failed record: %+v", data.backups)
	}
}

func TestBackupStandaloneFailsWhenPanelRepositorySpaceIsInsufficient(t *testing.T) {
	// Production break caught: source capacity alone cannot protect the panel repository from an oversized transfer.
	module, data, _ := newStandaloneBackupModule(t)
	original := panelBackupAvailableBytes
	panelBackupAvailableBytes = func(string) (int64, error) { return 1, nil }
	t.Cleanup(func() { panelBackupAvailableBytes = original })
	err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}})
	var operationErr *MySQLOperationError
	if !errors.As(err, &operationErr) || operationErr.Code != MySQLBackupSpaceInsufficient {
		t.Fatalf("error = %v, want %s", err, MySQLBackupSpaceInsufficient)
	}
	if len(data.backups) != 1 || data.backups[0].Status != "failed" {
		t.Fatalf("missing failed record: %+v", data.backups)
	}
}

func TestPanelFilesystemAvailableBytesLinuxChecksNormalizedRepositoryRootItself(t *testing.T) {
	// Production break caught: asking df about the parent misses a repository root that is itself a dedicated mount or NAS.
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(root, ".", "child", "..")
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	original := panelDFOutput
	var got string
	panelDFOutput = func(target string) ([]byte, error) {
		got = target
		return []byte("Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/test 100 1 99 1% /repo\n"), nil
	}
	t.Cleanup(func() { panelDFOutput = original })
	available, err := panelFilesystemAvailableBytesLinux(input)
	if err != nil {
		t.Fatal(err)
	}
	if got != want || available != 99*1024 {
		t.Fatalf("df target/available = %q/%d, want %q/%d", got, available, want, 99*1024)
	}
}

func TestWriteMySQLSecretContextRemovesFileWhenCloseFails(t *testing.T) {
	// Production break caught: returning a path after Close failed can upload incomplete credentials and leave the local secret behind.
	root := t.TempDir()
	secretPath := filepath.Join(root, "secret.cnf")
	original := createMySQLSecretContextFile
	var injected *closeFailSecretContextFile
	createMySQLSecretContextFile = func() (mysqlSecretContextFile, error) {
		file, err := os.OpenFile(secretPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		injected = &closeFailSecretContextFile{File: file}
		return injected, nil
	}
	t.Cleanup(func() { createMySQLSecretContextFile = original })
	name, err := writeMySQLSecretContext(store.Credential{Username: "root", Secret: map[string]string{"password": "secret"}}, 3306)
	if err == nil || name != "" {
		t.Fatalf("write result name=%q err=%v", name, err)
	}
	if _, statErr := os.Lstat(secretPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("secret context survived Close failure: %v", statErr)
	}
	if injected == nil || injected.closeCalls != 2 {
		t.Fatalf("Close calls = %v, want failure followed by cleanup retry", injected)
	}
}

func TestWriteMySQLSecretContextRemovesFileWhenWriteFails(t *testing.T) {
	// Production break caught: an incomplete option file must never survive a local write failure or be returned for upload.
	secretPath := filepath.Join(t.TempDir(), "secret.cnf")
	original := createMySQLSecretContextFile
	createMySQLSecretContextFile = func() (mysqlSecretContextFile, error) {
		file, err := os.OpenFile(secretPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		return writeFailSecretContextFile{File: file}, nil
	}
	t.Cleanup(func() { createMySQLSecretContextFile = original })
	name, err := writeMySQLSecretContext(store.Credential{Username: "root", Secret: map[string]string{"password": "secret"}}, 3306)
	if err == nil || name != "" {
		t.Fatalf("write result name=%q err=%v", name, err)
	}
	if _, statErr := os.Lstat(secretPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("secret context survived write failure: %v", statErr)
	}
}

func TestWriteMySQLSecretContextRetriesCloseAndRemovesFileWhenWriteFails(t *testing.T) {
	// Production break caught: a write error followed by a transient Close error must not leave an open secret file behind.
	secretPath := filepath.Join(t.TempDir(), "secret.cnf")
	original := createMySQLSecretContextFile
	var injected *writeFailCloseFailSecretContextFile
	createMySQLSecretContextFile = func() (mysqlSecretContextFile, error) {
		file, err := os.OpenFile(secretPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		injected = &writeFailCloseFailSecretContextFile{File: file}
		return injected, nil
	}
	t.Cleanup(func() { createMySQLSecretContextFile = original })
	name, err := writeMySQLSecretContext(store.Credential{Username: "root", Secret: map[string]string{"password": "secret"}}, 3306)
	if err == nil || name != "" {
		t.Fatalf("write result name=%q err=%v", name, err)
	}
	if injected == nil || injected.closeCalls != 2 {
		t.Fatalf("Close calls = %v, want failure followed by one cleanup retry", injected)
	}
	if _, statErr := os.Lstat(secretPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("secret context survived write and initial Close failures: %v", statErr)
	}
}

func TestWriteMySQLSecretContextRetriesCloseAndRemovesFileWhenChmodFails(t *testing.T) {
	// Production break caught: a chmod error followed by a transient Close error must stop before writing and remove the secret file.
	secretPath := filepath.Join(t.TempDir(), "secret.cnf")
	original := createMySQLSecretContextFile
	var injected *chmodFailCloseFailSecretContextFile
	createMySQLSecretContextFile = func() (mysqlSecretContextFile, error) {
		file, err := os.OpenFile(secretPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return nil, err
		}
		injected = &chmodFailCloseFailSecretContextFile{File: file}
		return injected, nil
	}
	t.Cleanup(func() { createMySQLSecretContextFile = original })
	name, err := writeMySQLSecretContext(store.Credential{Username: "root", Secret: map[string]string{"password": "secret"}}, 3306)
	if err == nil || name != "" {
		t.Fatalf("write result name=%q err=%v", name, err)
	}
	if injected == nil || injected.writeCalls != 0 {
		t.Fatalf("Write calls = %v, want chmod failure before secret contents are written", injected)
	}
	if injected.closeCalls != 2 {
		t.Fatalf("Close calls = %d, want failure followed by one cleanup retry", injected.closeCalls)
	}
	if _, statErr := os.Lstat(secretPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("secret context survived chmod and initial Close failures: %v", statErr)
	}
}

func TestBackupStandaloneJoinsSanitizedRemoteCleanupFailureWithPrimaryError(t *testing.T) {
	// Production break caught: a cleanup failure after the primary error must remain visible without leaking remote stderr or secrets.
	module, data, remote := newStandaloneBackupModule(t)
	remote.inspectErr = errors.New("mysqlsh inspection failed")
	remote.cleanupErr = errors.New("raw stderr contains top-secret")
	recorder := &backupRecorder{}
	err := module.Backup(context.Background(), standaloneBackupRequest(t), registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder})
	if err == nil || !strings.Contains(err.Error(), "mysqlsh inspection failed") || !strings.Contains(err.Error(), "MySQL remote backup cleanup failed") {
		t.Fatalf("joined error evidence missing: %v", err)
	}
	if strings.Contains(err.Error(), "top-secret") || strings.Contains(strings.Join(recorder.messages, "\n"), "top-secret") || strings.Contains(recorder.targetError, "top-secret") {
		t.Fatalf("cleanup stderr leaked: err=%v logs=%v target=%q", err, recorder.messages, recorder.targetError)
	}
	if len(data.backups) != 1 || data.backups[0].Status != "failed" {
		t.Fatalf("failed record missing: %+v", data.backups)
	}
}

func TestBackupStandaloneUsesLocalizedStepAndFailureMessages(t *testing.T) {
	// Production break caught: request language propagation must cover worker step messages, not only step titles.
	module, _, remote := newStandaloneBackupModule(t)
	remote.inspectErr = errors.New("inspection failed")
	recorder := &backupRecorder{}
	request := standaloneBackupRequest(t)
	request.Language = "zh-CN"
	_ = module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder})
	joined := strings.Join(recorder.messages, "\n")
	if !strings.Contains(joined, "MySQL 备份步骤") || !strings.Contains(joined, "失败") || strings.Contains(joined, "backup step") {
		t.Fatalf("worker messages were not localized: %v", recorder.messages)
	}
}

func TestBackupStandaloneLocalizesStableOperationErrorInTaskEvidence(t *testing.T) {
	// Production break caught: task logs and target evidence must show localized operator text while the returned error keeps its stable code.
	module, data, _ := newStandaloneBackupModule(t)
	data.credentialErr = store.ErrBoundCredentialNotFound
	recorder := &backupRecorder{}
	request := standaloneBackupRequest(t)
	request.Language = "zh-CN"
	err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: recorder})
	var operationErr *MySQLOperationError
	if !errors.As(err, &operationErr) || operationErr.Code != MySQLCredentialUnavailable {
		t.Fatalf("returned error = %v", err)
	}
	joined := strings.Join(recorder.messages, "\n") + "\n" + recorder.targetError
	if !strings.Contains(joined, "MySQL 管理员凭据不可用") || strings.Contains(joined, MySQLCredentialUnavailable) {
		t.Fatalf("task evidence was not localized: %s", joined)
	}
}

type closeFailSecretContextFile struct {
	*os.File
	closeCalls int
}

func (f *closeFailSecretContextFile) Close() error {
	f.closeCalls++
	if f.closeCalls == 1 {
		return errors.New("injected close failure")
	}
	return f.File.Close()
}

var _ io.Writer = (*closeFailSecretContextFile)(nil)

type writeFailSecretContextFile struct{ *os.File }

func (writeFailSecretContextFile) Write([]byte) (int, error) {
	return 0, errors.New("injected write failure")
}

type writeFailCloseFailSecretContextFile struct {
	*os.File
	closeCalls int
}

func (f *writeFailCloseFailSecretContextFile) Write([]byte) (int, error) {
	return 0, errors.New("injected write failure")
}

func (f *writeFailCloseFailSecretContextFile) Close() error {
	f.closeCalls++
	if f.closeCalls == 1 {
		return errors.New("injected close failure")
	}
	return f.File.Close()
}

type chmodFailCloseFailSecretContextFile struct {
	*os.File
	closeCalls int
	writeCalls int
}

func (f *chmodFailCloseFailSecretContextFile) Chmod(os.FileMode) error {
	return errors.New("injected chmod failure")
}

func (f *chmodFailCloseFailSecretContextFile) Write(contents []byte) (int, error) {
	f.writeCalls++
	return f.File.Write(contents)
}

func (f *chmodFailCloseFailSecretContextFile) Close() error {
	f.closeCalls++
	if f.closeCalls == 1 {
		return errors.New("injected close failure")
	}
	return f.File.Close()
}

type backupFakeStore struct {
	server                  store.Server
	instance                store.AppInstance
	credential              store.Credential
	credentialErr           error
	credentialLoads         int
	backups                 []store.AppBackup
	listCalls               int
	listErr                 error
	saveErrPhase            string
	saveErrUsed             bool
	statusAtDelete          string
	successSaveCalls        int
	saveCalls               int
	failRepeatedSuccessSave bool
	markDeleteErr           error
}

func newBackupFakeStore(t *testing.T) *backupFakeStore {
	t.Helper()
	return &backupFakeStore{
		server:     store.Server{ID: "srv_1234567890abcdef12345678", Name: "mysql", Host: "10.0.0.8", Username: "root"},
		instance:   store.AppInstance{ID: "app_1234567890abcdef12345678", App: "mysql", ServerID: "srv_1234567890abcdef12345678", Version: "8.0.36", Status: "installed", Topology: "standalone", Metadata: `{"port":3306,"rootUser":"root","endpoint":"10.0.0.8:3306"}`},
		credential: store.Credential{ID: "cred_1234567890abcdef12345678", Kind: "mysql", Username: "root", Status: "active", Purpose: "admin", Secret: map[string]string{"password": "top-secret"}},
	}
}

func (s *backupFakeStore) GetServer(id string, includeSecret bool) (store.Server, error) {
	return s.server, nil
}
func (s *backupFakeStore) GetAppInstance(id string) (store.AppInstance, error) {
	if s.instance.ID != id {
		return store.AppInstance{}, errors.New("instance not found")
	}
	return s.instance, nil
}
func (s *backupFakeStore) SaveAppInstance(v store.AppInstance) (store.AppInstance, error) {
	return v, nil
}
func (s *backupFakeStore) ListAppInstances() ([]store.AppInstance, error) { return nil, nil }
func (s *backupFakeStore) DeleteAppInstance(string) error                 { return nil }
func (s *backupFakeStore) GetBoundCredential(instanceID, purpose string, includeSecret bool) (store.Credential, error) {
	s.credentialLoads++
	if s.credentialErr != nil {
		return store.Credential{}, s.credentialErr
	}
	return s.credential, nil
}
func (s *backupFakeStore) SaveAppBackup(value store.AppBackup) (store.AppBackup, error) {
	s.saveCalls++
	if value.Status == "success" {
		s.successSaveCalls++
		if s.failRepeatedSuccessSave && s.successSaveCalls > 1 {
			return store.AppBackup{}, errors.New("repeated success save failed")
		}
	}
	if !s.saveErrUsed && s.saveErrPhase != "" && strings.Contains(value.Metadata, `"phase":"`+s.saveErrPhase+`"`) {
		s.saveErrUsed = true
		return store.AppBackup{}, errors.New("save backup failed")
	}
	for index := range s.backups {
		if s.backups[index].ID == value.ID {
			s.backups[index] = value
			return value, nil
		}
	}
	s.backups = append(s.backups, value)
	return value, nil
}
func (s *backupFakeStore) ListAppBackupsForInstances(ids []string, includeDeleted bool) ([]store.AppBackup, error) {
	s.listCalls++
	if s.listErr != nil {
		return nil, s.listErr
	}
	return append([]store.AppBackup(nil), s.backups...), nil
}
func (s *backupFakeStore) GetAppBackup(id string) (store.AppBackup, error) {
	for _, backup := range s.backups {
		if backup.ID == id {
			return backup, nil
		}
	}
	return store.AppBackup{}, errors.New("backup not found")
}
func (s *backupFakeStore) MarkAppBackupDeleted(id string, completedAt time.Time) (store.AppBackup, error) {
	if s.markDeleteErr != nil {
		return store.AppBackup{}, s.markDeleteErr
	}
	for index := range s.backups {
		if s.backups[index].ID != id {
			continue
		}
		s.statusAtDelete = s.newestBackupExcept(id).Status
		s.backups[index].Status = "deleted"
		s.backups[index].CompletedAt = completedAt
		return s.backups[index], nil
	}
	return store.AppBackup{}, errors.New("backup not found")
}
func (s *backupFakeStore) newestBackupExcept(id string) store.AppBackup {
	var newest store.AppBackup
	for _, backup := range s.backups {
		if backup.ID == id || backup.Status == "deleted" {
			continue
		}
		if newest.ID == "" || backup.CreatedAt.After(newest.CreatedAt) {
			newest = backup
		}
	}
	return newest
}

type backupFakeRemote struct {
	archive            []byte
	archiveSHA         string
	downloadSHA        string
	inspectOutput      string
	verificationOutput string
	sourceAvailable    int64
	inspectErr         error
	downloadErr        error
	cleanupErr         error
	commands           []string
	dumpRuns           int
	dryRunRuns         int
	packageRuns        int
	downloads          int
	cleanupRuns        int
	secretUploaded     bool
	scriptUploads      int
}

func newBackupFakeRemote() *backupFakeRemote {
	archive := backupArchiveWithDoneMarker()
	sum := fmt.Sprintf("%x", sha256.Sum256(archive))
	return &backupFakeRemote{
		archive: archive, archiveSHA: sum, downloadSHA: sum, sourceAvailable: 1 << 40,
		inspectOutput:      "__AIFAR_INFO__\t8.0.36\t123e4567-e89b-12d3-a456-426614174000\tuuid:1-9\t8.0.36\t1048576\n__AIFAR_SCHEMA__\taifar_business\n",
		verificationOutput: `__AIFAR_VERIFICATION__{"source":"mysql-shell-dump","inventoryAlgorithm":"sha256-nul-records-v1","inventorySha256":"75033abd15ea32598b2c7f68d7059c0f5f79992ec65529c4a057c57d27be33fc","files":[{"path":"@.done.json","size":2,"sha256":"44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"}],"schemaCount":1,"tableCount":1,"schemas":[{"name":"aifar_business","tableCount":1,"tables":[{"name":"orders"}]}]}`,
	}
}

func backupArchiveWithDoneMarker() []byte {
	var output bytes.Buffer
	writer := tar.NewWriter(&output)
	content := []byte("{}")
	_ = writer.WriteHeader(&tar.Header{Name: "dump/@.done.json", Mode: 0o600, Size: int64(len(content)), Typeflag: tar.TypeReg})
	_, _ = writer.Write(content)
	_ = writer.Close()
	return output.Bytes()
}

func (r *backupFakeRemote) Run(ctx context.Context, server store.Server, command string) (adapter.CommandResult, error) {
	r.commands = append(r.commands, command)
	switch {
	case strings.Contains(command, "__AIFAR_VERIFICATION__"):
		return adapter.CommandResult{Stdout: r.verificationOutput}, nil
	case strings.Contains(command, "__AIFAR_INFO__"):
		return adapter.CommandResult{Stdout: r.inspectOutput}, r.inspectErr
	case strings.Contains(command, "df -Pk"):
		return adapter.CommandResult{Stdout: fmt.Sprintf("%d\n", r.sourceAvailable)}, nil
	case strings.Contains(command, "dryRun: true"):
		r.dryRunRuns++
		return adapter.CommandResult{}, nil
	case strings.Contains(command, "logical-backup.sh"):
		r.dumpRuns++
		return adapter.CommandResult{}, nil
	case strings.Contains(command, "sha256sum"):
		r.packageRuns++
		return adapter.CommandResult{Stdout: fmt.Sprintf("__AIFAR_ARCHIVE__\t%d\t%s\n", len(r.archive), r.archiveSHA)}, nil
	case strings.Contains(command, "rm -rf"):
		r.cleanupRuns++
		return adapter.CommandResult{}, r.cleanupErr
	default:
		return adapter.CommandResult{}, nil
	}
}

func TestStandaloneBackupEmitsManifestV2FromCompletedDumpMetadata(t *testing.T) {
	module, data, _ := newStandaloneBackupModule(t)
	request := standaloneBackupRequest(t)
	if err := module.Backup(context.Background(), request, registry.RunContext{TaskID: "tsk_1234567890abcdef12345678", Log: &backupRecorder{}}); err != nil {
		t.Fatal(err)
	}
	if len(data.backups) != 1 {
		t.Fatalf("backups = %+v", data.backups)
	}
	manifestBytes, err := os.ReadFile(filepath.Join(filepath.Dir(data.backups[0].Path), "backup-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest BackupManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.ManifestVersion != 2 || manifest.Verification == nil || manifest.Verification.Source != "mysql-shell-dump" || manifest.Verification.Schemas[0].Tables[0].Name != "orders" {
		t.Fatalf("manifest v2 = %+v", manifest)
	}
	text := string(manifestBytes)
	for _, field := range []string{`"files":`, `"schemaCount":`, `"tableCount":`} {
		if !strings.Contains(text, field) {
			t.Fatalf("manifest missing canonical v2 field %s: %s", field, text)
		}
	}
	for _, legacy := range []string{`"inventory":`, `"rowsWritten":`, `"hasPrimaryKey":`, `"samplingAlgorithm":`, `"sampleLimitPerSchema":`, `"sampledTables":`, `"samples":`} {
		if strings.Contains(text, legacy) {
			t.Fatalf("manifest emitted non-contract field %s: %s", legacy, text)
		}
	}
}

func (r *backupFakeRemote) UploadFile(ctx context.Context, server store.Server, localPath, remotePath string, mode os.FileMode) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}
	if strings.HasSuffix(remotePath, "secret-context.cnf") {
		r.secretUploaded = mode.Perm() == 0o600 && strings.Contains(string(data), "top-secret")
	}
	if strings.HasSuffix(remotePath, "logical-backup.sh") {
		r.scriptUploads++
	}
	return nil
}

func (r *backupFakeRemote) DownloadFile(ctx context.Context, server store.Server, remotePath, localPath string, mode os.FileMode) (adapter.DownloadResult, error) {
	r.downloads++
	if r.downloadErr != nil {
		return adapter.DownloadResult{}, r.downloadErr
	}
	if err := os.WriteFile(localPath, r.archive, mode); err != nil {
		return adapter.DownloadResult{}, err
	}
	return adapter.DownloadResult{Size: int64(len(r.archive)), SHA256: r.downloadSHA}, nil
}

type backupRemoteWithoutDownloader struct{}

func (backupRemoteWithoutDownloader) Run(context.Context, store.Server, string) (adapter.CommandResult, error) {
	return adapter.CommandResult{}, nil
}
func (backupRemoteWithoutDownloader) UploadFile(context.Context, store.Server, string, string, os.FileMode) error {
	return nil
}

type backupRecorder struct {
	startedSteps, messages []string
	targetError            string
	targetStarts           []string
	targetFinishes         []string
	stepStatus             map[string]string
}

func (r *backupRecorder) Info(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}
func (r *backupRecorder) Error(format string, args ...any) {
	r.messages = append(r.messages, fmt.Sprintf(format, args...))
}
func (r *backupRecorder) StartTarget(target string) { r.targetStarts = append(r.targetStarts, target) }
func (r *backupRecorder) FinishTarget(_ string, status, errText string) {
	r.targetError = errText
	r.targetFinishes = append(r.targetFinishes, status)
}
func (r *backupRecorder) StartStep(_ string, name, _ string, _ int) {
	r.startedSteps = append(r.startedSteps, name)
}
func (r *backupRecorder) FinishStep(_ string, name, status, _ string) {
	if r.stepStatus == nil {
		r.stepStatus = map[string]string{}
	}
	r.stepStatus[name] = status
}

func newStandaloneBackupModule(t *testing.T) (Module, *backupFakeStore, *backupFakeRemote) {
	t.Helper()
	data := newBackupFakeStore(t)
	remote := newBackupFakeRemote()
	return NewModule(data, remote), data, remote
}

func standaloneBackupRequest(t *testing.T) registry.BackupRequest {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "mysql-backups")
	return registry.BackupRequest{
		Instance: store.AppInstance{ID: "app_1234567890abcdef12345678", App: "mysql", ServerID: "srv_1234567890abcdef12345678", Version: "8.0.36", Status: "installed", Topology: "standalone", Metadata: `{"port":3306,"rootUser":"root","endpoint":"10.0.0.8:3306"}`},
		Servers:  []store.Server{{ID: "srv_1234567890abcdef12345678", Host: "10.0.0.8"}},
		Language: "en", Actor: "operator", RepositoryDir: repository, KeepLast: 5,
		Parameters: map[string]any{"name": "nightly", "threads": 4, "maxRateMBps": 64},
	}
}

var _ registry.BackupModule = Module{}

// Keep time imported in this test fixture so timestamp assertions can be added without production-derived expectations.
var _ = time.Time{}
