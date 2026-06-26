package store

import (
	"path/filepath"
	"testing"
)

func TestBootstrapUserAndServerLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.BootstrapUser("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.UserByUsername("admin"); err != nil {
		t.Fatal(err)
	}
	server, err := db.SaveServer(Server{Name: "node-1", Host: "127.0.0.1", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Port != 22 {
		t.Fatalf("expected default port 22, got %d", server.Port)
	}
	if server.DeployDir != "/aifar/apps" {
		t.Fatalf("expected default deploy dir /aifar/apps, got %s", server.DeployDir)
	}
	servers, err := db.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected one server, got %d", len(servers))
	}
}

func TestTaskLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "test", Target: "local", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(task.ID, "info", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskTargetLog(task.ID, "srv-1", "info", "target hello"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(task.ID, "success", ""); err != nil {
		t.Fatal(err)
	}
	got, logs, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || len(logs) != 2 {
		t.Fatalf("unexpected task result: %+v logs=%d", got, len(logs))
	}
	if logs[1].Target != "srv-1" {
		t.Fatalf("expected target log to retain server id, got %+v", logs[1])
	}
	if err := db.ClearTaskLogs(task.ID); err != nil {
		t.Fatal(err)
	}
	_, logs, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected logs to be cleared, got %d", len(logs))
	}
	if err := db.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetTask(task.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted task to be missing, got %v", err)
	}
}

func TestTaskTargetsAndSteps(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "apps.docker.install", Target: "srv-1", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(task.ID, "srv-1", "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(task.ID, "srv-1", "load-server", "load target server", 1, "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(task.ID, "srv-1", "load-server", "", 0, "success", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(task.ID, "srv-1", "success", ""); err != nil {
		t.Fatal(err)
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Status != "success" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
	if len(steps) != 1 || steps[0].Status != "success" || steps[0].Title != "load target server" {
		t.Fatalf("unexpected steps: %+v", steps)
	}
}

func TestAppInstanceLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "docker", Version: "24.0.9", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.App != "docker" || got.ServerID != "srv-1" {
		t.Fatalf("unexpected app instance: %+v", got)
	}
	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAppInstance(instance.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted app instance to be missing, got %v", err)
	}
}
