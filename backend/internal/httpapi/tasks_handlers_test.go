package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"aifar-deployment/backend/internal/store"
)

func TestListTasksHidesUntrackableProbeTasks(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "owner", "owner")
	probe, err := db.CreateTask(store.Task{Type: "servers.probe", Target: "srv-1", Status: "running", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	install, err := db.CreateTask(store.Task{Type: "apps.mysql.install", Target: "srv-2", Status: "success", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected tasks list 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var tasks []store.Task
	if err := json.Unmarshal(rec.Body.Bytes(), &tasks); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.ID == probe.ID || task.Type == "servers.probe" {
			t.Fatalf("untrackable probe task should be hidden from task list, got %+v in %+v", task, tasks)
		}
	}
	if len(tasks) != 1 || tasks[0].ID != install.ID || !tasks[0].Trackable {
		t.Fatalf("expected only trackable install task, got %+v", tasks)
	}
}
