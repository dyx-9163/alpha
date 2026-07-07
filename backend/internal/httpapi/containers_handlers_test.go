package httpapi

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestContainerLogsEventsStreamsSnapshot(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	dockerAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/containers/demo/logs" {
			http.NotFound(w, r)
			return
		}
		if got := r.URL.Query().Get("timestamps"); got != "1" {
			t.Errorf("expected timestamps=1, got %q", got)
		}
		if got := r.URL.Query().Get("tail"); got != "5" {
			t.Errorf("expected tail=5, got %q", got)
		}
		_, _ = w.Write([]byte("2026-07-07T10:00:00Z demo line\n"))
	}))
	defer dockerAPI.Close()
	server, err := db.SaveServer(store.Server{Name: "docker-1", Host: "10.0.0.10", DockerHost: dockerAPI.URL})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	srv := httptest.NewServer(api.Router())
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/api/v2/containers/demo/logs/events?serverId="+server.ID+"&tail=5&batch=1", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.Contains(got, "text/event-stream") {
		t.Fatalf("expected event-stream content type, got %q", got)
	}

	result := make(chan string, 1)
	readErr := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(resp.Body)
		var body strings.Builder
		for {
			line, err := reader.ReadString('\n')
			body.WriteString(line)
			text := body.String()
			if strings.Contains(text, "container-logs-snapshot") && strings.Contains(text, "demo line") {
				result <- text
				return
			}
			if err != nil {
				readErr <- err
				return
			}
		}
	}()

	select {
	case text := <-result:
		cancel()
		if !strings.Contains(text, `"mode":"snapshot"`) {
			t.Fatalf("expected snapshot payload, got %s", text)
		}
	case err := <-readErr:
		t.Fatal(err)
	case <-time.After(2 * time.Second):
		cancel()
		t.Fatal("timed out waiting for container log snapshot event")
	}
}
