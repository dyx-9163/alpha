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
	"text/template"
	"time"
	"unicode"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
	"aifar-deployment/backend/internal/installer/uploadkit"
	"aifar-deployment/backend/internal/store"
)

//go:embed templates/install.sh templates/uninstall.sh templates/update-artifact.sh templates/update-artifact-bundle.sh
var templateFS embed.FS

type Logger = installerkit.Logger
type Remote = installerkit.Remote

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	GetAppInstance(id string) (store.AppInstance, error)
	ListAppInstances() ([]store.AppInstance, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	DeleteAppInstance(id string) error
}

type releaseStore interface {
	SaveAppRelease(v store.AppRelease) (store.AppRelease, error)
	DeleteOldAppReleases(instanceID string, keep int) (int, error)
}

type InstallRequest struct {
	Version    string
	Topology   string
	Language   string
	ServerID   string
	Parameters map[string]any
}

type DeleteRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
}

type CheckRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
}

type ArtifactUpdateRequest struct {
	Instance          store.AppInstance
	Server            store.Server
	Language          string
	Actor             string
	ServiceName       string
	ArtifactLocalPath string
	ArtifactFileName  string
}

type ArtifactBundleUpdateRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	Actor           string
	BundleLocalPath string
	BundleFileName  string
	Concurrency     int
}

type CheckResult struct {
	Status  string
	Message string
	Details map[string]any
}

type Service struct {
	store  Store
	remote Remote
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

func NewService(s Store, remote Remote) Service {
	return Service{store: s, remote: remote}
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
	var archiveLocal string
	options := optionsFromParameters(req.Parameters)
	if err := step(2, func() error {
		var resolveErr error
		options, resolveErr = s.resolveInstallOptions(options)
		if resolveErr != nil {
			return resolveErr
		}
		var bundleErr error
		bundle, bundleErr = SelectBundle(resources, req.Version)
		if bundleErr != nil {
			return bundleErr
		}
		if err := VerifyBundle(bundle); err != nil {
			return err
		}
		if err := options.Validate(); err != nil {
			return err
		}
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
	composeProject := composeProjectName(releaseID)
	ingressNetwork := options.NetworkName
	internalNetwork := releaseInternalNetworkName(releaseID)
	ingressContainer := ingressContainerName()
	workDir := installerkit.WorkDir(deployDir, AppName, bundle.Version, releaseTime)
	installRoot := installRootFromDeployDir(deployDir)
	archiveRemote := workDir + "/" + filepath.Base(archiveLocal)
	scriptRemote := workDir + "/install-aifar.sh"

	if err := step(3, func() error {
		logForServer.Info(copy.PrepareWorkDir, workDir)
		if _, err := installerkit.Run(ctx, s.remote, server, "mkdir -p "+installerkit.ShellQuote(workDir), logForServer, copy.RemoteCommandFailed); err != nil {
			return err
		}
		if err := uploadkit.Upload(ctx, s.remote, server, uploadkit.File{
			LocalPath:      archiveLocal,
			RemotePath:     archiveRemote,
			LogMessage:     copy.UploadBundle,
			LogArgs:        []any{bundle.Root},
			FailureMessage: copy.UploadBundleFailed,
		}, logForServer); err != nil {
			return err
		}
		script, err := renderInstallScript(installScriptData{
			InstallRoot:      installRoot,
			WorkDir:          workDir,
			ArchiveRemote:    archiveRemote,
			ServiceOrder:     serviceOrderText(),
			Version:          bundle.Version,
			ReleaseID:        releaseID,
			CreatedAt:        releaseTime.Format(time.RFC3339),
			ConfigHash:       configHash,
			ReleaseKeepCount: releaseKeepCount,
			ComposeProject:   composeProject,
			IngressNetwork:   ingressNetwork,
			InternalNetwork:  internalNetwork,
			IngressContainer: ingressContainer,
			Options:          options,
		})
		if err != nil {
			return err
		}
		scriptLocal, err := installerkit.WriteTempScript("aifar-service-install-*.sh", script)
		if err != nil {
			return err
		}
		defer os.Remove(scriptLocal)
		return uploadkit.Upload(ctx, s.remote, server, uploadkit.File{
			LocalPath:      scriptLocal,
			RemotePath:     scriptRemote,
			Mode:           0o755,
			LogMessage:     copy.UploadScript,
			FailureMessage: copy.UploadScriptFailed,
		}, logForServer)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	if err := step(4, func() error {
		logForServer.Info(copy.Deploying)
		_, err := installerkit.Run(ctx, s.remote, server, "sh "+installerkit.ShellQuote(scriptRemote), logForServer, copy.RemoteCommandFailed)
		return err
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}

	var instance store.AppInstance
	if err := step(5, func() error {
		metadata := installMetadata(server, installRoot, bundle.Version, releaseID, releaseTime, configHash, options)
		instanceID, err := s.existingAIFARInstanceID(target, installRoot)
		if err != nil {
			return err
		}
		data, _ := json.Marshal(metadata)
		instance = store.AppInstance{
			ID:       instanceID,
			App:      AppName,
			Version:  bundle.Version,
			ServerID: target,
			Status:   "installed",
			Topology: defaultTopology,
			Metadata: string(data),
		}
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(instance)
		if saveErr != nil {
			return saveErr
		}
		if releases, ok := s.store.(releaseStore); ok {
			manifest, _ := json.Marshal(releaseManifest(bundle.Version, releaseID, releaseTime, configHash, ingressNetwork, options.GatewayPort, options.WebPort))
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
			if _, err := releases.DeleteOldAppReleases(instance.ID, releaseKeepCount); err != nil {
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

	logForServer.Info(copy.Installed, instance.ID)
	finishTarget(recorder, target, "success", "")
	return nil
}

func installMetadata(server store.Server, installRoot, version, releaseID string, releaseTime time.Time, configHash string, options InstallOptions) map[string]any {
	metadata := map[string]any{
		"installRoot":      installRoot,
		"layout":           releaseLayout,
		"currentRelease":   installRoot + "/" + currentLinkName,
		"releaseId":        releaseID,
		"releasePath":      installRoot + "/" + releasesDirName + "/" + releaseID,
		"releaseVersion":   version,
		"releaseRetention": releaseKeepCount,
		"releaseCreatedAt": releaseTime.Format(time.RFC3339),
		"configHash":       configHash,
		"appDir":           installRoot + "/" + currentLinkName + "/" + appBundleDir,
		"envDir":           installRoot + "/" + currentLinkName + "/" + releaseEnvDirName,
		"networkName":      options.NetworkName,
		"endpoint":         fmt.Sprintf("%s:%d", server.Host, options.WebPort),
		"gatewayEndpoint":  fmt.Sprintf("%s:%d", server.Host, options.GatewayPort),
		"nacosEndpoint":    fmt.Sprintf("%s:%d", options.NacosHost, options.NacosWebPort),
		"webPort":          options.WebPort,
		"gatewayPort":      options.GatewayPort,
		"nacosSource":      options.NacosSource,
		"nacosInstanceId":  options.NacosInstanceID,
		"nacosHost":        options.NacosHost,
		"nacosPort":        options.NacosWebPort,
		"nacosWebPort":     options.NacosWebPort,
		"nacosApiPort":     options.NacosAPIPort,
		"services":         serviceOrder,
	}
	for key, value := range releaseOrchestrationMetadata(installRoot, releaseID, options.NetworkName, options.GatewayPort, options.WebPort) {
		metadata[key] = value
	}
	return metadata
}

func partialOrchestrationMetadata(current map[string]any, installRoot, releaseID, ingressNetwork string, gatewayPort, webPort int, changedServices []string) map[string]any {
	next := releaseOrchestrationMetadata(installRoot, releaseID, ingressNetwork, gatewayPort, webPort)
	changed := map[string]bool{}
	for _, service := range changedServices {
		changed[service] = true
	}
	if currentRoutes, ok := current["activeRoutes"].(map[string]any); ok {
		routes := map[string]any{}
		for key, value := range currentRoutes {
			routes[key] = value
		}
		if changed["gateway"] {
			routes["gateway"] = map[string]any{"container": releaseContainerName("gateway", releaseID), "port": gatewayPort}
		}
		if changed["web-vue3"] {
			routes["web-vue3"] = map[string]any{"container": releaseContainerName("web-vue3", releaseID), "port": webPort}
		}
		next["activeRoutes"] = routes
	}
	if currentContainers, ok := current["containers"].(map[string]any); ok {
		containers := map[string]any{}
		for key, value := range currentContainers {
			containers[key] = value
		}
		for _, service := range changedServices {
			containers[service] = releaseContainerName(service, releaseID)
		}
		next["containers"] = containers
	}
	return next
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

func releaseManifest(version, releaseID string, releaseTime time.Time, configHash, ingressNetwork string, gatewayPort, webPort int) map[string]any {
	manifest := map[string]any{
		"app":              AppName,
		"version":          version,
		"releaseId":        releaseID,
		"layout":           releaseLayout,
		"kind":             "full",
		"status":           "success",
		"configHash":       configHash,
		"createdAt":        releaseTime.Format(time.RFC3339),
		"releaseRetention": releaseKeepCount,
		"services":         serviceOrder,
	}
	for key, value := range releaseManifestFields(releaseID, ingressNetwork, gatewayPort, webPort) {
		manifest[key] = value
	}
	return manifest
}

func partialReleaseManifest(version, releaseID string, releaseTime time.Time, configHash, baseReleaseID, ingressNetwork string, gatewayPort, webPort int, artifact artifactInfo, orchestration map[string]any) map[string]any {
	manifest := map[string]any{
		"app":              AppName,
		"version":          version,
		"releaseId":        releaseID,
		"layout":           releaseLayout,
		"kind":             "partial",
		"status":           "success",
		"configHash":       configHash,
		"baseReleaseId":    baseReleaseID,
		"createdAt":        releaseTime.Format(time.RFC3339),
		"releaseRetention": releaseKeepCount,
		"services":         serviceOrder,
		"changedServices":  []string{artifact.ServiceName},
		"artifacts": map[string]any{
			artifact.ServiceName: map[string]any{
				"file":   artifact.FileName,
				"sha256": artifact.SHA256,
				"size":   artifact.Size,
			},
		},
	}
	for key, value := range releaseManifestFields(releaseID, ingressNetwork, gatewayPort, webPort) {
		manifest[key] = value
	}
	applyEffectiveReleaseFields(manifest, orchestration)
	return manifest
}

func partialBundleReleaseManifest(version, releaseID string, releaseTime time.Time, configHash, baseReleaseID, ingressNetwork string, gatewayPort, webPort int, artifacts []artifactInfo, concurrency int, orchestration map[string]any) map[string]any {
	changed := make([]string, 0, len(artifacts))
	artifactMap := make(map[string]any, len(artifacts))
	for _, artifact := range artifacts {
		changed = append(changed, artifact.ServiceName)
		artifactMap[artifact.ServiceName] = map[string]any{
			"file":   artifact.FileName,
			"sha256": artifact.SHA256,
			"size":   artifact.Size,
		}
	}
	manifest := map[string]any{
		"app":                   AppName,
		"version":               version,
		"releaseId":             releaseID,
		"layout":                releaseLayout,
		"kind":                  "partial",
		"status":                "success",
		"configHash":            configHash,
		"baseReleaseId":         baseReleaseID,
		"createdAt":             releaseTime.Format(time.RFC3339),
		"releaseRetention":      releaseKeepCount,
		"services":              serviceOrder,
		"changedServices":       changed,
		"deploymentConcurrency": concurrency,
		"artifacts":             artifactMap,
	}
	for key, value := range releaseManifestFields(releaseID, ingressNetwork, gatewayPort, webPort) {
		manifest[key] = value
	}
	applyEffectiveReleaseFields(manifest, orchestration)
	return manifest
}

type artifactInfo struct {
	ServiceName string
	FileName    string
	SHA256      string
	Size        int64
}

func (s Service) ValidateArtifactUpdate(req ArtifactUpdateRequest) error {
	copy := updateCopyFor(req.Language)
	_, err := artifactInfoFromRequest(req, copy)
	return err
}

func (s Service) UpdateArtifact(ctx context.Context, req ArtifactUpdateRequest, log Logger, targetLog targetLogger) error {
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

	var artifact artifactInfo
	var metadata map[string]any
	var installRoot string
	var version string
	var releaseTime time.Time
	var releaseID string
	var baseReleaseID string
	var configHash string
	var composeProject string
	var ingressNetwork string
	var internalNetwork string
	var ingressContainer string
	var gatewayPort int
	var webPort int
	var workDir string
	var artifactRemote string
	var scriptRemote string

	if err := step(1, func() error {
		var err error
		artifact, err = artifactInfoFromRequest(req, copy)
		if err != nil {
			return err
		}
		metadata = metadataFromInstance(req.Instance)
		installRoot = stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))
		version = stringFromMetadata(metadata, "releaseVersion", req.Instance.Version)
		if strings.TrimSpace(version) == "" {
			version = appBundleVersion
		}
		baseReleaseID = stringFromMetadata(metadata, "releaseId", "")
		releaseTime = time.Now().UTC()
		releaseID = newReleaseID("partial-"+artifact.ServiceName, releaseTime)
		configHash = partialUpdateConfigHash(stringFromMetadata(metadata, "configHash", ""), artifact.ServiceName, artifact.FileName, artifact.SHA256)
		composeProject = composeProjectName(releaseID)
		ingressNetwork = stringFromMetadata(metadata, "ingressNetwork", stringFromMetadata(metadata, "networkName", defaultNetworkName))
		internalNetwork = releaseInternalNetworkName(releaseID)
		ingressContainer = stringFromMetadata(metadata, "ingressContainer", ingressContainerName())
		gatewayPort = intFromMetadata(metadata, "gatewayPort", defaultGatewayPort)
		webPort = intFromMetadata(metadata, "webPort", defaultWebPort)
		deployDir := installerkit.RemoteDeployDir(req.Server.DeployDir)
		workDir = installerkit.WorkDir(deployDir, AppName+"-"+artifact.ServiceName, version, releaseTime)
		artifactRemote = workDir + "/" + installerkit.Sanitize(artifact.FileName)
		scriptRemote = workDir + "/update-aifar-artifact.sh"
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
			ServiceOrder:     serviceOrderText(),
			ServiceName:      artifact.ServiceName,
			ArtifactRemote:   artifactRemote,
			ArtifactFileName: artifact.FileName,
			ArtifactSHA256:   artifact.SHA256,
			ArtifactSize:     artifact.Size,
			Version:          version,
			ReleaseID:        releaseID,
			CreatedAt:        releaseTime.Format(time.RFC3339),
			ConfigHash:       configHash,
			ReleaseKeepCount: releaseKeepCount,
			ComposeProject:   composeProject,
			IngressNetwork:   ingressNetwork,
			InternalNetwork:  internalNetwork,
			IngressContainer: ingressContainer,
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
		metadata["releaseId"] = releaseID
		metadata["releasePath"] = installRoot + "/" + releasesDirName + "/" + releaseID
		metadata["releaseVersion"] = version
		metadata["releaseCreatedAt"] = releaseTime.Format(time.RFC3339)
		metadata["configHash"] = configHash
		orchestration := partialOrchestrationMetadata(metadata, installRoot, releaseID, ingressNetwork, gatewayPort, webPort, []string{artifact.ServiceName})
		for key, value := range orchestration {
			metadata[key] = value
		}
		metadata["lastPartialUpdate"] = map[string]any{
			"service":        artifact.ServiceName,
			"artifactFile":   artifact.FileName,
			"artifactSHA256": artifact.SHA256,
			"artifactSize":   artifact.Size,
			"baseReleaseId":  baseReleaseID,
			"releaseId":      releaseID,
			"updatedAt":      releaseTime.Format(time.RFC3339),
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
		if releases, ok := s.store.(releaseStore); ok {
			manifest, _ := json.Marshal(partialReleaseManifest(version, releaseID, releaseTime, configHash, baseReleaseID, ingressNetwork, gatewayPort, webPort, artifact, orchestration))
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
	if !artifactTypeAllowed(serviceName, fileName) {
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
	for _, service := range serviceOrder {
		if service == serviceName {
			return true
		}
	}
	return false
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
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return "", err
	}
	for _, candidate := range instances {
		if candidate.App != AppName || candidate.ServerID != serverID {
			continue
		}
		metadata := metadataFromInstance(candidate)
		if stringFromMetadata(metadata, "installRoot", installRoot) == installRoot {
			return candidate.ID, nil
		}
	}
	return "", nil
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
	if status := strings.TrimSpace(fmt.Sprint(lastCheck["status"])); status != "" && !dockerStatusReady(status) {
		return false
	}
	return strings.TrimSpace(fmt.Sprint(lastCheck["dockerVersion"])) != "" &&
		strings.TrimSpace(fmt.Sprint(lastCheck["composeVersion"])) != ""
}

func dockerStatusReady(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "installed", "running", "available", "ok", "success":
		return true
	default:
		return false
	}
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
	metadata := metadataFromInstance(req.Instance)
	networkName := stringFromMetadata(metadata, "networkName", defaultNetworkName)
	installRoot := stringFromMetadata(metadata, "installRoot", installRootFromDeployDir(req.Server.DeployDir))

	if err := step(1, func() error {
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

	if err := step(2, func() error {
		status, err := NewInspector(s.remote).Check(ctx, req.Server, installRoot, logForServer)
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

	if err := step(3, func() error {
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
	if err := step(1, func() error {
		var checkErr error
		status, checkErr = NewInspector(s.remote).Check(ctx, req.Server, installRoot, logForServer)
		return checkErr
	}); err != nil {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		_ = s.markInstanceStatus(req.Instance, "error", map[string]any{"message": msg})
		return CheckResult{Status: "error", Message: msg}, err
	}
	details := map[string]any{
		"message":             status.Message,
		"installRoot":         status.InstallRoot,
		"currentRelease":      status.CurrentRelease,
		"releaseId":           status.ReleaseID,
		"installRootExists":   status.InstallRootExists,
		"totalContainers":     status.TotalContainers,
		"runningContainers":   status.RunningContainers,
		"unhealthyContainers": status.UnhealthyContainers,
		"staleContainers":     status.StaleContainers,
		"ingressRunning":      status.IngressRunning,
		"containers":          status.Containers,
	}
	if err := step(2, func() error {
		return s.markInstanceStatus(req.Instance, status.Status, details)
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

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	metadata := metadataFromInstance(instance)
	metadata["lastCheck"] = details
	next, _ := json.Marshal(metadata)
	instance.Status = status
	instance.Metadata = string(next)
	_, err := s.store.SaveAppInstance(instance)
	return err
}

type installScriptData struct {
	InstallRoot      string
	WorkDir          string
	ArchiveRemote    string
	ServiceOrder     string
	Version          string
	ReleaseID        string
	CreatedAt        string
	ConfigHash       string
	ReleaseKeepCount int
	ComposeProject   string
	IngressNetwork   string
	InternalNetwork  string
	IngressContainer string
	Options          InstallOptions
}

type uninstallScriptData struct {
	InstallRoot  string
	NetworkName  string
	ServiceOrder string
}

type updateScriptData struct {
	InstallRoot      string
	WorkDir          string
	ServiceOrder     string
	ServiceName      string
	ArtifactRemote   string
	ArtifactFileName string
	ArtifactSHA256   string
	ArtifactSize     int64
	Version          string
	ReleaseID        string
	CreatedAt        string
	ConfigHash       string
	ReleaseKeepCount int
	ComposeProject   string
	IngressNetwork   string
	InternalNetwork  string
	IngressContainer string
}

type bundleUpdateScriptArtifact struct {
	ServiceName    string
	ArtifactRemote string
	ArtifactFile   string
	ArtifactSHA256 string
	ArtifactSize   int64
}

type bundleUpdateScriptData struct {
	InstallRoot      string
	WorkDir          string
	ServiceOrder     string
	ChangedServices  string
	Artifacts        []bundleUpdateScriptArtifact
	Version          string
	ReleaseID        string
	CreatedAt        string
	ConfigHash       string
	ReleaseKeepCount int
	ComposeProject   string
	IngressNetwork   string
	InternalNetwork  string
	IngressContainer string
	Concurrency      int
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
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return fallback
}

func installSteps(copy Copy) []installStepDef {
	return []installStepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "upload-bundle", Title: copy.UploadBundleStep},
		{Name: "deploy-compose", Title: copy.DeployCompose},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func deleteSteps(copy DeleteCopy) []installStepDef {
	return []installStepDef{
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
