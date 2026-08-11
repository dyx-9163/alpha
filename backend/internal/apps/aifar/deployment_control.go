package aifar

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

const (
	deploymentGenerationConflictCode = "AIFAR_RUNTIME_DEPLOYMENT_GENERATION_CONFLICT"
	agentStaleGenerationCode         = "AIFAR_RUNTIME_AGENT_STALE_GENERATION"
	agentGenerationConflictCode      = "AIFAR_RUNTIME_AGENT_GENERATION_CONFLICT"
	agentRejectedManifestCode        = "AIFAR_RUNTIME_AGENT_REJECTED_MANIFEST"
	agentUnavailableCode             = "AIFAR_RUNTIME_AGENT_UNAVAILABLE"
	agentAcceptanceInvalidCode       = "AIFAR_RUNTIME_AGENT_ACCEPTANCE_INVALID"
	remoteCleanupFailedCode          = "AIFAR_RUNTIME_REMOTE_CLEANUP_FAILED"
)

var (
	deploymentSpecHashPattern        = regexp.MustCompile(`^[a-f0-9]{64}$`)
	errAmbiguousDeploymentAcceptance = errors.New("ambiguous deployment acceptance")
	errExplicitlyInvalidAcceptance   = errors.New("explicitly invalid deployment acceptance")
)

type DeploymentMutationRequest struct {
	Instance           store.AppInstance
	Server             store.Server
	ServiceName        string
	ExpectedGeneration int64
	Actor              string
	TaskID             string
	Operation          string
	LockID             string
	InitialDeployment  *store.AIFARDeployment
	Mutate             func(*runtimeagent.DeploymentManifest) error
}

type deploymentControlError struct {
	code       string
	reasonCode string
	message    string
	ambiguous  bool
	cleanupErr bool
	cause      error
}

func (e *deploymentControlError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.message) == "" {
		return e.code
	}
	return e.code + ": " + e.message
}

func (e *deploymentControlError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}
func (e *deploymentControlError) StableCode() string {
	if e == nil {
		return ""
	}
	return e.code
}
func (e *deploymentControlError) ReasonCode() string {
	if e == nil {
		return ""
	}
	return e.reasonCode
}

func deploymentError(code, reason, messageKey string) error {
	return &deploymentControlError{code: code, reasonCode: reason, message: i18n.Text("", messageKey)}
}

func repairRequired(reason string, cause error) error {
	return &deploymentControlError{code: runtimeControlPlaneRepairCode, reasonCode: reason, message: i18n.Text("", "aifar.deploymentControl.repairRequired"), cause: cause}
}

func (s Service) MutateDeployment(ctx context.Context, req DeploymentMutationRequest, log Logger) (store.AIFARDeployment, error) {
	if err := ctx.Err(); err != nil {
		return store.AIFARDeployment{}, err
	}
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return store.AIFARDeployment{}, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	req.ServiceName = cleanAIFARServiceName(req.ServiceName)
	if req.Instance.ID == "" || req.Server.ID == "" || req.Instance.ServerID != req.Server.ID || !aifarServiceSupported(req.ServiceName) || strings.TrimSpace(req.Operation) == "" {
		return store.AIFARDeployment{}, deploymentError(agentRejectedManifestCode, "AIFAR_RUNTIME_MUTATION_INVALID", "aifar.deploymentControl.manifestRejected")
	}

	current, err := loadDeploymentForMutation(control, req.Instance.ID, req.ServiceName)
	if err != nil {
		var missing *deploymentControlError
		if req.Operation != "install-service" || req.ExpectedGeneration != 0 || strings.TrimSpace(req.LockID) == "" || req.InitialDeployment == nil || !errors.As(err, &missing) || missing.ReasonCode() != "AIFAR_RUNTIME_DEPLOYMENT_NOT_FOUND" {
			return store.AIFARDeployment{}, err
		}
		current = *req.InitialDeployment
		current.InstanceID = req.Instance.ID
		current.ServiceName = req.ServiceName
		current.Generation = 0
		current.ObservedGeneration = 0
	}
	if current.Generation != req.ExpectedGeneration {
		return current, deploymentError(deploymentGenerationConflictCode, deploymentGenerationConflictCode, "aifar.deploymentControl.generationConflict")
	}
	nextGeneration := current.Generation + 1
	manifest, err := buildRuntimeManifest(req.Instance, current, nextGeneration)
	if err != nil {
		return current, deploymentError(agentRejectedManifestCode, "AIFAR_RUNTIME_MANIFEST_BUILD_FAILED", "aifar.deploymentControl.manifestRejected")
	}
	if req.Mutate != nil {
		if err := req.Mutate(&manifest); err != nil {
			return current, err
		}
	}
	manifest.Metadata = runtimeagent.DeploymentMetadata{InstanceID: req.Instance.ID, Name: req.ServiceName, Generation: nextGeneration}
	manifest.Spec.ServiceName = req.ServiceName
	manifest.Service.Name = req.ServiceName
	manifest = runtimeagent.NormalizeDeploymentManifest(manifest)
	metadata := metadataFromInstance(req.Instance)
	config := runtimeInstanceConfig(req.Instance, metadata, path.Clean(stringFromMetadata(metadata, "installRoot", "")))
	if err := runtimeagent.ValidateDeploymentManifest(config, manifest); err != nil {
		return current, deploymentError(agentRejectedManifestCode, "AIFAR_RUNTIME_MANIFEST_INVALID", "aifar.deploymentControl.manifestRejected")
	}
	specHash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
	if err != nil {
		return current, repairRequired("AIFAR_RUNTIME_MANIFEST_HASH_FAILED", err)
	}
	specJSON, err := json.Marshal(manifest)
	if err != nil {
		return current, repairRequired("AIFAR_RUNTIME_MANIFEST_SERIALIZE_FAILED", err)
	}
	strategyJSON, err := json.Marshal(manifest.Spec.Strategy)
	if err != nil {
		return current, repairRequired("AIFAR_RUNTIME_STRATEGY_SERIALIZE_FAILED", err)
	}
	pendingConditions, err := deploymentConditionsJSON(false, "PendingAgentAcceptance", nextGeneration)
	if err != nil {
		return current, repairRequired("AIFAR_RUNTIME_CONDITION_SERIALIZE_FAILED", err)
	}

	next := current
	next.DesiredReplicas = manifest.Spec.Replicas
	next.CurrentRevision = manifest.Spec.PodRevision
	next.StrategyJSON = string(strategyJSON)
	next.SpecJSON = string(specJSON)
	next.Status = "pending_acceptance"
	next.ConditionsJSON = pendingConditions
	next.LastTransitionAt = time.Now().UTC()
	var saved store.AIFARDeployment
	if strings.TrimSpace(req.LockID) != "" {
		fenced, ok := s.store.(aifarDeploymentLockFencedStore)
		if !ok {
			return current, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_FENCE_UNAVAILABLE", nil)
		}
		saved, err = fenced.SaveAIFARDeploymentGenerationWithLock(req.LockID, next, req.ExpectedGeneration)
	} else {
		saved, err = control.SaveAIFARDeploymentGeneration(next, req.ExpectedGeneration)
	}
	if err != nil {
		if errors.Is(err, store.ErrAIFAROrchestrationLockOwnership) {
			return current, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
		}
		if errors.Is(err, store.ErrAIFARDeploymentGenerationConflict) || errors.Is(err, store.ErrAIFARDeploymentNotFound) {
			return current, deploymentError(deploymentGenerationConflictCode, deploymentGenerationConflictCode, "aifar.deploymentControl.generationConflict")
		}
		return current, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_WRITE_FAILED", err)
	}
	if log != nil {
		log.Info("AIFAR deployment mutation pending acceptance: service=%s generation=%d operation=%s", req.ServiceName, nextGeneration, safeOperationName(req.Operation))
	}

	acceptance, acceptErr := s.acceptDeployment(ctx, req.Server, manifest, req.TaskID, config.InstallRoot)
	cleanupErr := false
	if acceptErr != nil {
		var typed *deploymentControlError
		if errors.As(acceptErr, &typed) && typed.code == remoteCleanupFailedCode && acceptanceMatches(acceptance, nextGeneration, specHash) {
			cleanupErr = true
		} else if errors.As(acceptErr, &typed) && typed.ambiguous {
			cleanupErr = typed.cleanupErr
			state, readErr := s.readDeploymentStateOnce(ctx, req.Server, req.Instance.ID, req.ServiceName)
			if readErr != nil || !deploymentStateMatches(state, req.Instance.ID, req.ServiceName, nextGeneration, specHash) {
				return saved, repairRequired("AIFAR_RUNTIME_AGENT_READBACK_MISMATCH", readErr)
			}
			acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: state.Generation, SpecHash: state.SpecHash}
			if log != nil {
				log.Info("AIFAR deployment acceptance confirmed by readback: service=%s generation=%d", req.ServiceName, nextGeneration)
			}
		} else {
			return saved, acceptErr
		}
	}
	if !acceptanceMatches(acceptance, nextGeneration, specHash) {
		return saved, repairRequired("AIFAR_RUNTIME_AGENT_ACCEPTANCE_MISMATCH", nil)
	}
	if err := ctx.Err(); err != nil {
		return saved, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
	}
	var accepted store.AIFARDeployment
	if strings.TrimSpace(req.LockID) != "" {
		fenced := s.store.(aifarDeploymentLockFencedStore)
		accepted, err = markDeploymentAcceptedWithLock(fenced, req.LockID, saved, nextGeneration)
	} else {
		accepted, err = markDeploymentAccepted(control, saved, nextGeneration)
	}
	if err != nil {
		if errors.Is(err, store.ErrAIFAROrchestrationLockOwnership) {
			return saved, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
		}
		return saved, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_ACCEPT_FAILED", err)
	}
	if log != nil {
		log.Info("AIFAR deployment accepted: service=%s generation=%d", req.ServiceName, nextGeneration)
	}
	if cleanupErr {
		if log != nil {
			log.Error("%s: %s", remoteCleanupFailedCode, i18n.Text("", "aifar.deploymentControl.cleanupFailed"))
		}
		return accepted, nil
	}
	return accepted, nil
}

func (s Service) AcceptDeployment(ctx context.Context, server store.Server, manifest runtimeagent.DeploymentManifest) (acceptance runtimeagent.DeploymentAcceptance, returnedErr error) {
	return s.acceptDeployment(ctx, server, manifest, "", installRootFromDeployDir(server.DeployDir))
}

func (s Service) acceptDeployment(ctx context.Context, server store.Server, manifest runtimeagent.DeploymentManifest, taskID, installRoot string) (acceptance runtimeagent.DeploymentAcceptance, returnedErr error) {
	manifest = runtimeagent.NormalizeDeploymentManifest(manifest)
	installRoot = path.Clean(strings.TrimSpace(installRoot))
	if !manifestPathsUnderInstallRoot(manifest, installRoot) {
		return acceptance, deploymentError(agentRejectedManifestCode, "AIFAR_RUNTIME_MANIFEST_PATH_INVALID", "aifar.deploymentControl.manifestRejected")
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return acceptance, repairRequired("AIFAR_RUNTIME_MANIFEST_SERIALIZE_FAILED", err)
	}
	local, err := os.CreateTemp("", "aifar-deployment-*.json")
	if err != nil {
		return acceptance, repairRequired("AIFAR_RUNTIME_LOCAL_STAGE_FAILED", err)
	}
	localPath := local.Name()
	defer os.Remove(localPath)
	if err := local.Chmod(0o600); err != nil {
		_ = local.Close()
		return acceptance, repairRequired("AIFAR_RUNTIME_LOCAL_STAGE_FAILED", err)
	}
	if _, err := local.Write(data); err != nil {
		_ = local.Close()
		return acceptance, repairRequired("AIFAR_RUNTIME_LOCAL_STAGE_FAILED", err)
	}
	if err := local.Sync(); err != nil {
		_ = local.Close()
		return acceptance, repairRequired("AIFAR_RUNTIME_LOCAL_STAGE_FAILED", err)
	}
	if err := local.Close(); err != nil {
		return acceptance, repairRequired("AIFAR_RUNTIME_LOCAL_STAGE_FAILED", err)
	}

	mutationDir := path.Join(installRoot, "runtime", "agent", "mutations")
	token, err := randomMutationToken()
	if err != nil {
		return acceptance, repairRequired("AIFAR_RUNTIME_RANDOM_SOURCE_FAILED", err)
	}
	remoteName := strings.Join([]string{safeMutationComponent(manifest.Metadata.InstanceID), safeMutationComponent(manifest.Metadata.Name), safeTaskIDComponent(taskID), token}, "-") + ".json"
	remotePath := path.Join(mutationDir, remoteName)
	if _, err := s.remote.Run(ctx, server, "mkdir -p -- "+installerkit.ShellQuote(mutationDir)+" && chmod 0700 -- "+installerkit.ShellQuote(mutationDir)); err != nil {
		return acceptance, deploymentError(agentUnavailableCode, agentUnavailableCode, "aifar.deploymentControl.agentUnavailable")
	}
	uploaded := false
	if err := s.remote.UploadFile(ctx, server, localPath, remotePath, 0o600); err != nil {
		_ = s.cleanupRemoteManifest(ctx, server, remotePath)
		return acceptance, deploymentError(agentUnavailableCode, "AIFAR_RUNTIME_REMOTE_UPLOAD_FAILED", "aifar.deploymentControl.agentUnavailable")
	}
	uploaded = true
	result, applyErr := s.remote.Run(ctx, server, "aifar-agent apply-deployment --manifest "+installerkit.ShellQuote(remotePath))
	cleanupResultErr := error(nil)
	if uploaded {
		cleanupResultErr = s.cleanupRemoteManifest(ctx, server, remotePath)
	}
	if applyErr != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return acceptance, ctxErr
		}
		if explicit := classifyAgentApplyError(result, applyErr); explicit != nil {
			return acceptance, explicit
		}
		return acceptance, &deploymentControlError{code: agentUnavailableCode, reasonCode: agentUnavailableCode, message: i18n.Text("", "aifar.deploymentControl.agentUnavailable"), ambiguous: true, cleanupErr: cleanupResultErr != nil}
	}
	acceptance, err = decodeDeploymentAcceptance(result.Stdout)
	if err != nil {
		if errors.Is(err, errAmbiguousDeploymentAcceptance) {
			return runtimeagent.DeploymentAcceptance{}, &deploymentControlError{code: agentAcceptanceInvalidCode, reasonCode: agentAcceptanceInvalidCode, message: i18n.Text("", "aifar.deploymentControl.acceptanceInvalid"), ambiguous: true, cleanupErr: cleanupResultErr != nil}
		}
		return runtimeagent.DeploymentAcceptance{}, deploymentError(agentAcceptanceInvalidCode, agentAcceptanceInvalidCode, "aifar.deploymentControl.acceptanceInvalid")
	}
	if cleanupResultErr != nil {
		return acceptance, deploymentError(remoteCleanupFailedCode, remoteCleanupFailedCode, "aifar.deploymentControl.cleanupFailed")
	}
	return acceptance, nil
}

func (s Service) cleanupRemoteManifest(ctx context.Context, server store.Server, remotePath string) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_, err := s.remote.Run(cleanupCtx, server, "rm -f -- "+installerkit.ShellQuote(remotePath))
	return err
}

func loadDeploymentForMutation(control aifarDeploymentControlStore, instanceID, serviceName string) (store.AIFARDeployment, error) {
	deployments, err := control.ListAIFARDeployments(instanceID)
	if err != nil {
		return store.AIFARDeployment{}, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_READ_FAILED", err)
	}
	for _, deployment := range deployments {
		if cleanAIFARServiceName(deployment.ServiceName) == serviceName {
			return deployment, nil
		}
	}
	return store.AIFARDeployment{}, deploymentError(deploymentGenerationConflictCode, "AIFAR_RUNTIME_DEPLOYMENT_NOT_FOUND", "aifar.deploymentControl.generationConflict")
}

func markDeploymentAccepted(control aifarDeploymentControlStore, saved store.AIFARDeployment, generation int64) (store.AIFARDeployment, error) {
	conditions, err := deploymentConditionsJSON(true, "ManifestAccepted", generation)
	if err != nil {
		return saved, err
	}
	return control.AcceptAIFARDeployment(saved.InstanceID, saved.ServiceName, generation, "Accepted", conditions, time.Now().UTC())
}

func markDeploymentAcceptedWithLock(control aifarDeploymentLockFencedStore, lockID string, saved store.AIFARDeployment, generation int64) (store.AIFARDeployment, error) {
	conditions, err := deploymentConditionsJSON(true, "ManifestAccepted", generation)
	if err != nil {
		return saved, err
	}
	return control.AcceptAIFARDeploymentWithLock(lockID, saved, "Accepted", conditions, time.Now().UTC())
}

func deploymentConditionsJSON(accepted bool, reason string, generation int64) (string, error) {
	data, err := json.Marshal([]runtimeagent.DeploymentCondition{{Type: "Accepted", Status: accepted, Reason: reason, Generation: generation, LastTransitionTime: time.Now().UTC()}})
	return string(data), err
}

func acceptanceMatches(value runtimeagent.DeploymentAcceptance, generation int64, hash string) bool {
	return value.Accepted && value.Generation == generation && value.SpecHash == hash && deploymentSpecHashPattern.MatchString(value.SpecHash)
}

func deploymentStateMatches(value runtimeagent.DeploymentState, instanceID, serviceName string, generation int64, hash string) bool {
	return value.InstanceID == instanceID && cleanAIFARServiceName(value.ServiceName) == serviceName && value.Generation == generation && value.SpecHash == hash && deploymentSpecHashPattern.MatchString(value.SpecHash)
}

func (s Service) readDeploymentStateOnce(ctx context.Context, server store.Server, instanceID, serviceName string) (runtimeagent.DeploymentState, error) {
	result, err := s.remote.Run(ctx, server, "aifar-agent get-deployment --instance "+installerkit.ShellQuote(instanceID)+" --service "+installerkit.ShellQuote(serviceName))
	if err != nil {
		return runtimeagent.DeploymentState{}, errors.New("AIFAR deployment readback failed")
	}
	if len(result.Stdout) > 64<<10 {
		return runtimeagent.DeploymentState{}, errors.New("AIFAR deployment readback is invalid")
	}
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	var state runtimeagent.DeploymentState
	if err := decoder.Decode(&state); err != nil {
		return runtimeagent.DeploymentState{}, errors.New("AIFAR deployment readback is invalid")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return runtimeagent.DeploymentState{}, errors.New("AIFAR deployment readback is invalid")
	}
	return state, nil
}

func decodeDeploymentAcceptance(stdout string) (runtimeagent.DeploymentAcceptance, error) {
	if len(stdout) > 64<<10 {
		return runtimeagent.DeploymentAcceptance{}, errAmbiguousDeploymentAcceptance
	}
	decoder := json.NewDecoder(bytes.NewBufferString(stdout))
	decoder.DisallowUnknownFields()
	var acceptance runtimeagent.DeploymentAcceptance
	if err := decoder.Decode(&acceptance); err != nil {
		return acceptance, errAmbiguousDeploymentAcceptance
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return acceptance, errAmbiguousDeploymentAcceptance
	}
	if !acceptance.Accepted || acceptance.Generation <= 0 || !deploymentSpecHashPattern.MatchString(acceptance.SpecHash) {
		return acceptance, errExplicitlyInvalidAcceptance
	}
	return acceptance, nil
}

func classifyAgentApplyError(result installerkit.CommandResult, applyErr error) error {
	text := result.Stdout + "\n" + result.Stderr + "\n" + applyErr.Error()
	switch {
	case strings.Contains(text, "STALE_DEPLOYMENT_GENERATION"):
		return deploymentError(agentStaleGenerationCode, agentStaleGenerationCode, "aifar.deploymentControl.agentStale")
	case strings.Contains(text, "DEPLOYMENT_GENERATION_CONFLICT"):
		return deploymentError(agentGenerationConflictCode, agentGenerationConflictCode, "aifar.deploymentControl.agentConflict")
	case strings.Contains(text, "INVALID_DEPLOYMENT_MANIFEST"), strings.Contains(text, "DEPLOYMENT_IDENTITY_MISMATCH"):
		return deploymentError(agentRejectedManifestCode, agentRejectedManifestCode, "aifar.deploymentControl.manifestRejected")
	default:
		return nil
	}
}

func manifestPathsUnderInstallRoot(manifest runtimeagent.DeploymentManifest, installRoot string) bool {
	if installRoot == "" || installRoot == "/" || !path.IsAbs(installRoot) || path.Clean(installRoot) != installRoot || strings.Contains(installRoot, `\`) || containsDeploymentControl(installRoot) {
		return false
	}
	underRoot := func(value string) bool {
		return value != "" && path.IsAbs(value) && path.Clean(value) == value && !strings.Contains(value, `\`) && !containsDeploymentControl(value) && strings.HasPrefix(value, installRoot+"/")
	}
	for _, envFile := range manifest.Spec.EnvFiles {
		if !underRoot(envFile) {
			return false
		}
	}
	for _, volume := range manifest.Spec.Volumes {
		if !underRoot(volume.Source) {
			return false
		}
	}
	return true
}

func containsDeploymentControl(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) >= 0
}

func randomMutationToken() (string, error) {
	var data [12]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(data[:]), nil
}

func safeMutationComponent(value string) string {
	var out strings.Builder
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			out.WriteRune(r)
		} else {
			out.WriteByte('-')
		}
		if out.Len() >= 40 {
			break
		}
	}
	if out.Len() == 0 {
		return "mutation"
	}
	return out.String()
}

func safeOperationName(value string) string {
	return safeMutationComponent(strings.ToLower(strings.TrimSpace(value)))
}

func safeTaskIDComponent(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 64 {
		return "task"
	}
	for _, r := range value {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_') {
			return "task"
		}
	}
	return value
}
