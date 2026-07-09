package nacos

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/deleteflow"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installflow"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskrun"
)

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	ListAppInstances() ([]store.AppInstance, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	DeleteAppInstance(id string) error
}

type InstallRequest struct {
	Version     string
	Topology    string
	Language    string
	ServerID    string
	ServerIDs   []string
	Parameters  map[string]any
	Concurrency int
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

type CheckResult struct {
	Status  string
	Message string
	Details map[string]any
}

type Service struct {
	store  Store
	remote Remote
}

type stepDef = installflow.Step

type targetLogger func(target string) Logger

type stepRecorder = installflow.Recorder

func NewService(s Store, remote Remote) Service {
	return Service{store: s, remote: remote}
}

func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	topology := normalizeTopology(req.Topology)
	targets := targetServerIDs(req)
	if topology == "cluster" {
		targets = clusterServerIDs(req.Parameters, targets)
	}
	if len(targets) == 0 {
		return errors.New(copy.TargetRequired)
	}
	if topology == "standalone" && len(targets) != 1 {
		return errors.New(copy.SingleTargetOnly)
	}
	if topology == "cluster" && len(targets) != 3 {
		return errors.New(copy.ClusterNeedNodes)
	}
	if topology != "standalone" && topology != "cluster" {
		return fmt.Errorf(copy.TopologyUnsupported, topology)
	}
	options := nacosOptions(req.Parameters, topology)
	resolvedOptions, err := s.resolveInstallOptions(options)
	if err != nil {
		return err
	}
	options = resolvedOptions
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
	log.Info(copy.UsingJDK, bundle.JDKX64Path)

	preloadedServers := make(map[string]store.Server, len(targets))
	clusterNodes := make([]NacosClusterNode, 0, len(targets))
	for _, target := range targets {
		server, loadErr := s.store.GetServer(target, true)
		if loadErr != nil {
			return loadErr
		}
		preloadedServers[target] = server
		clusterNodes = append(clusterNodes, NacosClusterNode{
			ID:   server.ID,
			Name: server.Name,
			Host: server.Host,
			Port: options.Port,
		})
	}

	clusterID := ""
	if topology == "cluster" {
		clusterID = store.NewID("nacos_cluster")
	}
	installer := NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := nacosInstallSteps(copy)
	failures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(target string) error {
		logForServer := logForTarget(log, targetLog, target)
		installflow.StartTarget(recorder, target)
		step := newStepRunner(logForServer, recorder, target, copy, steps)
		server := preloadedServers[target]
		if err := step(1, "load-server", copy.LoadServer, func() error { return nil }); err != nil {
			msg := fmt.Sprintf(copy.LoadFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(2, "verify-resource", copy.VerifyResource, func() error { return VerifyBundle(bundle) }); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(3, "install-nacos", copy.InstallNacos, func() error {
			return installer.Install(ctx, server, bundle, options, clusterNodes, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		var instance store.AppInstance
		if err := step(4, "record-instance", copy.RecordInstance, func() error {
			metadataMap := map[string]any{
				"resourcePath":   bundle.ArchivePath,
				"jdkX64Path":     bundle.JDKX64Path,
				"jdkAarch64Path": bundle.JDKAarch64Path,
				"port":           options.Port,
				"grpcPort":       options.GRPCPort,
				"grpcRaftPort":   options.GRPCRaftPort,
				"raftPort":       options.RaftPort,
				"serviceName":    "aifar-nacos",
				"endpoint":       fmt.Sprintf("http://%s:%d/nacos", server.Host, options.Port),
				"authEnabled":    true,
				"nacosUser":      options.AdminUser,
				"topology":       topology,
				"mode":           topology,
				"dbSource":       options.Database.Source,
				"dbEnabled":      options.Database.Enabled,
			}
			if topology == "cluster" {
				metadataMap["clusterId"] = clusterID
				metadataMap["clusterNodes"] = clusterNodes
			}
			if options.Database.Enabled {
				metadataMap["dbHost"] = options.Database.Host
				metadataMap["dbPort"] = options.Database.Port
				metadataMap["dbName"] = options.Database.Name
				metadataMap["dbUser"] = options.Database.User
				metadataMap["dbInstanceId"] = options.Database.InstanceID
			}
			metadata, _ := json.Marshal(metadataMap)
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
				App:      "nacos",
				Version:  bundle.Version,
				ServerID: server.ID,
				Status:   "installed",
				Topology: topology,
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
	if len(failures) > 0 {
		return fmt.Errorf(copy.BatchFailed, len(failures), strings.Join(taskrun.FailureMessages(failures), "; "))
	}
	if topology == "cluster" {
		log.Info(copy.ClusterInstalled, len(targets))
	}
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
	uninstaller := NewInstaller(s.remote)
	return deleteflow.Run(deleteflow.Request{
		Target:     target,
		ServerName: req.Server.Name,
		InstanceID: req.Instance.ID,
		Log:        logForServer,
		Recorder:   recorder,
		Steps: []deleteflow.Step{
			{Name: "remove-remote", Title: copy.RemoveRemote, Run: func() error {
				return uninstaller.Uninstall(ctx, req.Server, req.Instance.Version, instancePort(req.Instance), logForServer)
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

func (s Service) Check(ctx context.Context, req CheckRequest, log Logger, targetLog targetLogger) (CheckResult, error) {
	copy := CopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	installflow.StartTarget(recorder, target)
	steps := nacosCheckSteps(copy)
	step := newStepRunner(logForServer, recorder, target, copy, steps)
	details := map[string]any{
		"checkedAt": time.Now().UTC().Format(time.RFC3339),
		"topology":  instanceTopology(req.Instance),
	}
	fail := func(err error) (CheckResult, error) {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		details["error"] = err.Error()
		_ = s.markInstanceStatus(req.Instance, "unavailable", details)
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "unavailable", Message: msg, Details: details}, err
	}
	if err := step(1, "check-runtime", copy.CheckRuntime, func() error {
		return NewInstaller(s.remote).Check(ctx, req.Server, instancePort(req.Instance), logForServer)
	}); err != nil {
		return fail(err)
	}
	if err := step(2, "update-instance", copy.UpdateInstance, func() error {
		return s.markInstanceStatus(req.Instance, "running", details)
	}); err != nil {
		return fail(err)
	}
	msg := fmt.Sprintf(copy.Checked, "running")
	logForServer.Info("%s", msg)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: "running", Message: msg, Details: details}, nil
}

func (s Service) markInstanceStatus(instance store.AppInstance, status string, details map[string]any) error {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if status == "running" {
		delete(metadata, "installFailed")
		delete(metadata, "failedAt")
		delete(metadata, "taskId")
		delete(metadata, "error")
	}
	metadata["lastCheck"] = details
	metadataJSON, _ := json.Marshal(metadata)
	instance.Status = status
	instance.Metadata = string(metadataJSON)
	_, err := s.store.SaveAppInstance(instance)
	return err
}

func instancePort(instance store.AppInstance) int {
	var metadata struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return normalizePort(metadata.Port, 8848)
}

func instanceTopology(instance store.AppInstance) string {
	if strings.TrimSpace(instance.Topology) != "" {
		return normalizeTopology(instance.Topology)
	}
	var metadata struct {
		Topology string `json:"topology"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return normalizeTopology(metadata.Topology)
}

func remoteInstallRoot(server store.Server, app, version string) string {
	return installerkit.InstallRoot(server.DeployDir, app)
}

func nacosInstallSteps(copy Copy) []stepDef {
	return []stepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "install-nacos", Title: copy.InstallNacos},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func nacosCheckSteps(copy Copy) []stepDef {
	return []stepDef{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}

func nacosDeleteSteps(copy DeleteCopy) []stepDef {
	return []stepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func newStepRunner(log Logger, recorder stepRecorder, target string, copy Copy, steps []stepDef) func(stepIndex int, stepName, label string, fn func() error) error {
	runner := installflow.Runner{
		Log:      log,
		Recorder: recorder,
		Target:   target,
		Steps:    steps,
		Messages: installflow.Messages{
			StepStart:  copy.StepStart,
			StepDone:   copy.StepDone,
			StepFailed: copy.StepFailed,
		},
	}
	return func(stepIndex int, stepName, label string, fn func() error) error {
		return runner.Run(stepIndex, stepName, label, fn)
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
	installflow.FinishTarget(recorder, target, status, errText)
}
