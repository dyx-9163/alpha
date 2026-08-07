package aifar

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installer/selinux"
	"aifar-deployment/backend/internal/installer/uploadkit"
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

	if err := step(2, func() error {
		var err error
		script, err = renderServiceInstallScript(serviceInstallScriptData{
			InstallRoot:         installRoot,
			ServiceOrder:        strings.Join(allServices, " "),
			NewServices:         strings.Join(missing, " "),
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
			moduleArchiveLocal, err = CreateServiceModuleArchive(bundle, missing)
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
			if _, err := installerkit.Run(ctx, s.remote, req.Server, "mkdir -p "+installerkit.ShellQuote(workDir)+" "+installerkit.ShellQuote(installRoot+"/runtime/services"), logForServer, "prepare AIFAR service module upload failed"); err != nil {
				return err
			}
			if err := uploadkit.Upload(ctx, s.remote, req.Server, uploadkit.File{LocalPath: moduleArchiveLocal, RemotePath: moduleArchiveRemote, LogMessage: "uploading AIFAR service modules", FailureMessage: "upload AIFAR service modules failed"}, logForServer); err != nil {
				return err
			}
			if _, err := installerkit.Run(ctx, s.remote, req.Server, "tar -xzf "+installerkit.ShellQuote(moduleArchiveRemote)+" -C "+installerkit.ShellQuote(installRoot+"/runtime/services"), logForServer, "extract AIFAR service modules failed"); err != nil {
				return err
			}
		}
		_, runErr := installerkit.Run(ctx, s.remote, req.Server, "sh -s <<'AIFAR_SERVICE_INSTALL'\n"+script+"\nAIFAR_SERVICE_INSTALL", logForServer, "AIFAR service installation failed")
		return runErr
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	if err := step(4, func() error {
		nextMetadata := metadataFromInstance(current)
		nextMetadata["releaseId"] = releaseID
		nextMetadata["currentRevision"] = releaseID
		nextMetadata["releaseVersion"] = version
		nextMetadata["releaseCreatedAt"] = releaseTime.Format(time.RFC3339)
		nextMetadata["configHash"] = configHash
		nextMetadata["serviceCatalog"] = serviceCatalogMetadataForInstall(serviceDefinitions, gatewayPort, webPort)
		nextMetadata = serviceInstallOrchestrationMetadata(nextMetadata, installRoot, releaseID, ingressNetwork, gatewayPort, webPort, missing)
		nextMetadata["lastServiceInstall"] = map[string]any{
			"services":    missing,
			"releaseId":   releaseID,
			"installedAt": releaseTime.Format(time.RFC3339),
			"reason":      strings.TrimSpace(req.Reason),
		}
		delete(nextMetadata, "orchestrationLock")
		next := current
		next.Status = "installed"
		next.Version = version
		if err := saveMetadata(s.store, next, nextMetadata); err != nil {
			return err
		}
		if orch, ok := s.store.(aifarOrchestrationStore); ok {
			if err := saveControlPlaneRevision(orch, current.ID, version, releaseID, configHash, desiredReplicasFromMetadata(nextMetadata), gatewayPort, webPort, missing, releaseTime, servicePorts(serviceDefinitions)); err != nil {
				return err
			}
		}
		if releases, ok := s.store.(releaseStore); ok {
			manifest, _ := json.Marshal(serviceInstallReleaseManifest(version, releaseID, releaseTime, configHash, strings.TrimSpace(req.Reason), missing, nextMetadata))
			if _, err := releases.SaveAppRelease(store.AppRelease{
				InstanceID:   current.ID,
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
			if _, err := releases.DeleteOldAppReleases(current.ID, releaseKeepCount); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		finishTarget(recorder, target, "failed", err.Error())
		return err
	}

	logForServer.Info("AIFAR services installed: %s", strings.Join(missing, ", "))
	finishTarget(recorder, target, "success", "")
	return nil
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

func serviceInstallOrchestrationMetadata(current map[string]any, installRoot, revision, ingressNetwork string, gatewayPort, webPort int, installedServices []string) map[string]any {
	next := copyMetadata(current)
	existing := servicesFromMetadata(current)
	allServices := mergeServices(existing, installedServices)
	next["services"] = allServices
	next["orchestrationModel"] = orchestrationModelK8sLikeV1
	next["releasePhase"] = releasePhaseActive
	next["runtimeService"] = "aifar-agent"
	next["ingressNetwork"] = ingressNetwork
	next["activeRoutes"] = releaseRoutes(gatewayPort, webPort)
	next["runtimeSpecPath"] = runtimeSpecPath(installRoot)
	desiredSource := desiredReplicasFromMetadata(current)
	activeEndpointSource := activeEndpointsFromMetadata(current)
	serviceRevisionSource := serviceRevisionsFromMetadata(current)
	containerSource := mapFromMetadataValue(current["containers"])
	desired := map[string]int{}
	activeEndpoints := map[string]any{}
	serviceRevisions := map[string]any{}
	containers := map[string]any{}
	for _, service := range allServices {
		replicas := desiredSource[service]
		if replicas < 0 {
			replicas = 0
		}
		desired[service] = replicas
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
		desired[service] = 1
		activeEndpoints[service] = releaseEndpointsForService(service, revision, 1, gatewayPort, webPort)
		serviceRevisions[service] = revision
		containers[service] = podContainerName(service, revision, 1)
	}
	next["desiredReplicas"] = desired
	next["activeEndpoints"] = activeEndpoints
	next["activeServices"] = activeServicesFromEndpointsForServices(desired, activeEndpoints, allServices)
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
