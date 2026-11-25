package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

var ErrPanelProcessAlreadyRunning = errors.New("another aifar-server process owns this database")

type PanelProcessLock struct {
	databasePath string
	db           *sql.DB
	conn         *sql.Conn
}

// AcquirePanelProcessLock excludes other Panel processes before task recovery.
// The sidecar remains on disk so all processes contend on the same SQLite file.
func AcquirePanelProcessLock(databasePath string) (*PanelProcessLock, error) {
	if databasePath == ":memory:" {
		return &PanelProcessLock{databasePath: databasePath}, nil
	}
	canonical, err := canonicalPanelDatabasePath(databasePath)
	if err != nil {
		return nil, err
	}
	filename := canonical + ".panel.lock"
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		if err := file.Close(); err != nil {
			return nil, err
		}
	} else if errors.Is(err, os.ErrExist) {
		info, err := os.Lstat(filename)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("Panel process lock is not a regular file: %s", filename)
		}
	} else {
		return nil, err
	}

	slashPath := filepath.ToSlash(filename)
	if filepath.VolumeName(filename) != "" && !strings.HasPrefix(slashPath, "/") {
		slashPath = "/" + slashPath
	}
	uri := url.URL{Scheme: "file", Path: slashPath, RawQuery: "mode=rw&_pragma=busy_timeout(0)"}
	db, err := sql.Open("sqlite", uri.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err == nil {
		if _, err = conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err == nil {
			return &PanelProcessLock{databasePath: canonical, db: db, conn: conn}, nil
		}
		_ = conn.Close()
	}
	_ = db.Close()
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&0xff == sqlite3.SQLITE_BUSY {
		return nil, fmt.Errorf("%w: %s", ErrPanelProcessAlreadyRunning, canonical)
	}
	return nil, err
}

func (lock *PanelProcessLock) DatabasePath() string {
	return lock.databasePath
}

func (lock *PanelProcessLock) Close() error {
	if lock.conn == nil {
		return nil
	}
	_, err := lock.conn.ExecContext(context.Background(), "ROLLBACK")
	return errors.Join(err, lock.conn.Close(), lock.db.Close())
}

func canonicalPanelDatabasePath(filename string) (string, error) {
	if filename == "" {
		return "", errors.New("Panel database path is required")
	}
	absolute, err := filepath.Abs(filename)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err == nil || !errors.Is(err, os.ErrNotExist) {
		return canonical, err
	}
	if _, statErr := os.Lstat(absolute); !errors.Is(statErr, os.ErrNotExist) {
		// A dangling symlink must not give the database a second lock name.
		return "", err
	}
	canonicalParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(canonicalParent, filepath.Base(absolute)), nil
}
