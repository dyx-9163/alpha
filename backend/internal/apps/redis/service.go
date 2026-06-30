package redis

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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
			"serviceName":  "aifar-redis",
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

func (s Service) installSentinel(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
	copy := CopyFor(req.Language)
	roles, err := redisSentinelRoles(req.Parameters, targetServerIDs(req), copy)
	if err != nil {
		return err
	}
	targets := roles.AllIDs
	masterTarget := roles.MasterID
	port := redisPort(req.Parameters)
	sentinelPort := redisSentinelPort(req.Parameters)
	quorum := redisQuorum(req.Parameters, len(roles.SentinelIDs))
	masterName, err := redisSentinelMasterName(req.Parameters, copy.SentinelMasterNameInvalid)
	if err != nil {
		return err
	}
	password := redisPassword(req.Parameters, req.DefaultPassword)
	if password == "" {
		return fmt.Errorf("redis password is required")
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

	clusterID := store.NewID("redis_sentinel")
	servers := make(map[string]store.Server, len(targets))
	for _, target := range targets {
		server, loadErr := s.store.GetServer(target, true)
		if loadErr != nil {
			return loadErr
		}
		servers[target] = server
	}
	masterServer := servers[masterTarget]
	installer := NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	failures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(target string) error {
		logForServer := logForTarget(log, targetLog, target)
		if recorder != nil {
			recorder.StartTarget(target)
		}
		steps := redisSentinelStepsForTarget(copy, roles.IsSentinel(target))
		step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
		server := servers[target]
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
		installRoot := remoteInstallRoot(server, "redis", bundle.Version)
		role := roles.RoleFor(target)
		if err := step(3, "install-redis", copy.InstallStandalone, func() error {
			if role == "sentinel" {
				return installer.InstallBinariesWithLanguage(ctx, server, bundle, port, password, logForServer, req.Language)
			}
			return installer.InstallWithLanguage(ctx, server, bundle, port, password, logForServer, req.Language)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		recordStepIndex := 4
		if roles.IsSentinel(target) {
			recordStepIndex = 5
			if err := step(4, "configure-sentinel", copy.ConfigureSentinel, func() error {
				return installer.ConfigureSentinelNode(ctx, server, SentinelNodeConfig{
					Version:      bundle.Version,
					InstallRoot:  installRoot,
					RedisPort:    port,
					SentinelPort: sentinelPort,
					Password:     password,
					MasterName:   masterName,
					MasterHost:   masterServer.Host,
					MasterPort:   port,
					Quorum:       quorum,
					Role:         role,
				}, logForServer)
			}); err != nil {
				msg := fmt.Sprintf(copy.InstallFailed, err)
				logForServer.Error("%s", msg)
				finishTarget(recorder, target, "failed", msg)
				return errors.New(msg)
			}
		}
		var instance store.AppInstance
		if err := step(recordStepIndex, "record-instance", copy.RecordInstance, func() error {
			metadataMap := map[string]any{
				"clusterId":      clusterID,
				"resourcePath":   bundle.ArchivePath,
				"rpmCount":       len(bundle.RPMPaths),
				"port":           port,
				"sentinelPort":   sentinelPort,
				"sentinelQuorum": quorum,
				"masterName":     masterName,
				"role":           role,
				"masterHost":     masterServer.Host,
				"serviceName":    "aifar-redis",
				"sentinel":       roles.IsSentinel(target),
				"topology":       "sentinel",
				"auth":           "password",
			}
			if roles.IsSentinel(target) {
				metadataMap["sentinelName"] = "aifar-redis-sentinel"
			}
			metadata, _ := json.Marshal(metadataMap)
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
			return errors.New(msg)
		}
		logForServer.Info(copy.Installed, instance.ID)
		finishTarget(recorder, target, "success", "")
		return nil
	})
	if len(failures) > 0 {
		return fmt.Errorf(copy.BatchFailed, len(failures), strings.Join(taskrun.FailureMessages(failures), "; "))
	}
	log.Info(copy.ClusterInstalled, "sentinel", len(targets))
	return nil
}

func (s Service) installCluster(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
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
	bundle, err := SelectBundle(resources, req.Version)
	if err != nil {
		return err
	}
	if err := VerifyBundle(bundle); err != nil {
		return err
	}
	log.Info(copy.UsingArchive, bundle.ArchivePath)
	log.Info(copy.UsingRPMs, len(bundle.RPMPaths))

	clusterID := store.NewID("redis_cluster")
	preloadedServers := make(map[string]store.Server, len(targets))
	bootstrapNodes := make([]ClusterBootstrapNode, 0, len(targets))
	for _, target := range targets {
		server, loadErr := s.store.GetServer(target, true)
		if loadErr != nil {
			return loadErr
		}
		preloadedServers[target] = server
		bootstrapNodes = append(bootstrapNodes, ClusterBootstrapNode{Host: server.Host, Port: port})
	}
	installer := NewInstaller(s.remote)
	recorder, _ := log.(stepRecorder)
	steps := redisInstallStepsFor("cluster", copy)
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
		installRoot := remoteInstallRoot(server, "redis", bundle.Version)
		if err := step(3, "install-redis", copy.InstallStandalone, func() error {
			return installer.InstallWithLanguage(ctx, server, bundle, port, password, logForServer, req.Language)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		if err := step(4, "enable-cluster", copy.EnableClusterNode, func() error {
			return installer.EnableClusterNode(ctx, server, ClusterNodeConfig{
				Version:     bundle.Version,
				InstallRoot: installRoot,
				Port:        port,
				Password:    password,
			}, logForServer)
		}); err != nil {
			msg := fmt.Sprintf(copy.InstallFailed, err)
			logForServer.Error("%s", msg)
			finishTarget(recorder, target, "failed", msg)
			return errors.New(msg)
		}
		return nil
	})
	if len(failures) > 0 {
		msg := fmt.Sprintf(copy.BatchFailed, len(failures), strings.Join(taskrun.FailureMessages(failures), "; "))
		failedTargets := taskrun.FailureTargets(failures)
		for _, target := range targets {
			if !failedTargets[target] {
				logForTarget(log, targetLog, target).Error("%s", msg)
				finishTarget(recorder, target, "failed", msg)
			}
		}
		return errors.New(msg)
	}

	bootstrapTarget := targets[0]
	bootstrapServer := preloadedServers[bootstrapTarget]
	bootstrapLog := logForTarget(log, targetLog, bootstrapTarget)
	bootstrapStep := newStepRunnerWithSteps(bootstrapLog, recorder, bootstrapTarget, copy, steps)
	bootstrapRoot := remoteInstallRoot(bootstrapServer, "redis", bundle.Version)
	if err := bootstrapStep(5, "bootstrap-cluster", copy.BootstrapCluster, func() error {
		return installer.BootstrapCluster(ctx, bootstrapServer, ClusterBootstrapConfig{
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

	recordFailures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(target string) error {
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
				"serviceName":  "aifar-redis",
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
			return errors.New(msg)
		}
		logForServer.Info(copy.Installed, instance.ID)
		finishTarget(recorder, target, "success", "")
		return nil
	})
	if len(recordFailures) > 0 {
		return fmt.Errorf(copy.BatchFailed, len(recordFailures), strings.Join(taskrun.FailureMessages(recordFailures), "; "))
	}
	log.Info(copy.ClusterInstalled, "cluster", len(targets))
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

func (s Service) Check(ctx context.Context, req CheckRequest, log Logger, targetLog targetLogger) (CheckResult, error) {
	copy := CopyFor(req.Language)
	target := req.Instance.ServerID
	if target == "" {
		target = req.Server.ID
	}
	logForServer := logForTarget(log, targetLog, target)
	recorder, _ := log.(stepRecorder)
	taskrun.StartTarget(recorder, target)
	topology := instanceTopology(req.Instance)
	steps := redisCheckStepsFor(topology, copy)
	step := newStepRunnerWithSteps(logForServer, recorder, target, copy, steps)
	details := map[string]any{
		"checkedAt": time.Now().UTC().Format(time.RFC3339),
		"topology":  topology,
	}

	fail := func(err error) (CheckResult, error) {
		msg := fmt.Sprintf(copyWithFallback(copy.CheckFailed, "Redis 检测失败：%s"), err)
		_ = s.markInstanceStatus(req.Instance, "failed", map[string]any{
			"checkedAt": details["checkedAt"],
			"topology":  topology,
			"error":     err.Error(),
		})
		finishTarget(recorder, target, "failed", msg)
		return CheckResult{Status: "failed", Message: msg, Details: details}, err
	}

	if err := step(1, "check-runtime", copyWithFallback(copy.CheckRuntime, "检查 Redis 运行状态"), func() error {
		return s.checkRedisRuntime(ctx, req.Server, req.Instance, req.DefaultPassword, logForServer)
	}); err != nil {
		return fail(err)
	}

	role := instanceRole(req.Instance)
	currentMasterEndpoint := ""
	nextStep := 2
	if topology == "sentinel" || topology == "cluster" {
		if err := step(2, "detect-role", copyWithFallback(copy.DetectRole, "检测 Redis 当前角色"), func() error {
			var detectErr error
			role, currentMasterEndpoint, detectErr = s.detectRedisRole(ctx, req.Server, req.Instance, req.DefaultPassword, logForServer)
			return detectErr
		}); err != nil {
			return fail(err)
		}
		details["role"] = role
		if currentMasterEndpoint != "" {
			details["currentMasterEndpoint"] = currentMasterEndpoint
		}
		nextStep = 3
	}

	if err := step(nextStep, "update-instance", copyWithFallback(copy.UpdateInstance, "更新 Redis 实例状态"), func() error {
		if topology == "sentinel" && currentMasterEndpoint != "" {
			return s.markRedisSentinelMaster(req.Instance, role, currentMasterEndpoint, details)
		}
		return s.markRedisInstanceStatus(req.Instance, "running", role, details)
	}); err != nil {
		return fail(err)
	}

	msg := fmt.Sprintf(copyWithFallback(copy.Checked, "Redis 实例检测完成：%s"), "running")
	logForServer.Info("%s", msg)
	finishTarget(recorder, target, "success", "")
	return CheckResult{Status: "running", Message: msg, Details: details}, nil
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

func instanceTopology(instance store.AppInstance) string {
	if strings.TrimSpace(instance.Topology) != "" {
		return normalizeTopology(instance.Topology)
	}
	return normalizeTopology(metadataString(appMetadata(instance), "topology"))
}

func instanceRole(instance store.AppInstance) string {
	role := metadataString(appMetadata(instance), "role")
	if role == "" {
		return "node"
	}
	return role
}

func instanceEndpoint(instance store.AppInstance) string {
	endpoint := metadataString(appMetadata(instance), "endpoint")
	if endpoint != "" {
		return endpoint
	}
	return ""
}

func instanceHasSentinel(instance store.AppInstance) bool {
	metadata := appMetadata(instance)
	return metadataBool(metadata, "sentinel") || metadataString(metadata, "role") == "sentinel" || metadataString(metadata, "sentinelName") != ""
}

func appMetadata(instance store.AppInstance) map[string]any {
	metadata := map[string]any{}
	_ = json.Unmarshal([]byte(instance.Metadata), &metadata)
	if metadata == nil {
		return map[string]any{}
	}
	return metadata
}

func metadataString(metadata map[string]any, key string) string {
	value, ok := metadata[key]
	if !ok || value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func metadataBool(metadata map[string]any, key string) bool {
	value, ok := metadata[key]
	if !ok {
		return false
	}
	switch v := value.(type) {
	case bool:
		return v
	case string:
		v = strings.TrimSpace(v)
		return strings.EqualFold(v, "true") || v == "1"
	default:
		return false
	}
}

func sameRedisSentinelGroup(base, candidate store.AppInstance) bool {
	if candidate.App != "redis" || instanceTopology(candidate) != "sentinel" {
		return false
	}
	baseMetadata := appMetadata(base)
	candidateMetadata := appMetadata(candidate)
	if clusterID := metadataString(baseMetadata, "clusterId"); clusterID != "" {
		return clusterID == metadataString(candidateMetadata, "clusterId")
	}
	if masterName := metadataString(baseMetadata, "masterName"); masterName != "" {
		return strings.EqualFold(masterName, metadataString(candidateMetadata, "masterName"))
	}
	return base.ID != "" && base.ID == candidate.ID
}

func normalizeEndpoint(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "tcp://")
	value = strings.TrimPrefix(value, "redis://")
	return value
}

func firstNonEmptyLine(value string) string {
	lines := nonEmptyLines(value)
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

func nonEmptyLines(value string) []string {
	var out []string
	for _, line := range strings.Split(value, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func splitEndpoint(value string) (string, int) {
	value = strings.TrimSpace(value)
	idx := strings.LastIndex(value, ":")
	if idx <= 0 {
		return value, 0
	}
	port, _ := strconv.Atoi(strings.TrimSpace(value[idx+1:]))
	return strings.Trim(value[:idx], "[]"), port
}

func normalizeTopology(topology string) string {
	topology = strings.ToLower(strings.TrimSpace(topology))
	if topology == "" {
		return "standalone"
	}
	return topology
}

func remoteInstallRoot(server store.Server, app, version string) string {
	return installerkit.InstallRoot(server.DeployDir, app)
}

func remoteLegacyInstallRoot(server store.Server, app, version string) string {
	return installerkit.LegacyInstallRoot(server.DeployDir, app, version)
}

func redisInstallSteps(copy Copy) []stepDef {
	return redisInstallStepsFor("standalone", copy)
}

func redisInstallStepsFor(topology string, copy Copy) []stepDef {
	switch normalizeTopology(topology) {
	case "sentinel":
		return redisSentinelStepsForTarget(copy, true)
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

func redisSentinelStepsForTarget(copy Copy, runsSentinel bool) []stepDef {
	steps := []stepDef{
		{Name: "load-server", Title: copy.LoadServer},
		{Name: "verify-resource", Title: copy.VerifyResource},
		{Name: "install-redis", Title: copy.InstallStandalone},
	}
	if runsSentinel {
		steps = append(steps, stepDef{Name: "configure-sentinel", Title: copy.ConfigureSentinel})
	}
	return append(steps, stepDef{Name: "record-instance", Title: copy.RecordInstance})
}

func redisCheckStepsFor(topology string, copy Copy) []stepDef {
	steps := []stepDef{
		{Name: "check-runtime", Title: copyWithFallback(copy.CheckRuntime, "检查 Redis 运行状态")},
	}
	if topology == "sentinel" || topology == "cluster" {
		steps = append(steps, stepDef{Name: "detect-role", Title: copyWithFallback(copy.DetectRole, "检测 Redis 当前角色")})
	}
	return append(steps, stepDef{Name: "update-instance", Title: copyWithFallback(copy.UpdateInstance, "更新 Redis 实例状态")})
}

func redisDeleteSteps(copy DeleteCopy) []stepDef {
	return []stepDef{
		{Name: "remove-remote", Title: copy.RemoveRemote},
		{Name: "delete-instance", Title: copy.DeleteInstance},
	}
}

func copyWithFallback(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func newStepRunner(log Logger, recorder stepRecorder, target string, copy Copy) func(stepIndex int, stepName, label string, fn func() error) error {
	steps := redisInstallSteps(copy)
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
