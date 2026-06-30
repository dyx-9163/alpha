package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/apps/deleteflow"
	"aifar-deployment/backend/internal/installer/installerkit"
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
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	DefaultPassword string
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

type stepDef = taskrun.Step

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
	case "innodb-cluster":
		return s.installInnoDBCluster(ctx, req, resources, log, targetLog)
	case "standalone":
	default:
		return fmt.Errorf(copy.ClusterUnsupported, topology)
	}
	targets := targetServerIDs(req)
	if len(targets) != 1 {
		return errors.New(copy.SingleTargetOnly)
	}
	options := mysqlOptions(req.Parameters, req.DefaultPassword)
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
	target := targets[0]
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	taskrun.StartTarget(recorder, target)
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
	if err := step(3, "install-mysql", copy.InstallStandalone, func() error {
		return installer.Install(ctx, server, bundle, options, logForServer)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	var instance store.AppInstance
	if err := step(4, "record-instance", copy.RecordInstance, func() error {
		metadata, _ := json.Marshal(map[string]any{
			"resourcePath": bundle.ArchivePath,
			"rpmCount":     len(bundle.RPMPaths),
			"port":         options.Port,
			"rootUser":     options.RootUser,
			"serviceName":  "aifar-mysql",
			"endpoint":     fmt.Sprintf("%s:%d", server.Host, options.Port),
			"topology":     "standalone",
			"auth":         "password",
		})
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
			App:      "mysql",
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

func (s Service) installInnoDBCluster(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	targets := targetServerIDs(req)
	if len(targets) < 3 {
		return errors.New(copy.ClusterNeedNodes)
	}
	options := mysqlOptions(req.Parameters, req.DefaultPassword)
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

	clusterID := store.NewID("mysql_cluster")
	clusterName := mysqlClusterName(req.Parameters)
	preloadedServers := make(map[string]store.Server, len(targets))
	nodes := make([]InnoDBClusterNode, 0, len(targets))
	for _, target := range targets {
		server, loadErr := s.store.GetServer(target, true)
		if loadErr != nil {
			return loadErr
		}
		preloadedServers[target] = server
		nodes = append(nodes, InnoDBClusterNode{Host: server.Host, Port: options.Port})
	}
	installer := NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := mysqlInstallStepsFor("innodb-cluster", copy)
	failedTargets, err := s.installInnoDBClusterBase(ctx, targets, preloadedServers, bundle, options, installer, recorder, steps, copy, log, targetLog, req.Concurrency)
	if err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		for _, target := range targets {
			if !failedTargets[target] {
				logForTarget(log, targetLog, target).Error("%s", msg)
				finishTarget(recorder, target, "failed", msg)
			}
		}
		return err
	}

	bootstrapTarget := targets[0]
	bootstrapServer := preloadedServers[bootstrapTarget]
	bootstrapLog := logForTarget(log, targetLog, bootstrapTarget)
	bootstrapStep := newStepRunnerWithSteps(bootstrapLog, recorder, bootstrapTarget, copy, steps)
	if err := bootstrapStep(4, "bootstrap-cluster", copy.BootstrapCluster, func() error {
		return installer.BootstrapInnoDBCluster(ctx, bootstrapServer, InnoDBClusterBootstrapRequest{
			ClusterName:  clusterName,
			InstallRoot:  remoteInstallRoot(bootstrapServer, "mysql", bundle.Version),
			RootUser:     options.RootUser,
			RootPassword: options.RootPassword,
			Nodes:        nodes,
		}, bootstrapLog)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		bootstrapLog.Error("%s", msg)
		for _, target := range targets {
			finishTarget(recorder, target, "failed", msg)
		}
		return err
	}

	for _, target := range targets {
		logForServer := logForTarget(log, targetLog, target)
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		server := preloadedServers[target]
		var instance store.AppInstance
		if err := step(5, "record-instance", copy.RecordInstance, func() error {
			metadata, _ := json.Marshal(map[string]any{
				"clusterId":    clusterID,
				"clusterName":  clusterName,
				"resourcePath": bundle.ArchivePath,
				"rpmCount":     len(bundle.RPMPaths),
				"port":         options.Port,
				"rootUser":     options.RootUser,
				"endpoint":     fmt.Sprintf("%s:%d", server.Host, options.Port),
				"topology":     "innodb-cluster",
				"auth":         "password",
			})
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
				App:      "mysql",
				Version:  bundle.Version,
				ServerID: server.ID,
				Status:   "installed",
				Topology: "innodb-cluster",
				Metadata: string(metadata),
			})
			return saveErr
		}); err != nil {
			msg := fmt.Sprintf(copy.RecordFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		logForServer.Info(clusterNodeInstalledMessage(copy), instance.ID)
		finishTarget(recorder, target, "success", "")
	}
	log.Info(copy.ClusterInstalled, len(targets))
	return nil
}

func (s Service) installInnoDBClusterBase(ctx context.Context, targets []string, servers map[string]store.Server, bundle Bundle, options InstallOptions, installer Installer, recorder stepRecorder, steps []stepDef, copy Copy, log Logger, targetLog targetLogger, concurrency int) (map[string]bool, error) {
	limit := normalizeConcurrency(concurrency, len(targets))
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	failedTargets := map[string]bool{}
	for _, target := range targets {
		target := target
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ctx.Done():
				mu.Lock()
				failedTargets[target] = true
				failures = append(failures, fmt.Sprintf("%s: %v", target, ctx.Err()))
				mu.Unlock()
				return
			case sem <- struct{}{}:
			}
			defer func() { <-sem }()
			logForServer := logForTarget(log, targetLog, target)
			taskrun.StartTarget(recorder, target)
			step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
			server := servers[target]
			if err := step(1, "load-server", copy.LoadServer, func() error { return nil }); err != nil {
				recordClusterBaseFailure(&mu, failedTargets, &failures, target, fmt.Sprintf(copy.LoadFailed, err))
				logForServer.Error("%s", fmt.Sprintf(copy.LoadFailed, err))
				finishTarget(recorder, target, "failed", fmt.Sprintf(copy.LoadFailed, err))
				return
			}
			if err := step(2, "verify-resource", copy.VerifyResource, func() error {
				return VerifyBundle(bundle)
			}); err != nil {
				msg := fmt.Sprintf(copy.InstallFailed, err)
				recordClusterBaseFailure(&mu, failedTargets, &failures, target, msg)
				logForServer.Error("%s", msg)
				finishTarget(recorder, target, "failed", msg)
				return
			}
			if err := step(3, "install-mysql", copy.InstallStandalone, func() error {
				return installer.Install(ctx, server, bundle, options, logForServer)
			}); err != nil {
				msg := fmt.Sprintf(copy.InstallFailed, err)
				recordClusterBaseFailure(&mu, failedTargets, &failures, target, msg)
				logForServer.Error("%s", msg)
				finishTarget(recorder, target, "failed", msg)
			}
		}()
	}
	wg.Wait()
	if len(failures) > 0 {
		return failedTargets, errors.New(strings.Join(failures, "; "))
	}
	return failedTargets, nil
}

func recordClusterBaseFailure(mu *sync.Mutex, failedTargets map[string]bool, failures *[]string, target, msg string) {
	mu.Lock()
	defer mu.Unlock()
	failedTargets[target] = true
	*failures = append(*failures, fmt.Sprintf("%s: %s", target, msg))
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log Logger, targetLog targetLogger) error {
	copy := DeleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	port := instancePort(req.Instance)
	uninstaller := NewUninstaller(s.remote)
	return deleteflow.Run(deleteflow.Request{
		Target:     target,
		ServerName: req.Server.Name,
		InstanceID: req.Instance.ID,
		Log:        logForServer,
		Recorder:   recorder,
		Steps: []deleteflow.Step{
			{Name: "remove-remote", Title: copy.RemoveRemote, Run: func() error {
				return uninstaller.Uninstall(ctx, req.Server, req.Instance.Version, port, logForServer)
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
	copy := CheckCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	taskrun.StartTarget(recorder, target)
	topology := instanceTopology(req.Instance)
	steps := mysqlCheckStepsFor(topology, copy)
	step := newCheckStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
	details := map[string]any{
		"checkedAt": time.Now().UTC().Format(time.RFC3339),
		"topology":  topology,
	}

	fail := func(err error) (CheckResult, error) {
		msg := fmt.Sprintf(copy.CheckFailed, err)
		_ = s.markInstanceStatus(req.Instance, "failed", map[string]any{
			"checkedAt": details["checkedAt"],
			"topology":  topology,
			"error":     err.Error(),
		})
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "failed", Message: msg, Details: details}, err
	}

	if err := step(1, "check-runtime", copy.CheckRuntime, func() error {
		return s.checkMySQLRuntime(ctx, req.Server, req.Instance, req.DefaultPassword, logForServer)
	}); err != nil {
		return fail(err)
	}

	primaryEndpoint := ""
	nextStep := 2
	if topology == "innodb-cluster" {
		if err := step(2, "detect-primary", copy.DetectPrimary, func() error {
			var detectErr error
			primaryEndpoint, detectErr = s.detectInnoDBPrimary(ctx, req.Server, req.Instance, req.DefaultPassword, logForServer)
			return detectErr
		}); err != nil {
			return fail(err)
		}
		details["currentPrimaryEndpoint"] = primaryEndpoint
		nextStep = 3
	}

	if err := step(nextStep, "update-instance", copy.UpdateInstance, func() error {
		if topology == "innodb-cluster" && primaryEndpoint != "" {
			return s.markInnoDBClusterPrimary(req.Instance, primaryEndpoint, details)
		}
		return s.markInstanceStatus(req.Instance, "running", details)
	}); err != nil {
		return fail(err)
	}

	msg := fmt.Sprintf(copy.Checked, "running")
	logForServer.Info("%s", msg)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: "running", Message: msg, Details: details}, nil
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

func normalizeConcurrency(value, total int) int {
	if total < 1 {
		return 1
	}
	if value < 1 {
		return total
	}
	if value > total {
		return total
	}
	return value
}

func remoteInstallRoot(server store.Server, app, version string) string {
	return installerkit.InstallRoot(server.DeployDir, app)
}

func remoteLegacyInstallRoot(server store.Server, app, version string) string {
	return installerkit.LegacyInstallRoot(server.DeployDir, app, version)
}

func mysqlInstallSteps(copy Copy) []stepDef {
	return mysqlInstallStepsFor("standalone", copy)
}

func mysqlInstallStepsFor(topology string, copy Copy) []stepDef {
	if normalizeTopology(topology) == "innodb-cluster" {
		return []stepDef{
			{Name: "load-server", Title: copy.LoadServer},
			{Name: "verify-resource", Title: copy.VerifyResource},
			{Name: "install-mysql", Title: copy.InstallStandalone},
			{Name: "bootstrap-cluster", Title: copy.BootstrapCluster},
			{Name: "record-instance", Title: copy.RecordInstance},
		}
	}
	return []stepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "install-mysql", Title: copy.InstallStandalone},
		{Name: "record-instance", Title: copy.RecordInstance},
	}
}

func mysqlDeleteSteps(copy DeleteCopy) []stepDef {
	return []stepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func mysqlCheckStepsFor(topology string, copy CheckCopy) []stepDef {
	if normalizeTopology(topology) == "innodb-cluster" {
		return []stepDef{
			{Name: "check-runtime", Title: copy.CheckRuntime},
			{Name: "detect-primary", Title: copy.DetectPrimary},
			{Name: "update-instance", Title: copy.UpdateInstance},
		}
	}
	return []stepDef{
		{Name: "check-runtime", Title: copy.CheckRuntime},
		{Name: "update-instance", Title: copy.UpdateInstance},
	}
}

func newStepRunner(log Logger, recorder stepRecorder, target string, copy Copy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := mysqlInstallSteps(copy)
	return newStepRunnerWithSteps(log, recorder, target, copy, steps)
}

func newStepRunnerWithSteps(log Logger, recorder stepRecorder, target string, copy Copy, steps []stepDef) func(stepIndex int, stepName, label string, fn func() error) error {
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

func newCheckStepRunnerWithSteps(log Logger, recorder stepRecorder, target string, copy CheckCopy, steps []stepDef) func(stepIndex int, stepName, label string, fn func() error) error {
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
