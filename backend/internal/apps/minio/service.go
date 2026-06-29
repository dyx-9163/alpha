package minio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/apps/deleteflow"
	minioinstaller "aifar-deployment/backend/internal/installer/minio"
	"aifar-deployment/backend/internal/store"
)

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	DeleteAppInstance(id string) error
}

type InstallRequest struct {
	Version         string
	Topology        string
	Language        string
	ServerID        string
	ServerIDs       []string
	DefaultPassword string
	Parameters      map[string]any
}

type DeleteRequest struct {
	Instance store.AppInstance
	Server   store.Server
	Language string
}

type Service struct {
	store  Store
	remote minioinstaller.Remote
}

type stepDef struct {
	Name  string
	Title string
}

type targetLogger func(target string) minioinstaller.Logger

type stepRecorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

func NewService(s Store, remote minioinstaller.Remote) Service {
	return Service{store: s, remote: remote}
}

func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log minioinstaller.Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	topology := normalizeTopology(req.Topology)
	switch topology {
	case "distributed":
		return s.installDistributed(ctx, req, resources, log, targetLog)
	case "standalone":
	default:
		return fmt.Errorf(copy.DistributedUnsupported, topology)
	}
	targets := targetServerIDs(req)
	if len(targets) != 1 {
		return errors.New(copy.SingleTargetOnly)
	}
	options := minioOptions(req.Parameters, req.DefaultPassword)
	if err := options.Validate(); err != nil {
		return err
	}
	bundle, err := minioinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := minioinstaller.VerifyBundle(bundle); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
	log.Info(copy.UsingGoToolchain, bundle.GoArchivePath)
	log.Info(copy.UsingGoModCache, bundle.GoModCachePath)
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))
	target := targets[0]
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	if recorder != nil {
		recorder.StartTarget(target)
	}
	step := newStepRunner(logForServer, recorder, target, copy)
	var server store.Server
	if err := step(1, "load-server", copy.LoadServer, func() error {
		var loadErr error
		server, loadErr = s.store.GetServer(target, true)
		return loadErr
	}); err != nil {
		msg := fmt.Sprintf(copy.LoadFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	if err := step(2, "verify-resource", copy.VerifyResource, func() error {
		return minioinstaller.VerifyBundle(bundle)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	installer := minioinstaller.NewInstaller(s.remote)
	installRoot := remoteInstallRoot(server, "minio", bundle.Version)
	if err := step(3, "select-data-disk", copy.SelectDataDisk, func() error {
		dataDir, dataErr := installer.ResolveDataDir(ctx, server, minioDataRoot(req.Parameters), installRoot, options.APIPort, logForServer)
		options.DataDir = dataDir
		return dataErr
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	if err := step(4, "install-minio", copy.InstallStandalone, func() error {
		return installer.Install(ctx, server, bundle, options, logForServer)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	var instance store.AppInstance
	if err := step(5, "record-instance", copy.RecordInstance, func() error {
		metadata, _ := json.Marshal(map[string]any{
			"resourcePath":  bundle.ArchivePath,
			"mcPath":        bundle.MCPath,
			"goArchivePath": bundle.GoArchivePath,
			"rpmCount":      len(bundle.RPMPaths),
			"apiPort":       options.APIPort,
			"consolePort":   options.ConsolePort,
			"rootUser":      options.RootUser,
			"serviceName":   fmt.Sprintf("aifar-minio-%d", options.APIPort),
			"dataDir":       options.DataDir,
			"endpoint":      fmt.Sprintf("http://%s:%d", server.Host, options.APIPort),
			"topology":      "standalone",
			"auth":          "password",
		})
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
			App:      "minio",
			Version:  bundle.Version,
			ServerID: server.ID,
			Status:   "installed",
			Topology: "standalone",
			Metadata: string(metadata),
		})
		return saveErr
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

func (s Service) installDistributed(ctx context.Context, req InstallRequest, resources []store.Resource, log minioinstaller.Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	targets := targetServerIDs(req)
	if len(targets) < 4 {
		return errors.New(copy.DistributedNeedNodes)
	}
	options := minioOptions(req.Parameters, req.DefaultPassword)
	if err := options.Validate(); err != nil {
		return err
	}
	bundle, err := minioinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := minioinstaller.VerifyBundle(bundle); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
	log.Info(copy.UsingGoToolchain, bundle.GoArchivePath)
	log.Info(copy.UsingGoModCache, bundle.GoModCachePath)
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))

	clusterID := store.NewID("minio_dist")
	preloadedServers := make(map[string]store.Server, len(targets))
	for _, target := range targets {
		server, loadErr := s.store.GetServer(target, true)
		if loadErr != nil {
			return loadErr
		}
		preloadedServers[target] = server
	}
	installer := minioinstaller.NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := minioInstallStepsFor("distributed", copy)
	dataDirs := make(map[string]string, len(targets))
	for _, target := range targets {
		logForServer := logForTarget(log, targetLog, target)
		if recorder != nil {
			recorder.StartTarget(target)
		}
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		var server store.Server
		if err := step(1, "load-server", copy.LoadServer, func() error {
			server = preloadedServers[target]
			return nil
		}); err != nil {
			msg := fmt.Sprintf(copy.LoadFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		if err := step(2, "verify-resource", copy.VerifyResource, func() error {
			return minioinstaller.VerifyBundle(bundle)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		installRoot := remoteInstallRoot(server, "minio", bundle.Version)
		if err := step(3, "select-data-disk", copy.SelectDataDisk, func() error {
			dataDir, dataErr := installer.ResolveDataDir(ctx, server, minioDataRoot(req.Parameters), installRoot, options.APIPort, logForServer)
			dataDirs[target] = dataDir
			return dataErr
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		nodeOptions := options
		nodeOptions.DataDir = dataDirs[target]
		if err := step(4, "install-minio", copy.InstallStandalone, func() error {
			return installer.Install(ctx, server, bundle, nodeOptions, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
	}

	volumes := make([]minioinstaller.DistributedVolume, 0, len(targets))
	for _, target := range targets {
		server := preloadedServers[target]
		volumes = append(volumes, minioinstaller.DistributedVolume{
			Host: server.Host,
			Port: options.APIPort,
			Path: dataDirs[target],
		})
	}
	for _, target := range targets {
		logForServer := logForTarget(log, targetLog, target)
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		server := preloadedServers[target]
		installRoot := remoteInstallRoot(server, "minio", bundle.Version)
		if err := step(5, "configure-distributed", copy.ConfigureDistributed, func() error {
			return installer.ConfigureDistributedNode(ctx, server, minioinstaller.DistributedNodeConfig{
				Version:      bundle.Version,
				InstallRoot:  installRoot,
				APIPort:      options.APIPort,
				ConsolePort:  options.ConsolePort,
				RootUser:     options.RootUser,
				RootPassword: options.RootPassword,
				Volumes:      volumes,
			}, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		var instance store.AppInstance
		if err := step(6, "record-instance", copy.RecordInstance, func() error {
			metadata, _ := json.Marshal(map[string]any{
				"clusterId":      clusterID,
				"resourcePath":   bundle.ArchivePath,
				"mcPath":         bundle.MCPath,
				"goArchivePath":  bundle.GoArchivePath,
				"rpmCount":       len(bundle.RPMPaths),
				"apiPort":        options.APIPort,
				"consolePort":    options.ConsolePort,
				"rootUser":       options.RootUser,
				"serviceName":    fmt.Sprintf("aifar-minio-%d", options.APIPort),
				"dataDir":        dataDirs[target],
				"endpoint":       fmt.Sprintf("http://%s:%d", server.Host, options.APIPort),
				"topology":       "distributed",
				"distributedSet": len(targets),
				"auth":           "password",
			})
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
				App:      "minio",
				Version:  bundle.Version,
				ServerID: server.ID,
				Status:   "installed",
				Topology: "distributed",
				Metadata: string(metadata),
			})
			return saveErr
		}); err != nil {
			msg := fmt.Sprintf(copy.RecordFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		logForServer.Info(copy.Installed, instance.ID)
		finishTarget(recorder, target, "success", "")
	}
	log.Info(copy.DistributedInstalled, len(targets))
	return nil
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log minioinstaller.Logger, targetLog targetLogger) error {
	copy := DeleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	apiPort := instanceAPIPort(req.Instance)
	uninstaller := minioinstaller.NewUninstaller(s.remote)
	return deleteflow.Run(deleteflow.Request{
		Target:     target,
		ServerName: req.Server.Name,
		InstanceID: req.Instance.ID,
		Log:        logForServer,
		Recorder:   recorder,
		Steps: []deleteflow.Step{
			{Name: "remove-remote", Title: copy.RemoveRemote, Run: func() error {
				return uninstaller.Uninstall(ctx, req.Server, req.Instance.Version, apiPort, logForServer)
			}},
			{Name: "delete-instance", Title: copy.DeleteInstance, Run: func() error {
				return s.store.DeleteAppInstance(req.Instance.ID)
			}},
		},
		Messages: deleteflow.Messages{
			StepStart:    copy.StepStart,
			StepDone:     copy.StepDone,
			StepFailed:   copy.StepFailed,
			DeleteFailed: copy.DeleteFailed,
			Deleted:      copy.Deleted,
		},
	})
}

func minioOptions(params map[string]any, defaultPassword string) minioinstaller.InstallOptions {
	return minioinstaller.InstallOptions{
		APIPort:      intParam(params, "apiPort", 9000),
		ConsolePort:  intParam(params, "consolePort", 9001),
		RootUser:     stringParam(params, "rootUser", "admin"),
		RootPassword: passwordParam(params, defaultPassword),
	}
}

func minioDataRoot(params map[string]any) string {
	for _, key := range []string{"dataRoot", "dataDiskRoot", "dataDir"} {
		if value, ok := params[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	return "/data/minio"
}

func validateMinioDataRoot(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, "/") {
		return errors.New("MinIO data disk root must be an absolute path")
	}
	if strings.IndexFunc(value, func(r rune) bool { return r <= ' ' }) >= 0 {
		return errors.New("MinIO data disk root must not contain whitespace")
	}
	return nil
}

func passwordParam(params map[string]any, fallback string) string {
	for _, key := range []string{"rootPassword", "password", "minioPassword"} {
		if value, ok := params[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" {
				return text
			}
		}
	}
	fallback = strings.TrimSpace(fallback)
	if fallback == "" {
		return "Oversea.123"
	}
	return fallback
}

func stringParam(params map[string]any, key, fallback string) string {
	if value, ok := params[key]; ok {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" {
			return text
		}
	}
	return fallback
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return normalizePort(v, fallback)
	case int64:
		return normalizePort(int(v), fallback)
	case float64:
		return normalizePort(int(v), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePort(n, fallback)
	default:
		return fallback
	}
}

func normalizePort(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}

func targetServerIDs(req InstallRequest) []string {
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			return
		}
		seen[id] = true
		out = append(out, id)
	}
	add(req.ServerID)
	for _, id := range req.ServerIDs {
		add(id)
	}
	return out
}

func instanceAPIPort(instance store.AppInstance) int {
	var metadata struct {
		APIPort int `json:"apiPort"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return normalizePort(metadata.APIPort, 9000)
}

func normalizeTopology(topology string) string {
	topology = strings.ToLower(strings.TrimSpace(topology))
	if topology == "" || topology == "single" {
		return "standalone"
	}
	return topology
}

func remoteInstallRoot(server store.Server, app, version string) string {
	deployDir := strings.TrimSpace(server.DeployDir)
	if deployDir == "" {
		deployDir = "/aifar/apps"
	}
	deployDir = "/" + strings.Trim(deployDir, "/")
	return deployDir + "/" + app + "/" + version
}

func minioInstallSteps(copy Copy) []stepDef {
	return minioInstallStepsFor("standalone", copy)
}

func minioInstallStepsFor(topology string, copy Copy) []stepDef {
	if normalizeTopology(topology) == "distributed" {
		return []stepDef{
			{Name: "load-server", Title: copy.LoadServer},
			{Name: "verify-resource", Title: copy.VerifyResource},
			{Name: "select-data-disk", Title: copy.SelectDataDisk},
			{Name: "install-minio", Title: copy.InstallStandalone},
			{Name: "configure-distributed", Title: copy.ConfigureDistributed},
			{Name: "record-instance", Title: copy.RecordInstance},
		}
	}
	return []stepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "select-data-disk", Title: copy.SelectDataDisk},
		{Name: "install-minio", Title: copy.InstallStandalone},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func minioDeleteSteps(copy DeleteCopy) []stepDef {
	return []stepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func newStepRunner(log minioinstaller.Logger, recorder stepRecorder, target string, copy Copy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := minioInstallSteps(copy)
	return newStepRunnerWithSteps(log, recorder, target, copy, steps)
}

func newStepRunnerWithSteps(log minioinstaller.Logger, recorder stepRecorder, target string, copy Copy, steps []stepDef) func(stepIndex int, stepName, label string, fn func() error) error {
	return func(stepIndex int, stepName, label string, fn func() error) error {
		if recorder != nil {
			recorder.StartStep(target, stepName, label, stepIndex)
		}
		log.Info(copy.StepStart, stepIndex, len(steps), label)
		if err := fn(); err != nil {
			log.Error(copy.StepFailed, stepIndex, len(steps), label, err)
			if recorder != nil {
				recorder.FinishStep(target, stepName, "failed", err.Error())
			}
			return err
		}
		log.Info(copy.StepDone, stepIndex, len(steps), label)
		if recorder != nil {
			recorder.FinishStep(target, stepName, "success", "")
		}
		return nil
	}
}

func logForTarget(fallback minioinstaller.Logger, targetLog targetLogger, target string) minioinstaller.Logger {
	if targetLog == nil {
		return fallback
	}
	if log := targetLog(target); log != nil {
		return log
	}
	return fallback
}

func finishTarget(recorder stepRecorder, target, status, errText string) {
	if recorder == nil {
		return
	}
	recorder.FinishTarget(target, status, errText)
}
