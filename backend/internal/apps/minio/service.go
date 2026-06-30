package minio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/apps/deleteflow"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskrun"
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
	Concurrency     int
}

type DeleteRequest struct {
	Instance   store.AppInstance
	Server     store.Server
	Language   string
	Parameters map[string]any
}

type Service struct {
	store  Store
	remote Remote
}

type stepDef struct {
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
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := VerifyBundle(bundle); err != nil {
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
		return VerifyBundle(bundle)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	installer := NewInstaller(s.remote)
	installRoot := remoteInstallRoot(server, "minio", bundle.Version)
	var dataDirs []string
	if err := step(3, "select-data-disk", copy.SelectDataDisk, func() error {
		var dataErr error
		dataDirs, dataErr = installer.ResolveDataDirs(ctx, server, minioDataDirRequest(req.Parameters, installRoot, options.APIPort, server.ID), logForServer)
		options.DataDirs = dataDirs
		options.DataDir = firstString(dataDirs)
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
			"serviceName":   "aifar-minio",
			"storageMode":   minioStorageMode(req.Parameters),
			"dataRoot":      minioDataRoot(req.Parameters),
			"diskDevice":    minioDiskDeviceForServer(req.Parameters, server.ID),
			"diskDevices":   minioDiskDevicesForServer(req.Parameters, server.ID),
			"dataDir":       options.DataDir,
			"dataDirs":      dataDirs,
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

func (s Service) installDistributed(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	targets := targetServerIDs(req)
	if len(targets) < 4 {
		return errors.New(copy.DistributedNeedNodes)
	}
	options := minioOptions(req.Parameters, req.DefaultPassword)
	if err := options.Validate(); err != nil {
		return err
	}
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := VerifyBundle(bundle); err != nil {
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
	installer := NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := minioInstallStepsFor("distributed", copy)
	targetIndexes := make(map[string]int, len(targets))
	for idx, target := range targets {
		targetIndexes[target] = idx
	}
	dataDirs := make([][]string, len(targets))
	failures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(target string) error {
		logForServer := logForTarget(log, targetLog, target)
		if recorder != nil {
			recorder.StartTarget(target)
		}
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		server := preloadedServers[target]
		if err := step(1, "load-server", copy.LoadServer, func() error {
			return nil
		}); err != nil {
			msg := fmt.Sprintf(copy.LoadFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(2, "verify-resource", copy.VerifyResource, func() error {
			return VerifyBundle(bundle)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		installRoot := remoteInstallRoot(server, "minio", bundle.Version)
		if err := step(3, "select-data-disk", copy.SelectDataDisk, func() error {
			nodeDataDirs, dataErr := installer.ResolveDataDirs(ctx, server, minioDataDirRequest(req.Parameters, installRoot, options.APIPort, server.ID), logForServer)
			dataDirs[targetIndexes[target]] = nodeDataDirs
			return dataErr
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		nodeOptions := options
		nodeOptions.DataDirs = dataDirs[targetIndexes[target]]
		nodeOptions.DataDir = firstString(nodeOptions.DataDirs)
		if err := step(4, "install-minio", copy.InstallStandalone, func() error {
			return installer.Install(ctx, server, bundle, nodeOptions, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		return nil
	})
	if len(failures) > 0 {
		msg := fmt.Sprintf(copy.InstallFailed, strings.Join(taskrun.FailureMessages(failures), "; "))
		failedTargets := taskrun.FailureTargets(failures)
		for _, target := range targets {
			if !failedTargets[target] {
				logForTarget(log, targetLog, target).Error("%s", msg)
				finishTarget(recorder, target, "failed", msg)
			}
		}
		return errors.New(msg)
	}

	volumes := make([]DistributedVolume, 0, len(targets))
	for idx, target := range targets {
		server := preloadedServers[target]
		for _, dataDir := range dataDirs[idx] {
			volumes = append(volumes, DistributedVolume{
				Host: server.Host,
				Port: options.APIPort,
				Path: dataDir,
			})
		}
	}
	recordFailures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(target string) error {
		logForServer := logForTarget(log, targetLog, target)
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		server := preloadedServers[target]
		installRoot := remoteInstallRoot(server, "minio", bundle.Version)
		if err := step(5, "configure-distributed", copy.ConfigureDistributed, func() error {
			return installer.ConfigureDistributedNode(ctx, server, DistributedNodeConfig{
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
			return errors.New(msg)
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
				"serviceName":    "aifar-minio",
				"storageMode":    minioStorageMode(req.Parameters),
				"dataRoot":       minioDataRoot(req.Parameters),
				"diskDevice":     minioDiskDeviceForServer(req.Parameters, server.ID),
				"diskDevices":    minioDiskDevicesForServer(req.Parameters, server.ID),
				"dataDir":        firstString(dataDirs[targetIndexes[target]]),
				"dataDirs":       dataDirs[targetIndexes[target]],
				"endpoint":       fmt.Sprintf("http://%s:%d", server.Host, options.APIPort),
				"topology":       "distributed",
				"distributedSet": len(targets),
				"volumeCount":    len(volumes),
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
			return errors.New(msg)
		}
		logForServer.Info(copy.Installed, instance.ID)
		finishTarget(recorder, target, "success", "")
		return nil
	})
	if len(recordFailures) > 0 {
		return fmt.Errorf(copy.InstallFailed, strings.Join(taskrun.FailureMessages(recordFailures), "; "))
	}
	log.Info(copy.DistributedInstalled, len(targets))
	return nil
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log Logger, targetLog targetLogger) error {
	copy := DeleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	apiPort := instanceAPIPort(req.Instance)
	uninstallOptions := minioUninstallOptions(req)
	uninstaller := NewUninstaller(s.remote)
	return deleteflow.Run(deleteflow.Request{
		Target:     target,
		ServerName: req.Server.Name,
		InstanceID: req.Instance.ID,
		Log:        logForServer,
		Recorder:   recorder,
		Steps: []deleteflow.Step{
			{Name: "remove-remote", Title: copy.RemoveRemote, Run: func() error {
				return uninstaller.Uninstall(ctx, req.Server, req.Instance.Version, apiPort, uninstallOptions, logForServer)
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

func normalizeTopology(topology string) string {
	topology = strings.ToLower(strings.TrimSpace(topology))
	if topology == "" || topology == "single" {
		return "standalone"
	}
	return topology
}

func remoteInstallRoot(server store.Server, app, version string) string {
	return installerkit.InstallRoot(server.DeployDir, app)
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

func newStepRunner(log Logger, recorder stepRecorder, target string, copy Copy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := minioInstallSteps(copy)
	return newStepRunnerWithSteps(log, recorder, target, copy, steps)
}

func newStepRunnerWithSteps(log Logger, recorder stepRecorder, target string, copy Copy, steps []stepDef) func(stepIndex int, stepName, label string, fn func() error) error {
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

func logForTarget(fallback Logger, targetLog targetLogger, target string) Logger {
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

func firstString(values []string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}
