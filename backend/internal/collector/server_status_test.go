package collector

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/realtime"
	"aifar-deployment/backend/internal/store"
)

func TestServerCollectorProbesWithSecretAndPublishesOnlyChanges(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	const secret = "collector-password-secret"
	server, err := db.SaveServer(store.Server{
		Name: "node-1", Host: "10.0.0.10", Port: 22,
		Username: "root", AuthType: "password", Password: secret,
		Status: "unknown",
	})
	if err != nil {
		t.Fatal(err)
	}

	events := realtime.NewHub()
	ch, unsubscribe := events.Subscribe()
	defer unsubscribe()
	manager := NewManager(db, events, time.Minute)
	manager.serverProbe = func(_ context.Context, target store.Server) error {
		if target.Password != secret {
			t.Fatalf("probe did not receive decrypted credential")
		}
		return fmt.Errorf("password=%s connection refused", target.Password)
	}

	if err := manager.collectServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetStatusSnapshot("server", server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "failed" || strings.Contains(snapshot.LastError, secret) {
		t.Fatalf("unsafe failed snapshot: %+v", snapshot)
	}
	if strings.Contains(snapshot.Payload, secret) {
		t.Fatalf("credential leaked in snapshot payload: %s", snapshot.Payload)
	}
	first := nextEvent(t, ch)
	if first.Type != "status.server.updated" || first.ResourceID != server.ID {
		t.Fatalf("unexpected server event: %+v", first)
	}
	if strings.Contains(fmt.Sprint(first.Payload), secret) {
		t.Fatalf("credential leaked in event: %+v", first)
	}

	if err := manager.collectServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case event := <-ch:
		t.Fatalf("unchanged server snapshot was republished: %+v", event)
	case <-time.After(50 * time.Millisecond):
	}
	stored, err := db.GetServer(server.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != "unknown" {
		t.Fatalf("collector mutated canonical server: %+v", stored)
	}
}

func TestServerCollectorSuccessfulProbeWritesAvailableSnapshot(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(store.Server{
		Name: "node-1", Host: "10.0.0.10", Port: 22,
		Username: "root", AuthType: "password", Password: "secret",
		Status: "failed", LastError: "old failure",
	})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(db, nil, time.Minute)
	manager.serverProbe = func(context.Context, store.Server) error { return nil }

	if err := manager.collectServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := db.GetStatusSnapshot("server", server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "available" || snapshot.LastError != "" {
		t.Fatalf("expected clean available snapshot, got %+v", snapshot)
	}
}

func TestServerCollectorBoundsConcurrencyAndTimesOutSlowProbe(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var fastServer store.Server
	for index := 0; index < 8; index++ {
		server, saveErr := db.SaveServer(store.Server{
			Name: fmt.Sprintf("fast-%d", index), Host: fmt.Sprintf("10.0.0.%d", index+1),
		})
		if saveErr != nil {
			t.Fatal(saveErr)
		}
		if index == 0 {
			fastServer = server
		}
	}
	if _, err := db.SaveServer(store.Server{Name: "slow", Host: "10.0.0.99"}); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(db, nil, time.Minute)
	manager.serverProbeWorkers = 2
	manager.serverProbeTimeout = 25 * time.Millisecond
	var mu sync.Mutex
	active := 0
	maxActive := 0
	manager.serverProbe = func(ctx context.Context, target store.Server) error {
		mu.Lock()
		active++
		if active > maxActive {
			maxActive = active
		}
		mu.Unlock()
		defer func() {
			mu.Lock()
			active--
			mu.Unlock()
		}()

		if target.Name == "slow" {
			<-ctx.Done()
			return ctx.Err()
		}
		time.Sleep(5 * time.Millisecond)
		return nil
	}

	if err := manager.collectServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	fastSnapshot, err := db.GetStatusSnapshot("server", fastServer.ID)
	if err != nil {
		t.Fatal(err)
	}
	if fastSnapshot.Status != "available" {
		t.Fatalf("expected fast server to be available, got %+v", fastSnapshot)
	}
	mu.Lock()
	observedMax := maxActive
	mu.Unlock()
	if observedMax > 2 {
		t.Fatalf("probe concurrency exceeded worker bound: %d", observedMax)
	}
}

func TestServerCollectorReturnsInfrastructureError(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	manager := NewManager(db, nil, time.Minute)
	manager.serverProbe = func(context.Context, store.Server) error { return nil }
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	if err := manager.collectServers(context.Background()); err == nil {
		t.Fatal("expected closed store to fail collection")
	}
}
