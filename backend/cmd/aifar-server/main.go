package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/alerts"
	"aifar-deployment/backend/internal/apps/aifar"
	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/collector"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/httpapi"
	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/resource"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

type credentialSecretStore interface {
	ValidateCredentialSecrets() error
	RotateCredentialSecrets() (int, error)
}

func prepareCredentialSecrets(cfg *config.Config, db credentialSecretStore) (int, bool, error) {
	if err := db.ValidateCredentialSecrets(); err != nil {
		return 0, false, fmt.Errorf("validate credential secrets: %w", err)
	}
	if cfg.PreviousCredentialSecret == "" {
		return 0, false, nil
	}

	rotated, err := db.RotateCredentialSecrets()
	if err != nil {
		return 0, false, fmt.Errorf("rotate credential secrets: %w", err)
	}
	cfg.PreviousCredentialSecret = ""
	return rotated, true, nil
}

func main() {
	cfg := config.Load()
	if err := cfg.ValidateServerSecurity(); err != nil {
		log.Fatalf("unsafe security configuration: %v; set strong values or use AIFAR_ALLOW_INSECURE_DEFAULTS=true only for local development", err)
	}
	if cfg.AllowInsecureDefaults {
		log.Printf("WARNING: insecure development credentials are explicitly allowed; do not use this mode in production")
	}

	db, err := store.OpenWithSecrets(cfg.DatabasePath, cfg.CredentialSecret, cfg.PreviousCredentialSecret)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()
	rotated, didRotate, err := prepareCredentialSecrets(&cfg, db)
	if err != nil {
		log.Fatalf("prepare credential encryption: %v", err)
	}
	if didRotate {
		log.Printf("rotated %d encrypted value(s) to AIFAR_CREDENTIAL_SECRET; remove AIFAR_PREVIOUS_CREDENTIAL_SECRET before the next start", rotated)
	}

	if err := db.BootstrapUser(cfg.BootstrapUsername, cfg.BootstrapPassword); err != nil {
		log.Fatalf("bootstrap user: %v", err)
	}

	resourceScanStartedAt := time.Now()
	if err := resource.ScanAndSave(db, cfg.ResourceDir); err != nil {
		log.Printf("resource scan warning: %v", err)
		now := time.Now()
		_ = db.UpsertCollectorRun(store.CollectorRun{Name: "resources.scan", Target: cfg.ResourceDir, Status: "failed", LastError: err.Error(), StartedAt: resourceScanStartedAt, FinishedAt: now, DurationMS: now.Sub(resourceScanStartedAt).Milliseconds(), UpdatedAt: now})
	} else {
		now := time.Now()
		_ = db.UpsertCollectorRun(store.CollectorRun{Name: "resources.scan", Target: cfg.ResourceDir, Status: "success", StartedAt: resourceScanStartedAt, FinishedAt: now, DurationMS: now.Sub(resourceScanStartedAt).Milliseconds(), UpdatedAt: now})
	}

	tasks := worker.NewManagerWithConcurrency(db, cfg.DeploymentConcurrency)
	events := realtime.NewHub()
	tasks.SetEventPublisher(events)
	if recovered, err := tasks.RecoverInterruptedTasks(""); err != nil {
		log.Printf("task recovery warning: %v", err)
	} else if len(recovered) > 0 {
		log.Printf("recovered %d interrupted task(s) from previous aifar-server process", len(recovered))
	}
	if recovered, err := aifar.RecoverInterruptedOrchestrationLocks(db); err != nil {
		log.Printf("AIFAR orchestration lock recovery warning: %v", err)
	} else if recovered > 0 {
		log.Printf("recovered %d interrupted AIFAR orchestration lock(s)", recovered)
	}
	aifar.NewAutoscaler(db, tasks, adapter.SSHRemote{}).Start(context.Background())
	alertManager := alerts.NewManager(db, events)
	collectorManager := collector.NewManager(db, events, time.Duration(cfg.CollectorIntervalSecs)*time.Second)
	collectorManager.SetAppRegistry(registry.NewFromRegistered(registry.Dependencies{Store: db, DefaultPassword: cfg.DefaultPassword}))
	collectorManager.SetAlertEvaluator(alertManager)
	collectorManager.Start(context.Background())
	api := httpapi.NewWithRealtime(cfg, db, tasks, events)

	log.Printf("AIFAR listening on %s", cfg.Addr)
	log.Printf("static=%s resources=%s database=%s", cfg.StaticDir, cfg.ResourceDir, cfg.DatabasePath)
	if err := http.ListenAndServe(cfg.Addr, api.Router()); err != nil {
		log.Fatal(err)
	}
}
