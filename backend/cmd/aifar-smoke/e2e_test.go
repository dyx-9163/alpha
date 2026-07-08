package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestValidateE2EConfigRequiresGuardAndInputs(t *testing.T) {
	problems := validateE2EConfig(e2eConfig{})
	for _, want := range []string{
		"AIFAR_E2E_ALLOW_MUTATION=1",
		"AIFAR_E2E_SERVER_IDS",
		"AIFAR_E2E_USERNAME",
		"AIFAR_E2E_PASSWORD",
		"AIFAR_E2E_APP_PASSWORD",
	} {
		if !containsProblem(problems, want) {
			t.Fatalf("expected validation problem containing %q, got %#v", want, problems)
		}
	}

	valid := e2eConfig{
		AllowMutation: true,
		ServerIDs:     []string{"srv-1"},
		PrimaryServer: "srv-1",
		Username:      "owner",
		Password:      "login-password",
		AppPassword:   "app-password",
		TaskTimeout:   time.Minute,
	}
	if got := validateE2EConfig(valid); len(got) != 0 {
		t.Fatalf("expected valid config, got %#v", got)
	}

	valid.DockerCleanup = "everything"
	if got := validateE2EConfig(valid); !containsProblem(got, "AIFAR_E2E_DOCKER_CLEANUP") {
		t.Fatalf("expected docker cleanup validation problem, got %#v", got)
	}
}

func TestE2EInstallSpecsBuildExpectedRequests(t *testing.T) {
	cfg := e2eConfig{
		ServerIDs:     []string{"srv-1", "srv-2"},
		PrimaryServer: "srv-1",
		AppPassword:   "app-password",
	}
	specs := e2eBaseInstallSpecs(cfg)
	var apps []string
	for _, spec := range specs {
		apps = append(apps, spec.App)
	}
	if want := []string{"docker", "mysql", "redis", "minio", "nacos"}; !reflect.DeepEqual(apps, want) {
		t.Fatalf("unexpected install order: got %#v want %#v", apps, want)
	}
	if got, ok := specs[0].Request["serverIds"].([]string); !ok || !reflect.DeepEqual(got, cfg.ServerIDs) {
		t.Fatalf("unexpected docker serverIds: %#v", specs[0].Request["serverIds"])
	}
	assertRequestString(t, specs[1].Request, "rootPassword", cfg.AppPassword)
	assertRequestString(t, specs[2].Request, "password", cfg.AppPassword)
	assertRequestString(t, specs[3].Request, "rootUser", "minioadmin")
	assertRequestString(t, specs[3].Request, "rootPassword", cfg.AppPassword)
	assertRequestString(t, specs[4].Request, "dbSource", "local")
	assertRequestString(t, specs[4].Request, "nacosUser", "nacos")
	assertRequestString(t, specs[4].Request, "nacosPassword", cfg.AppPassword)

	aifar := e2eAIFARInstallSpec(cfg, "nacos-1")
	if aifar.App != "aifar" || !reflect.DeepEqual(aifar.Targets, []string{"srv-1"}) {
		t.Fatalf("unexpected AIFAR spec: %#v", aifar)
	}
	assertRequestString(t, aifar.Request, "version", "runtime-v2")
	assertRequestString(t, aifar.Request, "nacosSource", "existing")
	assertRequestString(t, aifar.Request, "nacosInstanceId", "nacos-1")
	assertRequestString(t, aifar.Request, "nacosPassword", cfg.AppPassword)
}

func TestE2EInstanceConflicts(t *testing.T) {
	instances := []store.AppInstance{
		{ID: "docker-2", App: "docker", ServerID: "srv-2"},
		{ID: "mysql-1", App: "mysql", ServerID: "srv-1"},
		{ID: "redis-other", App: "redis", ServerID: "srv-9"},
	}
	got := e2eInstanceConflicts(instances, []string{"srv-1", "srv-2"}, "srv-1")
	want := []string{"docker:docker-2@srv-2", "mysql:mysql-1@srv-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected conflicts: got %#v want %#v", got, want)
	}
}

func TestCreatedInstanceIDs(t *testing.T) {
	before := []store.AppInstance{
		{ID: "old", App: "mysql", ServerID: "srv-1"},
	}
	after := []store.AppInstance{
		{ID: "old", App: "mysql", ServerID: "srv-1"},
		{ID: "new-2", App: "mysql", ServerID: "srv-2"},
		{ID: "new-1", App: "mysql", ServerID: "srv-1"},
		{ID: "redis-1", App: "redis", ServerID: "srv-1"},
	}
	got := createdInstanceIDs(before, after, "mysql", []string{"srv-1", "srv-2"})
	want := []string{"new-1", "new-2"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected created ids: got %#v want %#v", got, want)
	}
}

func TestCleanupInstanceIDsReverseAndDockerDefault(t *testing.T) {
	rep := &report{Stages: []e2eStageReport{{App: "docker", Action: "install", InstanceIDs: []string{"docker-1"}}}}
	runner := &e2eRunner{
		cfg:    e2eConfig{ServerIDs: []string{"srv-1"}, PrimaryServer: "srv-1"},
		report: rep,
		created: map[string]store.AppInstance{
			"docker-1": {ID: "docker-1", App: "docker", ServerID: "srv-1"},
			"mysql-1":  {ID: "mysql-1", App: "mysql", ServerID: "srv-1"},
			"redis-1":  {ID: "redis-1", App: "redis", ServerID: "srv-1"},
			"minio-1":  {ID: "minio-1", App: "minio", ServerID: "srv-1"},
			"nacos-1":  {ID: "nacos-1", App: "nacos", ServerID: "srv-1"},
			"aifar-1":  {ID: "aifar-1", App: "aifar", ServerID: "srv-1"},
		},
		createdOrder: []string{"docker-1", "mysql-1", "redis-1", "minio-1", "nacos-1", "aifar-1"},
	}

	got, err := runner.cleanupInstanceIDs(context.Background())
	if err != nil {
		t.Fatalf("cleanup ids failed: %v", err)
	}
	want := []string{"aifar-1", "nacos-1", "minio-1", "redis-1", "mysql-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected default cleanup order: got %#v want %#v", got, want)
	}
	if rep.Stages[0].CleanupStatus != "kept-docker" {
		t.Fatalf("expected docker install stage to be marked kept, got %q", rep.Stages[0].CleanupStatus)
	}

	runner.cfg.DockerCleanup = "created"
	got, err = runner.cleanupInstanceIDs(context.Background())
	if err != nil {
		t.Fatalf("cleanup ids failed: %v", err)
	}
	want = []string{"aifar-1", "nacos-1", "minio-1", "redis-1", "mysql-1", "docker-1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected docker cleanup order: got %#v want %#v", got, want)
	}
}

func TestWaitTaskBranches(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		var calls int
		runner, closeServer := newTaskPollingRunner(t, func(w http.ResponseWriter, r *http.Request) {
			calls++
			status := "running"
			if calls > 1 {
				status = "success"
			}
			writeJSONForTest(t, w, taskDetailResponse{Task: store.Task{ID: "task-1", Status: status}})
		})
		defer closeServer()

		task, err := runner.waitTask(context.Background(), "task-1")
		if err != nil {
			t.Fatalf("waitTask failed: %v", err)
		}
		if task.Status != "success" {
			t.Fatalf("unexpected task status: %s", task.Status)
		}
	})

	t.Run("failed", func(t *testing.T) {
		runner, closeServer := newTaskPollingRunner(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSONForTest(t, w, taskDetailResponse{Task: store.Task{ID: "task-2", Status: "failed", Error: "boom"}})
		})
		defer closeServer()

		_, err := runner.waitTask(context.Background(), "task-2")
		if err == nil || !strings.Contains(err.Error(), "status=failed") {
			t.Fatalf("expected failed task error, got %v", err)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		runner, closeServer := newTaskPollingRunner(t, func(w http.ResponseWriter, r *http.Request) {
			writeJSONForTest(t, w, taskDetailResponse{Task: store.Task{ID: "task-3", Status: "running"}})
		})
		runner.cfg.TaskTimeout = 20 * time.Millisecond
		defer closeServer()

		_, err := runner.waitTask(context.Background(), "task-3")
		if err == nil || !strings.Contains(err.Error(), "timed out") {
			t.Fatalf("expected timeout error, got %v", err)
		}
	})
}

func TestInstallAndCheckFakeHTTP(t *testing.T) {
	var listCalls int
	rep := &report{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/apps/instances":
			listCalls++
			if listCalls == 1 {
				writeJSONForTest(t, w, []store.AppInstance{})
				return
			}
			writeJSONForTest(t, w, []store.AppInstance{{ID: "mysql-1", App: "mysql", ServerID: "srv-1"}})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/apps/mysql/install":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode install body: %v", err)
			}
			if body["rootPassword"] != "app-password" {
				t.Fatalf("unexpected rootPassword in install request: %#v", body["rootPassword"])
			}
			writeJSONForTest(t, w, taskStartResponse{TaskID: "install-1", Status: "running"})
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/apps/instances/mysql-1/check":
			writeJSONForTest(t, w, taskStartResponse{TaskID: "check-1", Status: "running"})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/tasks/install-1":
			writeJSONForTest(t, w, taskDetailResponse{Task: store.Task{ID: "install-1", Status: "success"}})
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/tasks/check-1":
			writeJSONForTest(t, w, taskDetailResponse{Task: store.Task{ID: "check-1", Status: "success"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	runner := &e2eRunner{
		cfg:     e2eConfig{TaskTimeout: time.Second, PollInterval: time.Millisecond},
		report:  rep,
		client:  &e2eHTTPClient{baseURL: server.URL, client: server.Client(), checks: &rep.APIChecks},
		created: map[string]store.AppInstance{},
	}
	ids, err := runner.installAndCheck(context.Background(), e2eInstallSpec{
		App:     "mysql",
		Targets: []string{"srv-1"},
		Request: map[string]any{"rootPassword": "app-password"},
		Check:   true,
	})
	if err != nil {
		t.Fatalf("installAndCheck failed: %v", err)
	}
	if !reflect.DeepEqual(ids, []string{"mysql-1"}) {
		t.Fatalf("unexpected created ids: %#v", ids)
	}
	if len(runner.createdOrder) != 1 || runner.createdOrder[0] != "mysql-1" {
		t.Fatalf("created order not recorded: %#v", runner.createdOrder)
	}
	if len(rep.Stages) != 2 || rep.Stages[0].Status != "success" || rep.Stages[1].Status != "success" {
		t.Fatalf("unexpected stages: %#v", rep.Stages)
	}
	for _, check := range rep.APIChecks {
		if strings.Contains(check.Message, "app-password") {
			t.Fatalf("API check leaked request password: %#v", check)
		}
	}
}

func newTaskPollingRunner(t *testing.T, handler http.HandlerFunc) (*e2eRunner, func()) {
	t.Helper()
	rep := &report{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasPrefix(r.URL.Path, "/api/v2/tasks/") {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	runner := &e2eRunner{
		cfg:    e2eConfig{TaskTimeout: time.Second, PollInterval: time.Millisecond},
		report: rep,
		client: &e2eHTTPClient{baseURL: server.URL, client: server.Client(), checks: &rep.APIChecks},
	}
	return runner, server.Close
}

func assertRequestString(t *testing.T, request map[string]any, key, want string) {
	t.Helper()
	got, ok := request[key].(string)
	if !ok || got != want {
		t.Fatalf("request[%s] = %#v, want %q", key, request[key], want)
	}
}

func containsProblem(problems []string, want string) bool {
	for _, problem := range problems {
		if strings.Contains(problem, want) {
			return true
		}
	}
	return false
}

func writeJSONForTest(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("write json: %v", err)
	}
}
