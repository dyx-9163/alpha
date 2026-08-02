package mysql

import (
	"strings"

	"aifar-deployment/backend/internal/store"
)

// preflightDeleteBatch validates selection closure once before the first
// remote mutation. Unlike maintenanceInstances, it deliberately accepts an
// authoritative cluster with fewer than three remaining members so an
// interrupted whole-cluster deletion can be resumed.
func (s Service) preflightDeleteBatch(expected []store.AppInstance, language string) error {
	data, ok := s.store.(maintenanceReader)
	if !ok || len(expected) == 0 {
		return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
	}
	all, err := data.ListAppInstances()
	if err != nil {
		return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
	}
	byID := make(map[string]store.AppInstance, len(all))
	selected := make(map[string]bool, len(expected))
	clusterSelections := map[string][]store.AppInstance{}
	failedSelections := map[string][]store.AppInstance{}
	for _, instance := range all {
		byID[instance.ID] = instance
	}
	for _, requestInstance := range expected {
		fresh, found := byID[requestInstance.ID]
		if !found || selected[fresh.ID] || !sameDeleteInstanceIdentity(fresh, requestInstance) {
			return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
		}
		selected[fresh.ID] = true
		if IsFailedInstallCleanupInstance(fresh) {
			failedSelections[clusterIDFromInstance(fresh)] = append(failedSelections[clusterIDFromInstance(fresh)], fresh)
			continue
		}
		switch instanceTopology(fresh) {
		case "standalone":
			if err := s.requireNoMySQLMaintenance(fresh, language); err != nil {
				return err
			}
			if err := s.requireNoMySQLReconciliation(fresh, language); err != nil {
				return err
			}
		case "innodb-cluster":
			clusterID := clusterIDFromInstance(fresh)
			if clusterID == "" {
				return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
			}
			clusterSelections[clusterID] = append(clusterSelections[clusterID], fresh)
		default:
			return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
		}
	}
	for clusterID, instances := range failedSelections {
		if clusterID == "" {
			return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
		}
		current := 0
		for _, candidate := range all {
			if clusterIDFromInstance(candidate) == clusterID && IsFailedInstallCleanupInstance(candidate) {
				current++
				if !selected[candidate.ID] {
					return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
				}
			}
		}
		if current == 0 || current != len(instances) {
			return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
		}
	}
	for clusterID, selectedMembers := range clusterSelections {
		cluster, err := data.GetAppCluster(clusterID)
		if err != nil || cluster.App != "mysql" || instanceTopology(store.AppInstance{Topology: cluster.Topology}) != "innodb-cluster" {
			return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
		}
		authoritative, err := data.ListAppClusterMembers(clusterID)
		if err != nil || len(authoritative) == 0 || len(authoritative) > 3 || len(authoritative) != len(selectedMembers) {
			return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
		}
		authoritativeIDs := make(map[string]bool, len(authoritative))
		seenServers := make(map[string]bool, len(authoritative))
		resolved := make([]store.AppInstance, 0, len(authoritative))
		for _, member := range authoritative {
			candidate, found := byID[member.InstanceID]
			if !found || candidate.App != "mysql" || instanceTopology(candidate) != "innodb-cluster" ||
				clusterIDFromInstance(candidate) != clusterID || candidate.ServerID != member.ServerID || seenServers[candidate.ServerID] || !selected[candidate.ID] {
				return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
			}
			authoritativeIDs[candidate.ID] = true
			seenServers[candidate.ServerID] = true
			resolved = append(resolved, candidate)
		}
		for _, candidate := range all {
			if candidate.App == "mysql" && instanceTopology(candidate) == "innodb-cluster" && clusterIDFromInstance(candidate) == clusterID && !authoritativeIDs[candidate.ID] {
				return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
			}
		}
		if err := requireDeleteBatchMarkersClear(resolved, language); err != nil {
			return err
		}
	}
	return nil
}

func (s Service) requirePreflightedDeleteMember(expected store.AppInstance, language string) error {
	data, ok := s.store.(maintenanceReader)
	if !ok {
		return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
	}
	fresh, err := data.GetAppInstance(expected.ID)
	if err != nil || !sameDeleteInstanceIdentity(fresh, expected) {
		return localizedMySQLOperationError(language, MySQLBackupClusterUnhealthy)
	}
	return requireDeleteBatchMarkersClear([]store.AppInstance{fresh}, language)
}

func requireDeleteBatchMarkersClear(instances []store.AppInstance, language string) error {
	maintenancePresent := 0
	for _, instance := range instances {
		_, present, err := store.ParseMySQLMaintenanceMarker(instance.Metadata)
		if err != nil {
			return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
		}
		if present {
			maintenancePresent++
		}
		present, err = ReconciliationMarkerState(instance.Metadata)
		if err != nil || present {
			return localizedMySQLOperationError(language, MySQLReconciliationRequired)
		}
	}
	if maintenancePresent == 0 {
		return nil
	}
	if maintenancePresent != len(instances) {
		return localizedMySQLOperationError(language, MySQLMaintenanceStateInvalid)
	}
	return localizedMySQLOperationError(language, MySQLMaintenanceRequired)
}

func sameDeleteInstanceIdentity(left, right store.AppInstance) bool {
	return strings.TrimSpace(left.ID) != "" && left.ID == right.ID && left.App == "mysql" &&
		left.ServerID == right.ServerID && instanceTopology(left) == instanceTopology(right) &&
		clusterIDFromInstance(left) == clusterIDFromInstance(right)
}
