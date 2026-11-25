package store

import (
	"database/sql"
	"encoding/json"
	"errors"
)

var ErrSiloOfflineUpgradeLockChanged = errors.New("interrupted MinIO to Silo upgrade lock or checkpoint changed")

const siloOfflineUpgradeLockPrefix = "minio-silo:"

func SiloOfflineUpgradeLockResourceID(groupID string) string {
	return siloOfflineUpgradeLockPrefix + groupID
}

const selectInterruptedSiloOfflineUpgradeLock = `select locks.id, tasks.id, checkpoint.task_id, locks.heartbeat_at, tasks.finished_at
	from operation_locks locks
	join tasks on tasks.id=locks.owner_task_id
	join silo_offline_upgrade_checkpoints checkpoint on checkpoint.group_id=tasks.target
	where locks.scope='app-cluster' and locks.resource_id='` + siloOfflineUpgradeLockPrefix + `' || checkpoint.group_id
		and locks.operation='mutate' and locks.status='active'
		and tasks.type='apps.minio.upgrade' and tasks.target=? and tasks.status='failed' and tasks.interrupted_by_restart=1`

func (s *Store) GetInterruptedSiloOfflineUpgradeLock(groupID string) (OfflineUpgradeLockRecovery, error) {
	recovery, _, err := scanInterruptedOfflineUpgradeLock(s.db.QueryRow(selectInterruptedSiloOfflineUpgradeLock, groupID), groupID)
	return recovery, err
}

// TakeOverInterruptedSiloOfflineUpgradeLocks must be called after verifying
// that the interrupted worker has no active remote migration commands.
func (s *Store) TakeOverInterruptedSiloOfflineUpgradeLocks(recovery OfflineUpgradeLockRecovery, taskID string, requested []OperationLock) error {
	return s.takeOverInterruptedOfflineUpgradeLocks(recovery, taskID, "apps.minio.upgrade", selectInterruptedSiloOfflineUpgradeLock, ErrSiloOfflineUpgradeLockChanged, func(tx *sql.Tx) ([]OperationLock, error) {
		var rawNodes string
		if err := tx.QueryRow(`select nodes from silo_offline_upgrade_checkpoints where group_id=? and task_id=?`, recovery.ClusterID, recovery.CheckpointTaskID).Scan(&rawNodes); err != nil {
			return nil, err
		}
		var checkpointNodes []SiloOfflineUpgradeNode
		if err := json.Unmarshal([]byte(rawNodes), &checkpointNodes); err != nil {
			return nil, err
		}
		if len(checkpointNodes) != 2 || checkpointNodes[0].ServerID == checkpointNodes[1].ServerID {
			return nil, ErrSiloOfflineUpgradeLockChanged
		}
		nodes := make([]offlineUpgradeLockNode, 0, len(checkpointNodes))
		for _, node := range checkpointNodes {
			nodes = append(nodes, offlineUpgradeLockNode{node.InstanceID, node.ServerID})
		}
		return offlineUpgradeLockSet(SiloOfflineUpgradeLockResourceID(recovery.ClusterID), "minio", nodes, requested, ErrSiloOfflineUpgradeLockChanged)
	})
}
