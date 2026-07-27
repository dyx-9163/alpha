package mysql

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installflow"
	"aifar-deployment/backend/internal/store"
)

var standaloneRestoreStepNames = []string{
	"load-backup", "acquire-instance-lock", "verify-maintenance-confirmation", "verify-manifest",
	"verify-checksum", "verify-version", "create-pre-restore-backup", "upload-backup", "extract-backup",
	"dry-run-load", "capture-local-infile", "enable-local-infile", "drop-target-schemas", "load-dump",
	"restore-local-infile", "verify-schemas", "verify-data", "record-restore", "cleanup-workdir", "release-lock",
}

type restoreStore interface {
	backupStore
	SaveAppInstance(store.AppInstance) (store.AppInstance, error)
}

type standaloneRestoreProgress struct {
	runner   installflow.Runner
	recorder installflow.Recorder
	target   string
	steps    []installflow.Step
	started  map[int]bool
}

func newStandaloneRestoreProgress(run registry.RunContext, target, language string) *standaloneRestoreProgress {
	recorder, _ := run.Log.(installflow.Recorder)
	steps := make([]installflow.Step, len(standaloneRestoreStepNames))
	for index, name := range standaloneRestoreStepNames {
		steps[index] = installflow.Step{Name: name, Title: restoreStepTitle(language, name)}
	}
	progress := &standaloneRestoreProgress{recorder: recorder, target: target, steps: steps, started: map[int]bool{}}
	progress.runner = installflow.Runner{Log: run.Log, Recorder: recorder, Target: target, Steps: steps}
	installflow.StartTarget(recorder, target)
	return progress
}

func (p *standaloneRestoreProgress) step(index int, operation func() error) error {
	if index < 1 || index > len(p.steps) || p.started[index] {
		return errors.New("invalid or duplicate MySQL restore step")
	}
	p.started[index] = true
	definition := p.steps[index-1]
	return p.runner.Run(index, definition.Name, definition.Title, operation)
}

func (p *standaloneRestoreProgress) finish(retErr *error, ctx context.Context) {
	status := "success"
	errText := ""
	if *retErr != nil {
		status = "failed"
		errText = (*retErr).Error()
		if errors.Is(*retErr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			status = "cancelled"
		}
	}
	for index, definition := range p.steps {
		order := index + 1
		if p.started[order] {
			continue
		}
		terminal := status
		if terminal == "success" {
			terminal = "failed"
			*retErr = errors.New("MySQL restore completed with unrecorded steps")
			errText = (*retErr).Error()
			status = "failed"
		}
		if p.recorder != nil {
			p.recorder.StartStep(p.target, definition.Name, definition.Title, order)
			p.recorder.FinishStep(p.target, definition.Name, terminal, errText)
		}
	}
	installflow.FinishTarget(p.recorder, p.target, status, errText)
}

func (m Module) PlanRestore(ctx context.Context, req registry.RestoreRequest) ([]registry.InstallStepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Instance.App != "mysql" {
		return nil, mysqlOperationError(MySQLBackupStandaloneRequired)
	}
	if instanceTopology(req.Instance) == "innodb-cluster" {
		return m.planHealthyClusterRestore(ctx, req)
	}
	if instanceTopology(req.Instance) != "standalone" ||
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
	if instanceTopology(req.Instance) == "innodb-cluster" {
		return m.restoreHealthyInnoDBCluster(ctx, req.Clone(), run)
	}
	return m.service.restoreStandalone(ctx, req.Clone(), run)
}

func (s Service) restoreStandalone(ctx context.Context, req registry.RestoreRequest, run registry.RunContext) (retErr error) {
	return s.restoreLogical(ctx, req, run, nil)
}

func (s Service) restoreLogical(ctx context.Context, req registry.RestoreRequest, run registry.RunContext, cluster *resolvedInnoDBCluster) (retErr error) {
	progress := newStandaloneRestoreProgress(run, req.Instance.ServerID, req.Language)
	defer progress.finish(&retErr, ctx)
	data, ok := s.store.(restoreStore)
	if !ok {
		return errors.New("MySQL restore store is unavailable")
	}
	topology := "standalone"
	if cluster != nil {
		topology = "innodb-cluster"
	}
	var instance store.AppInstance
	var backup store.AppBackup
	if err := progress.step(1, func() error {
		var err error
		instance, err = data.GetAppInstance(req.Instance.ID)
		if err != nil || instance.App != "mysql" || instanceTopology(instance) != topology || instance.ServerID != req.Instance.ServerID {
			return localizedMySQLOperationError(req.Language, MySQLBackupStandaloneRequired)
		}
		backup, err = data.GetAppBackup(req.Backup.ID)
		owned := err == nil && backup.Status == "success" && backup.BackupType == "logical-full"
		if topology == "standalone" {
			owned = owned && sameStandaloneBackupOwner(instance, instance, backup)
		} else {
			owned = owned && clusterIDFromBackup(backup) == cluster.clusterID
		}
		if !owned {
			return localizedMySQLOperationError(req.Language, MySQLBackupStandaloneRequired)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := progress.step(2, func() error { return ctx.Err() }); err != nil {
		return err
	}
	if err := progress.step(3, func() error {
		if !validLogicalTaskID(run.TaskID) || !boolParameter(req.Parameters, "maintenanceConfirmed") || strings.TrimSpace(fmt.Sprint(req.Parameters["mode"])) != topology {
			return localizedMySQLOperationError(req.Language, MySQLRestoreMaintenanceRequired)
		}
		return nil
	}); err != nil {
		return err
	}
	repository, err := backuprepo.New(req.RepositoryDir)
	if err != nil {
		return localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
	}
	var verification backuprepo.Verification
	var manifest BackupManifest
	if err := progress.step(4, func() error {
		var err error
		verification, err = repository.Verify(backup)
		if err != nil {
			return localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
		}
		manifest, err = decodeRestoreManifest(verification.Manifest)
		if err != nil || manifest.BackupID != backup.ID || manifest.SourceServerID != backup.ServerID || manifest.ManifestVersion != 2 || manifest.Verification == nil || (topology == "innodb-cluster" && manifest.ClusterID != cluster.clusterID) {
			return localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := progress.step(5, func() error {
		if verification.SHA256 != backup.Checksum || verification.Size != backup.Size || !sameBackupPath(verification.Paths.Archive, backup.Path) {
			return localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
		}
		return nil
	}); err != nil {
		return err
	}
	canonicalManifest, err := CanonicalBackupManifestJSON(manifest)
	if err != nil {
		return localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	expectedManifestSHA := fmt.Sprintf("%x", sha256.Sum256(canonicalManifest))
	var server store.Server
	var credential store.Credential
	var inspection mysqlBackupInspection
	var readable bool
	probeWork := mysqlBackupWorkDir(run.TaskID)
	var secretPath string
	workdirPrepared := false
	cleanupRestoreWorkdir := func() error {
		if !workdirPrepared {
			return nil
		}
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, err := s.remote.Run(cleanupCtx, server, cleanupBackupCommand(probeWork))
		return err
	}
	defer func() {
		if progress.started[19] {
			return
		}
		cleanupErr := progress.step(19, cleanupRestoreWorkdir)
		if cleanupErr != nil && retErr == nil {
			retErr = cleanupErr
		}
	}()
	if err := progress.step(6, func() error {
		if err := updateRestorePhase(data, &backup, "preflight", run.TaskID, expectedManifestSHA); err != nil {
			return err
		}
		var err error
		server, err = s.store.GetServer(instance.ServerID, false)
		if err != nil {
			return err
		}
		credential, err = data.GetBoundCredential(instance.ID, "admin", true)
		if err != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
			return localizedMySQLOperationError(req.Language, MySQLCredentialUnavailable)
		}
		// Repository verification above deliberately precedes this first remote mutation.
		if _, err := s.remote.Run(ctx, server, bootstrapBackupWorkCommand(probeWork)); err != nil {
			return err
		}
		workdirPrepared = true
		secretPath, err = writeMySQLSecretContext(credential, instancePort(instance))
		if err != nil {
			return localizedMySQLOperationError(req.Language, MySQLCredentialUnavailable)
		}
		if err := s.remote.UploadFile(ctx, server, secretPath, path.Join(probeWork, "secret-context.cnf"), 0o600); err != nil {
			return err
		}
		inspectionResult, inspectErr := s.remote.Run(ctx, server, inspectBackupCommand(probeWork, instancePort(instance)))
		readable = inspectErr == nil
		if readable {
			inspection, err = parseMySQLBackupInspection(inspectionResult.Stdout)
			if err != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
			}
		} else if !isExplicitMySQLUnreachable(inspectErr) {
			return localizedMySQLOperationError(req.Language, MySQLRestoreTargetNotClean)
		}
		if !readable {
			if !boolParameter(req.Parameters, "disasterConfirmed") {
				return localizedMySQLOperationError(req.Language, MySQLRestoreTargetNotClean)
			}
		} else {
			if !sameSchemaSet(strings.Join(inspection.Schemas, "\n"), manifest.Schemas) || !boolParameter(req.Parameters, "createPreRestoreBackup") {
				return localizedMySQLOperationError(req.Language, MySQLRestoreTargetNotClean)
			}
			if err := ValidateRestoreCompatibility(manifest, backup.BackupType, instanceTopology(instance), inspection.MySQLVersion); err != nil {
				return err
			}
		}
		return s.reconcileMySQL(ctx, instance, req.Language)
	}); err != nil {
		return err
	}
	defer os.Remove(secretPath)
	if err := progress.step(7, func() error {
		if readable {
			preRequest := registry.BackupRequest{
				Instance: instance, Servers: []store.Server{server}, Language: req.Language, Actor: req.Actor,
				RepositoryDir: req.RepositoryDir, KeepLast: 1,
				Parameters: map[string]any{"name": "pre-restore", "threads": restoreThreads(req.Parameters), "maxRateMBps": 0},
			}
			if s.preRestoreBackup != nil {
				err = s.preRestoreBackup(ctx, preRequest, run)
			} else if cluster != nil {
				members := make([]ClusterMemberRef, 0, len(cluster.members))
				ids := make([]string, 0, len(cluster.members))
				for _, member := range cluster.members {
					members = append(members, ClusterMemberRef{InstanceID: member.instance.ID, ServerID: member.server.ID, Endpoint: member.endpoint, Role: member.role, Status: member.status})
					ids = append(ids, member.instance.ID)
				}
				err = s.backupStandaloneCore(ctx, preRequest, run, standaloneBackupExecution{backupType: "pre-restore", recordPlan: false, retention: false, topology: "innodb-cluster", clusterID: cluster.clusterID, members: members, routers: cluster.routers, retentionInstanceIDs: ids})
			} else {
				err = s.backupStandaloneCore(ctx, preRequest, run, standaloneBackupExecution{backupType: "pre-restore", recordPlan: false, retention: false})
			}
			if err != nil {
				return err
			}
		}
		return updateRestorePhase(data, &backup, "pre_restore_complete", run.TaskID, expectedManifestSHA)
	}); err != nil {
		return err
	}

	// The pre-restore core may have cleaned the task work directory. Recreate
	// the controlled restore workspace and upload only repository-verified data.
	if err := progress.step(8, func() error {
		if _, err := s.remote.Run(ctx, server, bootstrapBackupWorkCommand(probeWork)); err != nil {
			return err
		}
		if err := s.remote.UploadFile(ctx, server, secretPath, path.Join(probeWork, "secret-context.cnf"), 0o600); err != nil {
			return err
		}
		return s.remote.UploadFile(ctx, server, verification.Paths.Archive, path.Join(probeWork, "dump.tar"), 0o600)
	}); err != nil {
		return err
	}
	if err := progress.step(9, func() error {
		if _, err := s.remote.Run(ctx, server, extractRestoreArchiveCommand(probeWork)); err != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := progress.step(10, func() error { _, err := s.remote.Run(ctx, server, dryRunRestoreCommand(probeWork)); return err }); err != nil {
		return err
	}

	var session localInfileSession
	var sessionCleanup func()
	var guard *localInfileGuard
	if err := progress.step(11, func() error {
		var err error
		session, sessionCleanup, err = s.localInfileSession(ctx, instance, server, credential)
		if err != nil {
			return err
		}
		guard = newLocalInfileGuard(session)
		return guard.Capture(ctx)
	}); err != nil {
		return err
	}
	defer sessionCleanup()
	localInfileMayBeEnabled := false
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		var restoreErr error
		if !progress.started[15] {
			restoreErr = progress.step(15, func() error { return guard.Restore(cleanupCtx) })
		} else {
			restoreErr = guard.Restore(cleanupCtx)
		}
		var clearErr error
		if restoreErr == nil {
			clearErr = clearMySQLReconciliationMarker(data, instance.ID, guard.original, run.TaskID)
		}
		if restoreErr != nil {
			retErr = errors.Join(localizedMySQLOperationError(req.Language, MySQLLocalInfileRestoreFailed), restoreErr)
		} else if clearErr != nil {
			retErr = errors.Join(retErr, localizedMySQLOperationError(req.Language, MySQLReconciliationRequired), clearErr)
		}
		if retErr != nil && localInfileMayBeEnabled {
			if phaseErr := updateRestorePhase(data, &backup, "restore_incomplete", run.TaskID, expectedManifestSHA); phaseErr != nil {
				retErr = errors.Join(retErr, phaseErr)
			}
		}
	}()
	if err := progress.step(12, func() error {
		if err := recordMySQLReconciliationMarker(data, instance, guard.original, run.TaskID); err != nil {
			return errors.Join(localizedMySQLOperationError(req.Language, MySQLReconciliationRequired), err)
		}
		localInfileMayBeEnabled = true
		return guard.Enable(ctx)
	}); err != nil {
		return err
	}
	if err := progress.step(13, func() error {
		if err := updateRestorePhase(data, &backup, "schema_mutation_started", run.TaskID, expectedManifestSHA); err != nil {
			return err
		}
		_, err := s.remote.Run(ctx, server, dropSchemasCommand(probeWork, instancePort(instance), manifest.Schemas))
		return err
	}); err != nil {
		return err
	}
	if err := progress.step(14, func() error {
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
		return updateRestorePhase(data, &backup, "load_complete", run.TaskID, expectedManifestSHA)
	}); err != nil {
		return err
	}
	if err := progress.step(15, func() error {
		restoreCtx, restoreCancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer restoreCancel()
		return guard.Restore(restoreCtx)
	}); err != nil {
		return err
	}
	if err := progress.step(16, func() error {
		result, err := s.remote.Run(ctx, server, finalRestoreVerificationCommand(probeWork, instancePort(instance)))
		if err != nil || !matchesFinalRestoreVerification(result.Stdout, manifest.Verification) {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		return nil
	}); err != nil {
		return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	if err := progress.step(17, func() error {
		if err := verifyRestoreExpectation(data, repository, backup.ID, run.TaskID, expectedManifestSHA); err != nil {
			return err
		}
		if cluster != nil {
			return s.verifyClusterRecovered(ctx, cluster, run.TaskID)
		}
		return nil
	}); err != nil {
		return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	if err := progress.step(18, func() error {
		if err := clearMySQLReconciliationMarker(data, instance.ID, guard.original, run.TaskID); err != nil {
			return errors.Join(localizedMySQLOperationError(req.Language, MySQLReconciliationRequired), err)
		}
		return updateRestorePhase(data, &backup, "verified", run.TaskID, expectedManifestSHA)
	}); err != nil {
		return err
	}
	if err := progress.step(19, cleanupRestoreWorkdir); err != nil {
		return err
	}
	if err := progress.step(20, func() error { return ctx.Err() }); err != nil {
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

const restorePersistenceAttempts = 3

func updateRestorePhase(data backupStore, backup *store.AppBackup, phase, taskID, expectedManifestSHA string) error {
	allowed := map[string]bool{"preflight": true, "pre_restore_complete": true, "schema_mutation_started": true, "load_complete": true, "verified": true, "restore_incomplete": true}
	if backup == nil || !allowed[phase] || !validLogicalTaskID(taskID) || !lowerSHA256Pattern.MatchString(expectedManifestSHA) {
		return errors.New("invalid restore phase")
	}
	var lastErr error
	for attempt := 0; attempt < restorePersistenceAttempts; attempt++ {
		metadata, err := strictBackupMetadata(backup.Metadata)
		if err != nil {
			return err
		}
		if phase != "preflight" && (!rawJSONStringEquals(metadata["restoreTaskId"], taskID) || !rawJSONStringEquals(metadata["restoreExpectedManifestSha256"], expectedManifestSHA)) {
			return errors.New("restore expectation metadata changed")
		}
		metadata["restorePhase"], _ = json.Marshal(phase)
		metadata["restoreTaskId"], _ = json.Marshal(taskID)
		metadata["restoreExpectedManifestSha256"], _ = json.Marshal(expectedManifestSHA)
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		candidate := *backup
		candidate.Metadata = string(encoded)
		saved, err := data.SaveAppBackup(candidate)
		if err == nil {
			*backup = saved
			return nil
		}
		lastErr = err
	}
	return errors.Join(errors.New("persist MySQL restore phase failed"), lastErr)
}

func rawJSONStringEquals(raw json.RawMessage, expected string) bool {
	var value string
	return json.Unmarshal(raw, &value) == nil && value == expected
}

func verifyRestoreExpectation(data backupStore, repository *backuprepo.Repository, backupID, taskID, expectedManifestSHA string) error {
	fresh, err := data.GetAppBackup(backupID)
	if err != nil {
		return err
	}
	metadata, err := strictBackupMetadata(fresh.Metadata)
	if err != nil || !rawJSONStringEquals(metadata["restoreTaskId"], taskID) || !rawJSONStringEquals(metadata["restoreExpectedManifestSha256"], expectedManifestSHA) {
		return errors.New("restore expectation metadata changed")
	}
	verification, err := repository.Verify(fresh)
	if err != nil {
		return err
	}
	manifest, err := decodeRestoreManifest(verification.Manifest)
	if err != nil {
		return err
	}
	canonical, err := CanonicalBackupManifestJSON(manifest)
	if err != nil {
		return err
	}
	if fmt.Sprintf("%x", sha256.Sum256(canonical)) != expectedManifestSHA {
		return errors.New("restore manifest digest changed")
	}
	return nil
}

func recordMySQLReconciliationMarker(data restoreStore, instance store.AppInstance, original, taskID string) error {
	if (original != "ON" && original != "OFF") || !validLogicalTaskID(taskID) {
		return errors.New("invalid MySQL reconciliation marker")
	}
	marker := mysqlReconciliationMarker{Version: 1, Kind: "local_infile", OriginalValue: original, RecordedAt: time.Now().UTC().Format(time.RFC3339), TaskID: taskID}
	var lastErr error
	for attempt := 0; attempt < restorePersistenceAttempts; attempt++ {
		fresh, err := data.GetAppInstance(instance.ID)
		if err != nil || fresh.App != "mysql" || instanceTopology(fresh) != "standalone" || fresh.ServerID != instance.ServerID {
			return errors.New("MySQL instance ownership changed before reconciliation marker persistence")
		}
		metadata, err := strictBackupMetadata(fresh.Metadata)
		if err != nil {
			return err
		}
		metadata["mysqlReconciliation"], _ = json.Marshal(marker)
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		fresh.Metadata = string(encoded)
		if _, err = data.SaveAppInstance(fresh); err == nil {
			return nil
		}
		lastErr = err
	}
	return errors.Join(errors.New("persist MySQL reconciliation marker failed"), lastErr)
}

func clearMySQLReconciliationMarker(data restoreStore, instanceID, original, taskID string) error {
	var lastErr error
	for attempt := 0; attempt < restorePersistenceAttempts; attempt++ {
		fresh, err := data.GetAppInstance(instanceID)
		if err != nil {
			return err
		}
		metadata, marker, present, err := parseMySQLReconciliationMarker(fresh.Metadata)
		if err != nil {
			return err
		}
		if !present {
			return nil
		}
		if marker.OriginalValue != original || marker.TaskID != taskID {
			return errors.New("MySQL reconciliation marker ownership changed")
		}
		delete(metadata, "mysqlReconciliation")
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		fresh.Metadata = string(encoded)
		if _, err = data.SaveAppInstance(fresh); err == nil {
			return nil
		}
		lastErr = err
	}
	return errors.Join(errors.New("clear MySQL reconciliation marker failed"), lastErr)
}

func isExplicitMySQLUnreachable(err error) bool {
	if err == nil {
		return false
	}
	var operationError *net.OpError
	if errors.As(err, &operationError) && strings.EqualFold(operationError.Op, "dial") {
		return true
	}
	text := strings.ToLower(err.Error())
	for _, fragment := range []string{"connection refused", "no route to host", "network is unreachable", "host is unreachable", "dial tcp"} {
		if strings.Contains(text, fragment) {
			return true
		}
	}
	return false
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

func finalRestoreVerificationCommand(work string, port int) string {
	statements := []string{
		"SELECT '__AIFAR_VERIFY_PING__',1 /*__AIFAR_VERIFY_FINAL__*/",
		"SELECT '__AIFAR_VERIFY_SCHEMA__',s.schema_name,(SELECT COUNT(*) FROM information_schema.tables t WHERE t.table_schema=s.schema_name AND t.table_type='BASE TABLE') FROM information_schema.schemata s WHERE s.schema_name NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys') ORDER BY s.schema_name",
		"SELECT '__AIFAR_VERIFY_TABLE__',table_schema,table_name FROM information_schema.tables WHERE table_type='BASE TABLE' AND table_schema NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys') ORDER BY table_schema,table_name",
	}
	return localInfileSQLCommand(work, port, strings.Join(statements, "; "))
}

func matchesFinalRestoreVerification(output string, expected *BackupVerification) bool {
	if expected == nil {
		return false
	}
	ping := 0
	schemaCounts := map[string]int{}
	tables := make([]string, 0, expected.TableCount)
	for _, line := range strings.Split(output, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		switch {
		case len(fields) == 2 && fields[0] == "__AIFAR_VERIFY_PING__" && fields[1] == "1":
			ping++
		case len(fields) == 3 && fields[0] == "__AIFAR_VERIFY_SCHEMA__" && strictSchemaName.MatchString(fields[1]):
			count, err := strconv.Atoi(fields[2])
			if err != nil || count < 0 {
				return false
			}
			if _, duplicate := schemaCounts[fields[1]]; duplicate {
				return false
			}
			schemaCounts[fields[1]] = count
		case len(fields) == 3 && fields[0] == "__AIFAR_VERIFY_TABLE__" && strictSchemaName.MatchString(fields[1]) && strictSchemaName.MatchString(fields[2]):
			tables = append(tables, fields[1]+"\x00"+fields[2])
		default:
			return false
		}
	}
	if ping != 1 || len(schemaCounts) != expected.SchemaCount || len(tables) != expected.TableCount {
		return false
	}
	wantTables := make([]string, 0, expected.TableCount)
	for _, schema := range expected.Schemas {
		if schemaCounts[schema.Name] != schema.TableCount {
			return false
		}
		for _, table := range schema.Tables {
			wantTables = append(wantTables, schema.Name+"\x00"+table.Name)
		}
	}
	sort.Strings(tables)
	sort.Strings(wantTables)
	if strings.Join(tables, "\n") != strings.Join(wantTables, "\n") {
		return false
	}
	return true
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
