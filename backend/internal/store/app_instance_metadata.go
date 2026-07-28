package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// MySQLMaintenanceMarker is the deliberately small, non-secret restore
// safety record. It lives under app_instances.metadata.mysqlMaintenance.
type MySQLMaintenanceMarker struct {
	Version      int       `json:"version"`
	State        string    `json:"state"`
	Reason       string    `json:"reason"`
	Scope        string    `json:"scope"`
	ClusterID    string    `json:"clusterId,omitempty"`
	BackupID     string    `json:"backupId"`
	TaskID       string    `json:"taskId"`
	RestorePhase string    `json:"restorePhase"`
	RecordedAt   time.Time `json:"recordedAt"`
}

// MySQLDisasterRebuildCompletion is the controlled, non-secret payload used
// to atomically publish a rebuilt cluster and clear its maintenance markers.
type MySQLDisasterRebuildCompletion struct {
	ClusterID      string            `json:"clusterId"`
	SourceBackupID string            `json:"sourceBackupId"`
	TaskID         string            `json:"taskId"`
	Generation     int               `json:"generation"`
	Roles          map[string]string `json:"roles"`
	CompletedAt    time.Time         `json:"completedAt"`
}

type mysqlDisasterRebuildProgress struct {
	Version           int               `json:"version"`
	TaskID            string            `json:"taskId"`
	SourceBackupID    string            `json:"sourceBackupId"`
	ClusterID         string            `json:"clusterId"`
	RestoreGeneration int               `json:"restoreGeneration"`
	QuarantinePaths   map[string]string `json:"quarantinePaths"`
	SeedStage         string            `json:"seedStage"`
	MemberStages      map[string]string `json:"memberStages"`
	RouterStage       string            `json:"routerStage"`
	RouterStages      map[string]string `json:"routerStages"`
	CompletionStage   string            `json:"completionStage,omitempty"`
}

var (
	controlledMaintenanceInstanceID = regexp.MustCompile(`^app_[0-9a-f]{24}$`)
	controlledMaintenanceBackupID   = regexp.MustCompile(`^backup_[0-9a-f]{24}$`)
	controlledMaintenanceTaskID     = regexp.MustCompile(`^tsk_[0-9a-f]{24}$`)
	controlledMaintenanceClusterID  = regexp.MustCompile(`^cluster_[0-9a-f]{24}$`)
)

// ParseMySQLMaintenanceMarker validates only the nested marker. Other
// metadata belongs to the instance and intentionally remains forward-compatible.
func ParseMySQLMaintenanceMarker(raw string) (MySQLMaintenanceMarker, bool, error) {
	metadata, err := appInstanceMetadata(raw)
	if err != nil {
		return MySQLMaintenanceMarker{}, false, err
	}
	value, present := metadata["mysqlMaintenance"]
	if !present {
		return MySQLMaintenanceMarker{}, false, nil
	}
	decoder := json.NewDecoder(strings.NewReader(string(value)))
	decoder.DisallowUnknownFields()
	var marker MySQLMaintenanceMarker
	if err := decoder.Decode(&marker); err != nil {
		return MySQLMaintenanceMarker{}, true, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return MySQLMaintenanceMarker{}, true, errors.New("MySQL maintenance marker must be one object")
	}
	if err := validateMySQLMaintenanceMarker(marker); err != nil {
		return MySQLMaintenanceMarker{}, true, err
	}
	return marker, true, nil
}

func validateMySQLMaintenanceMarker(marker MySQLMaintenanceMarker) error {
	if marker.Version != 1 || marker.State != "required" || marker.Reason != "restore_incomplete" ||
		(marker.Scope != "standalone" && marker.Scope != "cluster") ||
		!controlledMaintenanceBackupID.MatchString(marker.BackupID) || !controlledMaintenanceTaskID.MatchString(marker.TaskID) ||
		(marker.RestorePhase != "schema_mutation_started" && marker.RestorePhase != "load_complete") ||
		marker.RecordedAt.IsZero() || marker.RecordedAt.Location() != time.UTC {
		return errors.New("invalid MySQL maintenance marker")
	}
	if marker.Scope == "cluster" {
		if !controlledMaintenanceClusterID.MatchString(marker.ClusterID) {
			return errors.New("invalid MySQL maintenance cluster marker")
		}
	} else if marker.ClusterID != "" {
		return errors.New("standalone MySQL maintenance marker has cluster ID")
	}
	return nil
}

// SetMySQLMaintenance atomically verifies the authoritative topology and
// writes one identical marker to every affected MySQL instance.
func (s *Store) SetMySQLMaintenance(instanceIDs []string, marker MySQLMaintenanceMarker) error {
	return s.mutateMySQLMaintenance(instanceIDs, marker, "set", "")
}

// AdvanceMySQLMaintenance atomically moves an existing owned marker phase.
func (s *Store) AdvanceMySQLMaintenance(instanceIDs []string, marker MySQLMaintenanceMarker, phase string) error {
	return s.mutateMySQLMaintenance(instanceIDs, marker, "advance", phase)
}

// ClearMySQLMaintenance atomically removes only an identical owned marker.
func (s *Store) ClearMySQLMaintenance(instanceIDs []string, marker MySQLMaintenanceMarker) error {
	return s.mutateMySQLMaintenance(instanceIDs, marker, "clear", "")
}

// CompleteMySQLDisasterRebuild performs the final control-plane publication
// and maintenance clear in one transaction. No caller-visible success can be
// reported unless all three markers and authoritative member rows still match.
func (s *Store) CompleteMySQLDisasterRebuild(instanceIDs []string, marker MySQLMaintenanceMarker, completion MySQLDisasterRebuildCompletion) error {
	if err := validateMySQLMaintenanceMarker(marker); err != nil {
		return err
	}
	if marker.Scope != "cluster" || completion.ClusterID != marker.ClusterID || completion.SourceBackupID != marker.BackupID ||
		!controlledMaintenanceTaskID.MatchString(completion.TaskID) || completion.Generation < 1 || completion.CompletedAt.IsZero() || completion.CompletedAt.Location() != time.UTC {
		return errors.New("invalid MySQL disaster rebuild completion")
	}
	ids := append([]string(nil), instanceIDs...)
	sort.Strings(ids)
	if len(ids) != 3 || len(completion.Roles) != 3 {
		return errors.New("MySQL disaster rebuild requires exactly three members")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	instances := make([]AppInstance, 0, 3)
	for index, id := range ids {
		if !controlledMaintenanceInstanceID.MatchString(id) || (index > 0 && ids[index-1] == id) {
			return errors.New("invalid MySQL disaster rebuild member")
		}
		var instance AppInstance
		if err := tx.QueryRow(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances where id=?`, id).Scan(&instance.ID, &instance.App, &instance.Version, &instance.ServerID, &instance.Status, &instance.Topology, &instance.Metadata, &instance.CreatedAt, &instance.UpdatedAt); err != nil {
			return err
		}
		if instance.App != "mysql" {
			return errors.New("MySQL disaster rebuild member ownership changed")
		}
		existing, present, err := ParseMySQLMaintenanceMarker(instance.Metadata)
		if err != nil || !present || !sameMySQLMaintenanceMarker(existing, marker) || validateMaintenanceTopology(instance, marker) != nil {
			return errors.New("MySQL disaster rebuild maintenance ownership changed")
		}
		role := strings.ToUpper(strings.TrimSpace(completion.Roles[id]))
		if role != "PRIMARY" && role != "SECONDARY" {
			return errors.New("invalid MySQL disaster rebuild member role")
		}
		instances = append(instances, instance)
	}
	if err := validateAuthoritativeMySQLMaintenanceClusterTx(tx, ids, instances, completion.ClusterID); err != nil {
		return err
	}
	if err := completeMySQLDisasterBackupTx(tx, completion); err != nil {
		return err
	}
	primaryCount := 0
	for _, instance := range instances {
		role := strings.ToUpper(strings.TrimSpace(completion.Roles[instance.ID]))
		if role == "PRIMARY" {
			primaryCount++
		}
		metadata, err := appInstanceMetadata(instance.Metadata)
		if err != nil {
			return err
		}
		delete(metadata, "mysqlMaintenance")
		metadata["role"], _ = json.Marshal(strings.ToLower(role))
		metadata["mysqlDisasterRestore"], _ = json.Marshal(map[string]any{
			"version": 1, "generation": completion.Generation, "sourceBackupId": completion.SourceBackupID,
			"taskId": completion.TaskID, "completedAt": completion.CompletedAt, "role": role, "status": "ONLINE",
		})
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`update app_instances set status='running',metadata=?,updated_at=? where id=?`, string(encoded), completion.CompletedAt, instance.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`update app_cluster_members set role=?,status='ONLINE',updated_at=? where cluster_id=? and instance_id=?`, role, completion.CompletedAt, completion.ClusterID, instance.ID); err != nil {
			return err
		}
	}
	if primaryCount != 1 {
		return errors.New("MySQL disaster rebuild must publish one PRIMARY")
	}
	var clusterMetadata string
	if err := tx.QueryRow(`select coalesce(metadata,'{}') from app_clusters where id=?`, completion.ClusterID).Scan(&clusterMetadata); err != nil {
		return err
	}
	metadata, err := appInstanceMetadata(clusterMetadata)
	if err != nil {
		return err
	}
	previousGeneration := 0
	if rawPrevious, present := metadata["mysqlDisasterRestore"]; present {
		var previous struct {
			Version    int `json:"version"`
			Generation int `json:"generation"`
		}
		if json.Unmarshal(rawPrevious, &previous) != nil || previous.Version != 1 || previous.Generation < 1 {
			return errors.New("invalid previous MySQL disaster rebuild generation")
		}
		previousGeneration = previous.Generation
	}
	if previousGeneration == int(^uint(0)>>1) || completion.Generation != previousGeneration+1 {
		return errors.New("MySQL disaster rebuild generation changed")
	}
	metadata["mysqlDisasterRestore"], _ = json.Marshal(map[string]any{
		"version": 1, "generation": completion.Generation, "sourceBackupId": completion.SourceBackupID,
		"taskId": completion.TaskID, "completedAt": completion.CompletedAt,
	})
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`update app_clusters set status='active',metadata=?,updated_at=? where id=?`, string(encoded), completion.CompletedAt, completion.ClusterID); err != nil {
		return err
	}
	return tx.Commit()
}

func completeMySQLDisasterBackupTx(tx *sql.Tx, completion MySQLDisasterRebuildCompletion) error {
	var app, backupType, status, raw string
	if err := tx.QueryRow(`select app,backup_type,status,coalesce(metadata,'{}') from app_backups where id=?`, completion.SourceBackupID).Scan(&app, &backupType, &status, &raw); err != nil {
		return err
	}
	if app != "mysql" || backupType != "logical-full" || status != "success" {
		return errors.New("MySQL disaster rebuild backup ownership changed")
	}
	metadata, err := appInstanceMetadata(raw)
	if err != nil {
		return err
	}
	var restoreTaskID string
	if value, present := metadata["restoreTaskId"]; !present || json.Unmarshal(value, &restoreTaskID) != nil || restoreTaskID != completion.TaskID {
		return errors.New("MySQL disaster rebuild backup task changed")
	}
	rawProgress, present := metadata["disasterRebuild"]
	if !present {
		return errors.New("MySQL disaster rebuild progress missing")
	}
	decoder := json.NewDecoder(strings.NewReader(string(rawProgress)))
	decoder.DisallowUnknownFields()
	var progress mysqlDisasterRebuildProgress
	if err := decoder.Decode(&progress); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("MySQL disaster rebuild progress must be one object")
	}
	if progress.Version != 1 || progress.TaskID != completion.TaskID || progress.SourceBackupID != completion.SourceBackupID ||
		progress.ClusterID != completion.ClusterID || progress.RestoreGeneration != completion.Generation ||
		progress.SeedStage != "verified" || progress.RouterStage != "verified" || progress.CompletionStage != "" {
		return errors.New("MySQL disaster rebuild progress is not ready for completion")
	}
	progress.CompletionStage = "completed"
	metadata["restorePhase"], _ = json.Marshal("verified")
	metadata["disasterRebuild"], err = json.Marshal(progress)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	result, err := tx.Exec(`update app_backups set metadata=? where id=? and status='success'`, string(encoded), completion.SourceBackupID)
	if err != nil {
		return err
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		return errors.New("MySQL disaster rebuild backup completion changed")
	}
	return nil
}

func (s *Store) mutateMySQLMaintenance(instanceIDs []string, marker MySQLMaintenanceMarker, action, phase string) error {
	if err := validateMySQLMaintenanceMarker(marker); err != nil {
		return err
	}
	if action == "advance" && phase != "load_complete" {
		return errors.New("invalid MySQL maintenance phase")
	}
	ids := append([]string(nil), instanceIDs...)
	sort.Strings(ids)
	if len(ids) == 0 {
		return errors.New("MySQL maintenance instances are required")
	}
	for i, id := range ids {
		if !controlledMaintenanceInstanceID.MatchString(id) || (i > 0 && ids[i-1] == id) {
			return errors.New("invalid MySQL maintenance instance ID")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	instances := make([]AppInstance, 0, len(ids))
	for _, id := range ids {
		var instance AppInstance
		if err := tx.QueryRow(`select id,app,version,server_id,status,topology,metadata,created_at,updated_at from app_instances where id=?`, id).Scan(&instance.ID, &instance.App, &instance.Version, &instance.ServerID, &instance.Status, &instance.Topology, &instance.Metadata, &instance.CreatedAt, &instance.UpdatedAt); err != nil {
			return err
		}
		if instance.App != "mysql" {
			return errors.New("MySQL maintenance instance ownership changed")
		}
		if err := validateMaintenanceTopology(instance, marker); err != nil {
			return err
		}
		instances = append(instances, instance)
	}
	if marker.Scope == "cluster" && len(instances) != 3 {
		return errors.New("MySQL maintenance cluster must have exactly three instances")
	}
	if marker.Scope == "cluster" {
		if err := validateAuthoritativeMySQLMaintenanceClusterTx(tx, ids, instances, marker.ClusterID); err != nil {
			return err
		}
	}
	if marker.Scope == "standalone" && len(instances) != 1 {
		return errors.New("MySQL maintenance standalone must have one instance")
	}
	for _, instance := range instances {
		metadata, err := appInstanceMetadata(instance.Metadata)
		if err != nil {
			return err
		}
		existing, present, err := ParseMySQLMaintenanceMarker(instance.Metadata)
		if err != nil {
			return err
		}
		switch action {
		case "set":
			if present {
				return errors.New("MySQL maintenance marker already exists")
			}
			metadata["mysqlMaintenance"], _ = json.Marshal(marker)
		case "advance":
			if !present || !sameMySQLMaintenanceMarker(existing, marker) {
				return errors.New("MySQL maintenance marker ownership changed")
			}
			existing.RestorePhase = phase
			metadata["mysqlMaintenance"], _ = json.Marshal(existing)
		case "clear":
			if !present || !sameMySQLMaintenanceMarker(existing, marker) {
				return errors.New("MySQL maintenance marker ownership changed")
			}
			delete(metadata, "mysqlMaintenance")
		default:
			return errors.New("invalid MySQL maintenance mutation")
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`update app_instances set metadata=?,updated_at=? where id=?`, string(encoded), time.Now().UTC(), instance.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func validateAuthoritativeMySQLMaintenanceClusterTx(tx *sql.Tx, ids []string, instances []AppInstance, clusterID string) error {
	var app, topology string
	if err := tx.QueryRow(`select app,topology from app_clusters where id=?`, clusterID).Scan(&app, &topology); err != nil || app != "mysql" || strings.TrimSpace(topology) != "innodb-cluster" {
		return errors.New("MySQL maintenance authoritative cluster changed")
	}
	rows, err := tx.Query(`select instance_id,coalesce(server_id,'') from app_cluster_members where cluster_id=? order by instance_id`, clusterID)
	if err != nil {
		return err
	}
	defer rows.Close()
	members := map[string]string{}
	for rows.Next() {
		var instanceID, serverID string
		if err := rows.Scan(&instanceID, &serverID); err != nil {
			return err
		}
		if _, duplicate := members[instanceID]; duplicate {
			return errors.New("MySQL maintenance authoritative member duplicate")
		}
		members[instanceID] = serverID
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(members) != 3 || len(ids) != 3 {
		return errors.New("MySQL maintenance authoritative member count changed")
	}
	for _, instance := range instances {
		serverID, present := members[instance.ID]
		if !present || serverID != instance.ServerID {
			return errors.New("MySQL maintenance authoritative member ownership changed")
		}
	}
	return nil
}

func validateMaintenanceTopology(instance AppInstance, marker MySQLMaintenanceMarker) error {
	if marker.Scope == "standalone" {
		if strings.TrimSpace(instance.Topology) != "standalone" {
			return errors.New("MySQL maintenance topology changed")
		}
		return nil
	}
	if strings.TrimSpace(instance.Topology) != "innodb-cluster" {
		return errors.New("MySQL maintenance topology changed")
	}
	metadata, err := appInstanceMetadata(instance.Metadata)
	if err != nil {
		return err
	}
	var clusterID string
	if raw, ok := metadata["clusterId"]; !ok || json.Unmarshal(raw, &clusterID) != nil || clusterID != marker.ClusterID {
		return errors.New("MySQL maintenance cluster ownership changed")
	}
	return nil
}

func appInstanceMetadata(raw string) (map[string]json.RawMessage, error) {
	decoder := json.NewDecoder(strings.NewReader(raw))
	metadata := map[string]json.RawMessage{}
	if err := decoder.Decode(&metadata); err != nil || metadata == nil {
		return nil, errors.New("app instance metadata must be one object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, errors.New("app instance metadata must be one object")
	}
	return metadata, nil
}

func sameMySQLMaintenanceOwner(left, right MySQLMaintenanceMarker) bool {
	return left.Version == right.Version && left.State == right.State && left.Reason == right.Reason && left.Scope == right.Scope && left.ClusterID == right.ClusterID && left.BackupID == right.BackupID && left.TaskID == right.TaskID
}

func sameMySQLMaintenanceMarker(left, right MySQLMaintenanceMarker) bool {
	return sameMySQLMaintenanceOwner(left, right) && left.RestorePhase == right.RestorePhase && left.RecordedAt.Equal(right.RecordedAt)
}

func (m MySQLMaintenanceMarker) String() string { return fmt.Sprintf("%s/%s", m.Scope, m.BackupID) }
