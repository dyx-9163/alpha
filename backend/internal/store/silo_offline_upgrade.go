package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var ErrSiloOfflineUpgradeCheckpointConflict = errors.New("Silo offline upgrade checkpoint changed")

type SiloOfflineUpgradeCheckpoint struct {
	TaskID string
	Nodes  []SiloOfflineUpgradeNode
}

type SiloOfflineUpgradeNode struct {
	InstanceID, ServerID, Root, Endpoint, LayoutSHA256 string
	SSHPort                                            int
	Before                                             []string
}

func (s *Store) GetSiloOfflineUpgradeCheckpoint(groupID string) (SiloOfflineUpgradeCheckpoint, error) {
	var checkpoint SiloOfflineUpgradeCheckpoint
	var nodes string
	err := s.db.QueryRow(`select task_id,nodes from silo_offline_upgrade_checkpoints where group_id=?`, groupID).
		Scan(&checkpoint.TaskID, &nodes)
	if err != nil {
		return SiloOfflineUpgradeCheckpoint{}, err
	}
	if err := json.Unmarshal([]byte(nodes), &checkpoint.Nodes); err != nil {
		return SiloOfflineUpgradeCheckpoint{}, err
	}
	return checkpoint, nil
}

func (s *Store) CreateSiloOfflineUpgradeCheckpoint(groupID string, checkpoint SiloOfflineUpgradeCheckpoint) error {
	nodes, err := json.Marshal(checkpoint.Nodes)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`insert into silo_offline_upgrade_checkpoints(group_id,task_id,nodes)
		values(?,?,?) on conflict(group_id) do nothing`, groupID, checkpoint.TaskID, string(nodes))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrSiloOfflineUpgradeCheckpointConflict
	}
	return nil
}

func (s *Store) DiscardSiloOfflineUpgradeCheckpoint(groupID, taskID string) error {
	return s.finishSiloOfflineUpgradeCheckpoint(groupID, taskID, nil)
}

func (s *Store) CompleteSiloOfflineUpgrade(groupID, taskID string, updates []AppInstanceVersionUpdate) error {
	if len(updates) != 2 || updates[0].Expected.ID == updates[1].Expected.ID ||
		updates[0].Expected.ServerID == updates[1].Expected.ServerID {
		return fmt.Errorf("%w: two distinct MinIO instances are required", ErrSiloOfflineUpgradeCheckpointConflict)
	}
	return s.finishSiloOfflineUpgradeCheckpoint(groupID, taskID, updates)
}

func (s *Store) finishSiloOfflineUpgradeCheckpoint(groupID, taskID string, updates []AppInstanceVersionUpdate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var nodesJSON string
	err = tx.QueryRow(`delete from silo_offline_upgrade_checkpoints where group_id=? and task_id=? returning nodes`, groupID, taskID).Scan(&nodesJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrSiloOfflineUpgradeCheckpointConflict
	}
	if err != nil {
		return err
	}
	if updates != nil {
		var nodes []SiloOfflineUpgradeNode
		if err := json.Unmarshal([]byte(nodesJSON), &nodes); err != nil || len(nodes) != len(updates) {
			return ErrSiloOfflineUpgradeCheckpointConflict
		}
		expected := map[string]string{}
		for _, node := range nodes {
			expected[node.InstanceID] = node.ServerID
		}
		for _, update := range updates {
			if update.Expected.App != "minio" || update.Expected.ServerID == "" || update.Expected.ServerID != expected[update.Expected.ID] {
				return ErrSiloOfflineUpgradeCheckpointConflict
			}
		}
	}
	if err := saveAppInstanceVersionsIfUnchanged(tx, updates); err != nil {
		return err
	}
	return tx.Commit()
}
