package main

import (
	"context"
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
