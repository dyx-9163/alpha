package aifar

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"reflect"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

const (
	runtimeMigrationOperation      = "migrate-runtime-model"
	runtimeMigrationReadMarker     = "AIFAR_RUNTIME_MIGRATION_READ"
	runtimeMigrationMaxLegacyBytes = 4 << 20
)

var requiredRuntimeMigrationAgentFeatures = []string{
	"service-manifest-v1",
	"service-generation-v1",
	"per-service-reconcile",
	"per-service-restart",
	"service-conditions-v1",
	"runtime-instance-snapshot-v1",
	"durable-legacy-archive-v1",
	"verified-bootstrap-stream-v1",
}

type runtimeMigrationStore interface {
	ListAIFARDeployments(instanceID string) ([]store.AIFARDeployment, error)
	CommitAIFARRuntimeMigrationWithLock(store.AIFARRuntimeMigrationCommit) (store.AppInstance, error)
}

type runtimeMigrationAuditStore interface {
	AddAudit(actor, action, target, status, detail string) error
}

type runtimeMigrationInputRunner interface {
	RunWithInput(ctx context.Context, server store.Server, command string, input []byte) (adapter.CommandResult, error)
}

type runtimeMigrationPlan struct {
	instance       store.AppInstance
	metadata       map[string]any
	legacy         runtimeagent.LegacyRuntimeSpec
	manifests      map[string]runtimeagent.DeploymentManifest
	hashes         map[string]string
	deployments    map[string]store.AIFARDeployment
	services       []string
	installRoot    string
	legacySpecPath string
	backupSpecPath string
	legacyJSON     []byte
}

type runtimeMigrationRemoteState struct {
	model      string
	legacyJSON []byte
}

// MigrateRuntimeModel performs the one-way instance-wide transition from the
// legacy runtime-spec writer to Agent-owned per-service manifests. The Agent's
// marker is the point of no return: every failure after it is repaired forward
// from exact generation/spec-hash readback and never by replaying an older
// desired generation.
func (s Service) MigrateRuntimeModel(ctx context.Context, req RuntimeMigrationRequest, log Logger) (returnedErr error) {
	if strings.TrimSpace(req.Instance.ID) == "" || req.Instance.App != AppName || strings.TrimSpace(req.Server.ID) == "" || req.Instance.ServerID != req.Server.ID {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_TARGET_INVALID", nil)
	}
	if strings.TrimSpace(req.TaskID) == "" || strings.TrimSpace(req.Actor) == "" {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_TASK_BOUNDARY_REQUIRED", nil)
	}
	control, ok := s.store.(runtimeMigrationStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_CONTROL_STORE_UNAVAILABLE", nil)
	}
	if _, ok := s.store.(aifarOrchestrationLockStore); !ok {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_EXACT_LOCK_STORE_REQUIRED", nil)
	}
	if _, ok := s.remote.(runtimeMigrationInputRunner); !ok {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_TYPED_BOOTSTRAP_UNAVAILABLE", nil)
	}

	auditStatus := "failed"
	defer func() {
		if returnedErr == nil {
			auditStatus = "success"
		}
		if audits, ok := s.store.(runtimeMigrationAuditStore); ok {
			detail, _ := json.Marshal(map[string]any{
				"taskId": req.TaskID, "reason": strings.TrimSpace(req.Reason),
				"model": orchestrationModelServiceControllerV1,
			})
			_ = audits.AddAudit(req.Actor, "apps.aifar.runtime.migrate", req.Instance.ID, auditStatus, string(detail))
		}
	}()

	instance, lock, err := s.acquireOrchestrationLock(req.Instance.ID, runtimeMigrationOperation, "", req.Actor, req.TaskID)
	if err != nil {
		return err
	}
	if strings.TrimSpace(lock.ID) == "" {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_EXACT_LOCK_REQUIRED", nil)
	}
	defer func() {
		if releaseErr := s.releaseRuntimeMigrationLock(lock); releaseErr != nil {
			if returnedErr != nil {
				returnedErr = repairRequired("AIFAR_RUNTIME_MIGRATION_LOCK_RELEASE_FAILED", errors.Join(returnedErr, releaseErr))
			} else {
				returnedErr = releaseErr
			}
		}
	}()
	if instance.App != AppName || instance.ServerID != req.Server.ID {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_TARGET_INVALID", nil)
	}
	taskCtx, stopHeartbeat := s.startAIFAROrchestrationLockHeartbeat(ctx, lock)
	defer stopHeartbeat()

	inspection, err := s.inspectRuntimeAgent(taskCtx, req.Server)
	if err != nil {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_STATUS_UNAVAILABLE", nil)
	}
	if err := validateRuntimeMigrationAgent(inspection); err != nil {
		return err
	}

	remoteState, err := s.readRuntimeMigrationState(taskCtx, req.Server, instance)
	if err != nil {
		return err
	}
	plan, err := s.buildRuntimeMigrationPlan(instance, remoteState.legacyJSON, control)
	if err != nil {
		return err
	}
	if err := validateRuntimeMigrationModelPair(plan.metadata, remoteState.model); err != nil {
		return err
	}
	if log != nil {
		log.Info("AIFAR runtime migration preflight passed: services=%d", len(plan.services))
	}

	if remoteState.model == "legacy" {
		bootstrapErr := s.bootstrapRuntimeMigrationLegacy(taskCtx, req.Server, plan)
		if taskCtx.Err() != nil {
			return taskCtx.Err()
		}
		// A transport error is ambiguous: the Agent may have durably installed
		// the marker before the SSH channel failed. Exact readback below is the
		// only success proof and is also the idempotent repair path.
		_ = bootstrapErr
	}

	states, err := s.readRuntimeMigrationDeployments(taskCtx, req.Server, plan)
	if err != nil {
		return err
	}
	if err := s.archiveLegacyRuntimeSpec(taskCtx, req.Server, plan); err != nil {
		return err
	}
	if err := s.commitRuntimeMigrationControlPlane(control, lock.ID, plan, states, req); err != nil {
		return err
	}
	if log != nil {
		log.Info("AIFAR runtime migration committed: services=%d model=%s", len(plan.services), orchestrationModelServiceControllerV1)
	}
	return nil
}

func (s Service) releaseRuntimeMigrationLock(lock store.AIFAROrchestrationLock) error {
	if strings.TrimSpace(lock.ID) == "" {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_LOCK_RELEASE_FAILED", nil)
	}
	lockStore, ok := s.store.(aifarOrchestrationLockStore)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_LOCK_RELEASE_FAILED", nil)
	}
	for attempt := 0; attempt < 3; attempt++ {
		released, err := lockStore.ReleaseAIFAROrchestrationLockByID(lock.ID)
		if err == nil && released {
			return nil
		}
	}
	return repairRequired("AIFAR_RUNTIME_MIGRATION_LOCK_RELEASE_FAILED", nil)
}

func validateRuntimeMigrationAgent(inspection runtimeAgentInspection) error {
	if !inspection.Found || inspection.Status != "running" {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_NOT_READY", nil)
	}
	for _, feature := range requiredRuntimeMigrationAgentFeatures {
		if !inspection.Features[feature] {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_FEATURE_MISSING", errors.New("required Agent feature is unavailable"))
		}
	}
	return nil
}

func (s Service) readRuntimeMigrationState(ctx context.Context, server store.Server, instance store.AppInstance) (runtimeMigrationRemoteState, error) {
	metadata := metadataFromInstance(instance)
	installRoot := normalizeInstallRoot(stringFromMetadata(metadata, "installRoot", ""))
	if installRoot == "" || installRoot == "/" || path.Clean(installRoot) != installRoot {
		return runtimeMigrationRemoteState{}, repairRequired("AIFAR_RUNTIME_MIGRATION_INSTALL_ROOT_INVALID", nil)
	}
	legacyPath := stringFromMetadata(metadata, "runtimeSpecPath", runtimeSpecPath(installRoot))
	if legacyPath != runtimeSpecPath(installRoot) {
		return runtimeMigrationRemoteState{}, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_PATH_MISMATCH", nil)
	}
	backupPath := legacyRuntimeSpecBackupPath(installRoot)
	markerPath := path.Join("/var/lib/aifar-agent/instances", instance.ID, "instance.json")
	command := strings.Join([]string{
		"set -eu",
		"echo " + runtimeMigrationReadMarker,
		"legacy=" + installerkit.ShellQuote(legacyPath),
		"backup=" + installerkit.ShellQuote(backupPath),
		"marker=" + installerkit.ShellQuote(markerPath),
		"source=",
		"if [ -f \"$backup\" ] && [ -f \"$legacy\" ]; then cmp -s \"$backup\" \"$legacy\" || { echo 'legacy migration sources differ' >&2; exit 41; }; fi",
		"if [ -f \"$backup\" ]; then source=\"$backup\"; elif [ -f \"$legacy\" ]; then source=\"$legacy\"; else echo 'legacy migration source is unavailable' >&2; exit 42; fi",
		"if [ -f \"$marker\" ]; then echo model=switched; else echo model=legacy; fi",
		"printf 'legacy='",
		"base64 < \"$source\" | tr -d '\\r\\n'",
		"printf '\\n'",
	}, "\n")
	result, err := s.remote.Run(ctx, server, command)
	if err != nil {
		return runtimeMigrationRemoteState{}, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_READ_FAILED", nil)
	}
	if len(result.Stdout) > base64.StdEncoding.EncodedLen(runtimeMigrationMaxLegacyBytes)+4096 {
		return runtimeMigrationRemoteState{}, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
	}
	var state runtimeMigrationRemoteState
	seen := map[string]bool{}
	for _, line := range strings.Split(result.Stdout, "\n") {
		key, value, found := strings.Cut(strings.TrimSpace(line), "=")
		if !found || (key != "model" && key != "legacy") {
			continue
		}
		if seen[key] {
			return state, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
		}
		seen[key] = true
		switch key {
		case "model":
			state.model = strings.TrimSpace(value)
		case "legacy":
			decoded, decodeErr := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
			if decodeErr != nil || len(decoded) == 0 || len(decoded) > runtimeMigrationMaxLegacyBytes {
				return state, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
			}
			state.legacyJSON = decoded
		}
	}
	if (state.model != "legacy" && state.model != "switched") || len(state.legacyJSON) == 0 {
		return state, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
	}
	return state, nil
}

func (s Service) buildRuntimeMigrationPlan(instance store.AppInstance, legacyJSON []byte, control runtimeMigrationStore) (runtimeMigrationPlan, error) {
	plan := runtimeMigrationPlan{instance: instance, metadata: metadataFromInstance(instance), legacyJSON: append([]byte(nil), legacyJSON...)}
	if err := decodeRuntimeMigrationLegacySpec(legacyJSON, &plan.legacy); err != nil {
		return plan, err
	}
	plan.legacy = runtimeagent.NormalizeSpec(plan.legacy)
	plan.installRoot = normalizeInstallRoot(stringFromMetadata(plan.metadata, "installRoot", ""))
	plan.legacySpecPath = runtimeSpecPath(plan.installRoot)
	plan.backupSpecPath = legacyRuntimeSpecBackupPath(plan.installRoot)
	if plan.legacy.InstanceID != instance.ID || plan.legacy.InstallRoot != plan.installRoot || plan.legacy.Network != stringFromMetadata(plan.metadata, "ingressNetwork", stringFromMetadata(plan.metadata, "networkName", defaultNetworkName)) {
		return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_INSTANCE_DIVERGED", nil)
	}
	if stringFromMetadata(plan.metadata, "runtimeDir", "") != path.Join(plan.installRoot, "runtime") || stringFromMetadata(plan.metadata, "envDir", "") != path.Join(plan.installRoot, "runtime", releaseEnvDirName) {
		return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_ENV_PATH_DIVERGED", nil)
	}

	services, err := exactMigrationMetadataServices(plan.metadata)
	if err != nil {
		return plan, err
	}
	plan.services = services
	if !migrationDesiredReplicaKeysExact(plan.metadata, plan.services) {
		return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_REPLICAS_DIVERGED", nil)
	}
	legacyDeployments := make(map[string]runtimeagent.DeploymentSpec, len(plan.legacy.Deployments))
	for _, deployment := range plan.legacy.Deployments {
		name := cleanAIFARServiceName(deployment.ServiceName)
		if name == "" || legacyDeployments[name].ServiceName != "" {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_SERVICE_SET_DIVERGED", nil)
		}
		legacyDeployments[name] = deployment
	}
	legacyServices := make(map[string]runtimeagent.ServiceSpec, len(plan.legacy.Services))
	for _, service := range plan.legacy.Services {
		name := cleanAIFARServiceName(service.Name)
		if name != "" {
			legacyServices[name] = service
		}
	}
	if !sameStringSet(plan.services, sortedMapKeys(legacyDeployments)) {
		return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_SERVICE_SET_DIVERGED", nil)
	}

	stored, err := control.ListAIFARDeployments(instance.ID)
	if err != nil {
		return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_CONTROL_READ_FAILED", nil)
	}
	plan.deployments = make(map[string]store.AIFARDeployment, len(stored))
	for _, deployment := range stored {
		name := cleanAIFARServiceName(deployment.ServiceName)
		if name == "" || plan.deployments[name].ServiceName != "" {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_SERVICE_SET_DIVERGED", nil)
		}
		if deployment.Generation > 1 || deployment.ObservedGeneration > 1 {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY", nil)
		}
		plan.deployments[name] = deployment
	}
	if !sameStringSet(plan.services, sortedMapKeys(plan.deployments)) {
		return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_SERVICE_SET_DIVERGED", nil)
	}

	plan.manifests = make(map[string]runtimeagent.DeploymentManifest, len(plan.services))
	plan.hashes = make(map[string]string, len(plan.services))
	config := runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion, InstanceID: instance.ID,
		InstallRoot: plan.legacy.InstallRoot, Network: plan.legacy.Network, Ingress: plan.legacy.Ingress,
	})
	definitions := serviceDefinitionsFromMetadata(plan.metadata)
	for _, serviceName := range plan.services {
		legacyDeployment := legacyDeployments[serviceName]
		legacyService, found := legacyServices[serviceName]
		if !found {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_SERVICE_SET_DIVERGED", nil)
		}
		storedDeployment := plan.deployments[serviceName]
		definition, found := catalogDefinition(definitions, serviceName)
		if !found {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
		}
		desired, found := exactMigrationDesiredReplica(plan.metadata, serviceName)
		if !found || desired != legacyDeployment.Replicas || storedDeployment.DesiredReplicas != desired {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_REPLICAS_DIVERGED", nil)
		}
		revision := currentRevisionForService(plan.metadata, serviceName)
		if revision == "" || legacyDeployment.PodRevision != revision || storedDeployment.CurrentRevision != revision {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_REVISION_DIVERGED", nil)
		}
		if legacyDeployment.Image != "aifar-"+serviceName+":"+revision {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_IMAGE_DIVERGED", nil)
		}
		port := definition.Port
		if definition.Role == "gateway" {
			port = intFromMetadata(plan.metadata, "gatewayPort", port)
		} else if definition.Role == "web" {
			port = intFromMetadata(plan.metadata, "webPort", port)
		}
		if len(legacyDeployment.Ports) != 1 || legacyDeployment.Ports[0].ContainerPort != port || legacyService.ListenPort != port || legacyService.TargetPort != port {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_PORT_DIVERGED", nil)
		}
		expectedDefaults := runtimeManifestDefaults(instance.ID, plan.installRoot, definition, storedDeployment, 1, plan.metadata)
		if !sameOrderedStrings(legacyDeployment.EnvFiles, expectedDefaults.Spec.EnvFiles) {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_ENV_PATH_DIVERGED", nil)
		}
		manifest := runtimeagent.NormalizeDeploymentManifest(runtimeagent.DeploymentManifest{
			APIVersion: runtimeagent.ManifestAPIVersion, Kind: runtimeagent.DeploymentManifestKind,
			Metadata: runtimeagent.DeploymentMetadata{InstanceID: instance.ID, Name: serviceName, Generation: 1},
			Spec:     legacyDeployment, Service: legacyService,
		})
		if err := runtimeagent.ValidateDeploymentManifest(config, manifest); err != nil {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
		}
		hash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
		if err != nil {
			return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_HASH_FAILED", nil)
		}
		if strings.TrimSpace(storedDeployment.SpecJSON) != "" {
			storedHash, hashErr := migrationStoredManifestHash(storedDeployment.SpecJSON)
			if hashErr != nil || storedHash != hash {
				return plan, repairRequired("AIFAR_RUNTIME_MIGRATION_CONTROL_SPEC_DIVERGED", nil)
			}
		}
		plan.manifests[serviceName] = manifest
		plan.hashes[serviceName] = hash
	}
	return plan, nil
}

func (s Service) bootstrapRuntimeMigrationLegacy(ctx context.Context, server store.Server, plan runtimeMigrationPlan) error {
	runner, ok := s.remote.(runtimeMigrationInputRunner)
	if !ok {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_TYPED_BOOTSTRAP_UNAVAILABLE", nil)
	}
	digest := sha256.Sum256(plan.legacyJSON)
	command := "aifar-agent bootstrap-runtime-stdin --instance " + installerkit.ShellQuote(plan.instance.ID) + " --sha256 " + installerkit.ShellQuote(hex.EncodeToString(digest[:]))
	_, err := runner.RunWithInput(ctx, server, command, plan.legacyJSON)
	return err
}

func decodeRuntimeMigrationLegacySpec(data []byte, target *runtimeagent.LegacyRuntimeSpec) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_LEGACY_SPEC_INVALID", nil)
	}
	return nil
}

func migrationStoredManifestHash(raw string) (string, error) {
	var manifest runtimeagent.DeploymentManifest
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return "", err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return "", errors.New("stored manifest has trailing data")
	}
	return runtimeagent.DeploymentManifestSpecHash(runtimeagent.NormalizeDeploymentManifest(manifest))
}

func validateRuntimeMigrationModelPair(metadata map[string]any, agentModel string) error {
	model := stringFromMetadata(metadata, "orchestrationModel", "")
	if model != orchestrationModelK8sLikeV1 && model != orchestrationModelServiceControllerV1 {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_MODEL_UNSUPPORTED", nil)
	}
	if model == orchestrationModelServiceControllerV1 && agentModel != "switched" {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY", nil)
	}
	return nil
}

func (s Service) readRuntimeMigrationDeployments(ctx context.Context, server store.Server, plan runtimeMigrationPlan) (map[string]runtimeagent.DeploymentState, error) {
	result, err := s.remote.Run(ctx, server, "aifar-agent get-instance-snapshot --instance "+installerkit.ShellQuote(plan.instance.ID))
	if err != nil || len(result.Stdout) == 0 || len(result.Stdout) > runtimeMigrationMaxLegacyBytes {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_READBACK_FAILED", nil)
	}
	var snapshot runtimeagent.RuntimeInstanceSnapshot
	decoder := json.NewDecoder(strings.NewReader(result.Stdout))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&snapshot); err != nil {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_READBACK_FAILED", nil)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_READBACK_FAILED", nil)
	}
	expectedInstance := runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion, InstanceID: plan.instance.ID,
		InstallRoot: plan.installRoot, Network: plan.legacy.Network, Ingress: plan.legacy.Ingress,
	})
	if !reflect.DeepEqual(runtimeagent.NormalizeInstanceConfig(snapshot.Instance), expectedInstance) {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_INSTANCE_DIVERGED", nil)
	}
	states := make(map[string]runtimeagent.DeploymentState, len(snapshot.Deployments))
	seenServices := make(map[string]bool, len(snapshot.Deployments))
	for _, deployment := range snapshot.Deployments {
		serviceName := cleanAIFARServiceName(deployment.ServiceName)
		if deployment.ManifestGeneration > 1 || deployment.StateGeneration > 1 || deployment.ObservedGeneration > 1 {
			return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY", nil)
		}
		if serviceName == "" || serviceName != deployment.ServiceName || seenServices[serviceName] {
			return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_READBACK_DIVERGED", nil)
		}
		seenServices[serviceName] = true
		states[serviceName] = runtimeagent.DeploymentState{
			InstanceID: plan.instance.ID, ServiceName: serviceName,
			Generation: deployment.StateGeneration, ObservedGeneration: deployment.ObservedGeneration,
			SpecHash: deployment.StateSpecHash, DesiredReplicas: deployment.DesiredReplicas,
		}
	}
	if !sameStringSet(plan.services, sortedMapKeys(states)) {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_SERVICE_SET_DIVERGED", nil)
	}
	for _, deployment := range snapshot.Deployments {
		serviceName := deployment.ServiceName
		state := states[serviceName]
		if deployment.ManifestGeneration != 1 || deployment.ManifestSpecHash != plan.hashes[serviceName] ||
			!deploymentStateMatches(state, plan.instance.ID, serviceName, 1, plan.hashes[serviceName]) ||
			state.DesiredReplicas != plan.manifests[serviceName].Spec.Replicas {
			return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_AGENT_READBACK_DIVERGED", nil)
		}
	}
	return states, nil
}

func (s Service) archiveLegacyRuntimeSpec(ctx context.Context, server store.Server, plan runtimeMigrationPlan) error {
	digest := sha256.Sum256(plan.legacyJSON)
	command := "aifar-agent archive-legacy-runtime --instance " + installerkit.ShellQuote(plan.instance.ID) + " --sha256 " + installerkit.ShellQuote(hex.EncodeToString(digest[:]))
	if _, err := s.remote.Run(ctx, server, command); err != nil {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_ARCHIVE_FAILED", nil)
	}
	return nil
}

func (s Service) commitRuntimeMigrationControlPlane(control runtimeMigrationStore, lockID string, plan runtimeMigrationPlan, states map[string]runtimeagent.DeploymentState, req RuntimeMigrationRequest) error {
	now := time.Now().UTC()
	conditions, err := deploymentConditionsJSON(true, "MigrationManifestAccepted", 1)
	if err != nil {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_CONTROL_COMMIT_FAILED", nil)
	}
	items := make([]store.AIFARRuntimeMigrationDeploymentCommit, 0, len(plan.services))
	for _, serviceName := range plan.services {
		current := plan.deployments[serviceName]
		if current.Generation > 1 || current.ObservedGeneration > 1 || states[serviceName].Generation != 1 {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY", nil)
		}
		specJSON, err := json.Marshal(plan.manifests[serviceName])
		if err != nil {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_CONTROL_COMMIT_FAILED", nil)
		}
		metadataJSON, _ := json.Marshal(map[string]any{
			"model": orchestrationModelServiceControllerV1, "specHash": plan.hashes[serviceName],
		})
		current.DesiredReplicas = plan.manifests[serviceName].Spec.Replicas
		current.CurrentRevision = plan.manifests[serviceName].Spec.PodRevision
		current.SpecJSON = string(specJSON)
		current.Generation = 1
		if current.ObservedGeneration == 0 {
			current.Status = "Accepted"
			current.MetadataJSON = string(metadataJSON)
			current.ConditionsJSON = conditions
			current.LastTransitionAt = now
		}
		items = append(items, store.AIFARRuntimeMigrationDeploymentCommit{Expected: plan.deployments[serviceName], Next: current})
	}
	for attempt := 0; attempt < appInstanceMetadataCASAttempts; attempt++ {
		current, err := s.store.GetAppInstance(plan.instance.ID)
		if err != nil {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_COMMIT_FAILED", nil)
		}
		metadata := metadataFromInstance(current)
		if err := validateRuntimeMigrationMetadata(current, metadata, plan); err != nil {
			return err
		}
		metadata["orchestrationModel"] = orchestrationModelServiceControllerV1
		metadata["legacyRuntimeSpecBackupPath"] = plan.backupSpecPath
		metadata["legacyRuntimeSpecReadOnly"] = true
		metadata["runtimeMigration"] = map[string]any{
			"model":       orchestrationModelServiceControllerV1,
			"taskId":      strings.TrimSpace(req.TaskID),
			"migratedAt":  now.Format(time.RFC3339),
			"generations": migrationGenerations(states),
			"specHashes":  copyStringMap(plan.hashes),
		}
		if agent, ok := metadata["agent"].(map[string]any); ok {
			agent["orchestrationModel"] = orchestrationModelServiceControllerV1
			agent["legacySpecBackup"] = plan.backupSpecPath
			delete(agent, "specPath")
			metadata["agent"] = agent
		}
		nextMetadata, err := json.Marshal(metadata)
		if err != nil {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_COMMIT_FAILED", nil)
		}
		_, err = control.CommitAIFARRuntimeMigrationWithLock(store.AIFARRuntimeMigrationCommit{
			LockID: lockID, InstanceID: plan.instance.ID, ExpectedInstanceUpdatedAt: current.UpdatedAt,
			NextMetadata: string(nextMetadata), Deployments: items,
		})
		if err == nil {
			return nil
		}
		if errors.Is(err, store.ErrAppInstanceConflict) {
			continue
		}
		if errors.Is(err, store.ErrAIFAROrchestrationLockOwnership) {
			return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
		}
		if errors.Is(err, store.ErrAIFARDeploymentGenerationConflict) {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_FORWARD_ONLY", err)
		}
		return repairRequired("AIFAR_RUNTIME_MIGRATION_CONTROL_COMMIT_FAILED", nil)
	}
	return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_COMMIT_FAILED", store.ErrAppInstanceConflict)
}

func validateRuntimeMigrationMetadata(current store.AppInstance, metadata map[string]any, plan runtimeMigrationPlan) error {
	if current.ID != plan.instance.ID || current.App != AppName || current.ServerID != plan.instance.ServerID {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	if err := validateRuntimeMigrationModelPair(metadata, "switched"); err != nil {
		return err
	}
	if stringFromMetadata(metadata, "installRoot", "") != plan.installRoot ||
		stringFromMetadata(metadata, "runtimeSpecPath", "") != plan.legacySpecPath ||
		stringFromMetadata(metadata, "runtimeDir", "") != path.Join(plan.installRoot, "runtime") ||
		stringFromMetadata(metadata, "envDir", "") != path.Join(plan.installRoot, "runtime", releaseEnvDirName) ||
		stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName)) != plan.legacy.Network ||
		intFromMetadata(metadata, "gatewayPort", defaultGatewayPort) != plan.legacy.Ingress.GatewayPort ||
		intFromMetadata(metadata, "webPort", defaultWebPort) != plan.legacy.Ingress.WebPort ||
		!reflect.DeepEqual(serviceDefinitionsFromMetadata(metadata), serviceDefinitionsFromMetadata(plan.metadata)) {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	expectedInstance := runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{
		APIVersion: runtimeagent.ManifestAPIVersion, InstanceID: current.ID,
		InstallRoot: plan.installRoot, Network: plan.legacy.Network, Ingress: plan.legacy.Ingress,
	})
	if !reflect.DeepEqual(runtimeInstanceConfig(current, metadata, plan.installRoot), expectedInstance) {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	services, err := exactMigrationMetadataServices(metadata)
	if err != nil || !sameStringSet(services, plan.services) || !migrationDesiredReplicaKeysExact(metadata, plan.services) {
		return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	for _, serviceName := range plan.services {
		desired, ok := exactMigrationDesiredReplica(metadata, serviceName)
		if !ok || desired != plan.manifests[serviceName].Spec.Replicas || currentRevisionForService(metadata, serviceName) != plan.manifests[serviceName].Spec.PodRevision {
			return repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
		}
	}
	return nil
}

func exactMigrationMetadataServices(metadata map[string]any) ([]string, error) {
	raw, ok := metadata["services"]
	if !ok {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	var values []string
	switch items := raw.(type) {
	case []string:
		values = append(values, items...)
	case []any:
		for _, item := range items {
			text, ok := item.(string)
			if !ok {
				return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
			}
			values = append(values, text)
		}
	default:
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	seen := map[string]bool{}
	services := make([]string, 0, len(values))
	for _, value := range values {
		name := cleanAIFARServiceName(value)
		if name == "" || seen[name] {
			return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
		}
		seen[name] = true
		services = append(services, name)
	}
	if len(services) == 0 {
		return nil, repairRequired("AIFAR_RUNTIME_MIGRATION_METADATA_DIVERGED", nil)
	}
	sort.Strings(services)
	return services, nil
}

func exactMigrationDesiredReplica(metadata map[string]any, serviceName string) (int, bool) {
	raw, ok := metadata["desiredReplicas"]
	if !ok {
		return 0, false
	}
	switch values := raw.(type) {
	case map[string]int:
		value, found := values[serviceName]
		return value, found && value >= 0
	case map[string]any:
		value, found := values[serviceName]
		if !found {
			return 0, false
		}
		n := intFromAny(value, -1)
		return n, n >= 0
	default:
		return 0, false
	}
}

func migrationDesiredReplicaKeysExact(metadata map[string]any, services []string) bool {
	wanted := make(map[string]bool, len(services))
	for _, serviceName := range services {
		wanted[serviceName] = true
	}
	seen := map[string]bool{}
	switch values := metadata["desiredReplicas"].(type) {
	case map[string]int:
		for serviceName, value := range values {
			if !wanted[serviceName] || value < 0 {
				return false
			}
			seen[serviceName] = true
		}
	case map[string]any:
		for serviceName, value := range values {
			if !wanted[serviceName] || intFromAny(value, -1) < 0 {
				return false
			}
			seen[serviceName] = true
		}
	default:
		return false
	}
	return len(seen) == len(wanted)
}

func sortedMapKeys[T any](values map[string]T) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sameStringSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aa := append([]string(nil), a...)
	bb := append([]string(nil), b...)
	sort.Strings(aa)
	sort.Strings(bb)
	for idx := range aa {
		if aa[idx] != bb[idx] {
			return false
		}
	}
	return true
}

func sameOrderedStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for idx := range a {
		if path.Clean(a[idx]) != path.Clean(b[idx]) || a[idx] != b[idx] {
			return false
		}
	}
	return true
}

func migrationGenerations(states map[string]runtimeagent.DeploymentState) map[string]int64 {
	out := make(map[string]int64, len(states))
	for serviceName, state := range states {
		out[serviceName] = state.Generation
	}
	return out
}

func copyStringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}
