package httpapi

import (
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestInstallManualPasswordDoesNotCreateCredential(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "nacos-1", Host: "192.168.74.132", Username: "root", Password: "ssh-password"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppInstance(store.AppInstance{
		App:      "nacos",
		Version:  "2.4.3",
		ServerID: server.ID,
		Status:   "installed",
		Topology: "standalone",
	}); err != nil {
		t.Fatal(err)
	}

	api.bindInstallCredentialReferences("nacos", registry.InstallRequest{
		App:        "nacos",
		Language:   "zh",
		ServerID:   server.ID,
		Parameters: map[string]any{"port": 8848, "nacosUser": "nacos", "nacosPassword": "manual-password"},
	})

	credentials, err := db.ListCredentials(store.CredentialQuery{Kind: "nacos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 0 {
		t.Fatalf("manual install password must not be cached as credential, got %+v", credentials)
	}
}

func TestInstallSelectedCredentialIsOnlyBoundToInstance(t *testing.T) {
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
	credential, err := db.SaveCredential(store.Credential{
		Name:      "nacos-admin",
		Kind:      "nacos",
		Username:  "nacos",
		Endpoint:  "http://192.168.74.132:8848/nacos",
		Scope:     "app-instance",
		Status:    "active",
		App:       "nacos",
		ServerID:  server.ID,
		Purpose:   "admin",
		Secret:    map[string]string{"password": "stored-by-user"},
		CreatedBy: "owner",
	})
	if err != nil {
		t.Fatal(err)
	}

	api.bindInstallCredentialReferences("nacos", registry.InstallRequest{
		App:        "nacos",
		Language:   "zh",
		ServerID:   server.ID,
		Parameters: map[string]any{"nacosCredentialId": credential.ID, "nacosPassword": "manual-password"},
	})

	credentials, err := db.ListCredentials(store.CredentialQuery{Kind: "nacos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].ID != credential.ID {
		t.Fatalf("install must not create or copy credentials, got %+v", credentials)
	}
	bindings, err := db.CredentialBindings(credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].AppInstanceID != instance.ID || bindings[0].Purpose != "nacos" {
		t.Fatalf("expected selected credential bound to nacos instance, got %+v", bindings)
	}
	withSecret, err := db.GetCredential(credential.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if withSecret.Secret["password"] != "stored-by-user" {
		t.Fatalf("selected credential secret must remain unchanged")
	}
}
