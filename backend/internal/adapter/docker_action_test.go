package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDockerContainerCommand(t *testing.T) {
	tests := map[string]string{
		"start":   "start",
		"stop":    "stop",
		"restart": "restart",
		"remove":  "rm",
		"rm":      "rm",
	}
	for action, want := range tests {
		got, ok := dockerContainerCommand(action)
		if !ok {
			t.Fatalf("dockerContainerCommand(%q) returned unsupported", action)
		}
		if got != want {
			t.Fatalf("dockerContainerCommand(%q) = %q, want %q", action, got, want)
		}
	}
	if _, ok := dockerContainerCommand("prune"); ok {
		t.Fatal("dockerContainerCommand accepted unsupported action")
	}
}

func TestDockerAPIContainerActionRemoveUsesDelete(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := dockerAPIContainerAction(context.Background(), server.URL, "abc123", "remove"); err != nil {
		t.Fatalf("dockerAPIContainerAction remove failed: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/containers/abc123" {
		t.Fatalf("path = %s, want /containers/abc123", gotPath)
	}
}

func TestDockerAPIContainerActionStartUsesPost(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	if err := dockerAPIContainerAction(context.Background(), server.URL, "abc123", "start"); err != nil {
		t.Fatalf("dockerAPIContainerAction start failed: %v", err)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST", gotMethod)
	}
	if gotPath != "/containers/abc123/start" {
		t.Fatalf("path = %s, want /containers/abc123/start", gotPath)
	}
}

func TestDockerAPIImageRemoveEscapesImageReference(t *testing.T) {
	var gotMethod, gotPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := dockerAPIImageRemove(context.Background(), server.URL, "registry.example.com/ns/app:1.0"); err != nil {
		t.Fatalf("dockerAPIImageRemove failed: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("method = %s, want DELETE", gotMethod)
	}
	if gotPath != "/images/registry.example.com%2Fns%2Fapp:1.0" {
		t.Fatalf("path = %s, want escaped image path", gotPath)
	}
}
