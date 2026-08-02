package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestVersionedMigrationsAreRecorded(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	count, err := db.CountRows("schema_migrations")
	if err != nil {
		t.Fatal(err)
	}
	if count != len(storeMigrations) {
		t.Fatalf("schema migration count=%d, want %d", count, len(storeMigrations))
	}
}

func TestTaskLeaseAndTraceLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "apps.aifar.upgrade", Target: "app-1", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetTaskTrace(task.ID, "idem-1", "corr-1"); err != nil {
		t.Fatal(err)
	}
	acquired, err := db.AcquireTaskLease(task.ID, "worker-1", time.Minute)
	if err != nil || !acquired {
		t.Fatalf("expected worker-1 lease, acquired=%v err=%v", acquired, err)
	}
	acquired, err = db.AcquireTaskLease(task.ID, "worker-2", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if acquired {
		t.Fatal("expected worker-2 to be blocked by active lease")
	}
	got, _, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LeaseOwner != "worker-1" || got.Attempt != 1 || got.IdempotencyKey != "idem-1" || got.CorrelationID != "corr-1" || got.LeaseExpiresAt.IsZero() {
		t.Fatalf("unexpected leased task: %+v", got)
	}
	if renewed, err := db.RenewTaskLease(task.ID, "worker-1", time.Minute); err != nil || !renewed {
		t.Fatalf("expected renew, renewed=%v err=%v", renewed, err)
	}
	if released, err := db.ReleaseTaskLease(task.ID, "worker-1"); err != nil || !released {
		t.Fatalf("expected release, released=%v err=%v", released, err)
	}
	if acquired, err := db.AcquireTaskLease(task.ID, "worker-2", time.Minute); err != nil || !acquired {
		t.Fatalf("expected worker-2 lease after release, acquired=%v err=%v", acquired, err)
	}
	if err := db.UpdateTaskStatus(task.ID, "success", ""); err != nil {
		t.Fatal(err)
	}
	got, _, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LeaseOwner != "" || !got.LeaseExpiresAt.IsZero() {
		t.Fatalf("expected terminal task to clear lease, got %+v", got)
	}
}

func TestOperationLockLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	lock, err := db.AcquireOperationLock(OperationLock{
		Scope:       "app-instance",
		ResourceID:  "app-1",
		Operation:   "upgrade",
		OwnerTaskID: "tsk-1",
		Owner:       "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireOperationLock(OperationLock{Scope: "app-instance", ResourceID: "app-1", Operation: "upgrade"}); err == nil {
		t.Fatal("expected lock conflict")
	} else {
		var conflict OperationLockConflict
		if !errors.As(err, &conflict) || conflict.Lock.ID != lock.ID {
			t.Fatalf("expected operation lock conflict, got %T %v", err, err)
		}
	}
	if _, err := db.AcquireOperationLock(OperationLock{Scope: "app-instance", ResourceID: "app-1", Operation: "backup"}); err != nil {
		t.Fatalf("different operation should be allowed: %v", err)
	}
	if got, err := db.HeartbeatOperationLock(lock.ID, time.Minute); err != nil || got.ExpiresAt.IsZero() {
		t.Fatalf("expected heartbeat, got %+v err=%v", got, err)
	}
	if released, err := db.ReleaseOperationLock(lock.ID); err != nil || !released {
		t.Fatalf("expected release, released=%v err=%v", released, err)
	}
	if _, err := db.AcquireOperationLock(OperationLock{Scope: "app-instance", ResourceID: "app-1", Operation: "upgrade"}); err != nil {
		t.Fatalf("expected lock after release: %v", err)
	}
}

func TestCredentialReferencesBlockDeleteAndCleanupWithInstance(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "redis", Version: "7.2.14", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.SaveCredential(Credential{Name: "redis-admin", Kind: "redis", Secret: map[string]string{"password": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveCredentialReference(CredentialReference{
		CredentialID:    credential.ID,
		ResourceType:    "app_instance",
		ResourceID:      instance.ID,
		Purpose:         "admin",
		Generated:       true,
		LifecyclePolicy: "delete-with-resource",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteCredential(credential.ID); err == nil || !strings.Contains(err.Error(), "referenced") {
		t.Fatalf("expected referenced credential delete to fail, got %v", err)
	}
	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	refs, err := db.ListCredentialReferences(credential.ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Fatalf("expected app instance credential references to be cleaned, got %+v", refs)
	}
	if err := db.DeleteCredential(credential.ID); err != nil {
		t.Fatalf("expected credential to be deletable after cleanup: %v", err)
	}
}

func TestAppClusterReleaseAssetsAndBackups(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	cluster, err := db.SaveAppCluster(AppCluster{App: "aifar", Name: "runtime", Topology: "single"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppClusterMember(AppClusterMember{ClusterID: cluster.ID, InstanceID: instance.ID, ServerID: "srv-1", Role: "leader"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppRelease(AppRelease{InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: "rel-1", Status: "success"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppReleaseArtifact(AppReleaseArtifact{InstanceID: instance.ID, ReleaseID: "rel-1", App: "aifar", ServiceName: "gateway", ArtifactType: "image", Name: "aifar-gateway", Version: "rel-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppReleaseSnapshot(AppReleaseSnapshot{InstanceID: instance.ID, ReleaseID: "rel-1", App: "aifar", SnapshotKind: "manifest", PayloadJSON: `{"releaseId":"rel-1"}`}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppBackup(AppBackup{App: "aifar", InstanceID: instance.ID, BackupType: "config", Status: "success", Path: "/tmp/aifar-config.tar.gz"}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := db.ListAppReleaseArtifacts(instance.ID, "rel-1")
	if err != nil || len(artifacts) != 1 || artifacts[0].ServiceName != "gateway" {
		t.Fatalf("unexpected artifacts: %+v err=%v", artifacts, err)
	}
	snapshots, err := db.ListAppReleaseSnapshots(instance.ID, "rel-1")
	if err != nil || len(snapshots) != 1 || snapshots[0].SnapshotKind != "manifest" {
		t.Fatalf("unexpected snapshots: %+v err=%v", snapshots, err)
	}
	backups, err := db.ListAppBackups(instance.ID)
	if err != nil || len(backups) != 1 || backups[0].Path == "" {
		t.Fatalf("unexpected backups: %+v err=%v", backups, err)
	}
	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	members, err := db.ListAppClusterMembers(cluster.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 0 {
		t.Fatalf("expected cluster member cleanup, got %+v", members)
	}
	artifacts, err = db.ListAppReleaseArtifacts(instance.ID, "rel-1")
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("expected release artifact cleanup, got %+v err=%v", artifacts, err)
	}
	backups, err = db.ListAppBackups(instance.ID)
	if err != nil || len(backups) != 1 {
		t.Fatalf("expected backup record to be retained for audit, got %+v err=%v", backups, err)
	}
}

// Production break caught: deleting app_cluster_members without pruning the
// now-empty parent leaves a stale cluster card after the final node is gone.
func TestDeleteAppInstancePrunesClusterOnlyAfterLastMember(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	cluster, err := db.SaveAppCluster(AppCluster{App: "mysql", Name: "aifarCluster", Topology: "innodb-cluster"})
	if err != nil {
		t.Fatal(err)
	}
	instances := make([]AppInstance, 0, 2)
	for _, serverID := range []string{"srv-1", "srv-2"} {
		instance, err := db.SaveAppInstance(AppInstance{App: "mysql", Version: "8.0.36", ServerID: serverID, Status: "installed", Topology: "innodb-cluster"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(AppClusterMember{ClusterID: cluster.ID, InstanceID: instance.ID, ServerID: serverID}); err != nil {
			t.Fatal(err)
		}
		instances = append(instances, instance)
	}
	if err := db.DeleteAppInstance(instances[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAppCluster(cluster.ID); err != nil {
		t.Fatalf("cluster with a remaining member was pruned: %v", err)
	}
	if err := db.DeleteAppInstance(instances[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAppCluster(cluster.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("empty cluster still exists or returned unexpected error: %v", err)
	}
}

func TestAppBackupLookupAndListForInstances(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	first, err := db.SaveAppBackup(AppBackup{ID: "backup-first", App: "mysql", InstanceID: "instance-a", BackupType: "logical-full", Status: "success", Path: "/backups/first", Checksum: "first", CreatedAt: time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.SaveAppBackup(AppBackup{ID: "backup-second", App: "mysql", InstanceID: "instance-b", BackupType: "logical-full", Status: "success", Path: "/backups/second", Checksum: "second", CreatedAt: time.Date(2026, 7, 27, 2, 0, 0, 0, time.UTC)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppBackup(AppBackup{ID: "backup-deleted", App: "mysql", InstanceID: "instance-c", BackupType: "logical-full", Status: "deleted", Path: "/backups/deleted", Checksum: "deleted", CreatedAt: time.Date(2026, 7, 27, 3, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetAppBackup(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.Path != "/backups/first" || got.Checksum != "first" {
		t.Fatalf("GetAppBackup returned %+v, want exact first backup", got)
	}

	listed, err := db.ListAppBackupsForInstances([]string{" instance-a ", "instance-b", "instance-a", ""}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].ID != second.ID || listed[1].ID != first.ID {
		t.Fatalf("active cluster backups = %+v, want second then first", listed)
	}
	excludedDeleted, err := db.ListAppBackupsForInstances([]string{"instance-c"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(excludedDeleted) != 0 {
		t.Fatalf("deleted backups without includeDeleted = %+v, want none", excludedDeleted)
	}
	withDeleted, err := db.ListAppBackupsForInstances([]string{"instance-c"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(withDeleted) != 1 || withDeleted[0].Status != "deleted" {
		t.Fatalf("deleted backups = %+v, want deleted record when requested", withDeleted)
	}
	empty, err := db.ListAppBackupsForInstances(nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty instance list = %+v, want no records", empty)
	}
}

func TestAppBackupStatusIsMonotonicAndDeletePreservesAuditMetadata(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	createdAt := time.Date(2026, 7, 27, 1, 0, 0, 0, time.UTC)
	backup, err := db.SaveAppBackup(AppBackup{ID: "backup-status", App: "mysql", InstanceID: "instance-a", BackupType: "logical-full", Status: "pending", Path: "/backups/archive.tar", Checksum: "checksum", TaskID: "task-create", Metadata: `{"source":"primary"}`, CreatedAt: createdAt})
	if err != nil {
		t.Fatal(err)
	}
	backup.Status = "running"
	if _, err := db.SaveAppBackup(backup); err != nil {
		t.Fatal(err)
	}
	backup.Status = "pending"
	backup.Path = "/backups/stale-path.tar"
	got, err := db.SaveAppBackup(backup)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" || got.Path != "/backups/archive.tar" {
		t.Fatalf("stale update must not regress a running backup: %+v", got)
	}
	got.Status = "success"
	finished, err := db.SaveAppBackup(got)
	if err != nil {
		t.Fatal(err)
	}
	finished.Status = "running"
	finished.Path = "/backups/regressed-after-success.tar"
	finished, err = db.SaveAppBackup(finished)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Status != "success" || finished.Path != "/backups/archive.tar" {
		t.Fatalf("stale update must not regress a successful backup: %+v", finished)
	}
	directDelete := finished
	directDelete.Status = "deleted"
	directDelete.TaskID = "task-delete"
	directDelete.Path = "/backups/replaced.tar"
	directDelete.Checksum = "replaced"
	directDelete.Size = 999
	directDelete.Metadata = `{"deletedBy":"generic-save"}`
	directDelete, err = db.SaveAppBackup(directDelete)
	if err != nil {
		t.Fatal(err)
	}
	if directDelete.Status != "success" || directDelete.TaskID != "task-create" || directDelete.Path != "/backups/archive.tar" || directDelete.Checksum != "checksum" || directDelete.Size != 0 || directDelete.Metadata != `{"source":"primary"}` {
		t.Fatalf("generic save must not perform deletion or replace provenance: %+v", directDelete)
	}
	completedAt := createdAt.Add(time.Hour)
	deleted, err := db.MarkAppBackupDeleted(backup.ID, completedAt)
	if err != nil {
		t.Fatal(err)
	}
	if deleted.Status != "deleted" || deleted.TaskID != "task-create" || deleted.Path != "/backups/archive.tar" || deleted.Checksum != "checksum" || deleted.Size != 0 || deleted.Metadata != `{"source":"primary"}` || !deleted.CompletedAt.Equal(completedAt) {
		t.Fatalf("deleted backup lost audit metadata: %+v", deleted)
	}
	afterDelete := deleted
	afterDelete.TaskID = "task-rewrite"
	afterDelete.Path = "/backups/rewrite.tar"
	afterDelete, err = db.SaveAppBackup(afterDelete)
	if err != nil {
		t.Fatal(err)
	}
	if afterDelete.TaskID != "task-create" || afterDelete.Path != "/backups/archive.tar" || !afterDelete.CompletedAt.Equal(completedAt) {
		t.Fatalf("generic save must not rewrite a deleted backup: %+v", afterDelete)
	}
}

func TestSaveAppReleaseExpandsManifestAssets(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	manifest := `{
		"kind":"rollout-bundle",
		"artifacts":{
			"oauth":{"type":"java","file":"oauth.jar","sha256":"abc","size":12,"remotePath":"/releases/rel-1/services/oauth/artifact/oauth.jar"},
			"web-vue3":{"type":"web","file":"dist.zip","sha256":"def","size":34,"remotePath":"/releases/rel-1/services/web-vue3/artifact/dist.zip"}
		},
		"snapshots":{
			"runtimeSpecBefore":"/releases/rel-1/snapshot/before-runtime-spec.json",
			"envBefore":{"oauth":"/releases/rel-1/services/oauth/snapshot/before.env"}
		},
		"serviceRevisionsBefore":{"oauth":"old"},
		"serviceRevisionsAfter":{"oauth":"rel-1"}
	}`
	if _, err := db.SaveAppRelease(AppRelease{
		InstanceID:   instance.ID,
		App:          "aifar",
		Version:      "runtime-v2",
		ReleaseID:    "rel-1",
		ServerID:     "srv-1",
		Status:       "success",
		ManifestJSON: manifest,
	}); err != nil {
		t.Fatal(err)
	}
	artifacts, err := db.ListAppReleaseArtifacts(instance.ID, "rel-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 2 || artifacts[0].Name == "" || artifacts[1].Checksum == "" {
		t.Fatalf("expected manifest artifacts to be expanded, got %+v", artifacts)
	}
	snapshots, err := db.ListAppReleaseSnapshots(instance.ID, "rel-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 4 {
		t.Fatalf("expected runtime/env/revision snapshots, got %+v", snapshots)
	}
	updatedManifest := `{"artifacts":{"oauth":{"type":"java","file":"oauth-v2.jar","sha256":"xyz","size":56,"remotePath":"/releases/rel-1/services/oauth/artifact/oauth-v2.jar"}}}`
	if _, err := db.SaveAppRelease(AppRelease{
		InstanceID:   instance.ID,
		App:          "aifar",
		Version:      "runtime-v2",
		ReleaseID:    "rel-1",
		ServerID:     "srv-1",
		Status:       "success",
		ManifestJSON: updatedManifest,
	}); err != nil {
		t.Fatal(err)
	}
	artifacts, err = db.ListAppReleaseArtifacts(instance.ID, "rel-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(artifacts) != 1 || artifacts[0].Name != "oauth-v2.jar" {
		t.Fatalf("expected manifest asset replacement, got %+v", artifacts)
	}
}

func TestStatusSnapshotHistoryOnlyRecordsChanges(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, status := range []string{"available", "available", "failed"} {
		if _, _, err := db.UpsertStatusSnapshot(StatusSnapshot{
			Scope:      "app",
			ResourceID: "app-1",
			Status:     status,
			Payload:    `{"status":"` + status + `"}`,
		}); err != nil {
			t.Fatal(err)
		}
	}
	history, err := db.ListStatusSnapshotHistory("app", "app-1", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 2 || history[0].Status != "failed" || history[1].Status != "available" {
		t.Fatalf("unexpected status history: %+v", history)
	}
}

func TestLegacyMySQLClusterMigrationBackfillsAuthoritativeTopology(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "aifar.db")
	db, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacyClusterID := "mysql_cluster_1234567890abcdef12345678"
	mysqlInstanceIDs := []string{
		"app_111111111111111111111111",
		"app_222222222222222222222222",
		"app_333333333333333333333333",
	}
	serverIDs := []string{
		"srv_111111111111111111111111",
		"srv_222222222222222222222222",
		"srv_333333333333333333333333",
	}
	for index := range mysqlInstanceIDs {
		metadata, _ := json.Marshal(map[string]any{
			"clusterId":   legacyClusterID,
			"clusterName": "aifarCluster",
			"endpoint":    "192.0.2." + string(rune('1'+index)) + ":3306",
			"topology":    "innodb-cluster",
		})
		if _, err := db.SaveAppInstance(AppInstance{
			ID: mysqlInstanceIDs[index], App: "mysql", Version: "8.0.36", ServerID: serverIDs[index],
			Status: "installed", Topology: "innodb-cluster", Metadata: string(metadata),
		}); err != nil {
			db.Close()
			t.Fatal(err)
		}
		routerMetadata, _ := json.Marshal(map[string]any{
			"clusterId":   legacyClusterID,
			"clusterName": "aifarCluster",
			"endpoint":    "192.0.2." + string(rune('1'+index)) + ":6446",
			"topology":    "router",
		})
		if _, err := db.SaveAppInstance(AppInstance{
			ID: NewID("app"), App: "mysql-router", Version: "8.0.36", ServerID: serverIDs[index],
			Status: "installed", Topology: "router", Metadata: string(routerMetadata),
		}); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`delete from schema_migrations where version=2026073001`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clusters, err := db.ListAppClusters("mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Name != "aifarCluster" || !strings.HasPrefix(clusters[0].ID, "cluster_") {
		t.Fatalf("expected one controlled MySQL cluster, got %+v", clusters)
	}
	members, err := db.ListAppClusterMembers(clusters[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 3 {
		t.Fatalf("expected three authoritative members, got %+v", members)
	}
	roles := map[string]int{}
	for _, member := range members {
		roles[member.Role]++
		if member.Status != "ONLINE" {
			t.Fatalf("member status=%q, want ONLINE", member.Status)
		}
	}
	if roles["PRIMARY"] != 1 || roles["SECONDARY"] != 2 {
		t.Fatalf("unexpected initial roles: %+v", roles)
	}
	instances, err := db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	rewritten := 0
	for _, instance := range instances {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
			t.Fatal(err)
		}
		if metadata["clusterId"] == clusters[0].ID {
			rewritten++
		}
		if metadata["clusterId"] == legacyClusterID {
			t.Fatalf("legacy cluster ID remains on %s", instance.ID)
		}
	}
	if rewritten != 6 {
		t.Fatalf("rewritten instances=%d, want 6", rewritten)
	}
}

func TestSaveAppClusterDeploymentRegistersInstancesAndMembersAtomically(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clusterID := "cluster_1234567890abcdef12345678"
	instances := []AppInstance{
		{ID: "app_111111111111111111111111", App: "mysql", Version: "8.0.36", ServerID: "srv_111111111111111111111111", Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`},
		{ID: "app_222222222222222222222222", App: "mysql", Version: "8.0.36", ServerID: "srv_222222222222222222222222", Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`},
		{ID: "app_333333333333333333333333", App: "mysql", Version: "8.0.36", ServerID: "srv_333333333333333333333333", Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster_1234567890abcdef12345678"}`},
	}
	members := []AppClusterMember{
		{ClusterID: clusterID, InstanceID: instances[0].ID, ServerID: instances[0].ServerID, Role: "PRIMARY", Status: "ONLINE"},
		{ClusterID: clusterID, InstanceID: instances[1].ID, ServerID: instances[1].ServerID, Role: "SECONDARY", Status: "ONLINE"},
		{ClusterID: clusterID, InstanceID: instances[2].ID, ServerID: instances[2].ServerID, Role: "SECONDARY", Status: "ONLINE"},
	}
	saved, err := db.SaveAppClusterDeployment(AppCluster{ID: clusterID, App: "mysql", Name: "aifarCluster", Topology: "innodb-cluster", Status: "active"}, instances, members)
	if err != nil {
		t.Fatal(err)
	}
	if len(saved) != 3 {
		t.Fatalf("saved instances=%d, want 3", len(saved))
	}
	cluster, err := db.GetAppCluster(clusterID)
	if err != nil || cluster.Name != "aifarCluster" {
		t.Fatalf("unexpected cluster=%+v err=%v", cluster, err)
	}
	gotMembers, err := db.ListAppClusterMembers(clusterID)
	if err != nil || len(gotMembers) != 3 {
		t.Fatalf("unexpected members=%+v err=%v", gotMembers, err)
	}
	if gotInstances, err := db.ListAppInstances(); err != nil || len(gotInstances) != 3 {
		t.Fatalf("unexpected instances=%+v err=%v", gotInstances, err)
	}
}

func TestSaveAppClusterDeploymentRollsBackEveryRecordWhenInstanceInsertFails(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.db.Exec(`create trigger reject_cluster_instance before insert on app_instances begin select raise(abort,'injected'); end`); err != nil {
		t.Fatal(err)
	}
	clusterID := "cluster_1234567890abcdef12345678"
	instance := AppInstance{ID: "app_111111111111111111111111", App: "mysql", Version: "8.0.36", ServerID: "srv_111111111111111111111111", Status: "installed", Topology: "innodb-cluster", Metadata: `{}`}
	_, err = db.SaveAppClusterDeployment(
		AppCluster{ID: clusterID, App: "mysql", Name: "aifarCluster", Topology: "innodb-cluster"},
		[]AppInstance{instance},
		[]AppClusterMember{{ClusterID: clusterID, InstanceID: instance.ID, ServerID: instance.ServerID, Role: "PRIMARY", Status: "ONLINE"}},
	)
	if err == nil {
		t.Fatal("expected injected instance failure")
	}
	for _, table := range []string{"app_clusters", "app_instances", "app_cluster_members"} {
		count, countErr := db.CountRows(table)
		if countErr != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v, want empty rollback", table, count, countErr)
		}
	}
}

func TestLegacyMySQLClusterMigrationLeavesAmbiguousMembershipUntouched(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "aifar.db")
	db, err := Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacyClusterID := "mysql_cluster_abcdef1234567890abcdef12"
	for index, id := range []string{"app_aaaaaaaaaaaaaaaaaaaaaaaa", "app_bbbbbbbbbbbbbbbbbbbbbbbb", "app_cccccccccccccccccccccccc"} {
		metadata := `{"clusterId":"` + legacyClusterID + `","clusterName":"ambiguous","topology":"innodb-cluster"}`
		if _, err := db.SaveAppInstance(AppInstance{ID: id, App: "mysql", Version: "8.0.36", ServerID: "srv_duplicate", Status: "installed", Topology: "innodb-cluster", Metadata: metadata}); err != nil {
			db.Close()
			t.Fatal(err)
		}
		_ = index
	}
	if _, err := db.db.Exec(`delete from schema_migrations where version=2026073001`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	clusters, err := db.ListAppClusters("mysql")
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 0 {
		t.Fatalf("ambiguous legacy group must not be repaired: %+v", clusters)
	}
	instances, err := db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	for _, instance := range instances {
		if !strings.Contains(instance.Metadata, legacyClusterID) {
			t.Fatalf("ambiguous instance was unexpectedly rewritten: %+v", instance)
		}
	}
}
