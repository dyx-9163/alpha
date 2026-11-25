package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

var ErrMySQLOfflineUpgradeLockChanged = errors.New("interrupted MySQL upgrade lock or checkpoint changed")

type MySQLOfflineUpgradeLockRecovery struct {
	ClusterID, LockID, OwnerTaskID, CheckpointTaskID string
}

const selectInterruptedMySQLOfflineUpgradeLock = `select locks.id, tasks.id, checkpoint.task_id, locks.heartbeat_at, tasks.finished_at
	from operation_locks locks
	join tasks on tasks.id=locks.owner_task_id
	join mysql_offline_upgrade_checkpoints checkpoint on checkpoint.cluster_id=locks.resource_id
	where locks.scope='app-cluster' and locks.resource_id=? and locks.operation='mutate' and locks.status='active'
		and tasks.type='apps.mysql.upgrade' and tasks.target=locks.resource_id and tasks.status='failed' and tasks.interrupted_by_restart=1`

func (s *Store) GetInterruptedMySQLOfflineUpgradeLock(clusterID string) (MySQLOfflineUpgradeLockRecovery, error) {
	recovery, _, err := scanInterruptedMySQLOfflineUpgradeLock(s.db.QueryRow(selectInterruptedMySQLOfflineUpgradeLock, clusterID), clusterID)
	return recovery, err
}

func scanInterruptedMySQLOfflineUpgradeLock(row *sql.Row, clusterID string) (MySQLOfflineUpgradeLockRecovery, time.Time, error) {
	recovery := MySQLOfflineUpgradeLockRecovery{ClusterID: clusterID}
	var heartbeat, interruptedAt time.Time
	err := row.Scan(&recovery.LockID, &recovery.OwnerTaskID, &recovery.CheckpointTaskID, &heartbeat, &interruptedAt)
	if err != nil {
		return MySQLOfflineUpgradeLockRecovery{}, time.Time{}, err
	}
	if heartbeat.After(interruptedAt) || recovery.CheckpointTaskID == "" {
		return MySQLOfflineUpgradeLockRecovery{}, time.Time{}, sql.ErrNoRows
	}
	return recovery, interruptedAt, nil
}

// TakeOverInterruptedMySQLOfflineUpgradeLocks must be called after the remote
// idle checks pass. It preserves other owners and transfers the full lock set
// in one transaction.
func (s *Store) TakeOverInterruptedMySQLOfflineUpgradeLocks(recovery MySQLOfflineUpgradeLockRecovery, taskID string, requested []OperationLock) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	current, interruptedAt, err := scanInterruptedMySQLOfflineUpgradeLock(tx.QueryRow(selectInterruptedMySQLOfflineUpgradeLock, recovery.ClusterID), recovery.ClusterID)
	if errors.Is(err, sql.ErrNoRows) || err == nil && current != recovery {
		return ErrMySQLOfflineUpgradeLockChanged
	}
	if err != nil {
		return err
	}
	var actor string
	err = tx.QueryRow(`select created_by from tasks where id=? and type='apps.mysql.upgrade' and target=? and status='running'`, taskID, recovery.ClusterID).Scan(&actor)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrMySQLOfflineUpgradeLockChanged
	}
	if err != nil {
		return err
	}
	checkpoint, err := scanMySQLOfflineUpgradeCheckpoint(tx.QueryRow(selectMySQLOfflineUpgradeCheckpoint, recovery.ClusterID))
	if err != nil {
		return err
	}
	locks, err := mysqlOfflineUpgradeLockSet(recovery.ClusterID, checkpoint.Nodes, requested)
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	for _, lock := range locks {
		previous, found, err := findOperationLockConflict(tx, lock.Scope, lock.ResourceID, lock.Operation)
		if err != nil {
			return err
		}
		if found {
			if previous.OwnerTaskID != recovery.OwnerTaskID || previous.HeartbeatAt.After(interruptedAt) {
				return OperationLockConflict{Lock: previous}
			}
			if _, err := tx.Exec(`update operation_locks set status='released',released_at=?,updated_at=? where id=?`, now, now, previous.ID); err != nil {
				return err
			}
		}
		lock.ID, lock.OwnerTaskID, lock.Owner, lock.Status = NewID("oplock"), taskID, actor, "active"
		lock.CreatedAt, lock.UpdatedAt, lock.HeartbeatAt, lock.ExpiresAt = now, now, now, now.Add(time.Hour)
		if err := insertOperationLock(tx, lock); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func mysqlOfflineUpgradeLockSet(clusterID string, nodes []MySQLOfflineUpgradeNode, requested []OperationLock) ([]OperationLock, error) {
	if len(nodes) < 3 {
		return nil, ErrMySQLOfflineUpgradeLockChanged
	}
	type lockKey struct{ scope, resourceID string }
	expected := map[lockKey]bool{{"app-cluster", clusterID}: true}
	for _, node := range nodes {
		instance := lockKey{"app-instance", node.InstanceID}
		if node.InstanceID == "" || node.ServerID == "" || expected[instance] {
			return nil, ErrMySQLOfflineUpgradeLockChanged
		}
		expected[instance] = true
		expected[lockKey{"app-target", "mysql:" + node.ServerID}] = true
		expected[lockKey{"server-config", node.ServerID}] = true
	}
	locks := make([]OperationLock, 0, len(expected))
	seen := make(map[lockKey]bool, len(expected))
	for _, lock := range requested {
		lock.Scope, lock.ResourceID = strings.TrimSpace(lock.Scope), strings.TrimSpace(lock.ResourceID)
		key := lockKey{lock.Scope, lock.ResourceID}
		if !expected[key] || lock.Operation != "mutate" {
			return nil, ErrMySQLOfflineUpgradeLockChanged
		}
		if !seen[key] {
			locks = append(locks, lock)
			seen[key] = true
		}
	}
	if len(locks) != len(expected) {
		return nil, ErrMySQLOfflineUpgradeLockChanged
	}
	return locks, nil
}
