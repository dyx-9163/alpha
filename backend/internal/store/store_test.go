package store

import (
	"path/filepath"
	"strings"
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

func TestServerSecretsAreEncryptedAtRest(t *testing.T) {
	db, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(Server{
		Name:       "node-1",
		Host:       "127.0.0.1",
		Username:   "root",
		Password:   "plain-password",
		PrivateKey: "plain-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	var rawPassword, rawPrivateKey string
	if err := db.db.QueryRow(`select password, private_key from servers where id=?`, server.ID).Scan(&rawPassword, &rawPrivateKey); err != nil {
		t.Fatal(err)
	}
	if rawPassword == "plain-password" || rawPrivateKey == "plain-key" {
		t.Fatalf("expected secrets to be encrypted at rest, got password=%q privateKey=%q", rawPassword, rawPrivateKey)
	}
	got, err := db.GetServer(server.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "plain-password" || got.PrivateKey != "plain-key" {
		t.Fatalf("expected decrypted secrets, got %+v", got)
	}
	public, err := db.GetServer(server.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if public.Password != "" || public.PrivateKey != "" {
		t.Fatalf("expected public server payload to hide secrets, got %+v", public)
	}
}

func TestServerReorderPersistsSortOrder(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.SaveServer(Server{Name: "node-1", Host: "10.0.0.1", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.SaveServer(Server{Name: "node-2", Host: "10.0.0.2", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := db.SaveServer(Server{Name: "node-3", Host: "10.0.0.3", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReorderServers([]string{second.ID, third.ID, first.ID}); err != nil {
		t.Fatal(err)
	}
	servers, err := db.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{second.ID, third.ID, first.ID}
	if len(servers) != len(want) {
		t.Fatalf("expected %d servers, got %d", len(want), len(servers))
	}
	for idx, server := range servers {
		if server.ID != want[idx] {
			t.Fatalf("expected server %d to be %s, got %s", idx, want[idx], server.ID)
		}
		if server.SortOrder != idx+1 {
			t.Fatalf("expected sort order %d, got %d", idx+1, server.SortOrder)
		}
	}
	got, err := db.GetServer(second.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.SortOrder != 1 {
		t.Fatalf("expected persisted sort order 1, got %d", got.SortOrder)
	}
}

func TestStorageSecretKeyIsEncryptedAndHidden(t *testing.T) {
	db, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	item, err := db.SaveStorageItem(StorageItem{
		InstanceID: "app-1",
		Kind:       "accessKey",
		Name:       "ops",
		AccessKey:  "ak",
		SecretKey:  "plain-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.SecretKey != "" {
		t.Fatalf("expected saved storage item response to hide secret key")
	}
	var rawSecret string
	if err := db.db.QueryRow(`select secret_key from storage_items where instance_id=? and kind=? and name=?`, "app-1", "accessKey", "ops").Scan(&rawSecret); err != nil {
		t.Fatal(err)
	}
	if rawSecret == "plain-secret" {
		t.Fatalf("expected storage secret key to be encrypted at rest")
	}
	items, err := db.ListStorageItems("app-1", "accessKey")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SecretKey != "" {
		t.Fatalf("expected listed storage item to hide secret key, got %+v", items)
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

func TestClearTaskLogsForTasks(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.CreateTask(Task{Type: "apps.docker.install", Target: "srv-1", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateTask(Task{Type: "apps.mysql.install", Target: "srv-2", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := db.CreateTask(Task{Type: "servers.probe", Target: "srv-3", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(first.ID, "info", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(second.ID, "info", "second-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(second.ID, "info", "second-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(third.ID, "info", "third"); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.ClearTaskLogsForTasks([]string{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted log rows, got %d", deleted)
	}
	for _, id := range []string{first.ID, second.ID} {
		_, logs, err := db.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) != 0 {
			t.Fatalf("expected logs for %s to be cleared, got %d", id, len(logs))
		}
	}
	_, logs, err := db.GetTask(third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected third task logs to remain, got %d", len(logs))
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

func TestTaskPersistenceMasksSensitiveText(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "test", Target: "token=target-secret", CreatedBy: "tester", Error: "password=create-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(task.ID, "error", "failed with password=log-secret"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(task.ID, "failed", "token=status-secret"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(task.ID, "password=target-secret", "failed", "secret=target-error"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(task.ID, "password=step-target", "install", "install", 1, "failed", "authorization=step-error"); err != nil {
		t.Fatal(err)
	}

	got, logs, err := db.GetTask(task.ID)
	if err != nil {
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
	combined := got.Target + got.Error + logs[0].Message + targets[0].Target + targets[0].Error + steps[0].Target + steps[0].Error
	for _, leaked := range []string{"target-secret", "create-secret", "log-secret", "status-secret", "target-error", "step-target", "step-error"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("expected %q to be masked from persisted task fields: %q", leaked, combined)
		}
	}
}

func TestAuditMasksSensitiveText(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AddAudit("admin", "servers.save", "password=target-secret", "success", `{"token":"message-secret"}`); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one audit row, got %d", len(items))
	}
	combined := items[0].Target + items[0].Message
	for _, leaked := range []string{"target-secret", "message-secret"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("expected %q to be masked from persisted audit fields: %q", leaked, combined)
		}
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
