package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBootstrapUserAndServerLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.BootstrapUser("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	user, err := db.UserByUsername("admin")
	if err != nil {
		t.Fatal(err)
	}
	if user.TokenVersion != 1 {
		t.Fatalf("expected bootstrap token version 1, got %d", user.TokenVersion)
	}
	server, err := db.SaveServer(Server{Name: "node-1", Host: "127.0.0.1", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if server.Port != 22 {
		t.Fatalf("expected default port 22, got %d", server.Port)
	}
	if server.DeployDir != "/aifar/apps" {
		t.Fatalf("expected default deploy dir /aifar/apps, got %s", server.DeployDir)
	}
	servers, err := db.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("expected one server, got %d", len(servers))
	}
}

func TestDeleteAppReleaseRemovesOnlyRequestedReleaseAndAuxiliaryRecords(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "runtime-v2", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	for _, releaseID := range []string{"release-old", "release-keep"} {
		if _, err := db.SaveAppRelease(AppRelease{InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: releaseID, Status: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.SaveAppReleaseArtifact(AppReleaseArtifact{InstanceID: instance.ID, ReleaseID: "release-old", App: "aifar", ServiceName: "gateway", ArtifactType: "jar", Name: "gateway.jar"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAppReleaseSnapshot(AppReleaseSnapshot{InstanceID: instance.ID, ReleaseID: "release-old", App: "aifar", SnapshotKind: "manifest", PayloadJSON: `{}`}); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteAppRelease(instance.ID, "release-old"); err != nil {
		t.Fatal(err)
	}
	releases, err := db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].ReleaseID != "release-keep" {
		t.Fatalf("expected only release-keep to remain, got %+v", releases)
	}
	artifacts, err := db.ListAppReleaseArtifacts(instance.ID, "release-old")
	if err != nil || len(artifacts) != 0 {
		t.Fatalf("expected release artifacts to be deleted, got %+v err=%v", artifacts, err)
	}
	snapshots, err := db.ListAppReleaseSnapshots(instance.ID, "release-old")
	if err != nil || len(snapshots) != 0 {
		t.Fatalf("expected release snapshots to be deleted, got %+v err=%v", snapshots, err)
	}
}

func TestOpenReadOnlyWithSecretDoesNotCreateDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "aifar.db")
	db, err := OpenReadOnlyWithSecret(path, "secret")
	if err == nil {
		db.Close()
		t.Fatal("expected missing read-only database to fail")
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("read-only open created database path, stat err=%v", statErr)
	}
	if _, statErr := os.Stat(filepath.Dir(path)); !os.IsNotExist(statErr) {
		t.Fatalf("read-only open created database directory, stat err=%v", statErr)
	}
}

func TestOpenReadOnlyWithSecretCanReadButNotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "aifar.db")
	db, err := OpenWithSecret(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServer(Server{Name: "node-1", Host: "127.0.0.1", Username: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ro, err := OpenReadOnlyWithSecret(path, "secret")
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	servers, err := ro.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers=%d, want 1", len(servers))
	}
	if _, err := ro.SaveServer(Server{Name: "node-2", Host: "127.0.0.2", Username: "root"}); err == nil {
		t.Fatal("expected write through read-only store to fail")
	}
}

func TestStatusSnapshotVersioning(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, changed, err := db.UpsertStatusSnapshot(StatusSnapshot{
		Scope:      "docker.summary",
		ResourceID: "srv-1",
		ServerID:   "srv-1",
		Status:     "available",
		Payload:    `{"available":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || first.Version != 1 {
		t.Fatalf("first snapshot changed=%v version=%d, want changed version 1", changed, first.Version)
	}
	second, changed, err := db.UpsertStatusSnapshot(StatusSnapshot{
		Scope:      "docker.summary",
		ResourceID: "srv-1",
		ServerID:   "srv-1",
		Status:     "available",
		Payload:    `{"available":true}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if changed || second.Version != 1 {
		t.Fatalf("unchanged snapshot changed=%v version=%d, want unchanged version 1", changed, second.Version)
	}
	third, changed, err := db.UpsertStatusSnapshot(StatusSnapshot{
		Scope:      "docker.summary",
		ResourceID: "srv-1",
		ServerID:   "srv-1",
		Status:     "failed",
		Payload:    `{"available":false}`,
		LastError:  "connection refused",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !changed || third.Version != 2 {
		t.Fatalf("changed snapshot changed=%v version=%d, want changed version 2", changed, third.Version)
	}
}

func TestDeleteAppInstanceRemovesAssociatedStatusSnapshots(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "running"})
	if err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"app.instance", "aifar.runtime"} {
		if _, _, err := db.UpsertStatusSnapshot(StatusSnapshot{Scope: scope, ResourceID: instance.ID, ServerID: "srv-1", Status: "running"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := db.UpsertStatusSnapshot(StatusSnapshot{Scope: "aifar.runtime", ResourceID: "app-other", ServerID: "srv-1", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"app.instance", "aifar.runtime"} {
		if _, err := db.GetStatusSnapshot(scope, instance.ID); !IsNotFound(err) {
			t.Fatalf("%s snapshot still exists after instance delete: %v", scope, err)
		}
		history, err := db.ListStatusSnapshotHistory(scope, instance.ID, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(history) != 0 {
			t.Fatalf("%s snapshot history still exists after instance delete: %+v", scope, history)
		}
	}
	if _, err := db.GetStatusSnapshot("aifar.runtime", "app-other"); err != nil {
		t.Fatalf("unrelated snapshot was removed: %v", err)
	}
}

func TestAIFAROrchestrationCRUDAndInstanceCleanup(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "docker-apps", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARDeployment(AIFARDeployment{
		InstanceID:      instance.ID,
		ServiceName:     "permission",
		DesiredReplicas: 2,
		CurrentRevision: "rev-1",
		StrategyJSON:    `{"maxSurge":1,"maxUnavailable":0}`,
		Status:          "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARReplicaSet(AIFARReplicaSet{
		InstanceID:  instance.ID,
		ServiceName: "permission",
		Revision:    "rev-1",
		Image:       "aifar-permission:rev-1",
		DesiredPods: 2,
		ReadyPods:   2,
		Status:      "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveAIFARPod(AIFARPod{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		Revision:      "rev-1",
		PodID:         "permission-rev-1-r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Port:          38010,
		Status:        "ready",
		Ready:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "permission", []AIFARServiceEndpoint{{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		PodID:         "permission-rev-1-r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Revision:      "rev-1",
		Port:          38010,
		State:         "active",
		Ready:         true,
	}}); err != nil {
		t.Fatal(err)
	}
	deployments, err := db.ListAIFARDeployments(instance.ID)
	if err != nil || len(deployments) != 1 || deployments[0].DesiredReplicas != 2 {
		t.Fatalf("unexpected deployments: %+v err=%v", deployments, err)
	}
	replicaSets, err := db.ListAIFARReplicaSets(instance.ID)
	if err != nil || len(replicaSets) != 1 || replicaSets[0].Revision != "rev-1" {
		t.Fatalf("unexpected replica sets: %+v err=%v", replicaSets, err)
	}
	pods, err := db.ListAIFARPods(instance.ID)
	if err != nil || len(pods) != 1 || !pods[0].Ready {
		t.Fatalf("unexpected pods: %+v err=%v", pods, err)
	}
	endpoints, err := db.ListAIFARServiceEndpoints(instance.ID)
	if err != nil || len(endpoints) != 1 || endpoints[0].State != "active" {
		t.Fatalf("unexpected endpoints: %+v err=%v", endpoints, err)
	}
	if _, err := db.SaveAIFARPod(AIFARPod{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		Revision:      "rev-old",
		PodID:         "permission-rev-old-r1",
		ContainerName: "aifar-pod-admin-permission-rev-old-r1",
		Port:          38010,
		Status:        "ready",
		Ready:         true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.ReplaceAIFARServiceEndpoints(instance.ID, "permission", []AIFARServiceEndpoint{{
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		PodID:         "permission-rev-1-r1",
		ContainerName: "aifar-pod-admin-permission-rev-1-r1",
		Revision:      "rev-1",
		Port:          38010,
		State:         "active",
		Ready:         true,
	}, {
		InstanceID:    instance.ID,
		ServiceName:   "permission",
		PodID:         "permission-rev-old-r1",
		ContainerName: "aifar-pod-admin-permission-rev-old-r1",
		Revision:      "rev-old",
		Port:          38010,
		State:         "active",
		Ready:         true,
	}}); err != nil {
		t.Fatal(err)
	}
	prunedPods, err := db.PruneAIFARPodRecords(instance.ID, []string{"aifar-pod-admin-permission-rev-1-r1"})
	if err != nil {
		t.Fatal(err)
	}
	prunedEndpoints, err := db.PruneAIFARServiceEndpointRecords(instance.ID, []string{"aifar-pod-admin-permission-rev-1-r1"})
	if err != nil {
		t.Fatal(err)
	}
	if prunedPods != 1 || prunedEndpoints != 1 {
		t.Fatalf("expected one stale pod and endpoint pruned, got pods=%d endpoints=%d", prunedPods, prunedEndpoints)
	}
	pods, err = db.ListAIFARPods(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 1 || pods[0].ContainerName != "aifar-pod-admin-permission-rev-1-r1" {
		t.Fatalf("unexpected pods after prune: %+v", pods)
	}
	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	pods, err = db.ListAIFARPods(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pods) != 0 {
		t.Fatalf("expected AIFAR pods to be deleted with instance, got %+v", pods)
	}
}

func TestAIFAROrchestrationLockLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().Add(-time.Minute)
	if _, err := db.AcquireAIFAROrchestrationLock(AIFAROrchestrationLock{
		InstanceID:  instance.ID,
		ServiceName: "gateway",
		Operation:   "scale-service",
		Actor:       "admin",
		TaskID:      "tsk-gateway",
		StartedAt:   started,
		ExpiresAt:   started.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(AIFAROrchestrationLock{
		InstanceID:  instance.ID,
		ServiceName: "im",
		Operation:   "scale-service",
		Actor:       "admin",
		TaskID:      "tsk-im",
	}); err == nil {
		t.Fatal("expected different service mutation to conflict at instance scope")
	} else {
		var conflict AIFAROrchestrationLockConflict
		if !errors.As(err, &conflict) || conflict.Lock.ServiceName != "gateway" {
			t.Fatalf("expected active gateway mutation to own the instance conflict, got %T %v", err, err)
		}
	}
	if _, err := db.AcquireAIFAROrchestrationLock(AIFAROrchestrationLock{
		InstanceID:  instance.ID,
		ServiceName: "gateway",
		Operation:   "autoscale",
	}); err == nil {
		t.Fatal("expected same service lock conflict")
	} else {
		var conflict AIFAROrchestrationLockConflict
		if !errors.As(err, &conflict) || conflict.Lock.ServiceName != "gateway" {
			t.Fatalf("expected gateway conflict, got %T %v", err, err)
		}
	}
	if _, err := db.AcquireAIFAROrchestrationLock(AIFAROrchestrationLock{
		InstanceID: instance.ID,
		Operation:  "delete",
	}); err == nil {
		t.Fatal("expected global lock to wait for service locks")
	}
	released, err := db.ReleaseAIFAROrchestrationLock(instance.ID, "scale-service", "gateway")
	if err != nil || !released {
		t.Fatalf("expected gateway lock release, released=%v err=%v", released, err)
	}
	if _, err := db.AcquireAIFAROrchestrationLock(AIFAROrchestrationLock{
		InstanceID:  instance.ID,
		ServiceName: "gateway",
		Operation:   "autoscale",
	}); err != nil {
		t.Fatalf("expected gateway lock after release, got %v", err)
	}
	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("expected locks to be deleted with instance, got %+v", locks)
	}
}

func TestUserTokenVersionChangesOnPasswordAndRoleUpdate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.ResetUserPassword("ops", "first"); err != nil {
		t.Fatal(err)
	}
	user, err := db.UserByUsername("ops")
	if err != nil {
		t.Fatal(err)
	}
	if user.TokenVersion != 1 {
		t.Fatalf("expected initial token version 1, got %d", user.TokenVersion)
	}
	if err := db.ResetUserPassword("ops", "second"); err != nil {
		t.Fatal(err)
	}
	user, err = db.UserByUsername("ops")
	if err != nil {
		t.Fatal(err)
	}
	if user.TokenVersion != 2 {
		t.Fatalf("expected password reset to increment token version to 2, got %d", user.TokenVersion)
	}
	if err := db.SetUserRole("ops", "operator"); err != nil {
		t.Fatal(err)
	}
	user, err = db.UserByUsername("ops")
	if err != nil {
		t.Fatal(err)
	}
	if user.Role != "operator" || user.TokenVersion != 3 {
		t.Fatalf("expected role update to increment version, got role=%s version=%d", user.Role, user.TokenVersion)
	}
}

func TestServerSecretsAreEncryptedAtRest(t *testing.T) {
	db, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	server, err := db.SaveServer(Server{
		Name:       "node-1",
		Host:       "127.0.0.1",
		Username:   "root",
		Password:   "plain-password",
		PrivateKey: "plain-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	var rawPassword, rawPrivateKey string
	if err := db.db.QueryRow(`select password, private_key from servers where id=?`, server.ID).Scan(&rawPassword, &rawPrivateKey); err != nil {
		t.Fatal(err)
	}
	if rawPassword == "plain-password" || rawPrivateKey == "plain-key" {
		t.Fatalf("expected secrets to be encrypted at rest, got password=%q privateKey=%q", rawPassword, rawPrivateKey)
	}
	got, err := db.GetServer(server.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "plain-password" || got.PrivateKey != "plain-key" {
		t.Fatalf("expected decrypted secrets, got %+v", got)
	}
	public, err := db.GetServer(server.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if public.Password != "" || public.PrivateKey != "" {
		t.Fatalf("expected public server payload to hide secrets, got %+v", public)
	}
}

func TestServerReorderPersistsSortOrder(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.SaveServer(Server{Name: "node-1", Host: "10.0.0.1", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.SaveServer(Server{Name: "node-2", Host: "10.0.0.2", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := db.SaveServer(Server{Name: "node-3", Host: "10.0.0.3", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.ReorderServers([]string{second.ID, third.ID, first.ID}); err != nil {
		t.Fatal(err)
	}
	servers, err := db.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{second.ID, third.ID, first.ID}
	if len(servers) != len(want) {
		t.Fatalf("expected %d servers, got %d", len(want), len(servers))
	}
	for idx, server := range servers {
		if server.ID != want[idx] {
			t.Fatalf("expected server %d to be %s, got %s", idx, want[idx], server.ID)
		}
		if server.SortOrder != idx+1 {
			t.Fatalf("expected sort order %d, got %d", idx+1, server.SortOrder)
		}
	}
	got, err := db.GetServer(second.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.SortOrder != 1 {
		t.Fatalf("expected persisted sort order 1, got %d", got.SortOrder)
	}
}

func TestStorageSecretKeyIsEncryptedAndHidden(t *testing.T) {
	db, err := OpenWithSecret(filepath.Join(t.TempDir(), "aifar.db"), "test-secret")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	item, err := db.SaveStorageItem(StorageItem{
		InstanceID: "app-1",
		Kind:       "accessKey",
		Name:       "ops",
		AccessKey:  "ak",
		SecretKey:  "plain-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	if item.SecretKey != "" {
		t.Fatalf("expected saved storage item response to hide secret key")
	}
	var rawSecret string
	if err := db.db.QueryRow(`select secret_key from storage_items where instance_id=? and kind=? and name=?`, "app-1", "accessKey", "ops").Scan(&rawSecret); err != nil {
		t.Fatal(err)
	}
	if rawSecret == "plain-secret" {
		t.Fatalf("expected storage secret key to be encrypted at rest")
	}
	items, err := db.ListStorageItems("app-1", "accessKey")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].SecretKey != "" {
		t.Fatalf("expected listed storage item to hide secret key, got %+v", items)
	}
}

func TestTaskLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "test", Target: "local", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(task.ID, "info", "hello"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskTargetLog(task.ID, "srv-1", "info", "target hello"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(task.ID, "success", ""); err != nil {
		t.Fatal(err)
	}
	got, logs, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "success" || len(logs) != 2 {
		t.Fatalf("unexpected task result: %+v logs=%d", got, len(logs))
	}
	if logs[1].Target != "srv-1" {
		t.Fatalf("expected target log to retain server id, got %+v", logs[1])
	}
	if err := db.ClearTaskLogs(task.ID); err != nil {
		t.Fatal(err)
	}
	_, logs, err = db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 0 {
		t.Fatalf("expected logs to be cleared, got %d", len(logs))
	}
	if err := db.DeleteTask(task.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := db.GetTask(task.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted task to be missing, got %v", err)
	}
}

func TestRecoverInterruptedTasks(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	pending, err := db.CreateTask(Task{Type: "aifar.scale.offline", Target: "aifar-1:file", Status: "pending", CreatedBy: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	running, err := db.CreateTask(Task{Type: "aifar.scale.in", Target: "aifar-1:oauth", Status: "running", CreatedBy: "admin", StartedAt: time.Now().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	finished, err := db.CreateTask(Task{Type: "aifar.scale.out", Target: "aifar-1:gateway", Status: "success", CreatedBy: "admin", FinishedAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(running.ID, "srv-1", "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(running.ID, "srv-1", "scale", "scale", 1, "running", ""); err != nil {
		t.Fatal(err)
	}

	recovered, err := db.RecoverInterruptedTasks("server restarted")
	if err != nil {
		t.Fatal(err)
	}
	if len(recovered) != 2 {
		t.Fatalf("expected two recovered tasks, got %+v", recovered)
	}
	for _, id := range []string{pending.ID, running.ID} {
		task, logs, err := db.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if task.Status != "failed" || task.Error != "server restarted" || task.FinishedAt.IsZero() {
			t.Fatalf("expected task %s to be failed after recovery, got %+v", id, task)
		}
		if len(logs) == 0 || logs[len(logs)-1].Message != "server restarted" {
			t.Fatalf("expected recovery log for task %s, got %+v", id, logs)
		}
	}
	targets, err := db.ListTaskTargets(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Status != "failed" || targets[0].Error != "server restarted" {
		t.Fatalf("expected running target to be failed, got %+v", targets)
	}
	steps, err := db.ListTaskSteps(running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Status != "failed" || steps[0].Error != "server restarted" {
		t.Fatalf("expected running step to be failed, got %+v", steps)
	}
	task, _, err := db.GetTask(finished.ID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Status != "success" {
		t.Fatalf("expected finished task to remain success, got %+v", task)
	}
}

func TestReconcilePendingAppReleasesFromTerminalTasks(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{ID: "aifar-1", App: "aifar", Version: "runtime-v2", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range []Task{
		{ID: "task-failed", Type: "apps.aifar.update", Status: "failed", Error: "no space left on device"},
		{ID: "task-cancelled", Type: "apps.aifar.update", Status: "cancelled", Error: "cancelled by operator"},
		{ID: "task-running", Type: "apps.aifar.update", Status: "running"},
	} {
		if _, err := db.CreateTask(task); err != nil {
			t.Fatal(err)
		}
	}
	for _, item := range []struct {
		releaseID string
		taskID    string
	}{
		{releaseID: "release-failed", taskID: "task-failed"},
		{releaseID: "release-cancelled", taskID: "task-cancelled"},
		{releaseID: "release-running", taskID: "task-running"},
		{releaseID: "release-unlinked", taskID: ""},
	} {
		manifest, _ := json.Marshal(map[string]any{"releaseId": item.releaseID, "taskId": item.taskID, "status": "pending", "phase": "pending"})
		if _, err := db.SaveAppRelease(AppRelease{
			InstanceID: instance.ID, App: "aifar", Version: "runtime-v2", ReleaseID: item.releaseID,
			ServerID: "srv-1", Status: "pending", ManifestJSON: string(manifest), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}

	updated, err := db.ReconcilePendingAppReleases()
	if err != nil {
		t.Fatal(err)
	}
	if updated != 2 {
		t.Fatalf("expected two reconciled releases, got %d", updated)
	}
	releases, err := db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]AppRelease{}
	for _, release := range releases {
		byID[release.ReleaseID] = release
	}
	for _, releaseID := range []string{"release-failed", "release-cancelled"} {
		release := byID[releaseID]
		if release.Status != "failed" || !release.ActivatedAt.IsZero() {
			t.Fatalf("expected %s to be failed without activation, got %+v", releaseID, release)
		}
		var manifest map[string]any
		if err := json.Unmarshal([]byte(release.ManifestJSON), &manifest); err != nil {
			t.Fatal(err)
		}
		if manifest["status"] != "failed" || manifest["phase"] != "failed" || strings.TrimSpace(manifest["failedAt"].(string)) == "" || strings.TrimSpace(manifest["error"].(string)) == "" {
			t.Fatalf("expected reconciled failure manifest, got %s", release.ManifestJSON)
		}
	}
	for _, releaseID := range []string{"release-running", "release-unlinked"} {
		if release := byID[releaseID]; release.Status != "pending" {
			t.Fatalf("expected %s to remain pending, got %+v", releaseID, release)
		}
	}
}

func TestBackupDatabase(t *testing.T) {
	root := t.TempDir()
	db, err := Open(filepath.Join(root, "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.BootstrapUser("admin", "secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.SaveServer(Server{Name: "node-1", Host: "127.0.0.1", Username: "root"}); err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(root, "backups", "snapshot.db")
	size, checksum, err := db.BackupDatabase(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	if size <= 0 || len(checksum) != 64 {
		t.Fatalf("expected backup size and sha256, got size=%d sha=%q", size, checksum)
	}
	backup, err := Open(backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer backup.Close()
	if _, err := backup.UserByUsername("admin"); err != nil {
		t.Fatalf("expected backed up user to be readable: %v", err)
	}
	servers, err := backup.ListServers()
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 1 || servers[0].Name != "node-1" {
		t.Fatalf("expected backed up server, got %+v", servers)
	}
}

func TestClearTaskLogsForTasks(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	first, err := db.CreateTask(Task{Type: "apps.docker.install", Target: "srv-1", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := db.CreateTask(Task{Type: "apps.mysql.install", Target: "srv-2", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	third, err := db.CreateTask(Task{Type: "servers.probe", Target: "srv-3", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(first.ID, "info", "first"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(second.ID, "info", "second-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(second.ID, "info", "second-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(third.ID, "info", "third"); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.ClearTaskLogsForTasks([]string{first.ID, second.ID})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 3 {
		t.Fatalf("expected 3 deleted log rows, got %d", deleted)
	}
	for _, id := range []string{first.ID, second.ID} {
		_, logs, err := db.GetTask(id)
		if err != nil {
			t.Fatal(err)
		}
		if len(logs) != 0 {
			t.Fatalf("expected logs for %s to be cleared, got %d", id, len(logs))
		}
	}
	_, logs, err := db.GetTask(third.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 {
		t.Fatalf("expected third task logs to remain, got %d", len(logs))
	}
}

func TestDeleteFinishedTasksBefore(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Now()
	oldCutoff := now.Add(-48 * time.Hour)
	oldFinished, err := db.CreateTask(Task{Type: "apps.docker.install", Target: "srv-old", Status: "success", CreatedBy: "tester", CreatedAt: oldCutoff, FinishedAt: oldCutoff})
	if err != nil {
		t.Fatal(err)
	}
	recentFinished, err := db.CreateTask(Task{Type: "apps.mysql.install", Target: "srv-recent", Status: "success", CreatedBy: "tester", CreatedAt: now, FinishedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	running, err := db.CreateTask(Task{Type: "servers.probe", Target: "srv-running", Status: "running", CreatedBy: "tester", CreatedAt: oldCutoff, StartedAt: oldCutoff})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(oldFinished.ID, "info", "old"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(oldFinished.ID, "srv-old", "success", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(oldFinished.ID, "srv-old", "install", "install", 1, "success", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(recentFinished.ID, "info", "recent"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(running.ID, "info", "running"); err != nil {
		t.Fatal(err)
	}

	deleted, err := db.DeleteFinishedTasksBefore(now.Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one old finished task deleted, got %d", deleted)
	}
	if _, _, err := db.GetTask(oldFinished.ID); !IsNotFound(err) {
		t.Fatalf("expected old finished task to be deleted, got %v", err)
	}
	for _, id := range []string{recentFinished.ID, running.ID} {
		if _, _, err := db.GetTask(id); err != nil {
			t.Fatalf("expected task %s to remain, got %v", id, err)
		}
	}
	for _, table := range []string{"task_logs", "task_targets", "task_steps"} {
		var count int
		if err := db.db.QueryRow(`select count(*) from `+table+` where task_id=?`, oldFinished.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("expected %s rows for deleted task to be removed, got %d", table, count)
		}
	}
}

func TestTaskTargetsAndSteps(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "apps.docker.install", Target: "srv-1", CreatedBy: "tester"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(task.ID, "srv-1", "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(task.ID, "srv-1", "load-server", "load target server", 1, "running", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(task.ID, "srv-1", "load-server", "", 0, "success", ""); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(task.ID, "srv-1", "success", ""); err != nil {
		t.Fatal(err)
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Status != "success" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
	if len(steps) != 1 || steps[0].Status != "success" || steps[0].Title != "load target server" {
		t.Fatalf("unexpected steps: %+v", steps)
	}
}

func TestTaskPersistenceMasksSensitiveText(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	task, err := db.CreateTask(Task{Type: "test", Target: "token=target-secret", CreatedBy: "tester", Error: "password=create-secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddTaskLog(task.ID, "error", "failed with password=log-secret"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateTaskStatus(task.ID, "failed", "token=status-secret"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskTarget(task.ID, "password=target-secret", "failed", "secret=target-error"); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertTaskStep(task.ID, "password=step-target", "install", "install", 1, "failed", "authorization=step-error"); err != nil {
		t.Fatal(err)
	}

	got, logs, err := db.GetTask(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	targets, err := db.ListTaskTargets(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	steps, err := db.ListTaskSteps(task.ID)
	if err != nil {
		t.Fatal(err)
	}
	combined := got.Target + got.Error + logs[0].Message + targets[0].Target + targets[0].Error + steps[0].Target + steps[0].Error
	for _, leaked := range []string{"target-secret", "create-secret", "log-secret", "status-secret", "target-error", "step-target", "step-error"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("expected %q to be masked from persisted task fields: %q", leaked, combined)
		}
	}
}

func TestDeleteAuditLogsBefore(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AddAudit("admin", "old.action", "srv-old", "success", "old"); err != nil {
		t.Fatal(err)
	}
	if err := db.AddAudit("admin", "recent.action", "srv-recent", "success", "recent"); err != nil {
		t.Fatal(err)
	}
	oldTime := time.Now().Add(-48 * time.Hour)
	if _, err := db.db.Exec(`update audit_logs set created_at=? where action=?`, oldTime, "old.action"); err != nil {
		t.Fatal(err)
	}
	deleted, err := db.DeleteAuditLogsBefore(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected one old audit row deleted, got %d", deleted)
	}
	items, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Action != "recent.action" {
		t.Fatalf("expected only recent audit row to remain, got %+v", items)
	}
}

func TestAuditMasksSensitiveText(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AddAudit("admin", "servers.save", "password=target-secret", "success", `{"token":"message-secret"}`); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListAudit()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one audit row, got %d", len(items))
	}
	combined := items[0].Target + items[0].Message
	for _, leaked := range []string{"target-secret", "message-secret"} {
		if strings.Contains(combined, leaked) {
			t.Fatalf("expected %q to be masked from persisted audit fields: %q", leaked, combined)
		}
	}
}

func TestAppInstanceLifecycle(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "docker", Version: "24.0.9", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.App != "docker" || got.ServerID != "srv-1" {
		t.Fatalf("unexpected app instance: %+v", got)
	}
	if _, err := db.SaveAppRelease(AppRelease{
		InstanceID:   instance.ID,
		App:          "docker",
		Version:      "24.0.9",
		ReleaseID:    "20260702T120000Z-24.0.9",
		ServerID:     "srv-1",
		Status:       "success",
		ManifestJSON: `{"releaseId":"20260702T120000Z-24.0.9"}`,
		ConfigHash:   strings.Repeat("a", 64),
	}); err != nil {
		t.Fatal(err)
	}
	releases, err := db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 1 || releases[0].ReleaseID != "20260702T120000Z-24.0.9" {
		t.Fatalf("unexpected app releases: %+v", releases)
	}
	if err := db.DeleteAppInstance(instance.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetAppInstance(instance.ID); !IsNotFound(err) {
		t.Fatalf("expected deleted app instance to be missing, got %v", err)
	}
	releases, err = db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 0 {
		t.Fatalf("expected app releases to be deleted with instance, got %+v", releases)
	}
}

func TestAppReleaseRetentionKeepsLatestThreeSuccesses(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "docker-apps", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	for idx := 0; idx < 5; idx++ {
		_, err := db.SaveAppRelease(AppRelease{
			InstanceID:  instance.ID,
			App:         "aifar",
			Version:     "docker-apps",
			ReleaseID:   "rel-" + string(rune('0'+idx)),
			ServerID:    "srv-1",
			Status:      "success",
			CreatedAt:   time.Date(2026, 7, 2, 12, idx, 0, 0, time.UTC),
			ActivatedAt: time.Date(2026, 7, 2, 12, idx, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := db.DeleteOldAppReleases(instance.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("expected two old releases deleted, got %d", deleted)
	}
	releases, err := db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 3 || releases[0].ReleaseID != "rel-4" || releases[2].ReleaseID != "rel-2" {
		t.Fatalf("expected latest three releases, got %+v", releases)
	}
}

func TestAppReleaseRetentionPrunesHistoricalBaseChain(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{App: "aifar", Version: "docker-apps", ServerID: "srv-1", Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	releases := []struct {
		id     string
		base   string
		minute int
		status string
	}{
		{id: "base-1", minute: 1, status: "success"},
		{id: "base-2", minute: 2, status: "success"},
		{id: "base-3", minute: 3, status: "success"},
		{id: "partial-4", base: "base-1", minute: 4, status: "success"},
		{id: "partial-5", base: "partial-4", minute: 5, status: "success"},
	}
	for _, release := range releases {
		manifest := `{"releaseId":"` + release.id + `"}`
		if release.base != "" {
			manifest = `{"releaseId":"` + release.id + `","baseReleaseId":"` + release.base + `"}`
		}
		_, err := db.SaveAppRelease(AppRelease{
			InstanceID:   instance.ID,
			App:          "aifar",
			Version:      "docker-apps",
			ReleaseID:    release.id,
			ServerID:     "srv-1",
			Status:       release.status,
			ManifestJSON: manifest,
			CreatedAt:    time.Date(2026, 7, 2, 12, release.minute, 0, 0, time.UTC),
			ActivatedAt:  time.Date(2026, 7, 2, 12, release.minute, 0, 0, time.UTC),
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := db.DeleteOldAppReleases(instance.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 2 {
		t.Fatalf("expected historical ancestors outside retention window to be deleted, got %d", deleted)
	}
	got, err := db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, release := range got {
		kept[release.ReleaseID] = true
	}
	for _, want := range []string{"partial-5", "partial-4", "base-3"} {
		if !kept[want] {
			t.Fatalf("expected retained release %s to be kept, got %+v", want, got)
		}
	}
	for _, unwanted := range []string{"base-2", "base-1"} {
		if kept[unwanted] {
			t.Fatalf("expected historical ancestor %s to be deleted, got %+v", unwanted, got)
		}
	}
}

func TestAppReleaseRetentionKeepsActiveServiceRevisionOutsideWindow(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	instance, err := db.SaveAppInstance(AppInstance{
		App:      "aifar",
		Version:  "docker-apps",
		ServerID: "srv-1",
		Status:   "installed",
		Metadata: `{"currentRevision":"rel-5","serviceRevisions":{"oauth":"rel-1","web-vue3":"rel-5"}}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	for idx := 1; idx <= 5; idx++ {
		releaseID := fmt.Sprintf("rel-%d", idx)
		if _, err := db.SaveAppRelease(AppRelease{
			InstanceID:   instance.ID,
			App:          "aifar",
			Version:      "docker-apps",
			ReleaseID:    releaseID,
			ServerID:     "srv-1",
			Status:       "success",
			ManifestJSON: `{"releaseId":"` + releaseID + `"}`,
			CreatedAt:    time.Date(2026, 7, 3, 12, idx, 0, 0, time.UTC),
			ActivatedAt:  time.Date(2026, 7, 3, 12, idx, 0, 0, time.UTC),
		}); err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := db.DeleteOldAppReleases(instance.ID, 3)
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("expected only inactive rel-2 outside retention window to be deleted, got %d", deleted)
	}
	got, err := db.ListAppReleases(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	kept := map[string]bool{}
	for _, release := range got {
		kept[release.ReleaseID] = true
	}
	for _, want := range []string{"rel-5", "rel-4", "rel-3", "rel-1"} {
		if !kept[want] {
			t.Fatalf("expected release %s to be kept, got %+v", want, got)
		}
	}
	if kept["rel-2"] {
		t.Fatalf("expected inactive rel-2 to be deleted, got %+v", got)
	}
}
