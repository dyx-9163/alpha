package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"aifar-deployment/backend/internal/rbac"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

type Publisher interface {
	Publish(realtime.Event)
}

type Manager struct {
	store  *store.Store
	events Publisher
}

func NewManager(s *store.Store, events Publisher) *Manager {
	return &Manager{store: s, events: events}
}

func (m *Manager) Evaluate(ctx context.Context) error {
	if m == nil || m.store == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	active := []string{}
	upsert := func(alert store.Alert) error {
		if alert.Fingerprint == "" {
			return nil
		}
		if alert.Scope != "task" {
			active = append(active, alert.Fingerprint)
		}
		saved, action, err := m.store.UpsertAlert(alert)
		if err != nil {
			return err
		}
		if action != "" {
			eventType := "alert.updated"
			if action == "created" {
				eventType = "alert.created"
			}
			m.publish(eventType, saved)
		}
		return nil
	}
	if err := m.evaluateSnapshots(upsert); err != nil {
		return err
	}
	if err := m.evaluateAppInstances(upsert); err != nil {
		return err
	}
	if err := m.evaluateTasks(upsert); err != nil {
		return err
	}
	resolved, err := m.store.ResolveMissingSystemAlerts(active)
	if err != nil {
		return err
	}
	for _, alert := range resolved {
		m.publish("alert.resolved", alert)
	}
	return nil
}

func (m *Manager) evaluateSnapshots(upsert func(store.Alert) error) error {
	snapshots, err := m.store.ListStatusSnapshots("", "")
	if err != nil {
		return err
	}
	instances, err := m.store.ListAppInstances()
	if err != nil {
		return err
	}
	activeInstances := make(map[string]store.AppInstance, len(instances))
	for _, instance := range instances {
		activeInstances[instance.ID] = instance
	}
	for _, snapshot := range snapshots {
		status := strings.ToLower(strings.TrimSpace(snapshot.Status))
		switch snapshot.Scope {
		case "docker.summary":
			if status == "failed" {
				message := snapshot.LastError
				if strings.TrimSpace(message) == "" {
					message = "Docker service is unavailable"
				}
				if err := upsert(store.Alert{
					Fingerprint:        "docker.summary:" + snapshot.ResourceID + ":failed",
					Severity:           "critical",
					Scope:              snapshot.Scope,
					ResourceID:         snapshot.ResourceID,
					ServerID:           snapshot.ServerID,
					App:                "docker",
					Status:             "open",
					Title:              "Docker service is unavailable",
					Message:            message,
					EvidenceJSON:       snapshotEvidence(snapshot),
					RequiredPermission: string(rbac.ContainersManage),
					LastSeenAt:         snapshot.UpdatedAt,
				}); err != nil {
					return err
				}
			}
		case "app.instance":
			instance, exists := activeInstances[snapshot.ResourceID]
			if !exists {
				continue
			}
			app := strings.ToLower(strings.TrimSpace(instance.App))
			if !appInstanceRuntimeAlertsEnabled(app) {
				continue
			}
			if status == "degraded" || isServiceUnavailableStatus(status) {
				severity := "warning"
				if isServiceUnavailableStatus(status) {
					severity = "critical"
				}
				serverID := snapshot.ServerID
				if strings.TrimSpace(serverID) == "" {
					serverID = instance.ServerID
				}
				message := strings.TrimSpace(snapshot.LastError)
				if message == "" {
					message = app + " runtime status is " + status
				}
				if err := upsert(store.Alert{
					Fingerprint:        "app.instance.runtime:" + snapshot.ResourceID + ":" + status,
					Severity:           severity,
					Scope:              snapshot.Scope,
					ResourceID:         snapshot.ResourceID,
					ServerID:           serverID,
					App:                app,
					InstanceID:         snapshot.ResourceID,
					Status:             "open",
					Title:              appAlertDisplayName(app) + " service is " + alertTitleStatus(status, false),
					Message:            message,
					EvidenceJSON:       snapshotEvidence(snapshot),
					RequiredPermission: string(permissionForApp(app)),
					LastSeenAt:         snapshot.UpdatedAt,
				}); err != nil {
					return err
				}
			}
		case "aifar.runtime":
			if _, exists := activeInstances[snapshot.ResourceID]; !exists {
				continue
			}
			if (status == "degraded" || isServiceUnavailableStatus(status)) && runtimeSnapshotNeedsAlert(status, snapshot) {
				severity := "warning"
				if isServiceUnavailableStatus(status) {
					severity = "critical"
				}
				if err := upsert(store.Alert{
					Fingerprint:        "aifar.runtime:" + snapshot.ResourceID + ":" + status,
					Severity:           severity,
					Scope:              snapshot.Scope,
					ResourceID:         snapshot.ResourceID,
					ServerID:           snapshot.ServerID,
					App:                "aifar",
					InstanceID:         snapshot.ResourceID,
					Status:             "open",
					Title:              "AIFAR Runtime is " + alertTitleStatus(status, false),
					Message:            aifarRuntimeMessage(status, snapshot),
					EvidenceJSON:       snapshotEvidence(snapshot),
					RequiredPermission: string(rbac.AppsManage),
					LastSeenAt:         snapshot.UpdatedAt,
				}); err != nil {
					return err
				}
			}
		case "server":
			if isServiceUnavailableStatus(status) {
				message := snapshot.LastError
				if strings.TrimSpace(message) == "" {
					message = "server status is " + status
				}
				if err := upsert(store.Alert{
					Fingerprint:        "server:" + snapshot.ResourceID + ":" + status,
					Severity:           "critical",
					Scope:              snapshot.Scope,
					ResourceID:         snapshot.ResourceID,
					ServerID:           snapshot.ServerID,
					Status:             "open",
					Title:              "Server is " + alertTitleStatus(status, false),
					Message:            message,
					EvidenceJSON:       snapshotEvidence(snapshot),
					RequiredPermission: string(rbac.ServersManage),
					LastSeenAt:         snapshot.UpdatedAt,
				}); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func appInstanceRuntimeAlertsEnabled(app string) bool {
	switch strings.ToLower(strings.TrimSpace(app)) {
	case "mysql", "mysql-router", "mysqlrouter", "redis", "minio", "nacos":
		return true
	default:
		return false
	}
}

func appAlertDisplayName(app string) string {
	switch strings.ToLower(strings.TrimSpace(app)) {
	case "mysql-router", "mysqlrouter":
		return "MYSQL-ROUTER"
	case "minio":
		return "MINIO"
	case "nacos":
		return "NACOS"
	default:
		return strings.ToUpper(strings.TrimSpace(app))
	}
}

func (m *Manager) evaluateAppInstances(upsert func(store.Alert) error) error {
	instances, err := m.store.ListAppInstances()
	if err != nil {
		return err
	}
	for _, instance := range instances {
		status := strings.ToLower(strings.TrimSpace(instance.Status))
		metadata := metadataMap(instance.Metadata)
		if !isInstallFailureInstance(status, metadata) {
			continue
		}
		message := metadataString(metadata, "error")
		if message == "" {
			message = metadataString(metadata, "message")
		}
		if message == "" {
			message = "instance status is " + status
		}
		app := strings.ToLower(strings.TrimSpace(instance.App))
		if err := upsert(store.Alert{
			Fingerprint:        "app.instance:" + instance.ID + ":install_failed",
			Severity:           "critical",
			Scope:              "app.instance",
			ResourceID:         instance.ID,
			ServerID:           instance.ServerID,
			App:                app,
			InstanceID:         instance.ID,
			Status:             "open",
			Title:              strings.ToUpper(app) + " installation failed",
			Message:            message,
			EvidenceJSON:       evidenceJSON(map[string]any{"app": app, "version": instance.Version, "topology": instance.Topology, "metadata": metadata}),
			RequiredPermission: string(permissionForApp(app)),
			LastSeenAt:         instance.UpdatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func alertTitleStatus(status string, installFailed bool) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if installFailed {
		return "failed"
	}
	if isServiceUnavailableStatus(status) {
		return "unavailable"
	}
	if status == "" {
		return "unknown"
	}
	return status
}

func (m *Manager) evaluateTasks(upsert func(store.Alert) error) error {
	tasks, err := m.store.ListTasks()
	if err != nil {
		return err
	}
	for _, task := range tasks {
		if strings.ToLower(strings.TrimSpace(task.Status)) != "failed" || !isLifecycleTask(task.Type) {
			continue
		}
		message := strings.TrimSpace(task.Error)
		if message == "" {
			message = "task failed"
		}
		if err := upsert(store.Alert{
			Fingerprint:        "task:" + task.ID + ":failed",
			Severity:           "warning",
			Scope:              "task",
			ResourceID:         task.ID,
			Status:             "open",
			Title:              "Lifecycle task failed",
			Message:            message,
			EvidenceJSON:       evidenceJSON(map[string]any{"taskId": task.ID, "type": task.Type, "target": task.Target, "finishedAt": task.FinishedAt}),
			RequiredPermission: string(permissionForTask(task.Type)),
			LastSeenAt:         task.FinishedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) publish(eventType string, alert store.Alert) {
	if m.events == nil {
		return
	}
	m.events.Publish(realtime.Event{
		Type:       eventType,
		Resource:   "alert",
		ResourceID: alert.ID,
		ServerID:   alert.ServerID,
		InstanceID: alert.InstanceID,
		Status:     alert.Status,
		Payload:    map[string]any{"alert": alert},
	})
}

func aifarRuntimeMessage(status string, snapshot store.StatusSnapshot) string {
	payload := metadataMap(snapshot.Payload)
	switch status {
	case "no-endpoints":
		return fmt.Sprintf("no active endpoints, ready pods %v of desired %v", payload["readyPods"], payload["desiredReplicas"])
	case "degraded":
		return fmt.Sprintf("ready pods %v of desired %v", payload["readyPods"], payload["desiredReplicas"])
	case "failed":
		if strings.TrimSpace(snapshot.LastError) != "" {
			return snapshot.LastError
		}
		return "runtime status is failed"
	default:
		return "runtime status is " + status
	}
}

func isServiceUnavailableStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "unavailable", "unhealthy", "no-endpoints", "down", "offline":
		return true
	default:
		return false
	}
}

func isInstallFailureInstance(status string, metadata map[string]any) bool {
	if installFailed, _ := metadata["installFailed"].(bool); installFailed {
		return true
	}
	installState := strings.ToLower(strings.TrimSpace(metadataString(metadata, "installState")))
	switch installState {
	case "failed", "install_failed", "installation_failed":
		return true
	}
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "install_failed", "installation_failed":
		return true
	default:
		return false
	}
}

func runtimeSnapshotNeedsAlert(status string, snapshot store.StatusSnapshot) bool {
	status = strings.ToLower(strings.TrimSpace(status))
	if status != "degraded" && status != "no-endpoints" {
		return true
	}
	payload := metadataMap(snapshot.Payload)
	ready, readyOK := metadataNumber(payload, "readyPods")
	desired, desiredOK := metadataNumber(payload, "desiredReplicas")
	if !readyOK || !desiredOK {
		return true
	}
	return desired > 0 && ready < desired
}

func metadataNumber(metadata map[string]any, key string) (float64, bool) {
	value, ok := metadata[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	default:
		return 0, false
	}
}

func isLifecycleTask(taskType string) bool {
	taskType = strings.ToLower(strings.TrimSpace(taskType))
	if taskType == "" {
		return false
	}
	if strings.Contains(taskType, ".check") || strings.Contains(taskType, ".probe") || strings.Contains(taskType, ".scan") {
		return false
	}
	return strings.Contains(taskType, ".install") ||
		strings.Contains(taskType, ".delete") ||
		strings.Contains(taskType, ".uninstall") ||
		strings.Contains(taskType, "install") ||
		strings.Contains(taskType, "uninstall")
}

func permissionForTask(taskType string) rbac.Permission {
	taskType = strings.ToLower(strings.TrimSpace(taskType))
	switch {
	case strings.Contains(taskType, ".mysql.") || strings.Contains(taskType, ".redis.") || strings.Contains(taskType, "mysql") || strings.Contains(taskType, "redis"):
		return rbac.DatabaseManage
	case strings.Contains(taskType, ".minio.") || strings.Contains(taskType, "storage"):
		return rbac.StorageManage
	case strings.Contains(taskType, ".docker."):
		return rbac.ContainersManage
	default:
		return rbac.AppsManage
	}
}

func permissionForApp(app string) rbac.Permission {
	switch strings.ToLower(strings.TrimSpace(app)) {
	case "mysql", "redis", "mysql-router", "mysqlrouter":
		return rbac.DatabaseManage
	case "minio":
		return rbac.StorageManage
	case "docker":
		return rbac.ContainersManage
	default:
		return rbac.AppsManage
	}
}

func snapshotEvidence(snapshot store.StatusSnapshot) string {
	payload := map[string]any{}
	if strings.TrimSpace(snapshot.Payload) != "" {
		_ = json.Unmarshal([]byte(snapshot.Payload), &payload)
	}
	return evidenceJSON(map[string]any{
		"scope":       snapshot.Scope,
		"resourceId":  snapshot.ResourceID,
		"serverId":    snapshot.ServerID,
		"status":      snapshot.Status,
		"lastError":   snapshot.LastError,
		"version":     snapshot.Version,
		"collectedAt": snapshot.CollectedAt,
		"payload":     payload,
	})
}

func evidenceJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func metadataMap(raw string) map[string]any {
	out := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

func metadataString(metadata map[string]any, key string) string {
	if value, ok := metadata[key]; ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return ""
}
