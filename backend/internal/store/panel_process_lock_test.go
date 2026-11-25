package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPanelProcessLockKeepsMemoryDatabasesIndependent(t *testing.T) {
	for i := 0; i < 2; i++ {
		lock, err := AcquirePanelProcessLock(":memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer lock.Close()
		if lock.DatabasePath() != ":memory:" {
			t.Fatalf("database path = %q, want :memory:", lock.DatabasePath())
		}
	}
}

func TestPanelProcessLockExcludesSecondPanelAndAllowsRestart(t *testing.T) {
	database := filepath.Join(t.TempDir(), "new", "panel #%.db")
	first, err := AcquirePanelProcessLock(database)
	if err != nil {
		t.Fatal(err)
	}
	firstClosed := false
	t.Cleanup(func() {
		if !firstClosed {
			_ = first.Close()
		}
	})
	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("sidecar lock touched the application database: %v", err)
	}
	start := time.Now()
	if second, err := AcquirePanelProcessLock(database); !errors.Is(err, ErrPanelProcessAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("a second Panel should fail to obtain the lock: %v", err)
	}
	if elapsed := time.Since(start); elapsed >= 2*time.Second {
		t.Fatalf("competing lock acquisition should fail promptly, took %v", elapsed)
	}
	other, err := AcquirePanelProcessLock(filepath.Join(filepath.Dir(database), "other.db"))
	if err != nil {
		t.Fatalf("independent database should acquire its own lock: %v", err)
	}
	defer other.Close()
	// App database clients use their ordinary SQLite connection while the
	// Panel retains its sidecar lock.
	app, err := Open(first.DatabasePath())
	if err != nil {
		t.Fatalf("open application database while holding lock: %v", err)
	}
	if err := app.Close(); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	firstClosed = true
	restarted, err := AcquirePanelProcessLock(database)
	if err != nil {
		t.Fatalf("restart should acquire the released lock: %v", err)
	}
	defer restarted.Close()
}

func TestPanelProcessLockResolvesFileAndDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	database := filepath.Join(root, "physical", "panel.db")
	if err := os.Mkdir(filepath.Dir(database), 0o755); err != nil {
		t.Fatal(err)
	}
	aliasDir := filepath.Join(root, "alias")
	if err := os.Symlink(filepath.Dir(database), aliasDir); err != nil {
		t.Skipf("directory symlinks unavailable: %v", err)
	}
	first, err := AcquirePanelProcessLock(filepath.Join(aliasDir, filepath.Base(database)))
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if second, err := AcquirePanelProcessLock(database); !errors.Is(err, ErrPanelProcessAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("directory alias should use the canonical lock: %v", err)
	}
	file, err := os.Create(database)
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	aliasFile := filepath.Join(root, "alias.db")
	if err := os.Symlink(database, aliasFile); err != nil {
		t.Skipf("file symlinks unavailable: %v", err)
	}
	if second, err := AcquirePanelProcessLock(aliasFile); !errors.Is(err, ErrPanelProcessAlreadyRunning) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("file alias should use the canonical lock: %v", err)
	}
}
