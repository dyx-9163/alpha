package collector

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
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
	var credentialMu sync.Mutex
	observedPassword := ""
	manager.serverProbe = func(_ context.Context, target store.Server) error {
		credentialMu.Lock()
		observedPassword = target.Password
		credentialMu.Unlock()
		return fmt.Errorf("password=%s connection refused", target.Password)
	}

	if err := manager.collectServers(context.Background()); err != nil {
		t.Fatal(err)
	}
	credentialMu.Lock()
	passwordSeenByProbe := observedPassword
	credentialMu.Unlock()
	if passwordSeenByProbe != secret {
		t.Fatal("probe did not receive decrypted credential")
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

func TestServerCollectorReturnsGetServerDatabaseError(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "aifar.db")
	db, err := store.Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(store.Server{Name: "node-1", Host: "10.0.0.10"})
	if err != nil {
		t.Fatal(err)
	}

	mutator, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mutator.Exec(`update servers set password=null where id=?`, server.ID); err != nil {
		_ = mutator.Close()
		t.Fatal(err)
	}
	if err := mutator.Close(); err != nil {
		t.Fatal(err)
	}

	manager := NewManager(db, nil, time.Minute)
	var unexpectedProbe atomic.Bool
	manager.serverProbe = func(context.Context, store.Server) error {
		unexpectedProbe.Store(true)
		return fmt.Errorf("unexpected probe for unscannable server row")
	}
	if err := manager.collectServers(context.Background()); err == nil {
		t.Fatal("expected post-list GetServer database error to fail collection")
	}
	if unexpectedProbe.Load() {
		t.Fatal("probe ran when the server row could not be scanned")
	}
	if _, err := db.GetStatusSnapshot("server", server.ID); !store.IsNotFound(err) {
		t.Fatalf("database error was persisted as an observation: %v", err)
	}
}

func TestServerCollectorTreatsCredentialDecryptionAsFailedObservation(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "aifar.db")
	seed, err := store.OpenWithSecret(databasePath, "correct-credential-key")
	if err != nil {
		t.Fatal(err)
	}
	server, err := seed.SaveServer(store.Server{
		Name: "node-1", Host: "10.0.0.10", Username: "root",
		AuthType: "password", Password: "collector-password-secret",
	})
	if err != nil {
		_ = seed.Close()
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}

	db, err := store.OpenWithSecret(databasePath, "wrong-credential-key")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	manager := NewManager(db, nil, time.Minute)
	var unexpectedProbe atomic.Bool
	manager.serverProbe = func(context.Context, store.Server) error {
		unexpectedProbe.Store(true)
		return fmt.Errorf("unexpected probe for undecryptable credentials")
	}
	if err := manager.collectServers(context.Background()); err != nil {
		t.Fatalf("credential decryption should remain a per-server observation: %v", err)
	}
	if unexpectedProbe.Load() {
		t.Fatal("probe ran when credentials could not be decrypted")
	}
	snapshot, err := db.GetStatusSnapshot("server", server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != "failed" || snapshot.LastError == "" {
		t.Fatalf("expected failed credential-decryption snapshot, got %+v", snapshot)
	}
	if strings.Contains(snapshot.LastError, "collector-password-secret") {
		t.Fatalf("credential leaked in failed snapshot: %+v", snapshot)
	}
}
