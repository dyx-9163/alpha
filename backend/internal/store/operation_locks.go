package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type OperationLockConflict struct {
	Lock OperationLock
}

func (e OperationLockConflict) Error() string {
	return fmt.Sprintf("active operation lock exists for %s/%s/%s", e.Lock.Scope, e.Lock.ResourceID, e.Lock.Operation)
}

func (s *Store) AcquireOperationLock(lock OperationLock) (OperationLock, error) {
	now := time.Now().UTC()
	lock.Scope = strings.TrimSpace(lock.Scope)
	lock.ResourceID = strings.TrimSpace(lock.ResourceID)
	lock.Operation = strings.TrimSpace(lock.Operation)
	lock.OwnerTaskID = strings.TrimSpace(lock.OwnerTaskID)
	lock.Owner = strings.TrimSpace(lock.Owner)
	lock.Metadata = strings.TrimSpace(lock.Metadata)
	if lock.Scope == "" || lock.ResourceID == "" || lock.Operation == "" {
		return lock, fmt.Errorf("operation lock requires scope, resource id and operation")
	}
	if lock.ID == "" {
		lock.ID = NewID("oplock")
	}
	if lock.Metadata == "" {
		lock.Metadata = "{}"
	}
	if lock.ExpiresAt.IsZero() || !lock.ExpiresAt.After(now) {
		lock.ExpiresAt = now.Add(time.Hour)
	}
	lock.HeartbeatAt = now
	if lock.CreatedAt.IsZero() {
		lock.CreatedAt = now
	}
	lock.UpdatedAt = now
	lock.Status = "active"

	tx, err := s.db.Begin()
	if err != nil {
		return lock, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`update operation_locks
		set status='expired', released_at=?, updated_at=?
		where status='active' and expires_at <= ?`, now, now, now); err != nil {
		return lock, err
	}
	conflict, found, err := findOperationLockConflict(tx, lock.Scope, lock.ResourceID, lock.Operation)
	if err != nil {
		return lock, err
	}
	if found {
		return lock, OperationLockConflict{Lock: conflict}
	}
	if _, err := tx.Exec(`insert into operation_locks(id,scope,resource_id,operation,owner_task_id,owner,status,expires_at,heartbeat_at,released_at,metadata,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		lock.ID, lock.Scope, lock.ResourceID, lock.Operation, lock.OwnerTaskID, lock.Owner, lock.Status, lock.ExpiresAt, lock.HeartbeatAt, nullableTime(lock.ReleasedAt), lock.Metadata, lock.CreatedAt, lock.UpdatedAt); err != nil {
		return lock, err
	}
	return lock, tx.Commit()
}

func (s *Store) HeartbeatOperationLock(id string, ttl time.Duration) (OperationLock, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return OperationLock{}, sql.ErrNoRows
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)
	res, err := s.db.Exec(`update operation_locks set heartbeat_at=?, expires_at=?, updated_at=? where id=? and status='active'`, now, expiresAt, now, id)
	if err != nil {
		return OperationLock{}, err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return OperationLock{}, sql.ErrNoRows
	}
	return s.GetOperationLock(id)
}

func (s *Store) HeartbeatOperationLocksByTaskID(taskID string, ttl time.Duration) (int, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return 0, nil
	}
	if ttl <= 0 {
		ttl = time.Hour
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`update operation_locks set heartbeat_at=?, expires_at=?, updated_at=? where owner_task_id=? and status='active'`,
		now, now.Add(ttl), now, taskID)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	return int(rows), err
}

func (s *Store) ReleaseOperationLock(id string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`update operation_locks set status='released', released_at=?, updated_at=? where id=? and status='active'`, now, now, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

func (s *Store) ReleaseOperationLocksByTaskID(taskID string) (int, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return 0, nil
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`update operation_locks set status='released', released_at=?, updated_at=? where owner_task_id=? and status='active'`,
		now, now, taskID)
	if err != nil {
		return 0, err
	}
	rows, err := res.RowsAffected()
	return int(rows), err
}

func (s *Store) ReleaseOperationLockByScope(scope, resourceID, operation string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`update operation_locks set status='released', released_at=?, updated_at=?
		where scope=? and resource_id=? and operation=? and status='active'`,
		now, now, strings.TrimSpace(scope), strings.TrimSpace(resourceID), strings.TrimSpace(operation))
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

func (s *Store) GetOperationLock(id string) (OperationLock, error) {
	row := s.db.QueryRow(`select id,scope,resource_id,operation,coalesce(owner_task_id,''),coalesce(owner,''),status,expires_at,heartbeat_at,released_at,coalesce(metadata,'{}'),created_at,updated_at
		from operation_locks where id=?`, strings.TrimSpace(id))
	return scanOperationLock(row)
}

func (s *Store) ListOperationLocks(scope, resourceID string, activeOnly bool) ([]OperationLock, error) {
	args := []any{}
	query := `select id,scope,resource_id,operation,coalesce(owner_task_id,''),coalesce(owner,''),status,expires_at,heartbeat_at,released_at,coalesce(metadata,'{}'),created_at,updated_at from operation_locks`
	where := []string{}
	if strings.TrimSpace(scope) != "" {
		where = append(where, "scope=?")
		args = append(args, strings.TrimSpace(scope))
	}
	if strings.TrimSpace(resourceID) != "" {
		where = append(where, "resource_id=?")
		args = append(args, strings.TrimSpace(resourceID))
	}
	if activeOnly {
		where = append(where, "status='active'")
	}
	if len(where) > 0 {
		query += " where " + strings.Join(where, " and ")
	}
	query += " order by created_at desc"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OperationLock{}
	for rows.Next() {
		lock, err := scanOperationLock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, lock)
	}
	return out, rows.Err()
}

func findOperationLockConflict(tx *sql.Tx, scope, resourceID, operation string) (OperationLock, bool, error) {
	row := tx.QueryRow(`select id,scope,resource_id,operation,coalesce(owner_task_id,''),coalesce(owner,''),status,expires_at,heartbeat_at,released_at,coalesce(metadata,'{}'),created_at,updated_at
		from operation_locks where scope=? and resource_id=? and operation=? and status='active' limit 1`, scope, resourceID, operation)
	lock, err := scanOperationLock(row)
	if err == sql.ErrNoRows {
		return OperationLock{}, false, nil
	}
	if err != nil {
		return OperationLock{}, false, err
	}
	return lock, true, nil
}

func scanOperationLock(scanner interface {
	Scan(dest ...any) error
}) (OperationLock, error) {
	var lock OperationLock
	var releasedAt sql.NullTime
	err := scanner.Scan(&lock.ID, &lock.Scope, &lock.ResourceID, &lock.Operation, &lock.OwnerTaskID, &lock.Owner, &lock.Status, &lock.ExpiresAt, &lock.HeartbeatAt, &releasedAt, &lock.Metadata, &lock.CreatedAt, &lock.UpdatedAt)
	lock.ReleasedAt = nullTime(releasedAt)
	return lock, err
}
