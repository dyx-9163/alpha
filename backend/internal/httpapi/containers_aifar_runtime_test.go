package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestAIFARRuntimeReturnsDegradedControlPlaneWhenAgentMissing(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime?serverId="+server.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body aifarRuntimeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.RuntimeStatus != "degraded" || body.Agent.Status != "missing" {
		t.Fatalf("expected degraded runtime with missing agent, got status=%s agent=%+v", body.RuntimeStatus, body.Agent)
	}
	if len(body.Instances) != 1 || body.Instances[0].ID != instance.ID || body.Instances[0].Legacy {
		t.Fatalf("unexpected instances: %+v", body.Instances)
	}
	if body.Instances[0].RuntimeConfig == nil || body.Instances[0].RuntimeConfig["global"] == nil {
		t.Fatalf("expected runtime config in instance response: %+v", body.Instances[0])
	}
	if len(body.Services) != 1 || body.Services[0].ServiceName != "permission" || body.Services[0].AppName != "alpha-permission" || body.Services[0].Status != "ready" {
		t.Fatalf("unexpected services: %+v", body.Services)
	}
}

func TestAIFARRuntimeServiceSummaryIgnoresNilResidualRecords(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "file",
		DesiredReplicas: 2,
		CurrentRevision: "<nil>",
		Status:          "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "file",
		Revision:    "rev-good",
		Image:       "aifar-file:rev-good",
		DesiredPods: 2,
		ReadyPods:   2,
		Status:      "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "file",
		Revision:    "<nil>",
		Image:       "aifar-file:<nil>",
		DesiredPods: 2,
		ReadyPods:   2,
		Status:      "ready",
	}); err != nil {
		t.Fatal(err)
	}
	for _, pod := range []store.AIFARPod{
		{InstanceID: instance.ID, ServiceName: "file", Revision: "<nil>", PodID: "file--nil--r1", ContainerName: "aifar-pod-admin-file--nil--r1", Port: 38005, Status: "ready", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", Revision: "<nil>", PodID: "file--nil--r2", ContainerName: "aifar-pod-admin-file--nil--r2", Port: 38005, Status: "ready", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", Revision: "rev-good", PodID: "file-rev-good-r1", ContainerName: "aifar-pod-admin-file-rev-good-r1", Port: 38005, Status: "ready", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", Revision: "rev-good", PodID: "file-rev-good-r2", ContainerName: "aifar-pod-admin-file-rev-good-r2", Port: 38005, Status: "ready", Ready: true},
	} {
		if _, err := db.SaveAIFARPod(pod); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "file", []store.AIFARServiceEndpoint{
		{InstanceID: instance.ID, ServiceName: "file", PodID: "file--nil--r1", ContainerName: "aifar-pod-admin-file--nil--r1", Revision: "<nil>", Port: 38005, State: "active", Ready: true},
		{InstanceID: instance.ID, ServiceName: "file", PodID: "file--nil--r2", ContainerName: "aifar-pod-admin-file--nil--r2", Revision: "<nil>", Port: 38005, State: "active", Ready: true},
	}); err != nil {
		t.Fatal(err)
	}

	response := aifarRuntimeResponse{RuntimeStatus: "ready", Agent: aifarRuntimeAgent{Status: "running"}}
	api.appendAIFARInstanceRuntime(&response, instance, map[string]adapter.DockerContainer{
		"aifar-pod-admin-file-rev-good-r1": {Name: "aifar-pod-admin-file-rev-good-r1", Image: "aifar-file:rev-good", State: "running", Status: "Up 1 minute (healthy)"},
		"aifar-pod-admin-file-rev-good-r2": {Name: "aifar-pod-admin-file-rev-good-r2", Image: "aifar-file:rev-good", State: "running", Status: "Up 1 minute (healthy)"},
	}, nil)

	if len(response.Services) != 1 {
		t.Fatalf("expected one service, got %+v", response.Services)
	}
	if len(response.Deployments) != 1 || response.Deployments[0].DeploymentName != "alpha-file" || response.Deployments[0].PodRevision != "rev-good" {
		t.Fatalf("expected deployment identity and pod revision to be split, got %+v", response.Deployments)
	}
	service := response.Services[0]
	if service.AppName != "alpha-file" || service.Image != "aifar-file:rev-good" || service.ReadyReplicas != 2 || service.Status != "ready" {
		t.Fatalf("expected service summary to use real ready pods, got %+v", service)
	}
	stale := 0
	for _, pod := range response.Pods {
		if strings.Contains(pod.ContainerName, "--nil--") {
			stale++
			if pod.Status != "stale" || pod.Revision != "" {
				t.Fatalf("expected nil residual pod to be stale with empty revision, got %+v", pod)
			}
		}
	}
	if stale != 2 {
		t.Fatalf("expected two stale residual pods, got %d in %+v", stale, response.Pods)
	}
}

func TestAIFARRuntimeScaleOutRequiresAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/containers/aifar/services/permission/scale-out?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
		t.Fatalf("expected agent-required error, got %s", rec.Body.String())
	}
}

func TestAIFARRuntimeInstallServicesRequiresAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"instanceId":"` + instance.ID + `","services":["file"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/containers/aifar/services/install?serverId="+server.ID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
		t.Fatalf("expected agent-required error, got %s", rec.Body.String())
	}
}

func TestAIFARRuntimeConfigRequiresAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"instanceId":"` + instance.ID + `","global":{"appCPUs":"3.0","appMemoryLimit":"3GB","jvmInitialRAMPercentage":25,"jvmMaxRAMPercentage":75}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v2/containers/aifar/runtime/config?serverId="+server.ID, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "AIFAR_AGENT_REQUIRED") {
		t.Fatalf("expected agent-required error, got %s", rec.Body.String())
	}
}

func TestAIFARRuntimeCleanupAndAgentUninstallStartTasksWithoutAgent(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeAIFARRuntimeActionModule{}
	api.apps = registry.New(module)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/containers/json" {
			_ = json.NewEncoder(w).Encode([]map[string]any{})
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer dockerAPI.Close()
	server, instance := seedAIFARRuntimeFixture(t, db, dockerAPI.URL)
	token := issueTestToken(t, db, secret, "owner", "owner")

	for _, tc := range []struct {
		name   string
		path   string
		action string
	}{
		{name: "cleanup", path: "/api/v2/containers/aifar/runtime/cleanup-stale", action: "aifar.runtime.cleanup"},
		{name: "uninstall-agent", path: "/api/v2/containers/aifar/runtime/uninstall-agent", action: "aifar.agent.uninstall"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path+"?serverId="+server.ID, strings.NewReader(`{"instanceId":"`+instance.ID+`"}`))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			api.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusAccepted {
				t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			taskID, _ := body["taskId"].(string)
			if taskID == "" {
				t.Fatalf("expected taskId in response: %+v", body)
			}
			waitForTaskStatus(t, db, taskID, "success")
			assertAuditExists(t, db, tc.action, "running", "owner", instance.ID)
		})
	}
	if module.cleanupCalls != 1 || module.uninstallCalls != 1 {
		t.Fatalf("expected cleanup and uninstall module calls, got cleanup=%d uninstall=%d", module.cleanupCalls, module.uninstallCalls)
	}
}

type fakeAIFARRuntimeActionModule struct {
	cleanupCalls   int
	uninstallCalls int
}

func (m *fakeAIFARRuntimeActionModule) Name() string { return "aifar" }

func (m *fakeAIFARRuntimeActionModule) Manifest(lang string) registry.Manifest {
	return registry.Manifest{Name: "aifar", BackendReady: true}
}

func (m *fakeAIFARRuntimeActionModule) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}

func (m *fakeAIFARRuntimeActionModule) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (m *fakeAIFARRuntimeActionModule) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	return nil
}

func (m *fakeAIFARRuntimeActionModule) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	return nil
}

func (m *fakeAIFARRuntimeActionModule) CleanupRuntimeStalePods(ctx context.Context, req registry.RuntimeCleanupRequest, run registry.RunContext) error {
	m.cleanupCalls++
	return nil
}

func (m *fakeAIFARRuntimeActionModule) UninstallRuntimeAgent(ctx context.Context, req registry.RuntimeAgentUninstallRequest, run registry.RunContext) error {
	m.uninstallCalls++
	return nil
}

func seedAIFARRuntimeFixture(t *testing.T, db *store.Store, dockerHost string) (store.Server, store.AppInstance) {
	t.Helper()
	server, err := db.SaveServer(store.Server{Name: "docker-1", Host: "10.0.0.10", DockerHost: dockerHost, DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "aifar",
		Version:  "runtime-v2",
		ServerID: server.ID,
		Status:   "installed",
		Metadata: `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin","endpoint":"10.0.0.10:8080","gatewayEndpoint":"10.0.0.10:38000","gatewayPort":38000,"webPort":8080,"runtimeService":"aifar-agent"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(store.AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "permission",
		DesiredReplicas: 1,
		CurrentRevision: "rev-1",
		Status:          "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(store.AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "permission",
		Revision:    "rev-1",
		Image:       "aifar-permission:rev-1",
		DesiredPods: 1,
		ReadyPods:   1,
		Status:      "ready",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARPod(store.AIFARPod{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		Revision:      "rev-1",
		PodID:         "r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Port:          38010,
		Status:        "running",
		Ready:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "permission", []store.AIFARServiceEndpoint{{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		PodID:         "r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Revision:      "rev-1",
		Port:          38010,
		State:         "active",
		Ready:         true,
	}}); err != nil {
		t.Fatal(err)
	}
	return server, instance
}
