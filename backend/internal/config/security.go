package config

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"unicode/utf8"
)

const (
	minimumPasswordLength = 12
	minimumSecretLength   = 32
)

// ValidateServerSecurity rejects development credentials before the server
// opens the database or starts listening. Local tooling must opt in to the
// unsafe defaults explicitly through AIFAR_ALLOW_INSECURE_DEFAULTS.
func (c Config) ValidateServerSecurity() error {
	var problems []string
	if c.CredentialSecretConfigured && strings.TrimSpace(c.CredentialSecret) == strings.TrimSpace(c.JWTSecret) {
		problems = append(problems, "AIFAR_CREDENTIAL_SECRET must be different from AIFAR_JWT_SECRET")
	}
	if strings.TrimSpace(c.PreviousCredentialSecret) != "" && strings.TrimSpace(c.PreviousCredentialSecret) == strings.TrimSpace(c.CredentialSecret) {
		problems = append(problems, "AIFAR_PREVIOUS_CREDENTIAL_SECRET must be different from AIFAR_CREDENTIAL_SECRET")
	}
	if c.AllowInsecureDefaults {
		if !isLoopbackListenerAddress(c.Addr) {
			problems = append(problems, "AIFAR_ALLOW_INSECURE_DEFAULTS=true requires AIFAR_ADDR to use exactly 127.0.0.1, localhost, or ::1")
		}
		return securityProblems(problems)
	}

	if insecurePassword(c.DefaultPassword) {
		problems = append(problems, fmt.Sprintf("AIFAR_DEFAULT_PASSWORD must be at least %d characters and must not use the built-in default", minimumPasswordLength))
	}
	if insecurePassword(c.BootstrapPassword) {
		problems = append(problems, fmt.Sprintf("AIFAR_BOOTSTRAP_PASSWORD must be at least %d characters and must not use the built-in default", minimumPasswordLength))
	}
	if insecureSecret(c.JWTSecret) {
		problems = append(problems, fmt.Sprintf("AIFAR_JWT_SECRET must be at least %d characters and must not be a placeholder", minimumSecretLength))
	}
	if !c.CredentialSecretConfigured {
		problems = append(problems, "AIFAR_CREDENTIAL_SECRET must be set explicitly and must not reuse AIFAR_JWT_SECRET")
	} else if insecureSecret(c.CredentialSecret) {
		problems = append(problems, fmt.Sprintf("AIFAR_CREDENTIAL_SECRET must be at least %d characters and must not be a placeholder", minimumSecretLength))
	}
	return securityProblems(problems)
}

func securityProblems(problems []string) error {
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func isLoopbackListenerAddress(addr string) bool {
	host, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		return false
	}
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}

func insecurePassword(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return utf8.RuneCountInString(strings.TrimSpace(value)) < minimumPasswordLength || normalized == "oversea.123" || isPlaceholder(normalized)
}

func insecureSecret(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	return utf8.RuneCountInString(strings.TrimSpace(value)) < minimumSecretLength || isPlaceholder(normalized)
}

func isPlaceholder(value string) bool {
	if value == "" {
		return true
	}
	for _, marker := range []string{"change-me", "changeme", "replace-me", "development-secret", "example-secret"} {
		if strings.Contains(value, marker) {
			return true
		}
	}
	return false
}
