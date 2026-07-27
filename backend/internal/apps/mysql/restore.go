package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

var standaloneRestoreStepNames = []string{
	"load-backup", "acquire-instance-lock", "verify-maintenance-confirmation", "verify-manifest",
	"verify-checksum", "verify-version", "create-pre-restore-backup", "upload-backup", "extract-backup",
	"dry-run-load", "capture-local-infile", "enable-local-infile", "drop-target-schemas", "load-dump",
	"restore-local-infile", "verify-schemas", "verify-data", "record-restore", "cleanup-workdir", "release-lock",
}

func (m Module) PlanRestore(ctx context.Context, req registry.RestoreRequest) ([]registry.InstallStepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Instance.App != "mysql" || instanceTopology(req.Instance) != "standalone" ||
		strings.TrimSpace(req.Instance.ID) == "" || strings.TrimSpace(req.Instance.ServerID) == "" ||
		req.Backup.App != "mysql" || req.Backup.InstanceID != req.Instance.ID ||
		req.Backup.ServerID != req.Instance.ServerID || req.Backup.BackupType != "logical-full" ||
		req.Backup.Status != "success" || strings.TrimSpace(req.RepositoryDir) == "" {
		return nil, mysqlOperationError(MySQLBackupStandaloneRequired)
	}
	plan := make([]registry.InstallStepPlan, len(standaloneRestoreStepNames))
	for index, name := range standaloneRestoreStepNames {
		plan[index] = registry.InstallStepPlan{Target: req.Instance.ServerID, Name: name, Title: restoreStepTitle(req.Language, name), Order: index + 1}
	}
	return plan, nil
}

func (m Module) Restore(ctx context.Context, req registry.RestoreRequest, run registry.RunContext) error {
	if _, err := m.PlanRestore(ctx, req); err != nil {
		return err
	}
	return m.service.restoreStandalone(ctx, req.Clone(), run)
}

func (s Service) restoreStandalone(ctx context.Context, req registry.RestoreRequest, run registry.RunContext) (retErr error) {
	if err := s.reconcileMySQL(ctx, req.Instance, req.Language); err != nil {
		return err
	}
	if !validLogicalTaskID(run.TaskID) || !boolParameter(req.Parameters, "maintenanceConfirmed") || strings.TrimSpace(fmt.Sprint(req.Parameters["mode"])) != "standalone" {
		return localizedMySQLOperationError(req.Language, MySQLRestoreMaintenanceRequired)
	}
	data, ok := s.store.(backupStore)
	if !ok {
		return errors.New("MySQL restore store is unavailable")
	}
	instance, err := data.GetAppInstance(req.Instance.ID)
	if err != nil || instance.App != "mysql" || instanceTopology(instance) != "standalone" || instance.ServerID != req.Instance.ServerID {
		return localizedMySQLOperationError(req.Language, MySQLBackupStandaloneRequired)
	}
	backup, err := data.GetAppBackup(req.Backup.ID)
	if err != nil || !sameStandaloneBackupOwner(instance, instance, backup) || backup.Status != "success" || backup.BackupType != "logical-full" {
		return localizedMySQLOperationError(req.Language, MySQLBackupStandaloneRequired)
	}
	repository, err := backuprepo.New(req.RepositoryDir)
	if err != nil {
		return localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
	}
	verification, err := repository.Verify(backup)
	if err != nil || verification.SHA256 != backup.Checksum || verification.Size != backup.Size || !sameBackupPath(verification.Paths.Archive, backup.Path) {
		return localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
	}
	manifest, err := decodeRestoreManifest(verification.Manifest)
	if err != nil || manifest.BackupID != backup.ID || manifest.InstanceID != backup.InstanceID || manifest.SourceServerID != backup.ServerID {
		return localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	if err := updateRestorePhase(data, &backup, "preflight"); err != nil {
		return err
	}
	server, err := s.store.GetServer(instance.ServerID, false)
	if err != nil {
		return err
	}
	credential, err := data.GetBoundCredential(instance.ID, "admin", true)
	if err != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
		return localizedMySQLOperationError(req.Language, MySQLCredentialUnavailable)
	}

	// Repository verification above deliberately precedes this first remote
	// mutation. The short-lived work directory carries no caller path input.
	probeWork := mysqlBackupWorkDir(run.TaskID)
	if _, err := s.remote.Run(ctx, server, bootstrapBackupWorkCommand(probeWork)); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, cleanupErr := s.remote.Run(cleanupCtx, server, cleanupBackupCommand(probeWork))
		if cleanupErr != nil && retErr == nil {
			retErr = cleanupErr
		}
	}()
	secretPath, err := writeMySQLSecretContext(credential, instancePort(instance))
	if err != nil {
		return localizedMySQLOperationError(req.Language, MySQLCredentialUnavailable)
	}
	defer os.Remove(secretPath)
	if err := s.remote.UploadFile(ctx, server, secretPath, path.Join(probeWork, "secret-context.cnf"), 0o600); err != nil {
		return err
	}
	inspectionResult, err := s.remote.Run(ctx, server, inspectBackupCommand(probeWork, instancePort(instance)))
	readable := err == nil
	var inspection mysqlBackupInspection
	if readable {
		inspection, err = parseMySQLBackupInspection(inspectionResult.Stdout)
		if err != nil {
			readable = false
		}
	}
	if !readable {
		if !boolParameter(req.Parameters, "disasterConfirmed") {
			return localizedMySQLOperationError(req.Language, MySQLRestoreTargetNotClean)
		}
	} else {
		if !boolParameter(req.Parameters, "createPreRestoreBackup") {
			return localizedMySQLOperationError(req.Language, MySQLRestoreTargetNotClean)
		}
		if err := ValidateRestoreCompatibility(manifest, backup.BackupType, instanceTopology(instance), inspection.MySQLVersion); err != nil {
			return err
		}
		preRequest := registry.BackupRequest{
			Instance: instance, Servers: []store.Server{server}, Language: req.Language, Actor: req.Actor,
			RepositoryDir: req.RepositoryDir, KeepLast: 1,
			Parameters: map[string]any{"name": "pre-restore", "threads": restoreThreads(req.Parameters), "maxRateMBps": 0},
		}
		if s.preRestoreBackup != nil {
			err = s.preRestoreBackup(ctx, preRequest, run)
		} else {
			err = s.backupStandaloneCore(ctx, preRequest, run, standaloneBackupExecution{backupType: "pre-restore", recordPlan: false, retention: false})
		}
		if err != nil {
			return err
		}
	}
	if err := updateRestorePhase(data, &backup, "pre_restore_complete"); err != nil {
		return err
	}

	// The pre-restore core may have cleaned the task work directory. Recreate
	// the controlled restore workspace and upload only repository-verified data.
	if _, err := s.remote.Run(ctx, server, bootstrapBackupWorkCommand(probeWork)); err != nil {
		return err
	}
	if err := s.remote.UploadFile(ctx, server, secretPath, path.Join(probeWork, "secret-context.cnf"), 0o600); err != nil {
		return err
	}
	if err := s.remote.UploadFile(ctx, server, verification.Paths.Archive, path.Join(probeWork, "dump.tar"), 0o600); err != nil {
		return err
	}
	if _, err := s.remote.Run(ctx, server, extractRestoreArchiveCommand(probeWork)); err != nil {
		return localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	if _, err := s.remote.Run(ctx, server, dryRunRestoreCommand(probeWork)); err != nil {
		return err
	}

	session, sessionCleanup, err := s.localInfileSession(ctx, instance, server, credential)
	if err != nil {
		return err
	}
	defer sessionCleanup()
	guard := newLocalInfileGuard(session)
	if err := guard.Capture(ctx); err != nil {
		return err
	}
	mutationStarted := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if restoreErr := guard.Restore(cleanupCtx); restoreErr != nil {
			_ = recordMySQLReconciliationMarker(s.store, instance, guard.original, run.TaskID)
			if mutationStarted {
				_ = updateRestorePhase(data, &backup, "restore_incomplete")
			}
			retErr = errors.Join(localizedMySQLOperationError(req.Language, MySQLLocalInfileRestoreFailed), restoreErr)
		} else if retErr != nil && mutationStarted {
			_ = updateRestorePhase(data, &backup, "restore_incomplete")
		}
	}()
	if err := guard.Enable(ctx); err != nil {
		return err
	}
	if err := updateRestorePhase(data, &backup, "schema_mutation_started"); err != nil {
		return err
	}
	mutationStarted = true
	if _, err := s.remote.Run(ctx, server, dropSchemasCommand(probeWork, instancePort(instance), manifest.Schemas)); err != nil {
		return err
	}
	script, err := RenderLogicalRestoreScript(LogicalRestoreScriptOptions{TaskID: run.TaskID, Threads: restoreThreads(req.Parameters)})
	if err != nil {
		return err
	}
	localScript, err := installerkit.WriteTempScript("aifar-mysql-restore-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(localScript)
	remoteScript := path.Join(probeWork, "logical-restore.sh")
	if err := s.remote.UploadFile(ctx, server, localScript, remoteScript, 0o700); err != nil {
		return err
	}
	if _, err := s.remote.Run(ctx, server, "sh "+installerkit.ShellQuote(remoteScript)); err != nil {
		return err
	}
	if err := updateRestorePhase(data, &backup, "load_complete"); err != nil {
		return err
	}
	restoreCtx, restoreCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	err = guard.Restore(restoreCtx)
	restoreCancel()
	if err != nil {
		return err
	}
	result, err := s.remote.Run(ctx, server, verifySchemasCommand(probeWork, instancePort(instance)))
	if err != nil || !sameSchemaSet(result.Stdout, manifest.Schemas) {
		return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	if _, err := s.remote.Run(ctx, server, verifyRestoreDataCommand(probeWork, instancePort(instance), manifest.Schemas)); err != nil {
		return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	if err := updateRestorePhase(data, &backup, "verified"); err != nil {
		return err
	}
	return nil
}

func boolParameter(values map[string]any, key string) bool {
	value, ok := values[key].(bool)
	return ok && value
}

func restoreThreads(values map[string]any) int {
	threads := intParameter(values, "threads")
	if !validLogicalThreads(threads) {
		return defaultBackupThreads
	}
	return threads
}

func decodeRestoreManifest(raw []byte) (BackupManifest, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var manifest BackupManifest
	if err := decoder.Decode(&manifest); err != nil {
		return BackupManifest{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return BackupManifest{}, errors.New("backup manifest must be one object")
	}
	return NormalizeBackupManifest(manifest)
}

func updateRestorePhase(data backupStore, backup *store.AppBackup, phase string) error {
	allowed := map[string]bool{"preflight": true, "pre_restore_complete": true, "schema_mutation_started": true, "load_complete": true, "verified": true, "restore_incomplete": true}
	if backup == nil || !allowed[phase] {
		return errors.New("invalid restore phase")
	}
	metadata, err := strictBackupMetadata(backup.Metadata)
	if err != nil {
		return err
	}
	metadata["restorePhase"], _ = json.Marshal(phase)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	backup.Metadata = string(encoded)
	saved, err := data.SaveAppBackup(*backup)
	if err == nil {
		*backup = saved
	}
	return err
}

func recordMySQLReconciliationMarker(data Store, instance store.AppInstance, original, taskID string) error {
	if (original != "ON" && original != "OFF") || !validLogicalTaskID(taskID) {
		return errors.New("invalid MySQL reconciliation marker")
	}
	metadata, err := strictBackupMetadata(instance.Metadata)
	if err != nil {
		return err
	}
	marker := mysqlReconciliationMarker{Version: 1, Kind: "local_infile", OriginalValue: original, RecordedAt: time.Now().UTC().Format(time.RFC3339), TaskID: taskID}
	metadata["mysqlReconciliation"], _ = json.Marshal(marker)
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	instance.Metadata = string(encoded)
	_, err = data.SaveAppInstance(instance)
	return err
}

const safeRestoreExtractHelper = `
import os, pathlib, sys, tarfile
work = pathlib.Path(sys.argv[1])
archive = work / "dump.tar"
dump = work / "dump"
if work.is_symlink() or archive.is_symlink() or not archive.is_file(): raise SystemExit(1)
if dump.exists() or dump.is_symlink(): raise SystemExit(1)
count = 0
total = 0
with tarfile.open(archive, "r:") as source:
  for member in source.getmembers():
    count += 1; total += member.size
    parts = pathlib.PurePosixPath(member.name).parts
    if count > 100000 or total > 1099511627776 or member.name.startswith("/") or ".." in parts or not parts or parts[0] != "dump" or not (member.isfile() or member.isdir()): raise SystemExit(1)
  source.extractall(work)
if not dump.is_dir() or dump.is_symlink(): raise SystemExit(1)
os.chmod(dump, 0o700)
`

func extractRestoreArchiveCommand(work string) string {
	return "python3 -c " + installerkit.ShellQuote(safeRestoreExtractHelper) + " " + installerkit.ShellQuote(work)
}

func dryRunRestoreCommand(work string) string {
	return "set -eu; test -f " + installerkit.ShellQuote(path.Join(work, "dump.tar")) + "; test -d " + installerkit.ShellQuote(path.Join(work, "dump")) + "; test -x " + installerkit.ShellQuote(path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh"))
}

func dropSchemasCommand(work string, port int, schemas []string) string {
	statements := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		if strictSchemaName.MatchString(schema) && !isSystemSchema(schema) {
			statements = append(statements, "DROP DATABASE IF EXISTS `"+schema+"`")
		}
	}
	return localInfileSQLCommand(work, port, strings.Join(statements, "; "))
}

func verifySchemasCommand(work string, port int) string {
	query := "SELECT schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys') ORDER BY schema_name /*__AIFAR_VERIFY_SCHEMAS__*/"
	return localInfileSQLCommand(work, port, query)
}

func verifyRestoreDataCommand(work string, port int, schemas []string) string {
	quoted := make([]string, 0, len(schemas))
	for _, schema := range schemas {
		quoted = append(quoted, "'"+schema+"'")
	}
	query := "SELECT 1 /*__AIFAR_VERIFY_DATA__*/; SELECT table_schema,COUNT(*) FROM information_schema.tables WHERE table_schema IN (" + strings.Join(quoted, ",") + ") GROUP BY table_schema ORDER BY table_schema"
	return localInfileSQLCommand(work, port, query)
}

func sameSchemaSet(output string, expected []string) bool {
	got := make([]string, 0, len(expected))
	for _, line := range strings.Split(output, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			got = append(got, value)
		}
	}
	want := append([]string(nil), expected...)
	sort.Strings(got)
	sort.Strings(want)
	return strings.Join(got, "\x00") == strings.Join(want, "\x00")
}

var _ registry.RestoreModule = Module{}
