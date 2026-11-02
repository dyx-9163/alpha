package store

import "database/sql"

// LatestTaskForTarget is independent of the global task-list display limit.
func (s *Store) LatestTaskForTarget(taskType, target string) (Task, error) {
	var t Task
	var startedAt, finishedAt, leaseExpiresAt sql.NullTime
	err := s.db.QueryRow(`select id,type,target,status,created_by,error,coalesce(lease_owner,''),lease_expires_at,coalesce(attempt,0),coalesce(idempotency_key,''),coalesce(correlation_id,''),created_at,started_at,finished_at from tasks where type=? and target=? order by created_at desc,id desc limit 1`, taskType, target).Scan(&t.ID, &t.Type, &t.Target, &t.Status, &t.CreatedBy, &t.Error, &t.LeaseOwner, &leaseExpiresAt, &t.Attempt, &t.IdempotencyKey, &t.CorrelationID, &t.CreatedAt, &startedAt, &finishedAt)
	t.StartedAt = nullTime(startedAt)
	t.FinishedAt = nullTime(finishedAt)
	t.LeaseExpiresAt = nullTime(leaseExpiresAt)
	return t, err
}
