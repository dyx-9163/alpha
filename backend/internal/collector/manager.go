package collector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/logmask"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

type Publisher interface {
	Publish(realtime.Event)
}

type AlertEvaluator interface {
	Evaluate(context.Context) error
}

type Manager struct {
	store     *store.Store
	events    Publisher
	alerts    AlertEvaluator
	apps      *registry.Registry
	interval  time.Duration
	timeout   time.Duration
	startedCh chan struct{}
}

func NewManager(s *store.Store, events Publisher, interval time.Duration) *Manager {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	return &Manager{
		store:     s,
		events:    events,
		interval:  interval,
		timeout:   8 * time.Second,
		startedCh: make(chan struct{}),
	}
}

func (m *Manager) SetAlertEvaluator(alerts AlertEvaluator) {
	if m != nil {
		m.alerts = alerts
	}
}

func (m *Manager) SetAppRegistry(apps *registry.Registry) {
	if m != nil {
		m.apps = apps
	}
}

func (m *Manager) Start(ctx context.Context) {
	if m == nil {
		return
	}
	go func() {
		close(m.startedCh)
		m.RunOnce(ctx)
		ticker := time.NewTicker(m.interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.RunOnce(ctx)
			}
		}
	}()
}

func (m *Manager) RunOnce(ctx context.Context) {
	if m == nil || m.store == nil {
		return
	}
	m.run(ctx, "servers", m.collectServers)
	m.run(ctx, "docker.summary", m.collectDockerSummaries)
	m.run(ctx, "app.instances", m.collectAppInstances)
	m.run(ctx, "aifar.runtime", m.collectAIFARRuntime)
	if m.alerts != nil {
		_ = m.alerts.Evaluate(ctx)
	}
}

func (m *Manager) run(ctx context.Context, name string, fn func(context.Context) error) {
	startedAt := time.Now()
	_ = m.store.UpsertCollectorRun(store.CollectorRun{Name: name, Status: "running", StartedAt: startedAt, UpdatedAt: startedAt})
	if m.events != nil {
		m.events.Publish(realtime.Event{Type: "collector.run.started", Resource: name, Status: "running"})
	}
	err := fn(ctx)
	finishedAt := time.Now()
	status := "success"
	errText := ""
	if err != nil {
		status = "failed"
		errText = err.Error()
	}
	_ = m.store.UpsertCollectorRun(store.CollectorRun{
		Name:       name,
		Status:     status,
		LastError:  errText,
		StartedAt:  startedAt,
		FinishedAt: finishedAt,
		DurationMS: finishedAt.Sub(startedAt).Milliseconds(),
		UpdatedAt:  finishedAt,
	})
	if m.events != nil {
		eventType := "collector.run.finished"
		if err != nil {
			eventType = "collector.run.failed"
		}
		m.events.Publish(realtime.Event{Type: eventType, Resource: name, Status: status, Payload: map[string]any{"durationMs": finishedAt.Sub(startedAt).Milliseconds(), "error": errText}})
	}
}

func (m *Manager) collectServers(ctx context.Context) error {
	servers, err := m.store.ListServers()
	if err != nil {
		return err
	}
	for _, server := range servers {
		payload := map[string]any{
			"id":         server.ID,
			"name":       server.Name,
			"host":       server.Host,
			"status":     server.Status,
			"dockerHost": server.DockerHost,
			"updatedAt":  server.UpdatedAt,
		}
		status := strings.TrimSpace(server.Status)
		if status == "" {
			status = "unknown"
		}
		if err := m.saveSnapshot(ctx, store.StatusSnapshot{
			Scope:       "server",
			ResourceID:  server.ID,
			ServerID:    server.ID,
			Status:      status,
			LastError:   server.LastError,
			Payload:     marshalPayload(payload),
			CollectedAt: time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) collectDockerSummaries(ctx context.Context) error {
	servers, err := m.store.ListServers()
	if err != nil {
		return err
	}
	var failures []string
	for _, publicServer := range servers {
		if strings.TrimSpace(publicServer.DockerHost) == "" {
			continue
		}
		server, err := m.store.GetServer(publicServer.ID, true)
		if err != nil {
			failures = append(failures, publicServer.ID+": "+err.Error())
			continue
		}
		child, cancel := context.WithTimeout(ctx, m.timeout)
		summary, err := adapter.DockerSummaryForServer(child, server)
		cancel()
		status := "available"
		errText := ""
		payload := map[string]any{
			"available": false,
			"endpoint":  server.DockerHost,
		}
		if err != nil {
			status = "failed"
			errText = err.Error()
			failures = append(failures, server.ID+": "+err.Error())
		} else {
			payload["available"] = true
			payload["summary"] = summary
		}
		if saveErr := m.saveSnapshot(ctx, store.StatusSnapshot{
			Scope:       "docker.summary",
			ResourceID:  server.ID,
			ServerID:    server.ID,
			Status:      status,
			LastError:   errText,
			Payload:     marshalPayload(payload),
			CollectedAt: time.Now(),
		}); saveErr != nil {
			return saveErr
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (m *Manager) collectAppInstances(ctx context.Context) error {
	if m.apps == nil {
		return nil
	}
	instances, err := m.store.ListAppInstances()
	if err != nil {
		return err
	}
	var failures []string
	for _, instance := range instances {
		app := strings.ToLower(strings.TrimSpace(instance.App))
		if !collectableAppInstance(app) || strings.TrimSpace(instance.ServerID) == "" {
			continue
		}
		module, ok := m.apps.Get(app)
		if !ok {
			continue
		}
		checkModule, ok := module.(registry.CheckModule)
		if !ok {
			continue
		}
		server, err := m.store.GetServer(instance.ServerID, true)
		if err != nil {
			errText := logmask.Mask(err.Error())
			failures = append(failures, instance.ID+": "+errText)
			if saveErr := m.saveAppInstanceSnapshot(ctx, instance, registry.InstanceStatus{Status: "failed", Message: errText}, errText); saveErr != nil {
				return saveErr
			}
			continue
		}
		child, cancel := context.WithTimeout(ctx, m.timeout)
		status, checkErr := checkModule.Check(child, registry.CheckRequest{
			Instance: instance,
			Server:   server,
			Language: "zh",
			Actor:    "collector",
		}, registry.RunContext{
			Log:       silentLogger{},
			TargetLog: func(string) registry.Logger { return silentLogger{} },
		})
		cancel()
		errText := ""
		if checkErr != nil {
			errText = logmask.Mask(checkErr.Error())
			failures = append(failures, instance.ID+": "+errText)
		}
		if strings.TrimSpace(status.Status) == "" {
			if checkErr != nil {
				status.Status = "failed"
			} else {
				status.Status = instance.Status
			}
		}
		if strings.TrimSpace(status.Message) != "" {
			status.Message = logmask.Mask(status.Message)
		}
		if err := m.saveAppInstanceSnapshot(ctx, instance, status, errText); err != nil {
			return err
		}
	}
	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	return nil
}

func (m *Manager) saveAppInstanceSnapshot(ctx context.Context, instance store.AppInstance, status registry.InstanceStatus, errText string) error {
	snapshotStatus := strings.ToLower(strings.TrimSpace(status.Status))
	if snapshotStatus == "" {
		snapshotStatus = "unknown"
	}
	payload := map[string]any{
		"app":        instance.App,
		"instanceId": instance.ID,
		"serverId":   instance.ServerID,
		"version":    instance.Version,
		"topology":   instance.Topology,
		"status":     snapshotStatus,
		"message":    strings.TrimSpace(status.Message),
		"details":    status.Details,
		"updatedAt":  time.Now().UTC().Format(time.RFC3339),
	}
	return m.saveSnapshot(ctx, store.StatusSnapshot{
		Scope:       "app.instance",
		ResourceID:  instance.ID,
		ServerID:    instance.ServerID,
		Status:      snapshotStatus,
		LastError:   errText,
		Payload:     marshalPayload(payload),
		CollectedAt: time.Now(),
	})
}

func collectableAppInstance(app string) bool {
	switch strings.ToLower(strings.TrimSpace(app)) {
	case "mysql", "redis", "mysql-router", "minio", "nacos":
		return true
	default:
		return false
	}
}

func (m *Manager) collectAIFARRuntime(ctx context.Context) error {
	instances, err := m.store.ListAppInstances()
	if err != nil {
		return err
	}
	for _, instance := range instances {
		if strings.TrimSpace(instance.App) != "aifar" {
			continue
		}
		deployments, _ := m.store.ListAIFARDeployments(instance.ID)
		pods, _ := m.store.ListAIFARPods(instance.ID)
		endpoints, _ := m.store.ListAIFARServiceEndpoints(instance.ID)
		status := summarizeAIFARStatus(instance.Status, deployments, pods, endpoints)
		desiredReplicas := countDesiredReplicas(deployments)
		if desiredReplicas > 0 {
			if dockerSnapshot, err := m.store.GetStatusSnapshot("docker.summary", instance.ServerID); err == nil && strings.EqualFold(dockerSnapshot.Status, "failed") {
				status = "no-endpoints"
			}
		}
		payload := map[string]any{
			"instanceId":      instance.ID,
			"serverId":        instance.ServerID,
			"version":         instance.Version,
			"status":          status,
			"appStatus":       instance.Status,
			"deployments":     len(deployments),
			"pods":            len(pods),
			"readyPods":       countReadyPods(pods),
			"activeEndpoints": countActiveEndpoints(endpoints),
			"desiredReplicas": desiredReplicas,
			"updatedAt":       instance.UpdatedAt,
		}
		if err := m.saveSnapshot(ctx, store.StatusSnapshot{
			Scope:       "aifar.runtime",
			ResourceID:  instance.ID,
			ServerID:    instance.ServerID,
			Status:      status,
			Payload:     marshalPayload(payload),
			CollectedAt: time.Now(),
		}); err != nil {
			return err
		}
	}
	return nil
}

type silentLogger struct{}

func (silentLogger) Info(string, ...any)  {}
func (silentLogger) Error(string, ...any) {}

func (m *Manager) saveSnapshot(ctx context.Context, snapshot store.StatusSnapshot) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	saved, changed, err := m.store.UpsertStatusSnapshot(snapshot)
	if err != nil {
		return err
	}
	if changed && m.events != nil {
		m.events.Publish(realtime.Event{
			Type:        "status." + saved.Scope + ".updated",
			Resource:    saved.Scope,
			ResourceID:  saved.ResourceID,
			ServerID:    saved.ServerID,
			InstanceID:  instanceIDForSnapshot(saved),
			Status:      saved.Status,
			Version:     saved.Version,
			CollectedAt: saved.CollectedAt,
			Payload:     snapshotEventPayload(saved),
		})
	}
	return nil
}

func snapshotEventPayload(snapshot store.StatusSnapshot) map[string]any {
	payload := map[string]any{}
	if strings.TrimSpace(snapshot.Payload) != "" {
		_ = json.Unmarshal([]byte(snapshot.Payload), &payload)
	}
	return map[string]any{
		"scope":       snapshot.Scope,
		"resourceId":  snapshot.ResourceID,
		"serverId":    snapshot.ServerID,
		"status":      snapshot.Status,
		"payload":     payload,
		"lastError":   snapshot.LastError,
		"version":     snapshot.Version,
		"collectedAt": snapshot.CollectedAt,
		"updatedAt":   snapshot.UpdatedAt,
	}
}

func marshalPayload(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(data)
}

func summarizeAIFARStatus(instanceStatus string, deployments []store.AIFARDeployment, pods []store.AIFARPod, endpoints []store.AIFARServiceEndpoint) string {
	status := strings.ToLower(strings.TrimSpace(instanceStatus))
	if status == "failed" || status == "error" {
		return "failed"
	}
	if len(deployments) == 0 {
		if status != "" {
			return status
		}
		return "unknown"
	}
	readyPods := countReadyPods(pods)
	desired := countDesiredReplicas(deployments)
	if desired == 0 {
		return "offline"
	}
	if readyPods == 0 && countActiveEndpoints(endpoints) == 0 {
		return "no-endpoints"
	}
	if readyPods < desired {
		return "degraded"
	}
	return "running"
}

func countReadyPods(pods []store.AIFARPod) int {
	total := 0
	for _, pod := range pods {
		if pod.Ready {
			total++
		}
	}
	return total
}

func countActiveEndpoints(endpoints []store.AIFARServiceEndpoint) int {
	total := 0
	for _, endpoint := range endpoints {
		if endpoint.Ready && strings.EqualFold(endpoint.State, "active") {
			total++
		}
	}
	return total
}

func countDesiredReplicas(deployments []store.AIFARDeployment) int {
	total := 0
	for _, deployment := range deployments {
		if deployment.DesiredReplicas > 0 {
			total += deployment.DesiredReplicas
		}
	}
	return total
}

func instanceIDForSnapshot(snapshot store.StatusSnapshot) string {
	if snapshot.Scope == "aifar.runtime" {
		return snapshot.ResourceID
	}
	return ""
}
