package httpapi

import (
	"regexp"
	"strings"

	"aifar-deployment/backend/internal/apps/mysql"
	"aifar-deployment/backend/internal/store"
)

var controlledMySQLMaintenanceClusterID = regexp.MustCompile(`^cluster_[0-9a-f]{24}$`)

func validMySQLMaintenanceClusterID(value string) bool {
	return controlledMySQLMaintenanceClusterID.MatchString(strings.TrimSpace(value))
}

// mysqlMaintenanceGate is the handler-side half of the fail-closed guard.
// Module services repeat it after the worker has acquired the raw lock.
func (a *API) mysqlMaintenanceGate(instance store.AppInstance) string {
	if !strings.EqualFold(strings.TrimSpace(instance.App), "mysql") {
		return ""
	}
	_, representativePresent, representativeErr := store.ParseMySQLMaintenanceMarker(instance.Metadata)
	if representativeErr != nil {
		return mysql.MySQLMaintenanceStateInvalid
	}
	instances := []store.AppInstance{instance}
	if strings.EqualFold(strings.TrimSpace(instance.Topology), "innodb-cluster") {
		clusterID := mysqlClusterID(instance)
		if clusterID == "" {
			if !representativePresent {
				// This is not a maintenance-state decision. Leave the malformed
				// raw cluster identity to the lifecycle action's validator so
				// it preserves its established cluster-health error contract.
				return ""
			}
			return mysql.MySQLMaintenanceStateInvalid
		}
		cluster, err := a.store.GetAppCluster(clusterID)
		if err != nil || cluster.App != "mysql" || !strings.EqualFold(strings.TrimSpace(cluster.Topology), "innodb-cluster") {
			return mysql.MySQLMaintenanceStateInvalid
		}
		authoritative, err := a.store.ListAppClusterMembers(clusterID)
		if err != nil || len(authoritative) != 3 {
			return mysql.MySQLMaintenanceStateInvalid
		}
		all, err := a.store.ListAppInstances()
		if err != nil {
			return mysql.MySQLMaintenanceStateInvalid
		}
		byID := map[string]store.AppInstance{}
		for _, candidate := range all {
			byID[candidate.ID] = candidate
		}
		instances = instances[:0]
		for _, member := range authoritative {
			candidate, found := byID[member.InstanceID]
			if !found || candidate.App != "mysql" || !strings.EqualFold(strings.TrimSpace(candidate.Topology), "innodb-cluster") || mysqlClusterID(candidate) != clusterID || candidate.ServerID != member.ServerID {
				return mysql.MySQLMaintenanceStateInvalid
			}
			instances = append(instances, candidate)
		}
		if len(instances) != 3 {
			return mysql.MySQLMaintenanceStateInvalid
		}
	} else if !strings.EqualFold(strings.TrimSpace(instance.Topology), "standalone") {
		return mysql.MySQLMaintenanceStateInvalid
	}
	present := 0
	var first store.MySQLMaintenanceMarker
	for _, candidate := range instances {
		marker, found, err := store.ParseMySQLMaintenanceMarker(candidate.Metadata)
		if err != nil {
			return mysql.MySQLMaintenanceStateInvalid
		}
		if found {
			if present > 0 && !sameHTTPMySQLMaintenanceMarker(first, marker) {
				return mysql.MySQLMaintenanceStateInvalid
			}
			first, present = marker, present+1
		}
	}
	if present == 0 {
		return ""
	}
	if present != len(instances) {
		return mysql.MySQLMaintenanceStateInvalid
	}
	return mysql.MySQLMaintenanceRequired
}

func sameHTTPMySQLMaintenanceMarker(left, right store.MySQLMaintenanceMarker) bool {
	return left.Version == right.Version && left.State == right.State && left.Reason == right.Reason && left.Scope == right.Scope && left.ClusterID == right.ClusterID && left.BackupID == right.BackupID && left.TaskID == right.TaskID && left.RestorePhase == right.RestorePhase && left.RecordedAt.Equal(right.RecordedAt)
}
