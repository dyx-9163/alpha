package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"
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

func TestMySQLOfflineUpgradeLockTakeoverRechecksDurableState(t *testing.T) {
	for _, change := range []string{"checkpoint changed", "old owner heartbeat", "incomplete lock set"} {
		t.Run(change, func(t *testing.T) {
			const clusterID = "cluster"
			file := filepath.Join(t.TempDir(), "panel.db")
			db, err := Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			old, err := db.CreateTask(Task{Type: "apps.mysql.upgrade", Target: clusterID, Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
			original, err := db.AcquireOperationLock(OperationLock{Scope: "app-cluster", ResourceID: clusterID, Operation: "mutate", OwnerTaskID: old.ID})
			if err != nil {
				t.Fatal(err)
			}
			checkpoint := MySQLOfflineUpgradeCheckpoint{TaskID: "original-task", Nodes: []MySQLOfflineUpgradeNode{
				{InstanceID: "instance1", ServerID: "server1"},
				{InstanceID: "instance2", ServerID: "server2"},
				{InstanceID: "instance3", ServerID: "server3"},
			}}
			if err := db.CreateMySQLOfflineUpgradeCheckpoint(clusterID, checkpoint); err != nil {
				t.Fatal(err)
			}
			if _, err := db.RecoverInterruptedTasks("interrupted at startup"); err != nil {
				t.Fatal(err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			db, err = Open(file)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			recovery, err := db.GetInterruptedMySQLOfflineUpgradeLock(clusterID)
			if err != nil || recovery.OwnerTaskID != old.ID || recovery.CheckpointTaskID != checkpoint.TaskID {
				t.Fatalf("restart state was not durable: %+v (%v)", recovery, err)
			}
			retry, err := db.CreateTask(Task{Type: old.Type, Target: clusterID, Status: "running"})
			if err != nil {
				t.Fatal(err)
			}
			requested := []OperationLock{{Scope: "app-cluster", ResourceID: clusterID, Operation: "mutate"}}
			for _, node := range checkpoint.Nodes {
				requested = append(requested,
					OperationLock{Scope: "app-instance", ResourceID: node.InstanceID, Operation: "mutate"},
					OperationLock{Scope: "app-target", ResourceID: "mysql:" + node.ServerID, Operation: "mutate"},
					OperationLock{Scope: "server-config", ResourceID: node.ServerID, Operation: "mutate"},
				)
			}
			switch change {
			case "checkpoint changed":
				if err := db.CompleteMySQLOfflineUpgrade(clusterID, checkpoint.TaskID, nil); err != nil {
					t.Fatal(err)
				}
				checkpoint.TaskID = "different-task"
				if err := db.CreateMySQLOfflineUpgradeCheckpoint(clusterID, checkpoint); err != nil {
					t.Fatal(err)
				}
			case "old owner heartbeat":
				if n, err := db.HeartbeatOperationLocksByTaskID(old.ID, time.Hour); err != nil || n != 1 {
					t.Fatalf("refresh old lock=%d (%v)", n, err)
				}
			case "incomplete lock set":
				requested = requested[:len(requested)-1]
			}
			if err := db.TakeOverInterruptedMySQLOfflineUpgradeLocks(recovery, retry.ID, requested); !errors.Is(err, ErrMySQLOfflineUpgradeLockChanged) {
				t.Fatalf("takeover should require unchanged guarded state: %v", err)
			}
			locks, err := db.ListOperationLocks("", "", true)
			if err != nil || len(locks) != 1 || locks[0].ID != original.ID || locks[0].OwnerTaskID != old.ID {
				t.Fatalf("failed takeover changed active locks: %+v (%v)", locks, err)
			}
		})
	}
}
