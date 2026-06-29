package worker

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"aifar-deployment/backend/internal/store"
)

func TestManagerUsesDeploymentConcurrencySetting(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.SetSetting("deploymentConcurrency", "1"); err != nil {
		t.Fatal(err)
	}

	manager := NewManagerWithConcurrency(db, 2)
	firstStarted := make(chan struct{})
	firstRelease := make(chan struct{})
	secondStarted := make(chan struct{})

	if _, err := manager.Start("test.first", "srv-1", "tester", func(ctx context.Context, log Logger) error {
		close(firstStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-firstRelease:
			return nil
		}
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-firstStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("first task did not start")
	}

	if _, err := manager.Start("test.second", "srv-2", "tester", func(ctx context.Context, log Logger) error {
		close(secondStarted)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
		t.Fatal("second task started before concurrency setting allowed it")
	case <-time.After(250 * time.Millisecond):
	}

	if err := db.SetSetting("deploymentConcurrency", "2"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-secondStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second task did not start after concurrency setting increased")
	}
	close(firstRelease)
}
