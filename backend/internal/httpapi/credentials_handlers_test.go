package httpapi

import (
	"testing"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestInstallManualPasswordCreatesGeneratedCredential(t *testing.T) {
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

	api.bindInstallCredentialReferences("nacos", registry.InstallRequest{
		App:        "nacos",
		Language:   "zh",
		Actor:      "owner",
		ServerID:   server.ID,
		Parameters: map[string]any{"port": 8848, "nacosUser": "nacos", "nacosPassword": "manual-password"},
	}, nil)

	credentials, err := db.ListCredentials(store.CredentialQuery{Kind: "nacos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 {
		t.Fatalf("manual install password should create one credential, got %+v", credentials)
	}
	credential := credentials[0]
	if credential.Username != "nacos" || credential.Endpoint != "http://192.168.74.132:8848/nacos" || credential.AppInstanceID != instance.ID || credential.Purpose != "nacos" {
		t.Fatalf("unexpected generated credential: %+v", credential)
	}
	withSecret, err := db.GetCredential(credential.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if withSecret.Secret["password"] != "manual-password" {
		t.Fatalf("generated credential did not preserve the manual install secret")
	}
	bindings, err := db.CredentialBindings(credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 1 || bindings[0].AppInstanceID != instance.ID || bindings[0].Purpose != "nacos" {
		t.Fatalf("expected generated credential bound to nacos instance, got %+v", bindings)
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
	}, nil)

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

func TestGeneratedInstallCredentialSpecForSupportedApps(t *testing.T) {
	cases := []struct {
		app       string
		params    map[string]any
		kind      string
		username  string
		secretKey string
		purpose   string
	}{
		{
			app:       "mysql",
			params:    map[string]any{"rootUser": "root", "rootPassword": "mysql-secret"},
			kind:      "mysql",
			username:  "root",
			secretKey: "password",
			purpose:   "admin",
		},
		{
			app:       "redis",
			params:    map[string]any{"password": "redis-secret"},
			kind:      "redis",
			username:  "default",
			secretKey: "password",
			purpose:   "redis",
		},
		{
			app:       "minio",
			params:    map[string]any{"rootUser": "minioadmin", "rootPassword": "minio-secret"},
			kind:      "minio",
			username:  "minioadmin",
			secretKey: "secretKey",
			purpose:   "minio",
		},
		{
			app:       "nacos",
			params:    map[string]any{"nacosUser": "nacos", "nacosPassword": "nacos-secret"},
			kind:      "nacos",
			username:  "nacos",
			secretKey: "password",
			purpose:   "nacos",
		},
	}
	for _, tt := range cases {
		spec, ok := generatedInstallCredentialSpecFor(tt.app, tt.params)
		if !ok {
			t.Fatalf("%s should produce a generated credential spec", tt.app)
		}
		if spec.Kind != tt.kind || spec.Username != tt.username || spec.SecretKey != tt.secretKey || spec.Purpose != tt.purpose {
			t.Fatalf("%s generated spec mismatch: %+v", tt.app, spec)
		}
	}
	if _, ok := generatedInstallCredentialSpecFor("nacos", map[string]any{"nacosCredentialId": "cred-existing", "nacosPassword": "manual"}); ok {
		t.Fatalf("selected existing credential should not be copied into a generated credential")
	}
}
