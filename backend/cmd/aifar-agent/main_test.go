package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"aifar-deployment/backend/internal/runtimeagent"
)

func TestStatusDoesNotRequireDockerHealth(t *testing.T) {
	handler := newAgentHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error {
		return errors.New("docker is not ready")
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"status":"running"`) {
		t.Fatalf("unexpected status response: %s", recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"restart-runtime"`) {
		t.Fatalf("status must advertise Runtime restart support: %s", recorder.Body.String())
	}
}

func TestServeAddressMustBeLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:18081", "127.42.9.8:18081", "[::1]:18081", "localhost:18081", "LOCALHOST:18081"} {
		if err := validateAgentListenAddress(addr); err != nil {
			t.Errorf("loopback address %q rejected: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:18081", "[::]:18081", "192.168.1.10:18081", "example.com:18081", ":18081", "[fe80::1%eth0]:18081", "127.0.0.1", "127.0.0.1:0", "127.0.0.1:70000"} {
		if err := validateAgentListenAddress(addr); err == nil {
			t.Errorf("non-loopback/invalid address %q accepted", addr)
		}
	}
	if err := serve("0.0.0.0:18081"); err == nil || !strings.Contains(err.Error(), "unsafe") {
		t.Fatalf("serve did not fail closed before listening: %v", err)
	}
}

func TestHealthStillReportsDockerHealth(t *testing.T) {
	handler := newAgentHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error {
		return errors.New("docker is not ready")
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("health code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
}

func TestAgentHandlerRecoversPanic(t *testing.T) {
	handler := newAgentHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error {
		panic("boom")
	})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/health", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("health code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "aifar-agent handler panic") {
		t.Fatalf("expected recovered panic response, got %s", recorder.Body.String())
	}
}

func TestPostRuntimeSpecRetriesEOF(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			hijacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer does not support hijack")
			}
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatalf("hijack failed: %v", err)
			}
			_ = conn.Close()
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "reconciled"})
	}))
	defer server.Close()

	err := postRuntimeSpec(context.Background(), strings.TrimPrefix(server.URL, "http://"), runtimeagent.RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
	})

	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if calls.Load() < 2 {
		t.Fatalf("expected at least two attempts, got %d", calls.Load())
	}
}

func TestPostRuntimeRestartUsesRestartAllEndpointAndPayload(t *testing.T) {
	spec := runtimeagent.RuntimeSpec{InstanceID: "admin", InstallRoot: "/aifar/apps/admin"}
	var gotPath string
	var gotSpec runtimeagent.RuntimeSpec
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotSpec); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	}))
	defer server.Close()

	if err := postRuntimeRestart(context.Background(), strings.TrimPrefix(server.URL, "http://"), spec); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/runtime/restart-all" {
		t.Fatalf("expected restart endpoint, got %q", gotPath)
	}
	if gotSpec.InstanceID != "admin" || gotSpec.InstallRoot != "/aifar/apps/admin" {
		t.Fatalf("unexpected posted spec: %#v", gotSpec)
	}
}

func TestPostRuntimeRestartDoesNotRetryEOF(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		hijacker := w.(http.Hijacker)
		conn, _, err := hijacker.Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.Close()
	}))
	defer server.Close()

	if err := postRuntimeRestart(context.Background(), strings.TrimPrefix(server.URL, "http://"), runtimeagent.RuntimeSpec{InstanceID: "admin"}); err == nil {
		t.Fatal("expected lost restart response to be returned without retry")
	}
	if calls.Load() != 1 {
		t.Fatalf("expected exactly one attempt, got %d", calls.Load())
	}
}

func TestRuntimeRestartHandlerReturnsManagerFailure(t *testing.T) {
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir(), Runner: failingRestartRunner{}})
	handler := newAgentHandler(manager, func(context.Context) error { return nil })
	body, err := json.Marshal(runtimeagent.NormalizeSpec(runtimeagent.RuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Deployments: []runtimeagent.DeploymentSpec{{ServiceName: "gateway", Image: "gateway:rev-1", PodRevision: "rev-1", Replicas: 1}},
	}))
	if err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/runtime/restart-all", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "RUNTIME_RESTART_FAILED") || strings.Contains(recorder.Body.String(), "remote-secret") {
		t.Fatalf("expected sanitized manager failure, got %s", recorder.Body.String())
	}
}

func TestAgentStatusAdvertisesRuntimeRestart(t *testing.T) {
	features, ok := agentStatus()["features"].([]string)
	if !ok {
		t.Fatalf("unexpected feature shape: %#v", agentStatus()["features"])
	}
	for _, feature := range features {
		if feature == "restart-runtime" {
			return
		}
	}
	t.Fatalf("restart-runtime feature missing: %#v", features)
}

func TestPutDeploymentReturnsAcceptedBeforeReadiness(t *testing.T) {
	stateDir := t.TempDir()
	store := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(agentTestInstanceConfig()); err != nil {
		t.Fatal(err)
	}
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: failingRestartRunner{}})
	handler := newAgentHandler(manager, func(context.Context) error { return nil })
	body, _ := json.Marshal(agentTestManifest(1, "permission:rev-1"))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPut, "/runtime/instances/admin/deployments/permission", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusAccepted {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), `"accepted":true`) {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}

func TestPutDeploymentPersistenceFailureReturnsSanitized500(t *testing.T) {
	stateDir := t.TempDir()
	store := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(agentTestInstanceConfig()); err != nil {
		t.Fatal(err)
	}
	deploymentsPath := filepath.Join(stateDir, "admin", "deployments")
	if err := os.WriteFile(deploymentsPath, []byte("remote-secret-path"), 0o600); err != nil {
		t.Fatal(err)
	}
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, ManifestStore: store})
	handler := newAgentHandler(manager, func(context.Context) error { return nil })
	body, _ := json.Marshal(agentTestManifest(1, "permission:rev-1"))
	request := httptest.NewRequest(http.MethodPut, "/runtime/instances/admin/deployments/permission", bytes.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusInternalServerError || !strings.Contains(recorder.Body.String(), "AGENT_STATE_PERSISTENCE_FAILED") {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "remote-secret") || strings.Contains(recorder.Body.String(), stateDir) {
		t.Fatalf("persistence error leaked internal data: %s", recorder.Body.String())
	}
}

func TestPutDeploymentRejectsMismatchStaleConflictAndUnsafeBodies(t *testing.T) {
	stateDir := t.TempDir()
	store := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(agentTestInstanceConfig()); err != nil {
		t.Fatal(err)
	}
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: failingRestartRunner{}})
	handler := newAgentHandler(manager, func(context.Context) error { return nil })
	put := func(path string, body []byte, contentType string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
		if contentType != "" {
			request.Header.Set("Content-Type", contentType)
		}
		handler.ServeHTTP(recorder, request)
		return recorder
	}
	body1, _ := json.Marshal(agentTestManifest(2, "permission:rev-2"))
	schemaMismatch := agentTestManifest(2, "permission:rev-2")
	schemaMismatch.APIVersion = "wrong/v1"
	schemaBody, _ := json.Marshal(schemaMismatch)
	if got := put("/runtime/instances/admin/deployments/permission", schemaBody, "application/json"); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "INVALID_DEPLOYMENT_MANIFEST") {
		t.Fatalf("schema status=%d body=%s", got.Code, got.Body.String())
	}
	missingSchema := agentTestManifest(2, "permission:rev-2")
	missingSchema.APIVersion = ""
	missingSchema.Kind = ""
	missingSchemaBody, _ := json.Marshal(missingSchema)
	if got := put("/runtime/instances/admin/deployments/permission", missingSchemaBody, "application/json"); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "INVALID_DEPLOYMENT_MANIFEST") {
		t.Fatalf("missing schema status=%d body=%s", got.Code, got.Body.String())
	}
	if got := put("/runtime/instances/admin/deployments/permission", body1, ""); got.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("content type status=%d body=%s", got.Code, got.Body.String())
	}
	if got := put("/runtime/instances/admin/deployments/permission", []byte(`{"metadata":`), "application/json"); got.Code != http.StatusBadRequest {
		t.Fatalf("malformed status=%d body=%s", got.Code, got.Body.String())
	}
	if got := put("/runtime/instances/admin/deployments/file", body1, "application/json"); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "DEPLOYMENT_IDENTITY_MISMATCH") {
		t.Fatalf("mismatch status=%d body=%s", got.Code, got.Body.String())
	}
	if got := put("/runtime/instances/admin/deployments/permission", body1, "application/json"); got.Code != http.StatusAccepted {
		t.Fatalf("initial status=%d body=%s", got.Code, got.Body.String())
	}
	stale, _ := json.Marshal(agentTestManifest(1, "permission:rev-1"))
	if got := put("/runtime/instances/admin/deployments/permission", stale, "application/json"); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "STALE_DEPLOYMENT_GENERATION") || strings.Contains(got.Body.String(), "permission:rev-1") {
		t.Fatalf("stale status=%d body=%s", got.Code, got.Body.String())
	}
	conflict, _ := json.Marshal(agentTestManifest(2, "permission:other"))
	if got := put("/runtime/instances/admin/deployments/permission", conflict, "application/json"); got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "DEPLOYMENT_GENERATION_CONFLICT") || strings.Contains(got.Body.String(), "permission:other") {
		t.Fatalf("conflict status=%d body=%s", got.Code, got.Body.String())
	}
	unknown := append(body1[:len(body1)-1], []byte(`,"secretUnknown":"do-not-echo"}`)...)
	if got := put("/runtime/instances/admin/deployments/permission", unknown, "application/json"); got.Code != http.StatusBadRequest || strings.Contains(got.Body.String(), "do-not-echo") {
		t.Fatalf("unknown status=%d body=%s", got.Code, got.Body.String())
	}
	oversize := append([]byte(`{"unknown":"`), bytes.Repeat([]byte("x"), int(maxAgentRequestBodyBytes))...)
	oversize = append(oversize, []byte(`"}`)...)
	if got := put("/runtime/instances/admin/deployments/permission", oversize, "application/json"); got.Code != http.StatusRequestEntityTooLarge || strings.Contains(got.Body.String(), string(oversize[:32])) {
		t.Fatalf("oversize status=%d body=%s", got.Code, got.Body.String())
	}
	if got := put("/runtime/instances/admin/deployments/%2e%2e", body1, "application/json"); got.Code != http.StatusBadRequest {
		t.Fatalf("traversal status=%d body=%s", got.Code, got.Body.String())
	}
	if got := put("/runtime/instances/admin/deployments/permission/extra", body1, "application/json"); got.Code != http.StatusNotFound {
		t.Fatalf("tail status=%d body=%s", got.Code, got.Body.String())
	}
}

func TestGetAndReconcileDeploymentRoutes(t *testing.T) {
	stateDir := t.TempDir()
	store := &runtimeagent.ManifestStore{StateDir: stateDir}
	if err := store.PutInstance(agentTestInstanceConfig()); err != nil {
		t.Fatal(err)
	}
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: stateDir, ManifestStore: store, Runner: failingRestartRunner{}})
	if _, err := manager.AcceptDeployment(context.Background(), agentTestManifest(1, "permission:rev-1")); err != nil {
		t.Fatal(err)
	}
	handler := newAgentHandler(manager, func(context.Context) error { return nil })
	for _, tc := range []struct {
		method, path string
		want         int
	}{
		{http.MethodGet, "/runtime/instances/admin/deployments/permission", http.StatusOK},
		{http.MethodGet, "/runtime/instances/admin/deployments/missing", http.StatusNotFound},
		{http.MethodPost, "/runtime/instances/admin/deployments/permission/reconcile", http.StatusAccepted},
		{http.MethodDelete, "/runtime/instances/admin/deployments/permission", http.StatusMethodNotAllowed},
	} {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, nil))
		if recorder.Code != tc.want {
			t.Fatalf("%s %s status=%d body=%s", tc.method, tc.path, recorder.Code, recorder.Body.String())
		}
	}
}

func TestServiceCLITransportAndValidation(t *testing.T) {
	manifest := agentTestManifest(7, "permission:rev-7")
	path := filepath.Join(t.TempDir(), "manifest.json")
	data, _ := json.Marshal(manifest)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	legacyPath := filepath.Join(t.TempDir(), "runtime-spec.json")
	legacyData, _ := json.Marshal(agentTestLegacySpec())
	if err := os.WriteFile(legacyPath, legacyData, 0o600); err != nil {
		t.Fatal(err)
	}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.EscapedPath())
		switch r.Method {
		case http.MethodPut:
			writeJSON(w, http.StatusAccepted, runtimeagent.DeploymentAcceptance{Accepted: true, Generation: 7, SpecHash: "safe"})
		case http.MethodGet:
			writeJSON(w, http.StatusOK, runtimeagent.DeploymentState{InstanceID: "admin", ServiceName: "permission", Generation: 7})
		default:
			writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
		}
	}))
	defer server.Close()
	addr := strings.TrimPrefix(server.URL, "http://")
	var out bytes.Buffer
	for _, command := range [][]string{
		{"apply-deployment", "--manifest", path, "--addr", addr},
		{"get-deployment", "--instance", "admin", "--service", "permission", "--addr", addr},
		{"reconcile-deployment", "--instance", "admin", "--service", "permission", "--addr", addr},
		{"bootstrap-runtime", "--spec", legacyPath, "--addr", addr},
	} {
		if err := runServiceAgentCommand(command[0], command[1:], &out); err != nil {
			t.Fatalf("%s: %v", command[0], err)
		}
	}
	want := []string{
		"PUT /runtime/instances/admin/deployments/permission",
		"GET /runtime/instances/admin/deployments/permission",
		"POST /runtime/instances/admin/deployments/permission/reconcile",
		"POST /runtime/bootstrap",
	}
	if !slices.Equal(requests, want) {
		t.Fatalf("requests=%v want=%v", requests, want)
	}
	if err := runServiceAgentCommand("apply-deployment", []string{"--addr", addr}, &out); err == nil {
		t.Fatal("missing --manifest accepted")
	}
	if err := runServiceAgentCommand("get-deployment", []string{"--instance", "../admin", "--service", "permission", "--addr", addr}, &out); err == nil {
		t.Fatal("unsafe identity accepted")
	}
}

func TestServiceCLINon2xxDoesNotEchoRemoteDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeAgentError(w, http.StatusConflict, "DEPLOYMENT_GENERATION_CONFLICT", "safe", map[string]string{"secret": "do-not-echo"})
	}))
	defer server.Close()
	var out bytes.Buffer
	err := runServiceAgentCommand("get-deployment", []string{"--instance", "admin", "--service", "permission", "--addr", strings.TrimPrefix(server.URL, "http://")}, &out)
	if err == nil || strings.Contains(err.Error(), "do-not-echo") || strings.Contains(out.String(), "do-not-echo") {
		t.Fatalf("err=%v out=%q", err, out.String())
	}
}

func TestBootstrapHTTPDisablesLegacyReconcile(t *testing.T) {
	manager := runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir(), Runner: failingRestartRunner{}})
	handler := newAgentHandler(manager, func(context.Context) error { return nil })
	data, _ := json.Marshal(agentTestLegacySpec())
	request := func(path string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	if got := request("/runtime/bootstrap"); got.Code != http.StatusAccepted {
		t.Fatalf("bootstrap status=%d body=%s", got.Code, got.Body.String())
	}
	for _, path := range []string{"/runtime/reconcile", "/runtime/bootstrap"} {
		got := request(path)
		if got.Code != http.StatusConflict || !strings.Contains(got.Body.String(), "LEGACY_RUNTIME_SPEC_DISABLED") || strings.Contains(got.Body.String(), "SECRET-GROUP") {
			t.Fatalf("%s status=%d body=%s", path, got.Code, got.Body.String())
		}
	}
}

func TestStatusRouteAdvertisesPerServiceFeaturesExactlyOnce(t *testing.T) {
	handler := newAgentHandler(runtimeagent.NewManager(runtimeagent.ManagerOptions{StateDir: t.TempDir()}), func(context.Context) error { return nil })
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	var status map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	values := status["features"].([]any)
	features := make([]string, 0, len(values))
	for _, value := range values {
		features = append(features, value.(string))
	}
	for _, want := range perServiceFeatures {
		if count := countString(features, want); count != 1 {
			t.Fatalf("feature %q count=%d features=%v", want, count, features)
		}
	}
}

func TestAgentStatusAdvertisesPerServiceFeaturesExactlyOnce(t *testing.T) {
	features := agentStatus()["features"].([]string)
	for _, want := range []string{"service-manifest-v1", "service-generation-v1", "per-service-reconcile", "per-service-restart", "service-conditions-v1"} {
		if count := countString(features, want); count != 1 {
			t.Fatalf("feature %q count=%d features=%v", want, count, features)
		}
	}
}

func agentTestInstanceConfig() runtimeagent.InstanceConfig {
	return runtimeagent.NormalizeInstanceConfig(runtimeagent.InstanceConfig{InstanceID: "admin", InstallRoot: "/aifar/apps/admin", Network: "aifar-network"})
}

func agentTestManifest(generation int64, image string) runtimeagent.DeploymentManifest {
	return runtimeagent.NormalizeDeploymentManifest(runtimeagent.DeploymentManifest{
		Metadata: runtimeagent.DeploymentMetadata{InstanceID: "admin", Name: "permission", Generation: generation},
		Spec:     runtimeagent.DeploymentSpec{ServiceName: "permission", Image: image, PodRevision: "rev-1", Replicas: 1},
		Service:  runtimeagent.ServiceSpec{Name: "permission", AppName: "permission", Port: 8081},
	})
}

func agentTestLegacySpec() runtimeagent.LegacyRuntimeSpec {
	return runtimeagent.NormalizeSpec(runtimeagent.LegacyRuntimeSpec{
		InstanceID:  "admin",
		InstallRoot: "/aifar/apps/admin",
		Network:     "aifar-network",
		Ingress:     runtimeagent.IngressSpec{GatewayService: "gateway", WebService: "gateway", GatewayPort: 38000, WebPort: 8080},
		Nacos:       runtimeagent.NacosSpec{Namespace: "prod", Group: "SECRET-GROUP"},
		Services: []runtimeagent.ServiceSpec{
			{Name: "gateway", AppName: "gateway", Port: 8080},
			{Name: "permission", AppName: "permission", Port: 8081},
		},
		Deployments: []runtimeagent.DeploymentSpec{
			{ServiceName: "gateway", Image: "gateway:rev-1", PodRevision: "rev-1", Replicas: 1},
			{ServiceName: "permission", Image: "permission:rev-1", PodRevision: "rev-1", Replicas: 1},
		},
	})
}

func countString(values []string, want string) int {
	count := 0
	for _, value := range values {
		if value == want {
			count++
		}
	}
	return count
}

type failingRestartRunner struct{}

func (failingRestartRunner) Run(_ context.Context, name string, args ...string) (runtimeagent.CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	if strings.Contains(call, "docker inspect -f {{.Id}}") {
		return runtimeagent.CommandResult{Stdout: "container-id\n"}, nil
	}
	if strings.Contains(call, "docker run ") {
		return runtimeagent.CommandResult{Stderr: "remote-secret"}, errors.New("remote-secret")
	}
	return runtimeagent.CommandResult{}, nil
}
