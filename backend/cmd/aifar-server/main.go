package main

import (
	"context"
	"log"
	"net/http"

	"aifar-deployment/backend/internal/adapter"
	"aifar-deployment/backend/internal/apps/aifar"
	"aifar-deployment/backend/internal/config"
	"aifar-deployment/backend/internal/httpapi"
	"aifar-deployment/backend/internal/resource"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func main() {
	cfg := config.Load()

	db, err := store.OpenWithSecret(cfg.DatabasePath, cfg.CredentialSecret)
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer db.Close()

	if err := db.BootstrapUser(cfg.BootstrapUsername, cfg.BootstrapPassword); err != nil {
		log.Fatalf("bootstrap user: %v", err)
	}

	if err := resource.ScanAndSave(db, cfg.ResourceDir); err != nil {
		log.Printf("resource scan warning: %v", err)
	}

	tasks := worker.NewManagerWithConcurrency(db, cfg.DeploymentConcurrency)
	aifar.NewAutoscaler(db, tasks, adapter.SSHRemote{}).Start(context.Background())
	api := httpapi.New(cfg, db, tasks)

	log.Printf("AIFAR listening on %s", cfg.Addr)
	log.Printf("static=%s resources=%s database=%s", cfg.StaticDir, cfg.ResourceDir, cfg.DatabasePath)
	if err := http.ListenAndServe(cfg.Addr, api.Router()); err != nil {
		log.Fatal(err)
	}
}
