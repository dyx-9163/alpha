package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

const selectMySQLOfflineUpgradeCheckpoint = `select task_id, bundle_sha256, nodes from mysql_offline_upgrade_checkpoints where cluster_id=?`

var ErrMySQLOfflineUpgradeCheckpointConflict = errors.New("MySQL offline upgrade checkpoint conflict")

type MySQLOfflineUpgradeCheckpoint struct {
	TaskID, BundleSHA256 string
	Nodes                []MySQLOfflineUpgradeNode
}

type MySQLOfflineUpgradeNode struct {
	InstanceID, ServerID, Root, Endpoint, Group, UUID, GTID string
	SSHPort                                                 int
}

func (s *Store) GetMySQLOfflineUpgradeCheckpoint(clusterID string) (MySQLOfflineUpgradeCheckpoint, error) {
	return scanMySQLOfflineUpgradeCheckpoint(s.db.QueryRow(selectMySQLOfflineUpgradeCheckpoint, clusterID))
}

func scanMySQLOfflineUpgradeCheckpoint(row *sql.Row) (MySQLOfflineUpgradeCheckpoint, error) {
	var checkpoint MySQLOfflineUpgradeCheckpoint
	var nodes string
	err := row.Scan(&checkpoint.TaskID, &checkpoint.BundleSHA256, &nodes)
	if err != nil {
		return MySQLOfflineUpgradeCheckpoint{}, err
	}
	if err := json.Unmarshal([]byte(nodes), &checkpoint.Nodes); err != nil {
		return MySQLOfflineUpgradeCheckpoint{}, err
	}
	return checkpoint, nil
}

func (s *Store) CreateMySQLOfflineUpgradeCheckpoint(clusterID string, checkpoint MySQLOfflineUpgradeCheckpoint) error {
	nodes, err := json.Marshal(checkpoint.Nodes)
	if err != nil {
		return err
	}
	result, err := s.db.Exec(`insert into mysql_offline_upgrade_checkpoints(cluster_id,task_id,bundle_sha256,nodes)
		values(?,?,?,?) on conflict(cluster_id) do nothing`, clusterID, checkpoint.TaskID, checkpoint.BundleSHA256, string(nodes))
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrMySQLOfflineUpgradeCheckpointConflict
	}
	return nil
}

func (s *Store) CompleteMySQLOfflineUpgrade(clusterID, taskID string, updates []AppInstanceVersionUpdate) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// The conditional delete both verifies ownership and removes the checkpoint.
	// It rolls back with any failed version update.
	result, err := tx.Exec(`delete from mysql_offline_upgrade_checkpoints where cluster_id=? and task_id=?`, clusterID, taskID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrMySQLOfflineUpgradeCheckpointConflict
	}
	if err := saveAppInstanceVersionsIfUnchanged(tx, updates); err != nil {
		return err
	}
	return tx.Commit()
}
