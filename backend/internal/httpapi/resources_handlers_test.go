package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourceRescanRunsAsTrackedWorkerTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	versionDir := filepath.Join(api.cfg.ResourceDir, "docker", "24.0.9")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "docker.tar"), []byte("bundle"), 0o644); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/resources/rescan", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected resource scan to return accepted task, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		TaskID string `json:"taskId"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.TaskID == "" || body.Status != "pending" {
		t.Fatalf("unexpected task response: %+v", body)
	}
	waitForTaskStatus(t, db, body.TaskID, "success")
	resources, err := db.ListResources()
	if err != nil {
		t.Fatal(err)
	}
	if len(resources) != 1 || resources[0].App != "docker" || resources[0].Version != "24.0.9" {
		t.Fatalf("resource scan task did not persist scanned resources: %+v", resources)
	}
	_, _, taskErr := db.GetTask(body.TaskID)
	if taskErr != nil {
		t.Fatal(taskErr)
	}
	assertAuditExists(t, db, "resources.scan", "success", "owner", api.cfg.ResourceDir)
}

func TestResourceRescanDoesNotReturnSynchronousResourceList(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/resources/rescan", nil)
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if strings.Contains(rec.Body.String(), `"app"`) || strings.Contains(rec.Body.String(), `"path"`) {
		t.Fatalf("resource rescan must not block to return resource rows, got body=%s", rec.Body.String())
	}
}
