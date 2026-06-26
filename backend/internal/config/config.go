package config

import (
	"os"
	"path/filepath"
	"strconv"
)

type Config struct {
	Addr                  string `json:"addr"`
	StaticDir             string `json:"staticDir"`
	ResourceDir           string `json:"resourceDir"`
	DatabasePath          string `json:"databasePath"`
	BootstrapUsername     string `json:"-"`
	BootstrapPassword     string `json:"-"`
	DefaultPassword       string `json:"-"`
	JWTSecret             string `json:"-"`
	DeploymentConcurrency int    `json:"deploymentConcurrency"`
	DefaultDeployDir      string `json:"defaultDeployDir"`
	ProviderMode          string `json:"providerMode"`
}

func Load() Config {
	root, _ := os.Getwd()
	defaultPassword := getenv("AIFAR_DEFAULT_PASSWORD", "Oversea.123")
	cfg := Config{
		Addr:                  getenv("AIFAR_ADDR", "0.0.0.0:8080"),
		StaticDir:             getenv("AIFAR_STATIC_DIR", filepath.Join(root, "web", "dist")),
		ResourceDir:           getenv("AIFAR_RESOURCE_DIR", filepath.Join(root, "resources")),
		DatabasePath:          getenv("AIFAR_DATABASE_PATH", filepath.Join(root, "data", "aifar.db")),
		BootstrapUsername:     getenv("AIFAR_BOOTSTRAP_USERNAME", "admin"),
		BootstrapPassword:     getenv("AIFAR_BOOTSTRAP_PASSWORD", defaultPassword),
		DefaultPassword:       defaultPassword,
		JWTSecret:             getenv("AIFAR_JWT_SECRET", "aifar-local-development-secret-change-me"),
		DeploymentConcurrency: getenvInt("AIFAR_DEPLOYMENT_CONCURRENCY", 2),
		DefaultDeployDir:      getenv("AIFAR_DEFAULT_DEPLOY_DIR", "/aifar/apps"),
		ProviderMode:          "real",
	}
	return cfg
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
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
