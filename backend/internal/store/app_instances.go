package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var ErrAppInstanceConflict = errors.New("app instance changed concurrently")

func (s *Store) SaveAppInstance(v AppInstance) (AppInstance, error) {
	now := time.Now().UTC()
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

func (s *Store) SaveAppInstanceIfUnchanged(next AppInstance, expectedUpdatedAt time.Time) (AppInstance, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AppInstance{}, err
	}
	defer tx.Rollback()
	saved, err := saveAppInstanceIfUnchangedTx(tx, next, expectedUpdatedAt)
	if err != nil {
		return AppInstance{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppInstance{}, err
	}
	return saved, nil
}

// SaveAppInstanceIfUnchangedWithLock persists runtime-config metadata only
// while lockID is the exact active global runtime-config owner for next.ID.
func (s *Store) SaveAppInstanceIfUnchangedWithLock(lockID string, next AppInstance, expectedUpdatedAt time.Time) (AppInstance, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AppInstance{}, err
	}
	defer tx.Rollback()
	if err := fenceActiveAIFARRuntimeConfigLockTx(tx, lockID, next.ID, time.Now().UTC()); err != nil {
		return AppInstance{}, err
	}
	saved, err := saveAppInstanceIfUnchangedTx(tx, next, expectedUpdatedAt)
	if err != nil {
		return AppInstance{}, err
	}
	if err := tx.Commit(); err != nil {
		return AppInstance{}, err
	}
	return saved, nil
}

func saveAppInstanceIfUnchangedTx(tx *sql.Tx, next AppInstance, expectedUpdatedAt time.Time) (AppInstance, error) {
	freshUpdatedAt := time.Now().UTC()
	if freshUpdatedAt.Sub(expectedUpdatedAt) < time.Millisecond {
		freshUpdatedAt = expectedUpdatedAt.Add(time.Millisecond)
	}
	result, err := tx.Exec(`update app_instances set version=?,server_id=?,status=?,topology=?,metadata=?,updated_at=? where id=? and updated_at=?`,
		next.Version, next.ServerID, next.Status, next.Topology, next.Metadata, freshUpdatedAt, next.ID, expectedUpdatedAt)
	if err != nil {
		return AppInstance{}, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return AppInstance{}, err
	}
	if rows == 0 {
		return AppInstance{}, ErrAppInstanceConflict
	}
	var saved AppInstance
	if err := tx.QueryRow(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances where id=?`, next.ID).
		Scan(&saved.ID, &saved.App, &saved.Version, &saved.ServerID, &saved.Status, &saved.Topology, &saved.Metadata, &saved.CreatedAt, &saved.UpdatedAt); err != nil {
		return AppInstance{}, err
	}
	return saved, nil
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
	if err := s.DeleteAIFAROrchestration(id); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	clusterRows, err := tx.Query(`select distinct cluster_id from app_cluster_members where instance_id=?`, id)
	if err != nil {
		return err
	}
	affectedClusterIDs := []string{}
	for clusterRows.Next() {
		var clusterID string
		if err := clusterRows.Scan(&clusterID); err != nil {
			clusterRows.Close()
			return err
		}
		affectedClusterIDs = append(affectedClusterIDs, clusterID)
	}
	if err := clusterRows.Err(); err != nil {
		clusterRows.Close()
		return err
	}
	if err := clusterRows.Close(); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from operation_locks where scope='app-instance' and resource_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from app_release_artifacts where instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from app_release_snapshots where instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from app_releases where instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from app_cluster_members where instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from credential_bindings where app_instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from credential_references where resource_type='app-instance' and resource_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`update credentials set app_instance_id='' where app_instance_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from status_snapshot_history where resource_id=? and scope in ('app.instance','aifar.runtime')`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`delete from status_snapshots where resource_id=? and scope in ('app.instance','aifar.runtime')`, id); err != nil {
		return err
	}
	res, err := tx.Exec(`delete from app_instances where id=?`, id)
	if err != nil {
		return err
	}
	if rows, _ := res.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	for _, clusterID := range affectedClusterIDs {
		if _, err := tx.Exec(`delete from app_clusters where id=? and not exists (select 1 from app_cluster_members where cluster_id=?)`, clusterID, clusterID); err != nil {
			return err
		}
	}
	return tx.Commit()
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
	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`insert into app_releases(id,instance_id,app,version,release_id,server_id,status,manifest_json,config_hash,created_at,activated_at)
		values(?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id, release_id) do update set
		version=excluded.version,server_id=excluded.server_id,status=excluded.status,
		manifest_json=excluded.manifest_json,config_hash=excluded.config_hash,activated_at=excluded.activated_at`,
		v.ID, v.InstanceID, v.App, v.Version, v.ReleaseID, v.ServerID, v.Status, v.ManifestJSON, v.ConfigHash, v.CreatedAt, nullableTime(v.ActivatedAt)); err != nil {
		return v, err
	}
	if err := replaceAppReleaseAuxiliaryRecordsTx(tx, v); err != nil {
		return v, err
	}
	return v, tx.Commit()
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

func (s *Store) DeleteAppRelease(instanceID, releaseID string) error {
	instanceID = strings.TrimSpace(instanceID)
	releaseID = strings.TrimSpace(releaseID)
	if instanceID == "" || releaseID == "" {
		return sql.ErrNoRows
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := deleteAppReleaseAuxiliaryRecordsTx(tx, instanceID, releaseID); err != nil {
		return err
	}
	result, err := tx.Exec(`delete from app_releases where instance_id=? and release_id=?`, instanceID, releaseID)
	if err != nil {
		return err
	}
	if rows, _ := result.RowsAffected(); rows == 0 {
		return sql.ErrNoRows
	}
	return tx.Commit()
}

func (s *Store) ReconcilePendingAppReleases() (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`select id,coalesce(manifest_json,'') from app_releases where app='aifar' and status='pending'`)
	if err != nil {
		return 0, err
	}
	type pendingRelease struct {
		id       string
		manifest string
	}
	pending := []pendingRelease{}
	for rows.Next() {
		var release pendingRelease
		if err := rows.Scan(&release.id, &release.manifest); err != nil {
			rows.Close()
			return 0, err
		}
		pending = append(pending, release)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}

	updated := 0
	for _, release := range pending {
		manifest := map[string]any{}
		if err := json.Unmarshal([]byte(release.manifest), &manifest); err != nil {
			continue
		}
		taskID, _ := manifest["taskId"].(string)
		taskID = strings.TrimSpace(taskID)
		if taskID == "" {
			continue
		}
		var taskStatus, taskError string
		var finishedAt sql.NullTime
		if err := tx.QueryRow(`select status,coalesce(error,''),finished_at from tasks where id=?`, taskID).Scan(&taskStatus, &taskError, &finishedAt); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return updated, err
		}
		switch taskStatus {
		case "failed", "cancelled", "timeout":
		default:
			continue
		}
		failedAt := time.Now().UTC()
		if finishedAt.Valid {
			failedAt = finishedAt.Time.UTC()
		}
		if strings.TrimSpace(taskError) == "" {
			taskError = fmt.Sprintf("associated task ended with status %s", taskStatus)
		}
		manifest["status"] = "failed"
		manifest["phase"] = "failed"
		manifest["taskStatus"] = taskStatus
		manifest["error"] = taskError
		manifest["failedAt"] = failedAt.Format(time.RFC3339Nano)
		raw, err := json.Marshal(manifest)
		if err != nil {
			return updated, err
		}
		result, err := tx.Exec(`update app_releases set status='failed',manifest_json=?,activated_at=null where id=? and status='pending'`, string(raw), release.id)
		if err != nil {
			return updated, err
		}
		if affected, _ := result.RowsAffected(); affected > 0 {
			updated++
		}
	}
	if err := tx.Commit(); err != nil {
		return updated, err
	}
	return updated, nil
}

func (s *Store) DeleteOldAppReleases(instanceID string, keep int) (int, error) {
	if keep < 1 {
		keep = 1
	}
	rows, err := s.db.Query(`select id,release_id from app_releases where instance_id=? and status='success' order by activated_at desc, created_at desc`, instanceID)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	type releaseRow struct {
		id      string
		release string
	}
	var rowsData []releaseRow
	for rows.Next() {
		var row releaseRow
		if err := rows.Scan(&row.id, &row.release); err != nil {
			return 0, err
		}
		rowsData = append(rowsData, row)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(rowsData) <= keep {
		return 0, nil
	}
	protected := s.activeAppReleaseIDs(instanceID)
	deleted := 0
	for _, row := range rowsData[keep:] {
		if protected[row.release] {
			continue
		}
		if _, err := s.db.Exec(`delete from app_release_artifacts where instance_id=? and release_id=?`, instanceID, row.release); err != nil {
			return deleted, err
		}
		if _, err := s.db.Exec(`delete from app_release_snapshots where instance_id=? and release_id=?`, instanceID, row.release); err != nil {
			return deleted, err
		}
		res, err := s.db.Exec(`delete from app_releases where id=?`, row.id)
		if err != nil {
			return deleted, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			deleted += int(rows)
		}
	}
	return deleted, nil
}

func (s *Store) activeAppReleaseIDs(instanceID string) map[string]bool {
	protected := map[string]bool{}
	var metadata string
	if err := s.db.QueryRow(`select coalesce(metadata,'') from app_instances where id=?`, instanceID).Scan(&metadata); err != nil || strings.TrimSpace(metadata) == "" {
		return protected
	}
	var data struct {
		CurrentRevision  string            `json:"currentRevision"`
		ReleaseID        string            `json:"releaseId"`
		ServiceRevisions map[string]string `json:"serviceRevisions"`
	}
	if err := json.Unmarshal([]byte(metadata), &data); err != nil {
		return protected
	}
	for _, releaseID := range []string{data.CurrentRevision, data.ReleaseID} {
		if releaseID = strings.TrimSpace(releaseID); releaseID != "" {
			protected[releaseID] = true
		}
	}
	for _, releaseID := range data.ServiceRevisions {
		if releaseID = strings.TrimSpace(releaseID); releaseID != "" {
			protected[releaseID] = true
		}
	}
	return protected
}
