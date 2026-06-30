package mysqlrouter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/deleteflow"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/taskrun"
)

type Store interface {
	GetServer(id string, includeSecret bool) (store.Server, error)
	SaveAppInstance(v store.AppInstance) (store.AppInstance, error)
	ListAppInstances() ([]store.AppInstance, error)
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

type clusterInfo struct {
	ID                string
	Name              string
	Endpoint          string
	BootstrapHost     string
	BootstrapPort     int
	RootUser          string
	NodeCount         int
	CurrentPrimary    string
	PrimaryDetectedAt string
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
	if topology := normalizeRouterTopology(req.Topology); topology != "router" {
		return fmt.Errorf(copy.RouterUnsupported, topology)
	}
	targets := targetServerIDs(req)
	if len(targets) == 0 {
		return errors.New(copy.TargetRequired)
	}
	cluster, err := s.ResolveCluster(req.Parameters, copy)
	if err != nil {
		return err
	}
	options := mysqlRouterOptions(req.Parameters, req.DefaultPassword, cluster)
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
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))
	log.Info(copy.UsingCluster, cluster.Name, cluster.Endpoint)

	installer := NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := mysqlRouterInstallSteps(copy)
	failures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(target string) error {
		logForServer := logForTarget(log, targetLog, target)
		taskrun.StartTarget(recorder, target)
		step := newStepRunner(logForServer, recorder, target, steps, copy)
		var server store.Server
		if err := step(1, "load-server", copy.LoadServer, func() error {
			var loadErr error
			server, loadErr = s.store.GetServer(target, true)
			return loadErr
		}); err != nil {
			msg := fmt.Sprintf(copy.LoadFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(2, "resolve-cluster", copy.ResolveCluster, func() error {
			if cluster.ID == "" || cluster.BootstrapHost == "" {
				return errors.New(copy.ClusterNoEndpoint)
			}
			return nil
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(3, "verify-resource", copy.VerifyResource, func() error {
			return VerifyBundle(bundle)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(4, "install-router", copy.InstallRouter, func() error {
			return installer.Install(ctx, server, bundle, options, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		var instance store.AppInstance
		if err := step(5, "record-instance", copy.RecordInstance, func() error {
			metadata, _ := json.Marshal(map[string]any{
				"clusterId":              cluster.ID,
				"clusterName":            cluster.Name,
				"clusterEndpoint":        cluster.Endpoint,
				"currentPrimaryEndpoint": cluster.CurrentPrimary,
				"bootstrapEndpoint":      fmt.Sprintf("%s:%d", options.BootstrapHost, options.BootstrapPort),
				"resourcePath":           bundle.ArchivePath,
				"rpmCount":               len(bundle.RPMPaths),
				"basePort":               options.BasePort,
				"readWritePort":          options.BasePort,
				"readOnlyPort":           options.BasePort + 1,
				"xReadWritePort":         options.BasePort + 2,
				"xReadOnlyPort":          options.BasePort + 3,
				"bindAddress":            options.BindAddress,
				"serviceName":            routerServiceName(options.BasePort),
				"endpoint":               fmt.Sprintf("%s:%d", server.Host, options.BasePort),
				"topology":               "router",
				"auth":                   "password",
			})
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
				App:      "mysql-router",
				Version:  bundle.Version,
				ServerID: server.ID,
				Status:   "installed",
				Topology: "router",
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
		return fmt.Errorf(copy.InstallFailed, strings.Join(taskrun.FailureMessages(failures), "; "))
	}
	return nil
}

func (s Service) ResolveCluster(params map[string]any, copy Copy) (clusterInfo, error) {
	clusterID := stringParam(params, "clusterId", "")
	if clusterID == "" {
		return clusterInfo{}, errors.New(copy.ClusterRequired)
	}
	instances, err := s.store.ListAppInstances()
	if err != nil {
		return clusterInfo{}, err
	}
	clusters := innoDBClusters(instances)
	for _, cluster := range clusters {
		if cluster.ID == clusterID || strings.EqualFold(cluster.Name, clusterID) {
			if cluster.Endpoint == "" {
				return clusterInfo{}, errors.New(copy.ClusterNoEndpoint)
			}
			host, port, parseErr := parseEndpoint(cluster.Endpoint, 3306)
			if parseErr != nil {
				return clusterInfo{}, fmt.Errorf("%s: %w", copy.ClusterNoEndpoint, parseErr)
			}
			cluster.BootstrapHost = host
			cluster.BootstrapPort = port
			if cluster.RootUser == "" {
				cluster.RootUser = "root"
			}
			return cluster, nil
		}
	}
	return clusterInfo{}, errors.New(copy.ClusterMissing)
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log Logger, targetLog targetLogger) error {
	copy := DeleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	basePort := instanceBasePort(req.Instance)
	uninstaller := NewUninstaller(s.remote)
	return deleteflow.Run(deleteflow.Request{
		Target:     target,
		ServerName: req.Server.Name,
		InstanceID: req.Instance.ID,
		Log:        logForServer,
		Recorder:   recorder,
		Steps: []deleteflow.Step{
			{Name: "remove-remote", Title: copy.RemoveRemote, Run: func() error {
				return uninstaller.Uninstall(ctx, req.Server, req.Instance.Version, basePort, logForServer)
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
	taskrun.StartTarget(recorder, target)
	steps := mysqlRouterCheckSteps(copy)
	step := newStepRunner(logForServer, recorder, target, steps, copy)
	details := map[string]any{"checkedAt": time.Now().UTC().Format(time.RFC3339)}
	fail := func(err error) (CheckResult, error) {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		_ = s.markInstanceStatus(req.Instance, "failed", details)
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "failed", Message: msg, Details: details}, err
	}
	if err := step(1, "check-runtime", copy.CheckRuntime, func() error {
		return s.checkRuntime(ctx, req.Server, req.Instance, logForServer)
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

func mysqlRouterInstallSteps(copy Copy) []taskrun.Step {
	return []taskrun.Step{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "resolve-cluster", Title: copy.ResolveCluster},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "install-router", Title: copy.InstallRouter},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func mysqlRouterDeleteSteps(copy DeleteCopy) []taskrun.Step {
	return []taskrun.Step{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func mysqlRouterCheckSteps(copy Copy) []taskrun.Step {
	return []taskrun.Step{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}

func newStepRunner(log Logger, recorder stepRecorder, target string, steps []taskrun.Step, copy Copy) func(stepIndex int, stepName, label string, fn func() error) error {
	runner := taskrun.Runner{
		Log:      log,
		Recorder: recorder,
		Target:   target,
		Steps:    steps,
		Messages: taskrun.Messages{
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
	taskrun.FinishTarget(recorder, target, status, errText)
}
