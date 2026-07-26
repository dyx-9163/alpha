package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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

func TestPostRuntimeRestartRetriesEOF(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			hijacker := w.(http.Hijacker)
			conn, _, err := hijacker.Hijack()
			if err != nil {
				t.Fatal(err)
			}
			_ = conn.Close()
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "restarted"})
	}))
	defer server.Close()

	if err := postRuntimeRestart(context.Background(), strings.TrimPrefix(server.URL, "http://"), runtimeagent.RuntimeSpec{InstanceID: "admin"}); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("expected exactly two attempts, got %d", calls.Load())
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
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/runtime/restart-all", bytes.NewReader(body)))

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status code = %d, body = %s", recorder.Code, recorder.Body.String())
	}
	if !strings.Contains(recorder.Body.String(), "start failed") {
		t.Fatalf("expected manager failure in response, got %s", recorder.Body.String())
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

type failingRestartRunner struct{}

func (failingRestartRunner) Run(_ context.Context, name string, args ...string) (runtimeagent.CommandResult, error) {
	call := name + " " + strings.Join(args, " ")
	if strings.Contains(call, "docker inspect -f {{.Id}}") {
		return runtimeagent.CommandResult{Stdout: "container-id\n"}, nil
	}
	if strings.Contains(call, "docker run ") {
		return runtimeagent.CommandResult{Stderr: "start failed"}, errors.New("start failed")
	}
	return runtimeagent.CommandResult{}, nil
}
