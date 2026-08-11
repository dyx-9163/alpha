package aifar

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"text/template"
	"time"
	"unicode"

	"aifar-deployment/backend/internal/agentdist"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

//go:embed templates/install.sh templates/uninstall.sh templates/update-artifact.sh templates/update-artifact-bundle.sh templates/rollback-artifact.sh templates/autoscale-out.sh templates/runtime-config.sh templates/runtime-diagnostics-estimate.sh templates/runtime-diagnostics-export.sh templates/runtime-diagnostics-cleanup.sh templates/service-install.sh templates/runtime-reconcile.sh templates/runtime-restart.sh templates/scale-service.sh
var templateFS embed.FS

type Logger = installerkit.Logger
type Remote = installerkit.Remote

type RuntimeDiagnosticRequest = registry.RuntimeDiagnosticRequest
type RuntimeDiagnosticDeleteRequest = registry.RuntimeDiagnosticDeleteRequest
type RuntimeDiagnosticStreamRequest = registry.RuntimeDiagnosticStreamRequest

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	GetAppInstance(id string) (store.AppInstance, error)
	ListAppInstances() ([]store.AppInstance, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	DeleteAppInstance(id string) error
}

type appInstanceCASStore interface {
	SaveAppInstanceIfUnchanged(next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error)
}

type aifarAcceptedProjectionStore interface {
	SaveAIFARAcceptedProjectionWithLock(lockID string, expected store.AIFARDeployment, next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error)
}

type runtimeConfigMetadataCASStore interface {
	SaveAppInstanceIfUnchangedWithLock(lockID string, next store.AppInstance, expectedUpdatedAt time.Time) (store.AppInstance, error)
}

const appInstanceMetadataCASAttempts = 8

func (s Service) updateAppInstanceMetadata(instanceID, repairReason string, mutate func(map[string]any) error) (store.AppInstance, error) {
	casStore, ok := s.store.(appInstanceCASStore)
	if !ok {
		return store.AppInstance{}, repairRequired(repairReason, errors.New("app instance compare-and-swap store is unavailable"))
	}
	for attempt := 0; attempt < appInstanceMetadataCASAttempts; attempt++ {
		current, err := s.store.GetAppInstance(instanceID)
		if err != nil {
			return store.AppInstance{}, repairRequired(repairReason, err)
		}
		metadata := metadataFromInstance(current)
		if err := mutate(metadata); err != nil {
			return current, err
		}
		raw, err := json.Marshal(metadata)
		if err != nil {
			return current, repairRequired(repairReason, err)
		}
		next := current
		next.Metadata = string(raw)
		saved, err := casStore.SaveAppInstanceIfUnchanged(next, current.UpdatedAt)
		if err == nil {
			return saved, nil
		}
		if !errors.Is(err, store.ErrAppInstanceConflict) {
			return current, repairRequired(repairReason, err)
		}
	}
	return store.AppInstance{}, repairRequired(repairReason, store.ErrAppInstanceConflict)
}

type resourceLister interface {
	ListResources() ([]store.Resource, error)
}

type releaseStore interface {
	SaveAppRelease(v store.AppRelease) (store.AppRelease, error)
	ListAppReleases(instanceID string) ([]store.AppRelease, error)
	DeleteOldAppReleases(instanceID string, keep int) (int, error)
}

type aifarOrchestrationStore interface {
	SaveAIFARDeployment(v store.AIFARDeployment) (store.AIFARDeployment, error)
	ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error)
	SaveAIFARReplicaSet(v store.AIFARReplicaSet) (store.AIFARReplicaSet, error)
	ListAIFARReplicaSets(instanceID string) ([]store.AIFARReplicaSet, error)
	SaveAIFARPod(v store.AIFARPod) (store.AIFARPod, error)
	ListAIFARPods(instanceID string) ([]store.AIFARPod, error)
	ReplaceAIFARServiceEndpoints(instanceID, serviceName string, endpoints []store.AIFARServiceEndpoint) error
	ListAIFARServiceEndpoints(instanceID string) ([]store.AIFARServiceEndpoint, error)
}

type aifarDeploymentControlStore interface {
	ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error)
	SaveAIFARDeploymentGeneration(next store.AIFARDeployment, expectedGeneration int64) (store.AIFARDeployment, error)
	AcceptAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error)
}

type aifarDeploymentLockFencedStore interface {
	SaveAIFARDeploymentGenerationWithLock(lockID string, next store.AIFARDeployment, expectedGeneration int64) (store.AIFARDeployment, error)
	AcceptAIFARDeploymentWithLock(lockID string, expected store.AIFARDeployment, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error)
}

type aifarInitialInstallFencedStore interface {
	SaveAIFARInitialDesiredWithLock(lockID string, deployments []store.AIFARDeployment, replicaSets []store.AIFARReplicaSet) error
	AcceptAIFARDeploymentWithLock(lockID string, expected store.AIFARDeployment, status, conditionsJSON string, at time.Time) (store.AIFARDeployment, error)
}

type aifarServiceInstallFencedStore interface {
	aifarDeploymentLockFencedStore
	SaveAIFARServiceInstallReplicaSetWithLock(lockID string, expected store.AIFARDeployment, next store.AIFARReplicaSet) (store.AIFARReplicaSet, error)
	CommitAIFARServiceInstallWithLock(commit store.AIFARServiceInstallCommit) (store.AppInstance, error)
}

type aifarRuntimeCleanupStore interface {
	PruneAIFARPodRecords(instanceID string, existingContainerNames []string) (int, error)
	PruneAIFARServiceEndpointRecords(instanceID string, existingContainerNames []string) (int, error)
}

type aifarOrchestrationLockStore interface {
	AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock) (store.AIFAROrchestrationLock, error)
	RenewAIFAROrchestrationLock(id string, expiresAt time.Time) (bool, error)
	ReleaseAIFAROrchestrationLockByID(id string) (bool, error)
	ReleaseAIFAROrchestrationLock(instanceID, operation, serviceName string) (bool, error)
	RecoverAIFAROrchestrationLocks(instanceID, reason string) (int, error)
}

func (s Service) startAIFAROrchestrationLockHeartbeat(ctx context.Context, lock store.AIFAROrchestrationLock) (context.Context, func()) {
	lockStore, ok := s.store.(aifarOrchestrationLockStore)
	if !ok || strings.TrimSpace(lock.ID) == "" {
		return ctx, func() {}
	}
	taskCtx, cancelTask := context.WithCancel(ctx)
	stop := make(chan struct{})
	done := make(chan struct{})
	var stopOnce sync.Once
	go func() {
		defer close(done)
		interval := s.orchestrationLockHeartbeatInterval
		if interval <= 0 {
			interval = orchestrationLockTTL / 3
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-ticker.C:
				renewed, err := lockStore.RenewAIFAROrchestrationLock(lock.ID, time.Now().UTC().Add(orchestrationLockTTL))
				if err != nil || !renewed {
					cancelTask()
					return
				}
			}
		}
	}()
	return taskCtx, func() {
		stopOnce.Do(func() {
			close(stop)
			<-done
		})
	}
}

type taskLookupStore interface {
	GetTask(id string) (store.Task, []store.TaskLog, error)
}

type runtimeDiagnosticsStore interface {
	SaveDiagnosticExport(store.DiagnosticExport) (store.DiagnosticExport, error)
	GetDiagnosticExport(id string) (store.DiagnosticExport, error)
	ReserveDiagnosticExportBytes(id string, bytes, quota int64) (store.DiagnosticExportStorageUsage, error)
	ReleaseDiagnosticExportReservation(id string) (bool, error)
	CommitLocalDiagnosticExport(store.LocalDiagnosticExportCommit) (store.DiagnosticExport, error)
	MarkDiagnosticExportFailed(id, errorText string, failedAt time.Time) (bool, error)
	MarkDiagnosticExportDownloaded(id string, downloadedAt time.Time) (bool, error)
	MarkDiagnosticExportCleanupPending(id string, attemptedAt time.Time) (bool, error)
	MarkDiagnosticExportCleanupFailed(id, cleanupError string) (bool, error)
	MarkDiagnosticExportDeleted(id string, deletedAt time.Time) (bool, error)
	ListDiagnosticExportsForReconcile() ([]store.DiagnosticExport, error)
	AcquireOperationLock(store.OperationLock) (store.OperationLock, error)
	ReleaseOperationLock(id string) (bool, error)
	AddAudit(actor, action, target, status, detail string) error
	ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error)
	ListAIFARPods(instanceID string) ([]store.AIFARPod, error)
	ListAppReleases(instanceID string) ([]store.AppRelease, error)
}

type InstallRequest struct {
	Version    string
	Topology   string
	Language   string
	Actor      string
	TaskID     string
	ServerID   string
	Parameters map[string]any
}

type DeleteRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	TaskID   string
}

type CheckRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
}

type ArtifactUpdateRequest struct {
	Instance           store.AppInstance
	Server             store.Server
	Language           string
	Actor              string
	TaskID             string
	ServiceName        string
	ExpectedGeneration int64
	LockID             string
	ArtifactLocalPath  string
	ArtifactFileName   string
}

type ArtifactBundleUpdateRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	TaskID          string
	BundleLocalPath string
	BundleFileName  string
	Concurrency     int
}

type ArtifactRollbackRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	TaskID          string
	TargetReleaseID string
	Services        []string
	Reason          string
	Force           bool
}

type ScaleOutRequest struct {
	Instance    store.AppInstance
	Server      store.Server
	Language    string
	Actor       string
	TaskID      string
	ServiceName string
	Reason      string
}

type ScaleRequest struct {
	Instance    store.AppInstance
	Server      store.Server
	Language    string
	Actor       string
	TaskID      string
	ServiceName string
	Replicas    int
	Reason      string
}

type ScaleServicesRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	TaskID          string
	DesiredReplicas map[string]int
	Reason          string
}

type InstallServicesRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	TaskID   string
	Services []string
	Reason   string
}

type RuntimeReconcileRequest struct {
	Instance    store.AppInstance
	Server      store.Server
	Language    string
	Actor       string
	TaskID      string
	ServiceName string
	Reason      string
}

type RuntimeRestartRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	TaskID   string
	Reason   string
}

type RuntimeMigrationRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Actor    string
	TaskID   string
	Reason   string
}

type RuntimeCleanupRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	TaskID   string
	Reason   string
}

type RuntimeAgentUninstallRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
	Actor    string
	TaskID   string
	Reason   string
}

type CheckResult struct {
	Status  string
	Message string
	Details map[string]any
}

type Service struct {
	store                              Store
	remote                             Remote
	archives                           RuntimeDiagnosticArchiveStorage
	orchestrationLockHeartbeatInterval time.Duration
}

type installStepDef struct {
	Name  string
	Title string
}

type targetLogger func(target string) Logger

type stepRecorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

type taskIDCarrier interface {
	TaskID() string
}

var requiredRuntimeAgentFeatures = []string{
	"reconcile-runtime",
	"local-runtime-controller",
	"endpoint-cache",
	"restart-runtime",
	"interactive-reconcile-priority",
	"runtime-delta-apply",
}

func NewService(s Store, remote Remote) Service {
	return Service{store: s, remote: remote}
}

func NewServiceWithDiagnosticStorage(s Store, remote Remote, archives RuntimeDiagnosticArchiveStorage) Service {
	return Service{store: s, remote: remote, archives: archives}
}

func fallbackTaskID(taskID string, log Logger) string {
	taskID = strings.TrimSpace(taskID)
	if taskID != "" {
		return taskID
	}
	if carrier, ok := log.(taskIDCarrier); ok {
		return strings.TrimSpace(carrier.TaskID())
	}
	return ""
}

func (s Service) ensureRuntimeAgent(ctx context.Context, server store.Server, workDir, lang string, log Logger) error {
	copy := copyFor(lang)
	agentLocal := agentdist.FindBinary()
	if agentLocal == "" {
		return errors.New("AIFAR runtime agent binary is missing; rebuild the backend or use a release package containing bin/aifar-agent-linux-amd64")
	}
	agentSHA256, _, err := fileSHA256(agentLocal)
	if err != nil {
		return fmt.Errorf("calculate AIFAR runtime agent checksum: %w", err)
	}
	inspection, err := s.inspectRuntimeAgent(ctx, server)
	if err != nil {
		return fmt.Errorf("inspect AIFAR runtime agent: %w", err)
	}
	if reason := inspection.upgradeReason(agentSHA256); reason == "" {
		if log != nil {
			log.Info("AIFAR runtime agent is already current; skip agent restart")
		}
		return nil
	} else if log != nil {
		log.Info("AIFAR runtime agent upgrade required: %s", reason)
	}
	workDir = strings.TrimRight(strings.TrimSpace(workDir), "/")
	if workDir == "" {
		workDir = installerkit.WorkDir(server.DeployDir, AppName+"-agent", "runtime-v2", time.Now().UTC())
	}
	if _, err := installerkit.Run(ctx, s.remote, server, "mkdir -p "+installerkit.ShellQuote(workDir), log, copy.RemoteCommandFailed); err != nil {
		return err
	}
	agentRemote := workDir + "/" + filepath.Base(agentLocal)
	if err := uploadkit.Upload(ctx, s.remote, server, uploadkit.File{
		LocalPath:      agentLocal,
		RemotePath:     agentRemote,
		Mode:           0o755,
		LogMessage:     copy.UploadAgent,
		FailureMessage: copy.UploadAgentFailed,
	}, log); err != nil {
		return err
	}
	command := strings.Join([]string{
		"set -eu",
		"echo AIFAR_AGENT_UPGRADE",
		"SUDO=\"\"",
		"if [ \"$(id -u)\" != \"0\" ]; then SUDO=\"sudo\"; fi",
		"$SUDO install -m 0755 " + installerkit.ShellQuote(agentRemote) + " /usr/local/bin/aifar-agent",
		"if command -v systemctl >/dev/null 2>&1; then",
		"  $SUDO systemctl daemon-reload >/dev/null 2>&1 || true",
		"  $SUDO systemctl restart aifar-agent",
		"else",
		"  echo \"systemctl is required to restart aifar-agent\" >&2",
		"  exit 1",
		"fi",
		"for i in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20 21 22 23 24 25 26 27 28 29 30; do",
		"  if status=\"$(aifar-agent status 2>/dev/null)\" && printf \"%s\" \"$status\" | grep -q '\"reconcile-runtime\"' && printf \"%s\" \"$status\" | grep -q '\"local-runtime-controller\"' && printf \"%s\" \"$status\" | grep -q '\"endpoint-cache\"' && printf \"%s\" \"$status\" | grep -q '\"restart-runtime\"' && printf \"%s\" \"$status\" | grep -q '\"interactive-reconcile-priority\"' && printf \"%s\" \"$status\" | grep -q '\"runtime-delta-apply\"'; then",
		"    exit 0",
		"  fi",
		"  sleep 1",
		"done",
		"echo \"aifar-agent service is not reachable after upgrade\" >&2",
		"exit 1",
	}, "\n")
	_, err = installerkit.Run(ctx, s.remote, server, command, log, "AIFAR runtime agent upgrade failed")
	return err
}

type runtimeAgentInspection struct {
	Found    bool
	Status   string
	Version  string
	SHA256   string
	Features map[string]bool
}

func (s Service) inspectRuntimeAgent(ctx context.Context, server store.Server) (runtimeAgentInspection, error) {
	result, err := s.remote.Run(ctx, server, runtimeAgentInspectionCommand())
	if err != nil {
		return runtimeAgentInspection{}, err
	}
	return parseRuntimeAgentInspection(result.Stdout), nil
}

func runtimeAgentInspectionCommand() string {
	return strings.Join([]string{
		"set +e",
		"echo AIFAR_AGENT_CHECK",
		"agent_path=\"$(command -v aifar-agent 2>/dev/null || true)\"",
		"if [ -z \"$agent_path\" ]; then",
		"  echo \"agentFound=false\"",
		"  echo \"sha256=\"",
		"  exit 0",
		"fi",
		"echo \"agentFound=true\"",
		"echo \"agentPath=$agent_path\"",
		"status_json=\"$(aifar-agent status 2>/dev/null)\"",
		"status_rc=$?",
		"if [ \"$status_rc\" -eq 0 ]; then",
		"  printf 'status=%s\\n' \"$status_json\"",
		"else",
		"  echo \"statusError=$status_rc\"",
		"fi",
		"if [ -f \"$agent_path\" ] && command -v sha256sum >/dev/null 2>&1; then",
		"  sha256sum \"$agent_path\" 2>/dev/null | awk '{print \"sha256=\" $1}'",
		"else",
		"  echo \"sha256=\"",
		"fi",
		"exit 0",
	}, "\n")
}

func parseRuntimeAgentInspection(output string) runtimeAgentInspection {
	inspection := runtimeAgentInspection{Features: map[string]bool{}}
	for _, line := range strings.Split(output, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok {
			continue
		}
		switch key {
		case "agentFound":
			inspection.Found = value == "true"
		case "sha256":
			inspection.SHA256 = strings.TrimSpace(value)
		case "status":
			var status struct {
				Status   string   `json:"status"`
				Version  string   `json:"version"`
				Features []string `json:"features"`
			}
			if err := json.Unmarshal([]byte(value), &status); err != nil {
				continue
			}
			inspection.Status = strings.TrimSpace(status.Status)
			inspection.Version = strings.TrimSpace(status.Version)
			for _, feature := range status.Features {
				feature = strings.TrimSpace(feature)
				if feature != "" {
					inspection.Features[feature] = true
				}
			}
		}
	}
	return inspection
}

func (i runtimeAgentInspection) upgradeReason(localSHA256 string) string {
	if !i.Found {
		return "aifar-agent is missing"
	}
	if i.Status != "running" {
		return "aifar-agent is not running or status is unavailable"
	}
	for _, feature := range requiredRuntimeAgentFeatures {
		if !i.Features[feature] {
			return "aifar-agent is missing feature " + feature
		}
	}
	if strings.TrimSpace(i.SHA256) == "" {
		return "aifar-agent checksum is unavailable"
	}
	if !strings.EqualFold(strings.TrimSpace(i.SHA256), strings.TrimSpace(localSHA256)) {
		return "aifar-agent binary checksum changed"
	}
	return ""
}

func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
	copy := copyFor(req.Language)
	target := strings.TrimSpace(req.ServerID)
	if target == "" {
		return errors.New(copy.TargetRequired)
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, installSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)

	var server store.Server
	if err := step(1, func() error {
		var loadErr error
		server, loadErr = s.store.GetServer(target, true)
		return loadErr
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	var bundle Bundle
	var serviceDefinitions []serviceDefinition
	var archiveLocal string
	options := optionsFromParameters(req.Parameters)
	if err := step(2, func() error {
		var bundleErr error
		bundle, bundleErr = SelectBundle(resources, req.Version)
		if bundleErr != nil {
			return bundleErr
		}
		if err := VerifyBundle(bundle); err != nil {
			return err
		}
		serviceDefinitions, bundleErr = discoverBundleServices(bundle)
		if bundleErr != nil {
			return bundleErr
		}
		selected := sliceParam(req.Parameters, "selectedServices", serviceNames(serviceDefinitions))
		if err := validateSelectedServicesForCatalog(selected, serviceDefinitions); err != nil {
			return err
		}
		options.SelectedServices = normalizeSelectedServicesForCatalog(selected, serviceDefinitions)
		var resolveErr error
		options, resolveErr = s.resolveInstallOptions(options)
		if resolveErr != nil {
			return resolveErr
		}
		if err := options.Validate(); err != nil {
			return err
		}
		serviceDefinitions = installServiceDefinitionsWithResources(serviceDefinitions, options)
		var archiveErr error
		archiveLocal, archiveErr = CreateBundleArchive(bundle)
		return archiveErr
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	defer os.Remove(archiveLocal)

	deployDir := installerkit.RemoteDeployDir(server.DeployDir)
	releaseTime := time.Now().UTC()
	releaseID := newReleaseID(bundle.Version, releaseTime)
	configHash := installConfigHash(options)
	ingressNetwork := options.NetworkName
	workDir := installerkit.WorkDir(deployDir, AppName, bundle.Version, releaseTime)
	installRoot := installRootFromDeployDir(deployDir)
	archiveRemote := workDir + "/" + filepath.Base(archiveLocal)
	agentLocal := agentdist.FindBinary()
	agentRemote := ""
	if agentLocal != "" {
		agentRemote = workDir + "/" + filepath.Base(agentLocal)
	}
	scriptRemote := workDir + "/install-aifar.sh"
	metadata := installMetadata(server, installRoot, bundle.Version, releaseID, releaseTime, configHash, options, req.Actor)
	metadata["serviceCatalog"] = serviceCatalogMetadataForInstall(serviceDefinitions, options.GatewayPort, options.WebPort)
	metadata["installState"] = "installing"
	if strings.TrimSpace(req.TaskID) != "" {
		metadata["taskId"] = strings.TrimSpace(req.TaskID)
	}
	var instance store.AppInstance
	var expectedBootstrapHashes map[string]string
	var expectedBootstrapDeployments map[string]store.AIFARDeployment
	var installLock store.AIFAROrchestrationLock
	installOwner := ""
	installCtx := ctx
	instanceCreatedAt := releaseTime

	if err := step(3, func() error {
		instanceID, lock, err := s.acquireInstallAttemptClaim(target, installRoot, req.Actor, fallbackTaskID(req.TaskID, log))
		if err != nil {
			return err
		}
		installLock = lock
		installOwner = strings.TrimSpace(lock.ID)
		if installOwner == "" {
			installOwner = store.NewID("aifarinstallattempt")
		}
		if instanceID != "" {
			previous, err := s.store.GetAppInstance(instanceID)
			if err != nil && !store.IsNotFound(err) {
				return err
			}
			if err == nil {
				if !previous.CreatedAt.IsZero() {
					instanceCreatedAt = previous.CreatedAt
				}
				previousMetadata := metadataFromInstance(previous)
				if previous.App != AppName || previous.ServerID != target || normalizeInstallRoot(stringFromMetadata(previousMetadata, "installRoot", "")) != normalizeInstallRoot(installRoot) || !s.reusableAIFARInstallInstance(previous, previousMetadata) {
					return repairRequired("AIFAR_RUNTIME_INSTALL_ATTEMPT_OWNERSHIP_CHANGED", nil)
				}
				previousReleaseID := stringFromMetadata(previousMetadata, "releaseId", "")
				sameAttempt := previousReleaseID != "" &&
					stringFromMetadata(previousMetadata, "releaseVersion", "") == bundle.Version &&
					stringFromMetadata(previousMetadata, "configHash", "") == configHash
				if sameAttempt {
					releaseID = previousReleaseID
					if previousReleaseTime, parseErr := time.Parse(time.RFC3339, stringFromMetadata(previousMetadata, "releaseCreatedAt", "")); parseErr == nil {
						releaseTime = previousReleaseTime.UTC()
					}
					metadata = installMetadata(server, installRoot, bundle.Version, releaseID, releaseTime, configHash, options, req.Actor)
					metadata["serviceCatalog"] = serviceCatalogMetadataForInstall(serviceDefinitions, options.GatewayPort, options.WebPort)
					metadata["installState"] = "installing"
					if strings.TrimSpace(req.TaskID) != "" {
						metadata["taskId"] = strings.TrimSpace(req.TaskID)
					}
				} else if control, ok := s.store.(aifarDeploymentControlStore); ok {
					deployments, listErr := control.ListAIFARDeployments(instanceID)
					if listErr != nil {
						return listErr
					}
					for _, deployment := range deployments {
						if deployment.Generation > 0 && strings.TrimSpace(deployment.SpecJSON) != "" {
							return repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_CONFIG_CHANGED", nil)
						}
					}
				}
			}
		}
		metadata["installAttemptOwner"] = installOwner
		data, _ := json.Marshal(metadata)
		instance = store.AppInstance{
			ID:        instanceID,
			App:       AppName,
			Version:   bundle.Version,
			ServerID:  target,
			Status:    "installing",
			Topology:  defaultTopology,
			Metadata:  string(data),
			CreatedAt: instanceCreatedAt,
		}
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(instance)
		return saveErr
	}); err != nil {
		if installLock.InstanceID != "" {
			s.releaseOrchestrationLock(installLock)
		}
		msg := fmt.Sprintf(copy.RecordFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	if installLock.InstanceID != "" {
		defer s.releaseOrchestrationLock(installLock)
		var stopHeartbeat func()
		installCtx, stopHeartbeat = s.startAIFAROrchestrationLockHeartbeat(ctx, installLock)
		defer stopHeartbeat()
	}

	if err := step(4, func() error {
		logForServer.Info(copy.PrepareWorkDir, workDir)
		if _, err := installerkit.Run(installCtx, s.remote, server, "mkdir -p "+installerkit.ShellQuote(workDir), logForServer, copy.RemoteCommandFailed); err != nil {
			return err
		}
		if err := uploadkit.Upload(installCtx, s.remote, server, uploadkit.File{
			LocalPath:      archiveLocal,
			RemotePath:     archiveRemote,
			LogMessage:     copy.UploadBundle,
			LogArgs:        []any{bundle.Root},
			FailureMessage: copy.UploadBundleFailed,
		}, logForServer); err != nil {
			return err
		}
		if agentLocal != "" {
			if err := uploadkit.Upload(installCtx, s.remote, server, uploadkit.File{
				LocalPath:      agentLocal,
				RemotePath:     agentRemote,
				Mode:           0o755,
				LogMessage:     copy.UploadAgent,
				FailureMessage: copy.UploadAgentFailed,
			}, logForServer); err != nil {
				return err
			}
		}
		var desiredErr error
		expectedBootstrapDeployments, desiredErr = s.saveInitialControlPlaneDesired(installLock.ID, instance, releaseID, bundle.Version, configHash, options, serviceDefinitions, releaseTime)
		if desiredErr != nil {
			return desiredErr
		}
		var hashErr error
		expectedBootstrapHashes, hashErr = s.initialDeploymentSpecHashes(instance, options.SelectedServices, expectedBootstrapDeployments)
		if hashErr != nil {
			return hashErr
		}
		script, err := renderInstallScript(installScriptData{
			InstallRoot:         installRoot,
			InstanceID:          instance.ID,
			WorkDir:             workDir,
			ArchiveRemote:       archiveRemote,
			AgentBinaryRemote:   agentRemote,
			ServiceOrder:        strings.Join(options.SelectedServices, " "),
			ServiceApplications: serviceCatalogPairs(serviceDefinitions, options.SelectedServices, func(definition serviceDefinition) string { return definition.ApplicationName }),
			ServicePorts: serviceCatalogPairs(serviceDefinitions, options.SelectedServices, func(definition serviceDefinition) string {
				if definition.Role == "gateway" {
					return fmt.Sprint(options.GatewayPort)
				}
				if definition.Role == "web" {
					return fmt.Sprint(options.WebPort)
				}
				return fmt.Sprint(definition.Port)
			}),
			ServiceKinds:       serviceCatalogPairs(serviceDefinitions, options.SelectedServices, func(definition serviceDefinition) string { return definition.Kind }),
			ServiceHealthPaths: serviceCatalogPairs(serviceDefinitions, options.SelectedServices, func(definition serviceDefinition) string { return definition.HealthPath }),
			ServiceAffinities:  serviceCatalogPairs(serviceDefinitions, options.SelectedServices, func(definition serviceDefinition) string { return definition.AffinityPolicy }),
			ServiceSpecHashes:  serviceSpecHashPairs(options.SelectedServices, expectedBootstrapHashes),
			GatewayService:     serviceNameForRole(serviceDefinitions, "gateway"),
			WebService:         serviceNameForRole(serviceDefinitions, "web"),
			Version:            bundle.Version,
			ReleaseID:          releaseID,
			CreatedAt:          releaseTime.Format(time.RFC3339),
			ConfigHash:         configHash,
			IngressNetwork:     ingressNetwork,
			Options:            options,
		})
		if err != nil {
			return err
		}
		scriptLocal, err := installerkit.WriteTempScript("aifar-service-install-*.sh", script)
		if err != nil {
			return err
		}
		defer os.Remove(scriptLocal)
		if err := uploadkit.Upload(installCtx, s.remote, server, uploadkit.File{
			LocalPath:      scriptLocal,
			RemotePath:     scriptRemote,
			Mode:           0o755,
			LogMessage:     copy.UploadScript,
			FailureMessage: copy.UploadScriptFailed,
		}, logForServer); err != nil {
			return err
		}
		return nil
	}); err != nil {
		_ = s.markInstallFailed(instance, metadata, err)
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(5, func() error {
		logForServer.Info(copy.Deploying)
		result, err := installerkit.Run(installCtx, s.remote, server, "sh "+installerkit.ShellQuote(scriptRemote), logForServer, copy.RemoteCommandFailed)
		if err != nil {
			return err
		}
		_, err = decodeBootstrapAcceptance(result.Stdout, instance.ID, expectedBootstrapHashes)
		if err != nil {
			return err
		}
		return s.acceptInitialControlPlane(installLock.ID, expectedBootstrapDeployments, options.SelectedServices)
	}); err != nil {
		_ = s.markInstallFailed(instance, metadata, err)
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(6, func() error {
		current, owned, err := s.currentInstallAttemptOwner(instance.ID, installOwner)
		if err != nil {
			return err
		}
		if !owned {
			return repairRequired("AIFAR_RUNTIME_INSTALL_ATTEMPT_OWNERSHIP_CHANGED", nil)
		}
		releases, ok := s.store.(releaseStore)
		if !ok {
			return repairRequired("AIFAR_RUNTIME_RELEASE_STORE_UNAVAILABLE", nil)
		}
		control, ok := s.store.(aifarDeploymentControlStore)
		if !ok {
			return repairRequired("AIFAR_RUNTIME_DEPLOYMENT_STORE_UNAVAILABLE", nil)
		}
		deployments, err := control.ListAIFARDeployments(instance.ID)
		if err != nil {
			return err
		}
		manifest, _ := json.Marshal(releaseManifest(bundle.Version, releaseID, releaseTime, configHash, ingressNetwork, options.GatewayPort, options.WebPort, options.SelectedServices, deployments))
		if _, err := releases.SaveAppRelease(store.AppRelease{
			InstanceID:   instance.ID,
			App:          AppName,
			Version:      bundle.Version,
			ReleaseID:    releaseID,
			ServerID:     target,
			Status:       "success",
			ManifestJSON: string(manifest),
			ConfigHash:   configHash,
			CreatedAt:    releaseTime,
			ActivatedAt:  releaseTime,
		}); err != nil {
			return err
		}
		delete(metadata, "installFailed")
		delete(metadata, "failedAt")
		delete(metadata, "error")
		metadata["installState"] = "installed"
		data, _ := json.Marshal(metadata)
		instance = store.AppInstance{
			ID:        instance.ID,
			App:       AppName,
			Version:   bundle.Version,
			ServerID:  target,
			Status:    "installed",
			Topology:  defaultTopology,
			Metadata:  string(data),
			CreatedAt: current.CreatedAt,
		}
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(instance)
		if saveErr != nil {
			return saveErr
		}
		if _, err := releases.DeleteOldAppReleases(instance.ID, releaseKeepCount); err != nil {
			logForServer.Info("%s", i18n.Text(req.Language, "aifar.install.releaseRetentionCleanupWarning"))
		}
		return nil
	}); err != nil {
		_ = s.markInstallFailed(instance, metadata, err)
		msg := fmt.Sprintf(copy.RecordFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	logForServer.Info(copy.Installed, instance.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func installMetadata(server store.Server, installRoot, version, releaseID string, releaseTime time.Time, configHash string, options InstallOptions, actor string) map[string]any {
	runtimeDir := installRoot + "/runtime"
	metadata := map[string]any{
		"installRoot":           installRoot,
		"runtimeDir":            runtimeDir,
		"orchestrationModel":    orchestrationModelServiceControllerV1,
		"releasePhase":          releasePhaseActive,
		"currentRevision":       releaseID,
		"releaseId":             releaseID,
		"releaseVersion":        version,
		"releaseCreatedAt":      releaseTime.Format(time.RFC3339),
		"configHash":            configHash,
		"appDir":                runtimeDir + "/" + appBundleDir,
		"envDir":                runtimeDir + "/" + releaseEnvDirName,
		"networkName":           options.NetworkName,
		"appCPUs":               options.AppCPUs,
		"appMemoryLimit":        options.AppMemoryLimit,
		"timezone":              options.Timezone,
		"runtimeConfigMode":     "dynamic-jvm-v1",
		"runtimeConfig":         runtimeConfigFromOptions(options, actor, releaseTime),
		"endpoint":              fmt.Sprintf("%s:%d", server.Host, options.WebPort),
		"gatewayEndpoint":       fmt.Sprintf("%s:%d", server.Host, options.GatewayPort),
		"nacosEndpoint":         fmt.Sprintf("%s:%d", options.NacosHost, options.NacosWebPort),
		"nacosRegistrationMode": "agent-proxy",
		"agent": map[string]any{
			"systemdService": "aifar-agent",
			"controlAddress": "127.0.0.1:18081",
		},
		"webPort":         options.WebPort,
		"gatewayPort":     options.GatewayPort,
		"nacosSource":     options.NacosSource,
		"nacosInstanceId": options.NacosInstanceID,
		"nacosHost":       options.NacosHost,
		"nacosPort":       options.NacosWebPort,
		"nacosWebPort":    options.NacosWebPort,
		"nacosApiPort":    options.NacosAPIPort,
		"services":        options.SelectedServices,
		"rolloutDefaults": map[string]any{
			"maxSurge":       1,
			"maxUnavailable": 0,
			"drainSeconds":   30,
		},
	}
	for key, value := range releaseOrchestrationMetadata(installRoot, releaseID, options.NetworkName, options.GatewayPort, options.WebPort, options.SelectedServices) {
		metadata[key] = value
	}
	return metadata
}

func installServiceDefinitionsWithResources(definitions []serviceDefinition, options InstallOptions) []serviceDefinition {
	out := append([]serviceDefinition(nil), definitions...)
	for index := range out {
		out[index].Resources = runtimeagent.ResourceSpec{CPUs: options.AppCPUs, Memory: options.AppMemoryLimit}
	}
	return out
}

const bootstrapAcceptanceMarker = "AIFAR_BOOTSTRAP_ACCEPTANCE="

type bootstrapDeploymentProof struct {
	Accepted    bool   `json:"accepted"`
	InstanceID  string `json:"instanceId"`
	ServiceName string `json:"serviceName"`
	Generation  int64  `json:"generation"`
	SpecHash    string `json:"specHash"`
}

type bootstrapAcceptanceProof struct {
	Accepted    bool                       `json:"accepted"`
	InstanceID  string                     `json:"instanceId"`
	Deployments []bootstrapDeploymentProof `json:"deployments"`
}

func decodeBootstrapAcceptance(stdout, instanceID string, expectedHashes map[string]string) (bootstrapAcceptanceProof, error) {
	var acceptance bootstrapAcceptanceProof
	payload := ""
	seen := false
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, bootstrapAcceptanceMarker) {
			continue
		}
		if seen {
			return acceptance, errors.New("aifar-agent bootstrap acceptance is duplicated")
		}
		seen = true
		payload = strings.TrimPrefix(line, bootstrapAcceptanceMarker)
	}
	if payload == "" || len(payload) > 1<<20 {
		return acceptance, errors.New("aifar-agent bootstrap acceptance is missing")
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&acceptance); err != nil {
		return acceptance, errors.New("aifar-agent bootstrap acceptance is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return acceptance, errors.New("aifar-agent bootstrap acceptance is invalid")
	}
	if !acceptance.Accepted || acceptance.InstanceID != instanceID || len(expectedHashes) < 1 || len(acceptance.Deployments) != len(expectedHashes) {
		return acceptance, errors.New("aifar-agent bootstrap acceptance does not match the installation")
	}
	seenServices := make(map[string]bool, len(expectedHashes))
	for _, deployment := range acceptance.Deployments {
		expectedHash, exists := expectedHashes[deployment.ServiceName]
		if !exists || seenServices[deployment.ServiceName] || !deployment.Accepted || deployment.InstanceID != instanceID || deployment.Generation != 1 ||
			!deploymentSpecHashPattern.MatchString(deployment.SpecHash) || deployment.SpecHash != expectedHash {
			return acceptance, errors.New("aifar-agent deployment acceptance is invalid")
		}
		seenServices[deployment.ServiceName] = true
	}
	return acceptance, nil
}

func (s Service) initialDeploymentSpecHashes(instance store.AppInstance, services []string, byService map[string]store.AIFARDeployment) (map[string]string, error) {
	hashes := make(map[string]string, len(services))
	for _, serviceName := range services {
		deployment, exists := byService[serviceName]
		if !exists || deployment.Generation != 1 || strings.TrimSpace(deployment.SpecJSON) == "" {
			return nil, repairRequired("AIFAR_RUNTIME_INSTALL_DESIRED_STATE_MISSING", nil)
		}
		manifest, err := buildRuntimeManifest(instance, deployment, 1)
		if err != nil {
			return nil, repairRequired("AIFAR_RUNTIME_MANIFEST_BUILD_FAILED", err)
		}
		hash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
		if err != nil {
			return nil, repairRequired("AIFAR_RUNTIME_MANIFEST_HASH_FAILED", err)
		}
		hashes[serviceName] = hash
	}
	return hashes, nil
}

func serviceSpecHashPairs(services []string, hashes map[string]string) string {
	pairs := make([]string, 0, len(services))
	for _, serviceName := range services {
		pairs = append(pairs, serviceName+"="+hashes[serviceName])
	}
	return strings.Join(pairs, " ")
}

func (s Service) saveInitialControlPlaneDesired(lockID string, instance store.AppInstance, revision, version, configHash string, options InstallOptions, definitions []serviceDefinition, now time.Time) (map[string]store.AIFARDeployment, error) {
	fenced, ok := s.store.(aifarInitialInstallFencedStore)
	if !ok {
		return nil, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	metadata := metadataFromInstance(instance)
	strategy := runtimeagent.NormalizeDeploymentStrategy(runtimeagent.DeploymentStrategySpec{})
	strategyJSON, err := json.Marshal(strategy)
	if err != nil {
		return nil, err
	}
	pendingConditions, err := deploymentConditionsJSON(false, "PendingAgentAcceptance", 1)
	if err != nil {
		return nil, err
	}
	deployments := make([]store.AIFARDeployment, 0, len(options.SelectedServices))
	replicaSets := make([]store.AIFARReplicaSet, 0, len(options.SelectedServices))
	byService := make(map[string]store.AIFARDeployment, len(options.SelectedServices))
	for _, serviceName := range options.SelectedServices {
		definition, found := catalogDefinition(definitions, serviceName)
		if !found {
			return nil, fmt.Errorf("AIFAR service %s is not defined", serviceName)
		}
		base := store.AIFARDeployment{InstanceID: instance.ID, ServiceName: serviceName, DesiredReplicas: 1, CurrentRevision: revision}
		installRoot := strings.TrimSpace(stringFromMetadata(metadata, "installRoot", ""))
		manifest := runtimeagent.NormalizeDeploymentManifest(runtimeManifestDefaults(instance.ID, installRoot, definition, base, 1, metadata))
		config := runtimeInstanceConfig(instance, metadata, installRoot)
		if err := runtimeagent.ValidateDeploymentManifest(config, manifest); err != nil {
			return nil, err
		}
		specJSON, err := json.Marshal(manifest)
		if err != nil {
			return nil, err
		}
		deployment := store.AIFARDeployment{
			InstanceID: instance.ID, ServiceName: serviceName, DesiredReplicas: 1, CurrentRevision: revision,
			StrategyJSON: string(strategyJSON), SpecJSON: string(specJSON), Generation: 1, ObservedGeneration: 0,
			Status: "pending_acceptance", MetadataJSON: `{"model":"` + orchestrationModelServiceControllerV1 + `"}`,
			ConditionsJSON: pendingConditions, LastTransitionAt: now, CreatedAt: now,
		}
		deployments = append(deployments, deployment)
		byService[serviceName] = deployment
		replicaSets = append(replicaSets, store.AIFARReplicaSet{
			InstanceID: instance.ID, ServiceName: serviceName, Revision: revision,
			Image: fmt.Sprintf("aifar-%s:%s", serviceName, revision), ArtifactHash: configHash,
			DesiredPods: 1, ReadyPods: 0, Status: "pending", MetadataJSON: fmt.Sprintf(`{"version":%q}`, version), CreatedAt: now,
		})
	}
	if err := fenced.SaveAIFARInitialDesiredWithLock(lockID, deployments, replicaSets); err != nil {
		if errors.Is(err, store.ErrAIFARDeploymentGenerationConflict) {
			return nil, repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_SET_CHANGED", err)
		}
		return nil, err
	}
	return byService, nil
}

func (s Service) acceptInitialControlPlane(lockID string, expected map[string]store.AIFARDeployment, services []string) error {
	fenced, ok := s.store.(aifarInitialInstallFencedStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	for _, serviceName := range services {
		desired, exists := expected[serviceName]
		if !exists {
			return repairRequired("AIFAR_RUNTIME_INSTALL_DESIRED_STATE_MISSING", nil)
		}
		conditions, err := deploymentConditionsJSON(true, "ManifestAccepted", 1)
		if err != nil {
			return err
		}
		_, err = fenced.AcceptAIFARDeploymentWithLock(lockID, desired, "Accepted", conditions, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	return nil
}

func (s Service) saveRolloutControlPlane(instanceID, version, revision, serviceName, artifactHash string, replicas, gatewayPort, webPort int, now time.Time) error {
	if replicas < 0 {
		replicas = 0
	}
	if orch, ok := s.store.(aifarOrchestrationStore); ok {
		return saveControlPlaneRevision(orch, instanceID, version, revision, artifactHash, map[string]int{serviceName: replicas}, gatewayPort, webPort, []string{serviceName}, now)
	}
	return nil
}

func saveControlPlaneRevision(orch aifarOrchestrationStore, instanceID, version, revision, artifactHash string, desired map[string]int, gatewayPort, webPort int, services []string, now time.Time, portCatalog ...map[string]int) error {
	strategy := `{"type":"RollingUpdate","maxSurge":1,"maxUnavailable":0,"drainSeconds":30}`
	for _, service := range services {
		replicas := desired[service]
		if replicas < 0 {
			replicas = 0
		}
		port := serviceDefaultPort(service, gatewayPort, webPort)
		if len(portCatalog) > 0 && portCatalog[0][service] > 0 {
			port = portCatalog[0][service]
		}
		if _, err := orch.SaveAIFARDeployment(store.AIFARDeployment{
			InstanceID:      instanceID,
			ServiceName:     service,
			DesiredReplicas: replicas,
			CurrentRevision: revision,
			StrategyJSON:    strategy,
			Status:          "active",
			MetadataJSON:    `{"model":"` + orchestrationModelServiceControllerV1 + `"}`,
			CreatedAt:       now,
		}); err != nil {
			return err
		}
		if _, err := orch.SaveAIFARReplicaSet(store.AIFARReplicaSet{
			InstanceID:   instanceID,
			ServiceName:  service,
			Revision:     revision,
			Image:        fmt.Sprintf("aifar-%s:%s", service, revision),
			ArtifactHash: artifactHash,
			DesiredPods:  replicas,
			ReadyPods:    replicas,
			Status:       "active",
			MetadataJSON: fmt.Sprintf(`{"version":%q}`, version),
			CreatedAt:    now,
		}); err != nil {
			return err
		}
		endpoints := make([]store.AIFARServiceEndpoint, 0, replicas)
		for replicaID := 1; replicaID <= replicas; replicaID++ {
			pod := store.AIFARPod{
				InstanceID:    instanceID,
				ServiceName:   service,
				Revision:      revision,
				PodID:         podID(service, revision, replicaID),
				ContainerName: podContainerName(service, revision, replicaID),
				Port:          port,
				Status:        "ready",
				Ready:         true,
				MetadataJSON:  fmt.Sprintf(`{"replicaId":%d}`, replicaID),
				CreatedAt:     now,
			}
			if _, err := orch.SaveAIFARPod(pod); err != nil {
				return err
			}
			endpoints = append(endpoints, store.AIFARServiceEndpoint{
				InstanceID:    instanceID,
				ServiceName:   service,
				PodID:         pod.PodID,
				ContainerName: pod.ContainerName,
				Revision:      revision,
				Port:          port,
				State:         "active",
				Ready:         true,
				MetadataJSON:  pod.MetadataJSON,
				CreatedAt:     now,
			})
		}
		if err := orch.ReplaceAIFARServiceEndpoints(instanceID, service, endpoints); err != nil {
			return err
		}
	}
	return nil
}

func ensureK8sLikeInstance(instance store.AppInstance, copy UpdateCopy) error {
	if instance.App != AppName {
		return errors.New(copy.UnsupportedInstance)
	}
	return ensureK8sLikeMetadata(metadataFromInstance(instance), copy)
}

func ensureK8sLikeMetadata(metadata map[string]any, copy UpdateCopy) error {
	model := ""
	if value, ok := metadata["orchestrationModel"]; ok {
		model = strings.TrimSpace(fmt.Sprint(value))
	}
	if IsServiceControllerModel(model) {
		return nil
	}
	if model == "" {
		model = legacyOrchestrationModel
	}
	return fmt.Errorf(copy.LegacyUpdateUnsupported, model)
}

func currentRevisionForService(metadata map[string]any, serviceName string) string {
	if revisions, ok := metadata["serviceRevisions"].(map[string]any); ok {
		if revision := metadataText(revisions[serviceName]); revision != "" {
			return revision
		}
	}
	if endpoints, ok := metadata["activeEndpoints"].(map[string]any); ok {
		if items, ok := endpoints[serviceName]; ok {
			if revision := firstEndpointRevision(items); revision != "" {
				return revision
			}
		}
	}
	return stringFromMetadata(metadata, "currentRevision", stringFromMetadata(metadata, "releaseId", ""))
}

func rolloutAcceptedIntentMetadata(current map[string]any, revision string, changedServices []string) map[string]any {
	next := copyMetadata(current)
	revisions := serviceRevisionsFromMetadata(current)
	for _, serviceName := range changedServices {
		revisions[serviceName] = revision
	}
	next["serviceRevisions"] = revisions
	next["releasePhase"] = releasePhaseActive
	return next
}

func mergeServiceScopedRolloutMetadata(metadata map[string]any, revision, configHash string, changedServices []string, audit map[string]any) map[string]any {
	orchestration := rolloutAcceptedIntentMetadata(metadata, revision, changedServices)
	for key, value := range orchestration {
		metadata[key] = value
	}
	serviceConfigHashes := mapFromMetadataValue(metadata["serviceConfigHashes"])
	if serviceConfigHashes == nil {
		serviceConfigHashes = map[string]any{}
	}
	serviceRollouts := mapFromMetadataValue(metadata["serviceRollouts"])
	if serviceRollouts == nil {
		serviceRollouts = map[string]any{}
	}
	for _, serviceName := range changedServices {
		serviceConfigHashes[serviceName] = configHash
		serviceAudit := copyMetadata(audit)
		serviceAudit["service"] = serviceName
		serviceRollouts[serviceName] = serviceAudit
	}
	metadata["serviceConfigHashes"] = serviceConfigHashes
	metadata["serviceRollouts"] = serviceRollouts
	metadata["configHash"] = configHash
	metadata["lastRollout"] = copyMetadata(audit)
	return orchestration
}

func firstEndpointRevision(value any) string {
	switch items := value.(type) {
	case []map[string]any:
		for _, item := range items {
			if revision := endpointRevision(item); revision != "" {
				return revision
			}
		}
	case []any:
		for _, raw := range items {
			if item, ok := raw.(map[string]any); ok {
				if revision := endpointRevision(item); revision != "" {
					return revision
				}
			}
		}
	}
	return ""
}

func endpointRevision(item map[string]any) string {
	for _, key := range []string{"revision", "releaseId"} {
		if revision := metadataText(item[key]); revision != "" {
			return revision
		}
	}
	return ""
}

func rolloutOrchestrationMetadata(current map[string]any, installRoot, revision, ingressNetwork string, gatewayPort, webPort int, changedServices []string) map[string]any {
	next := copyMetadata(current)
	for key, value := range releaseOrchestrationMetadata(installRoot, revision, ingressNetwork, gatewayPort, webPort, servicesFromMetadata(current)) {
		if _, exists := next[key]; !exists {
			next[key] = value
		}
	}
	next["orchestrationModel"] = orchestrationModelServiceControllerV1
	next["releasePhase"] = releasePhaseActive
	activeEndpoints := activeEndpointsFromMetadata(current)
	serviceRevisions := serviceRevisionsFromMetadata(current)
	containers := mapFromMetadataValue(current["containers"])
	if containers == nil {
		containers = map[string]any{}
	}
	for _, service := range changedServices {
		serviceRevisions[service] = revision
	}
	delete(next, "desiredReplicas")
	next["activeEndpoints"] = activeEndpoints
	next["serviceRevisions"] = serviceRevisions
	next["containers"] = containers
	next["activeRoutes"] = releaseRoutes(gatewayPort, webPort)
	next["autoscalePolicy"] = autoscalePolicyFromMetadata(current).metadata()
	return next
}

func serviceRevisionsFromMetadata(metadata map[string]any) map[string]any {
	out := map[string]any{}
	if raw, ok := metadata["serviceRevisions"].(map[string]any); ok {
		for key, value := range raw {
			if revision := metadataText(value); revision != "" {
				out[key] = revision
			}
		}
	}
	for _, service := range serviceOrder {
		if _, ok := out[service]; !ok {
			if revision := currentRevisionForService(metadata, service); revision != "" {
				out[service] = revision
			}
		}
	}
	return out
}

func mapFromMetadataValue(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out
	default:
		return nil
	}
}

func applyEffectiveReleaseFields(manifest map[string]any, orchestration map[string]any) {
	if containers := mapFromMetadataValue(orchestration["containers"]); len(containers) > 0 {
		manifest["containers"] = containers
	}
	if routes := mapFromMetadataValue(orchestration["activeRoutes"]); len(routes) > 0 {
		manifest["routes"] = routes
	}
}

func releaseManifest(version, releaseID string, releaseTime time.Time, configHash, ingressNetwork string, gatewayPort, webPort int, services []string, deployments []store.AIFARDeployment) map[string]any {
	services = serviceListOrDefault(services)
	desired := desiredReplicasFromDeployments(deployments)
	endpoints := releaseActiveEndpointsForServices(releaseID, gatewayPort, webPort, services)
	manifest := map[string]any{
		"app":             AppName,
		"version":         version,
		"releaseId":       releaseID,
		"kind":            "full",
		"status":          "success",
		"phase":           releasePhaseActive,
		"configHash":      configHash,
		"createdAt":       releaseTime.Format(time.RFC3339),
		"services":        services,
		"desiredReplicas": desired,
		"endpoints":       endpoints,
		"activeServices":  activeServicesFromEndpointsForServices(desired, endpoints, services),
		"autoscalePolicy": defaultAutoscalePolicy().metadata(),
	}
	for key, value := range releaseManifestFields(releaseID, ingressNetwork, gatewayPort, webPort, services) {
		manifest[key] = value
	}
	return manifest
}

func rolloutReleaseManifest(version, releaseID string, releaseTime time.Time, configHash, baseReleaseID, ingressNetwork string, gatewayPort, webPort int, artifact artifactInfo, orchestration map[string]any, installRoot, actor, taskID string, before map[string]string) map[string]any {
	services := servicesFromMetadata(orchestration)
	changed := []string{artifact.ServiceName}
	after := serviceRevisionMapAfter(releaseID, changed)
	if before == nil {
		before = map[string]string{}
	}
	artifactRemotePath := releaseServiceArtifactPath(installRoot, releaseID, artifact.ServiceName, artifact.FileName)
	snapshotDir := releaseServiceSnapshotPath(installRoot, releaseID, artifact.ServiceName)
	manifest := map[string]any{
		"schema":                 releaseManifestSchemaV2,
		"app":                    AppName,
		"version":                version,
		"releaseId":              releaseID,
		"kind":                   "rollout",
		"status":                 "success",
		"phase":                  releasePhaseActive,
		"configHash":             configHash,
		"baseReleaseId":          baseReleaseID,
		"previousRevision":       baseReleaseID,
		"createdAt":              releaseTime.Format(time.RFC3339),
		"actor":                  actor,
		"taskId":                 taskID,
		"releaseDir":             releaseDirPath(installRoot, releaseID),
		"services":               services,
		"changedServices":        changed,
		"serviceRevisionsBefore": before,
		"serviceRevisionsAfter":  after,
		"artifacts": map[string]any{
			artifact.ServiceName: map[string]any{
				"type":       artifactTypeForService(artifact.ServiceName, artifact.FileName),
				"file":       artifact.FileName,
				"sha256":     artifact.SHA256,
				"size":       artifact.Size,
				"remotePath": artifactRemotePath,
			},
		},
		"snapshots": map[string]any{
			"envBefore": map[string]string{
				artifact.ServiceName: snapshotDir + "/before.env",
			},
		},
	}
	for key, value := range releaseManifestFields(releaseID, ingressNetwork, gatewayPort, webPort, services) {
		manifest[key] = value
	}
	applyEffectiveReleaseFields(manifest, orchestration)
	applyEffectiveServiceFields(manifest, orchestration)
	return manifest
}

func rolloutBundleReleaseManifest(version, releaseID string, releaseTime time.Time, configHash, baseReleaseID, ingressNetwork string, gatewayPort, webPort int, artifacts []artifactInfo, concurrency int, orchestration map[string]any, installRoot, actor, taskID string, before map[string]string) map[string]any {
	changed := make([]string, 0, len(artifacts))
	artifactMap := make(map[string]any, len(artifacts))
	envBefore := make(map[string]string, len(artifacts))
	for _, artifact := range artifacts {
		changed = append(changed, artifact.ServiceName)
		artifactMap[artifact.ServiceName] = map[string]any{
			"type":       artifactTypeForService(artifact.ServiceName, artifact.FileName),
			"file":       artifact.FileName,
			"sha256":     artifact.SHA256,
			"size":       artifact.Size,
			"remotePath": releaseServiceArtifactPath(installRoot, releaseID, artifact.ServiceName, artifact.FileName),
		}
		envBefore[artifact.ServiceName] = releaseServiceSnapshotPath(installRoot, releaseID, artifact.ServiceName) + "/before.env"
	}
	if before == nil {
		before = map[string]string{}
	}
	services := servicesFromMetadata(orchestration)
	manifest := map[string]any{
		"schema":                 releaseManifestSchemaV2,
		"app":                    AppName,
		"version":                version,
		"releaseId":              releaseID,
		"kind":                   "rollout-bundle",
		"status":                 "success",
		"phase":                  releasePhaseActive,
		"configHash":             configHash,
		"baseReleaseId":          baseReleaseID,
		"previousRevision":       baseReleaseID,
		"createdAt":              releaseTime.Format(time.RFC3339),
		"actor":                  actor,
		"taskId":                 taskID,
		"releaseDir":             releaseDirPath(installRoot, releaseID),
		"services":               services,
		"changedServices":        changed,
		"serviceRevisionsBefore": before,
		"serviceRevisionsAfter":  serviceRevisionMapAfter(releaseID, changed),
		"deploymentConcurrency":  concurrency,
		"artifacts":              artifactMap,
		"snapshots": map[string]any{
			"envBefore": envBefore,
		},
	}
	for key, value := range releaseManifestFields(releaseID, ingressNetwork, gatewayPort, webPort, services) {
		manifest[key] = value
	}
	applyEffectiveReleaseFields(manifest, orchestration)
	applyEffectiveServiceFields(manifest, orchestration)
	return manifest
}

func artifactTypeForService(service, fileName string) string {
	if cleanAIFARServiceName(service) == "web-vue3" {
		return "web"
	}
	ext := strings.ToLower(filepath.Ext(fileName))
	if ext == ".zip" || ext == ".tar" || ext == ".tgz" || ext == ".gz" {
		return "web"
	}
	return "java"
}

type artifactInfo struct {
	ServiceName string
	FileName    string
	SHA256      string
	Size        int64
}

func (s Service) ValidateArtifactUpdate(req ArtifactUpdateRequest) error {
	copy := updateCopyFor(req.Language)
	if err := ensureK8sLikeInstance(req.Instance, copy); err != nil {
		return err
	}
	if req.ExpectedGeneration <= 0 {
		return errors.New(i18n.Text(req.Language, "aifar.runtimeDeployment.invalidRequest"))
	}
	_, err := artifactInfoFromRequest(req, copy)
	return err
}

func (s Service) UpdateArtifact(ctx context.Context, req ArtifactUpdateRequest, log Logger, targetLog targetLogger) (resultErr error) {
	copy := updateCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, updateSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)
	serviceName := cleanAIFARServiceName(req.ServiceName)
	lock := store.AIFAROrchestrationLock{ID: strings.TrimSpace(req.LockID), InstanceID: req.Instance.ID, ServiceName: serviceName, Operation: "update-artifact", TaskID: fallbackTaskID(req.TaskID, log)}
	if lock.ID == "" {
		lockedInstance, acquired, err := s.acquireOrchestrationLock(req.Instance.ID, lock.Operation, serviceName, req.Actor, lock.TaskID)
		if err != nil {
			msg := fmt.Sprintf(copy.UpdateFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		lock = acquired
		defer s.releaseOrchestrationLock(lock)
		lockedCtx, stopHeartbeat := s.startAIFAROrchestrationLockHeartbeat(ctx, lock)
		defer stopHeartbeat()
		ctx = lockedCtx
		req.Instance = lockedInstance
	} else {
		if err := s.validatePreheldArtifactLock(ctx, lock); err != nil {
			return err
		}
		freshInstance, err := s.store.GetAppInstance(req.Instance.ID)
		if err != nil {
			return err
		}
		req.Instance = freshInstance
	}
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	currentDeployment, err := loadDeploymentForMutation(control, req.Instance.ID, serviceName)
	if err != nil {
		return err
	}
	if currentDeployment.Generation != req.ExpectedGeneration {
		return deploymentError(deploymentGenerationConflictCode, deploymentGenerationConflictCode, "aifar.deploymentControl.generationConflict")
	}

	var artifact artifactInfo
	var metadata map[string]any
	var installRoot string
	var version string
	var releaseTime time.Time
	var releaseID string
	var baseReleaseID string
	var configHash string
	var ingressNetwork string
	var gatewayPort int
	var webPort int
	var workDir string
	var releaseDir string
	var artifactRemote string
	var releaseArtifact string
	var scriptRemote string
	var serviceRevisionsBefore map[string]string
	var pendingRelease *store.AppRelease
	defer func() {
		if resultErr == nil || pendingRelease == nil {
			return
		}
		if err := s.markRecordedReleaseFailed(pendingRelease, resultErr); err != nil {
			logForServer.Error("%s", fmt.Sprintf(copy.RecordFailed, err))
		}
	}()

	if err := step(1, func() error {
		var err error
		artifact, err = artifactInfoFromRequest(req, copy)
		if err != nil {
			return err
		}
		metadata = metadataFromInstance(req.Instance)
		if err := ensureK8sLikeMetadata(metadata, copy); err != nil {
			return err
		}
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		version = stringFromMetadata(metadata, "releaseVersion", req.Instance.Version)
		if strings.TrimSpace(version) == "" {
			version = appBundleVersion
		}
		baseReleaseID = currentRevisionForService(metadata, artifact.ServiceName)
		serviceRevisionsBefore = serviceRevisionMapBefore(metadata, []string{artifact.ServiceName})
		releaseTime = time.Now().UTC()
		releaseID = newReleaseID("rollout-"+artifact.ServiceName, releaseTime)
		configHash = partialUpdateConfigHash(stringFromMetadata(metadata, "configHash", ""), artifact.ServiceName, artifact.FileName, artifact.SHA256)
		ingressNetwork = stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName))
		gatewayPort = intFromMetadata(metadata, "gatewayPort", defaultGatewayPort)
		webPort = intFromMetadata(metadata, "webPort", defaultWebPort)
		deployDir := installerkit.RemoteDeployDir(req.Server.DeployDir)
		workDir = installerkit.WorkDir(deployDir, AppName+"-"+artifact.ServiceName, version, releaseTime)
		releaseDir = releaseDirPath(installRoot, releaseID)
		artifactRemote = workDir + "/" + installerkit.Sanitize(artifact.FileName)
		releaseArtifact = releaseServiceArtifactPath(installRoot, releaseID, artifact.ServiceName, artifact.FileName)
		scriptRemote = workDir + "/update-aifar-artifact.sh"
		if releases, ok := s.store.(releaseStore); ok {
			orchestration := rolloutAcceptedIntentMetadata(metadata, releaseID, []string{artifact.ServiceName})
			manifest := rolloutReleaseManifest(version, releaseID, releaseTime, configHash, baseReleaseID, ingressNetwork, gatewayPort, webPort, artifact, orchestration, installRoot, req.Actor, fallbackTaskID(req.TaskID, log), serviceRevisionsBefore)
			manifest["status"] = "pending"
			manifest["phase"] = "pending"
			raw, _ := json.Marshal(manifest)
			saved, err := releases.SaveAppRelease(store.AppRelease{
				InstanceID:   req.Instance.ID,
				App:          AppName,
				Version:      version,
				ReleaseID:    releaseID,
				ServerID:     target,
				Status:       "pending",
				ManifestJSON: string(raw),
				ConfigHash:   configHash,
				CreatedAt:    releaseTime,
			})
			if err != nil {
				return err
			}
			pendingRelease = &saved
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(2, func() error {
		logForServer.Info(copy.PrepareWorkDir, workDir)
		if _, err := installerkit.Run(ctx, s.remote, req.Server, "mkdir -p "+installerkit.ShellQuote(workDir), logForServer, copy.RemoteCommandFailed); err != nil {
			return err
		}
		if err := uploadkit.Upload(ctx, s.remote, req.Server, uploadkit.File{
			LocalPath:      req.ArtifactLocalPath,
			RemotePath:     artifactRemote,
			LogMessage:     copy.UploadArtifact,
			LogArgs:        []any{artifact.ServiceName, artifact.FileName},
			FailureMessage: copy.UploadArtifactFailed,
		}, logForServer); err != nil {
			return err
		}
		script, err := renderUpdateScript(updateScriptData{
			InstallRoot:      installRoot,
			WorkDir:          workDir,
			ReleaseDir:       releaseDir,
			ServiceOrder:     strings.Join(servicesFromMetadata(metadata), " "),
			ServiceName:      artifact.ServiceName,
			ArtifactRemote:   artifactRemote,
			ReleaseArtifact:  releaseArtifact,
			ArtifactFileName: artifact.FileName,
			ArtifactSHA256:   artifact.SHA256,
			ArtifactSize:     artifact.Size,
			Version:          version,
			ReleaseID:        releaseID,
			CreatedAt:        releaseTime.Format(time.RFC3339),
			ConfigHash:       configHash,
			IngressNetwork:   ingressNetwork,
		})
		if err != nil {
			return err
		}
		scriptLocal, err := installerkit.WriteTempScript("aifar-service-update-*.sh", script)
		if err != nil {
			return err
		}
		defer os.Remove(scriptLocal)
		return uploadkit.Upload(ctx, s.remote, req.Server, uploadkit.File{
			LocalPath:      scriptLocal,
			RemotePath:     scriptRemote,
			Mode:           0o755,
			LogMessage:     copy.UploadScript,
			FailureMessage: copy.UploadScriptFailed,
		}, logForServer)
	}); err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(3, func() error {
		logForServer.Info(copy.Deploying, artifact.ServiceName)
		_, err := installerkit.Run(ctx, s.remote, req.Server, "sh "+installerkit.ShellQuote(scriptRemote), logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(4, func() error {
		var saved store.AppInstance
		var orchestration map[string]any
		accepted, err := s.mutateDeploymentsFanOut(ctx, req.Instance, req.Server, req.Actor, fallbackTaskID(req.TaskID, log), req.Language, 1, []deploymentMutationPlan{{
			ServiceName:     artifact.ServiceName,
			Operation:       "update-artifact",
			LockAlreadyHeld: true,
			LockID:          lock.ID,
			Validate: func(_ store.AppInstance, deployment store.AIFARDeployment) error {
				if deployment.Generation != req.ExpectedGeneration {
					return deploymentError(deploymentGenerationConflictCode, deploymentGenerationConflictCode, "aifar.deploymentControl.generationConflict")
				}
				return nil
			},
			Mutate: func(manifest *runtimeagent.DeploymentManifest) error {
				manifest.Spec.PodRevision = releaseID
				manifest.Spec.Image = "aifar-" + artifact.ServiceName + ":" + releaseID
				return nil
			},
			Project: func(projectCtx context.Context, projectLock store.AIFAROrchestrationLock, acceptedDeployment store.AIFARDeployment) error {
				audit := map[string]any{
					"service":        artifact.ServiceName,
					"artifactFile":   artifact.FileName,
					"artifactSHA256": artifact.SHA256,
					"artifactSize":   artifact.Size,
					"baseReleaseId":  baseReleaseID,
					"releaseId":      releaseID,
					"updatedAt":      releaseTime.Format(time.RFC3339),
				}
				var projectErr error
				saved, projectErr = s.updateAcceptedDeploymentMetadata(projectCtx, projectLock, acceptedDeployment, "AIFAR_ARTIFACT_METADATA_REPAIR_REQUIRED", func(freshMetadata map[string]any) error {
					freshMetadata["releaseVersion"] = version
					configHash = partialUpdateConfigHash(stringFromMetadata(freshMetadata, "configHash", ""), artifact.ServiceName, artifact.FileName, artifact.SHA256)
					orchestration = mergeServiceScopedRolloutMetadata(freshMetadata, releaseID, configHash, []string{artifact.ServiceName}, audit)
					return nil
				})
				return projectErr
			},
		}}, log, targetLog)
		if err != nil {
			return err
		}
		if len(accepted) != 1 {
			return repairRequired("AIFAR_ARTIFACT_ACCEPTANCE_INCOMPLETE", nil)
		}
		if err := ctx.Err(); err != nil {
			return repairRequired("AIFAR_ARTIFACT_METADATA_REPAIR_REQUIRED", err)
		}
		if releases, ok := s.store.(releaseStore); ok {
			manifest, _ := json.Marshal(rolloutReleaseManifest(version, releaseID, releaseTime, configHash, baseReleaseID, ingressNetwork, gatewayPort, webPort, artifact, orchestration, installRoot, req.Actor, fallbackTaskID(req.TaskID, log), serviceRevisionsBefore))
			if _, err := releases.SaveAppRelease(store.AppRelease{
				InstanceID:   saved.ID,
				App:          AppName,
				Version:      version,
				ReleaseID:    releaseID,
				ServerID:     target,
				Status:       "success",
				ManifestJSON: string(manifest),
				ConfigHash:   configHash,
				CreatedAt:    releaseTime,
				ActivatedAt:  releaseTime,
			}); err != nil {
				return repairRequired("AIFAR_ARTIFACT_RELEASE_REPAIR_REQUIRED", err)
			}
			pendingRelease = nil
			if _, err := releases.DeleteOldAppReleases(saved.ID, releaseKeepCount); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf(copy.RecordFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	logForServer.Info(copy.Updated, releaseID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) validatePreheldArtifactLock(ctx context.Context, expected store.AIFAROrchestrationLock) error {
	inspector, ok := s.store.(interface {
		ListAIFAROrchestrationLocks(instanceID string, activeOnly bool) ([]store.AIFAROrchestrationLock, error)
	})
	if !ok {
		return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_FENCE_UNAVAILABLE", nil)
	}
	locks, err := inspector.ListAIFAROrchestrationLocks(expected.InstanceID, true)
	if err != nil {
		return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
	}
	for _, lock := range locks {
		if lock.ID == expected.ID && lock.InstanceID == expected.InstanceID &&
			cleanAIFARServiceName(lock.ServiceName) == cleanAIFARServiceName(expected.ServiceName) &&
			strings.TrimSpace(lock.Operation) == strings.TrimSpace(expected.Operation) &&
			strings.TrimSpace(lock.TaskID) == strings.TrimSpace(expected.TaskID) {
			return s.ensureAIFAROrchestrationLockOwnership(ctx, lock)
		}
	}
	return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", store.ErrAIFAROrchestrationLockOwnership)
}

func artifactInfoFromRequest(req ArtifactUpdateRequest, copy UpdateCopy) (artifactInfo, error) {
	if req.Instance.App != AppName {
		return artifactInfo{}, errors.New(copy.UnsupportedInstance)
	}
	if strings.TrimSpace(req.Instance.ServerID) == "" && strings.TrimSpace(req.Server.ID) == "" {
		return artifactInfo{}, errors.New(copy.TargetRequired)
	}
	serviceName := cleanAIFARServiceName(req.ServiceName)
	if !aifarServiceSupported(serviceName) {
		return artifactInfo{}, fmt.Errorf(copy.UnsupportedService, req.ServiceName)
	}
	localPath := strings.TrimSpace(req.ArtifactLocalPath)
	if localPath == "" {
		return artifactInfo{}, errors.New(copy.ArtifactRequired)
	}
	stat, err := os.Stat(localPath)
	if err != nil {
		return artifactInfo{}, err
	}
	if stat.IsDir() || stat.Size() == 0 {
		return artifactInfo{}, errors.New(copy.ArtifactRequired)
	}
	fileName := strings.TrimSpace(req.ArtifactFileName)
	if fileName == "" {
		fileName = filepath.Base(localPath)
	}
	fileName = filepath.Base(fileName)
	if fileName == "." || fileName == string(filepath.Separator) || strings.TrimSpace(fileName) == "" {
		return artifactInfo{}, errors.New(copy.ArtifactRequired)
	}
	if !artifactTypeAllowedForInstance(req.Instance, serviceName, fileName) {
		return artifactInfo{}, fmt.Errorf(copy.ArtifactTypeInvalid, serviceName)
	}
	if !artifactFileMatchesService(serviceName, fileName) {
		return artifactInfo{}, fmt.Errorf(copy.ArtifactTypeInvalid, serviceName)
	}
	sum, size, err := fileSHA256(localPath)
	if err != nil {
		return artifactInfo{}, err
	}
	return artifactInfo{ServiceName: serviceName, FileName: fileName, SHA256: sum, Size: size}, nil
}

func cleanAIFARServiceName(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func aifarServiceSupported(serviceName string) bool {
	return serviceNamePattern.MatchString(cleanAIFARServiceName(serviceName))
}

func artifactTypeAllowed(serviceName, fileName string) bool {
	name := strings.ToLower(strings.TrimSpace(fileName))
	if serviceName == "web-vue3" {
		return strings.HasSuffix(name, ".zip") ||
			strings.HasSuffix(name, ".tar") ||
			strings.HasSuffix(name, ".tgz") ||
			strings.HasSuffix(name, ".tar.gz")
	}
	return strings.HasSuffix(name, ".jar")
}

func artifactTypeAllowedForInstance(instance store.AppInstance, serviceName, fileName string) bool {
	definitions := serviceDefinitionsFromMetadata(metadataFromInstance(instance))
	if definition, ok := catalogDefinition(definitions, serviceName); ok {
		name := strings.ToLower(strings.TrimSpace(fileName))
		for _, extension := range definition.ArtifactExtensions {
			if strings.HasSuffix(name, strings.ToLower(strings.TrimSpace(extension))) {
				return true
			}
		}
		return false
	}
	return artifactTypeAllowed(serviceName, fileName)
}

func artifactFileMatchesService(serviceName, fileName string) bool {
	if serviceName == "web-vue3" {
		return true
	}
	hint := artifactFileServiceHint(fileName)
	return hint == "" || hint == serviceName
}

func artifactFileServiceHint(fileName string) string {
	name := strings.ToLower(filepath.Base(strings.TrimSpace(fileName)))
	tokens := strings.FieldsFunc(name, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for _, token := range tokens {
		for _, service := range serviceOrder {
			if service == "web-vue3" {
				continue
			}
			if token == service {
				return service
			}
		}
	}
	return ""
}

func fileSHA256(path string) (string, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func (s Service) existingAIFARInstanceID(serverID, installRoot string) (string, error) {
	return s.reusableAIFARInstallInstanceID(serverID, installRoot)
}

func installAttemptClaimInstanceID(serverID, installRoot string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(serverID) + "\x00" + normalizeInstallRoot(installRoot)))
	return "aifarinstall_" + hex.EncodeToString(sum[:12])
}

func (s Service) acquireInstallAttemptClaim(serverID, installRoot, actor, taskID string) (string, store.AIFAROrchestrationLock, error) {
	instanceID, err := s.reusableAIFARInstallInstanceID(serverID, installRoot)
	if err != nil {
		return "", store.AIFAROrchestrationLock{}, err
	}
	if instanceID != "" {
		_, lock, err := s.acquireOrchestrationLock(instanceID, "install", "", actor, taskID)
		return instanceID, lock, err
	}

	lockStore, ok := s.store.(aifarOrchestrationLockStore)
	if !ok {
		return "", store.AIFAROrchestrationLock{}, nil
	}
	instanceID = installAttemptClaimInstanceID(serverID, installRoot)
	now := time.Now().UTC()
	lock, err := lockStore.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instanceID, Operation: "install", Actor: strings.TrimSpace(actor), TaskID: strings.TrimSpace(taskID),
		StartedAt: now, ExpiresAt: now.Add(orchestrationLockTTL),
	})
	if err != nil {
		var conflict store.AIFAROrchestrationLockConflict
		if errors.As(err, &conflict) {
			return "", store.AIFAROrchestrationLock{}, orchestrationConflictError("", conflict.Lock.ServiceName)
		}
		return "", store.AIFAROrchestrationLock{}, err
	}
	releaseOnError := func(claimErr error) (string, store.AIFAROrchestrationLock, error) {
		s.releaseOrchestrationLock(lock)
		return "", store.AIFAROrchestrationLock{}, claimErr
	}
	claimed, getErr := s.store.GetAppInstance(instanceID)
	if getErr == nil {
		claimedMetadata := metadataFromInstance(claimed)
		if claimed.App != AppName || claimed.ServerID != serverID || normalizeInstallRoot(stringFromMetadata(claimedMetadata, "installRoot", "")) != normalizeInstallRoot(installRoot) || !s.reusableAIFARInstallInstance(claimed, claimedMetadata) {
			return releaseOnError(repairRequired("AIFAR_RUNTIME_INSTALL_ATTEMPT_OWNERSHIP_CHANGED", nil))
		}
		return instanceID, lock, nil
	}
	if !store.IsNotFound(getErr) {
		return releaseOnError(getErr)
	}
	concurrentID, listErr := s.reusableAIFARInstallInstanceID(serverID, installRoot)
	if listErr != nil {
		return releaseOnError(listErr)
	}
	if concurrentID != "" && concurrentID != instanceID {
		return releaseOnError(repairRequired("AIFAR_RUNTIME_INSTALL_ATTEMPT_OWNERSHIP_CHANGED", nil))
	}
	return instanceID, lock, nil
}

func (s Service) reusableAIFARInstallInstanceID(serverID, installRoot string) (string, error) {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return "", err
	}
	targetRoot := normalizeInstallRoot(installRoot)
	for _, candidate := range instances {
		if candidate.App != AppName || candidate.ServerID != serverID {
			continue
		}
		metadata := metadataFromInstance(candidate)
		candidateRoot := normalizeInstallRoot(stringFromMetadata(metadata, "installRoot", ""))
		if candidateRoot != "" && candidateRoot == targetRoot {
			if s.reusableAIFARInstallInstance(candidate, metadata) {
				return candidate.ID, nil
			}
			return "", fmt.Errorf("AIFAR service already exists on this server at %s; uninstall it before installing again", installRoot)
		}
	}
	return "", nil
}

func (s Service) reusableAIFARInstallInstance(instance store.AppInstance, metadata map[string]any) bool {
	if aifarInstallFailedInstance(instance, metadata) {
		return true
	}
	if !aifarInstallingInstance(instance, metadata) {
		return false
	}
	taskID := stringFromMetadata(metadata, "taskId", "")
	if taskID == "" {
		return true
	}
	tasks, ok := s.store.(taskLookupStore)
	if !ok {
		return false
	}
	task, _, err := tasks.GetTask(taskID)
	if err != nil {
		return store.IsNotFound(err)
	}
	return !activeTaskStatus(task.Status)
}

func aifarInstallingInstance(instance store.AppInstance, metadata map[string]any) bool {
	status := strings.ToLower(strings.TrimSpace(instance.Status))
	state := strings.ToLower(strings.TrimSpace(fmt.Sprint(metadata["installState"])))
	return status == "installing" || state == "installing"
}

func activeTaskStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "running":
		return true
	default:
		return false
	}
}

func aifarInstallFailedInstance(instance store.AppInstance, metadata map[string]any) bool {
	switch strings.ToLower(strings.TrimSpace(instance.Status)) {
	case "failed", "install_failed":
		return true
	}
	return boolFromMetadata(metadata, "installFailed")
}

func boolFromMetadata(metadata map[string]any, key string) bool {
	switch value := metadata[key].(type) {
	case bool:
		return value
	case string:
		text := strings.TrimSpace(strings.ToLower(value))
		return text == "true" || text == "1" || text == "yes"
	default:
		return false
	}
}

func (s Service) markInstallFailed(instance store.AppInstance, metadata map[string]any, installErr error) error {
	if strings.TrimSpace(instance.ID) == "" {
		return nil
	}
	owner := stringFromMetadata(metadata, "installAttemptOwner", "")
	current, owned, err := s.currentInstallAttemptOwner(instance.ID, owner)
	if err != nil {
		if store.IsNotFound(err) {
			return nil
		}
		return err
	}
	if !owned || strings.EqualFold(strings.TrimSpace(current.Status), "installed") {
		return nil
	}
	next := copyMetadata(metadata)
	next["installFailed"] = true
	next["installState"] = "install_failed"
	next["failedAt"] = time.Now().UTC().Format(time.RFC3339)
	next["error"] = truncateAIFARError(installErr)
	raw, _ := json.Marshal(next)
	instance.Status = "install_failed"
	instance.Metadata = string(raw)
	instance.CreatedAt = current.CreatedAt
	_, err = s.store.SaveAppInstance(instance)
	return err
}

func (s Service) currentInstallAttemptOwner(instanceID, owner string) (store.AppInstance, bool, error) {
	owner = strings.TrimSpace(owner)
	if owner == "" {
		return store.AppInstance{}, false, nil
	}
	if lockStore, ok := s.store.(aifarOrchestrationLockStore); ok {
		renewed, err := lockStore.RenewAIFAROrchestrationLock(owner, time.Now().UTC().Add(orchestrationLockTTL))
		if err != nil || !renewed {
			return store.AppInstance{}, false, err
		}
	}
	current, err := s.store.GetAppInstance(instanceID)
	if err != nil {
		return current, false, err
	}
	return current, stringFromMetadata(metadataFromInstance(current), "installAttemptOwner", "") == owner, nil
}

func truncateAIFARError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	const max = 500
	if len(text) <= max {
		return text
	}
	return text[:max] + "..."
}

func normalizeInstallRoot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	for len(value) > 1 && strings.HasSuffix(value, "/") {
		value = strings.TrimSuffix(value, "/")
	}
	return value
}

func (s Service) ensureDockerRuntimeReady(serverID string, copy Copy) error {
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if instance.App == "docker" && instance.ServerID == serverID && dockerRuntimeReady(instance) {
			return nil
		}
	}
	return errors.New(copy.DockerRuntimeRequired)
}

func dockerRuntimeReady(instance store.AppInstance) bool {
	if !dockerStatusReady(instance.Status) {
		return false
	}
	metadata := metadataFromInstance(instance)
	lastCheck, ok := metadata["lastCheck"].(map[string]any)
	if !ok {
		return true
	}
	if status := mapText(lastCheck, "status"); status != "" && !dockerStatusReady(status) {
		return false
	}
	return mapText(lastCheck, "dockerVersion") != ""
}

func dockerStatusReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "installed", "running", "available", "ok", "success":
		return true
	default:
		return false
	}
}

func mapText(values map[string]any, key string) string {
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log Logger, targetLog targetLogger) error {
	copy := deleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, deleteSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)
	lockedInstance, lock, err := s.acquireOrchestrationLock(req.Instance.ID, "delete", "", req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	defer s.releaseOrchestrationLock(lock)
	req.Instance = lockedInstance
	metadata := metadataFromInstance(req.Instance)
	networkName := stringFromMetadata(metadata, "networkName", defaultNetworkName)
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
	specPath := stringFromMetadata(metadata, "runtimeSpecPath", runtimeSpecPath(installRoot))

	if err := step(1, func() error {
		_, err := installerkit.Run(ctx, s.remote, req.Server, runtimeAgentUninstallCommand(installRoot, specPath), logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(2, func() error {
		script, err := renderUninstallScript(uninstallScriptData{
			InstallRoot:  installRoot,
			NetworkName:  networkName,
			ServiceOrder: reverseServiceOrderText(),
		})
		if err != nil {
			return err
		}
		_, err = installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_SERVICE_UNINSTALL'\n"+script+"\nAIFAR_SERVICE_UNINSTALL", logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(3, func() error {
		status, err := NewInspector(s.remote).Check(ctx, req.Server, installRoot, nil, logForServer)
		if err != nil {
			return err
		}
		if status.InstallRootExists {
			return fmt.Errorf("AIFAR service install root still exists: %s", installRoot)
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(4, func() error {
		return s.store.DeleteAppInstance(req.Instance.ID)
	}); err != nil {
		msg := fmt.Sprintf(copy.DeleteFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	logForServer.Info(copy.Deleted, req.Instance.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) Check(ctx context.Context, req CheckRequest, log Logger, targetLog targetLogger) (CheckResult, error) {
	copy := checkCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, checkSteps(copy), copy.StepStart, copy.StepDone, copy.StepFailed)
	metadata := metadataFromInstance(req.Instance)
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
	var status StatusResult
	var scaleStatus autoscaleStatus
	var scaleStatusOK bool
	var deployments []store.AIFARDeployment
	if err := step(1, func() error {
		control, ok := s.store.(aifarDeploymentControlStore)
		if !ok {
			return errors.New("AIFAR Deployment store is unavailable")
		}
		var listErr error
		deployments, listErr = control.ListAIFARDeployments(req.Instance.ID)
		if listErr != nil {
			return listErr
		}
		var checkErr error
		status, checkErr = NewInspector(s.remote).Check(ctx, req.Server, installRoot, serviceExpectations(deployments), logForServer)
		if checkErr == nil && !strings.EqualFold(strings.TrimSpace(req.Actor), "collector") {
			if collected, collectErr := collectAutoscaleStatus(ctx, s.remote, req.Server, installRoot); collectErr == nil {
				scaleStatus = collected
				scaleStatusOK = true
			}
		}
		return checkErr
	}); err != nil {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		_ = s.recordInstanceObservation(req.Instance.ID, map[string]any{"message": msg})
		return CheckResult{Status: "error", Message: msg}, err
	}
	details := map[string]any{
		"message":             status.Message,
		"installRoot":         status.InstallRoot,
		"orchestrationModel":  status.OrchestrationModel,
		"legacy":              status.OrchestrationModel == legacyOrchestrationModel,
		"releaseId":           status.ReleaseID,
		"installRootExists":   status.InstallRootExists,
		"totalContainers":     status.TotalContainers,
		"runningContainers":   status.RunningContainers,
		"unhealthyContainers": status.UnhealthyContainers,
		"expectedContainers":  status.ExpectedContainers,
		"missingContainers":   status.MissingContainers,
		"staleContainers":     status.StaleContainers,
		"ingressRunning":      status.IngressRunning,
		"containers":          status.Containers,
	}
	if scaleStatusOK {
		details["activeEndpoints"] = activeEndpointsFromMetrics(scaleStatus.Endpoints)
		details["activeServices"] = activeServicesFromEndpoints(desiredReplicasFromDeployments(deployments), activeEndpointsFromMetrics(scaleStatus.Endpoints))
		details["autoscaleMetrics"] = metricsMetadata(scaleStatus.Endpoints, time.Now().UTC())
	}
	if err := step(2, func() error {
		return s.recordInstanceObservation(req.Instance.ID, details)
	}); err != nil {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "error", Message: msg}, err
	}
	logForServer.Info(copy.Checked, status.Status)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: status.Status, Message: status.Message, Details: details}, nil
}

func (s Service) recordInstanceObservation(instanceID string, details map[string]any) error {
	_, err := s.updateAppInstanceMetadata(instanceID, "AIFAR_RUNTIME_CHECK_METADATA_REPAIR_REQUIRED", func(metadata map[string]any) error {
		metadata["lastCheck"] = details
		return nil
	})
	return err
}

type installScriptData struct {
	InstallRoot         string
	InstanceID          string
	WorkDir             string
	ArchiveRemote       string
	AgentBinaryRemote   string
	ServiceOrder        string
	ServiceApplications string
	ServicePorts        string
	ServiceKinds        string
	ServiceHealthPaths  string
	ServiceAffinities   string
	ServiceSpecHashes   string
	GatewayService      string
	WebService          string
	Version             string
	ReleaseID           string
	CreatedAt           string
	ConfigHash          string
	IngressNetwork      string
	Options             InstallOptions
}

type uninstallScriptData struct {
	InstallRoot  string
	NetworkName  string
	ServiceOrder string
}

type updateScriptData struct {
	InstallRoot      string
	WorkDir          string
	ReleaseDir       string
	ServiceOrder     string
	ServiceName      string
	ArtifactRemote   string
	ReleaseArtifact  string
	ArtifactFileName string
	ArtifactSHA256   string
	ArtifactSize     int64
	Version          string
	ReleaseID        string
	CreatedAt        string
	ConfigHash       string
	IngressNetwork   string
}

type bundleUpdateScriptArtifact struct {
	ServiceName     string
	ArtifactRemote  string
	ReleaseArtifact string
	ArtifactFile    string
	ArtifactSHA256  string
	ArtifactSize    int64
}

type bundleUpdateScriptData struct {
	InstallRoot     string
	WorkDir         string
	ReleaseDir      string
	ServiceOrder    string
	ChangedServices string
	Artifacts       []bundleUpdateScriptArtifact
	Version         string
	ReleaseID       string
	CreatedAt       string
	ConfigHash      string
	IngressNetwork  string
	Concurrency     int
}

type rollbackScriptData struct {
	InstallRoot      string
	WorkDir          string
	RollbackDir      string
	ServiceOrder     string
	ServiceName      string
	ArtifactRemote   string
	ArtifactFileName string
	ArtifactSHA256   string
	TargetRevision   string
	RollbackID       string
	CreatedAt        string
	ConfigHash       string
	IngressNetwork   string
}

func renderInstallScript(data installScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/install.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "install.sh", "aifar-install", string(content), selinux.AddTemplateFuncs(template.FuncMap{
		"quote": shellQuoteAny,
	}), data)
}

func renderUninstallScript(data uninstallScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/uninstall.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "uninstall.sh", "aifar-uninstall", string(content), template.FuncMap{
		"quote": shellQuoteAny,
	}, data)
}

func renderUpdateScript(data updateScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/update-artifact.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "update-artifact.sh", "aifar-update", string(content), template.FuncMap{
		"quote": shellQuoteAny,
	}, data)
}

func renderBundleUpdateScript(data bundleUpdateScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/update-artifact-bundle.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "update-artifact-bundle.sh", "aifar-update-bundle", string(content), template.FuncMap{
		"quote": shellQuoteAny,
	}, data)
}

func renderRollbackScript(data rollbackScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/rollback-artifact.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "rollback-artifact.sh", "aifar-rollback", string(content), template.FuncMap{
		"quote": shellQuoteAny,
	}, data)
}

func renderRuntimeConfigScript(data runtimeConfigScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/runtime-config.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "runtime-config.sh", "aifar-runtime-config", string(content), template.FuncMap{
		"quote": shellQuoteAny,
	}, data)
}

func shellQuoteAny(value any) string {
	return installerkit.ShellQuote(fmt.Sprint(value))
}

func installRootFromDeployDir(deployDir string) string {
	return installerkit.InstallRoot(installerkit.RemoteDeployDir(deployDir), installDirName)
}

func logForTarget(fallback Logger, targetLog targetLogger, target string) Logger {
	if targetLog == nil {
		return fallback
	}
	if log := targetLog(target); log != nil {
		return log
	}
	return fallback
}

func newStepRunner(log Logger, recorder stepRecorder, target string, steps []installStepDef, start, done, failed string) func(stepIndex int, fn func() error) error {
	return func(stepIndex int, fn func() error) error {
		stepName := fmt.Sprintf("step-%d", stepIndex)
		label := stepName
		if stepIndex > 0 && stepIndex <= len(steps) {
			stepName = steps[stepIndex-1].Name
			label = steps[stepIndex-1].Title
		}
		stepTotal := len(steps)
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(start, stepIndex, stepTotal, label)
		if err := fn(); err != nil {
			log.Error(failed, stepIndex, stepTotal, label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(done, stepIndex, stepTotal, label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func finishTarget(recorder stepRecorder, target, status, errText string) {
	if recorder != nil {
		recorder.FinishTarget(target, status, errText)
	}
}

func metadataFromInstance(instance store.AppInstance) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return metadata
}

func stringFromMetadata(metadata map[string]any, key, fallback string) string {
	if value, ok := metadata[key]; ok {
		if text := metadataText(value); text != "" {
			return text
		}
	}
	return fallback
}

func metadataText(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	switch strings.ToLower(text) {
	case "", "<nil>", "<no value>", "nil", "null":
		return ""
	default:
		return text
	}
}

func installSteps(copy Copy) []installStepDef {
	return []installStepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "record-desired-instance", Title: copy.RecordInstance},
		{Name: "upload-bundle", Title: copy.UploadBundleStep},
		{Name: "deploy-runtime", Title: copy.DeployCompose},
		{Name: "record-installed-instance", Title: copy.RecordInstance},
	}
}

func deleteSteps(copy DeleteCopy) []installStepDef {
	return []installStepDef{
		{Name: "remove-agent-runtime", Title: copy.RemoveAgent},
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "verify-removed", Title: copy.VerifyRemoved},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func checkSteps(copy CheckCopy) []installStepDef {
	return []installStepDef{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}

func updateSteps(copy UpdateCopy) []installStepDef {
	return []installStepDef{
		{Name: "validate-artifact", Title: copy.ValidateRequest},
		{Name: "upload-artifact", Title: copy.UploadArtifactStep},
		{Name: "apply-update", Title: copy.ApplyUpdate},
		{Name: "record-release", Title: copy.RecordRelease},
	}
}
