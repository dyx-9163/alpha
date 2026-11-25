package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"

	"github.com/go-chi/chi/v5"
)

type offlineUpgrader interface {
	RunOfflineUpgrade(context.Context, store.AppInstance, []store.Resource, registry.RunContext) error
}

type offlineUpgradePlanner interface {
	PlanOfflineUpgrade(store.AppInstance, []store.AppInstance, string) []registry.InstallStepPlan
}

type offlineUpgradeGroupResolver interface {
	ResolveOfflineUpgradeGroup(store.AppInstance) ([]store.AppInstance, error)
}

type middlewareUpgradePath struct {
	from, to, topology, resourceApp string
	members                         int
	artifacts                       []string
}

var middlewareUpgradePaths = map[string]middlewareUpgradePath{
	"mysql": {
		from: "8.0.36", to: "8.4.13", topology: "innodb-cluster",
		resourceApp: "mysql", members: 3,
		artifacts: []string{"mysql-aifar-8.4.13-official-bundle.tar"},
	},
	"minio": {
		from: "2025-10-15T17-29-55Z", to: "20261112", topology: "bucket-replication",
		resourceApp: "silo", members: 2,
		artifacts: []string{"silo_20261112000000.0.0_linux_amd64.tar.gz", "mcli_20261112000000.0.0_linux_amd64.tar.gz"},
	},
}

func (a *API) startMiddlewareUpgrade(w http.ResponseWriter, r *http.Request) {
	lang := languageFromRequest(r)
	var req struct {
		TargetVersion string `json:"targetVersion"`
		Confirmed     bool   `json:"confirmed"`
	}
	if !decode(w, r, &req) {
		return
	}
	if !req.Confirmed {
		writeError(w, http.StatusBadRequest, "UPGRADE_CONFIRMATION_REQUIRED", i18n.MiddlewareUpgradeText(lang, "confirm"), nil)
		return
	}
	instance, err := a.store.GetAppInstance(chi.URLParam(r, "id"))
	if err != nil {
		respond(w, instance, err)
		return
	}
	spec, found := middlewareUpgradePaths[instance.App]
	if !found || req.TargetVersion != spec.to || !a.middlewareUpgradeVersionAllowed(instance, spec) || instance.Topology != spec.topology {
		writeError(w, http.StatusConflict, "UPGRADE_PATH_UNSUPPORTED", i18n.MiddlewareUpgradeText(lang, "path"), nil)
		return
	}
	group, peers, err := a.middlewareUpgradePeers(instance, spec)
	if err != nil {
		writeError(w, http.StatusConflict, "UPGRADE_GROUP_INVALID", i18n.MiddlewareUpgradeText(lang, "group"), nil)
		return
	}
	if instance.App == "mysql" {
		if code := a.mysqlOrdinaryLifecycleGate(instance); code != "" {
			writeError(w, http.StatusConflict, code, i18n.MySQLBackupErrorText(lang, code), nil)
			return
		}
	}
	resources, err := a.store.ListResources()
	if err != nil {
		respondTask(w, store.Task{}, err)
		return
	}
	if !spec.hasResources(resources) {
		writeError(w, http.StatusConflict, "UPGRADE_RESOURCES_MISSING", i18n.MiddlewareUpgradeText(lang, "resources"), nil)
		return
	}
	module, _ := a.apps.Get(instance.App)
	upgrader, ok := module.(offlineUpgrader)
	if !ok {
		writeError(w, http.StatusConflict, "UPGRADE_MODULE_UNAVAILABLE", i18n.MiddlewareUpgradeText(lang, "unavailable"), nil)
		return
	}
	if resolver, ok := module.(offlineUpgradeGroupResolver); ok {
		resolved, err := resolver.ResolveOfflineUpgradeGroup(instance)
		if err != nil || !sameMiddlewareUpgradePeers(peers, resolved) {
			writeError(w, http.StatusConflict, "UPGRADE_GROUP_INVALID", i18n.MiddlewareUpgradeText(lang, "group"), nil)
			return
		}
	}
	participants, err := a.middlewareUpgradeParticipants(instance.App, group, peers)
	if err != nil {
		respondTask(w, store.Task{}, err)
		return
	}

	actor := currentUser(r).Username
	taskType := "apps." + instance.App + ".upgrade"
	task, err := a.store.CreateTask(store.Task{Type: taskType, Target: group, Status: "pending", CreatedBy: actor})
	if err != nil {
		respondTask(w, task, err)
		return
	}
	title := i18n.MiddlewareUpgradeText(lang, "execute")
	var plan []registry.InstallStepPlan
	if planner, ok := module.(offlineUpgradePlanner); ok {
		plan = planner.PlanOfflineUpgrade(instance, peers, lang)
	}
	fallbackPlan := len(plan) == 0
	if fallbackPlan {
		plan = []registry.InstallStepPlan{{Target: group, Name: "upgrade", Title: title, Order: 1}}
	}
	if err := a.storeInstallPlanOrDelete(task.ID, plan); err != nil {
		respondTask(w, task, err)
		return
	}
	specs := appInstanceOperationLockSpecs("middleware-upgrade", participants)
	targets := make([]string, 0, len(peers))
	for _, peer := range peers {
		targets = append(targets, peer.ServerID)
	}
	specs = append(specs, appInstallOperationLockSpecs(instance.App, targets)...)
	if instance.App == "mysql" {
		specs = append(mysqlClusterOperationLockSpecs("mysql-upgrade", instance), specs...)
		for _, participant := range participants {
			specs = append(specs, serverConfigOperationLockSpec("mysql-upgrade", participant.ServerID))
			if participant.App == "mysql-router" {
				specs = append(specs, appInstallOperationLockSpecs(participant.App, []string{participant.ServerID})...)
			}
		}
	}
	locks, acquired := a.acquireTaskOperationLocks(w, lang, task, specs)
	if !acquired {
		return
	}
	task, err = a.tasks.StartExistingWithLanguage(task, lang, func(ctx context.Context, log worker.Logger) error {
		fresh, err := a.store.GetAppInstance(instance.ID)
		if err != nil {
			return err
		}
		freshGroup, freshPeers, err := a.middlewareUpgradePeers(fresh, spec)
		if err != nil || freshGroup != group || !sameMiddlewareUpgradePeers(peers, freshPeers) {
			return errors.New(i18n.MiddlewareUpgradeText(lang, "group"))
		}
		if fresh.App == "mysql" {
			freshParticipants, err := a.middlewareUpgradeParticipants(fresh.App, group, freshPeers)
			if err != nil || !sameMiddlewareUpgradePeers(participants, freshParticipants) {
				return errors.New(i18n.MiddlewareUpgradeText(lang, "group"))
			}
			if code := a.mysqlOrdinaryLifecycleGate(fresh); code != "" {
				return errors.New(i18n.MySQLBackupErrorText(lang, code))
			}
		}
		if !log.TryEnterCommit() {
			return context.Canceled
		}
		if fallbackPlan {
			log.StartTarget(group)
			log.StartStep(group, "upgrade", title, 1)
		}
		err = upgrader.RunOfflineUpgrade(ctx, fresh, resources, registry.RunContext{
			TaskID: log.TaskID(), Language: lang, Log: log,
			Resources: resources, TargetLog: func(target string) registry.Logger { return log.Target(target) },
		})
		if !fallbackPlan {
			return err
		}
		if err != nil {
			log.FinishStep(group, "upgrade", "failed", err.Error())
			log.FinishTarget(group, "failed", err.Error())
			return err
		}
		log.FinishStep(group, "upgrade", "success", "")
		log.FinishTarget(group, "success", "")
		return nil
	})
	if err != nil {
		a.releaseOperationLocks(locks)
	} else {
		a.audit(r, taskType, instance.ID, "running", task.ID)
	}
	respondTask(w, task, err)
}

func (p middlewareUpgradePath) hasResources(resources []store.Resource) bool {
	found := make(map[string]bool)
	for _, resource := range resources {
		if resource.App == p.resourceApp && resource.Version == p.to && resource.Part == "backend" {
			found[filepath.Base(resource.Path)] = true
		}
	}
	for _, artifact := range p.artifacts {
		if !found[artifact] {
			return false
		}
	}
	return true
}

func (a *API) middlewareUpgradeParticipants(app, group string, peers []store.AppInstance) ([]store.AppInstance, error) {
	if app != "mysql" {
		return peers, nil
	}
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return nil, err
	}
	participants := append([]store.AppInstance(nil), peers...)
	for _, candidate := range instances {
		if candidate.App == "mysql-router" && mysqlClusterID(candidate) == group {
			participants = append(participants, candidate)
		}
	}
	return participants, nil
}

func (a *API) middlewareUpgradeVersionAllowed(instance store.AppInstance, spec middlewareUpgradePath) bool {
	if instance.Version == spec.from {
		return true
	}
	if instance.App != "mysql" || instance.Version != spec.to {
		return false
	}
	_, err := a.store.GetMySQLOfflineUpgradeCheckpoint(middlewareUpgradeGroup(instance))
	return err == nil
}

func (a *API) middlewareUpgradePeers(seed store.AppInstance, spec middlewareUpgradePath) (string, []store.AppInstance, error) {
	group := middlewareUpgradeGroup(seed)
	if group == "" || !a.middlewareUpgradeVersionAllowed(seed, spec) || seed.Topology != spec.topology {
		return "", nil, errors.New("invalid middleware upgrade source")
	}
	instances, err := a.store.ListAppInstances()
	if err != nil {
		return "", nil, err
	}
	resume := false
	if seed.App == "mysql" {
		_, checkpointErr := a.store.GetMySQLOfflineUpgradeCheckpoint(group)
		if checkpointErr != nil && !errors.Is(checkpointErr, sql.ErrNoRows) {
			return "", nil, checkpointErr
		}
		resume = checkpointErr == nil
	}
	peers := make([]store.AppInstance, 0, spec.members)
	servers := make(map[string]bool)
	for _, instance := range instances {
		if instance.App != seed.App || middlewareUpgradeGroup(instance) != group {
			continue
		}
		if (instance.Version != spec.from && !(resume && instance.Version == spec.to)) || instance.Topology != spec.topology || instance.ServerID == "" || servers[instance.ServerID] {
			return "", nil, errors.New("middleware upgrade group has incompatible members")
		}
		servers[instance.ServerID] = true
		peers = append(peers, instance)
	}
	if len(peers) != spec.members {
		return "", nil, errors.New("middleware upgrade group has unexpected member count")
	}
	return group, peers, nil
}

func middlewareUpgradeGroup(instance store.AppInstance) string {
	if instance.App == "mysql" {
		group := mysqlClusterID(instance)
		if validMySQLMaintenanceClusterID(group) {
			return group
		}
		return ""
	}
	var metadata struct {
		ReplicationGroupID string `json:"replicationGroupId"`
	}
	if json.Unmarshal([]byte(instance.Metadata), &metadata) != nil {
		return ""
	}
	return metadata.ReplicationGroupID
}

func sameMiddlewareUpgradePeers(expected, actual []store.AppInstance) bool {
	byID := make(map[string]string, len(expected))
	for _, peer := range expected {
		byID[peer.ID] = peer.ServerID
	}
	for _, peer := range actual {
		if byID[peer.ID] != peer.ServerID {
			return false
		}
	}
	return len(expected) == len(actual)
}
