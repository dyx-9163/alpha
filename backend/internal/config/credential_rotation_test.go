package config

import "testing"

func TestLoadReadsPreviousCredentialSecret(t *testing.T) {
	t.Setenv("AIFAR_CREDENTIAL_SECRET", "current-credential-secret")
	t.Setenv("AIFAR_PREVIOUS_CREDENTIAL_SECRET", "previous-credential-secret")

	cfg := Load()
	if cfg.CredentialSecret != "current-credential-secret" {
		t.Fatalf("CredentialSecret = %q", cfg.CredentialSecret)
	}
	if !cfg.CredentialSecretConfigured {
		t.Fatal("CredentialSecretConfigured = false, want true")
	}
	if cfg.PreviousCredentialSecret != "previous-credential-secret" {
		t.Fatalf("PreviousCredentialSecret = %q", cfg.PreviousCredentialSecret)
	}
}

func TestLoadPreservesPreviousCredentialSecretBytes(t *testing.T) {
	previousSecret := "  previous-secret-with-significant-whitespace  "
	t.Setenv("AIFAR_PREVIOUS_CREDENTIAL_SECRET", previousSecret)

	cfg := Load()
	if cfg.PreviousCredentialSecret != previousSecret {
		t.Fatalf("PreviousCredentialSecret = %q, want original bytes %q", cfg.PreviousCredentialSecret, previousSecret)
	}
}
