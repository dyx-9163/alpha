package aifar

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/store"
)

const (
	artifactBundleSchema       = "aifar-artifact-bundle-v1"
	artifactBundleManifestName = "manifest.json"
)

type artifactBundleManifest struct {
	Schema   string                          `json:"schema"`
	App      string                          `json:"app"`
	Kind     string                          `json:"kind"`
	Services []artifactBundleManifestService `json:"services"`
}

type artifactBundleManifestService struct {
	Service  string `json:"service"`
	Module   string `json:"module"`
	Artifact string `json:"artifact"`
	FileName string `json:"fileName"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

type artifactBundleItem struct {
	ServiceName string
	FileName    string
	LocalPath   string
	SHA256      string
	Size        int64
}

func (s Service) ValidateArtifactBundleUpdate(req ArtifactBundleUpdateRequest) error {
	copy := updateCopyFor(req.Language)
	if err := ensureK8sLikeInstance(req.Instance, copy); err != nil {
		return err
	}
	_, cleanup, err := s.artifactBundleItemsFromRequest(req, copy, false)
	if cleanup != nil {
		cleanup()
	}
	return err
}

func (s Service) UpdateArtifactBundle(ctx context.Context, req ArtifactBundleUpdateRequest, log Logger, targetLog targetLogger) (resultErr error) {
	copy := updateCopyFor(req.Language)
	items, cleanup, err := s.artifactBundleItemsFromRequest(req, copy, true)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return errors.New(copy.BundleEmpty)
	}
	orderBundleItems(items)

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
	lockedInstance, lock, err := s.acquireOrchestrationLock(req.Instance.ID, "update-artifact-bundle", "", req.Actor, fallbackTaskID(req.TaskID, log))
	if err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	defer s.releaseOrchestrationLock(lock)
	req.Instance = lockedInstance
	concurrency := store.NormalizeDeploymentConcurrency(fmt.Sprint(req.Concurrency), 1)
	artifacts := make([]artifactInfo, 0, len(items))
	for _, item := range items {
		artifacts = append(artifacts, artifactInfo{
			ServiceName: item.ServiceName,
			FileName:    item.FileName,
			SHA256:      item.SHA256,
			Size:        item.Size,
		})
	}

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
	var scriptRemote string
	var scriptArtifacts []bundleUpdateScriptArtifact
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

	log.Info(copy.BundleUpdating, len(items))

	if err := step(1, func() error {
		if err := ctx.Err(); err != nil {
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
		baseReleaseID = stringFromMetadata(metadata, "currentRevision", stringFromMetadata(metadata, "releaseId", ""))
		serviceRevisionsBefore = serviceRevisionMapBefore(metadata, artifactServiceNames(artifacts))
		releaseTime = time.Now().UTC()
		releaseID = newReleaseID("rollout-bundle", releaseTime)
		configHash = partialBundleUpdateConfigHash(stringFromMetadata(metadata, "configHash", ""), artifacts)
		ingressNetwork = stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName))
		gatewayPort = intFromMetadata(metadata, "gatewayPort", defaultGatewayPort)
		webPort = intFromMetadata(metadata, "webPort", defaultWebPort)
		deployDir := installerkit.RemoteDeployDir(req.Server.DeployDir)
		workDir = installerkit.WorkDir(deployDir, AppName+"-bundle", version, releaseTime)
		releaseDir = releaseDirPath(installRoot, releaseID)
		scriptRemote = workDir + "/update-aifar-artifact-bundle.sh"
		scriptArtifacts = make([]bundleUpdateScriptArtifact, 0, len(items))
		for _, item := range items {
			scriptArtifacts = append(scriptArtifacts, bundleUpdateScriptArtifact{
				ServiceName:     item.ServiceName,
				ArtifactRemote:  workDir + "/" + item.ServiceName + "/" + installerkit.Sanitize(item.FileName),
				ReleaseArtifact: releaseServiceArtifactPath(installRoot, releaseID, item.ServiceName, item.FileName),
				ArtifactFile:    item.FileName,
				ArtifactSHA256:  item.SHA256,
				ArtifactSize:    item.Size,
			})
		}
		if releases, ok := s.store.(releaseStore); ok {
			orchestration := rolloutOrchestrationMetadata(metadata, installRoot, releaseID, ingressNetwork, gatewayPort, webPort, artifactServiceNames(artifacts))
			manifest := rolloutBundleReleaseManifest(version, releaseID, releaseTime, configHash, baseReleaseID, ingressNetwork, gatewayPort, webPort, artifacts, concurrency, orchestration, installRoot, req.Actor, fallbackTaskID(req.TaskID, log), serviceRevisionsBefore)
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
		for idx, item := range items {
			logForServer.Info(copy.BundleServiceUpdating, idx+1, len(items), item.ServiceName)
			if err := uploadkit.Upload(ctx, s.remote, req.Server, uploadkit.File{
				LocalPath:      item.LocalPath,
				RemotePath:     scriptArtifacts[idx].ArtifactRemote,
				LogMessage:     copy.UploadArtifact,
				LogArgs:        []any{item.ServiceName, item.FileName},
				FailureMessage: copy.UploadArtifactFailed,
			}, logForServer); err != nil {
				return err
			}
		}
		script, err := renderBundleUpdateScript(bundleUpdateScriptData{
			InstallRoot:     installRoot,
			WorkDir:         workDir,
			ReleaseDir:      releaseDir,
			ServiceOrder:    strings.Join(servicesFromMetadata(metadata), " "),
			ChangedServices: artifactServiceNamesText(artifacts),
			Artifacts:       scriptArtifacts,
			Version:         version,
			ReleaseID:       releaseID,
			CreatedAt:       releaseTime.Format(time.RFC3339),
			ConfigHash:      configHash,
			IngressNetwork:  ingressNetwork,
			Concurrency:     concurrency,
			DesiredReplicas: replicaAssignmentsForServices(desiredReplicasFromMetadata(metadata), artifactServiceNames(artifacts)),
		})
		if err != nil {
			return err
		}
		scriptLocal, err := installerkit.WriteTempScript("aifar-service-bundle-update-*.sh", script)
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
		logForServer.Info(copy.Deploying, artifactServiceNamesText(artifacts))
		_, err := installerkit.Run(ctx, s.remote, req.Server, "sh "+installerkit.ShellQuote(scriptRemote), logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.UpdateFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(4, func() error {
		metadata["releaseId"] = releaseID
		metadata["currentRevision"] = releaseID
		metadata["releaseVersion"] = version
		metadata["releaseCreatedAt"] = releaseTime.Format(time.RFC3339)
		metadata["configHash"] = configHash
		orchestration := rolloutOrchestrationMetadata(metadata, installRoot, releaseID, ingressNetwork, gatewayPort, webPort, artifactServiceNames(artifacts))
		for key, value := range orchestration {
			metadata[key] = value
		}
		metadata["lastRollout"] = map[string]any{
			"service":               "bundle",
			"changedServices":       artifactServiceNames(artifacts),
			"baseReleaseId":         baseReleaseID,
			"releaseId":             releaseID,
			"updatedAt":             releaseTime.Format(time.RFC3339),
			"deploymentConcurrency": concurrency,
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
		for _, artifact := range artifacts {
			if err := s.saveRolloutControlPlane(saved.ID, version, releaseID, artifact.ServiceName, artifact.SHA256, desired[artifact.ServiceName], gatewayPort, webPort, releaseTime); err != nil {
				return err
			}
		}
		if releases, ok := s.store.(releaseStore); ok {
			manifest, _ := json.Marshal(rolloutBundleReleaseManifest(version, releaseID, releaseTime, configHash, baseReleaseID, ingressNetwork, gatewayPort, webPort, artifacts, concurrency, orchestration, installRoot, req.Actor, fallbackTaskID(req.TaskID, log), serviceRevisionsBefore))
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
				return err
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

	logForServer.Info(copy.BundleUpdated, target, len(items))
	finishTarget(recorder, target, "success", "")
	return nil
}

func orderBundleItems(items []artifactBundleItem) {
	order := make(map[string]int, len(serviceOrder))
	for idx, service := range serviceOrder {
		order[service] = idx
	}
	sort.SliceStable(items, func(i, j int) bool {
		return order[items[i].ServiceName] < order[items[j].ServiceName]
	})
}

func artifactServiceNames(artifacts []artifactInfo) []string {
	out := make([]string, 0, len(artifacts))
	for _, artifact := range artifacts {
		out = append(out, artifact.ServiceName)
	}
	return out
}

func artifactServiceNamesText(artifacts []artifactInfo) string {
	return strings.Join(artifactServiceNames(artifacts), " ")
}

func (s Service) artifactBundleItemsFromRequest(req ArtifactBundleUpdateRequest, copy UpdateCopy, extract bool) ([]artifactBundleItem, func(), error) {
	if req.Instance.App != AppName {
		return nil, nil, errors.New(copy.UnsupportedInstance)
	}
	if strings.TrimSpace(req.Instance.ServerID) == "" && strings.TrimSpace(req.Server.ID) == "" {
		return nil, nil, errors.New(copy.TargetRequired)
	}
	bundlePath := strings.TrimSpace(req.BundleLocalPath)
	if bundlePath == "" {
		return nil, nil, errors.New(copy.BundleRequired)
	}
	stat, err := os.Stat(bundlePath)
	if err != nil {
		return nil, nil, err
	}
	if stat.IsDir() || stat.Size() == 0 {
		return nil, nil, errors.New(copy.BundleRequired)
	}
	if !strings.EqualFold(filepath.Ext(req.BundleFileName), ".zip") && !strings.EqualFold(filepath.Ext(bundlePath), ".zip") {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, "update bundle must be a zip file")
	}

	reader, err := zip.OpenReader(bundlePath)
	if err != nil {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, err)
	}
	defer reader.Close()

	entries := map[string]*zip.File{}
	for _, file := range reader.File {
		clean, err := cleanBundleZipPath(file.Name)
		if err != nil {
			return nil, nil, fmt.Errorf(copy.BundleInvalid, err)
		}
		if file.FileInfo().IsDir() {
			continue
		}
		entries[clean] = file
	}
	manifestFile := entries[artifactBundleManifestName]
	if manifestFile == nil {
		return nil, nil, errors.New(copy.BundleManifestRequired)
	}
	manifest, err := readArtifactBundleManifest(manifestFile)
	if err != nil {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, err)
	}
	if manifest.Schema != artifactBundleSchema {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, "manifest schema is not supported")
	}
	if strings.TrimSpace(manifest.App) != "" && !strings.EqualFold(manifest.App, AppName) {
		return nil, nil, fmt.Errorf(copy.BundleInvalid, "manifest app is not aifar")
	}
	if len(manifest.Services) == 0 {
		return nil, nil, errors.New(copy.BundleEmpty)
	}

	tempDir := ""
	cleanup := func() {}
	if extract {
		tempDir, err = os.MkdirTemp("", "aifar-artifact-bundle-*")
		if err != nil {
			return nil, nil, err
		}
		cleanup = func() {
			_ = os.RemoveAll(tempDir)
		}
	}
	fail := func(err error) ([]artifactBundleItem, func(), error) {
		if extract {
			cleanup()
		}
		return nil, nil, err
	}

	seen := map[string]bool{}
	items := make([]artifactBundleItem, 0, len(manifest.Services))
	for _, service := range manifest.Services {
		serviceName := cleanAIFARServiceName(service.Service)
		if !aifarServiceSupported(serviceName) {
			return fail(fmt.Errorf(copy.UnsupportedService, service.Service))
		}
		if seen[serviceName] {
			return fail(fmt.Errorf(copy.BundleInvalid, fmt.Sprintf(copy.BundleDuplicateService, serviceName)))
		}
		seen[serviceName] = true
		artifactPath, err := cleanBundleZipPath(service.Artifact)
		if err != nil {
			return fail(fmt.Errorf(copy.BundleInvalid, err))
		}
		if artifactPath == artifactBundleManifestName {
			return fail(fmt.Errorf(copy.BundleInvalid, "manifest cannot be used as an artifact"))
		}
		zipFile := entries[artifactPath]
		if zipFile == nil {
			return fail(fmt.Errorf(copy.BundleArtifactMissing, artifactPath))
		}
		fileName := safeBundleArtifactFileName(service.FileName, artifactPath)
		if fileName == "" {
			return fail(errors.New(copy.ArtifactRequired))
		}
		if !artifactTypeAllowedForInstance(req.Instance, serviceName, fileName) {
			return fail(fmt.Errorf(copy.ArtifactTypeInvalid, serviceName))
		}
		if !artifactFileMatchesService(serviceName, fileName) {
			return fail(fmt.Errorf(copy.ArtifactTypeInvalid, serviceName))
		}
		item := artifactBundleItem{ServiceName: serviceName, FileName: fileName}
		if extract {
			localPath := filepath.Join(tempDir, serviceName, fileName)
			sum, size, err := extractBundleArtifact(zipFile, localPath)
			if err != nil {
				return fail(err)
			}
			item.LocalPath = localPath
			item.SHA256 = sum
			item.Size = size
		} else {
			sum, size, err := hashBundleArtifact(zipFile)
			if err != nil {
				return fail(err)
			}
			item.SHA256 = sum
			item.Size = size
		}
		if service.Size > 0 && service.Size != item.Size {
			return fail(fmt.Errorf(copy.BundleInvalid, fmt.Sprintf("artifact %s size mismatch: expected %d, got %d", artifactPath, service.Size, item.Size)))
		}
		if expected := strings.TrimSpace(service.SHA256); expected != "" && !strings.EqualFold(expected, item.SHA256) {
			return fail(fmt.Errorf(copy.BundleInvalid, fmt.Sprintf("artifact %s sha256 mismatch: expected %s, got %s", artifactPath, expected, item.SHA256)))
		}
		items = append(items, item)
	}
	return items, cleanup, nil
}

func readArtifactBundleManifest(file *zip.File) (artifactBundleManifest, error) {
	var manifest artifactBundleManifest
	reader, err := file.Open()
	if err != nil {
		return manifest, err
	}
	defer reader.Close()
	data, err := io.ReadAll(reader)
	if err != nil {
		return manifest, err
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &manifest); err != nil {
		return manifest, err
	}
	return manifest, nil
}

func cleanBundleZipPath(value string) (string, error) {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	if value == "" || strings.Contains(value, "\x00") || path.IsAbs(value) || strings.Contains(value, ":") {
		return "", fmt.Errorf("unsafe bundle path: %s", value)
	}
	clean := path.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("unsafe bundle path: %s", value)
	}
	return clean, nil
}

func safeBundleArtifactFileName(fileName, artifactPath string) string {
	fileName = strings.TrimSpace(strings.ReplaceAll(fileName, "\\", "/"))
	if fileName == "" {
		fileName = path.Base(artifactPath)
	}
	fileName = path.Base(fileName)
	if fileName == "." || fileName == "/" {
		return ""
	}
	return fileName
}

func hashBundleArtifact(file *zip.File) (string, int64, error) {
	reader, err := file.Open()
	if err != nil {
		return "", 0, err
	}
	defer reader.Close()
	hash := sha256.New()
	size, err := io.Copy(hash, reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}

func extractBundleArtifact(file *zip.File, localPath string) (string, int64, error) {
	reader, err := file.Open()
	if err != nil {
		return "", 0, err
	}
	defer reader.Close()
	if err := os.MkdirAll(filepath.Dir(localPath), 0o755); err != nil {
		return "", 0, err
	}
	out, err := os.OpenFile(localPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return "", 0, err
	}
	defer out.Close()
	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(out, hash), reader)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(hash.Sum(nil)), size, nil
}
