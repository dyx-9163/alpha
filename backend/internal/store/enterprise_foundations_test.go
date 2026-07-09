package store

import (
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
