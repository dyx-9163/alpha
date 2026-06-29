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
	res, err := s.db.Exec(`delete from app_instances where id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return nil
}
