package mysql

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/backuprepo"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/installflow"
	"aifar-deployment/backend/internal/store"
)

var disasterRebuildStepNames = []string{
	"stop-router", "stop-group-replication", "quarantine-old-data", "initialize-clean-seed",
	"restore-seed", "verify-seed", "create-cluster", "clone-members", "wait-members-online",
	"bootstrap-router", "verify-router-6446", "record-completion",
}

func (m Module) planDisasterRebuild(ctx context.Context, req registry.RestoreRequest) ([]registry.InstallStepPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := validateDisasterRebuildRequest(req); err != nil {
		return nil, err
	}
	return clusterBackupPlan(req.Instances, disasterRebuildStepNames), nil
}

func validateDisasterRebuildRequest(req registry.RestoreRequest) (store.MySQLMaintenanceMarker, error) {
	if strings.TrimSpace(fmt.Sprint(req.Parameters["mode"])) != "disaster-rebuild" ||
		!boolParameter(req.Parameters, "maintenanceConfirmed") || !boolParameter(req.Parameters, "disasterConfirmed") ||
		!boolParameter(req.Parameters, "serverPasswordsConfirmed") {
		return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLRebuildConfirmationRequired)
	}
	if err := validateClusterRequest(req.Instance, req.Instances, req.Servers); err != nil {
		return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLRebuildConfirmationRequired)
	}
	clusterID := clusterIDFromInstance(req.Instance)
	if req.Backup.App != "mysql" || req.Backup.BackupType != "logical-full" || req.Backup.Status != "success" ||
		strings.TrimSpace(req.RepositoryDir) == "" || clusterIDFromBackup(req.Backup) != clusterID {
		return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLRestoreManifestInvalid)
	}
	mapping, ok := controlledTargetMapping(req.Parameters["targetMapping"])
	if !ok || len(mapping) != 3 {
		return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLRebuildConfirmationRequired)
	}
	serverByID := make(map[string]store.Server, len(req.Servers))
	for _, server := range req.Servers {
		serverByID[server.ID] = server
	}
	var common store.MySQLMaintenanceMarker
	for index, instance := range req.Instances {
		if mapping[instance.ID] != instance.ServerID || serverByID[instance.ServerID].ID == "" || instance.Version == "" {
			return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLRebuildConfirmationRequired)
		}
		marker, present, err := store.ParseMySQLMaintenanceMarker(instance.Metadata)
		if err != nil || !present || marker.Scope != "cluster" || marker.ClusterID != clusterID || marker.BackupID != req.Backup.ID {
			return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLMaintenanceStateInvalid)
		}
		if index > 0 && !sameMaintenanceMarker(common, marker) {
			return store.MySQLMaintenanceMarker{}, mysqlOperationError(MySQLMaintenanceStateInvalid)
		}
		common = marker
	}
	return common, nil
}

func controlledTargetMapping(value any) (map[string]string, bool) {
	result := map[string]string{}
	switch typed := value.(type) {
	case map[string]any:
		for instanceID, rawServerID := range typed {
			serverID, ok := rawServerID.(string)
			if !ok || strings.TrimSpace(instanceID) != instanceID || strings.TrimSpace(serverID) != serverID || instanceID == "" || serverID == "" {
				return nil, false
			}
			result[instanceID] = serverID
		}
	case map[string]string:
		for instanceID, serverID := range typed {
			if strings.TrimSpace(instanceID) != instanceID || strings.TrimSpace(serverID) != serverID || instanceID == "" || serverID == "" {
				return nil, false
			}
			result[instanceID] = serverID
		}
	default:
		return nil, false
	}
	return result, true
}

func sortedDisasterMembers(instances []store.AppInstance) []store.AppInstance {
	result := append([]store.AppInstance(nil), instances...)
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type disasterRebuildStore interface {
	restoreStore
	maintenanceStore
	CompleteMySQLDisasterRebuild([]string, store.MySQLMaintenanceMarker, store.MySQLDisasterRebuildCompletion) error
}

type disasterRebuildProgress struct {
	Version           int               `json:"version"`
	TaskID            string            `json:"taskId"`
	SourceBackupID    string            `json:"sourceBackupId"`
	ClusterID         string            `json:"clusterId"`
	RestoreGeneration int               `json:"restoreGeneration"`
	QuarantinePaths   map[string]string `json:"quarantinePaths"`
	SeedStage         string            `json:"seedStage"`
	MemberStages      map[string]string `json:"memberStages"`
	RouterStage       string            `json:"routerStage"`
	RouterStages      map[string]string `json:"routerStages"`
	CompletionStage   string            `json:"completionStage,omitempty"`
}

type disasterExecutionState struct {
	data        disasterRebuildStore
	backup      store.AppBackup
	marker      store.MySQLMaintenanceMarker
	members     []clusterMemberNode
	seed        clusterMemberNode
	routers     []RouterRef
	credentials map[string]store.Credential
	manifest    BackupManifest
	repository  *backuprepo.Repository
	digest      string
	progress    disasterRebuildProgress
}

func newDisasterRebuildProgress(run registry.RunContext, target, language string) *standaloneRestoreProgress {
	recorder, _ := run.Log.(installflow.Recorder)
	steps := make([]installflow.Step, len(disasterRebuildStepNames))
	for index, name := range disasterRebuildStepNames {
		steps[index] = installflow.Step{Name: name, Title: restoreStepTitle(language, name)}
	}
	progress := &standaloneRestoreProgress{recorder: recorder, target: target, steps: steps, started: map[int]bool{}}
	progress.runner = installflow.Runner{Log: run.Log, Recorder: recorder, Target: target, Steps: steps}
	installflow.StartTarget(recorder, target)
	return progress
}

func (m Module) restoreDisasterRebuild(ctx context.Context, req registry.RestoreRequest, run registry.RunContext) (retErr error) {
	if !validLogicalTaskID(run.TaskID) {
		return localizedMySQLOperationError(req.Language, MySQLRebuildConfirmationRequired)
	}
	target := clusterIDFromInstance(req.Instance)
	runner := newDisasterRebuildProgress(run, target, req.Language)
	defer runner.finish(&retErr, ctx)
	state, err := m.service.prepareDisasterRebuild(ctx, req, run.TaskID)
	if err != nil {
		return err
	}
	quarantined := len(state.progress.QuarantinePaths) > 0
	defer func() {
		if retErr != nil && quarantined {
			_ = updateRestorePhase(state.data, &state.backup, "restore_incomplete", run.TaskID, state.digest)
		}
	}()

	scripts, cleanupScripts, err := m.service.prepareDisasterScripts(ctx, &state, run.TaskID)
	if err != nil {
		return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	defer func() {
		if cleanupErr := cleanupScripts(true); cleanupErr != nil {
			retErr = errors.Join(retErr, localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete))
		}
	}()

	if err := runner.step(1, func() error {
		for _, router := range state.routers {
			if state.progress.RouterStages[router.InstanceID] == "stopped" {
				continue
			}
			server, getErr := m.service.store.GetServer(router.ServerID, false)
			if getErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRebuildRouterFailed)
			}
			if _, runErr := m.service.remote.Run(ctx, server, "set -eu; systemctl stop aifar-mysql-router"); runErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRebuildRouterFailed)
			}
			state.progress.RouterStages[router.InstanceID] = "stopped"
			if saveDisasterProgress(state.data, &state.backup, state.progress) != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
		}
		state.progress.RouterStage = "stopped"
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	if err := runner.step(2, func() error {
		for _, member := range state.members {
			if _, runErr := m.service.remote.Run(ctx, member.server, "sh "+installerkit.ShellQuote(scripts[member.instance.ID])+" stop-gr"); runErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := runner.step(3, func() error {
		for _, member := range state.members {
			expected := disasterQuarantinePath(member.server, member.instance, run.TaskID)
			if existing := state.progress.QuarantinePaths[member.server.ID]; existing != "" {
				if existing != expected {
					return localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
				}
				if _, runErr := m.service.remote.Run(ctx, member.server, "sh "+installerkit.ShellQuote(scripts[member.instance.ID])+" verify-quarantine"); runErr != nil {
					return localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
				}
				continue
			}
			if _, runErr := m.service.remote.Run(ctx, member.server, "sh "+installerkit.ShellQuote(scripts[member.instance.ID])+" quarantine"); runErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
			quarantined = true
			state.progress.QuarantinePaths[member.server.ID] = expected
			if saveDisasterProgress(state.data, &state.backup, state.progress) != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	if err := runner.step(4, func() error {
		if state.progress.SeedStage != "" {
			return nil
		}
		if _, runErr := m.service.remote.Run(ctx, state.seed.server, "sh "+installerkit.ShellQuote(scripts[state.seed.instance.ID])+" initialize"); runErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		state.progress.SeedStage = "initialized"
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	seedWork := mysqlBackupWorkDir(run.TaskID + "-disaster-seed")
	seedWorkServer := state.seed.server
	seedCleaned := false
	cleanupSeed := func(strict bool) error {
		if seedCleaned {
			return nil
		}
		seedCleaned = true
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		_, cleanupErr := m.service.remote.Run(cleanupCtx, seedWorkServer, cleanupBackupCommand(seedWork))
		if strict {
			return cleanupErr
		}
		return nil
	}
	defer func() {
		if cleanupErr := cleanupSeed(true); cleanupErr != nil {
			retErr = errors.Join(retErr, localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete))
		}
	}()
	if err := runner.step(5, func() error {
		if state.progress.SeedStage == "loaded" || state.progress.SeedStage == "verified" {
			return nil
		}
		return m.service.loadDisasterSeed(ctx, &state, seedWork, run.TaskID, req.Language)
	}); err != nil {
		return err
	}
	if err := runner.step(6, func() error {
		if state.progress.SeedStage == "verified" {
			return nil
		}
		result, verifyErr := m.service.remote.Run(ctx, state.seed.server, finalRestoreVerificationCommand(seedWork, instancePort(state.seed.instance)))
		if verifyErr != nil || !matchesFinalRestoreVerification(result.Stdout, state.manifest.Verification) || verifyRestoreExpectation(state.data, state.repository, state.backup.ID, run.TaskID, state.digest) != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		state.progress.SeedStage = "verified"
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	if err := runner.step(7, func() error {
		if state.progress.MemberStages[state.seed.instance.ID] == "cluster-created" || state.progress.MemberStages[state.seed.instance.ID] == "ONLINE" {
			return nil
		}
		command, commandErr := disasterCreateClusterCommand(seedWork, state.seed, clusterNameFromInstance(state.seed.instance))
		if commandErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		if _, runErr := m.service.remote.Run(ctx, state.seed.server, command); runErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		state.progress.MemberStages[state.seed.instance.ID] = "cluster-created"
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	if err := runner.step(8, func() error {
		for _, member := range state.members {
			if member.instance.ID == state.seed.instance.ID || state.progress.MemberStages[member.instance.ID] == "cloned" || state.progress.MemberStages[member.instance.ID] == "ONLINE" {
				continue
			}
			if state.progress.MemberStages[member.instance.ID] == "" {
				if _, runErr := m.service.remote.Run(ctx, member.server, "sh "+installerkit.ShellQuote(scripts[member.instance.ID])+" initialize"); runErr != nil {
					return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
				}
				state.progress.MemberStages[member.instance.ID] = "initialized"
				if saveDisasterProgress(state.data, &state.backup, state.progress) != nil {
					return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
				}
			}
			command, commandErr := disasterCloneMemberCommand(seedWork, state.seed, member)
			if commandErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
			if _, runErr := m.service.remote.Run(ctx, state.seed.server, command); runErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
			state.progress.MemberStages[member.instance.ID] = "cloned"
			if saveDisasterProgress(state.data, &state.backup, state.progress) != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
		}
		return nil
	}); err != nil {
		return err
	}
	roles := map[string]string{}
	if err := runner.step(9, func() error {
		result, runErr := m.service.remote.Run(ctx, state.seed.server, "# __AIFAR_WAIT_ONLINE__\n"+inspectClusterMembersCommand(seedWork, instancePort(state.seed.instance)))
		if runErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		observed, parseErr := parseInnoDBClusterRuntime(result.Stdout)
		if parseErr != nil || len(observed) != 3 {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		byEndpoint := map[string]clusterMemberNode{}
		for _, member := range state.members {
			byEndpoint[normalizeEndpoint(member.endpoint)] = member
		}
		primaries := 0
		for _, item := range observed {
			member, found := byEndpoint[normalizeEndpoint(item.endpoint)]
			if !found || item.status != "ONLINE" || (item.role != "PRIMARY" && item.role != "SECONDARY") {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
			roles[member.instance.ID] = item.role
			state.progress.MemberStages[member.instance.ID] = "ONLINE"
			if item.role == "PRIMARY" {
				state.seed = member
				primaries++
			}
		}
		if primaries != 1 || len(roles) != 3 {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	if err := runner.step(10, func() error {
		for _, router := range state.routers {
			if state.progress.RouterStages[router.InstanceID] == "bootstrapped" || state.progress.RouterStages[router.InstanceID] == "verified" {
				continue
			}
			server, getErr := m.service.store.GetServer(router.ServerID, false)
			if getErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRebuildRouterFailed)
			}
			if runErr := m.service.bootstrapDisasterRouter(ctx, server, router, state.seed, state.credentials[state.seed.instance.ID], run.TaskID); runErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRebuildRouterFailed)
			}
			state.progress.RouterStages[router.InstanceID] = "bootstrapped"
			if saveDisasterProgress(state.data, &state.backup, state.progress) != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
		}
		state.progress.RouterStage = "bootstrapped"
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	if err := runner.step(11, func() error {
		cluster := resolvedInnoDBCluster{clusterID: state.progress.ClusterID, members: state.members, routers: state.routers, primary: state.seed, representative: state.seed.instance}
		if verifyErr := m.service.verifyClusterRecovered(ctx, &cluster, state.manifest.Schemas, run.TaskID); verifyErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRebuildRouterFailed)
		}
		for _, router := range state.routers {
			state.progress.RouterStages[router.InstanceID] = "verified"
		}
		state.progress.RouterStage = "verified"
		return saveDisasterProgress(state.data, &state.backup, state.progress)
	}); err != nil {
		return err
	}
	if err := runner.step(12, func() error {
		for _, member := range state.members {
			if state.progress.QuarantinePaths[member.server.ID] != disasterQuarantinePath(member.server, member.instance, run.TaskID) {
				return localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
			}
			if _, runErr := m.service.remote.Run(ctx, member.server, "sh "+installerkit.ShellQuote(scripts[member.instance.ID])+" verify"); runErr != nil {
				return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
			}
		}
		if verifyRestoreExpectation(state.data, state.repository, state.backup.ID, run.TaskID, state.digest) != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		if cleanupErr := cleanupScripts(true); cleanupErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		if cleanupErr := cleanupSeed(true); cleanupErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
		}
		ids := make([]string, 0, 3)
		for _, member := range state.members {
			ids = append(ids, member.instance.ID)
		}
		completion := store.MySQLDisasterRebuildCompletion{ClusterID: state.progress.ClusterID, SourceBackupID: state.backup.ID, TaskID: run.TaskID, Generation: state.progress.RestoreGeneration, Roles: roles, CompletedAt: time.Now().UTC()}
		if completeErr := state.data.CompleteMySQLDisasterRebuild(ids, state.marker, completion); completeErr != nil {
			return localizedMySQLOperationError(req.Language, MySQLMaintenanceStatePersistFailed)
		}
		return nil
	}); err != nil {
		return err
	}
	return nil
}

func (s Service) prepareDisasterRebuild(ctx context.Context, req registry.RestoreRequest, taskID string) (disasterExecutionState, error) {
	data, ok := s.store.(disasterRebuildStore)
	if !ok {
		return disasterExecutionState{}, errors.New("MySQL disaster rebuild store is unavailable")
	}
	fresh, err := data.GetAppInstance(req.Instance.ID)
	if err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
	}
	instances, err := maintenanceInstances(data, fresh)
	if err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
	}
	mapping, ok := controlledTargetMapping(req.Parameters["targetMapping"])
	if !ok || len(mapping) != 3 {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRebuildConfirmationRequired)
	}
	state := disasterExecutionState{data: data, credentials: map[string]store.Credential{}}
	for index, instance := range instances {
		if mapping[instance.ID] != instance.ServerID {
			return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRebuildConfirmationRequired)
		}
		marker, present, parseErr := store.ParseMySQLMaintenanceMarker(instance.Metadata)
		if parseErr != nil || !present || marker.Scope != "cluster" || marker.ClusterID != clusterIDFromInstance(instance) || marker.BackupID != req.Backup.ID || (index > 0 && !sameMaintenanceMarker(state.marker, marker)) {
			return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
		}
		state.marker = marker
		server, getErr := s.store.GetServer(instance.ServerID, false)
		if getErr != nil || strings.TrimSpace(server.Host) == "" {
			return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRebuildConfirmationRequired)
		}
		credential, credentialErr := data.GetBoundCredential(instance.ID, "admin", true)
		if credentialErr != nil || credential.Kind != "mysql" || credential.Status != "active" || strings.TrimSpace(credential.Username) == "" || strings.TrimSpace(credential.Secret["password"]) == "" {
			return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLCredentialUnavailable)
		}
		state.credentials[instance.ID] = credential
		state.members = append(state.members, clusterMemberNode{instance: instance, server: server, endpoint: instanceEndpoint(instance, server, instancePort(instance))})
	}
	state.backup, err = data.GetAppBackup(req.Backup.ID)
	if err != nil || state.backup.Status != "success" || state.backup.BackupType != "logical-full" || clusterIDFromBackup(state.backup) != state.marker.ClusterID {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	state.repository, err = backuprepo.New(req.RepositoryDir)
	if err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
	}
	verification, err := state.repository.Verify(state.backup)
	if err != nil || verification.SHA256 != state.backup.Checksum || verification.Size != state.backup.Size || !sameBackupPath(verification.Paths.Archive, state.backup.Path) {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLBackupVerifyFailed)
	}
	state.manifest, err = decodeRestoreManifest(verification.Manifest)
	if err != nil || state.manifest.ManifestVersion != 2 || state.manifest.Verification == nil || state.manifest.BackupID != state.backup.ID || state.manifest.ClusterID != state.marker.ClusterID || state.manifest.Topology != "innodb-cluster" {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	manifestMembers := map[string]string{}
	for _, member := range state.manifest.Members {
		manifestMembers[member.InstanceID] = member.ServerID
	}
	for _, member := range state.members {
		if manifestMembers[member.instance.ID] != member.server.ID || ValidateRestoreCompatibility(state.manifest, state.backup.BackupType, "innodb-cluster", member.instance.Version) != nil {
			return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
		}
		if member.server.ID == state.backup.ServerID {
			state.seed = member
		}
	}
	if state.seed.instance.ID == "" || len(manifestMembers) != 3 {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	all, err := data.ListAppInstances()
	if err != nil {
		return disasterExecutionState{}, err
	}
	for _, instance := range all {
		if instance.App == "mysql-router" && clusterIDFromInstance(instance) == state.marker.ClusterID {
			server, getErr := s.store.GetServer(instance.ServerID, false)
			if getErr != nil {
				return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRebuildRouterFailed)
			}
			state.routers = append(state.routers, RouterRef{InstanceID: instance.ID, ServerID: instance.ServerID, Endpoint: instanceEndpoint(instance, server, routerPort(instance)), Status: instance.Status})
		}
	}
	if len(state.routers) == 0 || !sameRouterIdentity(state.routers, state.manifest.Routers) {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreManifestInvalid)
	}
	canonical, _ := CanonicalBackupManifestJSON(state.manifest)
	state.digest = fmt.Sprintf("%x", sha256.Sum256(canonical))
	if err := updateRestorePhase(data, &state.backup, "preflight", taskID, state.digest); err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	cluster, clusterErr := data.GetAppCluster(state.marker.ClusterID)
	if clusterErr != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
	}
	generation, generationErr := nextDisasterRestoreGeneration(cluster.Metadata)
	if generationErr != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
	}
	state.progress, err = loadOrCreateDisasterProgress(state.backup, state.marker.ClusterID, taskID, generation)
	if err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
	}
	if err := validateDisasterProgressTopology(state, taskID); err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLMaintenanceStateInvalid)
	}
	if err := saveDisasterProgress(data, &state.backup, state.progress); err != nil {
		return disasterExecutionState{}, localizedMySQLOperationError(req.Language, MySQLRestoreIncomplete)
	}
	return state, nil
}

func nextDisasterRestoreGeneration(raw string) (int, error) {
	metadata, err := strictBackupMetadata(raw)
	if err != nil {
		return 0, err
	}
	value, present := metadata["mysqlDisasterRestore"]
	if !present {
		return 1, nil
	}
	var previous struct {
		Version    int `json:"version"`
		Generation int `json:"generation"`
	}
	if err := json.Unmarshal(value, &previous); err != nil || previous.Version != 1 || previous.Generation < 1 || previous.Generation == int(^uint(0)>>1) {
		return 0, errors.New("invalid prior MySQL disaster restore generation")
	}
	return previous.Generation + 1, nil
}

func loadOrCreateDisasterProgress(backup store.AppBackup, clusterID, taskID string, generation int) (disasterRebuildProgress, error) {
	metadata, err := strictBackupMetadata(backup.Metadata)
	if err != nil {
		return disasterRebuildProgress{}, err
	}
	raw, present := metadata["disasterRebuild"]
	if !present {
		return disasterRebuildProgress{
			Version: 1, TaskID: taskID, SourceBackupID: backup.ID, ClusterID: clusterID, RestoreGeneration: generation,
			QuarantinePaths: map[string]string{}, MemberStages: map[string]string{}, RouterStages: map[string]string{},
		}, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var progress disasterRebuildProgress
	if err := decoder.Decode(&progress); err != nil {
		return disasterRebuildProgress{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return disasterRebuildProgress{}, errors.New("disaster rebuild progress must be one object")
	}
	if progress.Version != 1 || progress.TaskID != taskID || progress.SourceBackupID != backup.ID || progress.ClusterID != clusterID || progress.RestoreGeneration != generation || progress.QuarantinePaths == nil || progress.MemberStages == nil || progress.RouterStages == nil || progress.CompletionStage != "" {
		return disasterRebuildProgress{}, errors.New("disaster rebuild progress identity changed")
	}
	return progress, nil
}

func validateDisasterProgressTopology(state disasterExecutionState, taskID string) error {
	membersByInstance := make(map[string]clusterMemberNode, len(state.members))
	membersByServer := make(map[string]clusterMemberNode, len(state.members))
	for _, member := range state.members {
		membersByInstance[member.instance.ID] = member
		membersByServer[member.server.ID] = member
	}
	for serverID, quarantine := range state.progress.QuarantinePaths {
		member, present := membersByServer[serverID]
		if !present || quarantine != disasterQuarantinePath(member.server, member.instance, taskID) {
			return errors.New("disaster rebuild quarantine progress changed")
		}
	}
	for instanceID, stage := range state.progress.MemberStages {
		member, present := membersByInstance[instanceID]
		if !present {
			return errors.New("disaster rebuild member progress changed")
		}
		if member.instance.ID == state.seed.instance.ID {
			if stage != "cluster-created" && stage != "ONLINE" {
				return errors.New("invalid disaster rebuild seed member stage")
			}
		} else if stage != "initialized" && stage != "cloned" && stage != "ONLINE" {
			return errors.New("invalid disaster rebuild member stage")
		}
	}
	if state.progress.SeedStage != "" && state.progress.SeedStage != "initialized" && state.progress.SeedStage != "loaded" && state.progress.SeedStage != "verified" {
		return errors.New("invalid disaster rebuild seed stage")
	}
	routers := make(map[string]bool, len(state.routers))
	for _, router := range state.routers {
		routers[router.InstanceID] = true
	}
	for instanceID, stage := range state.progress.RouterStages {
		if !routers[instanceID] || (stage != "stopped" && stage != "bootstrapped" && stage != "verified") {
			return errors.New("invalid disaster rebuild Router progress")
		}
	}
	if state.progress.RouterStage != "" && state.progress.RouterStage != "stopped" && state.progress.RouterStage != "bootstrapped" && state.progress.RouterStage != "verified" {
		return errors.New("invalid disaster rebuild aggregate Router stage")
	}
	return nil
}

func saveDisasterProgress(data backupStore, backup *store.AppBackup, progress disasterRebuildProgress) error {
	if backup == nil || progress.Version != 1 || progress.TaskID == "" || progress.SourceBackupID != backup.ID || progress.ClusterID == "" || progress.RestoreGeneration < 1 {
		return errors.New("invalid disaster rebuild progress")
	}
	var lastErr error
	for attempt := 0; attempt < restorePersistenceAttempts; attempt++ {
		fresh, err := data.GetAppBackup(backup.ID)
		if err != nil || fresh.Status != "success" || fresh.BackupType != "logical-full" {
			return errors.New("disaster rebuild backup ownership changed")
		}
		metadata, err := strictBackupMetadata(fresh.Metadata)
		if err != nil {
			return err
		}
		metadata["disasterRebuild"], err = json.Marshal(progress)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		fresh.Metadata = string(encoded)
		saved, err := data.SaveAppBackup(fresh)
		if err == nil {
			*backup = saved
			return nil
		}
		lastErr = err
	}
	return errors.Join(errors.New("persist disaster rebuild progress failed"), lastErr)
}

func sameRouterIdentity(current, manifest []RouterRef) bool {
	if len(current) == 0 || len(current) != len(manifest) {
		return false
	}
	key := func(value RouterRef) string {
		return value.InstanceID + "\x00" + value.ServerID + "\x00" + normalizeEndpoint(value.Endpoint)
	}
	left, right := make([]string, len(current)), make([]string, len(manifest))
	for index := range current {
		left[index] = key(current[index])
	}
	for index := range manifest {
		right[index] = key(manifest[index])
	}
	sort.Strings(left)
	sort.Strings(right)
	return strings.Join(left, "\n") == strings.Join(right, "\n")
}

func disasterQuarantinePath(server store.Server, instance store.AppInstance, taskID string) string {
	return path.Join(remoteInstallRoot(server, "mysql", instance.Version), "data") + ".quarantine-" + taskID
}

func (s Service) prepareDisasterScripts(ctx context.Context, state *disasterExecutionState, taskID string) (map[string]string, func(bool) error, error) {
	paths := map[string]string{}
	workByInstance := map[string]string{}
	cleaned := false
	cleanup := func(strict bool) error {
		if cleaned {
			return nil
		}
		cleaned = true
		var result error
		for _, member := range state.members {
			work := workByInstance[member.instance.ID]
			if work == "" {
				continue
			}
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
			_, err := s.remote.Run(cleanupCtx, member.server, "rm -f -- "+installerkit.ShellQuote(path.Join(work, "disaster-rebuild.sh"))+" "+installerkit.ShellQuote(path.Join(work, "secret-context.cnf"))+" "+installerkit.ShellQuote(path.Join(work, "admin-init.sql")))
			cancel()
			if strict && err != nil {
				result = errors.Join(result, err)
			}
		}
		return result
	}
	for _, member := range state.members {
		installRoot := remoteInstallRoot(member.server, "mysql", member.instance.Version)
		work := path.Join(installRoot, "_disaster", taskID+"-"+member.instance.ID)
		workByInstance[member.instance.ID] = work
		options := DisasterRebuildScriptOptions{TaskID: taskID, InstallRoot: installRoot, WorkDir: work, DataDir: path.Join(installRoot, "data"), QuarantineDir: disasterQuarantinePath(member.server, member.instance, taskID), Port: instancePort(member.instance)}
		script, err := renderDisasterRebuildScript(options)
		if err != nil {
			_ = cleanup(false)
			return nil, cleanup, err
		}
		localScript, err := installerkit.WriteTempScript("aifar-mysql-disaster-*.sh", script)
		if err != nil {
			_ = cleanup(false)
			return nil, cleanup, err
		}
		if _, err = s.remote.Run(ctx, member.server, "set -eu; mkdir -p "+installerkit.ShellQuote(work)); err == nil {
			err = s.remote.UploadFile(ctx, member.server, localScript, path.Join(work, "disaster-rebuild.sh"), 0o700)
		}
		_ = os.Remove(localScript)
		if err != nil {
			_ = cleanup(false)
			return nil, cleanup, err
		}
		secret, err := writeMySQLSecretContext(state.credentials[member.instance.ID], instancePort(member.instance))
		if err == nil {
			err = s.remote.UploadFile(ctx, member.server, secret, path.Join(work, "secret-context.cnf"), 0o600)
		}
		if secret != "" {
			_ = os.Remove(secret)
		}
		if err != nil {
			_ = cleanup(false)
			return nil, cleanup, err
		}
		adminInit, err := writeDisasterAdminInitSQL(state.credentials[member.instance.ID])
		if err == nil {
			err = s.remote.UploadFile(ctx, member.server, adminInit, path.Join(work, "admin-init.sql"), 0o600)
		}
		if adminInit != "" {
			_ = os.Remove(adminInit)
		}
		if err != nil {
			_ = cleanup(false)
			return nil, cleanup, err
		}
		paths[member.instance.ID] = path.Join(work, "disaster-rebuild.sh")
	}
	return paths, cleanup, nil
}

func writeDisasterAdminInitSQL(credential store.Credential) (string, error) {
	username, err := mysqlSQLString(credential.Username)
	if err != nil {
		return "", err
	}
	password, err := mysqlSQLString(credential.Secret["password"])
	if err != nil {
		return "", err
	}
	contents := fmt.Sprintf("ALTER USER 'root'@'localhost' IDENTIFIED BY '%s';\nCREATE USER IF NOT EXISTS '%s'@'%%' IDENTIFIED BY '%s';\nALTER USER '%s'@'%%' IDENTIFIED BY '%s';\nGRANT ALL PRIVILEGES ON *.* TO '%s'@'%%' WITH GRANT OPTION;\nFLUSH PRIVILEGES;\n", password, username, password, username, password, username)
	file, err := os.CreateTemp("", "aifar-mysql-admin-init-*.sql")
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err = file.Chmod(0o600); err == nil {
		_, err = file.WriteString(contents)
	}
	closeErr := file.Close()
	if err != nil || closeErr != nil {
		_ = os.Remove(name)
		return "", errors.Join(err, closeErr)
	}
	return name, nil
}

func mysqlSQLString(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.ContainsAny(value, "\x00\r\n") {
		return "", errors.New("invalid MySQL administrator secret")
	}
	value = strings.ReplaceAll(value, `\`, `\\`)
	return strings.ReplaceAll(value, `'`, `''`), nil
}

func (s Service) loadDisasterSeed(ctx context.Context, state *disasterExecutionState, work, taskID, language string) (retErr error) {
	if _, err := s.remote.Run(ctx, state.seed.server, bootstrapBackupWorkCommand(work)); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	secret, err := writeMySQLSecretContext(state.credentials[state.seed.instance.ID], instancePort(state.seed.instance))
	if err != nil {
		return localizedMySQLOperationError(language, MySQLCredentialUnavailable)
	}
	defer os.Remove(secret)
	if err := s.remote.UploadFile(ctx, state.seed.server, secret, path.Join(work, "secret-context.cnf"), 0o600); err != nil {
		return localizedMySQLOperationError(language, MySQLCredentialUnavailable)
	}
	if err := s.remote.UploadFile(ctx, state.seed.server, state.repositoryPath(state.backup), path.Join(work, "dump.tar"), 0o600); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	if _, err := s.remote.Run(ctx, state.seed.server, extractRestoreArchiveCommand(work)); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreManifestInvalid)
	}
	if _, err := s.remote.Run(ctx, state.seed.server, dryRunRestoreCommand(work)); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	session, sessionCleanup, err := s.localInfileSession(ctx, state.seed.instance, state.seed.server, state.credentials[state.seed.instance.ID])
	if err != nil {
		return err
	}
	defer sessionCleanup()
	guard := newLocalInfileGuard(session)
	if err := guard.Capture(ctx); err != nil {
		return err
	}
	if err := guard.Enable(ctx); err != nil {
		return err
	}
	defer func() {
		restoreCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if err := guard.Restore(restoreCtx); err != nil {
			retErr = errors.Join(retErr, localizedMySQLOperationError(language, MySQLLocalInfileRestoreFailed))
		}
	}()
	if err := updateRestorePhase(state.data, &state.backup, "schema_mutation_started", taskID, state.digest); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	script, err := RenderLogicalRestoreScript(LogicalRestoreScriptOptions{TaskID: taskID + "-disaster-seed", Threads: restoreThreads(map[string]any{"threads": defaultBackupThreads})})
	if err != nil {
		return err
	}
	localScript, err := installerkit.WriteTempScript("aifar-mysql-disaster-restore-*.sh", script)
	if err != nil {
		return err
	}
	defer os.Remove(localScript)
	remoteScript := path.Join(work, "logical-restore.sh")
	if err := s.remote.UploadFile(ctx, state.seed.server, localScript, remoteScript, 0o700); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	if _, err := s.remote.Run(ctx, state.seed.server, "sh "+installerkit.ShellQuote(remoteScript)); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	state.progress.SeedStage = "loaded"
	if err := updateRestorePhase(state.data, &state.backup, "load_complete", taskID, state.digest); err != nil {
		return localizedMySQLOperationError(language, MySQLRestoreIncomplete)
	}
	return saveDisasterProgress(state.data, &state.backup, state.progress)
}

func (state *disasterExecutionState) repositoryPath(backup store.AppBackup) string {
	return backup.Path
}

var controlledClusterName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

func disasterCreateClusterCommand(work string, seed clusterMemberNode, clusterName string) (string, error) {
	if !controlledClusterName.MatchString(clusterName) || strings.TrimSpace(seed.server.Host) == "" || strings.TrimSpace(instanceRootUser(seed.instance)) == "" {
		return "", errors.New("invalid controlled cluster identity")
	}
	nameJSON, _ := json.Marshal(clusterName)
	userJSON, _ := json.Marshal(instanceRootUser(seed.instance))
	js := "print('__AIFAR_CREATE_CLUSTER__'); shell.connect({scheme:'mysql',host:'127.0.0.1',port:" + fmt.Sprint(instancePort(seed.instance)) + ",user:" + string(userJSON) + "}); dba.createCluster(" + string(nameJSON) + ");"
	return mysqlShellJSCommand(work, instancePort(seed.instance), js), nil
}

func disasterCloneMemberCommand(work string, seed, member clusterMemberNode) (string, error) {
	if strings.TrimSpace(member.server.Host) == "" || strings.TrimSpace(instanceRootUser(member.instance)) == "" || member.instance.ID == seed.instance.ID {
		return "", errors.New("invalid controlled clone member")
	}
	hostJSON, _ := json.Marshal(member.server.Host)
	userJSON, _ := json.Marshal(instanceRootUser(member.instance))
	nameJSON, _ := json.Marshal(clusterNameFromInstance(seed.instance))
	js := "print('__AIFAR_CLONE_MEMBER__ " + member.instance.ID + "'); const cluster=dba.getCluster(" + string(nameJSON) + "); cluster.addInstance({scheme:'mysql',host:" + string(hostJSON) + ",port:" + fmt.Sprint(instancePort(member.instance)) + ",user:" + string(userJSON) + `}, {recoveryMethod: "clone", interactive:false});`
	return mysqlShellJSCommand(work, instancePort(seed.instance), js), nil
}

func mysqlShellJSCommand(work string, port int, js string) string {
	mysqlsh := path.Join(mysqlInstallRoot, "mysql-shell", "bin", "mysqlsh")
	return "set -eu; test -x " + installerkit.ShellQuote(mysqlsh) + "; " + installerkit.ShellQuote(mysqlsh) + " --defaults-file=" + installerkit.ShellQuote(path.Join(work, "secret-context.cnf")) + " --js --host=127.0.0.1 --port=" + fmt.Sprint(port) + " --execute " + installerkit.ShellQuote(js)
}

func (s Service) bootstrapDisasterRouter(ctx context.Context, server store.Server, router RouterRef, seed clusterMemberNode, credential store.Credential, taskID string) (retErr error) {
	work := path.Join(remoteInstallRoot(server, "mysql", seed.instance.Version), "_disaster", taskID+"-router")
	if _, err := s.remote.Run(ctx, server, "set -eu; mkdir -p "+installerkit.ShellQuote(work)); err != nil {
		return err
	}
	passwordFile, err := os.CreateTemp("", "aifar-mysql-router-secret-*")
	if err != nil {
		return err
	}
	passwordPath := passwordFile.Name()
	defer os.Remove(passwordPath)
	if err = passwordFile.Chmod(0o600); err == nil {
		_, err = passwordFile.WriteString(credential.Secret["password"])
	}
	closeErr := passwordFile.Close()
	if err != nil || closeErr != nil {
		return errors.Join(err, closeErr)
	}
	remotePassword := path.Join(work, "router-password")
	if err := s.remote.UploadFile(ctx, server, passwordPath, remotePassword, 0o600); err != nil {
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
		defer cancel()
		if _, cleanupErr := s.remote.Run(cleanupCtx, server, "rm -f -- "+installerkit.ShellQuote(remotePassword)); cleanupErr != nil {
			retErr = errors.Join(retErr, cleanupErr)
		}
	}()
	installRoot := remoteInstallRoot(server, "mysql", seed.instance.Version)
	bootstrapURI := strings.TrimSpace(credential.Username) + "@" + seed.server.Host + ":" + fmt.Sprint(instancePort(seed.instance))
	command := "set -eu; echo __AIFAR_ROUTER_BOOTSTRAP__ >/dev/null; cat " + installerkit.ShellQuote(remotePassword) + " | " + installerkit.ShellQuote(path.Join(installRoot, "mysql-router", "bin", "mysqlrouter")) + " --bootstrap " + installerkit.ShellQuote(bootstrapURI) + " --directory " + installerkit.ShellQuote(path.Join(installRoot, "router")) + " --conf-base-port " + fmt.Sprint(routerPortForEndpoint(router.Endpoint)) + " --force --user aifar-router; systemctl start aifar-mysql-router"
	_, retErr = s.remote.Run(ctx, server, command)
	return retErr
}

type DisasterRebuildScriptOptions struct {
	TaskID        string
	InstallRoot   string
	WorkDir       string
	DataDir       string
	QuarantineDir string
	Port          int
}

func validateDisasterRebuildScriptOptions(options DisasterRebuildScriptOptions) error {
	installRoot := path.Clean(options.InstallRoot)
	workDir := path.Clean(options.WorkDir)
	dataDir := path.Clean(options.DataDir)
	quarantine := path.Clean(options.QuarantineDir)
	workBase := path.Join(installRoot, "_disaster", options.TaskID)
	validWorkDir := workDir == workBase
	if strings.HasPrefix(workDir, workBase+"-") {
		validWorkDir = validManifestStoreID(strings.TrimPrefix(workDir, workBase+"-"), "app_")
	}
	if !validLogicalTaskID(options.TaskID) || !strings.HasPrefix(installRoot, "/") || installRoot == "/" ||
		!validWorkDir || dataDir != path.Join(installRoot, "data") || quarantine != dataDir+".quarantine-"+options.TaskID ||
		options.Port < 1 || options.Port > 65535 {
		return errors.New("invalid controlled MySQL disaster rebuild script options")
	}
	return nil
}
