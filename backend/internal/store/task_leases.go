package store

import (
	"database/sql"
	"strings"
	"time"
)

func (s *Store) AcquireTaskLease(id, owner string, ttl time.Duration) (bool, error) {
	id = strings.TrimSpace(id)
	owner = strings.TrimSpace(owner)
	if id == "" || owner == "" {
		return false, sql.ErrNoRows
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`update tasks
		set lease_owner=?, lease_expires_at=?, attempt=coalesce(attempt,0)+1
		where id=? and status in ('pending','running')
		and (coalesce(lease_owner,'')='' or lease_owner=? or lease_expires_at is null or lease_expires_at <= ?)`,
		owner, now.Add(ttl), id, owner, now)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

func (s *Store) RenewTaskLease(id, owner string, ttl time.Duration) (bool, error) {
	id = strings.TrimSpace(id)
	owner = strings.TrimSpace(owner)
	if id == "" || owner == "" {
		return false, sql.ErrNoRows
	}
	if ttl <= 0 {
		ttl = time.Minute
	}
	now := time.Now().UTC()
	res, err := s.db.Exec(`update tasks set lease_expires_at=? where id=? and lease_owner=? and status in ('pending','running')`, now.Add(ttl), id, owner)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

func (s *Store) ReleaseTaskLease(id, owner string) (bool, error) {
	id = strings.TrimSpace(id)
	owner = strings.TrimSpace(owner)
	if id == "" || owner == "" {
		return false, sql.ErrNoRows
	}
	res, err := s.db.Exec(`update tasks set lease_owner='', lease_expires_at=null where id=? and lease_owner=?`, id, owner)
	if err != nil {
		return false, err
	}
	rows, err := res.RowsAffected()
	return rows > 0, err
}

func (s *Store) SetTaskTrace(id, idempotencyKey, correlationID string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return sql.ErrNoRows
	}
	_, err := s.db.Exec(`update tasks set idempotency_key=?, correlation_id=? where id=?`, strings.TrimSpace(idempotencyKey), strings.TrimSpace(correlationID), id)
	return err
}
