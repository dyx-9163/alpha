package httpapi

import (
	"context"
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func TestRegisterInstallCredentialsIncludesNacos(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "nacos-1", Host: "192.168.74.132", Username: "root", Password: "ssh-password"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "nacos",
		Version:  "2.4.3",
		ServerID: server.ID,
		Status:   "installed",
		Topology: "standalone",
	})
	if err != nil {
		t.Fatal(err)
	}
	task, err := db.CreateTask(store.Task{Type: "test.credentials", Target: server.ID, Status: "pending", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := api.tasks.StartExistingWithLanguage(task, "zh", func(ctx context.Context, log worker.Logger) error {
		api.registerInstallCredentials(ctx, "nacos", registry.InstallRequest{
			App:        "nacos",
			Language:   "zh",
			ServerID:   server.ID,
			Parameters: map[string]any{"port": 8848},
		}, "owner", log)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	waitForTaskStatus(t, db, task.ID, "success")

	credentials, err := db.ListCredentials(store.CredentialQuery{Kind: "nacos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 {
		t.Fatalf("expected one nacos credential, got %+v", credentials)
	}
	credential := credentials[0]
	if credential.Username != "nacos" || credential.Endpoint != "http://192.168.74.132:8848/nacos" || credential.Purpose != "admin" || !credential.HasSecret {
		t.Fatalf("unexpected nacos credential: %+v", credential)
	}
	withSecret, err := db.GetCredential(credential.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if withSecret.Secret["password"] != "nacos" {
		t.Fatalf("expected default nacos password to be stored")
	}
	bindings, err := db.CredentialBindings(credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].AppInstanceID != instance.ID || bindings[0].Purpose != "admin" {
		t.Fatalf("expected credential bound to nacos instance, got %+v", bindings)
	}
}
