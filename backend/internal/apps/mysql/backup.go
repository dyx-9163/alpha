package mysql

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installflow"
	"aifar-deployment/backend/internal/store"
)

const (
	defaultBackupThreads    = 4
	backupSpaceSafetyBytes  = int64(64 << 20)
	maxBackupRetentionCount = 1000
)

type backupStore interface {
	GetBoundCredential(appInstanceID, purpose string, includeSecret bool) (store.Credential, error)
	GetAppInstance(id string) (store.AppInstance, error)
	GetAppBackup(id string) (store.AppBackup, error)
	SaveAppBackup(store.AppBackup) (store.AppBackup, error)
	ListAppBackupsForInstances(instanceIDs []string, includeDeleted bool) ([]store.AppBackup, error)
	MarkAppBackupDeleted(id string, completedAt time.Time) (store.AppBackup, error)
}

type backupParameters struct {
	Name        string
	Threads     int
	MaxRateMBps int
	KeepLast    int
}

type mysqlBackupInspection struct {
	MySQLVersion      string
	MySQLShellVersion string
	ServerUUID        string
	GTIDExecuted      string
	Schemas           []string
	EstimatedBytes    int64
}

type standaloneBackupState struct {
	record      store.AppBackup
	paths       backuprepo.BackupPaths
	manifest    []byte
	archiveSHA  string
	archiveSize int64
	remoteClean bool
	committed   bool
	repository  *backuprepo.Repository
	backupStore backupStore
	remote      Remote
	server      store.Server
	remoteWork  string
	log         Logger
}

var panelBackupAvailableBytes = panelFilesystemAvailableBytes

type mysqlSecretContextFile interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Close() error
}

var createMySQLSecretContextFile = func() (mysqlSecretContextFile, error) {
	return os.CreateTemp("", "aifar-mysql-secret-*.cnf")
}

var panelDFOutput = func(target string) ([]byte, error) {
	return exec.Command("df", "-Pk", target).Output()
}

func (m Module) PlanBackup(ctx context.Context, req registry.BackupRequest) ([]registry.InstallStepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if req.Instance.App != "mysql" || instanceTopology(req.Instance) != "standalone" {
		return nil, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	if strings.TrimSpace(req.Instance.ID) == "" || strings.TrimSpace(req.Instance.ServerID) == "" || strings.TrimSpace(req.RepositoryDir) == "" {
		return nil, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	_ = normalizeStandaloneBackupParameters(req)
	steps := standaloneBackupStepDefinitions(req.Language)
	plan := make([]registry.InstallStepPlan, len(steps))
	for index, step := range steps {
		plan[index] = registry.InstallStepPlan{Target: req.Instance.ServerID, Name: step.Name, Title: step.Title, Order: index + 1}
	}
	return plan, nil
}

func (m Module) Backup(ctx context.Context, req registry.BackupRequest, run registry.RunContext) error {
	if _, err := m.PlanBackup(ctx, req); err != nil {
		return err
	}
	return m.service.backupStandalone(ctx, req.Clone(), run)
}

var backupVerificationStepNames = []string{"load-backup", "verify-manifest", "verify-checksum", "record-verification"}

func (m Module) VerifyBackup(ctx context.Context, backupID, repositoryDir, language string, run registry.RunContext) error {
	data, ok := m.service.store.(backupStore)
	if !ok {
		return errors.New("MySQL backup store is unavailable")
	}
	copy := BackupCopyFor(language)
	steps := make([]installflow.Step, len(backupVerificationStepNames))
	for index, name := range backupVerificationStepNames {
		steps[index] = installflow.Step{Name: name, Title: copy.StepTitles[name]}
	}
	recorder, _ := run.Log.(installflow.Recorder)
	installflow.StartTarget(recorder, backupID)
	runner := installflow.Runner{
		Log: run.Log, Recorder: recorder, Target: backupID, Steps: steps,
		Messages: installflow.Messages{StepStart: copy.StepStart, StepDone: copy.StepDone, StepFailed: copy.StepFailed},
	}
	step := func(index int, fn func() error) error {
		definition := steps[index-1]
		return runner.Run(index, definition.Name, definition.Title, fn)
	}
	var backup store.AppBackup
	var repository *backuprepo.Repository
	var verification backuprepo.Verification
	if err := step(1, func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		var err error
		backup, err = data.GetAppBackup(backupID)
		if err != nil {
			return localizedMySQLOperationError(language, MySQLBackupVerifyNotAllowed)
		}
		if backup.App != "mysql" || backup.Status != "success" {
			return localizedMySQLOperationError(language, MySQLBackupVerifyNotAllowed)
		}
		if _, err := strictBackupMetadata(backup.Metadata); err != nil {
			return localizedMySQLOperationError(language, MySQLBackupVerifyFailed)
		}
		instance, err := data.GetAppInstance(backup.InstanceID)
		if err != nil || instance.App != "mysql" || instanceTopology(instance) != "standalone" || instance.ID != backup.InstanceID || instance.ServerID != backup.ServerID || backup.BackupType != "logical-full" {
			return localizedMySQLOperationError(language, MySQLBackupStandaloneRequired)
		}
		return nil
	}); err != nil {
		installflow.FinishTarget(recorder, backupID, "failed", err.Error())
		return err
	}
	if err := step(2, func() error {
		var err error
		repository, err = backuprepo.New(repositoryDir)
		if err == nil {
			verification, err = repository.Verify(backup)
		}
		if err != nil {
			if recordErr := recordBackupVerification(data, backup, "failed"); recordErr != nil {
				return errors.Join(localizedMySQLOperationError(language, MySQLBackupVerifyFailed), localizedMySQLOperationError(language, MySQLBackupVerificationRecordFailed))
			}
			return localizedMySQLOperationError(language, MySQLBackupVerifyFailed)
		}
		return nil
	}); err != nil {
		installflow.FinishTarget(recorder, backupID, "failed", copy.VerificationFailed)
		return err
	}
	if err := step(3, func() error {
		if verification.SHA256 != backup.Checksum || verification.Size != backup.Size || !sameBackupPath(verification.Paths.Archive, backup.Path) {
			return localizedMySQLOperationError(language, MySQLBackupVerifyFailed)
		}
		return ctx.Err()
	}); err != nil {
		_ = recordBackupVerification(data, backup, "failed")
		installflow.FinishTarget(recorder, backupID, "failed", copy.VerificationFailed)
		return err
	}
	if err := step(4, func() error { return recordBackupVerification(data, backup, "success") }); err != nil {
		installflow.FinishTarget(recorder, backupID, "failed", copy.VerificationRecordFailed)
		return localizedMySQLOperationError(language, MySQLBackupVerificationRecordFailed)
	}
	installflow.FinishTarget(recorder, backupID, "success", "")
	return nil
}

func recordBackupVerification(data backupStore, backup store.AppBackup, result string) error {
	metadata, err := strictBackupMetadata(backup.Metadata)
	if err != nil {
		return err
	}
	resultJSON, _ := json.Marshal(result)
	verifiedAtJSON, _ := json.Marshal(time.Now().UTC().Format(time.RFC3339Nano))
	metadata["verificationResult"] = resultJSON
	metadata["verifiedAt"] = verifiedAtJSON
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	backup.Metadata = string(encoded)
	_, err = data.SaveAppBackup(backup)
	return err
}

func strictBackupMetadata(value string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	metadata := map[string]json.RawMessage{}
	if err := decoder.Decode(&metadata); err != nil || metadata == nil {
		return nil, errors.New("backup metadata must be one JSON object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("backup metadata must contain one JSON object")
	}
	return metadata, nil
}

func localizedMySQLOperationError(language, code string) error {
	return errors.Join(mysqlOperationError(code), errors.New(MySQLBackupErrorText(language, code)))
}

func sameBackupPath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && filepath.Clean(leftAbs) == filepath.Clean(rightAbs)
}

func (s Service) backupStandalone(ctx context.Context, req registry.BackupRequest, run registry.RunContext) (retErr error) {
	data, ok := s.store.(backupStore)
	if !ok {
		return errors.New("MySQL backup store is unavailable")
	}
	downloader, ok := s.remote.(installerkit.FileDownloader)
	if !ok {
		return s.retainUnavailableDownloaderFailure(req, run, data)
	}
	if !validLogicalTaskID(run.TaskID) {
		return errors.New("invalid backup task ID")
	}
	repository, err := backuprepo.New(req.RepositoryDir)
	if err != nil {
		return err
	}
	parameters := normalizeStandaloneBackupParameters(req)
	backupID := store.NewID("backup")
	state := &standaloneBackupState{
		record: store.AppBackup{
			ID: backupID, App: "mysql", InstanceID: req.Instance.ID, ServerID: req.Instance.ServerID,
			BackupType: "logical-full", Status: "pending", TaskID: run.TaskID,
			Metadata: backupMetadata(parameters, "pending"), CreatedAt: time.Now().UTC(),
		},
		repository: repository, backupStore: data, remote: s.remote,
		remoteWork: mysqlBackupWorkDir(run.TaskID), log: run.Log,
	}
	state.record, err = data.SaveAppBackup(state.record)
	if err != nil {
		return err
	}
	recorder, _ := run.Log.(installflow.Recorder)
	copy := BackupCopyFor(req.Language)
	targetStarted := false
	defer func() {
		if !state.remoteClean && state.server.ID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			_, cleanupErr := s.remote.Run(cleanupCtx, state.server, cleanupBackupCommand(state.remoteWork))
			cancel()
			if cleanupErr == nil {
				state.remoteClean = true
			} else {
				safeCleanupErr := errors.New(copy.RemoteCleanupFailed)
				if state.log != nil {
					state.log.Error("%s", copy.RemoteCleanupFailed)
				}
				if state.record.Status == "success" {
					return
				}
				if retErr == nil {
					retErr = safeCleanupErr
				} else {
					retErr = errors.Join(retErr, safeCleanupErr)
				}
			}
		}
		if retErr == nil {
			return
		}
		if state.committed {
			published := state.record
			published.Status = "success"
			published.Path = state.paths.Archive
			published.Checksum = state.archiveSHA
			published.Size = state.archiveSize
			if deleteErr := state.repository.Delete(published); deleteErr != nil {
				retErr = errors.Join(retErr, deleteErr)
			}
		} else if state.paths.PartialArchive != "" {
			_ = os.Remove(state.paths.PartialArchive)
		}
		state.record.Status = "failed"
		state.record.Path = state.paths.Archive
		state.record.CompletedAt = time.Now().UTC()
		state.record.Metadata = backupMetadata(parameters, "failed")
		_, _ = data.SaveAppBackup(state.record)
		if targetStarted {
			installflow.FinishTarget(recorder, req.Instance.ServerID, "failed", backupDisplayError(req.Language, retErr).Error())
		}
	}()

	installflow.StartTarget(recorder, req.Instance.ServerID)
	targetStarted = true
	steps := standaloneBackupStepDefinitions(req.Language)
	runner := installflow.Runner{
		Log: run.Log, Recorder: recorder, Target: req.Instance.ServerID, Steps: steps,
		Messages: installflow.Messages{
			StepStart:  copy.StepStart,
			StepDone:   copy.StepDone,
			StepFailed: copy.StepFailed,
		},
	}
	step := func(index int, fn func() error) error {
		definition := steps[index-1]
		var operationErr error
		runnerErr := runner.Run(index, definition.Name, definition.Title, func() error {
			operationErr = fn()
			return backupDisplayError(req.Language, operationErr)
		})
		if operationErr != nil {
			return operationErr
		}
		return runnerErr
	}

	var credential store.Credential
	var inspection mysqlBackupInspection
	var script string
	if err := step(1, func() error {
		if req.Instance.App != "mysql" || instanceTopology(req.Instance) != "standalone" {
			return mysqlOperationError(MySQLBackupUnsupportedTopology)
		}
		server, err := s.store.GetServer(req.Instance.ServerID, true)
		if err != nil {
			return err
		}
		state.server = server
		state.record.Status = "running"
		state.record.Metadata = backupMetadata(parameters, "running")
		state.record, err = data.SaveAppBackup(state.record)
		return err
	}); err != nil {
		return err
	}
	if err := step(2, func() error { return ctx.Err() }); err != nil {
		return err
	}
	if err := step(3, func() error {
		var err error
		credential, err = data.GetBoundCredential(req.Instance.ID, "admin", true)
		if err != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
			return mysqlOperationError(MySQLCredentialUnavailable)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := step(4, func() error {
		if _, err := s.remote.Run(ctx, state.server, bootstrapBackupWorkCommand(state.remoteWork)); err != nil {
			return err
		}
		secretPath, err := writeMySQLSecretContext(credential, instancePort(req.Instance))
		if err != nil {
			return mysqlOperationError(MySQLCredentialUnavailable)
		}
		defer os.Remove(secretPath)
		if err := s.remote.UploadFile(ctx, state.server, secretPath, path.Join(state.remoteWork, "secret-context.cnf"), 0o600); err != nil {
			return err
		}
		result, err := s.remote.Run(ctx, state.server, inspectBackupCommand(state.remoteWork, instancePort(req.Instance)))
		if err != nil {
			return err
		}
		inspection, err = parseMySQLBackupInspection(result.Stdout)
		return err
	}); err != nil {
		return err
	}
	if err := step(5, func() error {
		result, err := s.remote.Run(ctx, state.server, sourceSpaceCommand(state.remoteWork))
		if err != nil {
			return err
		}
		sourceFree, err := strconv.ParseInt(strings.TrimSpace(result.Stdout), 10, 64)
		if err != nil {
			return err
		}
		panelFree, err := panelBackupAvailableBytes(req.RepositoryDir)
		if err != nil {
			return err
		}
		required := backupRequiredSpace(inspection.EstimatedBytes)
		if sourceFree < required || panelFree < required {
			return mysqlOperationError(MySQLBackupSpaceInsufficient)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := step(6, func() error {
		var err error
		state.paths, err = repository.Prepare(backupID)
		if err != nil {
			return err
		}
		state.record.Path = state.paths.Archive
		state.record, err = data.SaveAppBackup(state.record)
		if err != nil {
			return err
		}
		_, err = s.remote.Run(ctx, state.server, prepareDumpDirectoryCommand(state.remoteWork))
		return err
	}); err != nil {
		return err
	}
	if err := step(7, func() error {
		var err error
		script, err = RenderLogicalBackupScript(LogicalBackupScriptOptions{TaskID: run.TaskID, Threads: parameters.Threads, MaxRateMBps: parameters.MaxRateMBps})
		if err != nil {
			return err
		}
		_, err = s.remote.Run(ctx, state.server, dryRunBackupCommand(state.remoteWork, parameters.Threads, parameters.MaxRateMBps))
		return err
	}); err != nil {
		return err
	}
	if err := step(8, func() error {
		local, err := installerkit.WriteTempScript("aifar-mysql-backup-*.sh", script)
		if err != nil {
			return err
		}
		defer os.Remove(local)
		remoteScript := path.Join(state.remoteWork, "logical-backup.sh")
		if err := s.remote.UploadFile(ctx, state.server, local, remoteScript, 0o700); err != nil {
			return err
		}
		_, err = s.remote.Run(ctx, state.server, "sh "+installerkit.ShellQuote(remoteScript))
		return err
	}); err != nil {
		return err
	}
	if err := step(9, func() error {
		manifest := BackupManifest{
			BackupID: backupID, App: "mysql", Topology: "standalone", InstanceID: req.Instance.ID,
			SourceServerID: state.server.ID, SourceEndpoint: net.JoinHostPort(state.server.Host, strconv.Itoa(instancePort(req.Instance))),
			SourceServerUUID: inspection.ServerUUID, MySQLVersion: inspection.MySQLVersion, MySQLShellVersion: inspection.MySQLShellVersion,
			Schemas: inspection.Schemas, ExcludedSchemas: append([]string(nil), fixedSystemSchemas...), Consistent: true,
			GTIDExecuted: inspection.GTIDExecuted, CreatedAt: state.record.CreatedAt, TaskID: run.TaskID,
		}
		var err error
		state.manifest, err = CanonicalBackupManifestJSON(manifest)
		return err
	}); err != nil {
		return err
	}
	if err := step(10, func() error {
		result, err := s.remote.Run(ctx, state.server, packageBackupCommand(state.remoteWork))
		if err != nil {
			return err
		}
		state.archiveSize, state.archiveSHA, err = parsePackagedArchive(result.Stdout)
		return err
	}); err != nil {
		return err
	}
	if err := step(11, func() error {
		result, err := downloader.DownloadFile(ctx, state.server, path.Join(state.remoteWork, "dump.tar"), state.paths.PartialArchive, 0o600)
		if err != nil {
			return mysqlOperationError(MySQLBackupTransferFailed)
		}
		if result.Size != state.archiveSize {
			return mysqlOperationError(MySQLBackupChecksumMismatch)
		}
		if result.SHA256 != state.archiveSHA {
			return mysqlOperationError(MySQLBackupChecksumMismatch)
		}
		return nil
	}); err != nil {
		return err
	}
	if err := step(12, func() error {
		if err := repository.Commit(state.paths, state.manifest, state.archiveSHA, state.archiveSize); err != nil {
			return mysqlOperationError(MySQLBackupChecksumMismatch)
		}
		state.committed = true
		return nil
	}); err != nil {
		return err
	}
	if err := step(13, func() error {
		state.record.Path = state.paths.Archive
		state.record.Checksum = state.archiveSHA
		state.record.Size = state.archiveSize
		state.record.Metadata = backupMetadataWithInspection(parameters, "committed", inspection)
		saved, err := data.SaveAppBackup(state.record)
		if err != nil {
			return err
		}
		state.record = saved
		return nil
	}); err != nil {
		return err
	}
	if err := step(14, func() error {
		state.record.Status = "success"
		state.record.CompletedAt = time.Now().UTC()
		state.record.Metadata = backupMetadataWithInspection(parameters, "success", inspection)
		saved, err := data.SaveAppBackup(state.record)
		if err != nil {
			return err
		}
		state.record = saved
		items, err := data.ListAppBackupsForInstances([]string{req.Instance.ID}, false)
		if err != nil {
			if run.Log != nil {
				run.Log.Error("%s", copy.RetentionCleanupFailed)
			}
			return nil
		}
		current := state.record
		foundCurrent := false
		for index := range items {
			if items[index].ID == current.ID {
				items[index] = current
				foundCurrent = true
				break
			}
		}
		if !foundCurrent {
			items = append(items, current)
		}
		candidates := repository.RetentionCandidates(items, parameters.KeepLast)
		if len(candidates) > 0 && run.Log != nil {
			run.Log.Info(copy.RetentionSelected, len(candidates))
		}
		for _, candidate := range candidates {
			if candidate.App != "mysql" || candidate.InstanceID != req.Instance.ID || candidate.ServerID != req.Instance.ServerID || candidate.BackupType != "logical-full" || instanceTopology(req.Instance) != "standalone" {
				if run.Log != nil {
					run.Log.Error("%s", copy.RetentionCleanupFailed)
				}
				continue
			}
			freshInstance, instanceErr := data.GetAppInstance(req.Instance.ID)
			if instanceErr != nil || !sameStandaloneBackupOwner(req.Instance, freshInstance, candidate) {
				if run.Log != nil {
					run.Log.Error("%s", copy.RetentionCleanupFailed)
				}
				continue
			}
			fresh, freshErr := data.GetAppBackup(candidate.ID)
			if freshErr != nil || !sameBackupRecord(candidate, fresh) {
				if run.Log != nil {
					run.Log.Error("%s", copy.RetentionCleanupFailed)
				}
				continue
			}
			deletion, deleteErr := repository.BeginDelete(fresh)
			if deleteErr != nil {
				if run.Log != nil {
					run.Log.Error("%s", copy.RetentionCleanupFailed)
				}
				continue
			}
			if _, markErr := data.MarkAppBackupDeleted(fresh.ID, time.Now().UTC()); markErr != nil {
				_ = deletion.Rollback()
				if run.Log != nil {
					run.Log.Error("%s", copy.RetentionCleanupFailed)
				}
				continue
			}
			if finalizeErr := deletion.Finalize(); finalizeErr != nil && run.Log != nil {
				run.Log.Error("%s", copy.RetentionCleanupFailed)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := step(15, func() error {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, err := s.remote.Run(cleanupCtx, state.server, cleanupBackupCommand(state.remoteWork))
		if err == nil {
			state.remoteClean = true
			return nil
		}
		if run.Log != nil {
			run.Log.Error("%s", copy.RemoteCleanupFailed)
		}
		return nil
	}); err != nil {
		return err
	}

	installflow.FinishTarget(recorder, req.Instance.ServerID, "success", "")
	return nil
}

func sameBackupRecord(left, right store.AppBackup) bool {
	return left.ID == right.ID && left.App == right.App && left.InstanceID == right.InstanceID && left.ServerID == right.ServerID &&
		left.BackupType == right.BackupType && left.Status == "success" && right.Status == "success" && sameBackupPath(left.Path, right.Path) &&
		left.Checksum == right.Checksum && left.Size == right.Size
}

func sameStandaloneBackupOwner(expected, current store.AppInstance, backup store.AppBackup) bool {
	return current.ID == expected.ID && current.ID == backup.InstanceID && current.App == "mysql" && instanceTopology(current) == "standalone" &&
		current.ServerID == expected.ServerID && current.ServerID == backup.ServerID
}

func (s Service) retainUnavailableDownloaderFailure(req registry.BackupRequest, run registry.RunContext, data backupStore) error {
	record := store.AppBackup{ID: store.NewID("backup"), App: "mysql", InstanceID: req.Instance.ID, ServerID: req.Instance.ServerID, BackupType: "logical-full", Status: "failed", TaskID: run.TaskID, Metadata: backupMetadata(normalizeStandaloneBackupParameters(req), "failed"), CreatedAt: time.Now().UTC(), CompletedAt: time.Now().UTC()}
	_, _ = data.SaveAppBackup(record)
	return mysqlOperationError(MySQLBackupTransferFailed)
}

func standaloneBackupStepDefinitions(lang string) []installflow.Step {
	names := []string{"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql", "check-backup-space", "prepare-workdir", "dry-run-dump", "dump-instance", "build-manifest", "package-backup", "transfer-backup", "verify-checksum", "record-backup", "apply-retention", "cleanup-workdir"}
	copy := BackupCopyFor(lang)
	out := make([]installflow.Step, len(names))
	for index := range names {
		out[index] = installflow.Step{Name: names[index], Title: copy.StepTitles[names[index]]}
	}
	return out
}

func backupDisplayError(lang string, err error) error {
	if err == nil {
		return nil
	}
	type joinedErrors interface{ Unwrap() []error }
	if joined, ok := err.(joinedErrors); ok {
		parts := make([]string, 0, len(joined.Unwrap()))
		for _, cause := range joined.Unwrap() {
			if display := backupDisplayError(lang, cause); display != nil && strings.TrimSpace(display.Error()) != "" {
				parts = append(parts, display.Error())
			}
		}
		if len(parts) > 0 {
			return errors.New(strings.Join(parts, "\n"))
		}
	}
	var stable interface{ StableCode() string }
	if errors.As(err, &stable) && stable.StableCode() != "" {
		return errors.New(MySQLBackupErrorText(lang, stable.StableCode()))
	}
	return err
}

func normalizeStandaloneBackupParameters(req registry.BackupRequest) backupParameters {
	threads := intParameter(req.Parameters, "threads")
	if threads <= 0 {
		threads = defaultBackupThreads
	}
	if threads > maxLogicalThreads {
		threads = maxLogicalThreads
	}
	rate := intParameter(req.Parameters, "maxRateMBps")
	if rate < 0 {
		rate = 0
	}
	if rate > maxLogicalRateMBps {
		rate = maxLogicalRateMBps
	}
	keep := req.KeepLast
	if keep < 1 {
		keep = 1
	}
	if keep > maxBackupRetentionCount {
		keep = maxBackupRetentionCount
	}
	name := strings.TrimSpace(fmt.Sprint(req.Parameters["name"]))
	if name == "<nil>" {
		name = ""
	}
	if len(name) > 128 {
		name = name[:128]
	}
	return backupParameters{Name: name, Threads: threads, MaxRateMBps: rate, KeepLast: keep}
}

func intParameter(values map[string]any, key string) int {
	value, ok := values[key]
	if !ok {
		return 0
	}
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		if typed > math.MaxInt || typed < math.MinInt {
			return 0
		}
		return int(typed)
	case float64:
		if typed != math.Trunc(typed) || typed > math.MaxInt || typed < math.MinInt {
			return 0
		}
		return int(typed)
	default:
		return 0
	}
}

func backupMetadata(parameters backupParameters, phase string) string {
	data, _ := json.Marshal(map[string]any{"name": parameters.Name, "threads": parameters.Threads, "maxRateMBps": parameters.MaxRateMBps, "keepLast": parameters.KeepLast, "phase": phase})
	return string(data)
}

func backupMetadataWithInspection(parameters backupParameters, phase string, inspection mysqlBackupInspection) string {
	data, _ := json.Marshal(map[string]any{"name": parameters.Name, "threads": parameters.Threads, "maxRateMBps": parameters.MaxRateMBps, "keepLast": parameters.KeepLast, "phase": phase, "mysqlVersion": inspection.MySQLVersion, "mysqlShellVersion": inspection.MySQLShellVersion, "schemas": inspection.Schemas})
	return string(data)
}

func writeMySQLSecretContext(credential store.Credential, port int) (string, error) {
	username := strings.TrimSpace(credential.Username)
	password := credential.Secret["password"]
	if username == "" {
		username = "root"
	}
	quote := func(value string) (string, error) {
		if strings.ContainsAny(value, "\x00\r\n") {
			return "", errors.New("invalid MySQL option value")
		}
		value = strings.ReplaceAll(value, `\`, `\\`)
		value = strings.ReplaceAll(value, `"`, `\"`)
		return `"` + value + `"`, nil
	}
	userValue, err := quote(username)
	if err != nil {
		return "", err
	}
	passwordValue, err := quote(password)
	if err != nil || password == "" {
		return "", errors.New("missing MySQL password")
	}
	file, err := createMySQLSecretContextFile()
	if err != nil {
		return "", err
	}
	name := file.Name()
	if chmodErr := file.Chmod(0o600); chmodErr != nil {
		return "", errors.Join(chmodErr, cleanupMySQLSecretContext(file, name, false))
	}
	_, err = fmt.Fprintf(file, "[client]\nuser=%s\npassword=%s\nhost=127.0.0.1\nport=%d\n", userValue, passwordValue, port)
	if err != nil {
		return "", errors.Join(err, cleanupMySQLSecretContext(file, name, false))
	}
	closeErr := file.Close()
	if closeErr != nil {
		return "", errors.Join(closeErr, cleanupMySQLSecretContext(file, name, true))
	}
	return name, nil
}

func cleanupMySQLSecretContext(file mysqlSecretContextFile, name string, closeAlreadyAttempted bool) error {
	var cleanupErrors []error
	if closeAlreadyAttempted {
		if err := file.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, errors.New("failed to close MySQL secret context during cleanup"))
		}
	} else if err := file.Close(); err != nil {
		cleanupErrors = append(cleanupErrors, errors.New("failed to close MySQL secret context during cleanup"))
		if retryErr := file.Close(); retryErr != nil {
			cleanupErrors = append(cleanupErrors, errors.New("failed to close MySQL secret context after cleanup retry"))
		}
	}
	if err := os.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		cleanupErrors = append(cleanupErrors, errors.New("failed to remove MySQL secret context during cleanup"))
	}
	return errors.Join(cleanupErrors...)
}

func bootstrapBackupWorkCommand(work string) string {
	root := path.Dir(work)
	return "set -eu; install -d -m 0700 " + installerkit.ShellQuote(root) + "; test ! -L " + installerkit.ShellQuote(root) + "; install -d -m 0700 " + installerkit.ShellQuote(work) + "; test ! -L " + installerkit.ShellQuote(work)
}

func inspectBackupCommand(work string, port int) string {
	mysqlsh := path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")
	query := "SELECT '__AIFAR_INFO__',@@version,@@server_uuid,@@GLOBAL.gtid_executed,COALESCE((SELECT SUM(data_length+index_length) FROM information_schema.tables WHERE table_schema NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys')),0); SELECT '__AIFAR_SCHEMA__',schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys') ORDER BY schema_name;"
	return "set -eu; test -x " + installerkit.ShellQuote(mysqlsh) + "; printf '__AIFAR_SHELL__\\t'; " + installerkit.ShellQuote(mysqlsh) + " --version | awk '{print $NF}'; " + installerkit.ShellQuote(mysqlsh) + " --defaults-file=" + installerkit.ShellQuote(path.Join(work, "secret-context.cnf")) + " --sql --raw --skip-column-names --host=127.0.0.1 --port=" + strconv.Itoa(port) + " --execute " + installerkit.ShellQuote(query)
}

func parseMySQLBackupInspection(output string) (mysqlBackupInspection, error) {
	var result mysqlBackupInspection
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "\t")
		switch {
		case len(parts) == 2 && parts[0] == "__AIFAR_SHELL__":
			result.MySQLShellVersion = strings.TrimSpace(parts[1])
		case len(parts) >= 6 && parts[0] == "__AIFAR_INFO__":
			result.MySQLVersion, result.ServerUUID, result.GTIDExecuted, result.MySQLShellVersion = parts[1], parts[2], parts[3], parts[4]
			result.EstimatedBytes, _ = strconv.ParseInt(parts[5], 10, 64)
		case len(parts) >= 5 && parts[0] == "__AIFAR_INFO__":
			result.MySQLVersion, result.ServerUUID, result.GTIDExecuted = parts[1], parts[2], parts[3]
			result.EstimatedBytes, _ = strconv.ParseInt(parts[4], 10, 64)
		case len(parts) == 2 && parts[0] == "__AIFAR_SCHEMA__":
			result.Schemas = append(result.Schemas, parts[1])
		}
	}
	if scanner.Err() != nil {
		return result, scanner.Err()
	}
	manifest := BackupManifest{App: "mysql", Topology: "standalone", Schemas: result.Schemas, ExcludedSchemas: append([]string(nil), fixedSystemSchemas...)}
	if _, err := normalizeBusinessSchemas(manifest.Schemas); err != nil {
		return result, err
	}
	if !canonicalRequired(result.MySQLVersion, result.MySQLShellVersion, result.ServerUUID) || result.EstimatedBytes <= 0 {
		return result, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	return result, nil
}

func sourceSpaceCommand(work string) string {
	return "df -Pk " + installerkit.ShellQuote(work) + " | awk 'NR==2 {printf \"%.0f\\n\", $4*1024}'"
}

const logicalBackupDryRunHelper = `import os
import stat
import subprocess
import sys

work, mysqlsh, threads, max_rate = sys.argv[1:]
def checked(path, directory, mode=None):
    flags = os.O_RDONLY | os.O_NOFOLLOW
    if directory:
        flags |= os.O_DIRECTORY
    fd = os.open(path, flags)
    details = os.fstat(fd)
    valid = stat.S_ISDIR(details.st_mode) if directory else stat.S_ISREG(details.st_mode)
    if not valid or details.st_uid != os.geteuid() or (mode is not None and stat.S_IMODE(details.st_mode) != mode):
        os.close(fd)
        raise SystemExit(1)
    return fd
dump_fd = checked(os.path.join(work, "dump"), True, 0o700)
secret_fd = checked(os.path.join(work, "secret-context.cnf"), False, 0o600)
mysqlsh_fd = checked(mysqlsh, False)
options = 'consistent: true, threads: %s, compression: "zstd", users: false, showProgress: false, dryRun: true, excludeSchemas: ["information_schema", "mysql", "mysql_innodb_cluster_metadata", "performance_schema", "sys"]' % threads
if int(max_rate) > 0:
    options += ', maxRate: "%sM"' % max_rate
js = 'util.dumpInstance("/proc/self/fd/%d", {%s});' % (dump_fd, options)
js_fd = os.memfd_create("aifar-logical-backup-dry-run", os.MFD_CLOEXEC)
os.fchmod(js_fd, 0o600)
os.write(js_fd, js.encode("utf-8"))
completed = subprocess.run(["/proc/self/fd/%d" % mysqlsh_fd, "--defaults-file=/proc/self/fd/%d" % secret_fd, "--js", "--file", "/proc/self/fd/%d" % js_fd], pass_fds=(dump_fd, secret_fd, mysqlsh_fd, js_fd), check=False)
raise SystemExit(completed.returncode)
`

func dryRunBackupCommand(work string, threads, maxRateMBps int) string {
	mysqlsh := path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")
	return "python3 -c " + installerkit.ShellQuote(logicalBackupDryRunHelper) + " " + installerkit.ShellQuote(work) + " " + installerkit.ShellQuote(mysqlsh) + " " + strconv.Itoa(threads) + " " + strconv.Itoa(maxRateMBps)
}
func prepareDumpDirectoryCommand(work string) string {
	return "set -eu; test -d " + installerkit.ShellQuote(work) + "; test ! -e " + installerkit.ShellQuote(path.Join(work, "dump")) + "; install -d -m 0700 " + installerkit.ShellQuote(path.Join(work, "dump"))
}
func packageBackupCommand(work string) string {
	archive := path.Join(work, "dump.tar")
	return "set -eu; test ! -e " + installerkit.ShellQuote(archive) + "; tar -C " + installerkit.ShellQuote(work) + " -cf " + installerkit.ShellQuote(archive) + " dump; size=$(stat -c %s " + installerkit.ShellQuote(archive) + "); sha=$(sha256sum " + installerkit.ShellQuote(archive) + " | awk '{print $1}'); printf '__AIFAR_ARCHIVE__\\t%s\\t%s\\n' \"$size\" \"$sha\""
}
func cleanupBackupCommand(work string) string {
	return "set -eu; case " + installerkit.ShellQuote(work) + " in /aifar/apps/mysql/_backup/*) rm -rf -- " + installerkit.ShellQuote(work) + ";; *) exit 1;; esac"
}

func parsePackagedArchive(output string) (int64, string, error) {
	for _, line := range strings.Split(output, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 3 || parts[0] != "__AIFAR_ARCHIVE__" {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size <= 0 || len(parts[2]) != 64 {
			return 0, "", mysqlOperationError(MySQLBackupChecksumMismatch)
		}
		for _, char := range parts[2] {
			if !strings.ContainsRune("0123456789abcdef", char) {
				return 0, "", mysqlOperationError(MySQLBackupChecksumMismatch)
			}
		}
		return size, parts[2], nil
	}
	return 0, "", mysqlOperationError(MySQLBackupChecksumMismatch)
}

func backupRequiredSpace(estimated int64) int64 {
	if estimated <= 0 || estimated > (math.MaxInt64-backupSpaceSafetyBytes)/2 {
		return math.MaxInt64
	}
	return estimated*2 + backupSpaceSafetyBytes
}

func panelFilesystemAvailableBytes(target string) (int64, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "windows" {
		volume := filepath.VolumeName(abs)
		drive := strings.TrimSuffix(volume, ":")
		output, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "[int64][System.IO.DriveInfo]::new('"+strings.ReplaceAll(drive, "'", "''")+"').AvailableFreeSpace").Output()
		if err != nil {
			return 0, err
		}
		return strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64)
	}
	return panelFilesystemAvailableBytesLinux(abs)
}

func panelFilesystemAvailableBytesLinux(target string) (int64, error) {
	abs, err := filepath.Abs(target)
	if err != nil {
		return 0, err
	}
	abs = filepath.Clean(abs)
	info, err := os.Lstat(abs)
	if err != nil {
		return 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return 0, errors.New("panel backup repository is not a controlled directory")
	}
	output, err := panelDFOutput(abs)
	if err != nil {
		return 0, err
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) < 2 {
		return 0, errors.New("panel filesystem space unavailable")
	}
	fields := strings.Fields(lines[len(lines)-1])
	if len(fields) < 4 {
		return 0, errors.New("panel filesystem space unavailable")
	}
	blocks, err := strconv.ParseInt(fields[3], 10, 64)
	if err != nil || blocks > math.MaxInt64/1024 {
		return 0, errors.New("panel filesystem space unavailable")
	}
	return blocks * 1024, nil
}

var _ = adapter.DownloadResult{}
