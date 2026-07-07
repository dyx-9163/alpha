package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestRecordFailedInstallInstancesCreatesCleanupInstance(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "minio-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}

	count, err := api.recordFailedInstallInstances(context.Background(), registry.InstallRequest{
		App:       "minio",
		Version:   "2026-test",
		Topology:  "standalone",
		ServerIDs: []string{server.ID},
		Parameters: map[string]any{
			"apiPort":     9010,
			"consolePort": "9011",
			"rootUser":    "admin",
		},
	}, time.Now().Add(-time.Minute), "task-failed", errors.New("remote install failed"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one failed instance, got %d", count)
	}

	instances, err := db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected one app instance, got %+v", instances)
	}
	instance := instances[0]
	if instance.App != "minio" || instance.ServerID != server.ID || instance.Status != "failed" {
		t.Fatalf("unexpected failed instance: %+v", instance)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["installFailed"] != true || metadata["taskId"] != "task-failed" || metadata["endpoint"] != "http://10.0.0.9:9010" {
		t.Fatalf("failed install metadata missing cleanup context: %+v", metadata)
	}
}

func TestRecordFailedInstallInstancesSkipsInstancesRecordedDuringTask(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "redis-1", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-time.Second)
	if _, err := db.SaveAppInstance(store.AppInstance{
		App:      "redis",
		Version:  "7.2.14",
		ServerID: server.ID,
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"port":6379}`,
	}); err != nil {
		t.Fatal(err)
	}

	count, err := api.recordFailedInstallInstances(context.Background(), registry.InstallRequest{
		App:       "redis",
		Version:   "7.2.14",
		Topology:  "standalone",
		ServerIDs: []string{server.ID},
	}, startedAt, "task-failed", errors.New("late cluster bootstrap failed"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no duplicate failed instance, got %d", count)
	}
	instances, err := db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Status != "installed" {
		t.Fatalf("expected only the recorded installed instance, got %+v", instances)
	}
}

func TestRequireExplicitInstallPasswordsRejectsDefaultFallback(t *testing.T) {
	if err := requireExplicitInstallPasswords("mysql", "en", map[string]any{"rootUser": "root"}); err == nil {
		t.Fatal("expected mysql install without password to be rejected")
	}
	if err := requireExplicitInstallPasswords("mysql", "en", map[string]any{"rootPassword": "manual"}); err != nil {
		t.Fatalf("expected mysql explicit password to pass: %v", err)
	}
	if err := requireExplicitInstallPasswords("nacos", "en", map[string]any{"nacosPassword": "manual", "dbSource": "manual"}); err == nil {
		t.Fatal("expected nacos manual database source without db password to be rejected")
	}
	if err := requireExplicitInstallPasswords("nacos", "en", map[string]any{"nacosPassword": "manual", "dbSource": "manual", "dbPassword": "db-manual"}); err != nil {
		t.Fatalf("expected nacos explicit passwords to pass: %v", err)
	}
}
