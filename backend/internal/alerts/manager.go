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
	if err := m.evaluateCollectorRuns(upsert); err != nil {
		return err
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

func (m *Manager) evaluateCollectorRuns(upsert func(store.Alert) error) error {
	runs, err := m.store.ListCollectorRuns()
	if err != nil {
		return err
	}
	for _, run := range runs {
		if strings.ToLower(strings.TrimSpace(run.Status)) != "failed" {
			continue
		}
		message := strings.TrimSpace(run.LastError)
		if message == "" {
			message = "collector failed"
		}
		if err := upsert(store.Alert{
			Fingerprint:        "collector:" + run.Name + ":failed",
			Severity:           "warning",
			Scope:              "collector",
			ResourceID:         run.Name,
			Status:             "open",
			Title:              "Collector " + run.Name + " failed",
			Message:            message,
			EvidenceJSON:       evidenceJSON(map[string]any{"run": run.Name, "target": run.Target, "finishedAt": run.FinishedAt, "durationMs": run.DurationMS}),
			RequiredPermission: string(rbac.SettingsManage),
			LastSeenAt:         run.UpdatedAt,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) evaluateSnapshots(upsert func(store.Alert) error) error {
	snapshots, err := m.store.ListStatusSnapshots("", "")
	if err != nil {
		return err
	}
	for _, snapshot := range snapshots {
		status := strings.ToLower(strings.TrimSpace(snapshot.Status))
		switch snapshot.Scope {
		case "docker.summary":
			if status == "failed" {
				message := snapshot.LastError
				if strings.TrimSpace(message) == "" {
					message = "Docker summary collection failed"
				}
				if err := upsert(store.Alert{
					Fingerprint:        "docker.summary:" + snapshot.ResourceID + ":failed",
					Severity:           "critical",
					Scope:              snapshot.Scope,
					ResourceID:         snapshot.ResourceID,
					ServerID:           snapshot.ServerID,
					App:                "docker",
					Status:             "open",
					Title:              "Docker summary collection failed",
					Message:            message,
					EvidenceJSON:       snapshotEvidence(snapshot),
					RequiredPermission: string(rbac.ContainersManage),
					LastSeenAt:         snapshot.UpdatedAt,
				}); err != nil {
					return err
				}
			}
		case "aifar.runtime":
			if status == "degraded" || isServiceUnavailableStatus(status) {
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
					Title:              "AIFAR Runtime is " + status,
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
					Title:              "Server is " + status,
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

func (m *Manager) evaluateAppInstances(upsert func(store.Alert) error) error {
	instances, err := m.store.ListAppInstances()
	if err != nil {
		return err
	}
	for _, instance := range instances {
		status := strings.ToLower(strings.TrimSpace(instance.Status))
		metadata := metadataMap(instance.Metadata)
		installFailed, _ := metadata["installFailed"].(bool)
		if !alertableInstanceStatus(status) && !installFailed {
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
		severity := instanceAlertSeverity(status, installFailed)
		if err := upsert(store.Alert{
			Fingerprint:        "app.instance:" + instance.ID + ":" + status,
			Severity:           severity,
			Scope:              "app.instance",
			ResourceID:         instance.ID,
			ServerID:           instance.ServerID,
			App:                app,
			InstanceID:         instance.ID,
			Status:             "open",
			Title:              strings.ToUpper(app) + " instance is " + status,
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

func instanceAlertSeverity(status string, installFailed bool) string {
	status = strings.ToLower(strings.TrimSpace(status))
	if isServiceUnavailableStatus(status) || installFailed {
		return "critical"
	}
	return "warning"
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

func alertableInstanceStatus(status string) bool {
	switch status {
	case "failed", "error", "unavailable", "degraded", "unhealthy", "no-endpoints", "down", "offline":
		return true
	default:
		return false
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
