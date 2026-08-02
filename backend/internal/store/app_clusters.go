package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

func (s *Store) SaveAppClusterDeployment(cluster AppCluster, instances []AppInstance, members []AppClusterMember) ([]AppInstance, error) {
	cluster = normalizeAppCluster(cluster)
	if cluster.ID == "" || cluster.App == "" || cluster.Name == "" || len(instances) == 0 || len(instances) != len(members) {
		return nil, sql.ErrNoRows
	}
	now := time.Now()
	if cluster.CreatedAt.IsZero() {
		cluster.CreatedAt = now
	}
	cluster.UpdatedAt = now
	instanceByID := make(map[string]AppInstance, len(instances))
	serverIDs := make(map[string]bool, len(instances))
	for index := range instances {
		instance := instances[index]
		instance.ID = strings.TrimSpace(instance.ID)
		instance.ServerID = strings.TrimSpace(instance.ServerID)
		if instance.ID == "" || instance.App != cluster.App || instance.ServerID == "" || serverIDs[instance.ServerID] {
			return nil, fmt.Errorf("invalid app cluster instance ownership")
		}
		if instance.CreatedAt.IsZero() {
			instance.CreatedAt = now
		}
		instance.UpdatedAt = now
		instances[index] = instance
		instanceByID[instance.ID] = instance
		serverIDs[instance.ServerID] = true
	}
	seenMembers := make(map[string]bool, len(members))
	for index := range members {
		member := normalizeAppClusterMember(members[index])
		instance, ok := instanceByID[member.InstanceID]
		if member.ClusterID != cluster.ID || !ok || member.ServerID != instance.ServerID || seenMembers[member.InstanceID] {
			return nil, fmt.Errorf("invalid app cluster membership")
		}
		if member.ID == "" {
			member.ID = NewID("clmember")
		}
		if member.CreatedAt.IsZero() {
			member.CreatedAt = now
		}
		member.UpdatedAt = now
		members[index] = member
		seenMembers[member.InstanceID] = true
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`insert into app_clusters(id,app,name,topology,status,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?)`,
		cluster.ID, cluster.App, cluster.Name, cluster.Topology, cluster.Status, cluster.Metadata, cluster.CreatedAt, cluster.UpdatedAt); err != nil {
		return nil, err
	}
	for _, instance := range instances {
		if _, err := tx.Exec(`insert into app_instances(id,app,version,server_id,status,topology,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?,?)`,
			instance.ID, instance.App, instance.Version, instance.ServerID, instance.Status, instance.Topology, instance.Metadata, instance.CreatedAt, instance.UpdatedAt); err != nil {
			return nil, err
		}
	}
	for _, member := range members {
		if _, err := tx.Exec(`insert into app_cluster_members(id,cluster_id,instance_id,server_id,role,status,metadata,created_at,updated_at) values(?,?,?,?,?,?,?,?,?)`,
			member.ID, member.ClusterID, member.InstanceID, member.ServerID, member.Role, member.Status, member.Metadata, member.CreatedAt, member.UpdatedAt); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return instances, nil
}

func (s *Store) SaveAppCluster(cluster AppCluster) (AppCluster, error) {
	cluster = normalizeAppCluster(cluster)
	if cluster.App == "" || cluster.Name == "" {
		return AppCluster{}, sql.ErrNoRows
	}
	now := time.Now()
	if cluster.ID == "" {
		cluster.ID = NewID("cluster")
		cluster.CreatedAt = now
	}
	if cluster.CreatedAt.IsZero() {
		cluster.CreatedAt = now
	}
	cluster.UpdatedAt = now
	_, err := s.db.Exec(`insert into app_clusters(id,app,name,topology,status,metadata,created_at,updated_at)
		values(?,?,?,?,?,?,?,?)
		on conflict(app,name) do update set
		topology=excluded.topology,status=excluded.status,metadata=excluded.metadata,updated_at=excluded.updated_at`,
		cluster.ID, cluster.App, cluster.Name, cluster.Topology, cluster.Status, cluster.Metadata, cluster.CreatedAt, cluster.UpdatedAt)
	if err != nil {
		return AppCluster{}, err
	}
	return s.getAppClusterByAppName(cluster.App, cluster.Name)
}

func (s *Store) GetAppCluster(id string) (AppCluster, error) {
	row := s.db.QueryRow(`select id,app,name,topology,status,coalesce(metadata,'{}'),created_at,updated_at from app_clusters where id=?`, strings.TrimSpace(id))
	return scanAppCluster(row)
}

func (s *Store) getAppClusterByAppName(app, name string) (AppCluster, error) {
	row := s.db.QueryRow(`select id,app,name,topology,status,coalesce(metadata,'{}'),created_at,updated_at from app_clusters where app=? and name=?`, strings.TrimSpace(app), strings.TrimSpace(name))
	return scanAppCluster(row)
}

func (s *Store) ListAppClusters(app string) ([]AppCluster, error) {
	query := `select id,app,name,topology,status,coalesce(metadata,'{}'),created_at,updated_at from app_clusters`
	args := []any{}
	if strings.TrimSpace(app) != "" {
		query += ` where app=?`
		args = append(args, strings.TrimSpace(app))
	}
	query += ` order by app, name`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppCluster{}
	for rows.Next() {
		cluster, err := scanAppCluster(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, cluster)
	}
	return out, rows.Err()
}

func (s *Store) DeleteAppCluster(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`delete from app_cluster_members where cluster_id=?`, strings.TrimSpace(id)); err != nil {
		return err
	}
	res, err := tx.Exec(`delete from app_clusters where id=?`, strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) SaveAppClusterMember(member AppClusterMember) (AppClusterMember, error) {
	member = normalizeAppClusterMember(member)
	if member.ClusterID == "" || member.InstanceID == "" {
		return AppClusterMember{}, sql.ErrNoRows
	}
	now := time.Now()
	if member.ID == "" {
		member.ID = NewID("clmember")
		member.CreatedAt = now
	}
	if member.CreatedAt.IsZero() {
		member.CreatedAt = now
	}
	member.UpdatedAt = now
	_, err := s.db.Exec(`insert into app_cluster_members(id,cluster_id,instance_id,server_id,role,status,metadata,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?)
		on conflict(cluster_id,instance_id) do update set
		server_id=excluded.server_id,role=excluded.role,status=excluded.status,metadata=excluded.metadata,updated_at=excluded.updated_at`,
		member.ID, member.ClusterID, member.InstanceID, member.ServerID, member.Role, member.Status, member.Metadata, member.CreatedAt, member.UpdatedAt)
	return member, err
}

func (s *Store) ListAppClusterMembers(clusterID string) ([]AppClusterMember, error) {
	rows, err := s.db.Query(`select id,cluster_id,instance_id,coalesce(server_id,''),coalesce(role,''),status,coalesce(metadata,'{}'),created_at,updated_at
		from app_cluster_members where cluster_id=? order by role, instance_id`, strings.TrimSpace(clusterID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppClusterMember{}
	for rows.Next() {
		member, err := scanAppClusterMember(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, member)
	}
	return out, rows.Err()
}

func normalizeAppCluster(cluster AppCluster) AppCluster {
	cluster.ID = strings.TrimSpace(cluster.ID)
	cluster.App = strings.TrimSpace(cluster.App)
	cluster.Name = strings.TrimSpace(cluster.Name)
	cluster.Topology = strings.TrimSpace(cluster.Topology)
	if cluster.Topology == "" {
		cluster.Topology = "standalone"
	}
	cluster.Status = strings.TrimSpace(cluster.Status)
	if cluster.Status == "" {
		cluster.Status = "active"
	}
	cluster.Metadata = strings.TrimSpace(cluster.Metadata)
	if cluster.Metadata == "" {
		cluster.Metadata = "{}"
	}
	return cluster
}

func normalizeAppClusterMember(member AppClusterMember) AppClusterMember {
	member.ID = strings.TrimSpace(member.ID)
	member.ClusterID = strings.TrimSpace(member.ClusterID)
	member.InstanceID = strings.TrimSpace(member.InstanceID)
	member.ServerID = strings.TrimSpace(member.ServerID)
	member.Role = strings.TrimSpace(member.Role)
	member.Status = strings.TrimSpace(member.Status)
	if member.Status == "" {
		member.Status = "active"
	}
	member.Metadata = strings.TrimSpace(member.Metadata)
	if member.Metadata == "" {
		member.Metadata = "{}"
	}
	return member
}

func scanAppCluster(scanner interface {
	Scan(dest ...any) error
}) (AppCluster, error) {
	var cluster AppCluster
	err := scanner.Scan(&cluster.ID, &cluster.App, &cluster.Name, &cluster.Topology, &cluster.Status, &cluster.Metadata, &cluster.CreatedAt, &cluster.UpdatedAt)
	return cluster, err
}

func scanAppClusterMember(scanner interface {
	Scan(dest ...any) error
}) (AppClusterMember, error) {
	var member AppClusterMember
	err := scanner.Scan(&member.ID, &member.ClusterID, &member.InstanceID, &member.ServerID, &member.Role, &member.Status, &member.Metadata, &member.CreatedAt, &member.UpdatedAt)
	return member, err
}
