package aifar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/runtimeagent"
	"aifar-deployment/backend/internal/store"
)

type serviceInstallScriptData struct {
	InstallRoot         string
	ServiceOrder        string
	NewServices         string
	ServiceApplications string
	ServicePorts        string
	ServiceKinds        string
	ServiceHealthPaths  string
	ServiceAffinities   string
	GatewayService      string
	WebService          string
	Version             string
	ReleaseID           string
	CreatedAt           string
	ConfigHash          string
	IngressNetwork      string
}

func (s Service) InstallServices(ctx context.Context, req InstallServicesRequest, log Logger, targetLog targetLogger) error {
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(
		logForServer,
		recorder,
		target,
		serviceInstallSteps(),
		"AIFAR service install step %d/%d started: %s",
		"AIFAR service install step %d/%d completed: %s",
		"AIFAR service install step %d/%d failed: %s: %v",
	)

	var current store.AppInstance
	var lock store.AIFAROrchestrationLock
	var metadata map[string]any
	var requested []string
	var missing []string
	var prepareServices []string
	var allServices []string
	var installRoot string
	var version string
	var releaseTime time.Time
	var releaseID string
	var configHash string
	var ingressNetwork string
	var gatewayPort int
	var webPort int
	var script string
	var serviceDefinitions []serviceDefinition
	var acceptedRevisions map[string]string
	var acceptedDeployments []store.AIFARDeployment
	var moduleArchiveLocal string
	var moduleArchiveRemote string
	var workDir string

	if err := step(1, func() error {
		var err error
		current, lock, err = s.acquireOrchestrationLock(req.Instance.ID, "install-services", "", req.Actor, fallbackTaskID(req.TaskID, log))
		if err != nil {
			return err
		}
		metadata = metadataFromInstance(current)
		serviceDefinitions = serviceDefinitionsFromMetadata(metadata)
		version = stringFromMetadata(metadata, "releaseVersion", current.Version)
		if strings.TrimSpace(version) == "" {
			version = appBundleVersion
		}
		if lister, ok := s.store.(resourceLister); ok {
			resources, err := lister.ListResources()
			if err != nil {
				return err
			}
			bundle, err := SelectBundle(resources, version)
			if err != nil {
				return err
			}
			serviceDefinitions, err = discoverBundleServices(bundle)
			if err != nil {
				return err
			}
		}
		requested, err = normalizeRequestedAIFARServices(req.Services, serviceDefinitions)
		if err != nil {
			return err
		}
		if err := ensureK8sLikeMetadata(metadata, UpdateCopy{LegacyUpdateUnsupported: "legacy AIFAR orchestration model %s does not support service installation; reinstall with k8s-like orchestration first"}); err != nil {
			return err
		}
		installed := servicesFromMetadata(metadata)
		missing = missingServices(requested, installed)
		if len(missing) == 0 {
			return errors.New("selected AIFAR services are already installed")
		}
		allServices = mergeServices(installed, missing, serviceDefinitions)
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		if strings.TrimSpace(installRoot) == "" {
			return errors.New("AIFAR install root is missing")
		}
		releaseTime = time.Now().UTC()
		releaseID = newReleaseID("services-"+strings.Join(missing, "-"), releaseTime)
		configHash = serviceInstallConfigHash(stringFromMetadata(metadata, "configHash", ""), missing)
		attempt, prepare, err := s.planServiceInstallAttempt(current.ID, missing, serviceInstallAttempt{
			Revision: releaseID, Version: version, ConfigHash: configHash, CreatedAt: releaseTime,
		})
		if err != nil {
			return err
		}
		releaseID = attempt.Revision
		version = attempt.Version
		configHash = attempt.ConfigHash
		releaseTime = attempt.CreatedAt
		prepareServices = prepare
		ingressNetwork = stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName))
		gatewayPort = intFromMetadata(metadata, "gatewayPort", defaultGatewayPort)
		webPort = intFromMetadata(metadata, "webPort", defaultWebPort)
		return nil
	}); err != nil {
		if lock.InstanceID != "" {
			s.releaseOrchestrationLock(lock)
		}
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	defer s.releaseOrchestrationLock(lock)
	lockedCtx, stopHeartbeat := s.startAIFAROrchestrationLockHeartbeat(ctx, lock)
	defer stopHeartbeat()

	if err := step(2, func() error {
		var err error
		script, err = renderServiceInstallScript(serviceInstallScriptData{
			InstallRoot:         installRoot,
			ServiceOrder:        strings.Join(allServices, " "),
			NewServices:         strings.Join(prepareServices, " "),
			ServiceApplications: serviceCatalogPairs(serviceDefinitions, allServices, func(definition serviceDefinition) string { return definition.ApplicationName }),
			ServicePorts:        serviceCatalogPairs(serviceDefinitions, allServices, func(definition serviceDefinition) string { return fmt.Sprint(definition.Port) }),
			ServiceKinds:        serviceCatalogPairs(serviceDefinitions, allServices, func(definition serviceDefinition) string { return definition.Kind }),
			ServiceHealthPaths:  serviceCatalogPairs(serviceDefinitions, allServices, func(definition serviceDefinition) string { return definition.HealthPath }),
			ServiceAffinities:   serviceCatalogPairs(serviceDefinitions, allServices, func(definition serviceDefinition) string { return definition.AffinityPolicy }),
			GatewayService:      serviceNameForRole(serviceDefinitions, "gateway"),
			WebService:          serviceNameForRole(serviceDefinitions, "web"),
			Version:             version,
			ReleaseID:           releaseID,
			CreatedAt:           releaseTime.Format(time.RFC3339),
			ConfigHash:          configHash,
			IngressNetwork:      ingressNetwork,
		})
		if err != nil {
			return err
		}
		if lister, ok := s.store.(resourceLister); ok {
			resources, err := lister.ListResources()
			if err != nil {
				return err
			}
			bundle, err := SelectBundle(resources, version)
			if err != nil {
				return err
			}
			if len(prepareServices) == 0 {
				return nil
			}
			moduleArchiveLocal, err = CreateServiceModuleArchive(bundle, prepareServices)
			if err != nil {
				return err
			}
			workDir = installerkit.WorkDir(installerkit.RemoteDeployDir(req.Server.DeployDir), "aifar-services", version, releaseTime)
			moduleArchiveRemote = workDir + "/" + filepath.Base(moduleArchiveLocal)
		}
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}
	if moduleArchiveLocal != "" {
		defer os.Remove(moduleArchiveLocal)
	}

	if err := step(3, func() error {
		logForServer.Info("installing AIFAR services: %s", strings.Join(missing, ", "))
		if moduleArchiveLocal != "" {
			if _, err := installerkit.Run(lockedCtx, s.remote, req.Server, "mkdir -p "+installerkit.ShellQuote(workDir)+" "+installerkit.ShellQuote(installRoot+"/runtime/services"), logForServer, "prepare AIFAR service module upload failed"); err != nil {
				return err
			}
			if err := uploadkit.Upload(lockedCtx, s.remote, req.Server, uploadkit.File{LocalPath: moduleArchiveLocal, RemotePath: moduleArchiveRemote, LogMessage: "uploading AIFAR service modules", FailureMessage: "upload AIFAR service modules failed"}, logForServer); err != nil {
				return err
			}
			if _, err := installerkit.Run(lockedCtx, s.remote, req.Server, "tar -xzf "+installerkit.ShellQuote(moduleArchiveRemote)+" -C "+installerkit.ShellQuote(installRoot+"/runtime/services"), logForServer, "extract AIFAR service modules failed"); err != nil {
				return err
			}
		}
		if len(prepareServices) > 0 {
			if _, runErr := installerkit.Run(lockedCtx, s.remote, req.Server, "sh -s <<'AIFAR_SERVICE_INSTALL'\n"+script+"\nAIFAR_SERVICE_INSTALL", logForServer, "AIFAR service installation failed"); runErr != nil {
				return runErr
			}
		}
		var acceptErr error
		acceptedRevisions, acceptedDeployments, acceptErr = s.acceptInstalledServiceManifests(lockedCtx, req, current, metadata, allServices, missing, serviceDefinitions, releaseID, version, configHash, releaseTime, lock, logForServer)
		return acceptErr
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(4, func() error {
		fenced, ok := s.store.(aifarServiceInstallFencedStore)
		if !ok {
			return repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_FENCE_UNAVAILABLE", nil)
		}
		for attempt := 0; attempt < appInstanceMetadataCASAttempts; attempt++ {
			if err := s.ensureAIFAROrchestrationLockOwnership(lockedCtx, lock); err != nil {
				return err
			}
			fresh, err := s.store.GetAppInstance(current.ID)
			if err != nil {
				return err
			}
			nextMetadata := metadataFromInstance(fresh)
			nextMetadata["releaseId"] = releaseID
			nextMetadata["currentRevision"] = releaseID
			nextMetadata["releaseVersion"] = version
			nextMetadata["releaseCreatedAt"] = releaseTime.Format(time.RFC3339)
			nextMetadata["configHash"] = configHash
			nextMetadata["serviceCatalog"] = serviceCatalogMetadataForInstall(serviceDefinitions, gatewayPort, webPort)
			nextMetadata = serviceInstallOrchestrationMetadata(nextMetadata, installRoot, ingressNetwork, gatewayPort, webPort, missing, acceptedRevisions)
			nextMetadata["lastServiceInstall"] = map[string]any{"services": missing, "releaseId": releaseID, "installedAt": releaseTime.Format(time.RFC3339), "reason": strings.TrimSpace(req.Reason)}
			delete(nextMetadata, "orchestrationLock")
			next := fresh
			next.Version = version
			next.Metadata = mustJSONMetadata(nextMetadata)
			manifest, _ := json.Marshal(serviceInstallReleaseManifest(version, releaseID, releaseTime, configHash, strings.TrimSpace(req.Reason), missing, nextMetadata))
			_, err = fenced.CommitAIFARServiceInstallWithLock(store.AIFARServiceInstallCommit{
				LockID: lock.ID, ExpectedDeployments: acceptedDeployments, NextInstance: next, ExpectedInstanceUpdatedAt: fresh.UpdatedAt,
				Release: store.AppRelease{InstanceID: current.ID, App: AppName, Version: version, ReleaseID: releaseID, ServerID: target, Status: "success", ManifestJSON: string(manifest), ConfigHash: configHash, CreatedAt: releaseTime, ActivatedAt: releaseTime},
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
			return err
		}
		return repairRequired("AIFAR_SERVICE_INSTALL_METADATA_CONFLICT", store.ErrAppInstanceConflict)
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	logForServer.Info("AIFAR services installed: %s", strings.Join(missing, ", "))
	finishTarget(recorder, target, "success", "")
	return nil
}

type serviceInstallAttempt struct {
	Revision   string    `json:"revision"`
	Version    string    `json:"version"`
	ConfigHash string    `json:"configHash"`
	CreatedAt  time.Time `json:"createdAt"`
}

func (s Service) planServiceInstallAttempt(instanceID string, services []string, proposed serviceInstallAttempt) (serviceInstallAttempt, []string, error) {
	control, ok := s.store.(aifarDeploymentControlStore)
	if !ok {
		return proposed, nil, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_UNAVAILABLE", nil)
	}
	deployments, err := control.ListAIFARDeployments(instanceID)
	if err != nil {
		return proposed, nil, err
	}
	byService := make(map[string]store.AIFARDeployment, len(deployments))
	for _, deployment := range deployments {
		byService[cleanAIFARServiceName(deployment.ServiceName)] = deployment
	}
	prepare := make([]string, 0, len(services))
	var persisted *serviceInstallAttempt
	for _, serviceName := range services {
		deployment, exists := byService[serviceName]
		if !exists {
			prepare = append(prepare, serviceName)
			continue
		}
		accepted := deploymentHasAcceptedDesired(deployment)
		pending := deployment.Generation > 0 && deployment.Status == "pending_acceptance" && deployment.DesiredReplicas == 1 && strings.TrimSpace(deployment.SpecJSON) != ""
		prepared := deployment.Generation == 0 && deployment.Status == "install_prepared"
		if !accepted && !pending && !prepared {
			return proposed, nil, repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_STATE_INVALID", nil)
		}
		attempt, err := decodeServiceInstallAttempt(deployment.MetadataJSON)
		if err != nil || attempt.Revision != deployment.CurrentRevision {
			return proposed, nil, repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_IDENTITY_INVALID", err)
		}
		if persisted == nil {
			copyAttempt := attempt
			persisted = &copyAttempt
			continue
		}
		if persisted.Revision != attempt.Revision || persisted.Version != attempt.Version || persisted.ConfigHash != attempt.ConfigHash || !persisted.CreatedAt.Equal(attempt.CreatedAt) {
			return proposed, nil, repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_IDENTITY_CONFLICT", nil)
		}
	}
	if persisted == nil {
		return proposed, prepare, nil
	}
	if len(prepare) > 0 {
		return proposed, nil, repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_SET_CHANGED", nil)
	}
	return *persisted, nil, nil
}

func deploymentHasAcceptedDesired(deployment store.AIFARDeployment) bool {
	if deployment.Generation <= 0 || deployment.DesiredReplicas != 1 || strings.TrimSpace(deployment.SpecJSON) == "" {
		return false
	}
	if deployment.Status == "Accepted" {
		return true
	}
	if deployment.ObservedGeneration < deployment.Generation {
		return false
	}
	switch deployment.Status {
	case "Progressing", "Available", "Degraded", "Offline":
		return true
	default:
		return false
	}
}

func encodeServiceInstallAttempt(attempt serviceInstallAttempt) (string, error) {
	data, err := json.Marshal(attempt)
	return string(data), err
}

func decodeServiceInstallAttempt(data string) (serviceInstallAttempt, error) {
	var attempt serviceInstallAttempt
	decoder := json.NewDecoder(strings.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&attempt); err != nil {
		return attempt, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return attempt, errors.New("AIFAR service install attempt metadata is invalid")
	}
	if strings.TrimSpace(attempt.Revision) == "" || strings.TrimSpace(attempt.Version) == "" || !deploymentSpecHashPattern.MatchString(attempt.ConfigHash) || attempt.CreatedAt.IsZero() {
		return attempt, errors.New("AIFAR service install attempt identity is incomplete")
	}
	return attempt, nil
}

func (s Service) acceptInstalledServiceManifests(
	ctx context.Context,
	req InstallServicesRequest,
	current store.AppInstance,
	metadata map[string]any,
	allServices, missing []string,
	definitions []serviceDefinition,
	revision, version, configHash string,
	now time.Time,
	lock store.AIFAROrchestrationLock,
	log Logger,
) (map[string]string, []store.AIFARDeployment, error) {
	control, controlOK := s.store.(aifarDeploymentControlStore)
	fenced, fencedOK := s.store.(aifarServiceInstallFencedStore)
	if !controlOK || !fencedOK {
		return nil, nil, errors.New("AIFAR per-service deployment control store is unavailable")
	}
	deployments, err := control.ListAIFARDeployments(current.ID)
	if err != nil {
		return nil, nil, err
	}
	byService := make(map[string]store.AIFARDeployment, len(deployments))
	for _, deployment := range deployments {
		byService[cleanAIFARServiceName(deployment.ServiceName)] = deployment
	}

	mutationMetadata := copyMetadata(metadata)
	mutationMetadata["services"] = allServices
	mutationMetadata["serviceCatalog"] = serviceCatalogMetadataForInstall(definitions, intFromMetadata(metadata, "gatewayPort", defaultGatewayPort), intFromMetadata(metadata, "webPort", defaultWebPort))
	mutationInstance := current
	mutationInstance.Metadata = mustJSONMetadata(mutationMetadata)
	acceptedRevisions := make(map[string]string, len(missing))
	acceptedDeployments := make([]store.AIFARDeployment, 0, len(missing))
	for _, serviceName := range missing {
		deployment, exists := byService[serviceName]
		if exists && deploymentHasAcceptedDesired(deployment) {
			acceptedRevisions[serviceName] = deployment.CurrentRevision
			acceptedDeployments = append(acceptedDeployments, deployment)
			if err := ctx.Err(); err != nil {
				return nil, nil, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
			}
			if err := saveAcceptedServiceReplicaSet(fenced, lock.ID, deployment, version, configHash, now); err != nil {
				return nil, nil, err
			}
			continue
		}

		if exists && deployment.Generation > 0 {
			if deployment.Status != "pending_acceptance" || strings.TrimSpace(deployment.SpecJSON) == "" {
				return nil, nil, repairRequired("AIFAR_RUNTIME_INSTALL_RETRY_STATE_INVALID", nil)
			}
			accepted, err := s.resumePendingServiceInstall(ctx, req, mutationInstance, deployment, lock, log)
			if err != nil {
				return nil, nil, err
			}
			byService[serviceName] = accepted
			acceptedRevisions[serviceName] = accepted.CurrentRevision
			acceptedDeployments = append(acceptedDeployments, accepted)
			if err := ctx.Err(); err != nil {
				return nil, nil, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
			}
			if err := saveAcceptedServiceReplicaSet(fenced, lock.ID, accepted, version, configHash, now); err != nil {
				return nil, nil, err
			}
			continue
		}

		attemptMetadata, err := encodeServiceInstallAttempt(serviceInstallAttempt{Revision: revision, Version: version, ConfigHash: configHash, CreatedAt: now})
		if err != nil {
			return nil, nil, err
		}
		base := store.AIFARDeployment{
			InstanceID: current.ID, ServiceName: serviceName, DesiredReplicas: 1,
			CurrentRevision: revision, Status: "install_prepared", MetadataJSON: attemptMetadata, CreatedAt: now,
		}
		accepted, err := s.MutateDeployment(ctx, DeploymentMutationRequest{
			Instance: mutationInstance, Server: req.Server, ServiceName: serviceName,
			ExpectedGeneration: 0, Actor: req.Actor, TaskID: req.TaskID, Operation: "install-service",
			LockID: lock.ID, InitialDeployment: &base,
			Mutate: func(manifest *runtimeagent.DeploymentManifest) error {
				manifest.Spec.Replicas = 1
				manifest.Spec.PodRevision = revision
				manifest.Spec.Image = "aifar-" + serviceName + ":" + revision
				return nil
			},
		}, log)
		if err != nil {
			return nil, nil, err
		}
		byService[serviceName] = accepted
		acceptedRevisions[serviceName] = accepted.CurrentRevision
		acceptedDeployments = append(acceptedDeployments, accepted)
		if err := ctx.Err(); err != nil {
			return nil, nil, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
		}
		if err := saveAcceptedServiceReplicaSet(fenced, lock.ID, accepted, version, configHash, now); err != nil {
			return nil, nil, err
		}
	}
	return acceptedRevisions, acceptedDeployments, nil
}

func (s Service) resumePendingServiceInstall(ctx context.Context, req InstallServicesRequest, instance store.AppInstance, pending store.AIFARDeployment, lock store.AIFAROrchestrationLock, log Logger) (store.AIFARDeployment, error) {
	manifest, err := buildRuntimeManifest(instance, pending, pending.Generation)
	if err != nil {
		return pending, repairRequired("AIFAR_RUNTIME_MANIFEST_BUILD_FAILED", err)
	}
	hash, err := runtimeagent.DeploymentManifestSpecHash(manifest)
	if err != nil {
		return pending, repairRequired("AIFAR_RUNTIME_MANIFEST_HASH_FAILED", err)
	}
	installRoot := stringFromMetadata(metadataFromInstance(instance), "installRoot", "")
	acceptance, acceptErr := s.acceptDeployment(ctx, req.Server, manifest, req.TaskID, installRoot)
	if acceptErr != nil {
		var typed *deploymentControlError
		if errors.As(acceptErr, &typed) && typed.ambiguous {
			state, readErr := s.readDeploymentStateOnce(ctx, req.Server, instance.ID, pending.ServiceName)
			if readErr != nil || !deploymentStateMatches(state, instance.ID, pending.ServiceName, pending.Generation, hash) {
				return pending, repairRequired("AIFAR_RUNTIME_AGENT_READBACK_MISMATCH", readErr)
			}
			acceptance = runtimeagent.DeploymentAcceptance{Accepted: true, Generation: state.Generation, SpecHash: state.SpecHash}
		} else {
			return pending, acceptErr
		}
	}
	if !acceptanceMatches(acceptance, pending.Generation, hash) {
		return pending, repairRequired("AIFAR_RUNTIME_AGENT_ACCEPTANCE_MISMATCH", nil)
	}
	if err := ctx.Err(); err != nil {
		return pending, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_LOST", err)
	}
	fenced, ok := s.store.(aifarDeploymentLockFencedStore)
	if !ok {
		return pending, repairRequired("AIFAR_RUNTIME_ORCHESTRATION_LOCK_FENCE_UNAVAILABLE", nil)
	}
	accepted, err := markDeploymentAcceptedWithLock(fenced, lock.ID, pending, pending.Generation)
	if err != nil {
		return pending, repairRequired("AIFAR_RUNTIME_CONTROL_STORE_ACCEPT_FAILED", err)
	}
	if log != nil {
		log.Info("AIFAR deployment accepted: service=%s generation=%d", pending.ServiceName, pending.Generation)
	}
	return accepted, nil
}

func saveAcceptedServiceReplicaSet(fenced aifarServiceInstallFencedStore, lockID string, expected store.AIFARDeployment, version, configHash string, now time.Time) error {
	_, err := fenced.SaveAIFARServiceInstallReplicaSetWithLock(lockID, expected, store.AIFARReplicaSet{
		InstanceID: expected.InstanceID, ServiceName: expected.ServiceName, Revision: expected.CurrentRevision,
		Image: "aifar-" + expected.ServiceName + ":" + expected.CurrentRevision, ArtifactHash: configHash,
		DesiredPods: 1, ReadyPods: 0, Status: "pending", MetadataJSON: fmt.Sprintf(`{"version":%q}`, version), CreatedAt: now,
	})
	return err
}

func mustJSONMetadata(metadata map[string]any) string {
	data, _ := json.Marshal(metadata)
	return string(data)
}

func serviceInstallSteps() []installStepDef {
	return []installStepDef{
		{Name: "validate-service-install", Title: "validate AIFAR service installation"},
		{Name: "render-service-install", Title: "render AIFAR service install script"},
		{Name: "apply-service-install", Title: "build and start missing AIFAR services"},
		{Name: "record-service-install", Title: "record AIFAR service control plane"},
	}
}

func normalizeRequestedAIFARServices(values []string, definitions []serviceDefinition) ([]string, error) {
	if len(values) == 0 {
		return nil, errors.New("select at least one AIFAR service")
	}
	selected := make(map[string]bool, len(values))
	for _, value := range values {
		service := cleanAIFARServiceName(value)
		if service == "" {
			continue
		}
		if !serviceNamePattern.MatchString(service) {
			return nil, fmt.Errorf("unsupported AIFAR service: %s", value)
		}
		selected[service] = true
	}
	out := make([]string, 0, len(selected))
	for _, service := range serviceNames(definitions) {
		if selected[service] {
			out = append(out, service)
			delete(selected, service)
		}
	}
	if len(selected) > 0 {
		unknown := make([]string, 0, len(selected))
		for service := range selected {
			unknown = append(unknown, service)
		}
		sort.Strings(unknown)
		return nil, fmt.Errorf("unsupported AIFAR service: %s", strings.Join(unknown, ", "))
	}
	if len(out) == 0 {
		return nil, errors.New("select at least one AIFAR service")
	}
	return out, nil
}

func missingServices(requested, installed []string) []string {
	installedSet := make(map[string]bool, len(installed))
	for _, service := range installed {
		installedSet[service] = true
	}
	out := make([]string, 0, len(requested))
	for _, service := range requested {
		if !installedSet[service] {
			out = append(out, service)
		}
	}
	return out
}

func mergeServices(existing, additions []string, catalogs ...[]serviceDefinition) []string {
	selected := make(map[string]bool, len(existing)+len(additions))
	for _, values := range [][]string{existing, additions} {
		for _, service := range values {
			service = cleanAIFARServiceName(service)
			if aifarServiceSupported(service) {
				selected[service] = true
			}
		}
	}
	definitions := legacyServiceDefinitions()
	if len(catalogs) > 0 && len(catalogs[0]) > 0 {
		definitions = catalogs[0]
	}
	out := make([]string, 0, len(selected))
	for _, definition := range definitions {
		if selected[definition.Name] {
			out = append(out, definition.Name)
			delete(selected, definition.Name)
		}
	}
	if len(selected) > 0 {
		remaining := make([]string, 0, len(selected))
		for service := range selected {
			remaining = append(remaining, service)
		}
		sort.Strings(remaining)
		out = append(out, remaining...)
	}
	return out
}

func serviceInstallConfigHash(baseHash string, services []string) string {
	data, _ := json.Marshal(map[string]any{
		"baseConfigHash":  baseHash,
		"installServices": services,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func serviceInstallOrchestrationMetadata(current map[string]any, installRoot, ingressNetwork string, gatewayPort, webPort int, installedServices []string, acceptedRevisions map[string]string) map[string]any {
	next := copyMetadata(current)
	existing := servicesFromMetadata(current)
	allServices := mergeServices(existing, installedServices)
	next["services"] = allServices
	next["orchestrationModel"] = orchestrationModelServiceControllerV1
	next["releasePhase"] = releasePhaseActive
	next["runtimeService"] = "aifar-agent"
	next["ingressNetwork"] = ingressNetwork
	next["activeRoutes"] = releaseRoutes(gatewayPort, webPort)
	activeEndpointSource := activeEndpointsFromMetadata(current)
	serviceRevisionSource := serviceRevisionsFromMetadata(current)
	containerSource := mapFromMetadataValue(current["containers"])
	activeEndpoints := map[string]any{}
	serviceRevisions := map[string]any{}
	containers := map[string]any{}
	for _, service := range allServices {
		if value, ok := activeEndpointSource[service]; ok {
			activeEndpoints[service] = value
		}
		if value, ok := serviceRevisionSource[service]; ok {
			serviceRevisions[service] = value
		}
		if containerSource != nil {
			if value, ok := containerSource[service]; ok {
				containers[service] = value
			}
		}
	}
	for _, service := range installedServices {
		serviceRevisions[service] = acceptedRevisions[service]
		delete(activeEndpoints, service)
		delete(containers, service)
	}
	delete(next, "desiredReplicas")
	next["activeEndpoints"] = activeEndpoints
	next["serviceRevisions"] = serviceRevisions
	next["containers"] = containers
	next["autoscalePolicy"] = autoscalePolicyFromMetadata(current).metadata()
	return next
}

func serviceInstallReleaseManifest(version, releaseID string, releaseTime time.Time, configHash, reason string, installedServices []string, orchestration map[string]any) map[string]any {
	manifest := map[string]any{
		"app":               AppName,
		"version":           version,
		"releaseId":         releaseID,
		"kind":              "service-install",
		"status":            "success",
		"phase":             releasePhaseActive,
		"configHash":        configHash,
		"createdAt":         releaseTime.Format(time.RFC3339),
		"installedServices": installedServices,
		"services":          servicesFromMetadata(orchestration),
	}
	if strings.TrimSpace(reason) != "" {
		manifest["reason"] = strings.TrimSpace(reason)
	}
	applyEffectiveReleaseFields(manifest, orchestration)
	applyEffectiveServiceFields(manifest, orchestration)
	return manifest
}

func renderServiceInstallScript(data serviceInstallScriptData) (string, error) {
	content, err := templateFS.ReadFile("templates/service-install.sh")
	if err != nil {
		return "", err
	}
	return installerkit.RenderTemplate(AppName, "service-install.sh", "aifar-service-install", string(content), selinux.AddTemplateFuncs(template.FuncMap{
		"quote": shellQuoteAny,
	}), data)
}
