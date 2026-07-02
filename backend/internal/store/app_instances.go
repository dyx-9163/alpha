package store

import (
	"database/sql"
	"time"
)

func (s *Store) SaveAppInstance(v AppInstance) (AppInstance, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = NewID("app")
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err := s.db.Exec(`insert into app_instances(id,app,version,server_id,status,topology,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set version=excluded.version,server_id=excluded.server_id,status=excluded.status,topology=excluded.topology,metadata=excluded.metadata,updated_at=excluded.updated_at`,
		v.ID, v.App, v.Version, v.ServerID, v.Status, v.Topology, v.Metadata, v.CreatedAt, v.UpdatedAt)
	return v, err
}

func (s *Store) ListAppInstances() ([]AppInstance, error) {
	rows, err := s.db.Query(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances order by created_at desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppInstance{}
	for rows.Next() {
		var v AppInstance
		if err := rows.Scan(&v.ID, &v.App, &v.Version, &v.ServerID, &v.Status, &v.Topology, &v.Metadata, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) GetAppInstance(id string) (AppInstance, error) {
	var v AppInstance
	err := s.db.QueryRow(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances where id=?`, id).
		Scan(&v.ID, &v.App, &v.Version, &v.ServerID, &v.Status, &v.Topology, &v.Metadata, &v.CreatedAt, &v.UpdatedAt)
	return v, err
}

func (s *Store) DeleteAppInstance(id string) error {
	if _, err := s.db.Exec(`delete from app_releases where instance_id=?`, id); err != nil {
		return err
	}
	res, err := s.db.Exec(`delete from app_instances where id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) SaveAppRelease(v AppRelease) (AppRelease, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = NewID("rel")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	if v.ActivatedAt.IsZero() && v.Status == "success" {
		v.ActivatedAt = now
	}
	_, err := s.db.Exec(`insert into app_releases(id,instance_id,app,version,release_id,server_id,status,manifest_json,config_hash,created_at,activated_at)
		values(?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id, release_id) do update set
		version=excluded.version,server_id=excluded.server_id,status=excluded.status,
		manifest_json=excluded.manifest_json,config_hash=excluded.config_hash,activated_at=excluded.activated_at`,
		v.ID, v.InstanceID, v.App, v.Version, v.ReleaseID, v.ServerID, v.Status, v.ManifestJSON, v.ConfigHash, v.CreatedAt, nullableTime(v.ActivatedAt))
	return v, err
}

func (s *Store) ListAppReleases(instanceID string) ([]AppRelease, error) {
	rows, err := s.db.Query(`select id,instance_id,app,version,release_id,server_id,status,coalesce(manifest_json,''),coalesce(config_hash,''),created_at,activated_at
		from app_releases where instance_id=? order by activated_at desc, created_at desc`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppRelease{}
	for rows.Next() {
		var v AppRelease
		var activatedAt sql.NullTime
		if err := rows.Scan(&v.ID, &v.InstanceID, &v.App, &v.Version, &v.ReleaseID, &v.ServerID, &v.Status, &v.ManifestJSON, &v.ConfigHash, &v.CreatedAt, &activatedAt); err != nil {
			return nil, err
		}
		v.ActivatedAt = nullTime(activatedAt)
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) DeleteOldAppReleases(instanceID string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	rows, err := s.db.Query(`select id from app_releases where instance_id=? and status='success' order by activated_at desc, created_at desc`, instanceID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(ids) <= keep {
		return 0, nil
	}
	deleted := 0
	for _, id := range ids[keep:] {
		res, err := s.db.Exec(`delete from app_releases where id=?`, id)
		if err != nil {
			return deleted, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			deleted += int(rows)
		}
	}
	return deleted, nil
}
