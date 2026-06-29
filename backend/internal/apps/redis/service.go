package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"aifar-deployment/backend/internal/apps/deleteflow"
	redisinstaller "aifar-deployment/backend/internal/installer/redis"
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
	remote redisinstaller.Remote
}

type stepDef struct {
	Name  string
	Title string
}

type targetLogger func(target string) redisinstaller.Logger

type stepRecorder interface {
	StartTarget(target string)
	FinishTarget(target, status, errText string)
	StartStep(target, name, title string, order int)
	FinishStep(target, name, status, errText string)
}

func NewService(s Store, remote redisinstaller.Remote) Service {
	return Service{store: s, remote: remote}
}

func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log redisinstaller.Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	topology := normalizeTopology(req.Topology)
	switch topology {
	case "sentinel":
		return s.installSentinel(ctx, req, resources, log, targetLog)
	case "cluster":
		return s.installCluster(ctx, req, resources, log, targetLog)
	case "standalone":
	default:
		return fmt.Errorf(copy.TopologyUnsupported, topology)
	}
	targets := targetServerIDs(req)
	if len(targets) != 1 {
		return errors.New(copy.SingleTargetOnly)
	}
	port := redisPort(req.Parameters)
	password := redisPassword(req.Parameters, req.DefaultPassword)
	if password == "" {
		return fmt.Errorf("redis password is required")
	}
	bundle, err := redisinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := redisinstaller.VerifyBundle(bundle); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
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
		return redisinstaller.VerifyBundle(bundle)
	}); err != nil {
		msg := fmt.Sprintf(copy.InstallFailed, err)
		logForServer.Error("%s", msg)
		finishTarget(recorder, target, "failed", msg)
		return err
	}
	installer := redisinstaller.NewInstaller(s.remote)
	if err := step(3, "install-redis", copy.InstallStandalone, func() error {
		return installer.InstallWithLanguage(ctx, server, bundle, port, password, logForServer, req.Language)
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
			"port":         port,
			"serviceName":  fmt.Sprintf("aifar-redis-%d", port),
			"topology":     "standalone",
			"auth":         "password",
		})
		var saveErr error
		instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
			App:      "redis",
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

func (s Service) installSentinel(ctx context.Context, req InstallRequest, resources []store.Resource, log redisinstaller.Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	targets := targetServerIDs(req)
	if len(targets) < 3 {
		return errors.New(copy.SentinelNeedNodes)
	}
	port := redisPort(req.Parameters)
	sentinelPort := redisSentinelPort(req.Parameters)
	quorum := redisQuorum(req.Parameters, len(targets))
	password := redisPassword(req.Parameters, req.DefaultPassword)
	if password == "" {
		return fmt.Errorf("redis password is required")
	}
	bundle, err := redisinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := redisinstaller.VerifyBundle(bundle); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))

	clusterID := store.NewID("redis_sentinel")
	servers := make(map[string]store.Server, len(targets))
	installer := redisinstaller.NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := redisInstallStepsFor("sentinel", copy)
	masterTarget := targets[0]
	var masterServer store.Server
	for idx, target := range targets {
		logForServer := logForTarget(log, targetLog, target)
		if recorder != nil {
			recorder.StartTarget(target)
		}
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		var server store.Server
		if err := step(1, "load-server", copy.LoadServer, func() error {
			var loadErr error
			server, loadErr = s.store.GetServer(target, true)
			if loadErr == nil {
				servers[target] = server
				if target == masterTarget {
					masterServer = server
				}
			}
			return loadErr
		}); err != nil {
			msg := fmt.Sprintf(copy.LoadFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		if idx > 0 && strings.TrimSpace(masterServer.Host) == "" {
			masterServer = servers[masterTarget]
		}
		if err := step(2, "verify-resource", copy.VerifyResource, func() error {
			return redisinstaller.VerifyBundle(bundle)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		installRoot := remoteInstallRoot(server, "redis", bundle.Version)
		if err := step(3, "install-redis", copy.InstallStandalone, func() error {
			return installer.InstallWithLanguage(ctx, server, bundle, port, password, logForServer, req.Language)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		role := "replica"
		if target == masterTarget {
			role = "master"
		}
		if err := step(4, "configure-sentinel", copy.ConfigureSentinel, func() error {
			return installer.ConfigureSentinelNode(ctx, server, redisinstaller.SentinelNodeConfig{
				Version:      bundle.Version,
				InstallRoot:  installRoot,
				RedisPort:    port,
				SentinelPort: sentinelPort,
				Password:     password,
				MasterHost:   masterServer.Host,
				MasterPort:   port,
				Quorum:       quorum,
				Role:         role,
			}, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		var instance store.AppInstance
		if err := step(5, "record-instance", copy.RecordInstance, func() error {
			metadata, _ := json.Marshal(map[string]any{
				"clusterId":      clusterID,
				"resourcePath":   bundle.ArchivePath,
				"rpmCount":       len(bundle.RPMPaths),
				"port":           port,
				"sentinelPort":   sentinelPort,
				"sentinelQuorum": quorum,
				"role":           role,
				"masterHost":     masterServer.Host,
				"serviceName":    fmt.Sprintf("aifar-redis-%d", port),
				"sentinelName":   fmt.Sprintf("aifar-redis-sentinel-%d", sentinelPort),
				"topology":       "sentinel",
				"auth":           "password",
			})
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
				App:      "redis",
				Version:  bundle.Version,
				ServerID: server.ID,
				Status:   "installed",
				Topology: "sentinel",
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
	log.Info(copy.ClusterInstalled, "sentinel", len(targets))
	return nil
}

func (s Service) installCluster(ctx context.Context, req InstallRequest, resources []store.Resource, log redisinstaller.Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	targets := targetServerIDs(req)
	if len(targets) < 3 {
		return errors.New(copy.ClusterNeedNodes)
	}
	port := redisPort(req.Parameters)
	replicas := redisClusterReplicas(req.Parameters)
	if replicas > 0 && len(targets) < (replicas+1)*3 {
		return fmt.Errorf("redis cluster with %d replica(s) requires at least %d target servers", replicas, (replicas+1)*3)
	}
	password := redisPassword(req.Parameters, req.DefaultPassword)
	if password == "" {
		return fmt.Errorf("redis password is required")
	}
	bundle, err := redisinstaller.SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := redisinstaller.VerifyBundle(bundle); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))

	clusterID := store.NewID("redis_cluster")
	preloadedServers := make(map[string]store.Server, len(targets))
	bootstrapNodes := make([]redisinstaller.ClusterBootstrapNode, 0, len(targets))
	for _, target := range targets {
		server, loadErr := s.store.GetServer(target, true)
		if loadErr != nil {
			return loadErr
		}
		preloadedServers[target] = server
		bootstrapNodes = append(bootstrapNodes, redisinstaller.ClusterBootstrapNode{Host: server.Host, Port: port})
	}
	installer := redisinstaller.NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := redisInstallStepsFor("cluster", copy)
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
			return redisinstaller.VerifyBundle(bundle)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		installRoot := remoteInstallRoot(server, "redis", bundle.Version)
		if err := step(3, "install-redis", copy.InstallStandalone, func() error {
			return installer.InstallWithLanguage(ctx, server, bundle, port, password, logForServer, req.Language)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
		if err := step(4, "enable-cluster", copy.EnableClusterNode, func() error {
			return installer.EnableClusterNode(ctx, server, redisinstaller.ClusterNodeConfig{
				Version:     bundle.Version,
				InstallRoot: installRoot,
				Port:        port,
				Password:    password,
			}, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return err
		}
	}

	bootstrapTarget := targets[0]
	bootstrapServer := preloadedServers[bootstrapTarget]
	bootstrapLog := logForTarget(log, targetLog, bootstrapTarget)
	bootstrapStep := newStepRunnerWithSteps(bootstrapLog, recorder, bootstrapTarget, copy, steps)
	bootstrapRoot := remoteInstallRoot(bootstrapServer, "redis", bundle.Version)
	if err := bootstrapStep(5, "bootstrap-cluster", copy.BootstrapCluster, func() error {
		return installer.BootstrapCluster(ctx, bootstrapServer, redisinstaller.ClusterBootstrapConfig{
			Version:     bundle.Version,
			InstallRoot: bootstrapRoot,
			Port:        port,
			Password:    password,
			Replicas:    replicas,
			Nodes:       bootstrapNodes,
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
		if err := step(6, "record-instance", copy.RecordInstance, func() error {
			metadata, _ := json.Marshal(map[string]any{
				"clusterId":    clusterID,
				"resourcePath": bundle.ArchivePath,
				"rpmCount":     len(bundle.RPMPaths),
				"port":         port,
				"replicas":     replicas,
				"serviceName":  fmt.Sprintf("aifar-redis-%d", port),
				"endpoint":     fmt.Sprintf("%s:%d", server.Host, port),
				"topology":     "cluster",
				"auth":         "password",
			})
			var saveErr error
			instance, saveErr = s.store.SaveAppInstance(store.AppInstance{
				App:      "redis",
				Version:  bundle.Version,
				ServerID: server.ID,
				Status:   "installed",
				Topology: "cluster",
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
	log.Info(copy.ClusterInstalled, "cluster", len(targets))
	return nil
}

func redisPassword(params map[string]any, fallback string) string {
	value, ok := params["password"]
	if !ok {
		value, ok = params["redisPassword"]
	}
	if !ok {
		return strings.TrimSpace(fallback)
	}
	password := strings.TrimSpace(fmt.Sprint(value))
	if password == "" {
		password = strings.TrimSpace(fallback)
	}
	return password
}

func (s Service) Delete(ctx context.Context, req DeleteRequest, log redisinstaller.Logger, targetLog targetLogger) error {
	copy := DeleteCopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	port := instancePort(req.Instance)
	uninstaller := redisinstaller.NewUninstaller(s.remote)
	return deleteflow.Run(deleteflow.Request{
		Target:     target,
		ServerName: req.Server.Name,
		InstanceID: req.Instance.ID,
		Log:        logForServer,
		Recorder:   recorder,
		Steps: []deleteflow.Step{
			{Name: "remove-remote", Title: copy.RemoveRemote, Run: func() error {
				if normalizeTopology(req.Instance.Topology) == "sentinel" {
					return uninstaller.UninstallSentinelWithLanguage(ctx, req.Server, req.Instance.Version, port, instanceSentinelPort(req.Instance), logForServer, req.Language)
				}
				return uninstaller.UninstallWithLanguage(ctx, req.Server, req.Instance.Version, port, logForServer, req.Language)
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

func redisPort(params map[string]any) int {
	value, ok := params["port"]
	if !ok {
		return 6379
	}
	switch v := value.(type) {
	case int:
		return normalizePort(v)
	case int64:
		return normalizePort(int(v))
	case float64:
		return normalizePort(int(v))
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePort(n)
	default:
		return 6379
	}
}

func redisSentinelPort(params map[string]any) int {
	return intParam(params, "sentinelPort", 26379)
}

func redisQuorum(params map[string]any, targetCount int) int {
	defaultQuorum := targetCount/2 + 1
	if defaultQuorum < 2 {
		defaultQuorum = 2
	}
	return intParam(params, "quorum", defaultQuorum)
}

func redisClusterReplicas(params map[string]any) int {
	replicas := intParam(params, "replicas", 0)
	if replicas < 0 {
		return 0
	}
	return replicas
}

func intParam(params map[string]any, key string, fallback int) int {
	value, ok := params[key]
	if !ok {
		return fallback
	}
	switch v := value.(type) {
	case int:
		return normalizePortWithFallback(v, fallback)
	case int64:
		return normalizePortWithFallback(int(v), fallback)
	case float64:
		return normalizePortWithFallback(int(v), fallback)
	case string:
		n, _ := strconv.Atoi(strings.TrimSpace(v))
		return normalizePortWithFallback(n, fallback)
	default:
		return fallback
	}
}

func normalizePort(port int) int {
	if port <= 0 || port > 65535 {
		return 6379
	}
	return port
}

func normalizePortWithFallback(port, fallback int) int {
	if port <= 0 || port > 65535 {
		return fallback
	}
	return port
}

func instancePort(instance store.AppInstance) int {
	var metadata struct {
		Port int `json:"port"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	return normalizePort(metadata.Port)
}

func instanceSentinelPort(instance store.AppInstance) int {
	var metadata struct {
		SentinelPort int `json:"sentinelPort"`
	}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	if metadata.SentinelPort <= 0 || metadata.SentinelPort > 65535 {
		return 26379
	}
	return metadata.SentinelPort
}

func normalizeTopology(topology string) string {
	topology = strings.ToLower(strings.TrimSpace(topology))
	if topology == "" {
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

func redisInstallSteps(copy Copy) []stepDef {
	return redisInstallStepsFor("standalone", copy)
}

func redisInstallStepsFor(topology string, copy Copy) []stepDef {
	switch normalizeTopology(topology) {
	case "sentinel":
		return []stepDef{
			{Name: "load-server", Title: copy.LoadServer},
			{Name: "verify-resource", Title: copy.VerifyResource},
			{Name: "install-redis", Title: copy.InstallStandalone},
			{Name: "configure-sentinel", Title: copy.ConfigureSentinel},
			{Name: "record-instance", Title: copy.RecordInstance},
		}
	case "cluster":
		return []stepDef{
			{Name: "load-server", Title: copy.LoadServer},
			{Name: "verify-resource", Title: copy.VerifyResource},
			{Name: "install-redis", Title: copy.InstallStandalone},
			{Name: "enable-cluster", Title: copy.EnableClusterNode},
			{Name: "bootstrap-cluster", Title: copy.BootstrapCluster},
			{Name: "record-instance", Title: copy.RecordInstance},
		}
	default:
		return []stepDef{
			{Name: "load-server", Title: copy.LoadServer},
			{Name: "verify-resource", Title: copy.VerifyResource},
			{Name: "install-redis", Title: copy.InstallStandalone},
			{Name: "record-instance", Title: copy.RecordInstance},
		}
	}
}

func redisDeleteSteps(copy DeleteCopy) []stepDef {
	return []stepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func newStepRunner(log redisinstaller.Logger, recorder stepRecorder, target string, copy Copy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := redisInstallSteps(copy)
	return newStepRunnerWithSteps(log, recorder, target, copy, steps)
}

func newStepRunnerWithSteps(log redisinstaller.Logger, recorder stepRecorder, target string, copy Copy, steps []stepDef) func(stepIndex int, stepName, label string, fn func() error) error {
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

func logForTarget(fallback redisinstaller.Logger, targetLog targetLogger, target string) redisinstaller.Logger {
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
