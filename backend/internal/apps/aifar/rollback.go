package aifar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/store"
)

type rollbackArtifactRef struct {
	ServiceName string
	FileName    string
	SHA256      string
	RemotePath  string
}

func (s Service) ValidateArtifactRollback(req ArtifactRollbackRequest) error {
	copy := updateCopyFor(req.Language)
	if err := ensureK8sLikeInstance(req.Instance, copy); err != nil {
		return err
	}
	if strings.TrimSpace(req.TargetReleaseID) == "" {
		return errors.New("target release is required")
	}
	if strings.TrimSpace(req.Reason) == "" {
		return errors.New("rollback reason is required")
	}
	_, manifest, err := s.findReleaseManifest(req.Instance.ID, req.TargetReleaseID)
	if err != nil {
		return err
	}
	services := rollbackServicesFromRequest(req.Services, manifest)
	if len(services) == 0 {
		return errors.New("rollback services are required")
	}
	for _, service := range services {
		if !aifarServiceSupported(service) {
			return fmt.Errorf(copy.UnsupportedService, service)
		}
		if _, err := rollbackArtifactFromManifest(manifest, service); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) RollbackArtifact(ctx context.Context, req ArtifactRollbackRequest, log Logger, targetLog targetLogger) error {
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
	lockService := ""
	servicesForLock := req.Services
	if len(servicesForLock) == 1 {
		lockService = cleanAIFARServiceName(servicesForLock[0])
	}
	lockedInstance, err := s.acquireOrchestrationLock(req.Instance.ID, "rollback-artifact", lockService, req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	defer s.releaseOrchestrationLock(req.Instance.ID, "rollback-artifact", lockService)
	req.Instance = lockedInstance

	var targetRelease store.AppRelease
	var targetManifest map[string]any
	var metadata map[string]any
	var installRoot string
	var version string
	var rollbackTime time.Time
	var rollbackID string
	var currentBaseRevision string
	var configHash string
	var ingressNetwork string
	var gatewayPort int
	var webPort int
	var workDir string
	var rollbackDir string
	var artifacts []rollbackArtifactRef
	var serviceNames []string
	var revisionsBefore map[string]string

	if err := step(1, func() error {
		var err error
		targetRelease, targetManifest, err = s.findReleaseManifest(req.Instance.ID, req.TargetReleaseID)
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
		serviceNames = rollbackServicesFromRequest(req.Services, targetManifest)
		if len(serviceNames) == 0 {
			return errors.New("rollback services are required")
		}
		artifacts = make([]rollbackArtifactRef, 0, len(serviceNames))
		for _, service := range serviceNames {
			ref, err := rollbackArtifactFromManifest(targetManifest, service)
			if err != nil {
				return err
			}
			artifacts = append(artifacts, ref)
		}
		revisionsBefore = serviceRevisionMapBefore(metadata, serviceNames)
		currentBaseRevision = stringFromMetadata(metadata, "currentRevision", stringFromMetadata(metadata, "releaseId", ""))
		rollbackTime = time.Now().UTC()
		rollbackID = newReleaseID("rollback-"+sanitizeReleasePart(req.TargetReleaseID), rollbackTime)
		configHash = rollbackConfigHash(stringFromMetadata(metadata, "configHash", ""), req.TargetReleaseID, serviceNames)
		ingressNetwork = stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName))
		gatewayPort = intFromMetadata(metadata, "gatewayPort", defaultGatewayPort)
		webPort = intFromMetadata(metadata, "webPort", defaultWebPort)
		deployDir := installerkit.RemoteDeployDir(req.Server.DeployDir)
		workDir = installerkit.WorkDir(deployDir, AppName+"-rollback", version, rollbackTime)
		rollbackDir = releaseDirPath(installRoot, rollbackID)
		if releases, ok := s.store.(releaseStore); ok {
			manifest := rollbackReleaseManifest(version, rollbackID, rollbackTime, configHash, currentBaseRevision, req.TargetReleaseID, req.Reason, req.Actor, fallbackTaskID(req.TaskID, log), installRoot, ingressNetwork, gatewayPort, webPort, serviceNames, artifacts, revisionsBefore)
			manifest["status"] = "pending"
			manifest["phase"] = "pending"
			raw, _ := json.Marshal(manifest)
			if _, err := releases.SaveAppRelease(store.AppRelease{
				InstanceID:   req.Instance.ID,
				App:          AppName,
				Version:      version,
				ReleaseID:    rollbackID,
				ServerID:     target,
				Status:       "pending",
				ManifestJSON: string(raw),
				ConfigHash:   configHash,
				CreatedAt:    rollbackTime,
			}); err != nil {
				return err
			}
		}
		_ = targetRelease
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
		for _, artifact := range artifacts {
			script, err := renderRollbackScript(rollbackScriptData{
				InstallRoot:      installRoot,
				WorkDir:          workDir,
				RollbackDir:      rollbackDir,
				ServiceOrder:     strings.Join(servicesFromMetadata(metadata), " "),
				ServiceName:      artifact.ServiceName,
				DesiredReplicas:  replicaAssignments(map[string]int{artifact.ServiceName: desiredReplicasFromMetadata(metadata)[artifact.ServiceName]}),
				ArtifactRemote:   artifact.RemotePath,
				ArtifactFileName: artifact.FileName,
				ArtifactSHA256:   artifact.SHA256,
				TargetRevision:   req.TargetReleaseID,
				RollbackID:       rollbackID,
				CreatedAt:        rollbackTime.Format(time.RFC3339),
				ConfigHash:       configHash,
				IngressNetwork:   ingressNetwork,
			})
			if err != nil {
				return err
			}
			scriptLocal, err := installerkit.WriteTempScript("aifar-service-rollback-*.sh", script)
			if err != nil {
				return err
			}
			defer func(path string) {
				_ = removeTempFile(path)
			}(scriptLocal)
			if err := uploadkit.Upload(ctx, s.remote, req.Server, uploadkit.File{
				LocalPath:      scriptLocal,
				RemotePath:     workDir + "/rollback-" + artifact.ServiceName + ".sh",
				Mode:           0o755,
				LogMessage:     copy.UploadScript,
				FailureMessage: copy.UploadScriptFailed,
			}, logForServer); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(3, func() error {
		for _, artifact := range artifacts {
			logForServer.Info(copy.Deploying, artifact.ServiceName)
			if _, err := installerkit.Run(ctx, s.remote, req.Server, "sh "+installerkit.ShellQuote(workDir+"/rollback-"+artifact.ServiceName+".sh"), logForServer, copy.RemoteCommandFailed); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(4, func() error {
		targetRevision := req.TargetReleaseID
		metadata["releaseId"] = targetRevision
		metadata["currentRevision"] = targetRevision
		metadata["releaseVersion"] = version
		metadata["releaseCreatedAt"] = rollbackTime.Format(time.RFC3339)
		metadata["configHash"] = configHash
		orchestration := rolloutOrchestrationMetadata(metadata, installRoot, targetRevision, ingressNetwork, gatewayPort, webPort, serviceNames)
		for key, value := range orchestration {
			metadata[key] = value
		}
		metadata["lastRollout"] = map[string]any{
			"service":         "rollback",
			"changedServices": serviceNames,
			"baseReleaseId":   currentBaseRevision,
			"rollbackId":      rollbackID,
			"rollbackTo":      req.TargetReleaseID,
			"reason":          strings.TrimSpace(req.Reason),
			"updatedAt":       rollbackTime.Format(time.RFC3339),
		}
		data, _ := json.Marshal(metadata)
		next := req.Instance
		next.Status = "installed"
		next.Version = version
		next.Metadata = string(data)
		saved, err := s.store.SaveAppInstance(next)
		if err != nil {
			return err
		}
		desired := desiredReplicasFromMetadata(metadata)
		hashByService := map[string]string{}
		for _, artifact := range artifacts {
			hashByService[artifact.ServiceName] = artifact.SHA256
		}
		for _, service := range serviceNames {
			if err := s.saveRolloutControlPlane(saved.ID, version, targetRevision, service, hashByService[service], desired[service], gatewayPort, webPort, rollbackTime); err != nil {
				return err
			}
		}
		if releases, ok := s.store.(releaseStore); ok {
			manifest := rollbackReleaseManifest(version, rollbackID, rollbackTime, configHash, currentBaseRevision, req.TargetReleaseID, req.Reason, req.Actor, fallbackTaskID(req.TaskID, log), installRoot, ingressNetwork, gatewayPort, webPort, serviceNames, artifacts, revisionsBefore)
			raw, _ := json.Marshal(manifest)
			if _, err := releases.SaveAppRelease(store.AppRelease{
				InstanceID:   saved.ID,
				App:          AppName,
				Version:      version,
				ReleaseID:    rollbackID,
				ServerID:     target,
				Status:       "success",
				ManifestJSON: string(raw),
				ConfigHash:   configHash,
				CreatedAt:    rollbackTime,
				ActivatedAt:  rollbackTime,
			}); err != nil {
				return err
			}
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

	logForServer.Info(copy.Updated, rollbackID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func (s Service) findReleaseManifest(instanceID, releaseID string) (store.AppRelease, map[string]any, error) {
	releases, ok := s.store.(releaseStore)
	if !ok {
		return store.AppRelease{}, nil, errors.New("release history is unavailable")
	}
	items, err := releases.ListAppReleases(instanceID)
	if err != nil {
		return store.AppRelease{}, nil, err
	}
	for _, item := range items {
		if item.ReleaseID != releaseID {
			continue
		}
		manifest := map[string]any{}
		if strings.TrimSpace(item.ManifestJSON) != "" {
			_ = json.Unmarshal([]byte(item.ManifestJSON), &manifest)
		}
		return item, manifest, nil
	}
	return store.AppRelease{}, nil, fmt.Errorf("target release not found: %s", releaseID)
}

func rollbackServicesFromRequest(requested []string, manifest map[string]any) []string {
	services := make([]string, 0, len(requested))
	seen := map[string]bool{}
	for _, service := range requested {
		service = cleanAIFARServiceName(service)
		if service == "" || seen[service] {
			continue
		}
		seen[service] = true
		services = append(services, service)
	}
	if len(services) > 0 {
		return services
	}
	for _, service := range stringsFromManifestValue(manifest["changedServices"]) {
		service = cleanAIFARServiceName(service)
		if service == "" || seen[service] {
			continue
		}
		seen[service] = true
		services = append(services, service)
	}
	return services
}

func rollbackArtifactFromManifest(manifest map[string]any, service string) (rollbackArtifactRef, error) {
	service = cleanAIFARServiceName(service)
	artifacts, _ := manifest["artifacts"].(map[string]any)
	raw, _ := artifacts[service].(map[string]any)
	ref := rollbackArtifactRef{
		ServiceName: service,
		FileName:    strings.TrimSpace(fmt.Sprint(raw["file"])),
		SHA256:      strings.TrimSpace(fmt.Sprint(raw["sha256"])),
		RemotePath:  strings.TrimSpace(fmt.Sprint(raw["remotePath"])),
	}
	if ref.FileName == "" || ref.SHA256 == "" || ref.RemotePath == "" {
		return rollbackArtifactRef{}, fmt.Errorf("release artifact for service %s is not rollback-capable", service)
	}
	return ref, nil
}

func stringsFromManifestValue(value any) []string {
	switch raw := value.(type) {
	case []string:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if text := strings.TrimSpace(item); text != "" {
				out = append(out, text)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(raw))
		for _, item := range raw {
			if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func rollbackConfigHash(baseHash, targetReleaseID string, services []string) string {
	data, _ := json.Marshal(map[string]any{
		"baseConfigHash": baseHash,
		"rollbackTo":     targetReleaseID,
		"services":       services,
	})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func rollbackReleaseManifest(version, rollbackID string, rollbackTime time.Time, configHash, baseReleaseID, rollbackTo, reason, actor, taskID, installRoot, ingressNetwork string, gatewayPort, webPort int, services []string, artifacts []rollbackArtifactRef, before map[string]string) map[string]any {
	artifactMap := make(map[string]any, len(artifacts))
	for _, artifact := range artifacts {
		artifactMap[artifact.ServiceName] = map[string]any{
			"type":       artifactTypeForService(artifact.ServiceName, artifact.FileName),
			"file":       artifact.FileName,
			"sha256":     artifact.SHA256,
			"remotePath": artifact.RemotePath,
		}
	}
	manifest := map[string]any{
		"schema":                 releaseManifestSchemaV2,
		"app":                    AppName,
		"version":                version,
		"releaseId":              rollbackID,
		"kind":                   "rollback",
		"status":                 "success",
		"phase":                  releasePhaseActive,
		"configHash":             configHash,
		"baseReleaseId":          rollbackTo,
		"previousRevision":       baseReleaseID,
		"rollbackFrom":           baseReleaseID,
		"rollbackTo":             rollbackTo,
		"reason":                 strings.TrimSpace(reason),
		"actor":                  actor,
		"taskId":                 taskID,
		"createdAt":              rollbackTime.Format(time.RFC3339),
		"releaseDir":             releaseDirPath(installRoot, rollbackID),
		"services":               serviceListOrDefault(services),
		"changedServices":        serviceListOrDefault(services),
		"serviceRevisionsBefore": before,
		"serviceRevisionsAfter":  serviceRevisionMapAfter(rollbackTo, services),
		"artifacts":              artifactMap,
	}
	for key, value := range releaseManifestFields(rollbackTo, ingressNetwork, gatewayPort, webPort, services) {
		manifest[key] = value
	}
	return manifest
}

func removeTempFile(path string) error {
	return os.Remove(path)
}
