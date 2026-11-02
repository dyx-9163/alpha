package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMigrationCLIUsesAuthenticatedTaskAPI(t *testing.T) {
	t.Setenv("AIFAR_API_TOKEN", "lab-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer lab-token" || r.URL.Path != "/api/v2/apps/instances/lab/runtime/migration" {
			t.Errorf("unexpected request %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		w.Write([]byte(`{"taskId":"task-lab","status":"pending"}`))
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := runRuntimeMigrationCLI([]string{"--server", server.URL, "--instance", "lab"}, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out.Bytes(), []byte("task-lab")) {
		t.Fatal(out.String())
	}
}

func TestMigrationCLIRequiresToken(t *testing.T) {
	t.Setenv("AIFAR_API_TOKEN", "")
	var out bytes.Buffer
	if err := runRuntimeMigrationCLI([]string{"--instance", "lab"}, &out); err == nil {
		t.Fatal("expected missing token rejection")
	}
}

func TestMigrationCLIWaitReportsTaskFailure(t *testing.T) {
	t.Setenv("AIFAR_API_TOKEN", "lab-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			w.WriteHeader(202)
			w.Write([]byte(`{"taskId":"task-lab","status":"pending"}`))
			return
		}
		if r.URL.Path != "/api/v2/tasks/task-lab" {
			t.Errorf("unexpected polling path: %s", r.URL.Path)
		}
		w.Write([]byte(`{"task":{"status":"failed","error":"injected failure"}}`))
	}))
	defer server.Close()
	var out bytes.Buffer
	if err := runRuntimeMigrationCLI([]string{"--server", server.URL, "--instance", "lab", "--wait"}, &out); err == nil {
		t.Fatal("failed task reported success")
	}
}

func TestMigrationCLIRefusesCredentialRedirect(t *testing.T) {
	t.Setenv("AIFAR_API_TOKEN", "lab-token")
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached = true }))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer origin.Close()
	var out bytes.Buffer
	if err := runRuntimeMigrationCLI([]string{"--server", origin.URL, "--instance", "lab"}, &out); err == nil || reached {
		t.Fatal("redirect followed with credentials")
	}
}
