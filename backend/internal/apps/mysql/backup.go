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

var standaloneBackupStepNames = []string{
	"load-instance", "acquire-instance-lock", "resolve-credential", "inspect-mysql", "check-backup-space",
	"prepare-workdir", "dry-run-dump", "dump-instance", "build-manifest", "package-backup", "transfer-backup",
	"verify-checksum", "record-backup", "apply-retention", "cleanup-workdir",
}

type backupStore interface {
	GetBoundCredential(appInstanceID, purpose string, includeSecret bool) (store.Credential, error)
	GetAppInstance(id string) (store.AppInstance, error)
	GetAppBackup(id string) (store.AppBackup, error)
	SaveAppBackup(store.AppBackup) (store.AppBackup, error)
	ListAppBackupsForInstances(instanceIDs []string, includeDeleted bool) ([]store.AppBackup, error)
	MarkAppBackupDeleted(id string, completedAt time.Time) (store.AppBackup, error)
}

type clusterBackupVerificationStore interface {
	backupStore
	GetServer(id string, includeSecret bool) (store.Server, error)
	GetAppCluster(id string) (store.AppCluster, error)
	ListAppClusterMembers(clusterID string) ([]store.AppClusterMember, error)
	ListAppInstances() ([]store.AppInstance, error)
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
	published   bool
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
	if req.Instance.App != "mysql" {
		return nil, mysqlOperationError(MySQLBackupUnsupportedTopology)
	}
	if instanceTopology(req.Instance) == "innodb-cluster" {
		return m.planInnoDBClusterBackup(ctx, req)
	}
	if instanceTopology(req.Instance) != "standalone" {
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
	if instanceTopology(req.Instance) == "innodb-cluster" {
		return m.service.backupInnoDBCluster(ctx, req.Clone(), run)
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
		if err != nil || instance.App != "mysql" || instance.ID != backup.InstanceID || instance.ServerID != backup.ServerID || backup.BackupType != "logical-full" {
			return localizedMySQLOperationError(language, MySQLBackupVerifyNotAllowed)
		}
		switch instanceTopology(instance) {
		case "standalone":
		case "innodb-cluster":
			clusterID := clusterIDFromInstance(instance)
			if clusterID == "" || clusterIDFromBackup(backup) != clusterID {
				return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
			}
			clusterData, ok := m.service.store.(clusterBackupVerificationStore)
			if !ok || validateClusterBackupVerificationOwner(clusterData, backup, instance) != nil {
				return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
			}
		default:
			return localizedMySQLOperationError(language, MySQLBackupVerifyNotAllowed)
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

func validateClusterBackupVerificationOwner(data clusterBackupVerificationStore, backup store.AppBackup, representative store.AppInstance) error {
	clusterID := clusterIDFromInstance(representative)
	cluster, err := data.GetAppCluster(clusterID)
	if err != nil || cluster.ID != clusterID || cluster.App != "mysql" || cluster.Topology != "innodb-cluster" {
		return errors.New("invalid authoritative MySQL cluster")
	}
	members, err := data.ListAppClusterMembers(clusterID)
	if err != nil || len(members) != 3 {
		return errors.New("invalid authoritative MySQL cluster membership")
	}
	instances, err := data.ListAppInstances()
	if err != nil {
		return err
	}
	byID := make(map[string]store.AppInstance, len(instances))
	for _, instance := range instances {
		byID[instance.ID] = instance
	}
	seenInstances := make(map[string]struct{}, len(members))
	seenServers := make(map[string]struct{}, len(members))
	representativeFound := false
	for _, member := range members {
		if member.ClusterID != clusterID || member.InstanceID == "" || member.ServerID == "" {
			return errors.New("invalid authoritative MySQL cluster member")
		}
		if _, duplicate := seenInstances[member.InstanceID]; duplicate {
			return errors.New("duplicate authoritative MySQL cluster instance")
		}
		if _, duplicate := seenServers[member.ServerID]; duplicate {
			return errors.New("duplicate authoritative MySQL cluster server")
		}
		instance, ok := byID[member.InstanceID]
		if !ok || instance.App != "mysql" || instance.ServerID != member.ServerID || instanceTopology(instance) != "innodb-cluster" || clusterIDFromInstance(instance) != clusterID {
			return errors.New("authoritative MySQL cluster member drifted")
		}
		server, err := data.GetServer(member.ServerID, false)
		if err != nil || server.ID != member.ServerID || strings.TrimSpace(server.Host) == "" {
			return errors.New("authoritative MySQL cluster server drifted")
		}
		seenInstances[member.InstanceID] = struct{}{}
		seenServers[member.ServerID] = struct{}{}
		if member.InstanceID == representative.ID && member.ServerID == backup.ServerID {
			representativeFound = true
		}
	}
	if !representativeFound {
		return errors.New("backup owner is outside authoritative MySQL cluster")
	}
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

type standaloneBackupExecution struct {
	backupType           string
	recordPlan           bool
	retention            bool
	topology             string
	clusterID            string
	members              []ClusterMemberRef
	routers              []RouterRef
	retentionInstanceIDs []string
	progressTarget       string
}

func (s Service) backupStandalone(ctx context.Context, req registry.BackupRequest, run registry.RunContext) error {
	if err := s.requireNoMySQLMaintenance(req.Instance, req.Language); err != nil {
		return err
	}
	if err := s.requireNoMySQLReconciliation(req.Instance, req.Language); err != nil {
		return err
	}
	return s.backupStandaloneCore(ctx, req, run, standaloneBackupExecution{backupType: "logical-full", recordPlan: true, retention: true})
}

func (s Service) backupStandaloneCore(ctx context.Context, req registry.BackupRequest, run registry.RunContext, execution standaloneBackupExecution) (retErr error) {
	if execution.backupType != "logical-full" && execution.backupType != "pre-restore" {
		return mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	topology := execution.topology
	if topology == "" {
		topology = "standalone"
	}
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
			BackupType: execution.backupType, Status: "pending", TaskID: run.TaskID,
			Metadata: backupMetadataForExecution(parameters, "pending", mysqlBackupInspection{}, execution), CreatedAt: time.Now().UTC(),
		},
		repository: repository, backupStore: data, remote: s.remote,
		remoteWork: mysqlBackupWorkDir(run.TaskID), log: run.Log,
	}
	progressTarget := execution.progressTarget
	if progressTarget == "" {
		progressTarget = req.Instance.ServerID
	}
	state.record, err = data.SaveAppBackup(state.record)
	if err != nil {
		return err
	}
	recorder, _ := run.Log.(installflow.Recorder)
	if !execution.recordPlan {
		recorder = nil
	}
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
		if retErr == nil || state.published {
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
		state.record.Metadata = backupMetadataForExecution(parameters, "failed", mysqlBackupInspection{}, execution)
		_, _ = data.SaveAppBackup(state.record)
		if targetStarted {
			installflow.FinishTarget(recorder, progressTarget, "failed", backupDisplayError(req.Language, retErr).Error())
		}
	}()

	installflow.StartTarget(recorder, progressTarget)
	targetStarted = true
	steps := standaloneBackupStepDefinitions(req.Language)
	runner := installflow.Runner{
		Log: run.Log, Recorder: recorder, Target: progressTarget, Steps: steps,
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
	var verification *BackupVerification
	var script string
	if err := step(1, func() error {
		if req.Instance.App != "mysql" || instanceTopology(req.Instance) != topology {
			return mysqlOperationError(MySQLBackupUnsupportedTopology)
		}
		server, err := s.store.GetServer(req.Instance.ServerID, true)
		if err != nil {
			return err
		}
		state.server = server
		state.record.Status = "running"
		state.record.Metadata = backupMetadataForExecution(parameters, "running", mysqlBackupInspection{}, execution)
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
		if err := uploadMySQLCredentialContext(ctx, s.remote, state.server, credential, instancePort(req.Instance), path.Join(state.remoteWork, "secret-context.cnf")); err != nil {
			return mysqlOperationError(MySQLCredentialUnavailable)
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
		result, err := s.remote.Run(ctx, state.server, inspectDumpVerificationCommand(state.remoteWork))
		if err != nil {
			return err
		}
		verification, err = parseDumpVerification(result.Stdout, inspection.Schemas)
		if err != nil {
			return err
		}
		manifest := BackupManifest{
			ManifestVersion: 2,
			BackupID:        backupID, App: "mysql", Topology: topology, InstanceID: req.Instance.ID, ClusterID: execution.clusterID,
			SourceServerID: state.server.ID, SourceEndpoint: net.JoinHostPort(state.server.Host, strconv.Itoa(instancePort(req.Instance))),
			SourceServerUUID: inspection.ServerUUID, MySQLVersion: inspection.MySQLVersion, MySQLShellVersion: inspection.MySQLShellVersion,
			Schemas: inspection.Schemas, ExcludedSchemas: append([]string(nil), fixedSystemSchemas...), Consistent: true,
			GTIDExecuted: inspection.GTIDExecuted, CreatedAt: state.record.CreatedAt, TaskID: run.TaskID,
			Verification: verification, Members: execution.members, Routers: execution.routers,
		}
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
		metadata, err := backupMetadataFromVerifiedManifest(parameters, "committed", state.manifest)
		if err != nil {
			return err
		}
		state.record.Metadata = metadata
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
		metadata, err := backupMetadataFromVerifiedManifest(parameters, "success", state.manifest)
		if err != nil {
			return err
		}
		state.record.Metadata = metadata
		saved, err := data.SaveAppBackup(state.record)
		if err != nil {
			return err
		}
		state.record = saved
		state.published = true
		if !execution.retention {
			return nil
		}
		instanceIDs := execution.retentionInstanceIDs
		if len(instanceIDs) == 0 {
			instanceIDs = []string{req.Instance.ID}
		}
		items, err := data.ListAppBackupsForInstances(instanceIDs, false)
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
		seenCandidates := map[string]bool{}
		for _, candidate := range candidates {
			if seenCandidates[candidate.ID] {
				continue
			}
			seenCandidates[candidate.ID] = true
			if candidate.App != "mysql" || candidate.BackupType != "logical-full" || (topology == "standalone" && (candidate.InstanceID != req.Instance.ID || candidate.ServerID != req.Instance.ServerID)) {
				if run.Log != nil {
					run.Log.Error("%s", copy.RetentionCleanupFailed)
				}
				continue
			}
			freshInstance, instanceErr := data.GetAppInstance(candidate.InstanceID)
			owned := instanceErr == nil
			if topology == "standalone" {
				owned = owned && sameStandaloneBackupOwner(req.Instance, freshInstance, candidate)
			} else {
				owned = owned && instanceTopology(freshInstance) == "innodb-cluster" && clusterIDFromInstance(freshInstance) == execution.clusterID && candidate.ServerID == freshInstance.ServerID
			}
			if !owned {
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
		return errors.New(copy.RemoteCleanupFailed)
	}); err != nil {
		return err
	}

	installflow.FinishTarget(recorder, progressTarget, "success", "")
	return nil
}

func parseDumpVerification(output string, schemas []string) (*BackupVerification, error) {
	const prefix = "__AIFAR_VERIFICATION__"
	var encoded string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, prefix) {
			if encoded != "" {
				return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
			}
			encoded = strings.TrimPrefix(line, prefix)
		}
	}
	if encoded == "" {
		return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var verification BackupVerification
	if err := decoder.Decode(&verification); err != nil {
		return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	normalized, err := normalizeBackupVerification(&verification, schemas)
	if err != nil {
		return nil, err
	}
	return normalized, nil
}

const dumpVerificationHelper = `
import hashlib, json, pathlib, re, sys
root = pathlib.Path(sys.argv[1]) / "dump"
max_uint64 = 18446744073709551615
name_pattern = re.compile(r"^[A-Za-z_][A-Za-z0-9_]{0,63}$")
system_schemas = {"information_schema", "mysql", "mysql_innodb_cluster_metadata", "performance_schema", "sys"}
def reject(): raise SystemExit(1)
def unique_object(pairs):
  value = {}
  for key, item in pairs:
    if key in value: reject()
    value[key] = item
  return value
def load_object(path):
  if path.is_symlink() or not path.is_file(): reject()
  try:
    text = path.read_text(encoding="utf-8")
    start = len(text) - len(text.lstrip())
    value, end = json.JSONDecoder(object_pairs_hook=unique_object).raw_decode(text, start)
    if text[end:].strip() or type(value) is not dict: reject()
    return value
  except SystemExit: raise
  except Exception: reject()
def uint64(value):
  if type(value) is not int or value < 0 or value > max_uint64: reject()
  return value
def safe_relative(value):
  if type(value) is not str or not value or "\\" in value or "\0" in value or value.startswith("/"): reject()
  parts = value.split("/")
  if any(part in ("", ".", "..") for part in parts): reject()
  try: value.encode("utf-8")
  except Exception: reject()
  return value
def safe_basename(value):
  safe_relative(value)
  if "/" in value: reject()
  return value
def name_list(value):
  if type(value) is not list or any(type(item) is not str or not name_pattern.fullmatch(item) for item in value): reject()
  folded = [item.casefold() for item in value]
  if len(set(folded)) != len(folded): reject()
  return value
def basename_map(value, expected):
  if type(value) is not dict or set(value) != set(expected): reject()
  basenames = []
  for key in expected:
    if type(key) is not str: reject()
    basenames.append(safe_basename(value[key]))
  if len(set(basenames)) != len(basenames): reject()
  return value
if root.is_symlink() or not root.is_dir(): reject()
inventory = []
inventory_paths = set()
for item in sorted(root.rglob("*"), key=lambda value: value.relative_to(root).as_posix().encode("utf-8")):
  if item.is_symlink() or not item.is_file():
    if item.is_dir() and not item.is_symlink(): continue
    reject()
  relative = safe_relative(item.relative_to(root).as_posix())
  if relative in inventory_paths: reject()
  inventory_paths.add(relative)
  digest = hashlib.sha256()
  size = 0
  with item.open("rb") as stream:
    for block in iter(lambda: stream.read(1024 * 1024), b""):
      size += len(block); digest.update(block)
  inventory.append({"path": relative, "size": size, "sha256": digest.hexdigest()})
if not inventory: reject()
completion = load_object(root / "@.done.json")
if type(completion.get("end")) is not str or not completion["end"].strip(): reject()
uint64(completion.get("dataBytes"))
table_bytes = completion.get("tableDataBytes")
chunk_bytes = completion.get("chunkFileBytes")
if type(table_bytes) is not dict or type(chunk_bytes) is not dict: reject()
for schema_name, table_values in table_bytes.items():
  if type(schema_name) is not str or type(table_values) is not dict: reject()
  for table_name, size in table_values.items():
    if type(table_name) is not str: reject()
    uint64(size)
for filename, size in chunk_bytes.items():
  safe_relative(filename); uint64(size)
top = load_object(root / "@.json")
if top.get("version") != "2.0.1" or top.get("origin") != "dumpInstance" or top.get("consistent") is not True: reject()
schema_names = name_list(top.get("schemas"))
if not schema_names or any(name.casefold() in system_schemas for name in schema_names): reject()
schema_basenames = basename_map(top.get("basenames"), schema_names)
catalog = set()
metadata_paths = set()
control_paths = {"@.json", "@.done.json"}
schemas = []
def claim_metadata(relative):
  safe_relative(relative)
  if relative in control_paths or relative in metadata_paths or relative not in inventory_paths: reject()
  metadata_paths.add(relative)
for schema_name in sorted(schema_names, key=lambda value: value.encode("utf-8")):
  schema_relative = schema_basenames[schema_name] + ".json"
  claim_metadata(schema_relative)
  schema_metadata = load_object(root / schema_relative)
  if schema_metadata.get("schema") != schema_name or schema_metadata.get("includesDdl") is not True or schema_metadata.get("includesViewsDdl") is not True or schema_metadata.get("includesData") is not True: reject()
  tables = name_list(schema_metadata.get("tables"))
  views = name_list(schema_metadata.get("views"))
  if {item.casefold() for item in tables} & {item.casefold() for item in views}: reject()
  object_names = tables + views
  object_basenames = basename_map(schema_metadata.get("basenames"), object_names)
  table_output = []
  for table_name in sorted(tables, key=lambda value: value.encode("utf-8")):
    table_relative = object_basenames[table_name] + ".json"
    claim_metadata(table_relative)
    table_metadata = load_object(root / table_relative)
    options = table_metadata.get("options")
    if type(options) is not dict or options.get("schema") != schema_name or options.get("table") != table_name: reject()
    if type(options.get("columns")) is not list or any(type(column) is not str for column in options["columns"]): reject()
    if type(table_metadata.get("primaryIndex")) is not list or any(type(column) is not str for column in table_metadata["primaryIndex"]): reject()
    if table_metadata.get("includesData") is not True or table_metadata.get("includesDdl") is not True or table_metadata.get("extension") != "tsv.zst" or table_metadata.get("compression") != "zstd": reject()
    key = (schema_name, table_name)
    if key in catalog: reject()
    catalog.add(key)
    table_output.append({"name": table_name})
  schemas.append({"name": schema_name, "tableCount": len(table_output), "tables": table_output})
actual_metadata = {path for path in inventory_paths if path.endswith(".json")}
if actual_metadata != metadata_paths | control_paths: reject()
declared_tables = set()
for schema_name, table_values in table_bytes.items():
  if schema_name not in schema_names: reject()
  for table_name in table_values:
    key = (schema_name, table_name)
    if key not in catalog or key in declared_tables: reject()
    declared_tables.add(key)
if declared_tables != catalog: reject()
data_paths = {path for path in inventory_paths if path.endswith(".tsv.zst")}
if set(chunk_bytes) != data_paths: reject()
inventory_hash = hashlib.sha256()
for item in inventory:
  inventory_hash.update(item["sha256"].encode("ascii") + b"\0" + str(item["size"]).encode("ascii") + b"\0" + item["path"].encode("utf-8") + b"\0")
verification = {"source":"mysql-shell-dump","inventoryAlgorithm":"sha256-nul-records-v1","inventorySha256":inventory_hash.hexdigest(),"files":inventory,"schemaCount":len(schemas),"tableCount":sum(item["tableCount"] for item in schemas),"schemas":schemas}
print("__AIFAR_VERIFICATION__" + json.dumps(verification, separators=(",", ":"), ensure_ascii=False))
`

func inspectDumpVerificationCommand(work string) string {
	return "python3 -c " + installerkit.ShellQuote(dumpVerificationHelper) + " " + installerkit.ShellQuote(work)
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
	copy := BackupCopyFor(lang)
	out := make([]installflow.Step, len(standaloneBackupStepNames))
	for index := range standaloneBackupStepNames {
		out[index] = installflow.Step{Name: standaloneBackupStepNames[index], Title: copy.StepTitles[standaloneBackupStepNames[index]]}
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

func backupMetadataForExecution(parameters backupParameters, phase string, inspection mysqlBackupInspection, execution standaloneBackupExecution) string {
	metadata := map[string]any{"name": parameters.Name, "threads": parameters.Threads, "maxRateMBps": parameters.MaxRateMBps, "keepLast": parameters.KeepLast, "phase": phase}
	if inspection.MySQLVersion != "" {
		metadata["mysqlVersion"], metadata["mysqlShellVersion"], metadata["schemas"] = inspection.MySQLVersion, inspection.MySQLShellVersion, inspection.Schemas
	}
	if execution.clusterID != "" {
		metadata["clusterId"] = execution.clusterID
	}
	encoded, _ := json.Marshal(metadata)
	return string(encoded)
}

func backupMetadataFromVerifiedManifest(parameters backupParameters, phase string, raw []byte) (string, error) {
	manifest, err := decodeRestoreManifest(raw)
	if err != nil {
		return "", err
	}
	metadata := map[string]any{
		"name": parameters.Name, "threads": parameters.Threads, "maxRateMBps": parameters.MaxRateMBps,
		"keepLast": parameters.KeepLast, "phase": phase, "manifestVersion": manifest.ManifestVersion,
		"topology": manifest.Topology, "mysqlVersion": manifest.MySQLVersion,
		"mysqlShellVersion": manifest.MySQLShellVersion, "schemas": manifest.Schemas,
	}
	if manifest.ClusterID != "" {
		metadata["clusterId"] = manifest.ClusterID
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
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
	secretPath := path.Join(work, "secret-context.cnf")
	query := "SELECT '__AIFAR_INFO__',@@version,@@server_uuid,@@GLOBAL.gtid_executed,COALESCE((SELECT SUM(data_length+index_length) FROM information_schema.tables WHERE table_schema NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys')),0); SELECT '__AIFAR_SCHEMA__',schema_name FROM information_schema.schemata WHERE schema_name NOT IN ('information_schema','mysql','mysql_innodb_cluster_metadata','performance_schema','sys') ORDER BY schema_name;"
	return mysqlRemoteCredentialValidationCommand(secretPath) + "; test -x " + installerkit.ShellQuote(mysqlsh) + "; printf '__AIFAR_SHELL__\\t'; " + installerkit.ShellQuote(mysqlsh) + " --version | awk '{print $NF}'; " + installerkit.ShellQuote(mysqlsh) + " --defaults-file=" + installerkit.ShellQuote(secretPath) + " --sql --raw --skip-column-names --host=127.0.0.1 --port=" + strconv.Itoa(port) + " --execute " + installerkit.ShellQuote(query)
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
