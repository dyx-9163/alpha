package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestCredentialStoreEncryptsAndHidesSecret(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	created, err := s.SaveCredential(Credential{
		Name:      "mysql root",
		Kind:      "mysql",
		Username:  "root",
		Endpoint:  "10.0.0.10:3306",
		Scope:     "app-instance",
		Status:    "active",
		CreatedBy: "admin",
		Secret:    map[string]string{"password": "secret-value"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Secret != nil {
		t.Fatal("saved credential response must not include plaintext secret")
	}
	if !created.HasSecret || created.CurrentVersion != 1 {
		t.Fatalf("unexpected credential secret state: %+v", created)
	}

	list, err := s.ListCredentials(CredentialQuery{Kind: "mysql"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected one credential, got %d", len(list))
	}
	if list[0].Secret != nil {
		t.Fatal("credential list must not include plaintext secret")
	}

	withoutSecret, err := s.GetCredential(created.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if withoutSecret.Secret != nil {
		t.Fatal("GetCredential without includeSecret must not include plaintext secret")
	}

	withSecret, err := s.GetCredential(created.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got := withSecret.Secret["password"]; got != "secret-value" {
		t.Fatalf("decrypted password = %q, want secret-value", got)
	}
}

func TestCredentialVersionsKeepLatestThree(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	credential, err := s.SaveCredential(Credential{Name: "redis", Kind: "redis", Secret: map[string]string{"password": "v1"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"v2", "v3", "v4"} {
		credential.Secret = map[string]string{"password": password}
		credential, err = s.SaveCredential(credential)
		if err != nil {
			t.Fatal(err)
		}
	}
	if credential.CurrentVersion != 4 {
		t.Fatalf("current version = %d, want 4", credential.CurrentVersion)
	}
	count, err := s.CountRows("credential_versions")
	if err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("credential version count = %d, want 3", count)
	}
}

func TestDeleteBoundCredentialFails(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	credential, err := s.SaveCredential(Credential{Name: "minio", Kind: "minio", Secret: map[string]string{"password": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.SaveAppInstance(AppInstance{App: "minio", Version: "2025", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindCredential(CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCredential(credential.ID); err == nil {
		t.Fatal("expected deleting a bound credential to fail")
	}
}

func TestDeleteCredentialPrunesStaleBindings(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	credential, err := s.SaveCredential(Credential{
		Name:          "nacos",
		Kind:          "nacos",
		AppInstanceID: "missing_app",
		Secret:        map[string]string{"password": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindCredential(CredentialBinding{CredentialID: credential.ID, AppInstanceID: "missing_app", Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteCredential(credential.ID); err != nil {
		t.Fatalf("stale app instance binding must not block credential deletion: %v", err)
	}
	var bindingCount int
	if err := s.db.QueryRow(`select count(*) from credential_bindings where credential_id=?`, credential.ID).Scan(&bindingCount); err != nil {
		t.Fatal(err)
	}
	if bindingCount != 0 {
		t.Fatalf("stale credential bindings = %d, want 0", bindingCount)
	}
}

func TestDeleteAppInstanceRemovesCredentialReferences(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	instance, err := s.SaveAppInstance(AppInstance{App: "redis", Version: "7.2", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := s.SaveCredential(Credential{
		Name:          "redis",
		Kind:          "redis",
		AppInstanceID: instance.ID,
		Secret:        map[string]string{"password": "secret"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindCredential(CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "runtime"}); err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCredential(credential.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.AppInstanceID != "" {
		t.Fatalf("deleted instance reference = %q, want empty", got.AppInstanceID)
	}
	bindings, err := s.CredentialBindings(credential.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(bindings) != 0 {
		t.Fatalf("credential bindings after instance delete = %+v, want none", bindings)
	}
	if err := s.DeleteCredential(credential.ID); err != nil {
		t.Fatalf("credential should be deletable after bound app instance is deleted: %v", err)
	}
}

func TestCredentialListFallsBackToBindingAppInstanceID(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	credential, err := s.SaveCredential(Credential{Name: "nacos", Kind: "nacos", Secret: map[string]string{"password": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := s.SaveAppInstance(AppInstance{App: "nacos", Version: "2.4.3", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindCredential(CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}

	list, err := s.ListCredentials(CredentialQuery{Kind: "nacos"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AppInstanceID != instance.ID {
		t.Fatalf("expected app instance from credential binding, got %+v", list)
	}
	got, err := s.GetCredential(credential.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.AppInstanceID != instance.ID {
		t.Fatalf("expected get credential to expose binding app instance, got %+v", got)
	}
}

func TestGetBoundCredentialReturnsOnlyActiveRequestedBinding(t *testing.T) {
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	instance, err := s.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	admin, err := s.SaveCredential(Credential{Name: "mysql-admin", Kind: "mysql", Purpose: "runtime", Status: "active", Secret: map[string]string{"password": "admin-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := s.SaveCredential(Credential{Name: "mysql-runtime", Kind: "mysql", Status: "active", Secret: map[string]string{"password": "runtime-secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindCredential(CredentialBinding{CredentialID: admin.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindCredential(CredentialBinding{CredentialID: runtime.ID, AppInstanceID: instance.ID, Purpose: "runtime"}); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetBoundCredential(instance.ID, "admin", true)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != admin.ID || got.Purpose != "admin" || got.Secret["password"] != "admin-secret" {
		t.Fatalf("GetBoundCredential returned %+v, want active admin credential with decrypted password", got)
	}
	withoutSecret, err := s.GetBoundCredential(instance.ID, "admin", false)
	if err != nil {
		t.Fatal(err)
	}
	if withoutSecret.Secret != nil {
		t.Fatalf("GetBoundCredential without secret returned plaintext: %+v", withoutSecret)
	}
	if _, err := s.GetBoundCredential(instance.ID, "replication", true); !errors.Is(err, ErrBoundCredentialNotFound) {
		t.Fatalf("wrong-purpose error = %v, want ErrBoundCredentialNotFound", err)
	}
}

func TestGetBoundCredentialRejectsInactiveMissingSecretAndAmbiguousBindings(t *testing.T) {
	t.Run("inactive", func(t *testing.T) {
		s, instance := newBoundCredentialStore(t)
		inactive, err := s.SaveCredential(Credential{Name: "retired-admin", Kind: "mysql", Status: "retired", Secret: map[string]string{"password": "retired-secret"}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.BindCredential(CredentialBinding{CredentialID: inactive.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetBoundCredential(instance.ID, "admin", true); !errors.Is(err, ErrBoundCredentialNotFound) {
			t.Fatalf("inactive error = %v, want ErrBoundCredentialNotFound", err)
		}
	})
	t.Run("missing secret", func(t *testing.T) {
		s, instance := newBoundCredentialStore(t)
		credential, err := s.SaveCredential(Credential{Name: "empty-admin", Kind: "mysql", Status: "active"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.BindCredential(CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
			t.Fatal(err)
		}
		if _, err := s.GetBoundCredential(instance.ID, "admin", true); !errors.Is(err, ErrBoundCredentialSecretMissing) {
			t.Fatalf("missing-secret error = %v, want ErrBoundCredentialSecretMissing", err)
		}
	})
	t.Run("ambiguous", func(t *testing.T) {
		s, instance := newBoundCredentialStore(t)
		for _, name := range []string{"first-admin", "second-admin"} {
			credential, err := s.SaveCredential(Credential{Name: name, Kind: "mysql", Status: "active", Secret: map[string]string{"password": name}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.BindCredential(CredentialBinding{CredentialID: credential.ID, AppInstanceID: instance.ID, Purpose: "admin"}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.GetBoundCredential(instance.ID, "admin", true); !errors.Is(err, ErrBoundCredentialAmbiguous) {
			t.Fatalf("ambiguous error = %v, want ErrBoundCredentialAmbiguous", err)
		}
	})
}

func newBoundCredentialStore(t *testing.T) (*Store, AppInstance) {
	t.Helper()
	s, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	instance, err := s.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	return s, instance
}
