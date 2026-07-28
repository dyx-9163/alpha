package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func (s *Store) SaveAppReleaseArtifact(artifact AppReleaseArtifact) (AppReleaseArtifact, error) {
	artifact = normalizeAppReleaseArtifact(artifact)
	if artifact.InstanceID == "" || artifact.ReleaseID == "" || artifact.App == "" || artifact.ArtifactType == "" || artifact.Name == "" {
		return AppReleaseArtifact{}, sql.ErrNoRows
	}
	if artifact.ID == "" {
		artifact.ID = NewID("artifact")
	}
	if artifact.CreatedAt.IsZero() {
		artifact.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`insert into app_release_artifacts(id,instance_id,release_id,app,service_name,artifact_type,name,version,checksum,size,path,metadata,created_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		artifact.ID, artifact.InstanceID, artifact.ReleaseID, artifact.App, artifact.ServiceName, artifact.ArtifactType, artifact.Name,
		artifact.Version, artifact.Checksum, artifact.Size, artifact.Path, artifact.Metadata, artifact.CreatedAt)
	return artifact, err
}

func (s *Store) ListAppReleaseArtifacts(instanceID, releaseID string) ([]AppReleaseArtifact, error) {
	rows, err := s.db.Query(`select id,instance_id,release_id,app,coalesce(service_name,''),artifact_type,name,coalesce(version,''),coalesce(checksum,''),size,coalesce(path,''),coalesce(metadata,'{}'),created_at
		from app_release_artifacts where instance_id=? and release_id=? order by service_name, artifact_type, name`, strings.TrimSpace(instanceID), strings.TrimSpace(releaseID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppReleaseArtifact{}
	for rows.Next() {
		var artifact AppReleaseArtifact
		if err := rows.Scan(&artifact.ID, &artifact.InstanceID, &artifact.ReleaseID, &artifact.App, &artifact.ServiceName, &artifact.ArtifactType, &artifact.Name, &artifact.Version, &artifact.Checksum, &artifact.Size, &artifact.Path, &artifact.Metadata, &artifact.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, artifact)
	}
	return out, rows.Err()
}

func (s *Store) SaveAppReleaseSnapshot(snapshot AppReleaseSnapshot) (AppReleaseSnapshot, error) {
	snapshot = normalizeAppReleaseSnapshot(snapshot)
	if snapshot.InstanceID == "" || snapshot.ReleaseID == "" || snapshot.App == "" || snapshot.SnapshotKind == "" {
		return AppReleaseSnapshot{}, sql.ErrNoRows
	}
	if snapshot.ID == "" {
		snapshot.ID = NewID("snapshot")
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now()
	}
	_, err := s.db.Exec(`insert into app_release_snapshots(id,instance_id,release_id,app,snapshot_kind,status,payload_json,checksum,metadata,created_at,restored_at)
		values(?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id,release_id,snapshot_kind) do update set
		status=excluded.status,payload_json=excluded.payload_json,checksum=excluded.checksum,metadata=excluded.metadata,restored_at=excluded.restored_at`,
		snapshot.ID, snapshot.InstanceID, snapshot.ReleaseID, snapshot.App, snapshot.SnapshotKind, snapshot.Status, snapshot.PayloadJSON, snapshot.Checksum, snapshot.Metadata, snapshot.CreatedAt, nullableTime(snapshot.RestoredAt))
	return snapshot, err
}

func (s *Store) ListAppReleaseSnapshots(instanceID, releaseID string) ([]AppReleaseSnapshot, error) {
	rows, err := s.db.Query(`select id,instance_id,release_id,app,snapshot_kind,status,payload_json,coalesce(checksum,''),coalesce(metadata,'{}'),created_at,restored_at
		from app_release_snapshots where instance_id=? and release_id=? order by snapshot_kind`, strings.TrimSpace(instanceID), strings.TrimSpace(releaseID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AppReleaseSnapshot{}
	for rows.Next() {
		var snapshot AppReleaseSnapshot
		var restoredAt sql.NullTime
		if err := rows.Scan(&snapshot.ID, &snapshot.InstanceID, &snapshot.ReleaseID, &snapshot.App, &snapshot.SnapshotKind, &snapshot.Status, &snapshot.PayloadJSON, &snapshot.Checksum, &snapshot.Metadata, &snapshot.CreatedAt, &restoredAt); err != nil {
			return nil, err
		}
		snapshot.RestoredAt = nullTime(restoredAt)
		out = append(out, snapshot)
	}
	return out, rows.Err()
}

func (s *Store) SaveAppBackup(backup AppBackup) (AppBackup, error) {
	backup = normalizeAppBackup(backup)
	if backup.App == "" || backup.BackupType == "" {
		return AppBackup{}, sql.ErrNoRows
	}
	if backup.ID == "" {
		backup.ID = NewID("backup")
	}
	if backup.CreatedAt.IsZero() {
		backup.CreatedAt = time.Now()
	}
	result, err := s.db.Exec(`insert into app_backups(id,app,instance_id,server_id,backup_type,status,path,checksum,size,task_id,metadata,created_at,completed_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(id) do update set
		status=excluded.status,path=excluded.path,checksum=excluded.checksum,size=excluded.size,task_id=excluded.task_id,metadata=excluded.metadata,completed_at=excluded.completed_at
		where (lower(app_backups.status)=lower(excluded.status) and lower(app_backups.status)<>'deleted')
			or (lower(app_backups.status)='pending' and lower(excluded.status) in ('running','success','failed'))
			or (lower(app_backups.status)='running' and lower(excluded.status) in ('success','failed'))
			or lower(app_backups.status) not in ('pending','running','success','failed','deleted')`,
		backup.ID, backup.App, backup.InstanceID, backup.ServerID, backup.BackupType, backup.Status, backup.Path, backup.Checksum, backup.Size, backup.TaskID, backup.Metadata, backup.CreatedAt, nullableTime(backup.CompletedAt))
	if err != nil {
		return AppBackup{}, err
	}
	if rows, err := result.RowsAffected(); err != nil {
		return AppBackup{}, err
	} else if rows == 0 {
		return s.GetAppBackup(backup.ID)
	}
	return s.GetAppBackup(backup.ID)
}

func (s *Store) ListAppBackups(instanceID string) ([]AppBackup, error) {
	query := `select id,app,coalesce(instance_id,''),coalesce(server_id,''),backup_type,status,coalesce(path,''),coalesce(checksum,''),size,coalesce(task_id,''),coalesce(metadata,'{}'),created_at,completed_at from app_backups`
	args := []any{}
	if strings.TrimSpace(instanceID) != "" {
		query += ` where instance_id=?`
		args = append(args, strings.TrimSpace(instanceID))
	}
	query += ` order by created_at desc`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAppBackups(rows)
}

func (s *Store) GetAppBackup(id string) (AppBackup, error) {
	var backup AppBackup
	var completedAt sql.NullTime
	err := s.db.QueryRow(`select id,app,coalesce(instance_id,''),coalesce(server_id,''),backup_type,status,coalesce(path,''),coalesce(checksum,''),size,coalesce(task_id,''),coalesce(metadata,'{}'),created_at,completed_at
		from app_backups where id=?`, strings.TrimSpace(id)).
		Scan(&backup.ID, &backup.App, &backup.InstanceID, &backup.ServerID, &backup.BackupType, &backup.Status, &backup.Path, &backup.Checksum, &backup.Size, &backup.TaskID, &backup.Metadata, &backup.CreatedAt, &completedAt)
	if err != nil {
		return AppBackup{}, err
	}
	backup.CompletedAt = nullTime(completedAt)
	return backup, nil
}

func (s *Store) ListAppBackupsForInstances(instanceIDs []string, includeDeleted bool) ([]AppBackup, error) {
	ids := normalizeAppBackupInstanceIDs(instanceIDs)
	if len(ids) == 0 {
		return []AppBackup{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, 0, len(ids)+1)
	for index, id := range ids {
		placeholders[index] = "?"
		args = append(args, id)
	}
	query := `select id,app,coalesce(instance_id,''),coalesce(server_id,''),backup_type,status,coalesce(path,''),coalesce(checksum,''),size,coalesce(task_id,''),coalesce(metadata,'{}'),created_at,completed_at
		from app_backups where instance_id in (` + strings.Join(placeholders, ",") + `)`
	if !includeDeleted {
		query += ` and status <> ?`
		args = append(args, "deleted")
	}
	query += ` order by created_at desc, id desc`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanAppBackups(rows)
}

func (s *Store) MarkAppBackupDeleted(id string, completedAt time.Time) (AppBackup, error) {
	backup, err := s.GetAppBackup(id)
	if err != nil {
		return AppBackup{}, err
	}
	if backup.Status == "deleted" {
		return backup, nil
	}
	if !appBackupStatusTransitionAllowed(backup.Status, "deleted") {
		return AppBackup{}, fmt.Errorf("backup status %q cannot transition to deleted", backup.Status)
	}
	if completedAt.IsZero() {
		completedAt = time.Now()
	}
	if _, err := s.db.Exec(`update app_backups set status='deleted', completed_at=? where id=?`, completedAt, backup.ID); err != nil {
		return AppBackup{}, err
	}
	return s.GetAppBackup(backup.ID)
}

func scanAppBackups(rows *sql.Rows) ([]AppBackup, error) {
	out := []AppBackup{}
	for rows.Next() {
		var backup AppBackup
		var completedAt sql.NullTime
		if err := rows.Scan(&backup.ID, &backup.App, &backup.InstanceID, &backup.ServerID, &backup.BackupType, &backup.Status, &backup.Path, &backup.Checksum, &backup.Size, &backup.TaskID, &backup.Metadata, &backup.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		backup.CompletedAt = nullTime(completedAt)
		out = append(out, backup)
	}
	return out, rows.Err()
}

func normalizeAppBackupInstanceIDs(instanceIDs []string) []string {
	seen := map[string]struct{}{}
	ids := make([]string, 0, len(instanceIDs))
	for _, id := range instanceIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}

func appBackupStatusTransitionAllowed(current, next string) bool {
	current = strings.ToLower(strings.TrimSpace(current))
	next = strings.ToLower(strings.TrimSpace(next))
	if current == next {
		return true
	}
	switch current {
	case "pending":
		return next == "running" || next == "success" || next == "failed"
	case "running":
		return next == "success" || next == "failed"
	case "success", "failed":
		return next == "deleted"
	case "deleted":
		return false
	default:
		return true
	}
}

func deleteAppReleaseAuxiliaryRecordsTx(tx *sql.Tx, instanceID, releaseID string) error {
	for _, stmt := range []string{
		`delete from app_release_artifacts where instance_id=? and release_id=?`,
		`delete from app_release_snapshots where instance_id=? and release_id=?`,
	} {
		if _, err := tx.Exec(stmt, instanceID, releaseID); err != nil {
			return err
		}
	}
	return nil
}

func replaceAppReleaseAuxiliaryRecordsTx(tx *sql.Tx, release AppRelease) error {
	if err := deleteAppReleaseAuxiliaryRecordsTx(tx, release.InstanceID, release.ReleaseID); err != nil {
		return err
	}
	manifest := map[string]any{}
	if strings.TrimSpace(release.ManifestJSON) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(release.ManifestJSON), &manifest); err != nil {
		return nil
	}
	now := release.CreatedAt
	if now.IsZero() {
		now = time.Now()
	}
	if artifacts, ok := releaseManifestMap(manifest["artifacts"]); ok {
		for service, raw := range artifacts {
			item, ok := releaseManifestMap(raw)
			if !ok {
				continue
			}
			name := releaseManifestText(item["file"])
			if name == "" {
				name = service
			}
			artifactType := releaseManifestText(item["type"])
			if artifactType == "" {
				artifactType = "artifact"
			}
			metadata, _ := json.Marshal(item)
			if _, err := tx.Exec(`insert into app_release_artifacts(id,instance_id,release_id,app,service_name,artifact_type,name,version,checksum,size,path,metadata,created_at)
				values(?,?,?,?,?,?,?,?,?,?,?,?,?)`,
				NewID("artifact"), release.InstanceID, release.ReleaseID, release.App, service, artifactType, name, release.Version,
				releaseManifestText(item["sha256"]), releaseManifestInt64(item["size"]), releaseManifestText(item["remotePath"]), normalizeJSONText(string(metadata)), now); err != nil {
				return err
			}
		}
	}
	if snapshots, ok := releaseManifestMap(manifest["snapshots"]); ok {
		if err := insertReleaseSnapshotPathTx(tx, release, "runtime-spec-before", "", releaseManifestText(snapshots["runtimeSpecBefore"]), now); err != nil {
			return err
		}
		if envBefore, ok := releaseManifestMap(snapshots["envBefore"]); ok {
			for service, raw := range envBefore {
				if err := insertReleaseSnapshotPathTx(tx, release, "env-before:"+service, service, releaseManifestText(raw), now); err != nil {
					return err
				}
			}
		}
	}
	for _, key := range []string{"serviceRevisionsBefore", "serviceRevisionsAfter"} {
		if value, ok := manifest[key]; ok {
			payload, _ := json.Marshal(value)
			if _, err := tx.Exec(`insert into app_release_snapshots(id,instance_id,release_id,app,snapshot_kind,status,payload_json,checksum,metadata,created_at,restored_at)
				values(?,?,?,?,?,?,?,?,?,?,?)`,
				NewID("snapshot"), release.InstanceID, release.ReleaseID, release.App, key, release.Status, normalizeJSONText(string(payload)), "", "{}", now, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

func insertReleaseSnapshotPathTx(tx *sql.Tx, release AppRelease, kind, service, path string, createdAt time.Time) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	payload, _ := json.Marshal(map[string]any{
		"service": service,
		"path":    path,
	})
	_, err := tx.Exec(`insert into app_release_snapshots(id,instance_id,release_id,app,snapshot_kind,status,payload_json,checksum,metadata,created_at,restored_at)
		values(?,?,?,?,?,?,?,?,?,?,?)`,
		NewID("snapshot"), release.InstanceID, release.ReleaseID, release.App, kind, release.Status, string(payload), "", "{}", createdAt, nil)
	return err
}

func releaseManifestMap(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[string]string:
		out := make(map[string]any, len(typed))
		for key, value := range typed {
			out[key] = value
		}
		return out, true
	default:
		return nil, false
	}
}

func releaseManifestText(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func releaseManifestInt64(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int64:
		return typed
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case string:
		var parsed int64
		_, _ = fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed)
		return parsed
	default:
		return 0
	}
}

func normalizeAppReleaseArtifact(artifact AppReleaseArtifact) AppReleaseArtifact {
	artifact.ID = strings.TrimSpace(artifact.ID)
	artifact.InstanceID = strings.TrimSpace(artifact.InstanceID)
	artifact.ReleaseID = strings.TrimSpace(artifact.ReleaseID)
	artifact.App = strings.TrimSpace(artifact.App)
	artifact.ServiceName = strings.TrimSpace(artifact.ServiceName)
	artifact.ArtifactType = strings.TrimSpace(artifact.ArtifactType)
	artifact.Name = strings.TrimSpace(artifact.Name)
	artifact.Version = strings.TrimSpace(artifact.Version)
	artifact.Checksum = strings.TrimSpace(artifact.Checksum)
	artifact.Path = strings.TrimSpace(artifact.Path)
	artifact.Metadata = normalizeJSONText(artifact.Metadata)
	return artifact
}

func normalizeAppReleaseSnapshot(snapshot AppReleaseSnapshot) AppReleaseSnapshot {
	snapshot.ID = strings.TrimSpace(snapshot.ID)
	snapshot.InstanceID = strings.TrimSpace(snapshot.InstanceID)
	snapshot.ReleaseID = strings.TrimSpace(snapshot.ReleaseID)
	snapshot.App = strings.TrimSpace(snapshot.App)
	snapshot.SnapshotKind = strings.TrimSpace(snapshot.SnapshotKind)
	snapshot.Status = strings.TrimSpace(snapshot.Status)
	if snapshot.Status == "" {
		snapshot.Status = "captured"
	}
	snapshot.PayloadJSON = normalizeJSONText(snapshot.PayloadJSON)
	snapshot.Checksum = strings.TrimSpace(snapshot.Checksum)
	snapshot.Metadata = normalizeJSONText(snapshot.Metadata)
	return snapshot
}

func normalizeAppBackup(backup AppBackup) AppBackup {
	backup.ID = strings.TrimSpace(backup.ID)
	backup.App = strings.TrimSpace(backup.App)
	backup.InstanceID = strings.TrimSpace(backup.InstanceID)
	backup.ServerID = strings.TrimSpace(backup.ServerID)
	backup.BackupType = strings.TrimSpace(backup.BackupType)
	backup.Status = strings.TrimSpace(backup.Status)
	if backup.Status == "" {
		backup.Status = "pending"
	}
	backup.Path = strings.TrimSpace(backup.Path)
	backup.Checksum = strings.TrimSpace(backup.Checksum)
	backup.TaskID = strings.TrimSpace(backup.TaskID)
	backup.Metadata = normalizeJSONText(backup.Metadata)
	return backup
}

func normalizeJSONText(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "{}"
	}
	return value
}
