package store

import (
	"path/filepath"
	"strings"
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
	saveAuthoritativeMaintenanceCluster(t, db, "cluster_1234567890abcdef12345678", first, second, third)
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

func TestMySQLMaintenanceClusterRequiresAuthoritativeThreeMembers(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	second := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	third := saveMaintenanceInstance(t, db, `{"clusterId":"cluster_1234567890abcdef12345678"}`)
	if err := db.SetMySQLMaintenance([]string{first.ID, second.ID, third.ID}, validClusterMaintenanceMarker()); err == nil {
		t.Fatal("expected missing authoritative app_cluster_members to reject marker write")
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
}

func TestMySQLMaintenanceRejectsWrongFieldPrefixesAndAdvanceIdentityChanges(t *testing.T) {
	badID := `{"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"standalone","backupId":"app_1234567890abcdef12345678","taskId":"backup_1234567890abcdef12345678","restorePhase":"schema_mutation_started","recordedAt":"2026-07-28T00:00:00Z"}}`
	if _, _, err := ParseMySQLMaintenanceMarker(badID); err == nil {
		t.Fatal("expected field-specific backup/task ID rejection")
	}
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36", ServerID: NewID("srv"), Status: "running", Topology: "standalone", Metadata: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	marker := MySQLMaintenanceMarker{Version: 1, State: "required", Reason: "restore_incomplete", Scope: "standalone", BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678", RestorePhase: "schema_mutation_started", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC)}
	if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
		t.Fatal(err)
	}
	marker.RecordedAt = marker.RecordedAt.Add(time.Second)
	if err := db.AdvanceMySQLMaintenance([]string{instance.ID}, marker, "load_complete"); err == nil {
		t.Fatal("expected advance to reject changed marker timestamp")
	}
}

func TestParseMySQLMaintenanceMarkerRequiresExactV1Contract(t *testing.T) {
	validStandalone := `{"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"standalone","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"schema_mutation_started","recordedAt":"2026-07-28T00:00:00Z"}}`
	validCluster := `{"mysqlMaintenance":{"version":1,"state":"required","reason":"restore_incomplete","scope":"cluster","clusterId":"cluster_1234567890abcdef12345678","backupId":"backup_1234567890abcdef12345678","taskId":"tsk_1234567890abcdef12345678","restorePhase":"load_complete","recordedAt":"2026-07-28T00:00:00Z"}}`
	for name, raw := range map[string]string{
		"nested null":              `{"mysqlMaintenance":null}`,
		"nested array":             `{"mysqlMaintenance":[]}`,
		"unknown field":            strings.Replace(validStandalone, `"recordedAt"`, `"extra":true,"recordedAt"`, 1),
		"wrong version":            strings.Replace(validStandalone, `"version":1`, `"version":2`, 1),
		"wrong state":              strings.Replace(validStandalone, `"state":"required"`, `"state":"cleared"`, 1),
		"wrong reason":             strings.Replace(validStandalone, `"reason":"restore_incomplete"`, `"reason":"operator"`, 1),
		"wrong scope":              strings.Replace(validStandalone, `"scope":"standalone"`, `"scope":"member"`, 1),
		"standalone cluster ID":    strings.Replace(validStandalone, `"backupId"`, `"clusterId":"cluster_1234567890abcdef12345678","backupId"`, 1),
		"cluster missing ID":       strings.Replace(validCluster, `"clusterId":"cluster_1234567890abcdef12345678",`, "", 1),
		"cluster malformed ID":     strings.Replace(validCluster, `"cluster_1234567890abcdef12345678"`, `"cluster_bad"`, 1),
		"wrong backup ID":          strings.Replace(validStandalone, `"backup_1234567890abcdef12345678"`, `"app_1234567890abcdef12345678"`, 1),
		"wrong task ID":            strings.Replace(validStandalone, `"tsk_1234567890abcdef12345678"`, `"backup_1234567890abcdef12345678"`, 1),
		"wrong phase":              strings.Replace(validStandalone, `"schema_mutation_started"`, `"verified"`, 1),
		"non UTC recordedAt":       strings.Replace(validStandalone, `"2026-07-28T00:00:00Z"`, `"2026-07-28T08:00:00+08:00"`, 1),
		"missing recordedAt":       strings.Replace(validStandalone, `,"recordedAt":"2026-07-28T00:00:00Z"`, "", 1),
		"trailing nested document": `{"mysqlMaintenance":{"version":1}{"version":1}}`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, present, err := ParseMySQLMaintenanceMarker(raw); err == nil {
				t.Fatalf("invalid marker accepted: present=%v err=%v raw=%s", present, err, raw)
			}
		})
	}
	for name, raw := range map[string]string{"standalone": validStandalone, "cluster": validCluster} {
		t.Run("valid "+name, func(t *testing.T) {
			if _, present, err := ParseMySQLMaintenanceMarker(raw); err != nil || !present {
				t.Fatalf("valid marker rejected: present=%v err=%v raw=%s", present, err, raw)
			}
		})
	}
}

func TestMySQLMaintenanceStandaloneCompareAndSetRequiresExactCurrentMarker(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{
		App: "mysql", Version: "8.0.36", ServerID: NewID("srv"), Status: "running", Topology: "standalone", Metadata: `{}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	marker := MySQLMaintenanceMarker{
		Version: 1, State: "required", Reason: "restore_incomplete", Scope: "standalone",
		BackupID: "backup_1234567890abcdef12345678", TaskID: "tsk_1234567890abcdef12345678",
		RestorePhase: "schema_mutation_started", RecordedAt: time.Date(2026, 7, 28, 0, 0, 0, 0, time.UTC),
	}
	if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err != nil {
		t.Fatal(err)
	}
	if err := db.SetMySQLMaintenance([]string{instance.ID}, marker); err == nil {
		t.Fatal("duplicate marker set was not rejected")
	}
	stale := marker
	stale.RecordedAt = stale.RecordedAt.Add(time.Second)
	if err := db.AdvanceMySQLMaintenance([]string{instance.ID}, stale, "load_complete"); err == nil {
		t.Fatal("stale marker advanced current state")
	}
	if err := db.AdvanceMySQLMaintenance([]string{instance.ID}, marker, "load_complete"); err != nil {
		t.Fatal(err)
	}
	if err := db.ClearMySQLMaintenance([]string{instance.ID}, marker); err == nil {
		t.Fatal("pre-advance marker cleared load_complete state")
	}
	marker.RestorePhase = "load_complete"
	if err := db.ClearMySQLMaintenance([]string{instance.ID}, marker); err != nil {
		t.Fatal(err)
	}
	fresh, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := ParseMySQLMaintenanceMarker(fresh.Metadata); err != nil || present {
		t.Fatalf("exact clear did not remove marker: present=%v err=%v metadata=%s", present, err, fresh.Metadata)
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

func saveAuthoritativeMaintenanceCluster(t *testing.T, db *Store, clusterID string, instances ...AppInstance) {
	t.Helper()
	cluster, err := db.SaveAppCluster(AppCluster{ID: clusterID, App: "mysql", Name: "maintenance", Topology: "innodb-cluster", Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if _, err := db.SaveAppClusterMember(AppClusterMember{ClusterID: cluster.ID, InstanceID: instance.ID, ServerID: instance.ServerID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
	}
}
