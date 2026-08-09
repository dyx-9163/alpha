package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrAIFARDeploymentGenerationConflict = errors.New("AIFAR deployment generation conflict")
	ErrAIFARDeploymentNotFound           = errors.New("AIFAR deployment not found")
)

type AIFAROrchestrationLockConflict struct {
	Lock AIFAROrchestrationLock
}

func (e AIFAROrchestrationLockConflict) Error() string {
	scope := strings.TrimSpace(e.Lock.ServiceName)
	if scope == "" {
		scope = "instance"
	}
	return fmt.Sprintf("active AIFAR orchestration lock exists for %s", scope)
}

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
	if v.DesiredReplicas < 0 {
		v.DesiredReplicas = 0
	}
	if v.Generation <= 0 {
		v.Generation = 1
	}
	_, err := s.db.Exec(`insert into aifar_deployments(id,instance_id,service_name,desired_replicas,current_revision,updating_revision,strategy_json,spec_json,generation,observed_generation,status,metadata_json,conditions_json,last_transition_at,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		on conflict(instance_id, service_name) do update set
		desired_replicas=excluded.desired_replicas,current_revision=excluded.current_revision,updating_revision=excluded.updating_revision,
		strategy_json=excluded.strategy_json,spec_json=case when excluded.spec_json <> '' then excluded.spec_json else aifar_deployments.spec_json end,
		generation=case when excluded.generation > aifar_deployments.generation then excluded.generation else aifar_deployments.generation end,
		observed_generation=case when excluded.observed_generation > aifar_deployments.observed_generation then excluded.observed_generation else aifar_deployments.observed_generation end,
		status=excluded.status,metadata_json=excluded.metadata_json,
		conditions_json=case when excluded.conditions_json <> '' then excluded.conditions_json else aifar_deployments.conditions_json end,
		last_transition_at=coalesce(excluded.last_transition_at,aifar_deployments.last_transition_at),updated_at=excluded.updated_at`,
		v.ID, v.InstanceID, v.ServiceName, v.DesiredReplicas, v.CurrentRevision, v.UpdatingRevision, v.StrategyJSON, v.SpecJSON, v.Generation, v.ObservedGeneration, v.Status, v.MetadataJSON, v.ConditionsJSON, nullableTime(v.LastTransitionAt), v.CreatedAt, v.UpdatedAt)
	return v, err
}

func (s *Store) ListAIFARDeployments(instanceID string) ([]AIFARDeployment, error) {
	rows, err := s.db.Query(`select id,instance_id,service_name,desired_replicas,current_revision,coalesce(updating_revision,''),coalesce(strategy_json,''),coalesce(spec_json,''),coalesce(generation,1),coalesce(observed_generation,0),status,coalesce(metadata_json,''),coalesce(conditions_json,''),last_transition_at,created_at,updated_at
		from aifar_deployments where instance_id=? order by service_name`, instanceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIFARDeployment{}
	for rows.Next() {
		v, err := scanAIFARDeployment(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) SaveAIFARDeploymentGeneration(next AIFARDeployment, expectedGeneration int64) (AIFARDeployment, error) {
	now := time.Now()
	if next.ID == "" {
		next.ID = NewID("aifardeploy")
	}
	if next.CreatedAt.IsZero() {
		next.CreatedAt = now
	}
	if next.DesiredReplicas < 0 {
		next.DesiredReplicas = 0
	}
	next.Generation = expectedGeneration + 1
	next.ObservedGeneration = 0
	next.UpdatedAt = now

	tx, err := s.db.Begin()
	if err != nil {
		return AIFARDeployment{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`insert into aifar_deployments(id,instance_id,service_name,desired_replicas,current_revision,updating_revision,strategy_json,spec_json,generation,observed_generation,status,metadata_json,conditions_json,last_transition_at,created_at,updated_at)
		select ?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?
		where ?=0 or exists(select 1 from aifar_deployments where instance_id=? and service_name=?)
		on conflict(instance_id, service_name) do update set
		desired_replicas=excluded.desired_replicas,current_revision=excluded.current_revision,updating_revision=excluded.updating_revision,
		strategy_json=excluded.strategy_json,spec_json=excluded.spec_json,generation=excluded.generation,observed_generation=excluded.observed_generation,status=excluded.status,
		metadata_json=excluded.metadata_json,conditions_json=excluded.conditions_json,last_transition_at=excluded.last_transition_at,updated_at=excluded.updated_at
		where aifar_deployments.generation=?`,
		next.ID, next.InstanceID, next.ServiceName, next.DesiredReplicas, next.CurrentRevision, next.UpdatingRevision, next.StrategyJSON, next.SpecJSON, next.Generation, next.ObservedGeneration, next.Status, next.MetadataJSON, next.ConditionsJSON, nullableTime(next.LastTransitionAt), next.CreatedAt, next.UpdatedAt, expectedGeneration, next.InstanceID, next.ServiceName, expectedGeneration)
	if err != nil {
		return AIFARDeployment{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AIFARDeployment{}, err
	}
	if affected == 0 {
		if _, err := getAIFARDeployment(tx, next.InstanceID, next.ServiceName); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return AIFARDeployment{}, ErrAIFARDeploymentNotFound
			}
			return AIFARDeployment{}, err
		}
		return AIFARDeployment{}, ErrAIFARDeploymentGenerationConflict
	}
	saved, err := getAIFARDeployment(tx, next.InstanceID, next.ServiceName)
	if err != nil {
		return AIFARDeployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return AIFARDeployment{}, err
	}
	return saved, nil
}

// AcceptAIFARDeployment records Agent acceptance only while the desired
// generation still exactly matches generation. It deliberately does not
// advance ObservedGeneration: acceptance proves durable enqueue, not runtime
// reconciliation or readiness.
func (s *Store) AcceptAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (AIFARDeployment, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AIFARDeployment{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`update aifar_deployments set
		status=?, conditions_json=?, last_transition_at=?, updated_at=?
		where instance_id=? and service_name=? and generation=?
			and observed_generation=0 and lower(status)='pending_acceptance'`,
		status, conditionsJSON, at, time.Now(), instanceID, serviceName, generation)
	if err != nil {
		return AIFARDeployment{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AIFARDeployment{}, err
	}
	if affected == 0 {
		current, err := getAIFARDeployment(tx, instanceID, serviceName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return AIFARDeployment{}, ErrAIFARDeploymentNotFound
			}
			return AIFARDeployment{}, err
		}
		if current.Generation != generation {
			return AIFARDeployment{}, ErrAIFARDeploymentGenerationConflict
		}
		if strings.EqualFold(current.Status, "Accepted") || current.ObservedGeneration >= generation {
			if err := tx.Commit(); err != nil {
				return AIFARDeployment{}, err
			}
			return current, nil
		}
		return AIFARDeployment{}, ErrAIFARDeploymentGenerationConflict
	}
	accepted, err := getAIFARDeployment(tx, instanceID, serviceName)
	if err != nil {
		return AIFARDeployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return AIFARDeployment{}, err
	}
	return accepted, nil
}

func (s *Store) ObserveAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (AIFARDeployment, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return AIFARDeployment{}, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`update aifar_deployments set
		status=?, conditions_json=?, observed_generation=?, last_transition_at=?, updated_at=?
		where instance_id=? and service_name=? and generation=?`,
		status, conditionsJSON, generation, at, time.Now(), instanceID, serviceName, generation)
	if err != nil {
		return AIFARDeployment{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AIFARDeployment{}, err
	}
	if affected == 0 {
		current, err := getAIFARDeployment(tx, instanceID, serviceName)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return AIFARDeployment{}, ErrAIFARDeploymentNotFound
			}
			return AIFARDeployment{}, err
		}
		if current.Generation > generation {
			if err := tx.Commit(); err != nil {
				return AIFARDeployment{}, err
			}
			return current, nil
		}
		return AIFARDeployment{}, ErrAIFARDeploymentGenerationConflict
	}
	observed, err := getAIFARDeployment(tx, instanceID, serviceName)
	if err != nil {
		return AIFARDeployment{}, err
	}
	if err := tx.Commit(); err != nil {
		return AIFARDeployment{}, err
	}
	return observed, nil
}

type aifarDeploymentScanner interface {
	Scan(dest ...any) error
}

func scanAIFARDeployment(scanner aifarDeploymentScanner) (AIFARDeployment, error) {
	var v AIFARDeployment
	var lastTransitionAt sql.NullTime
	err := scanner.Scan(&v.ID, &v.InstanceID, &v.ServiceName, &v.DesiredReplicas, &v.CurrentRevision, &v.UpdatingRevision, &v.StrategyJSON, &v.SpecJSON, &v.Generation, &v.ObservedGeneration, &v.Status, &v.MetadataJSON, &v.ConditionsJSON, &lastTransitionAt, &v.CreatedAt, &v.UpdatedAt)
	v.LastTransitionAt = nullTime(lastTransitionAt)
	return v, err
}

func getAIFARDeployment(tx *sql.Tx, instanceID, serviceName string) (AIFARDeployment, error) {
	row := tx.QueryRow(`select id,instance_id,service_name,desired_replicas,current_revision,coalesce(updating_revision,''),coalesce(strategy_json,''),coalesce(spec_json,''),coalesce(generation,1),coalesce(observed_generation,0),status,coalesce(metadata_json,''),coalesce(conditions_json,''),last_transition_at,created_at,updated_at
		from aifar_deployments where instance_id=? and service_name=?`, instanceID, serviceName)
	return scanAIFARDeployment(row)
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
	if v.DesiredPods < 0 {
		v.DesiredPods = 0
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

func (s *Store) AcquireAIFAROrchestrationLock(v AIFAROrchestrationLock) (AIFAROrchestrationLock, error) {
	now := time.Now().UTC()
	v.InstanceID = strings.TrimSpace(v.InstanceID)
	v.ServiceName = strings.TrimSpace(v.ServiceName)
	v.Operation = strings.TrimSpace(v.Operation)
	v.Actor = strings.TrimSpace(v.Actor)
	v.TaskID = strings.TrimSpace(v.TaskID)
	if v.InstanceID == "" || v.Operation == "" {
		return v, fmt.Errorf("AIFAR orchestration lock requires instance id and operation")
	}
	if v.ID == "" {
		v.ID = NewID("aifarlock")
	}
	if v.StartedAt.IsZero() {
		v.StartedAt = now
	}
	if v.ExpiresAt.IsZero() || !v.ExpiresAt.After(v.StartedAt) {
		v.ExpiresAt = v.StartedAt.Add(time.Hour)
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = now
	}
	v.UpdatedAt = now
	v.Status = "active"

	tx, err := s.db.Begin()
	if err != nil {
		return v, err
	}
	defer tx.Rollback()
	if err := expireAIFAROrchestrationLocks(tx, now); err != nil {
		return v, err
	}
	conflict, found, err := findAIFAROrchestrationLockConflict(tx, v.InstanceID, v.ServiceName)
	if err != nil {
		return v, err
	}
	if found {
		return v, AIFAROrchestrationLockConflict{Lock: conflict}
	}
	_, err = tx.Exec(`insert into aifar_orchestration_locks(id,instance_id,service_name,operation,actor,task_id,status,started_at,expires_at,released_at,created_at,updated_at)
		values(?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ID, v.InstanceID, v.ServiceName, v.Operation, v.Actor, v.TaskID, v.Status, v.StartedAt, v.ExpiresAt, nullableTime(v.ReleasedAt), v.CreatedAt, v.UpdatedAt)
	if err != nil {
		return v, err
	}
	return v, tx.Commit()
}

func (s *Store) ReleaseAIFAROrchestrationLock(instanceID, operation, serviceName string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`update aifar_orchestration_locks
		set status='released', released_at=?, updated_at=?
		where instance_id=? and service_name=? and operation=? and status='active'`,
		now, now, strings.TrimSpace(instanceID), strings.TrimSpace(serviceName), strings.TrimSpace(operation))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func (s *Store) ReleaseAIFAROrchestrationLockByID(id string) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`update aifar_orchestration_locks
		set status='released', released_at=?, updated_at=?
		where id=? and status='active'`, now, now, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func (s *Store) RenewAIFAROrchestrationLock(id string, expiresAt time.Time) (bool, error) {
	now := time.Now().UTC()
	res, err := s.db.Exec(`update aifar_orchestration_locks
		set expires_at=?, updated_at=?
		where id=? and status='active' and expires_at > ?`,
		expiresAt.UTC(), now, strings.TrimSpace(id), now)
	if err != nil {
		return false, err
	}
	affected, err := res.RowsAffected()
	return affected > 0, err
}

func (s *Store) RecoverAIFAROrchestrationLocks(instanceID, reason string) (int, error) {
	now := time.Now().UTC()
	query := `update aifar_orchestration_locks
		set status='recovered', released_at=?, updated_at=?
		where status='active'`
	args := []any{now, now}
	if strings.TrimSpace(instanceID) != "" {
		query += ` and instance_id=?`
		args = append(args, strings.TrimSpace(instanceID))
	}
	res, err := s.db.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	return int(affected), nil
}

func (s *Store) ListAIFAROrchestrationLocks(instanceID string, activeOnly bool) ([]AIFAROrchestrationLock, error) {
	args := []any{}
	query := `select id,instance_id,service_name,operation,coalesce(actor,''),coalesce(task_id,''),status,started_at,expires_at,released_at,created_at,updated_at from aifar_orchestration_locks`
	clauses := []string{}
	if strings.TrimSpace(instanceID) != "" {
		clauses = append(clauses, "instance_id=?")
		args = append(args, strings.TrimSpace(instanceID))
	}
	if activeOnly {
		clauses = append(clauses, "status='active'")
	}
	if len(clauses) > 0 {
		query += " where " + strings.Join(clauses, " and ")
	}
	query += " order by started_at desc"
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AIFAROrchestrationLock{}
	for rows.Next() {
		v, err := scanAIFAROrchestrationLock(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
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
		`delete from aifar_orchestration_locks where instance_id=?`,
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

type lockScanner interface {
	Scan(dest ...any) error
}

func scanAIFAROrchestrationLock(scanner lockScanner) (AIFAROrchestrationLock, error) {
	var v AIFAROrchestrationLock
	var releasedAt sql.NullTime
	err := scanner.Scan(&v.ID, &v.InstanceID, &v.ServiceName, &v.Operation, &v.Actor, &v.TaskID, &v.Status, &v.StartedAt, &v.ExpiresAt, &releasedAt, &v.CreatedAt, &v.UpdatedAt)
	v.ReleasedAt = nullTime(releasedAt)
	return v, err
}

func expireAIFAROrchestrationLocks(tx *sql.Tx, now time.Time) error {
	_, err := tx.Exec(`update aifar_orchestration_locks
		set status='expired', released_at=?, updated_at=?
		where status='active' and expires_at <= ?`, now, now, now)
	return err
}

func findAIFAROrchestrationLockConflict(tx *sql.Tx, instanceID, serviceName string) (AIFAROrchestrationLock, bool, error) {
	query := `select id,instance_id,service_name,operation,coalesce(actor,''),coalesce(task_id,''),status,started_at,expires_at,released_at,created_at,updated_at
		from aifar_orchestration_locks
		where instance_id=? and status='active'
		order by started_at limit 1`
	args := []any{instanceID}
	if strings.TrimSpace(serviceName) != "" {
		query = `select id,instance_id,service_name,operation,coalesce(actor,''),coalesce(task_id,''),status,started_at,expires_at,released_at,created_at,updated_at
			from aifar_orchestration_locks
			where instance_id=? and status='active' and (service_name='' or service_name=?)
			order by started_at limit 1`
		args = append(args, strings.TrimSpace(serviceName))
	}
	row := tx.QueryRow(query, args...)
	lock, err := scanAIFAROrchestrationLock(row)
	if err == sql.ErrNoRows {
		return AIFAROrchestrationLock{}, false, nil
	}
	if err != nil {
		return AIFAROrchestrationLock{}, false, err
	}
	return lock, true, nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
