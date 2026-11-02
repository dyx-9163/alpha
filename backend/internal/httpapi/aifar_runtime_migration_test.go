package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func TestRuntimeMigrationResumeBindsOriginalInstanceAndTerminalTask(t *testing.T) {
	for _, tc := range []struct {
		name, status, target string
		want                 int
	}{{"failed", "failed", "same", 202}, {"active", "running", "same", 409}, {"wrong-instance", "failed", "other", 400}} {
		t.Run(tc.name, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
			instance.Metadata = `{"orchestrationModel":"agent-runtime-v2"}`
			if _, err := db.SaveAppInstance(instance); err != nil {
				t.Fatal(err)
			}
			target := instance.ID
			if tc.target != "same" {
				target = "other"
			}
			previous, err := db.CreateTask(store.Task{Type: runtimeMigrationTaskType, Target: target, Status: tc.status, CreatedBy: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			var queued store.Task
			api.runtime.startExistingTask = func(task store.Task, _ string, _ worker.Job) (store.Task, error) { queued = task; return task, nil }
			token := issueTestToken(t, db, secret, "owner", "owner")
			req := httptest.NewRequest("POST", "/api/v2/apps/instances/"+instance.ID+"/runtime/migration", strings.NewReader(`{"resumeTaskId":"`+previous.ID+`"}`))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("got %d %s", rec.Code, rec.Body.String())
			}
			if tc.want == 202 {
				if queued.CorrelationID != previous.ID || queued.Target != instance.ID {
					t.Fatalf("lost recovery provenance: %+v", queued)
				}
				steps, err := db.ListTaskSteps(queued.ID)
				if err != nil || len(steps) != 5 {
					t.Fatalf("missing persisted migration stages: %v %v", steps, err)
				}
				if !decorateTask(queued).Trackable {
					t.Fatal("migration hidden from task center")
				}
			}
		})
	}
}

func TestRuntimeMigrationCompletedInstanceDoesNotReapplyLegacyGeneration(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest("POST", "/api/v2/apps/instances/"+instance.ID+"/runtime/migration", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	var result map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &result)
	if rec.Code != 200 || result["status"] != "complete" {
		t.Fatalf("got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeMigrationEntryRequiresManagePermission(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "reader", "viewer")
	for _, method := range []string{"GET", "POST"} {
		req := httptest.NewRequest(method, "/api/v2/apps/instances/"+instance.ID+"/runtime/migration", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatalf("%s permission gate got %d", method, rec.Code)
		}
	}
}

func TestRuntimeMigrationStatusFindsTaskBeyondGlobalHistoryLimit(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	previous, err := db.CreateTask(store.Task{Type: runtimeMigrationTaskType, Target: instance.ID, Status: "failed"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 201; i++ {
		if _, err := db.CreateTask(store.Task{Type: "apps.other.install", Target: "unrelated", Status: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest("GET", "/api/v2/apps/instances/"+instance.ID+"/runtime/migration", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), previous.ID) {
		t.Fatalf("lost prior migration task: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeMigrationEntryQueuesLegacyInstance(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	instance.Metadata = `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}
	started := false
	api.runtime.startExistingTask = func(task store.Task, lang string, job worker.Job) (store.Task, error) {
		started = true
		return task, nil
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/runtime/migration", strings.NewReader(`{"reason":"upgrade"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted || !started {
		t.Fatalf("expected queued migration, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestRuntimeMigrationEntryRejectsUnknownFields(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/runtime/migration", strings.NewReader(`{"shell":"touch /tmp/no"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected strict request rejection, got %d", rec.Code)
	}
}
