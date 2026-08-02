package mysql

import (
	"encoding/json"
	"regexp"
	"strings"

	"aifar-deployment/backend/internal/store"
)

var failedInstallCleanupTaskID = regexp.MustCompile(`^tsk_[0-9a-f]{24}$`)

type failedInstallCleanupMetadata struct {
	InstallFailed bool   `json:"installFailed"`
	TaskID        string `json:"taskId"`
	ClusterID     string `json:"clusterId"`
	Topology      string `json:"topology"`
}

// IsFailedInstallCleanupInstance identifies only the synthetic InnoDB Cluster
// placeholders created when installation failed before authoritative cluster
// topology was recorded. Maintenance or reconciliation state always wins.
func IsFailedInstallCleanupInstance(instance store.AppInstance) bool {
	if !strings.EqualFold(strings.TrimSpace(instance.App), "mysql") ||
		!strings.EqualFold(strings.TrimSpace(instance.Status), "failed") ||
		!strings.EqualFold(strings.TrimSpace(instance.Topology), "innodb-cluster") {
		return false
	}
	var metadata failedInstallCleanupMetadata
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil ||
		!metadata.InstallFailed || !failedInstallCleanupTaskID.MatchString(strings.TrimSpace(metadata.TaskID)) ||
		!strings.EqualFold(strings.TrimSpace(metadata.Topology), "innodb-cluster") ||
		strings.TrimSpace(metadata.ClusterID) != "mysql-failed-"+strings.TrimSpace(metadata.TaskID) {
		return false
	}
	if _, present, err := store.ParseMySQLMaintenanceMarker(instance.Metadata); err != nil || present {
		return false
	}
	if present, err := ReconciliationMarkerState(instance.Metadata); err != nil || present {
		return false
	}
	return true
}

func (s Service) isFreshFailedInstallCleanupInstance(expected store.AppInstance) bool {
	reader, ok := s.store.(interface {
		GetAppInstance(string) (store.AppInstance, error)
	})
	if !ok || strings.TrimSpace(expected.ID) == "" {
		return false
	}
	fresh, err := reader.GetAppInstance(expected.ID)
	if err != nil || fresh.ServerID != expected.ServerID {
		return false
	}
	return IsFailedInstallCleanupInstance(fresh)
}
