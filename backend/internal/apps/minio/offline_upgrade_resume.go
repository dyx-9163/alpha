package minio

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

type offlineSiloState struct {
	runtime                                                                          string
	stopAttempted, backupComplete, switchAttempted, startAttempted, restoreAttempted bool
}

func parseOfflineSiloState(output string) (offlineSiloState, error) {
	fields := strings.Split(strings.TrimSpace(output), "\t")
	invalid := errors.New("invalid remote Silo upgrade state")
	if len(fields) != 7 || fields[0] != "AIFAR_SILO_STATE" {
		return offlineSiloState{}, invalid
	}
	for _, flag := range fields[2:] {
		if flag != "0" && flag != "1" {
			return offlineSiloState{}, invalid
		}
	}
	state := offlineSiloState{
		runtime: fields[1], stopAttempted: fields[2] == "1", backupComplete: fields[3] == "1",
		switchAttempted: fields[4] == "1", startAttempted: fields[5] == "1", restoreAttempted: fields[6] == "1",
	}
	switch state.runtime {
	case "old-running", "old-stopped", "silo-running", "silo-stopped":
	default:
		return offlineSiloState{}, invalid
	}
	if (state.backupComplete && !state.stopAttempted) ||
		(state.switchAttempted && !state.backupComplete) ||
		(state.startAttempted && (!state.switchAttempted || state.restoreAttempted || strings.HasPrefix(state.runtime, "old-"))) ||
		(state.runtime == "silo-running" && !state.startAttempted) ||
		(state.runtime == "silo-stopped" && !state.switchAttempted) {
		return offlineSiloState{}, invalid
	}
	return state, nil
}

func (state offlineSiloState) skip(phase string) bool {
	switch phase {
	case "stage", "before":
		return true
	case "stop":
		return state.runtime != "old-running"
	case "backup":
		return state.backupComplete
	case "switch":
		return state.runtime == "silo-running"
	}
	return false
}

func validateSiloResumeBarrier(states []offlineSiloState) error {
	for i, state := range states {
		peer := states[1-i]
		if (state.runtime == "old-running" && state.backupComplete) ||
			(state.switchAttempted && !peer.backupComplete) ||
			(state.startAttempted && (!peer.switchAttempted || strings.HasPrefix(peer.runtime, "old-"))) ||
			(state.runtime == "old-stopped" && !state.stopAttempted) {
			return errors.New("Silo resume state does not match the two-site stop, backup, and cutover order")
		}
	}
	return nil
}

func (s Service) loadOfflineSiloNodes(group []store.AppInstance, operationID string, resuming bool) ([]offlineSiloNode, error) {
	if len(group) != 2 || !siloTaskIDPattern.MatchString(operationID) {
		return nil, errors.New("invalid Silo upgrade task or two-member group")
	}
	nodes := make([]offlineSiloNode, 0, len(group))
	for _, instance := range group {
		meta, err := parseOfflineSiloMember(instance, resuming)
		if err != nil {
			return nil, err
		}
		server, err := s.store.GetServer(instance.ServerID, true)
		if err != nil || server.ID != instance.ServerID || minioEndpoint(server.Host, meta.APIPort) != meta.Endpoint {
			return nil, errors.New("could not match a MinIO member to its recorded SSH host and endpoint")
		}
		root := remoteInstallRoot(server, "minio", offlineMinioVersion)
		backup := path.Join(path.Dir(root), "_backups", "minio-silo", operationID)
		if !validSiloPath(root) || !validSiloPath(backup) {
			return nil, errors.New("unsupported MinIO install or backup path")
		}
		for _, volume := range meta.DataDirs {
			if siloPathsOverlap(volume, backup) || siloPathsOverlap(volume, path.Join(root, "bin")) ||
				siloPathsOverlap(volume, path.Join(root, "conf")) {
				return nil, errors.New("MinIO data paths overlap the backup, configuration, or program files")
			}
		}
		nodes = append(nodes, offlineSiloNode{instance: instance, server: server, meta: meta, root: root, backup: backup})
	}
	if strings.EqualFold(nodes[0].server.Host, nodes[1].server.Host) {
		return nil, errors.New("MinIO replication members must run on separate SSH hosts")
	}
	return nodes, nil
}

func siloCheckpointNode(node offlineSiloNode) store.SiloOfflineUpgradeNode {
	layout, _ := json.Marshal(node.meta)
	return store.SiloOfflineUpgradeNode{
		InstanceID: node.instance.ID, ServerID: node.server.ID, SSHPort: node.server.Port,
		Root: node.root, Endpoint: node.meta.Endpoint, LayoutSHA256: fmt.Sprintf("%x", sha256.Sum256(layout)),
	}
}

func newSiloCheckpoint(operationID string, nodes []offlineSiloNode) store.SiloOfflineUpgradeCheckpoint {
	checkpoint := store.SiloOfflineUpgradeCheckpoint{TaskID: operationID}
	for _, node := range nodes {
		saved := siloCheckpointNode(node)
		saved.Before = slices.Clone(node.before)
		checkpoint.Nodes = append(checkpoint.Nodes, saved)
	}
	return checkpoint
}

func matchSiloCheckpoint(nodes []offlineSiloNode, checkpoint store.SiloOfflineUpgradeCheckpoint) error {
	if len(checkpoint.Nodes) != len(nodes) {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	for i := range nodes {
		expected := siloCheckpointNode(nodes[i])
		index := slices.IndexFunc(checkpoint.Nodes, func(saved store.SiloOfflineUpgradeNode) bool {
			return saved.InstanceID == expected.InstanceID && saved.ServerID == expected.ServerID &&
				saved.SSHPort == expected.SSHPort && saved.Root == expected.Root &&
				saved.Endpoint == expected.Endpoint && saved.LayoutSHA256 == expected.LayoutSHA256
		})
		if index < 0 {
			return store.ErrSiloOfflineUpgradeCheckpointConflict
		}
		nodes[i].before = slices.Clone(checkpoint.Nodes[index].Before)
	}
	if !reciprocalSiloBaselines(nodes) {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	return nil
}

func reciprocalSiloBaselines(nodes []offlineSiloNode) bool {
	first, second := nodes[0].before, nodes[1].before
	return len(first) >= 2 && len(second) >= 2 && first[0] != "" && second[0] != "" &&
		first[0] != second[0] && first[0] == second[1] && first[1] == second[0]
}

func (m Module) CheckOfflineUpgradeResumeIdle(ctx context.Context, seed store.AppInstance, checkpointTaskID string, run registry.RunContext) error {
	data, ok := m.service.store.(offlineSiloCheckpointReader)
	if !ok {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	group, err := m.service.ResolveOfflineUpgradeGroup(seed)
	if err != nil {
		return err
	}
	var metadata offlineSiloMetadata
	if json.Unmarshal([]byte(seed.Metadata), &metadata) != nil {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	checkpoint, err := data.GetSiloOfflineUpgradeCheckpoint(metadata.GroupID)
	if err != nil {
		return err
	}
	if checkpoint.TaskID != checkpointTaskID {
		return store.ErrSiloOfflineUpgradeCheckpointConflict
	}
	nodes, err := m.service.loadOfflineSiloNodes(group, checkpointTaskID, true)
	if err != nil {
		return err
	}
	if err := matchSiloCheckpoint(nodes, checkpoint); err != nil {
		return err
	}
	deadline, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	return siloParallel(nodes, func(_ int, node offlineSiloNode) error {
		_, err := m.service.runOfflineSiloCommand(deadline, node, "idle", run.Language)
		return err
	})
}

func (s Service) readOfflineSiloStates(ctx context.Context, nodes []offlineSiloNode, language string) ([]offlineSiloState, error) {
	states := make([]offlineSiloState, len(nodes))
	err := siloParallel(nodes, func(i int, node offlineSiloNode) error {
		raw, err := s.runOfflineSiloCommand(ctx, node, "state", language)
		if err != nil {
			return err
		}
		state, err := parseOfflineSiloState(raw)
		if err != nil {
			return fmt.Errorf("%s: %w", node.server.ID, err)
		}
		states[i] = state
		return nil
	})
	return states, err
}

// Each node gets the whole phase budget, even if the other host is unreachable.
func siloParallel(nodes []offlineSiloNode, f func(int, offlineSiloNode) error) error {
	var workers sync.WaitGroup
	failures := make([]error, len(nodes))
	for i, node := range nodes {
		workers.Add(1)
		go func() {
			defer workers.Done()
			failures[i] = f(i, node)
		}()
	}
	workers.Wait()
	return errors.Join(failures...)
}

func (s Service) recoverOfflineSiloGroup(ctx context.Context, data offlineUpgradeDataStore, groupID, operationID string, nodes []offlineSiloNode, language string) error {
	states, stateErr := s.readOfflineSiloStates(ctx, nodes, language)
	if stateErr != nil || slices.ContainsFunc(states, func(state offlineSiloState) bool { return state.startAttempted }) {
		// Do not downgrade storage when either site may have been opened by
		// Silo. Stop only proven target services; an untouched MinIO stays up.
		stopErr := siloParallel(nodes, func(i int, node offlineSiloNode) error {
			if !strings.HasPrefix(states[i].runtime, "silo-") {
				return nil
			}
			_, err := s.runOfflineSiloCommand(ctx, node, "stop-for-recovery", language)
			return err
		})
		return errors.Join(stateErr, stopErr, errors.New(siloText(language,
			"两端状态不明确或 Silo 可能已访问存储。保持业务停写，并保留原断点和两份冷备。",
			"A site could not be verified or Silo may have touched the volumes. Keep business writes frozen and retain the original checkpoint and both cold backups.")))
	}
	needsRestore := func(i int) bool {
		return states[i].runtime == "silo-stopped" || (states[i].runtime == "old-stopped" && states[i].stopAttempted)
	}
	if slices.ContainsFunc(states, func(state offlineSiloState) bool {
		return state.runtime == "old-stopped" && !state.stopAttempted
	}) {
		return errors.New(siloText(language, "发现并非由本任务停止的 MinIO，请核对现场后再恢复", "A stopped MinIO instance was not stopped by this task. Inspect it before recovery."))
	}
	for _, phase := range []string{"restore-minio", "start-old"} {
		if err := siloParallel(nodes, func(i int, node offlineSiloNode) error {
			if !needsRestore(i) {
				return nil
			}
			_, err := s.runOfflineSiloCommand(ctx, node, phase, language)
			return err
		}); err != nil {
			return errors.Join(err, errors.New(siloText(language,
				"原 MinIO 未完整恢复，保持停写并保留原断点。",
				"MinIO did not recover on both sites. Keep writes frozen and retain the original checkpoint.")))
		}
	}
	if err := siloParallel(nodes, func(_ int, node offlineSiloNode) error {
		_, err := s.runOfflineSiloCommand(ctx, node, "verify-old", language)
		return err
	}); err != nil {
		return errors.Join(err, errors.New(siloText(language,
			"原 MinIO 尚未通过两端运行验证，保留原断点。",
			"The original MinIO services have not passed verification on both sites. The original checkpoint was retained.")))
	}
	if err := data.DiscardSiloOfflineUpgradeCheckpoint(groupID, operationID); err != nil {
		return err
	}
	return errors.New(siloText(language, "两端为原 MinIO 状态，原迁移断点已关闭，可重新发起迁移。",
		"Both sites are back on the original MinIO state. The old checkpoint was closed; a fresh migration can be submitted."))
}
