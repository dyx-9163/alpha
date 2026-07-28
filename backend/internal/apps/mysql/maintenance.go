package mysql

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"aifar-deployment/backend/internal/store"
)

type maintenanceStore interface {
	GetAppInstance(string) (store.AppInstance, error)
	ListAppInstances() ([]store.AppInstance, error)
	GetAppCluster(string) (store.AppCluster, error)
	ListAppClusterMembers(string) ([]store.AppClusterMember, error)
	SetMySQLMaintenance([]string, store.MySQLMaintenanceMarker) error
	AdvanceMySQLMaintenance([]string, store.MySQLMaintenanceMarker, string) error
	ClearMySQLMaintenance([]string, store.MySQLMaintenanceMarker) error
}

func (s Service) requireNoMySQLMaintenance(expected store.AppInstance, language string) error {
	data, ok := s.store.(maintenanceStore)
	if !ok {
		return nil
	} // Legacy fakes cannot model a persisted safety marker.
	instance, err := data.GetAppInstance(expected.ID)
	if err != nil || instance.App != "mysql" {
		return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
	}
	instances, err := maintenanceInstances(data, instance)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
	}
	var common store.MySQLMaintenanceMarker
	presentCount := 0
	for _, candidate := range instances {
		marker, present, parseErr := store.ParseMySQLMaintenanceMarker(candidate.Metadata)
		if parseErr != nil {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		if present {
			if presentCount > 0 && !sameMaintenanceMarker(marker, common) {
				return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
			}
			common = marker
			presentCount++
		}
	}
	if presentCount == 0 {
		return nil
	}
	if presentCount != len(instances) {
		return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
	}
	return localizedMySQLOperationError(language, MySQLMaintenanceRequired)
}

func maintenanceInstances(data maintenanceStore, representative store.AppInstance) ([]store.AppInstance, error) {
	topology := instanceTopology(representative)
	if topology == "standalone" {
		return []store.AppInstance{representative}, nil
	}
	if topology != "innodb-cluster" {
		return nil, errors.New("invalid MySQL maintenance topology")
	}
	clusterID := clusterIDFromInstance(representative)
	if clusterID == "" {
		return nil, errors.New("missing MySQL maintenance cluster ID")
	}
	cluster, err := data.GetAppCluster(clusterID)
	if err != nil || cluster.App != "mysql" || instanceTopology(store.AppInstance{Topology: cluster.Topology}) != "innodb-cluster" {
		return nil, errors.New("invalid authoritative MySQL maintenance cluster")
	}
	authoritative, err := data.ListAppClusterMembers(clusterID)
	if err != nil || len(authoritative) != 3 {
		return nil, errors.New("invalid authoritative MySQL maintenance cluster members")
	}
	all, err := data.ListAppInstances()
	if err != nil {
		return nil, err
	}
	byID := map[string]store.AppInstance{}
	for _, candidate := range all {
		byID[candidate.ID] = candidate
	}
	members := make([]store.AppInstance, 0, 3)
	seenServers := map[string]bool{}
	for _, member := range authoritative {
		candidate, found := byID[member.InstanceID]
		if !found || candidate.App != "mysql" || instanceTopology(candidate) != "innodb-cluster" || clusterIDFromInstance(candidate) != clusterID || candidate.ServerID != member.ServerID || seenServers[candidate.ServerID] {
			return nil, errors.New("invalid authoritative MySQL maintenance member ownership")
		}
		seenServers[candidate.ServerID] = true
		members = append(members, candidate)
	}
	if len(members) != 3 {
		return nil, errors.New("invalid MySQL maintenance cluster membership")
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members, nil
}

func (s Service) setMySQLMaintenance(expected store.AppInstance, backupID, taskID, language string) (store.MySQLMaintenanceMarker, []string, error) {
	data, ok := s.store.(maintenanceStore)
	if !ok {
		return s.setLegacyMySQLMaintenance(expected, backupID, taskID, language)
	}
	instance, err := data.GetAppInstance(expected.ID)
	if err != nil {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	instances, err := maintenanceInstances(data, instance)
	if err != nil {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	scope := "standalone"
	clusterID := ""
	if instanceTopology(instance) == "innodb-cluster" {
		scope, clusterID = "cluster", clusterIDFromInstance(instance)
	}
	ids := make([]string, 0, len(instances))
	for _, candidate := range instances {
		ids = append(ids, candidate.ID)
	}
	marker := store.MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: scope, ClusterID: clusterID, BackupID: backupID, TaskID: taskID, RestorePhase: "schema_mutation_started", RecordedAt: time.Now().UTC()}
	if err := data.SetMySQLMaintenance(ids, marker); err != nil {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	return marker, ids, nil
}

func (s Service) advanceMySQLMaintenance(ids []string, marker store.MySQLMaintenanceMarker, language string) (store.MySQLMaintenanceMarker, error) {
	data, ok := s.store.(maintenanceStore)
	if !ok {
		return s.advanceLegacyMySQLMaintenance(ids, marker, language)
	}
	if err := data.AdvanceMySQLMaintenance(ids, marker, "load_complete"); err != nil {
		return marker, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	marker.RestorePhase = "load_complete"
	return marker, nil
}

func (s Service) clearMySQLMaintenance(ids []string, marker store.MySQLMaintenanceMarker, language string) error {
	data, ok := s.store.(maintenanceStore)
	if !ok {
		return s.clearLegacyMySQLMaintenance(ids, marker, language)
	}
	if err := data.ClearMySQLMaintenance(ids, marker); err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	return nil
}

// Legacy test stores predate transactional metadata helpers. Production Store
// always takes the atomic branch above; this compatibility path keeps older
// fake stores focused on remote lifecycle behavior.
func (s Service) setLegacyMySQLMaintenance(expected store.AppInstance, backupID, taskID, language string) (store.MySQLMaintenanceMarker, []string, error) {
	data, ok := s.store.(restoreStore)
	if !ok {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	fresh, err := data.GetAppInstance(expected.ID)
	if err != nil {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	scope, clusterID := "standalone", ""
	if instanceTopology(fresh) == "innodb-cluster" {
		scope, clusterID = "cluster", clusterIDFromInstance(fresh)
	}
	marker := store.MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: scope, ClusterID: clusterID, BackupID: backupID, TaskID: taskID, RestorePhase: "schema_mutation_started", RecordedAt: time.Now().UTC()}
	metadata, err := strictBackupMetadata(fresh.Metadata)
	if err != nil {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	metadata["mysqlMaintenance"], _ = json.Marshal(marker)
	encoded, _ := json.Marshal(metadata)
	fresh.Metadata = string(encoded)
	if _, err = data.SaveAppInstance(fresh); err != nil {
		return store.MySQLMaintenanceMarker{}, nil, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	return marker, []string{fresh.ID}, nil
}

func (s Service) advanceLegacyMySQLMaintenance(ids []string, marker store.MySQLMaintenanceMarker, language string) (store.MySQLMaintenanceMarker, error) {
	data, ok := s.store.(restoreStore)
	if !ok || len(ids) != 1 {
		return marker, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	fresh, err := data.GetAppInstance(ids[0])
	if err != nil {
		return marker, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	metadata, err := strictBackupMetadata(fresh.Metadata)
	if err != nil {
		return marker, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	marker.RestorePhase = "load_complete"
	metadata["mysqlMaintenance"], _ = json.Marshal(marker)
	encoded, _ := json.Marshal(metadata)
	fresh.Metadata = string(encoded)
	if _, err = data.SaveAppInstance(fresh); err != nil {
		return marker, localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	return marker, nil
}

func (s Service) clearLegacyMySQLMaintenance(ids []string, marker store.MySQLMaintenanceMarker, language string) error {
	data, ok := s.store.(restoreStore)
	if !ok || len(ids) != 1 {
		return localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	fresh, err := data.GetAppInstance(ids[0])
	if err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	metadata, err := strictBackupMetadata(fresh.Metadata)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	delete(metadata, "mysqlMaintenance")
	encoded, _ := json.Marshal(metadata)
	fresh.Metadata = string(encoded)
	if _, err = data.SaveAppInstance(fresh); err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	return nil
}

// ClearMaintenance performs the owner-approved recovery acknowledgement only
// after re-reading the marker and confirming the MySQL runtime is reachable.
func (m Module) ClearMaintenance(ctx context.Context, instance store.AppInstance, language, taskID string, log Logger) error {
	data, ok := m.service.store.(maintenanceStore)
	if !ok {
		return localizedMySQLOperationError(language, MySQLMaintenanceStatePersistFailed)
	}
	fresh, err := data.GetAppInstance(instance.ID)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
	}
	instances, err := maintenanceInstances(data, fresh)
	if err != nil {
		return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
	}
	var marker store.MySQLMaintenanceMarker
	ids := make([]string, 0, len(instances))
	for index, candidate := range instances {
		_, _, reconciliationPresent, reconciliationErr := parseMySQLReconciliationMarker(candidate.Metadata)
		if reconciliationErr != nil || reconciliationPresent {
			return localizedMySQLOperationError(language, MySQLReconciliationRequired)
		}
		candidateMarker, present, parseErr := store.ParseMySQLMaintenanceMarker(candidate.Metadata)
		if parseErr != nil || !present || (index > 0 && !sameMaintenanceMarker(marker, candidateMarker)) {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		marker = candidateMarker
		ids = append(ids, candidate.ID)
	}
	if marker.Scope == "standalone" {
		server, getErr := m.service.store.GetServer(fresh.ServerID, false)
		if getErr != nil {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		credentials, available := m.service.store.(backupStore)
		if !available {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		credential, credentialErr := credentials.GetBoundCredential(fresh.ID, "admin", true)
		if credentialErr != nil || credential.Status != "active" || credential.Kind != "mysql" || strings.TrimSpace(credential.Secret["password"]) == "" {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		probe, probeErr := m.service.probeMySQLRuntime(ctx, server, fresh, credential.Secret["password"], log)
		if probeErr != nil || !probe.pingRunning() {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
	} else {
		cluster, clusterErr := m.service.resolveHealthyInnoDBCluster(ctx, fresh, taskID)
		if clusterErr != nil || len(cluster.members) != 3 || m.service.verifyMaintenanceClusterHealth(ctx, &cluster, taskID) != nil {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		// Runtime resolver verifies exactly three ONLINE members and one PRIMARY.
	}
	return m.service.clearMySQLMaintenance(ids, marker, language)
}

func sameMaintenanceMarker(left, right store.MySQLMaintenanceMarker) bool {
	return left.Version == right.Version && left.State == right.State && left.Reason == right.Reason && left.Scope == right.Scope && left.ClusterID == right.ClusterID && left.BackupID == right.BackupID && left.TaskID == right.TaskID && left.RestorePhase == right.RestorePhase && left.RecordedAt.Equal(right.RecordedAt)
}

func maintenanceErrorCode(err error) string {
	var operation *MySQLOperationError
	if errors.As(err, &operation) {
		return operation.Code
	}
	return ""
}

func maintenanceClusterIDs(instances []store.AppInstance) []string {
	ids := make([]string, 0, len(instances))
	for _, instance := range instances {
		ids = append(ids, strings.TrimSpace(instance.ID))
	}
	sort.Strings(ids)
	return ids
}
