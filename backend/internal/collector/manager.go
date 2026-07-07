package collector

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

type Publisher interface {
	Publish(realtime.Event)
}

type Manager struct {
	store     *store.Store
	events    Publisher
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
	m.run(ctx, "aifar.runtime", m.collectAIFARRuntime)
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
			"desiredReplicas": countDesiredReplicas(deployments),
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
		})
	}
	return nil
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
