package config

import (
	"strings"
	"testing"
)

func secureTestConfig() Config {
	return Config{
		DefaultPassword:            "panel-password-2026",
		BootstrapPassword:          "bootstrap-password-2026",
		JWTSecret:                  "jwt-secret-with-at-least-thirty-two-characters",
		CredentialSecret:           "credential-secret-with-at-least-thirty-two-characters",
		CredentialSecretConfigured: true,
	}
}

func TestValidateServerSecurityAcceptsStrongConfiguration(t *testing.T) {
	cfg := secureTestConfig()
	if err := cfg.ValidateServerSecurity(); err != nil {
		t.Fatalf("ValidateServerSecurity() error = %v", err)
	}
}

func TestValidateServerSecurityRejectsBuiltInDefaultsAndPlaceholders(t *testing.T) {
	cfg := secureTestConfig()
	cfg.DefaultPassword = "Oversea.123"
	cfg.BootstrapPassword = "Oversea.123"
	cfg.JWTSecret = "aifar-local-development-secret-change-me"
	cfg.CredentialSecret = "change-me-before-production"

	err := cfg.ValidateServerSecurity()
	if err == nil {
		t.Fatal("ValidateServerSecurity() error = nil, want unsafe configuration error")
	}
	for _, key := range []string{"AIFAR_DEFAULT_PASSWORD", "AIFAR_BOOTSTRAP_PASSWORD", "AIFAR_JWT_SECRET", "AIFAR_CREDENTIAL_SECRET"} {
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("ValidateServerSecurity() error = %q, want %s", err, key)
		}
	}
}

func TestValidateServerSecurityAllowsExplicitDevelopmentOverrideOnLoopback(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8080", "localhost:8080", "[::1]:8080"} {
		t.Run(addr, func(t *testing.T) {
			cfg := Config{Addr: addr, AllowInsecureDefaults: true}
			if err := cfg.ValidateServerSecurity(); err != nil {
				t.Fatalf("ValidateServerSecurity() with loopback override error = %v", err)
			}
		})
	}
}

func TestValidateServerSecurityRejectsDevelopmentOverrideOutsideLoopback(t *testing.T) {
	for _, addr := range []string{"", "*", ":8080", "*:8080", "0.0.0.0", "0.0.0.0:8080", "::", "[::]:8080", "app.internal:8080", "127.0.0.1:8080]", "[::1]:8080]"} {
		t.Run(addr, func(t *testing.T) {
			cfg := Config{Addr: addr, AllowInsecureDefaults: true}
			err := cfg.ValidateServerSecurity()
			if err == nil || !strings.Contains(err.Error(), "AIFAR_ALLOW_INSECURE_DEFAULTS") {
				t.Fatalf("ValidateServerSecurity() error = %v, want non-loopback override error", err)
			}
		})
	}
}

func TestValidateServerSecurityRejectsExplicitFalseWithWeakDefaults(t *testing.T) {
	t.Setenv("AIFAR_ADDR", "127.0.0.1:8080")
	t.Setenv("AIFAR_ALLOW_INSECURE_DEFAULTS", "false")
	t.Setenv("AIFAR_DEFAULT_PASSWORD", "Oversea.123")
	t.Setenv("AIFAR_BOOTSTRAP_PASSWORD", "Oversea.123")
	t.Setenv("AIFAR_JWT_SECRET", "aifar-local-development-secret-change-me")
	t.Setenv("AIFAR_CREDENTIAL_SECRET", "")

	cfg := Load()
	if cfg.AllowInsecureDefaults {
		t.Fatal("Load().AllowInsecureDefaults = true, want explicit false preserved")
	}
	if err := cfg.ValidateServerSecurity(); err == nil {
		t.Fatal("ValidateServerSecurity() error = nil, want weak defaults rejected")
	}
}

func TestValidateServerSecurityRejectsEqualRotationSecretsUnderOverride(t *testing.T) {
	cfg := Config{
		Addr:                     "127.0.0.1:8080",
		CredentialSecret:         "same-secret",
		PreviousCredentialSecret: "same-secret",
		AllowInsecureDefaults:    true,
	}
	err := cfg.ValidateServerSecurity()
	if err == nil || !strings.Contains(err.Error(), "AIFAR_PREVIOUS_CREDENTIAL_SECRET") {
		t.Fatalf("ValidateServerSecurity() error = %v, want equal rotation secret error", err)
	}
}

func TestValidateServerSecurityRejectsEqualWhitespaceRotationSecretsUnderOverride(t *testing.T) {
	cfg := Config{
		Addr:                     "127.0.0.1:8080",
		CredentialSecret:         "   ",
		PreviousCredentialSecret: "   ",
		AllowInsecureDefaults:    true,
	}
	err := cfg.ValidateServerSecurity()
	if err == nil || !strings.Contains(err.Error(), "AIFAR_PREVIOUS_CREDENTIAL_SECRET") {
		t.Fatalf("ValidateServerSecurity() error = %v, want equal raw rotation secret error", err)
	}
}

func TestValidateServerSecurityRejectsRotationSecretsThatDeriveTheSameKey(t *testing.T) {
	cfg := Config{
		Addr:                     "127.0.0.1:8080",
		CredentialSecret:         "current-secret",
		PreviousCredentialSecret: "  current-secret  ",
		AllowInsecureDefaults:    true,
	}
	err := cfg.ValidateServerSecurity()
	if err == nil || !strings.Contains(err.Error(), "AIFAR_PREVIOUS_CREDENTIAL_SECRET") {
		t.Fatalf("ValidateServerSecurity() error = %v, want derived-key equivalence error", err)
	}
}

func TestValidateServerSecurityRequiresExplicitDistinctCredentialSecret(t *testing.T) {
	cfg := secureTestConfig()
	cfg.CredentialSecretConfigured = false
	cfg.CredentialSecret = cfg.JWTSecret
	if err := cfg.ValidateServerSecurity(); err == nil || !strings.Contains(err.Error(), "must be set explicitly") {
		t.Fatalf("ValidateServerSecurity() error = %v, want explicit credential secret error", err)
	}

	cfg.CredentialSecretConfigured = true
	if err := cfg.ValidateServerSecurity(); err == nil || !strings.Contains(err.Error(), "must be different") {
		t.Fatalf("ValidateServerSecurity() error = %v, want distinct credential secret error", err)
	}
}

func TestValidateServerSecurityAllowsWeakPreviousSecretForOneTimeRotation(t *testing.T) {
	cfg := secureTestConfig()
	cfg.PreviousCredentialSecret = "change-me-before-production"
	if err := cfg.ValidateServerSecurity(); err != nil {
		t.Fatalf("ValidateServerSecurity() rotation error = %v", err)
	}
}

func TestValidateServerSecurityCountsUnicodeCharactersConsistently(t *testing.T) {
	cfg := secureTestConfig()
	cfg.DefaultPassword = strings.Repeat("密", minimumPasswordLength)
	cfg.BootstrapPassword = strings.Repeat("码", minimumPasswordLength)
	cfg.JWTSecret = strings.Repeat("甲", minimumSecretLength)
	cfg.CredentialSecret = strings.Repeat("乙", minimumSecretLength)
	if err := cfg.ValidateServerSecurity(); err != nil {
		t.Fatalf("ValidateServerSecurity() Unicode error = %v", err)
	}

	cfg.DefaultPassword = strings.Repeat("密", minimumPasswordLength-1)
	if err := cfg.ValidateServerSecurity(); err == nil || !strings.Contains(err.Error(), "AIFAR_DEFAULT_PASSWORD") {
		t.Fatalf("ValidateServerSecurity() error = %v, want short Unicode password error", err)
	}
}

func TestLoadReadsInsecureDefaultsOverride(t *testing.T) {
	t.Setenv("AIFAR_ALLOW_INSECURE_DEFAULTS", "true")
	if cfg := Load(); !cfg.AllowInsecureDefaults {
		t.Fatal("Load().AllowInsecureDefaults = false, want true")
	}
}

func TestLoadTracksExplicitCredentialSecret(t *testing.T) {
	t.Setenv("AIFAR_CREDENTIAL_SECRET", "credential-secret-with-at-least-thirty-two-characters")
	cfg := Load()
	if !cfg.CredentialSecretConfigured {
		t.Fatal("Load().CredentialSecretConfigured = false, want true")
	}
}

func TestLoadPreservesCredentialSecretBytes(t *testing.T) {
	secret := "  credential-secret-with-significant-whitespace  "
	t.Setenv("AIFAR_CREDENTIAL_SECRET", secret)

	cfg := Load()
	if !cfg.CredentialSecretConfigured {
		t.Fatal("Load().CredentialSecretConfigured = false, want true")
	}
	if cfg.CredentialSecret != secret {
		t.Fatalf("Load().CredentialSecret = %q, want original bytes %q", cfg.CredentialSecret, secret)
	}
}

func TestLoadTreatsWhitespaceCredentialSecretAsConfiguredButInvalid(t *testing.T) {
	secret := "   "
	t.Setenv("AIFAR_CREDENTIAL_SECRET", secret)

	cfg := Load()
	if !cfg.CredentialSecretConfigured {
		t.Fatal("Load().CredentialSecretConfigured = false, want explicit whitespace value tracked")
	}
	if cfg.CredentialSecret != secret {
		t.Fatalf("Load().CredentialSecret = %q, want original bytes %q", cfg.CredentialSecret, secret)
	}
	if err := cfg.ValidateServerSecurity(); err == nil || !strings.Contains(err.Error(), "AIFAR_CREDENTIAL_SECRET") {
		t.Fatalf("ValidateServerSecurity() error = %v, want whitespace credential secret rejected", err)
	}
}
