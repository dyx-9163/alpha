package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMySQLOfflineUpgradeCheckpointIsDurableAndInsertOnly(t *testing.T) {
	file := filepath.Join(t.TempDir(), "panel.db")
	db, err := Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.GetMySQLOfflineUpgradeCheckpoint("cluster"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected no checkpoint, got %v", err)
	}
	checkpoint := MySQLOfflineUpgradeCheckpoint{
		TaskID: "original-task", BundleSHA256: "pinned-digest",
		Nodes: []MySQLOfflineUpgradeNode{{InstanceID: "instance", ServerID: "server", Root: "/aifar/apps/mysql",
			Endpoint: "mysql:3306", Group: "group-uuid", UUID: "member-uuid", GTID: "group-uuid:1-20", SSHPort: 2222}},
	}
	if err := db.CreateMySQLOfflineUpgradeCheckpoint("cluster", checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMySQLOfflineUpgradeCheckpoint("cluster", MySQLOfflineUpgradeCheckpoint{TaskID: "retry"}); err == nil {
		t.Fatal("a retry overwrote the original checkpoint")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	actual, err := db.GetMySQLOfflineUpgradeCheckpoint("cluster")
	if err != nil || !reflect.DeepEqual(actual, checkpoint) {
		t.Fatalf("checkpoint after reopening = %+v, %v", actual, err)
	}
}

func TestCompleteMySQLOfflineUpgradeCommitsVersionsAndClearsCheckpointTogether(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	member, err := db.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateMySQLOfflineUpgradeCheckpoint("cluster", MySQLOfflineUpgradeCheckpoint{TaskID: "original-task"}); err != nil {
		t.Fatal(err)
	}
	update := AppInstanceVersionUpdate{Expected: member, Version: "8.4.13"}
	if err := db.CompleteMySQLOfflineUpgrade("cluster", "another-task", []AppInstanceVersionUpdate{update}); err == nil {
		t.Fatal("a different task completed the upgrade")
	}
	stale := AppInstanceVersionUpdate{Expected: AppInstance{ID: "missing"}, Version: "8.4.13"}
	if err := db.CompleteMySQLOfflineUpgrade("cluster", "original-task", []AppInstanceVersionUpdate{update, stale}); !errors.Is(err, ErrAppInstanceConflict) {
		t.Fatalf("expected member conflict, got %v", err)
	}
	actual, err := db.GetAppInstance(member.ID)
	if err != nil || actual.Version != "8.0.36" {
		t.Fatalf("version changed after unsuccessful completion: %+v, %v", actual, err)
	}
	if _, err := db.GetMySQLOfflineUpgradeCheckpoint("cluster"); err != nil {
		t.Fatalf("checkpoint removed after unsuccessful completion: %v", err)
	}
	if err := db.CompleteMySQLOfflineUpgrade("cluster", "original-task", []AppInstanceVersionUpdate{update}); err != nil {
		t.Fatal(err)
	}
	actual, err = db.GetAppInstance(member.ID)
	if err != nil || actual.Version != "8.4.13" {
		t.Fatalf("version after completion = %+v, %v", actual, err)
	}
	if _, err := db.GetMySQLOfflineUpgradeCheckpoint("cluster"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("checkpoint remains after completion: %v", err)
	}
}
