package store

import (
	"path/filepath"
	"testing"
	"time"
)

func TestMySQLMaintenanceMarkerLifecycleIsAtomic(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	second := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	third := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	marker := MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: "cluster", ClusterID: "cluster_1234567890abcdef12345678", BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678", RestorePhase: "schema_mutation_started", RecordedAt: time.Now().UTC()}
	if err := db.SetMySQLMaintenance([]string{first.ID, second.ID, third.ID}, marker); err != nil {
		t.Fatalf("set: %v", err)
	}
	if err := db.AdvanceMySQLMaintenance([]string{first.ID, second.ID, third.ID}, marker, "load_complete"); err != nil {
		t.Fatalf("advance: %v", err)
	}
	marker.RestorePhase = "load_complete"
	if err := db.ClearMySQLMaintenance([]string{first.ID, second.ID, third.ID}, marker); err != nil {
		t.Fatalf("clear: %v", err)
	}
	for _, id := range []string{first.ID, second.ID, third.ID} {
		instance, getErr := db.GetAppInstance(id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if _, present, parseErr := ParseMySQLMaintenanceMarker(instance.Metadata); parseErr != nil || present {
			t.Fatalf("marker after clear: present=%v err=%v", present, parseErr)
		}
	}
}

func TestMySQLMaintenanceRejectsMalformedAndDivergentMetadataWithoutPartialWrite(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	second := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_other1234567890abcdef123"}`)
	third := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	marker := validClusterMaintenanceMarker()
	if err := db.SetMySQLMaintenance([]string{first.ID, second.ID, third.ID}, marker); err == nil {
		t.Fatal("expected divergent topology rejection")
	}
	for _, id := range []string{first.ID, second.ID, third.ID} {
		instance, getErr := db.GetAppInstance(id)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if _, present, _ := ParseMySQLMaintenanceMarker(instance.Metadata); present {
			t.Fatalf("partial marker persisted on %s", id)
		}
	}
	if _, _, err := ParseMySQLMaintenanceMarker(`{"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"standalone","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"schema_mutation_started","recordedAt":"2026-07-28T00:00:00Z","extra":true}}`); err == nil {
		t.Fatal("expected unknown marker field rejection")
	}
}

func saveMaintenanceInstance(t *testing.T, db *Store, metadata string) AppInstance {
	t.Helper()
	instance, err := db.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36", ServerID: NewID("srv"), Status: "running", Topology: "innodb-cluster", Metadata: metadata})
	if err != nil {
		t.Fatal(err)
	}
	return instance
}

func validClusterMaintenanceMarker() MySQLMaintenanceMarker {
	return MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: "cluster", ClusterID: "cluster_1234567890abcdef12345678", BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678", RestorePhase: "schema_mutation_started", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)}
}
