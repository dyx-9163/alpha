package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"aifar-deployment/backend/internal/apps/registry"
	"aifar-deployment/backend/internal/store"
	"aifar-deployment/backend/internal/worker"
)

func TestRuntimeDiagnosticsCreateReturnsTaskAndExportIDs(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	release := make(chan struct{})
	module := &fakeRuntimeDiagnosticsModule{
		db: db, exportRelease: release,
		estimate: registry.RuntimeDiagnosticEstimateResult{Allowed: true},
	}
	api.apps = registry.New(module)
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	token := issueTestToken(t, db, secret, "owner", "owner")
	now := time.Now().UTC().Truncate(time.Second)

	req := runtimeDiagnosticRequest(t, http.MethodPost, "/api/v2/containers/aifar/runtime/diagnostics/exports?serverId="+server.ID, token,
		instance.ID, now.Add(-2*time.Hour), now.Add(-time.Hour), []string{"permission"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	var response struct {
		TaskID   string `json:"taskId"`
		ExportID string `json:"exportId"`
		Status   string `json:"status"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TaskID == "" || response.ExportID == "" || response.Status != "pending" {
		t.Fatalf("unexpected response: %+v", response)
	}

	export, err := db.GetDiagnosticExport(response.ExportID)
	if err != nil {
		t.Fatal(err)
	}
	if export.TaskID != response.TaskID || export.InstanceID != instance.ID || export.ServerID != server.ID || export.Status != "pending" || export.CreatedBy != "owner" {
		t.Fatalf("unexpected pending export: %+v", export)
	}
	if len(export.Services) != 1 || export.Services[0] != "permission" || !export.SinceAt.Equal(now.Add(-2*time.Hour)) || !export.UntilAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("unexpected export selection: %+v", export)
	}

	task, _, err := db.GetTask(response.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type != "aifar.runtime.diagnostics.export" || task.Target != instance.ID || task.CreatedBy != "owner" {
		t.Fatalf("unexpected export task: %+v", task)
	}
	wantSteps := []string{"load-instance", "validate-request", "estimate-size", "collect-file-logs", "collect-container-logs", "collect-diagnostics", "redact-and-manifest", "create-archive", "record-export"}
	assertRuntimeDiagnosticTaskPlan(t, db, response.TaskID, server.ID, wantSteps)

	locks, err := db.ListOperationLocks("runtime-diagnostics", instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 1 || locks[0].Operation != "export" || locks[0].OwnerTaskID != response.TaskID {
		t.Fatalf("expected exact per-instance export lock, got %+v", locks)
	}
	assertAuditExists(t, db, "containers.aifar.runtime.diagnostics.export", "running", "owner", instance.ID)

	close(release)
	waitForTaskStatus(t, db, response.TaskID, "success")
}

func TestRuntimeDiagnosticsRoutesRequireAppsManage(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "viewer", "viewer")
	for _, tc := range []struct {
		method string
		path   string
	}{
		{http.MethodPost, "/api/v2/containers/aifar/runtime/diagnostics/estimate?serverId=srv-1"},
		{http.MethodPost, "/api/v2/containers/aifar/runtime/diagnostics/exports?serverId=srv-1"},
		{http.MethodGet, "/api/v2/containers/aifar/runtime/diagnostics/exports?serverId=srv-1&instanceId=app-1"},
		{http.MethodGet, "/api/v2/containers/aifar/runtime/diagnostics/exports/diag-1/download?serverId=srv-1"},
		{http.MethodDelete, "/api/v2/containers/aifar/runtime/diagnostics/exports/diag-1?serverId=srv-1"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`))
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		api.Router().ServeHTTP(rec, req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("%s %s: expected 403, got %d body=%s", tc.method, tc.path, rec.Code, rec.Body.String())
		}
		assertRuntimeDiagnosticAPIError(t, rec)
	}
}

func TestRuntimeDiagnosticsCreateStartFailureReleasesLockAndDeletesUnusedTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(&fakeRuntimeDiagnosticsModule{db: db, estimate: registry.RuntimeDiagnosticEstimateResult{Allowed: true}})
	api.runtime.startExistingTask = func(task store.Task, _ string, _ worker.Job) (store.Task, error) {
		return task, errors.New("injected task start failure")
	}
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	token := issueTestToken(t, db, secret, "owner", "owner")
	now := time.Now().UTC().Truncate(time.Second)

	req := runtimeDiagnosticRequest(t, http.MethodPost, "/api/v2/containers/aifar/runtime/diagnostics/exports?serverId="+server.ID, token,
		instance.ID, now.Add(-2*time.Hour), now.Add(-time.Hour), []string{"permission"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	assertRuntimeDiagnosticAPIError(t, rec)
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("start failure must delete the unused task, got %+v", tasks)
	}
	locks, err := db.ListOperationLocks("runtime-diagnostics", instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("start failure must release the export lock, got %+v", locks)
	}
	page, err := db.ListDiagnosticExports(instance.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.Items[0].Status != "failed" || page.Items[0].TaskID != "" || page.Items[0].CleanupStatus != "complete" {
		t.Fatalf("start failure must leave one reachable failed record without a dangling task id, got %+v", page.Items)
	}
}

func TestRuntimeDiagnosticsCreatePlanFailureLeavesNoTaskExportOrLock(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	api.apps = registry.New(&fakeRuntimeDiagnosticsModule{db: db, estimate: registry.RuntimeDiagnosticEstimateResult{Allowed: true}})
	api.runtime.storeDiagnosticTaskPlan = func(string, string, []simpleTaskStep) error {
		return errors.New("injected task plan failure")
	}
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	token := issueTestToken(t, db, secret, "owner", "owner")
	now := time.Now().UTC().Truncate(time.Second)

	req := runtimeDiagnosticRequest(t, http.MethodPost, "/api/v2/containers/aifar/runtime/diagnostics/exports?serverId="+server.ID, token,
		instance.ID, now.Add(-2*time.Hour), now.Add(-time.Hour), []string{"permission"})
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d body=%s", rec.Code, rec.Body.String())
	}
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 0 {
		t.Fatalf("plan failure must delete the unused task, got %+v", tasks)
	}
	page, err := db.ListDiagnosticExports(instance.ID, 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 0 {
		t.Fatalf("plan failure must not create an export record, got %+v", page.Items)
	}
	locks, err := db.ListOperationLocks("runtime-diagnostics", instance.ID, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(locks) != 0 {
		t.Fatalf("plan failure must not leave a lock, got %+v", locks)
	}
}

func TestRuntimeDiagnosticsEstimateAndListUseSelectedInstance(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeRuntimeDiagnosticsModule{db: db, estimate: registry.RuntimeDiagnosticEstimateResult{
		Services:  []registry.RuntimeDiagnosticServiceEstimate{{Service: "permission", FileBytes: 100, ContainerBytes: 200}},
		FileBytes: 100, ContainerBytes: 200, TotalBytes: 300, RequiredBytes: 900, AvailableBytes: 4096, Allowed: true,
	}}
	api.apps = registry.New(module)
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	token := issueTestToken(t, db, secret, "owner", "owner")
	now := time.Now().UTC().Truncate(time.Second)

	estimateReq := runtimeDiagnosticRequest(t, http.MethodPost, "/api/v2/containers/aifar/runtime/diagnostics/estimate?serverId="+server.ID, token,
		instance.ID, now.Add(-2*time.Hour), now.Add(-time.Hour), []string{"permission"})
	estimateRec := httptest.NewRecorder()
	api.Router().ServeHTTP(estimateRec, estimateReq)
	if estimateRec.Code != http.StatusOK {
		t.Fatalf("expected estimate 200, got %d body=%s", estimateRec.Code, estimateRec.Body.String())
	}
	var estimate registry.RuntimeDiagnosticEstimateResult
	if err := json.Unmarshal(estimateRec.Body.Bytes(), &estimate); err != nil {
		t.Fatal(err)
	}
	if estimate.TotalBytes != 300 || !estimate.Allowed || module.estimateRequest.Instance.ID != instance.ID || module.estimateRequest.Server.ID != server.ID {
		t.Fatalf("unexpected estimate: response=%+v request=%+v", estimate, module.estimateRequest)
	}

	export, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		InstanceID: instance.ID, ServerID: server.ID, Status: "pending", Services: []string{"permission"},
		SinceAt: now.Add(-2 * time.Hour), UntilAt: now.Add(-time.Hour), CreatedBy: "owner", CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), CleanupStatus: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	listReq := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime/diagnostics/exports?serverId="+server.ID+"&instanceId="+instance.ID+"&page=1&pageSize=10", nil)
	listReq.Header.Set("Authorization", "Bearer "+token)
	listRec := httptest.NewRecorder()
	api.Router().ServeHTTP(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("expected list 200, got %d body=%s", listRec.Code, listRec.Body.String())
	}
	var page store.DiagnosticExportPage
	if err := json.Unmarshal(listRec.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != export.ID || page.PageSize != 10 {
		t.Fatalf("unexpected diagnostic export page: %+v", page)
	}
}

func TestRuntimeDiagnosticDownloadDoesNotDeleteOnShortWrite(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeRuntimeDiagnosticsModule{db: db, streamData: []byte("0123456789"), streamLimit: 4, streamErr: io.ErrClosedPipe}
	api.apps = registry.New(module)
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	export := saveReadyRuntimeDiagnosticExport(t, db, server, instance, []byte("0123456789"))
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime/diagnostics/exports/"+export.ID+"/download?serverId="+server.ID+"&deleteAfterDownload=true", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != "0123" || strings.Contains(rec.Body.String(), `"code"`) {
		t.Fatalf("expected committed short binary response with no JSON, got code=%d body=%q", rec.Code, rec.Body.String())
	}
	assertRuntimeDiagnosticDownloadHeaders(t, rec, export)
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "ready" || !got.DownloadedAt.IsZero() || got.CleanupStatus != "none" {
		t.Fatalf("short download must remain ready and not downloaded: %+v", got)
	}
	assertNoRuntimeDiagnosticDeleteTask(t, db)
	if module.deleteCalls != 0 {
		t.Fatalf("short download must not call delete, got %d", module.deleteCalls)
	}
	assertAuditExists(t, db, "containers.aifar.runtime.diagnostics.download", "failed", "owner", export.ID)
}

func TestRuntimeDiagnosticDownloadQueuesDeleteOnlyAfterFullCopy(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	archive := []byte("complete-gzip-archive")
	module := &fakeRuntimeDiagnosticsModule{db: db, streamData: archive}
	api.apps = registry.New(module)
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	export := saveReadyRuntimeDiagnosticExport(t, db, server, instance, archive)
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime/diagnostics/exports/"+export.ID+"/download?serverId="+server.ID+"&deleteAfterDownload=true", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK || rec.Body.String() != string(archive) {
		t.Fatalf("unexpected download: code=%d body=%q", rec.Code, rec.Body.String())
	}
	assertRuntimeDiagnosticDownloadHeaders(t, rec, export)
	assertAuditExists(t, db, "containers.aifar.runtime.diagnostics.download", "success", "owner", export.ID)
	deleteTask := waitForRuntimeDiagnosticTaskType(t, db, "aifar.runtime.diagnostics.delete")
	if deleteTask.Target != instance.ID+":"+export.ID {
		t.Fatalf("unexpected post-download delete task: %+v", deleteTask)
	}
	waitForTaskStatus(t, db, deleteTask.ID, "success")
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "deleted" || got.DownloadedAt.IsZero() || got.DeletedAt.IsZero() || got.CleanupStatus != "complete" {
		t.Fatalf("expected downloaded and deleted export: %+v", got)
	}
	assertAuditExists(t, db, "containers.aifar.runtime.diagnostics.delete", "running", "owner", deleteTask.Target)
}

func TestRuntimeDiagnosticDownloadRejectsInvalidMetadataBeforeHeaders(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeRuntimeDiagnosticsModule{db: db, streamData: []byte("archive")}
	api.apps = registry.New(module)
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	export := saveReadyRuntimeDiagnosticExport(t, db, server, instance, []byte("archive"))
	export.SHA256 = "not-a-sha256"
	if _, err := db.SaveDiagnosticExport(export); err != nil {
		t.Fatal(err)
	}
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodGet, "/api/v2/containers/aifar/runtime/diagnostics/exports/"+export.ID+"/download?serverId="+server.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 before headers, got %d body=%s", rec.Code, rec.Body.String())
	}
	assertRuntimeDiagnosticAPIError(t, rec)
	if module.streamCalls != 0 {
		t.Fatalf("invalid metadata must not stream, got %d calls", module.streamCalls)
	}
}

func TestRuntimeDiagnosticManualDeleteCreatesAuditedWorker(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	module := &fakeRuntimeDiagnosticsModule{db: db}
	api.apps = registry.New(module)
	server, instance := seedAIFARRuntimeFixture(t, db, "http://docker.invalid")
	export := saveReadyRuntimeDiagnosticExport(t, db, server, instance, []byte("archive"))
	token := issueTestToken(t, db, secret, "owner", "owner")

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/containers/aifar/runtime/diagnostics/exports/"+export.ID+"?serverId="+server.ID, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String())
	}
	taskID := decodeTaskID(t, rec)
	task, _, err := db.GetTask(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if task.Type != "aifar.runtime.diagnostics.delete" || task.Target != instance.ID+":"+export.ID {
		t.Fatalf("unexpected delete task: %+v", task)
	}
	assertRuntimeDiagnosticTaskPlan(t, db, task.ID, task.Target, []string{"validate-export", "delete-remote-archive", "record-deletion"})
	waitForTaskStatus(t, db, taskID, "success")
	got, err := db.GetDiagnosticExport(export.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "deleted" || got.CleanupStatus != "complete" {
		t.Fatalf("expected deleted export: %+v", got)
	}
	assertAuditExists(t, db, "containers.aifar.runtime.diagnostics.delete", "running", "owner", task.Target)
}

type fakeRuntimeDiagnosticsModule struct {
	db *store.Store

	mu              sync.Mutex
	estimate        registry.RuntimeDiagnosticEstimateResult
	estimateRequest registry.RuntimeDiagnosticRequest
	exportRelease   <-chan struct{}
	exportErr       error
	streamData      []byte
	streamLimit     int
	streamErr       error
	streamCalls     int
	deleteCalls     int
	deleteErr       error
}

func (m *fakeRuntimeDiagnosticsModule) Name() string { return "aifar" }
func (m *fakeRuntimeDiagnosticsModule) Manifest(string) registry.Manifest {
	return registry.Manifest{Name: "aifar", BackendReady: true}
}
func (m *fakeRuntimeDiagnosticsModule) PreflightInstall(context.Context, registry.InstallRequest, []store.Resource) (registry.PreflightResult, error) {
	return registry.PreflightResult{}, nil
}
func (m *fakeRuntimeDiagnosticsModule) PlanInstall(context.Context, registry.InstallRequest, []store.Resource) ([]registry.InstallStepPlan, error) {
	return nil, nil
}
func (m *fakeRuntimeDiagnosticsModule) ValidateInstall(context.Context, registry.InstallRequest, []store.Resource) error {
	return nil
}
func (m *fakeRuntimeDiagnosticsModule) Install(context.Context, registry.InstallRequest, registry.RunContext) error {
	return nil
}
func (m *fakeRuntimeDiagnosticsModule) EstimateRuntimeDiagnostics(_ context.Context, req registry.RuntimeDiagnosticRequest, _ registry.RunContext) (registry.RuntimeDiagnosticEstimateResult, error) {
	m.mu.Lock()
	m.estimateRequest = req
	m.mu.Unlock()
	return m.estimate, nil
}
func (m *fakeRuntimeDiagnosticsModule) ExportRuntimeDiagnostics(ctx context.Context, _ registry.RuntimeDiagnosticRequest, _ registry.RunContext) error {
	if m.exportRelease != nil {
		select {
		case <-m.exportRelease:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return m.exportErr
}
func (m *fakeRuntimeDiagnosticsModule) DeleteRuntimeDiagnosticExport(ctx context.Context, req registry.RuntimeDiagnosticDeleteRequest, _ registry.RunContext) error {
	m.mu.Lock()
	m.deleteCalls++
	err := m.deleteErr
	m.mu.Unlock()
	if err != nil || m.db == nil {
		return err
	}
	now := time.Now().UTC()
	updated, updateErr := m.db.MarkDiagnosticExportCleanupPending(req.Export.ID, now)
	if updateErr != nil || !updated {
		return errors.New("failed to mark fake cleanup pending")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	updated, updateErr = m.db.MarkDiagnosticExportDeleted(req.Export.ID, now.Add(time.Millisecond))
	if updateErr != nil || !updated {
		return errors.New("failed to mark fake export deleted")
	}
	return nil
}
func (m *fakeRuntimeDiagnosticsModule) StreamRuntimeDiagnosticExport(_ context.Context, req registry.RuntimeDiagnosticStreamRequest, dst io.Writer) (int64, error) {
	m.mu.Lock()
	m.streamCalls++
	data, limit, streamErr := append([]byte(nil), m.streamData...), m.streamLimit, m.streamErr
	m.mu.Unlock()
	if limit > 0 && limit < len(data) {
		data = data[:limit]
	}
	written, err := dst.Write(data)
	if err != nil {
		return int64(written), err
	}
	if streamErr != nil {
		return int64(written), streamErr
	}
	if m.db != nil {
		updated, updateErr := m.db.MarkDiagnosticExportDownloaded(req.Export.ID, time.Now().UTC())
		if updateErr != nil || !updated {
			return int64(written), errors.New("failed to mark fake export downloaded")
		}
	}
	return int64(written), nil
}

func runtimeDiagnosticRequest(t *testing.T, method, path, token, instanceID string, sinceAt, untilAt time.Time, services []string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]any{"instanceId": instanceID, "sinceAt": sinceAt.Format(time.RFC3339), "untilAt": untilAt.Format(time.RFC3339), "services": services})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(method, path, strings.NewReader(string(body)))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	return req
}

func saveReadyRuntimeDiagnosticExport(t *testing.T, db *store.Store, server store.Server, instance store.AppInstance, archive []byte) store.DiagnosticExport {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	id := store.NewID("diag")
	name := "aifar-diagnostics-" + instance.ID + "-20260727T080000Z.tar.gz"
	export, err := db.SaveDiagnosticExport(store.DiagnosticExport{
		ID: id, InstanceID: instance.ID, ServerID: server.ID, Status: "ready", Services: []string{"permission"},
		SinceAt: now.Add(-2 * time.Hour), UntilAt: now.Add(-time.Hour), RemoteRelativePath: id + "/" + name,
		ArchiveName: name, ArchiveBytes: int64(len(archive)), UncompressedBytes: int64(len(archive) * 2), SHA256: strings.Repeat("a", 64),
		CreatedBy: "owner", CreatedAt: now.Add(-time.Hour), ReadyAt: now.Add(-30 * time.Minute), ExpiresAt: now.Add(time.Hour), CleanupStatus: "none",
	})
	if err != nil {
		t.Fatal(err)
	}
	return export
}

func assertRuntimeDiagnosticTaskPlan(t *testing.T, db *store.Store, taskID, target string, names []string) {
	t.Helper()
	steps, err := db.ListTaskSteps(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) != len(names) {
		t.Fatalf("expected %d steps, got %+v", len(names), steps)
	}
	for index, name := range names {
		if steps[index].Name != name || steps[index].Target != target {
			t.Fatalf("unexpected step %d: %+v", index, steps[index])
		}
	}
	targets, err := db.ListTaskTargets(taskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0].Target != target {
		t.Fatalf("expected one target %q, got %+v", target, targets)
	}
}

func assertRuntimeDiagnosticDownloadHeaders(t *testing.T, rec *httptest.ResponseRecorder, export store.DiagnosticExport) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/gzip" {
		t.Fatalf("unexpected content type %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != strconv.FormatInt(export.ArchiveBytes, 10) {
		t.Fatalf("unexpected content length %q", got)
	}
	if got := rec.Header().Get("X-AIFAR-Diagnostic-SHA256"); got != export.SHA256 {
		t.Fatalf("unexpected sha256 %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, export.ArchiveName) {
		t.Fatalf("unexpected content disposition %q", got)
	}
}

func assertRuntimeDiagnosticAPIError(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("expected JSON error, got %q: %v", rec.Body.String(), err)
	}
	if body["code"] == "" || body["message"] == "" {
		t.Fatalf("unexpected error body: %+v", body)
	}
}

func assertNoRuntimeDiagnosticDeleteTask(t *testing.T, db *store.Store) {
	t.Helper()
	tasks, err := db.ListTasks()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		if task.Type == "aifar.runtime.diagnostics.delete" {
			t.Fatalf("unexpected delete task: %+v", task)
		}
	}
}

func waitForRuntimeDiagnosticTaskType(t *testing.T, db *store.Store, taskType string) store.Task {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		tasks, err := db.ListTasks()
		if err != nil {
			t.Fatal(err)
		}
		for _, task := range tasks {
			if task.Type == taskType {
				return task
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("task type %s was not created", taskType)
	return store.Task{}
}
