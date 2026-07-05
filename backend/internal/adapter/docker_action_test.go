package adapter

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestDockerAPISummaryCountsVisibleImagesFromImageList(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/info":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Containers":9,"Images":47,"ContainersRunning":4,"Driver":"overlay2","ServerVersion":"24.0.9","DockerRootDir":"/var/lib/docker"}`))
		case "/images/json":
			if r.URL.Query().Get("all") != "" {
				t.Fatalf("summary image count should use docker images semantics, got all=%q", r.URL.Query().Get("all"))
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"Id":"sha256:a","RepoTags":["aifar-gateway:rev"],"Size":10,"Created":1},{"Id":"sha256:b","RepoTags":["<none>:<none>"],"Size":20,"Created":1}]`))
		case "/networks":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[]`))
		case "/volumes":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"Volumes":[]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	summary, err := dockerAPISummary(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("dockerAPISummary failed: %v", err)
	}
	if summary.Images != 2 {
		t.Fatalf("summary.Images = %d, want visible image list count 2", summary.Images)
	}
	if summary.Containers != 9 || summary.Running != 4 {
		t.Fatalf("summary lost info fields: %+v", summary)
	}
}

func TestParseDockerContainersIncludesLabels(t *testing.T) {
	rows := strings.Join([]string{
		`{"ID":"abc123","Names":"aifar-oauth-release","Image":"aifar-oauth:release","State":"running","Status":"Up 2 minutes","Ports":"38001/tcp","Networks":"aifar-network","CreatedAt":"now","Labels":"aifar.app=aifar,aifar.service=oauth,aifar.install-root=/aifar/apps/admin"}`,
	}, "\n")
	containers, err := parseDockerContainers([]byte(rows))
	if err != nil {
		t.Fatal(err)
	}
	if len(containers) != 1 {
		t.Fatalf("expected one container, got %+v", containers)
	}
	if containers[0].Labels["aifar.app"] != "aifar" || containers[0].Labels["aifar.service"] != "oauth" || containers[0].Labels["aifar.install-root"] != "/aifar/apps/admin" {
		t.Fatalf("expected AIFAR labels, got %+v", containers[0].Labels)
	}
}
