package mysql

import (
	"context"
	"errors"
	"slices"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/i18n"
	"aifar-deployment/backend/internal/installer/installerkit"
	"aifar-deployment/backend/internal/store"
)

func (u *offlineUpgrade) resuming() bool { return u.resume }

func (u *offlineUpgrade) operationID() string {
	if u.checkpoint.TaskID != "" {
		return u.checkpoint.TaskID
	}
	return u.run.TaskID
}

func (u *offlineUpgrade) allServersUpgraded() bool {
	for i := range u.nodes {
		if u.versions[i] != OfflineUpgradeTargetVersion {
			return false
		}
	}
	return true
}

func (node upgradeNode) checkpointIdentity() store.MySQLOfflineUpgradeNode {
	return store.MySQLOfflineUpgradeNode{
		InstanceID: node.instance.ID, ServerID: node.server.ID,
		Root: node.root, Endpoint: node.endpoint, SSHPort: node.server.Port,
	}
}

func (u *offlineUpgrade) createCheckpoint() error {
	checkpoint := store.MySQLOfflineUpgradeCheckpoint{
		TaskID: u.run.TaskID, BundleSHA256: u.bundle.ArchiveSHA256,
	}
	for i, node := range slices.Concat(u.nodes, u.routers) {
		saved := node.checkpointIdentity()
		if i < len(u.nodes) {
			view := u.baseline[i]
			saved.UUID, saved.Group, saved.GTID = view.uuid, view.group, view.gtid
		}
		checkpoint.Nodes = append(checkpoint.Nodes, saved)
	}
	if err := u.data.CreateMySQLOfflineUpgradeCheckpoint(OfflineUpgradeClusterID(u.seed), checkpoint); err != nil {
		return errors.New(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlCheckpointWrite"))
	}
	u.checkpoint = checkpoint
	return nil
}

func (u *offlineUpgrade) matchCheckpoint() error {
	invalid := errors.New(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlCheckpointInvalid"))
	if len(u.checkpoint.Nodes) != len(u.nodes)+len(u.routers) {
		return invalid
	}
	byID := make(map[string]store.MySQLOfflineUpgradeNode, len(u.checkpoint.Nodes))
	for _, saved := range u.checkpoint.Nodes {
		if saved.InstanceID == "" || byID[saved.InstanceID].InstanceID != "" {
			return invalid
		}
		byID[saved.InstanceID] = saved
	}
	u.baseline = make([]upgradeView, len(u.nodes))
	for i, node := range slices.Concat(u.nodes, u.routers) {
		saved := byID[node.instance.ID]
		identity := saved
		identity.UUID, identity.Group, identity.GTID = "", "", ""
		if identity != node.checkpointIdentity() {
			return invalid
		}
		if i < len(u.nodes) {
			if !uuidPattern.MatchString(saved.UUID) || !uuidPattern.MatchString(saved.Group) || !upgradeGTIDPattern.MatchString(saved.GTID) {
				return invalid
			}
			u.baseline[i] = upgradeView{uuid: saved.UUID, group: saved.Group, gtid: saved.GTID, endpoint: saved.Endpoint}
		}
	}
	return nil
}

// CheckOfflineUpgradeResumeIdle runs before a replacement worker takes over
// the interrupted worker's Panel locks. Each mutating phase holds the same
// exclusive directory lock from entry through its recovery trap.
func (m Module) CheckOfflineUpgradeResumeIdle(ctx context.Context, seed store.AppInstance, checkpointTaskID string, run registry.RunContext) error {
	data, ok := m.service.store.(offlineUpgradeStore)
	invalid := errors.New(i18n.MiddlewareUpgradeText(run.Language, "mysqlCheckpointInvalid"))
	if !ok || !validLogicalTaskID(checkpointTaskID) || OfflineUpgradeClusterID(seed) == "" {
		return invalid
	}
	u := offlineUpgrade{service: m.service, data: data, run: run, seed: seed, versions: map[int]string{}}
	if err := u.loadUpgradeTopology(); err != nil {
		return err
	}
	if u.checkpoint.TaskID != checkpointTaskID {
		return invalid
	}
	if err := u.matchCheckpoint(); err != nil {
		return err
	}

	deadline, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for _, node := range slices.Concat(u.nodes, u.routers) {
		root := installerkit.ShellQuote(node.root)
		command := "set -eu; test \"$(realpath -e -- " + root + ")\" = " + root + "; flock -n -- " + root + " true"
		probeCtx, done := context.WithTimeout(deadline, 6*time.Second)
		_, err := u.remote(probeCtx, node, "resume-idle-check", command, false)
		done()
		if err != nil {
			return errors.Join(errors.New(i18n.MiddlewareUpgradeText(run.Language, "mysqlResumeOldPhaseNotIdle")), err)
		}
	}
	return nil
}

func (u *offlineUpgrade) resumeNodes(ctx context.Context) error {
	log := u.run.LoggerForTarget(OfflineUpgradeClusterID(u.seed))
	log.Info(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlCheckpointResuming"), u.operationID())
	for _, node := range u.nodes {
		if _, err := u.script(ctx, node, "resume-server-check"); err != nil {
			return err
		}
	}
	views, known, err := u.readViews(ctx)
	if err != nil {
		return err
	}
	for i, view := range views {
		recorded := u.nodes[i].instance.Version
		if (view.version != OfflineUpgradeSourceVersion && view.version != OfflineUpgradeTargetVersion) ||
			(recorded == OfflineUpgradeTargetVersion && view.version != OfflineUpgradeTargetVersion) {
			return u.phaseError(u.nodes[i], "resume-server-version")
		}
		u.versions[i] = view.version
	}
	if _, err := validateUpgradeViews(views, u.versions, known); err != nil {
		return errors.New(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlCheckpointInvalid"))
	}
	return nil
}

func (u *offlineUpgrade) completeCheckpoint() error {
	group := OfflineUpgradeClusterID(u.seed)
	updates := make([]store.AppInstanceVersionUpdate, 0, len(u.nodes)+len(u.routers))
	for _, node := range slices.Concat(u.nodes, u.routers) {
		current, err := u.data.GetAppInstance(node.instance.ID)
		if err != nil || current.App != node.instance.App || current.ServerID != node.instance.ServerID ||
			OfflineUpgradeClusterID(current) != group || current.Version != OfflineUpgradeTargetVersion ||
			appMetadata(current)["offlineUpgradeTaskId"] != u.operationID() {
			return errors.New(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlCheckpointChanged"))
		}
		updates = append(updates, store.AppInstanceVersionUpdate{
			Expected: current, Version: OfflineUpgradeTargetVersion,
			Metadata: map[string]any{"offlineUpgradePending": false},
		})
	}
	if err := u.data.CompleteMySQLOfflineUpgrade(group, u.operationID(), updates); err != nil {
		return errors.New(i18n.MiddlewareUpgradeText(u.run.Language, "mysqlCheckpointWrite"))
	}
	return nil
}
