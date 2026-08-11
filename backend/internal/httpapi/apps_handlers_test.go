package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func TestAIFARArtifactUpdateRequiresPositiveExpectedGeneration(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"INVALID_AIFAR_ARTIFACT_EXPECTED_GENERATION"`) {
		t.Fatalf("expected strict expectedGeneration rejection, got status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAIFARArtifactBundleAndRollbackRejectLegacyModelWithLocalizedMigrationContract(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	instance.Metadata = `{"orchestrationModel":"agent-runtime-v2","installRoot":"/aifar/apps/admin"}`
	if _, err := db.SaveAppInstance(instance); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	wantMessages := map[string]string{
		"en": "This AIFAR instance has not migrated to agent-service-controller-v1; complete the runtime migration first.",
		"zh": "当前 AIFAR 实例尚未迁移到 agent-service-controller-v1，请先完成运行时迁移。",
	}

	for _, language := range []string{"en", "zh"} {
		for _, entry := range []struct {
			name string
			path string
			body func() (io.Reader, string)
		}{
			{name: "single artifact", path: "/api/v2/apps/instances/" + instance.ID + "/aifar/update-artifact", body: func() (io.Reader, string) {
				return aifarArtifactMultipart(t, "1", "permission", "permission.jar")
			}},
			{name: "artifact bundle", path: "/api/v2/apps/instances/" + instance.ID + "/aifar/update-artifact-bundle", body: func() (io.Reader, string) {
				return aifarBundleMultipart(t, "runtime-bundle.zip")
			}},
			{name: "rollback", path: "/api/v2/apps/instances/" + instance.ID + "/aifar/rollback", body: func() (io.Reader, string) {
				return strings.NewReader(`{"targetReleaseId":"release-old","reason":"operator rollback"}`), "application/json"
			}},
		} {
			t.Run(language+"/"+entry.name, func(t *testing.T) {
				body, contentType := entry.body()
				req := httptest.NewRequest(http.MethodPost, entry.path, body)
				req.Header.Set("Authorization", "Bearer "+token)
				req.Header.Set("Content-Type", contentType)
				req.Header.Set("X-AIFAR-Language", language)
				rec := httptest.NewRecorder()

				api.Router().ServeHTTP(rec, req)

				assertAIFARRuntimeMigrationRequired(t, rec, instance.ID, wantMessages[language])
			})
		}
	}
}

func TestAIFARArtifactUpdateRejectsEmptyAndUnknownModelsWithMigrationContract(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	const wantMessage = "This AIFAR instance has not migrated to agent-service-controller-v1; complete the runtime migration first."
	for _, model := range []struct {
		name     string
		metadata string
	}{
		{name: "empty", metadata: `{"installRoot":"/aifar/apps/admin"}`},
		{name: "unknown", metadata: `{"orchestrationModel":"unknown-controller","installRoot":"/aifar/apps/admin"}`},
	} {
		t.Run(model.name, func(t *testing.T) {
			instance.Metadata = model.metadata
			if _, err := db.SaveAppInstance(instance); err != nil {
				t.Fatal(err)
			}
			body, contentType := aifarArtifactMultipart(t, "1", "permission", "permission.jar")
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("X-AIFAR-Language", "en")
			rec := httptest.NewRecorder()

			api.Router().ServeHTTP(rec, req)

			assertAIFARRuntimeMigrationRequired(t, rec, instance.ID, wantMessage)
		})
	}
}

func TestAIFARArtifactUpdateUsesPrestartServiceLockAndReportsOnlyOwnerTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	if _, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "permission", Operation: "reconcile",
		Actor: "other", TaskID: "task-owner", ExpiresAt: time.Now().UTC().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "1", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected service-lock conflict, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		Code    string         `json:"code"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Code != "AIFAR_RUNTIME_SERVICE_LOCKED" || len(response.Details) != 1 || response.Details["ownerTaskId"] != "task-owner" {
		t.Fatalf("unexpected stable conflict response: %+v", response)
	}
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("pre-start conflict must clean the unstarted task, got %+v", tasks)
	}
}

func TestAIFARArtifactUpdateRevalidatesGenerationAfterPrestartLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "2", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"AIFAR_RUNTIME_DEPLOYMENT_GENERATION_CONFLICT"`) {
		t.Fatalf("expected locked fresh-generation rejection, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 || len(tasks) != 0 {
		t.Fatalf("stale pre-start request leaked task or lock: tasks=%+v locks=%+v", tasks, locks)
	}
}

func TestAIFARArtifactUpdateAllowsPeerLockAndLifecycleReleasesOnlyOwnedLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	peer, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "file", Operation: "update-artifact",
		Actor: "other", TaskID: "task-file", ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	var capturedLifecycle worker.TaskLifecycle
	api.runtime.startExistingTaskWithLifecycle = func(task store.Task, _ string, _ worker.Job, lifecycle worker.TaskLifecycle) (store.Task, error) {
		capturedLifecycle = lifecycle
		return task, nil
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "1", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted || capturedLifecycle.Start == nil || capturedLifecycle.Finish == nil {
		t.Fatalf("expected accepted task with lock lifecycle, status=%d body=%s", rec.Code, rec.Body.String())
	}
	active, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 2 {
		t.Fatalf("peer service lock must not block target service lock: %+v", active)
	}
	genericLocks, err := db.ListOperationLocks("app-instance", instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(genericLocks) != 0 {
		t.Fatalf("AIFAR single-service upload must not acquire a generic instance lock: %+v", genericLocks)
	}
	lifecycleCtx, cancelLifecycle := context.WithCancel(context.Background())
	capturedLifecycle.Start(lifecycleCtx, cancelLifecycle)
	if err := capturedLifecycle.Finish(); err != nil {
		t.Fatal(err)
	}
	active, err = db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != peer.ID {
		t.Fatalf("task lifecycle must release only its exact target lock: %+v", active)
	}
}

func TestAIFARArtifactUpdateQueuedCancellationRemovesExactTemporaryArtifact(t *testing.T) {
	module := &fakeArtifactUpdateModule{fakePlannedLifecycleModule: &fakePlannedLifecycleModule{name: "aifar"}}
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(module)
	api.tasks = worker.NewManagerWithConcurrency(db, 1)
	holderStarted := make(chan struct{})
	holderRelease := make(chan struct{}, 1)
	t.Cleanup(func() {
		select {
		case holderRelease <- struct{}{}:
		default:
		}
	})
	holderTask, err := api.tasks.Start("test.artifact-slot-holder", "holder", "owner", func(ctx context.Context, log worker.Logger) error {
		close(holderStarted)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-holderRelease:
			return nil
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-holderStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("slot holder did not start")
	}
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "1", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted queued upload, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	artifactPath := module.artifactPath
	if artifactPath == "" {
		t.Fatal("handler did not pass its exact temporary artifact path to validation")
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("queued artifact must exist before cancellation: %v", err)
	}
	if !api.tasks.Cancel(taskID) {
		t.Fatal("expected queued artifact task cancellation")
	}
	waitForTaskStatus(t, db, taskID, "cancelled")
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("queued cancellation leaked exact temporary artifact %q: %v", artifactPath, err)
	}
	holderRelease <- struct{}{}
	waitForTaskStatus(t, db, holderTask.ID, "success")
}

func TestAIFARArtifactUpdateSuccessRemovesExactTemporaryArtifact(t *testing.T) {
	module := &fakeArtifactUpdateModule{fakePlannedLifecycleModule: &fakePlannedLifecycleModule{name: "aifar"}}
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(module)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "1", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected accepted upload, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	waitForTaskStatus(t, db, taskID, "success")
	if module.artifactPath == "" {
		t.Fatal("module did not receive exact temporary artifact path")
	}
	if _, err := os.Stat(module.artifactPath); !os.IsNotExist(err) {
		t.Fatalf("successful lifecycle leaked exact temporary artifact %q: %v", module.artifactPath, err)
	}
}

func TestAIFARArtifactUpdateStartFailureFinishesLifecycleAndRemovesTemporaryArtifact(t *testing.T) {
	module := &fakeArtifactUpdateModule{fakePlannedLifecycleModule: &fakePlannedLifecycleModule{name: "aifar"}}
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(module)
	var capturedLifecycle worker.TaskLifecycle
	api.runtime.startExistingTaskWithLifecycle = func(task store.Task, _ string, _ worker.Job, lifecycle worker.TaskLifecycle) (store.Task, error) {
		capturedLifecycle = lifecycle
		return task, errors.New("start failed")
	}
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	token := issueTestToken(t, db, secret, "owner", "owner")
	body, contentType := aifarArtifactMultipart(t, "1", "permission", "permission.jar")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/aifar/update-artifact", body)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", contentType)
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code == http.StatusAccepted || capturedLifecycle.Finish == nil {
		t.Fatalf("expected synchronous start failure with lifecycle, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if module.artifactPath == "" {
		t.Fatal("module did not receive exact temporary artifact path")
	}
	if _, err := os.Stat(module.artifactPath); !os.IsNotExist(err) {
		t.Fatalf("start failure leaked exact temporary artifact %q: %v", module.artifactPath, err)
	}
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 || len(tasks) != 0 {
		t.Fatalf("start failure leaked task or exact lock: tasks=%+v locks=%+v", tasks, locks)
	}
	finishDone := make(chan error, 1)
	go func() { finishDone <- capturedLifecycle.Finish() }()
	select {
	case err := <-finishDone:
		if err != nil {
			t.Fatalf("handler must leave lifecycle finish idempotent: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("idempotent lifecycle finish blocked after synchronous start failure")
	}
}

func TestRuntimeDeploymentLifecycleFinishBeforeStartCleansOnlyExactTemporaryArtifact(t *testing.T) {
	api, db, _ := newAuthzTestAPI(t)
	_, instance := seedAIFARRuntimeFixture(t, db, "unix:///var/run/docker.sock")
	lock, err := db.AcquireAIFAROrchestrationLock(store.AIFAROrchestrationLock{
		InstanceID: instance.ID, ServiceName: "permission", Operation: "update-artifact",
		Actor: "owner", TaskID: "task-start-failure", ExpiresAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	artifactPath := filepath.Join(dir, "aifar-artifact-exact.jar")
	neighborPath := filepath.Join(dir, "aifar-artifact-neighbor.jar")
	if err := os.WriteFile(artifactPath, []byte("artifact"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neighborPath, []byte("neighbor"), 0o600); err != nil {
		t.Fatal(err)
	}
	lifecycle := api.runtime.runtimeDeploymentLockLifecycle(lock, "en", artifactPath)

	if err := lifecycle.Finish(); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Finish(); err != nil {
		t.Fatalf("lifecycle cleanup must be idempotent: %v", err)
	}
	if _, err := os.Stat(artifactPath); !os.IsNotExist(err) {
		t.Fatalf("exact temporary artifact was not removed: %v", err)
	}
	if _, err := os.Stat(neighborPath); err != nil {
		t.Fatalf("exact cleanup removed or damaged neighboring path: %v", err)
	}
	locks, err := db.ListAIFAROrchestrationLocks(instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("pre-start lifecycle finish leaked service lock: %+v", locks)
	}
}

func aifarArtifactMultipart(t *testing.T, expectedGeneration, serviceName, fileName string) (*bytes.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if expectedGeneration != "" {
		if err := writer.WriteField("expectedGeneration", expectedGeneration); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.WriteField("service", serviceName); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("artifact", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("test artifact")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType()
}

func aifarBundleMultipart(t *testing.T, fileName string) (*bytes.Reader, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("bundle", fileName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("legacy requests must be rejected before bundle parsing")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(body.Bytes()), writer.FormDataContentType()
}

func assertAIFARRuntimeMigrationRequired(t *testing.T, rec *httptest.ResponseRecorder, instanceID, wantMessage string) {
	t.Helper()
	var response struct {
		Code    string         `json:"code"`
		Message string         `json:"message"`
		Details map[string]any `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode migration response: %v body=%s", err, rec.Body.String())
	}
	if rec.Code != http.StatusConflict || response.Code != "AIFAR_RUNTIME_MIGRATION_REQUIRED" || response.Message != wantMessage {
		t.Fatalf("unexpected migration response: status=%d response=%+v", rec.Code, response)
	}
	if len(response.Details) != 1 || response.Details["instanceId"] != instanceID {
		t.Fatalf("migration response leaked or omitted details: %+v", response.Details)
	}
}

func TestDecodeMySQLBackupRequestAcceptsOnlyPositiveKeepLastOverride(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/instance-1/backup", strings.NewReader(`{"name":"nightly","threads":4,"maxRateMBps":64,"keepLast":8,"schemas":["orders","billing"]}`))
	rec := httptest.NewRecorder()
	body, ok := decodeMySQLBackupRequest(rec, req)
	if !ok {
		t.Fatalf("expected valid request, got status=%d body=%s", rec.Code, rec.Body.String())
	}
	if body.Name != "nightly" || body.Threads != 4 || body.MaxRateMBps != 64 || body.KeepLast == nil || *body.KeepLast != 8 || len(body.Schemas) != 2 {
		t.Fatalf("unexpected decoded request: %#v", body)
	}

	omittedReq := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/instance-1/backup", strings.NewReader(`{"name":"default-policy","schemas":["orders"]}`))
	omittedRec := httptest.NewRecorder()
	omitted, ok := decodeMySQLBackupRequest(omittedRec, omittedReq)
	if !ok {
		t.Fatalf("expected omitted keepLast to be valid, got status=%d body=%s", omittedRec.Code, omittedRec.Body.String())
	}
	if omitted.KeepLast != nil {
		t.Fatalf("omitted keepLast must remain nil for handler-level fallback, got %v", *omitted.KeepLast)
	}
}

func TestDecodeMySQLBackupRequestRejectsRepositoryDirAndNonPositiveKeepLast(t *testing.T) {
	tests := []string{
		`{"repositoryDir":"/tmp/user-controlled"}`,
		`{"keepLast":0}`,
		`{"keepLast":-1}`,
		`{`,
		`{"name":"first"}{"name":"second"}`,
		`{"name":"first"}{"repositoryDir":"/tmp/user-controlled"}`,
		`null`,
		`{"unknown":true}`,
		`{"name":"nightly","schemas":[]}`,
		`{"name":"nightly"}`,
	}
	for _, body := range tests {
		t.Run(body, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/instance-1/backup", strings.NewReader(body))
			rec := httptest.NewRecorder()
			if _, ok := decodeMySQLBackupRequest(rec, req); ok {
				t.Fatalf("expected request rejection for %s", body)
			}
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"code":"INVALID_JSON"`) {
				t.Fatalf("expected INVALID_JSON 400, got status=%d body=%s", rec.Code, rec.Body.String())
			}
		})
	}
}

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
	}, map[string]any{"kind": "rollout", "changedServices": []any{"oauth"}}, registry.ArtifactRollbackInspection{})
	if _, exists := failed["activatedAt"]; exists {
		t.Fatalf("failed release must not expose a zero activation time: %+v", failed)
	}

	activatedAt := time.Now().UTC()
	success := aifarReleaseResponseItem(store.AppRelease{
		ID: "rel-success", InstanceID: "aifar-1", App: "aifar", Version: "runtime-v2",
		ReleaseID: "release-success", Status: "success", CreatedAt: activatedAt, ActivatedAt: activatedAt,
	}, map[string]any{"kind": "rollout", "changedServices": []any{"oauth", "gateway"}}, registry.ArtifactRollbackInspection{
		CurrentServices:  []string{"oauth"},
		RollbackServices: []string{"gateway"},
	})
	if got, exists := success["activatedAt"]; !exists || got != activatedAt {
		t.Fatalf("successful release must expose activation time: %+v", success)
	}
	if got := success["currentServices"]; !reflect.DeepEqual(got, []string{"oauth"}) {
		t.Fatalf("current services = %#v, want [oauth]", got)
	}
	if got := success["rollbackServices"]; !reflect.DeepEqual(got, []string{"gateway"}) {
		t.Fatalf("rollback services = %#v, want [gateway]", got)
	}
	if got := success["rollbackAvailable"]; got != true {
		t.Fatalf("rollback available = %#v, want true", got)
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

// Production break caught: failed InnoDB Cluster installation placeholders do
// not have authoritative app_clusters rows, but selecting the complete failed
// group must still create and run the cleanup task.
func TestBatchDeleteAllowsCompleteFailedMySQLClusterCleanup(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "mysql"}
	api.apps = registry.New(module)
	const installTaskID = "tsk_1234567890abcdef12345678"
	clusterID := "mysql-failed-" + installTaskID
	passwords := map[string]string{}
	instanceIDs := make([]string, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{
			Name: fmt.Sprintf("mysql-failed-%d", index+1), Host: fmt.Sprintf("10.0.1.%d", index+1),
			Username: "root", Password: "server-pass",
		})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{
			App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "failed", Topology: "innodb-cluster",
			Metadata: fmt.Sprintf(`{"installFailed":true,"taskId":%q,"clusterId":%q,"topology":"innodb-cluster","port":3306}`, installTaskID, clusterID),
		})
		if err != nil {
			t.Fatal(err)
		}
		passwords[server.ID] = "server-pass"
		instanceIDs = append(instanceIDs, instance.ID)
	}
	body, _ := json.Marshal(map[string]any{"instanceIds": instanceIDs, "serverPasswords": passwords})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/batch-delete", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected failed cluster cleanup to be accepted, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	waitForTaskStatus(t, db, taskID, "success")
	if module.deleteCalls != 3 {
		t.Fatalf("expected all three failed placeholders to be cleaned, got %d delete calls", module.deleteCalls)
	}
}

// Production break caught: a batch-aware application must validate once
// before the worker starts and every per-item delete must receive the same
// immutable selected-instance scope.
func TestBatchDeletePreflightsAndFreezesSelectedScope(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	base := &fakePlannedLifecycleModule{name: "demo"}
	module := &fakeBatchDeleteModule{fakePlannedLifecycleModule: base}
	api.apps = registry.New(module)
	passwords := map[string]string{}
	ids := make([]string, 0, 2)
	for index := 0; index < 2; index++ {
		server, err := db.SaveServer(store.Server{Name: fmt.Sprintf("demo-%d", index), Host: fmt.Sprintf("10.0.2.%d", index+1), Username: "root", Password: "server-pass"})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{App: "demo", Version: "1.0.0", ServerID: server.ID, Status: "installed"})
		if err != nil {
			t.Fatal(err)
		}
		passwords[server.ID] = "server-pass"
		ids = append(ids, instance.ID)
	}
	body, _ := json.Marshal(map[string]any{"instanceIds": ids, "serverPasswords": passwords})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/batch-delete", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	waitForTaskStatus(t, db, decodeTaskID(t, rec), "success")
	if module.preflightCalls != 1 || base.deleteCalls != 2 {
		t.Fatalf("preflightCalls=%d deleteCalls=%d", module.preflightCalls, base.deleteCalls)
	}
	if len(base.deleteScopes) != 2 || !reflect.DeepEqual(base.deleteScopes[0], ids) || !reflect.DeepEqual(base.deleteScopes[1], ids) {
		t.Fatalf("delete scopes=%v want=%v", base.deleteScopes, ids)
	}
}

// Production break caught: using per-instance app locks lets a batch delete
// overlap a cluster backup/restore held on the authoritative cluster lock.
func TestDeleteMySQLClusterInstancesUseRawClusterMutationLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(&fakePlannedLifecycleModule{name: "mysql"})
	clusterID := "cluster_1234567890abcdef12345678"
	servers, instances := saveHTTPTestMySQLCluster(t, db, clusterID, "server-pass")
	ownerTask, err := db.CreateTask(store.Task{Type: "apps.mysql.backup", Target: instances[0].ID, Status: "running", CreatedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AcquireOperationLock(store.OperationLock{Scope: "app-cluster", ResourceID: clusterID, Operation: operationLockMutation, OwnerTaskID: ownerTask.ID, Owner: "owner", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	passwords := map[string]string{}
	ids := make([]string, 0, len(instances))
	for index := range instances {
		ids = append(ids, instances[index].ID)
		passwords[servers[index].ID] = "server-pass"
	}
	body, _ := json.Marshal(map[string]any{"instanceIds": ids, "serverPasswords": passwords})
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/batch-delete", strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"OPERATION_LOCKED"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

// Production break caught: a cluster instance without a raw clusterId must
// not fall back to an unlocked single-instance deletion.
func TestDeleteMySQLClusterInstanceRejectsMissingRawClusterIDBeforeTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakePlannedLifecycleModule{name: "mysql"}
	api.apps = registry.New(module)
	server, err := db.SaveServer(store.Server{Name: "mysql-1", Host: "10.0.0.9", Username: "root", Password: "server-pass"})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := db.SaveAppInstance(store.AppInstance{App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster", Metadata: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/delete", strings.NewReader(`{"serverPassword":"server-pass"}`))
	req.Header.Set("Authorization", "Bearer "+issueTestToken(t, db, secret, "owner", "owner"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"MYSQL_BACKUP_CLUSTER_UNHEALTHY"`) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	tasks, err := db.ListTasks()
	if err != nil || len(tasks) != 0 || module.deleteCalls != 0 {
		t.Fatalf("invalid cluster deletion created work: tasks=%+v deleteCalls=%d err=%v", tasks, module.deleteCalls, err)
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

func TestCheckAppInstanceUsesMutationLockOnlyForMySQL(t *testing.T) {
	for _, test := range []struct {
		app        string
		wantStatus int
	}{
		{app: "mysql", wantStatus: http.StatusConflict},
		{app: "demo", wantStatus: http.StatusAccepted},
	} {
		t.Run(test.app, func(t *testing.T) {
			api, db, secret := newAuthzTestAPI(t)
			module := &fakePlannedLifecycleModule{name: test.app}
			api.apps = registry.New(module)
			server, err := db.SaveServer(store.Server{Name: test.app + "-1", Host: "10.0.0.9", Username: "root"})
			if err != nil {
				t.Fatal(err)
			}
			instance, err := db.SaveAppInstance(store.AppInstance{App: test.app, Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "standalone", Metadata: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			ownerTask, err := db.CreateTask(store.Task{Type: "apps.mysql.restore", Target: instance.ID, Status: "running", CreatedBy: "owner"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := db.AcquireOperationLock(store.OperationLock{Scope: "app-instance", ResourceID: instance.ID, Operation: operationLockMutation, OwnerTaskID: ownerTask.ID, Owner: "owner", ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
			token := issueTestToken(t, db, secret, "owner", "owner")
			req := httptest.NewRequest(http.MethodPost, "/api/v2/apps/instances/"+instance.ID+"/check", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			rec := httptest.NewRecorder()
			api.Router().ServeHTTP(rec, req)
			if rec.Code != test.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if test.app == "mysql" && !strings.Contains(rec.Body.String(), `"code":"OPERATION_LOCKED"`) {
				t.Fatalf("body=%s", rec.Body.String())
			}
		})
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
	_, instances := saveHTTPTestMySQLCluster(t, db, "cluster_1234567890abcdef12345678", "")
	token := issueTestToken(t, db, secret, "owner", "owner")

	body := `{"instanceIds":["` + instances[0].ID + `","` + instances[1].ID + `","` + instances[2].ID + `"]}`
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
	if len(steps) != 3 || steps[0].Name != "cluster-start" || steps[1].Name != "cluster-start" || steps[2].Name != "cluster-start" {
		t.Fatalf("expected pre-stored cluster plan steps, got %+v", steps)
	}
	waitForTaskStatus(t, db, taskID, "success")
	if module.clusterStartCalls != 1 {
		t.Fatalf("expected cluster start module call, got %d", module.clusterStartCalls)
	}
}

func saveHTTPTestMySQLCluster(t *testing.T, db *store.Store, clusterID, password string) ([]store.Server, []store.AppInstance) {
	t.Helper()
	if _, err := db.SaveAppCluster(store.AppCluster{ID: clusterID, App: "mysql", Name: "test", Topology: "innodb-cluster", Status: "active"}); err != nil {
		t.Fatal(err)
	}
	servers := make([]store.Server, 0, 3)
	instances := make([]store.AppInstance, 0, 3)
	for index := 0; index < 3; index++ {
		server, err := db.SaveServer(store.Server{Name: fmt.Sprintf("mysql-%d", index+1), Host: fmt.Sprintf("10.0.0.%d", index+1), Username: "root", Password: password})
		if err != nil {
			t.Fatal(err)
		}
		instance, err := db.SaveAppInstance(store.AppInstance{
			App: "mysql", Version: "8.0.36", ServerID: server.ID, Status: "installed", Topology: "innodb-cluster",
			Metadata: `{"clusterId":"` + clusterID + `"}`,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.SaveAppClusterMember(store.AppClusterMember{ClusterID: clusterID, InstanceID: instance.ID, ServerID: server.ID, Role: "SECONDARY", Status: "ONLINE"}); err != nil {
			t.Fatal(err)
		}
		servers = append(servers, server)
		instances = append(instances, instance)
	}
	return servers, instances
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
	deleteScopes                   [][]string
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

type fakeArtifactUpdateModule struct {
	*fakePlannedLifecycleModule
	artifactPath string
}

func (m *fakeArtifactUpdateModule) ValidateArtifactUpdate(_ context.Context, req registry.ArtifactUpdateRequest) error {
	m.artifactPath = req.ArtifactLocalPath
	return nil
}

func (m *fakeArtifactUpdateModule) PlanArtifactUpdate(_ context.Context, req registry.ArtifactUpdateRequest) ([]registry.InstallStepPlan, error) {
	return []registry.InstallStepPlan{{Target: req.Instance.ID + ":" + req.ServiceName, Name: "update-artifact", Title: "Update artifact", Order: 1}}, nil
}

func (m *fakeArtifactUpdateModule) UpdateArtifact(_ context.Context, req registry.ArtifactUpdateRequest, _ registry.RunContext) error {
	m.artifactPath = req.ArtifactLocalPath
	return nil
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
	if req.Batch != nil {
		m.deleteScopes = append(m.deleteScopes, req.Batch.IDs())
	}
	return nil
}

type fakeBatchDeleteModule struct {
	*fakePlannedLifecycleModule
	preflightCalls int
}

func (m *fakeBatchDeleteModule) PreflightDeleteBatch(ctx context.Context, requests []registry.DeleteRequest) error {
	m.preflightCalls++
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
