package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"aifar-deployment/backend/internal/config"
)

type recordingCredentialSecretStore struct {
	calls       []string
	validateErr error
	rotateErr   error
	rotated     int
}

func (s *recordingCredentialSecretStore) ValidateCredentialSecrets() error {
	s.calls = append(s.calls, "validate")
	return s.validateErr
}

func (s *recordingCredentialSecretStore) RotateCredentialSecrets() (int, error) {
	s.calls = append(s.calls, "rotate")
	return s.rotated, s.rotateErr
}

func TestPrepareCredentialSecretsWithoutPreviousOnlyValidates(t *testing.T) {
	cfg := config.Config{CredentialSecret: "current-secret-material"}
	db := &recordingCredentialSecretStore{rotated: 7}

	rotated, didRotate, err := prepareCredentialSecrets(&cfg, db)
	if err != nil {
		t.Fatalf("prepareCredentialSecrets() error = %v", err)
	}
	if rotated != 0 || didRotate {
		t.Fatalf("prepareCredentialSecrets() = (%d, %t), want (0, false)", rotated, didRotate)
	}
	if !reflect.DeepEqual(db.calls, []string{"validate"}) {
		t.Fatalf("calls = %v, want validation only", db.calls)
	}
}

func TestPrepareCredentialSecretsValidatesThenRotatesPreviousSecret(t *testing.T) {
	cfg := config.Config{
		CredentialSecret:         "current-secret-material",
		PreviousCredentialSecret: "previous-secret-material",
	}
	db := &recordingCredentialSecretStore{rotated: 7}

	rotated, didRotate, err := prepareCredentialSecrets(&cfg, db)
	if err != nil {
		t.Fatalf("prepareCredentialSecrets() error = %v", err)
	}
	if rotated != 7 || !didRotate {
		t.Fatalf("prepareCredentialSecrets() = (%d, %t), want (7, true)", rotated, didRotate)
	}
	if !reflect.DeepEqual(db.calls, []string{"validate", "rotate"}) {
		t.Fatalf("calls = %v, want validation before rotation", db.calls)
	}
	if cfg.PreviousCredentialSecret != "" {
		t.Fatalf("PreviousCredentialSecret = %q, want cleared after successful rotation", cfg.PreviousCredentialSecret)
	}
}

func TestPrepareCredentialSecretsTreatsWhitespacePreviousAsConfigured(t *testing.T) {
	cfg := config.Config{
		CredentialSecret:         "current-secret-material",
		PreviousCredentialSecret: "   ",
	}
	db := &recordingCredentialSecretStore{rotated: 2}

	rotated, didRotate, err := prepareCredentialSecrets(&cfg, db)
	if err != nil {
		t.Fatalf("prepareCredentialSecrets() error = %v", err)
	}
	if rotated != 2 || !didRotate {
		t.Fatalf("prepareCredentialSecrets() = (%d, %t), want (2, true)", rotated, didRotate)
	}
	if !reflect.DeepEqual(db.calls, []string{"validate", "rotate"}) {
		t.Fatalf("calls = %v, want validation before rotation", db.calls)
	}
}

func TestPrepareCredentialSecretsStopsWhenValidationFails(t *testing.T) {
	cfg := config.Config{
		CredentialSecret:         "current-secret-material",
		PreviousCredentialSecret: "previous-secret-material",
	}
	db := &recordingCredentialSecretStore{validateErr: errors.New("encrypted value cannot be decrypted")}

	_, _, err := prepareCredentialSecrets(&cfg, db)
	if err == nil || !strings.Contains(err.Error(), "validate credential secrets") {
		t.Fatalf("prepareCredentialSecrets() error = %v, want validation context", err)
	}
	if strings.Contains(err.Error(), cfg.CredentialSecret) || strings.Contains(err.Error(), cfg.PreviousCredentialSecret) {
		t.Fatalf("prepareCredentialSecrets() error exposes configured secret: %q", err)
	}
	if !reflect.DeepEqual(db.calls, []string{"validate"}) {
		t.Fatalf("calls = %v, want rotation skipped after validation failure", db.calls)
	}
	if cfg.PreviousCredentialSecret == "" {
		t.Fatal("PreviousCredentialSecret cleared after failed validation")
	}
}

func TestPrepareCredentialSecretsPropagatesRotationFailure(t *testing.T) {
	cfg := config.Config{
		CredentialSecret:         "current-secret-material",
		PreviousCredentialSecret: "previous-secret-material",
	}
	db := &recordingCredentialSecretStore{rotateErr: errors.New("transaction failed")}

	_, _, err := prepareCredentialSecrets(&cfg, db)
	if err == nil || !strings.Contains(err.Error(), "rotate credential secrets") {
		t.Fatalf("prepareCredentialSecrets() error = %v, want rotation context", err)
	}
	if strings.Contains(err.Error(), cfg.CredentialSecret) || strings.Contains(err.Error(), cfg.PreviousCredentialSecret) {
		t.Fatalf("prepareCredentialSecrets() error exposes configured secret: %q", err)
	}
	if !reflect.DeepEqual(db.calls, []string{"validate", "rotate"}) {
		t.Fatalf("calls = %v, want validation before rotation", db.calls)
	}
	if cfg.PreviousCredentialSecret == "" {
		t.Fatal("PreviousCredentialSecret cleared after failed rotation")
	}
}
