package store

import (
	"database/sql"
	"strings"
	"time"

	"aifar-deployment/backend/internal/logmask"
)

func (s *Store) UpsertCollectorRun(run CollectorRun) error {
	run.Name = strings.TrimSpace(run.Name)
	if run.Name == "" {
		return sql.ErrNoRows
	}
	if run.Status == "" {
		run.Status = "unknown"
	}
	if run.UpdatedAt.IsZero() {
		run.UpdatedAt = time.Now()
	}
	run.LastError = logmask.Mask(run.LastError)
	_, err := s.db.Exec(`insert into collector_runs(name,target,status,last_error,started_at,finished_at,duration_ms,updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(name) do update set
		target=excluded.target,status=excluded.status,last_error=excluded.last_error,
		started_at=excluded.started_at,finished_at=excluded.finished_at,
		duration_ms=excluded.duration_ms,updated_at=excluded.updated_at`,
		run.Name, run.Target, run.Status, run.LastError, nullableTime(run.StartedAt), nullableTime(run.FinishedAt), run.DurationMS, run.UpdatedAt)
	return err
}

func (s *Store) ListCollectorRuns() ([]CollectorRun, error) {
	rows, err := s.db.Query(`select name,coalesce(target,''),status,coalesce(last_error,''),started_at,finished_at,duration_ms,updated_at from collector_runs order by name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CollectorRun{}
	for rows.Next() {
		var run CollectorRun
		var startedAt, finishedAt sql.NullTime
		if err := rows.Scan(&run.Name, &run.Target, &run.Status, &run.LastError, &startedAt, &finishedAt, &run.DurationMS, &run.UpdatedAt); err != nil {
			return nil, err
		}
		run.StartedAt = nullTime(startedAt)
		run.FinishedAt = nullTime(finishedAt)
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *Store) UpsertStatusSnapshot(snapshot StatusSnapshot) (StatusSnapshot, bool, error) {
	snapshot.Scope = strings.TrimSpace(snapshot.Scope)
	snapshot.ResourceID = strings.TrimSpace(snapshot.ResourceID)
	if snapshot.Scope == "" || snapshot.ResourceID == "" {
		return snapshot, false, sql.ErrNoRows
	}
	if snapshot.Status == "" {
		snapshot.Status = "unknown"
	}
	if snapshot.CollectedAt.IsZero() {
		snapshot.CollectedAt = time.Now()
	}
	snapshot.UpdatedAt = time.Now()
	snapshot.LastError = logmask.Mask(snapshot.LastError)
	current, err := s.GetStatusSnapshot(snapshot.Scope, snapshot.ResourceID)
	if err != nil && !IsNotFound(err) {
		return snapshot, false, err
	}
	changed := IsNotFound(err) ||
		current.Status != snapshot.Status ||
		current.Payload != snapshot.Payload ||
		current.LastError != snapshot.LastError ||
		current.ServerID != snapshot.ServerID
	if changed {
		snapshot.Version = current.Version + 1
		if snapshot.Version < 1 {
			snapshot.Version = 1
		}
	} else {
		snapshot.Version = current.Version
	}
	_, err = s.db.Exec(`insert into status_snapshots(scope,resource_id,server_id,status,payload,last_error,version,collected_at,updated_at)
		values(?,?,?,?,?,?,?,?,?)
		on conflict(scope,resource_id) do update set
		server_id=excluded.server_id,status=excluded.status,payload=excluded.payload,last_error=excluded.last_error,
		version=excluded.version,collected_at=excluded.collected_at,updated_at=excluded.updated_at`,
		snapshot.Scope, snapshot.ResourceID, snapshot.ServerID, snapshot.Status, snapshot.Payload, snapshot.LastError, snapshot.Version, snapshot.CollectedAt, snapshot.UpdatedAt)
	return snapshot, changed, err
}

func (s *Store) GetStatusSnapshot(scope, resourceID string) (StatusSnapshot, error) {
	var snapshot StatusSnapshot
	err := s.db.QueryRow(`select scope,resource_id,coalesce(server_id,''),status,payload,coalesce(last_error,''),version,collected_at,updated_at
		from status_snapshots where scope=? and resource_id=?`, strings.TrimSpace(scope), strings.TrimSpace(resourceID)).
		Scan(&snapshot.Scope, &snapshot.ResourceID, &snapshot.ServerID, &snapshot.Status, &snapshot.Payload, &snapshot.LastError, &snapshot.Version, &snapshot.CollectedAt, &snapshot.UpdatedAt)
	return snapshot, err
}

func (s *Store) ListStatusSnapshots(scope, serverID string) ([]StatusSnapshot, error) {
	scope = strings.TrimSpace(scope)
	serverID = strings.TrimSpace(serverID)
	query := `select scope,resource_id,coalesce(server_id,''),status,payload,coalesce(last_error,''),version,collected_at,updated_at from status_snapshots`
	args := []any{}
	where := []string{}
	if scope != "" {
		where = append(where, "scope=?")
		args = append(args, scope)
	}
	if serverID != "" {
		where = append(where, "server_id=?")
		args = append(args, serverID)
	}
	if len(where) > 0 {
		query += " where " + strings.Join(where, " and ")
	}
	query += " order by scope, resource_id"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []StatusSnapshot{}
	for rows.Next() {
		var snapshot StatusSnapshot
		if err := rows.Scan(&snapshot.Scope, &snapshot.ResourceID, &snapshot.ServerID, &snapshot.Status, &snapshot.Payload, &snapshot.LastError, &snapshot.Version, &snapshot.CollectedAt, &snapshot.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, snapshot)
	}
	return out, rows.Err()
}
