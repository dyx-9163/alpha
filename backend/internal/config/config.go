package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Addr                       string `json:"addr"`
	StaticDir                  string `json:"staticDir"`
	ResourceDir                string `json:"resourceDir"`
	DatabasePath               string `json:"databasePath"`
	DatabaseBackupDir          string `json:"databaseBackupDir"`
	BootstrapUsername          string `json:"-"`
	BootstrapPassword          string `json:"-"`
	DefaultPassword            string `json:"-"`
	JWTSecret                  string `json:"-"`
	CredentialSecret           string `json:"-"`
	CredentialSecretConfigured bool   `json:"-"`
	PreviousCredentialSecret   string `json:"-"`
	AllowInsecureDefaults      bool   `json:"-"`
	DeploymentConcurrency      int    `json:"deploymentConcurrency"`
	DefaultDeployDir           string `json:"defaultDeployDir"`
	InstallerTemplateDir       string `json:"installerTemplateDir"`
	ProviderMode               string `json:"providerMode"`
	AuthMaxFailures            int    `json:"authMaxFailures"`
	AuthLockoutSeconds         int    `json:"authLockoutSeconds"`
	MaxRequestBodyBytes        int64  `json:"maxRequestBodyBytes"`
	AuditRetentionDays         int    `json:"auditRetentionDays"`
	TaskRetentionDays          int    `json:"taskRetentionDays"`
	CollectorIntervalSecs      int    `json:"collectorIntervalSeconds"`
}

func Load() Config {
	root, _ := os.Getwd()
	defaultPassword := getenv("AIFAR_DEFAULT_PASSWORD", "Oversea.123")
	jwtSecret := getenv("AIFAR_JWT_SECRET", "aifar-local-development-secret-change-me")
	credentialSecret, credentialSecretConfigured := getenvOptional("AIFAR_CREDENTIAL_SECRET")
	if !credentialSecretConfigured {
		// Preserve the historical development fallback. Production validation
		// requires AIFAR_CREDENTIAL_SECRET to be set explicitly.
		credentialSecret = jwtSecret
	}
	databasePath := getenv("AIFAR_DATABASE_PATH", filepath.Join(root, "data", "aifar.db"))
	cfg := Config{
		Addr:                       getenv("AIFAR_ADDR", "0.0.0.0:8080"),
		StaticDir:                  getenv("AIFAR_STATIC_DIR", filepath.Join(root, "web", "dist")),
		ResourceDir:                getenv("AIFAR_RESOURCE_DIR", filepath.Join(root, "resources")),
		DatabasePath:               databasePath,
		DatabaseBackupDir:          getenv("AIFAR_DATABASE_BACKUP_DIR", filepath.Join(filepath.Dir(databasePath), "backups")),
		BootstrapUsername:          getenv("AIFAR_BOOTSTRAP_USERNAME", "admin"),
		BootstrapPassword:          getenv("AIFAR_BOOTSTRAP_PASSWORD", defaultPassword),
		DefaultPassword:            defaultPassword,
		JWTSecret:                  jwtSecret,
		CredentialSecret:           credentialSecret,
		CredentialSecretConfigured: credentialSecretConfigured,
		PreviousCredentialSecret:   getenv("AIFAR_PREVIOUS_CREDENTIAL_SECRET", ""),
		AllowInsecureDefaults:      getenvBool("AIFAR_ALLOW_INSECURE_DEFAULTS", false),
		DeploymentConcurrency:      getenvInt("AIFAR_DEPLOYMENT_CONCURRENCY", 2),
		DefaultDeployDir:           getenv("AIFAR_DEFAULT_DEPLOY_DIR", "/aifar/apps"),
		InstallerTemplateDir:       getenv("AIFAR_INSTALLER_TEMPLATE_DIR", filepath.Join("config", "installers")),
		ProviderMode:               "real",
		AuthMaxFailures:            getenvInt("AIFAR_AUTH_MAX_FAILURES", 5),
		AuthLockoutSeconds:         getenvInt("AIFAR_AUTH_LOCKOUT_SECONDS", 300),
		MaxRequestBodyBytes:        getenvInt64("AIFAR_MAX_REQUEST_BODY_BYTES", 4<<30),
		AuditRetentionDays:         getenvInt("AIFAR_AUDIT_RETENTION_DAYS", 180),
		TaskRetentionDays:          getenvInt("AIFAR_TASK_RETENTION_DAYS", 90),
		CollectorIntervalSecs:      getenvInt("AIFAR_COLLECTOR_INTERVAL_SECONDS", 15),
	}
	return cfg
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvOptional(key string) (string, bool) {
	value, ok := os.LookupEnv(key)
	return value, ok && value != ""
}

func getenvInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func getenvInt64(key string, fallback int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 1 {
		return fallback
	}
	return parsed
}

func getenvBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}
