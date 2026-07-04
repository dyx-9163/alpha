package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	if len(body.Services) != 1 || body.Services[0].ServiceName != "permission" || body.Services[0].Status != "ready" {
		t.Fatalf("unexpected services: %+v", body.Services)
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

func seedAIFARRuntimeFixture(t *testing.T, db *store.Store, dockerHost string) (store.Server, store.AppInstance) {
	t.Helper()
	server, err := db.SaveServer(store.Server{Name: "docker-1", Host: "10.0.0.10", DockerHost: dockerHost, DeployDir: "/aifar/apps"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: server.ID,
		Status:   "installed",
		Metadata: `{"orchestrationModel":"k8s-like-v1","installRoot":"/aifar/apps/admin","endpoint":"10.0.0.10:8080","gatewayEndpoint":"10.0.0.10:38000","gatewayPort":38000,"webPort":8080,"ingressContainer":"aifar-admin-ingress"}`,
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
