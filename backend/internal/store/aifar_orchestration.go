package store

import (
	"database/sql"
	"strings"
	"time"
)

func (s *Store) SaveAIFARDeployment(v AIFARDeployment) (AIFARDeployment, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = NewID("aifardeploy")
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	if v.DesiredReplicas < 1 {
		v.DesiredReplicas = 1
	}
	_, err := s.db.Exec(`insert into aifar_deployments(id,instance_id,service_name,desired_replicas,current_revision,updating_revision,strategy_json,status,metadata_json,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id, service_name) do update set
		desired_replicas=excluded.desired_replicas,current_revision=excluded.current_revision,updating_revision=excluded.updating_revision,
		strategy_json=excluded.strategy_json,status=excluded.status,metadata_json=excluded.metadata_json,updated_at=excluded.updated_at`,
		v.ID, v.InstanceID, v.ServiceName, v.DesiredReplicas, v.CurrentRevision, v.UpdatingRevision, v.StrategyJSON, v.Status, v.MetadataJSON, v.CreatedAt, v.UpdatedAt)
	return v, err
}

func (s *Store) ListAIFARDeployments(instanceID string) ([]AIFARDeployment, error) {
	rows, err := s.db.Query(`select id,instance_id,service_name,desired_replicas,current_revision,coalesce(updating_revision,''),coalesce(strategy_json,''),status,coalesce(metadata_json,''),created_at,updated_at
		from aifar_deployments where instance_id=? order by service_name`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIFARDeployment{}
	for rows.Next() {
		var v AIFARDeployment
		if err := rows.Scan(&v.ID, &v.InstanceID, &v.ServiceName, &v.DesiredReplicas, &v.CurrentRevision, &v.UpdatingRevision, &v.StrategyJSON, &v.Status, &v.MetadataJSON, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SaveAIFARReplicaSet(v AIFARReplicaSet) (AIFARReplicaSet, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = NewID("aifarrs")
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	if v.DesiredPods < 1 {
		v.DesiredPods = 1
	}
	_, err := s.db.Exec(`insert into aifar_replicasets(id,instance_id,service_name,revision,image,artifact_hash,desired_pods,ready_pods,status,metadata_json,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id, service_name, revision) do update set
		image=excluded.image,artifact_hash=excluded.artifact_hash,desired_pods=excluded.desired_pods,
		ready_pods=excluded.ready_pods,status=excluded.status,metadata_json=excluded.metadata_json,updated_at=excluded.updated_at`,
		v.ID, v.InstanceID, v.ServiceName, v.Revision, v.Image, v.ArtifactHash, v.DesiredPods, v.ReadyPods, v.Status, v.MetadataJSON, v.CreatedAt, v.UpdatedAt)
	return v, err
}

func (s *Store) ListAIFARReplicaSets(instanceID string) ([]AIFARReplicaSet, error) {
	rows, err := s.db.Query(`select id,instance_id,service_name,revision,image,coalesce(artifact_hash,''),desired_pods,ready_pods,status,coalesce(metadata_json,''),created_at,updated_at
		from aifar_replicasets where instance_id=? order by created_at desc`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIFARReplicaSet{}
	for rows.Next() {
		var v AIFARReplicaSet
		if err := rows.Scan(&v.ID, &v.InstanceID, &v.ServiceName, &v.Revision, &v.Image, &v.ArtifactHash, &v.DesiredPods, &v.ReadyPods, &v.Status, &v.MetadataJSON, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SaveAIFARPod(v AIFARPod) (AIFARPod, error) {
	now := time.Now()
	if v.ID == "" {
		v.ID = NewID("aifarpod")
		v.CreatedAt = now
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	_, err := s.db.Exec(`insert into aifar_pods(id,instance_id,service_name,revision,pod_id,container_name,port,status,ready,metadata_json,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id, service_name, pod_id) do update set
		revision=excluded.revision,container_name=excluded.container_name,port=excluded.port,status=excluded.status,
		ready=excluded.ready,metadata_json=excluded.metadata_json,updated_at=excluded.updated_at`,
		v.ID, v.InstanceID, v.ServiceName, v.Revision, v.PodID, v.ContainerName, v.Port, v.Status, boolInt(v.Ready), v.MetadataJSON, v.CreatedAt, v.UpdatedAt)
	return v, err
}

func (s *Store) ListAIFARPods(instanceID string) ([]AIFARPod, error) {
	rows, err := s.db.Query(`select id,instance_id,service_name,revision,pod_id,container_name,port,status,ready,coalesce(metadata_json,''),created_at,updated_at
		from aifar_pods where instance_id=? order by service_name, pod_id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIFARPod{}
	for rows.Next() {
		var v AIFARPod
		var ready int
		if err := rows.Scan(&v.ID, &v.InstanceID, &v.ServiceName, &v.Revision, &v.PodID, &v.ContainerName, &v.Port, &v.Status, &ready, &v.MetadataJSON, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.Ready = ready != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ReplaceAIFARServiceEndpoints(instanceID, serviceName string, endpoints []AIFARServiceEndpoint) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`delete from aifar_service_endpoints where instance_id=? and service_name=?`, instanceID, serviceName); err != nil {
		return err
	}
	for _, v := range endpoints {
		now := time.Now()
		if v.ID == "" {
			v.ID = NewID("aifarendp")
		}
		if v.CreatedAt.IsZero() {
			v.CreatedAt = now
		}
		v.UpdatedAt = now
		if _, err := tx.Exec(`insert into aifar_service_endpoints(id,instance_id,service_name,pod_id,container_name,revision,port,state,ready,metadata_json,created_at,updated_at)
			values(?,?,?,?,?,?,?,?,?,?,?,?)`,
			v.ID, instanceID, serviceName, v.PodID, v.ContainerName, v.Revision, v.Port, v.State, boolInt(v.Ready), v.MetadataJSON, v.CreatedAt, v.UpdatedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) ListAIFARServiceEndpoints(instanceID string) ([]AIFARServiceEndpoint, error) {
	rows, err := s.db.Query(`select id,instance_id,service_name,pod_id,container_name,revision,port,state,ready,coalesce(metadata_json,''),created_at,updated_at
		from aifar_service_endpoints where instance_id=? order by service_name, pod_id`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIFARServiceEndpoint{}
	for rows.Next() {
		var v AIFARServiceEndpoint
		var ready int
		if err := rows.Scan(&v.ID, &v.InstanceID, &v.ServiceName, &v.PodID, &v.ContainerName, &v.Revision, &v.Port, &v.State, &ready, &v.MetadataJSON, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		v.Ready = ready != 0
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) PruneAIFARPodRecords(instanceID string, existingContainerNames []string) (int, error) {
	return s.pruneAIFARContainerRecords("aifar_pods", instanceID, existingContainerNames)
}

func (s *Store) PruneAIFARServiceEndpointRecords(instanceID string, existingContainerNames []string) (int, error) {
	return s.pruneAIFARContainerRecords("aifar_service_endpoints", instanceID, existingContainerNames)
}

func (s *Store) pruneAIFARContainerRecords(table, instanceID string, existingContainerNames []string) (int, error) {
	names := uniqueNonEmpty(existingContainerNames)
	var (
		result sql.Result
		err    error
	)
	if len(names) == 0 {
		result, err = s.db.Exec(`delete from `+table+` where instance_id=?`, instanceID)
	} else {
		placeholders := strings.TrimRight(strings.Repeat("?,", len(names)), ",")
		args := make([]any, 0, len(names)+1)
		args = append(args, instanceID)
		for _, name := range names {
			args = append(args, name)
		}
		result, err = s.db.Exec(`delete from `+table+` where instance_id=? and container_name not in (`+placeholders+`)`, args...)
	}
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func uniqueNonEmpty(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func (s *Store) DeleteAIFAROrchestration(instanceID string) error {
	for _, stmt := range []string{
		`delete from aifar_service_endpoints where instance_id=?`,
		`delete from aifar_pods where instance_id=?`,
		`delete from aifar_replicasets where instance_id=?`,
		`delete from aifar_deployments where instance_id=?`,
	} {
		if _, err := s.db.Exec(stmt, instanceID); err != nil {
			if err == sql.ErrNoRows {
				continue
			}
			return err
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
