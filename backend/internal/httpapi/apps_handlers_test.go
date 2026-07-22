package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
)

func TestRecordFailedInstallInstancesCreatesCleanupInstance(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "minio-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}

	count, err := api.recordFailedInstallInstances(context.Background(), registry.InstallRequest{
		App:       "minio",
		Version:   "2026-test",
		Topology:  "standalone",
		ServerIDs: []string{server.ID},
		Parameters: map[string]any{
			"apiPort":     9010,
			"consolePort": "9011",
			"rootUser":    "admin",
		},
	}, time.Now().Add(-time.Minute), "task-failed", errors.New("remote install failed"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("expected one failed instance, got %d", count)
	}

	instances, err := db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 {
		t.Fatalf("expected one app instance, got %+v", instances)
	}
	instance := instances[0]
	if instance.App != "minio" || instance.ServerID != server.ID || instance.Status != "failed" {
		t.Fatalf("unexpected failed instance: %+v", instance)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(instance.Metadata), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["installFailed"] != true || metadata["taskId"] != "task-failed" || metadata["endpoint"] != "http://10.0.0.9:9010" {
		t.Fatalf("failed install metadata missing cleanup context: %+v", metadata)
	}
}

func TestAIFARReleaseResponseOmitsZeroActivationTime(t *testing.T) {
	failed := aifarReleaseResponseItem(store.AppRelease{
		ID: "rel-failed", InstanceID: "aifar-1", App: "aifar", Version: "runtime-v2",
		ReleaseID: "release-failed", Status: "failed", CreatedAt: time.Now(),
	}, map[string]any{"kind": "rollout", "changedServices": []any{"oauth"}})
	if _, exists := failed["activatedAt"]; exists {
		t.Fatalf("failed release must not expose a zero activation time: %+v", failed)
	}

	activatedAt := time.Now().UTC()
	success := aifarReleaseResponseItem(store.AppRelease{
		ID: "rel-success", InstanceID: "aifar-1", App: "aifar", Version: "runtime-v2",
		ReleaseID: "release-success", Status: "success", CreatedAt: activatedAt, ActivatedAt: activatedAt,
	}, map[string]any{"kind": "rollout", "changedServices": []any{"oauth"}, "artifacts": map[string]any{"oauth": map[string]any{"file": "oauth.jar"}}})
	if got, exists := success["activatedAt"]; !exists || got != activatedAt {
		t.Fatalf("successful release must expose activation time: %+v", success)
	}
}

func TestRecordFailedInstallInstancesSkipsInstancesRecordedDuringTask(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "redis-1", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Now().Add(-time.Second)
	if _, err := db.SaveAppInstance(store.AppInstance{
		App:      "redis",
		Version:  "7.2.14",
		ServerID: server.ID,
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"port":6379}`,
	}); err != nil {
		t.Fatal(err)
	}

	count, err := api.recordFailedInstallInstances(context.Background(), registry.InstallRequest{
		App:       "redis",
		Version:   "7.2.14",
		Topology:  "standalone",
		ServerIDs: []string{server.ID},
	}, startedAt, "task-failed", errors.New("late cluster bootstrap failed"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("expected no duplicate failed instance, got %d", count)
	}
	instances, err := db.ListAppInstances()
	if err != nil {
		t.Fatal(err)
	}
	if len(instances) != 1 || instances[0].Status != "installed" {
		t.Fatalf("expected only the recorded installed instance, got %+v", instances)
	}
}

func TestRequireExplicitInstallPasswordsRejectsDefaultFallback(t *testing.T) {
	if err := requireExplicitInstallPasswords("mysql", "en", map[string]any{"rootUser": "root"}); err == nil {
		t.Fatal("expected mysql install without password to be rejected")
	}
	if err := requireExplicitInstallPasswords("mysql", "en", map[string]any{"rootPassword": "manual"}); err != nil {
		t.Fatalf("expected mysql explicit password to pass: %v", err)
	}
	if err := requireExplicitInstallPasswords("nacos", "en", map[string]any{"nacosPassword": "manual", "dbSource": "manual"}); err == nil {
		t.Fatal("expected nacos manual database source without db password to be rejected")
	}
	if err := requireExplicitInstallPasswords("nacos", "en", map[string]any{"nacosPassword": "manual", "dbSource": "manual", "dbPassword": "db-manual"}); err != nil {
		t.Fatalf("expected nacos explicit passwords to pass: %v", err)
	}
}

func TestDeleteAppInstanceStoresPlanBeforeTaskRuns(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "demo"}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "demo-1", Host: "10.0.0.9", Username: "root", Password: "server-pass"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "demo", Version: "1.0.0", ServerID: server.ID, Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/delete", strings.NewReader(`{"serverPassword":"server-pass"}`))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	steps, err := db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Target != server.ID || steps[0].Name != "delete" || steps[0].Status != "pending" {
		t.Fatalf("expected pre-stored delete plan step, got %+v", steps)
	}
	targets, err := db.ListTaskTargets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Target != server.ID || targets[0].Status != "pending" {
		t.Fatalf("expected pre-stored delete target, got %+v", targets)
	}
	waitForTaskStatus(t, db, taskID, "success")
	if module.deleteCalls != 1 {
		t.Fatalf("expected delete module call, got %d", module.deleteCalls)
	}
}

func TestCheckAppInstanceStoresPlanBeforeTaskRuns(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "demo"}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "demo-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "demo", Version: "1.0.0", ServerID: server.ID, Status: "installed"})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/check", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	steps, err := db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 1 || steps[0].Target != server.ID || steps[0].Name != "check" || steps[0].Status != "pending" {
		t.Fatalf("expected pre-stored check plan step, got %+v", steps)
	}
	waitForTaskStatus(t, db, taskID, "success")
	if module.checkCalls != 1 {
		t.Fatalf("expected check module call, got %d", module.checkCalls)
	}
}

func TestStorageCleanupEstimateUsesMinioModule(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "minio"}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "minio-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "minio", Version: "2025", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{"apiPort":9000}`})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/storage/"+instance.ID+"/cleanup-estimate?retentionDays=14", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body registry.StorageCleanupEstimateResult
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if module.cleanupEstimateCalls != 1 || module.lastCleanupRetentionDays != 14 {
		t.Fatalf("expected cleanup estimate module call with 14 days, calls=%d days=%d", module.cleanupEstimateCalls, module.lastCleanupRetentionDays)
	}
	if body.Status != "available" || body.RetentionDays != 14 || body.ObjectCount != 3 || body.Bytes != 2048 {
		t.Fatalf("unexpected cleanup estimate response: %+v", body)
	}
}

func TestStorageCleanupPolicyStartsTaskAndStoresPolicy(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "minio"}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "minio-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "minio", Version: "2025", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{"apiPort":9000}`})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"enabled":true,"bucket":"aifar","prefix":"logs/","retentionDays":60}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/storage/"+instance.ID+"/cleanup-policy", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	steps, err := db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Name != "apply-cleanup-policy" || steps[1].Name != "record-cleanup-policy" {
		t.Fatalf("expected cleanup policy task plan, got %+v", steps)
	}
	waitForTaskStatus(t, db, taskID, "success")
	if module.cleanupPolicyCalls != 1 || module.lastCleanupPolicyRetentionDays != 60 || module.lastCleanupPolicyBucket != "aifar" || module.lastCleanupPolicyPrefix != "logs/" {
		t.Fatalf("expected cleanup policy module call, got calls=%d bucket=%s prefix=%s days=%d", module.cleanupPolicyCalls, module.lastCleanupPolicyBucket, module.lastCleanupPolicyPrefix, module.lastCleanupPolicyRetentionDays)
	}
	items, err := db.ListStorageItems(instance.ID, "cleanupPolicy")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].Name != "aifar:logs/" || items[0].Policy != "enabled" {
		t.Fatalf("expected stored cleanup policy item, got %+v", items)
	}
	current, err := db.GetAppInstance(instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(current.Metadata, `"cleanupPolicy"`) || !strings.Contains(current.Metadata, `"retentionDays":60`) || !strings.Contains(current.Metadata, `"ruleId":"rule-test"`) {
		t.Fatalf("expected cleanup policy metadata to be recorded: %s", current.Metadata)
	}
}

func TestStorageCleanupPolicyRejectsInvalidBucketAndPrefix(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "minio"}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "minio-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "minio", Version: "2025", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{"apiPort":9000}`})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")

	tests := []struct {
		name string
		body string
		code string
	}{
		{name: "bucket with whitespace", body: `{"enabled":true,"bucket":"aifar logs","retentionDays":60}`, code: "INVALID_STORAGE_CLEANUP_BUCKET"},
		{name: "prefix with newline", body: "{\"enabled\":true,\"bucket\":\"aifar\",\"prefix\":\"logs\\n2026\",\"retentionDays\":60}", code: "INVALID_STORAGE_CLEANUP_PREFIX"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/storage/"+instance.ID+"/cleanup-policy", strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			api.Router().ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400, got %d body=%s", rec.Code, rec.Body.String())
			}
			var body map[string]any
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body["code"] != tt.code {
				t.Fatalf("expected code %s, got %#v", tt.code, body)
			}
		})
	}
	if module.cleanupPolicyCalls != 0 {
		t.Fatalf("invalid requests should not call cleanup policy module, got %d", module.cleanupPolicyCalls)
	}
}

func TestInstallAppRejectsConcurrentMutationLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{
		name:           "demo",
		installStarted: make(chan struct{}, 1),
		installRelease: make(chan struct{}),
	}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "demo-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertResource(store.Resource{App: "demo", Part: "backend", Version: "1.0.0", Path: "resources/demo/1.0.0"}); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	body := `{"serverId":"` + server.ID + `","version":"1.0.0"}`

	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/demo/install", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected first install to be accepted, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	released := false
	defer func() {
		if !released {
			close(module.installRelease)
		}
	}()

	select {
	case <-module.installStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for install task to start")
	}
	locks, err := db.ListOperationLocks("app-target", "demo:"+server.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].OwnerTaskID != taskID || locks[0].Operation != "mutate" {
		t.Fatalf("expected active install mutation lock, got %+v", locks)
	}

	req2 := httptest.NewRequest(http.MethodPost, "/api/v2/apps/demo/install", strings.NewReader(body))
	req2.Header.Set("Authorization", "Bearer "+token)
	req2.Header.Set("Content-Type", "application/json")
	rec2 := httptest.NewRecorder()
	api.Router().ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("expected concurrent install to be rejected, got %d body=%s", rec2.Code, rec2.Body.String())
	}

	close(module.installRelease)
	released = true
	waitForTaskStatus(t, db, taskID, "success")
	deadline := time.Now().Add(2 * time.Second)
	for {
		active, err := db.ListOperationLocks("app-target", "demo:"+server.ID, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(active) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected operation lock to be released, got %+v", active)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestStartMySQLClusterStoresPlanBeforeTaskRuns(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "mysql"}
	api.apps = registry.New(module)
	server1, err := db.SaveServer(store.Server{Name: "mysql-1", Host: "10.0.0.1", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	server2, err := db.SaveServer(store.Server{Name: "mysql-2", Host: "10.0.0.2", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance1, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server1.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster-a"}`})
	if err != nil {
		t.Fatal(err)
	}
	instance2, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server2.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{"clusterId":"cluster-a"}`})
	if err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"instanceIds":["` + instance1.ID + `","` + instance2.ID + `"]}`
	req := httptest.NewRequest(http.MethodPost, "/api/v2/database/mysql/clusters/start", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	steps, err := db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != 2 || steps[0].Name != "cluster-start" || steps[1].Name != "cluster-start" {
		t.Fatalf("expected pre-stored cluster plan steps, got %+v", steps)
	}
	waitForTaskStatus(t, db, taskID, "success")
	if module.clusterStartCalls != 1 {
		t.Fatalf("expected cluster start module call, got %d", module.clusterStartCalls)
	}
}

func TestInstallPostHookRecordsCredentialReferencesAndClusterMembers(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "redis-1", Host: "10.0.0.8", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "redis",
		Version:  "7.2.14",
		ServerID: server.ID,
		Status:   "installed",
		Topology: "sentinel",
		Metadata: `{"replicationGroupId":"redis-prod","role":"master","endpoint":"10.0.0.8:6379"}`,
	})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := db.SaveCredential(store.Credential{Name: "redis-admin", Kind: "redis", Secret: map[string]string{"password": "secret"}})
	if err != nil {
		t.Fatal(err)
	}

	api.bindInstallCredentialReferences("redis", registry.InstallRequest{
		App:       "redis",
		Version:   "7.2.14",
		Topology:  "sentinel",
		ServerIDs: []string{server.ID},
		Actor:     "admin",
		Parameters: map[string]any{
			"redisCredentialId": credential.ID,
		},
	}, nil)

	refs, err := db.ListCredentialReferences(credential.ID, "app-instance", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || refs[0].Purpose != "redis" || refs[0].Generated {
		t.Fatalf("expected selected credential reference, got %+v", refs)
	}
	clusters, err := db.ListAppClusters("redis")
	if err != nil {
		t.Fatal(err)
	}
	if len(clusters) != 1 || clusters[0].Name != "redis-prod" || clusters[0].Topology != "sentinel" {
		t.Fatalf("expected redis cluster record, got %+v", clusters)
	}
	members, err := db.ListAppClusterMembers(clusters[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(members) != 1 || members[0].InstanceID != instance.ID || members[0].Role != "master" {
		t.Fatalf("expected redis cluster member, got %+v", members)
	}

	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodGet, "/api/v2/credentials/"+credential.ID+"/references", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected references response 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []store.CredentialReference `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Items) != 1 || body.Items[0].ResourceID != instance.ID {
		t.Fatalf("unexpected references body: %+v", body)
	}
}

func TestInstallPostHookRecordsGeneratedCredentialReference(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	server, err := db.SaveServer(store.Server{Name: "minio-1", Host: "10.0.0.9", Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{
		App:      "minio",
		Version:  "2025",
		ServerID: server.ID,
		Status:   "installed",
		Topology: "standalone",
		Metadata: `{"apiPort":9000,"endpoint":"http://10.0.0.9:9000"}`,
	})
	if err != nil {
		t.Fatal(err)
	}

	api.bindInstallCredentialReferences("minio", registry.InstallRequest{
		App:       "minio",
		Version:   "2025",
		Topology:  "standalone",
		ServerIDs: []string{server.ID},
		Actor:     "admin",
		Parameters: map[string]any{
			"rootUser":     "admin",
			"rootPassword": "manual-secret",
			"apiPort":      9000,
		},
	}, nil)

	credentials, err := db.ListCredentials(store.CredentialQuery{Kind: "minio"})
	if err != nil {
		t.Fatal(err)
	}
	if len(credentials) != 1 || credentials[0].AppInstanceID != instance.ID {
		t.Fatalf("expected generated minio credential, got %+v", credentials)
	}
	refs, err := db.ListCredentialReferences(credentials[0].ID, "app-instance", instance.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 1 || !refs[0].Generated || refs[0].LifecyclePolicy != "delete-with-resource" {
		t.Fatalf("expected generated credential reference, got %+v", refs)
	}
}

func decodeTaskID(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	taskID, _ := body["taskId"].(string)
	if taskID == "" {
		t.Fatalf("expected taskId in response: %+v", body)
	}
	return taskID
}

type fakePlannedLifecycleModule struct {
	name                           string
	deleteCalls                    int
	checkCalls                     int
	installCalls                   int
	clusterStartCalls              int
	cleanupEstimateCalls           int
	lastCleanupRetentionDays       int
	cleanupPolicyCalls             int
	lastCleanupPolicyBucket        string
	lastCleanupPolicyPrefix        string
	lastCleanupPolicyRetentionDays int
	installStarted                 chan struct{}
	installRelease                 chan struct{}
}

func (m *fakePlannedLifecycleModule) Name() string { return m.name }

func (m *fakePlannedLifecycleModule) Manifest(lang string) registry.Manifest {
	return registry.Manifest{Name: m.name, BackendReady: true}
}

func (m *fakePlannedLifecycleModule) PreflightInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}

func (m *fakePlannedLifecycleModule) PlanInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}

func (m *fakePlannedLifecycleModule) ValidateInstall(ctx context.Context, req registry.InstallRequest, resources []store.Resource) error {
	return nil
}

func (m *fakePlannedLifecycleModule) Install(ctx context.Context, req registry.InstallRequest, run registry.RunContext) error {
	m.installCalls++
	if m.installStarted != nil {
		select {
		case m.installStarted <- struct{}{}:
		default:
		}
	}
	if m.installRelease != nil {
		select {
		case <-m.installRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (m *fakePlannedLifecycleModule) PlanDelete(ctx context.Context, req registry.DeleteRequest) ([]registry.InstallStepPlan, error) {
	return []registry.InstallStepPlan{{Target: req.Server.ID, Name: "delete", Title: "Delete demo", Order: 1}}, nil
}

func (m *fakePlannedLifecycleModule) Delete(ctx context.Context, req registry.DeleteRequest, run registry.RunContext) error {
	m.deleteCalls++
	return nil
}

func (m *fakePlannedLifecycleModule) PlanCheck(ctx context.Context, req registry.CheckRequest) ([]registry.InstallStepPlan, error) {
	return []registry.InstallStepPlan{{Target: req.Server.ID, Name: "check", Title: "Check demo", Order: 1}}, nil
}

func (m *fakePlannedLifecycleModule) Check(ctx context.Context, req registry.CheckRequest, run registry.RunContext) (registry.InstanceStatus, error) {
	m.checkCalls++
	return registry.InstanceStatus{Status: "healthy"}, nil
}

func (m *fakePlannedLifecycleModule) EstimateStorageCleanup(ctx context.Context, req registry.StorageCleanupEstimateRequest, run registry.RunContext) (registry.StorageCleanupEstimateResult, error) {
	m.cleanupEstimateCalls++
	m.lastCleanupRetentionDays = req.RetentionDays
	return registry.StorageCleanupEstimateResult{
		Status:        "available",
		RetentionDays: req.RetentionDays,
		ObjectCount:   3,
		Bytes:         2048,
		Source:        "test",
	}, nil
}

func (m *fakePlannedLifecycleModule) ApplyStorageCleanupPolicy(ctx context.Context, req registry.StorageCleanupPolicyRequest, run registry.RunContext) (registry.StorageCleanupPolicyResult, error) {
	m.cleanupPolicyCalls++
	m.lastCleanupPolicyBucket = req.Bucket
	m.lastCleanupPolicyPrefix = req.Prefix
	m.lastCleanupPolicyRetentionDays = req.RetentionDays
	return registry.StorageCleanupPolicyResult{
		Status:        "enabled",
		Bucket:        req.Bucket,
		Prefix:        req.Prefix,
		RetentionDays: req.RetentionDays,
		RuleID:        "rule-test",
		Source:        "test",
	}, nil
}

func (m *fakePlannedLifecycleModule) PlanClusterStart(ctx context.Context, req registry.ClusterStartRequest) ([]registry.InstallStepPlan, error) {
	steps := make([]registry.InstallStepPlan, 0, len(req.Servers))
	for index, server := range req.Servers {
		steps = append(steps, registry.InstallStepPlan{Target: server.ID, Name: "cluster-start", Title: "Start cluster", Order: index + 1})
	}
	return steps, nil
}

func (m *fakePlannedLifecycleModule) StartCluster(ctx context.Context, req registry.ClusterStartRequest, run registry.RunContext) error {
	m.clusterStartCalls++
	return nil
}
