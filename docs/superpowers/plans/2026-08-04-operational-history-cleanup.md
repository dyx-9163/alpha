# Operational History Cleanup Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a manual “清理运行历史” action that safely removes expired status history and noisy alert update events without changing current monitoring or alert state.

**Architecture:** Add fixed-semantics, context-aware batch delete methods to the SQLite Store, then orchestrate adaptive short transactions in the maintenance Service. Expose the operation as a permission-protected worker task guarded by an existing operation lock, and add a separately confirmed button to the current data-maintenance panel. The current retention cleanup remains unchanged; `aifar-agent` is untouched.

**Tech Stack:** Go 1.24, `database/sql`, `modernc.org/sqlite`, Chi, existing worker/operation-lock/audit infrastructure, Vue 3, TypeScript, Element Plus, Vitest.

## Global Constraints

- Delete only `status_snapshot_history.created_at < cutoff` and `alert_events.event = 'updated' AND created_at < cutoff`.
- Use a fixed seven-day retention cutoff computed once when the task starts.
- Never delete from `status_snapshots`, `alerts`, `collector_runs`, task/audit tables, application records, releases, backups, credentials, or settings.
- Keep alert lifecycle events such as `created`, `acknowledged`, `muted`, `unmuted`, and `resolved`.
- Use short independent transactions; initial batch size is 100 rows and the hard maximum is 500 rows.
- Do not use `OFFSET`, a full-table `COUNT(*)`, a startup-time large index migration, `VACUUM`, or `VACUUM INTO`.
- The cleanup must be cancellable, idempotent on rerun, self-mutually-exclusive, and must not terminate `aifar-server` on failure.
- Do not change `aifar-agent` or any remote-node behavior.
- All new user-visible backend and frontend text must have Chinese and English translations.
- HTTP errors keep the `{ code, message, details }` contract and must not expose SQL or database paths.
- Before editing `web/src`, read `design/ant-design-system-portable202606.md` completely.

## File Structure

- Create `backend/internal/store/operational_history_cleanup.go`: fixed-scope status-history and alert-update batch delete primitives.
- Create `backend/internal/store/operational_history_cleanup_test.go`: cutoff, scope, batch-bound, cancellation, and rerun coverage.
- Modify `backend/internal/maintenance/service.go`: cleanup result/progress types, adaptive batching, cancellable duty-cycle pauses.
- Modify `backend/internal/maintenance/service_test.go`: fake batch store and orchestration tests.
- Modify `backend/internal/httpapi/api.go`: register the new settings-manage route.
- Modify `backend/internal/httpapi/maintenance_handlers.go`: create/lock/start/audit the cleanup worker task and report phase summaries.
- Modify `backend/internal/httpapi/authz_test.go`: permission, accepted-task, preserved-data, audit, and conflict tests.
- Modify `backend/internal/i18n/messages.go`: bilingual task-step, progress, summary, and API messages.
- Modify `web/src/components/DataMaintenancePanel.vue`: add the independently confirmed action.
- Create `web/src/components/DataMaintenancePanel.test.ts`: confirmation, cancellation, permission, and POST behavior.
- Modify `web/src/i18n/messages.ts`: bilingual button, confirmation, success, failure, and space-reuse wording.

---

### Task 1: Store batch-deletion primitives

**Files:**
- Create: `backend/internal/store/operational_history_cleanup.go`
- Create: `backend/internal/store/operational_history_cleanup_test.go`

**Interfaces:**
- Produces: `HistoryCleanupBatch { Deleted int; LastID int64; Done bool }`.
- Produces: `(*Store).DeleteStatusSnapshotHistoryBatch(ctx context.Context, cutoff time.Time, afterID int64, limit int) (HistoryCleanupBatch, error)`.
- Produces: `(*Store).DeleteAlertUpdateEventsBatch(ctx context.Context, cutoff time.Time, afterID int64, limit int) (HistoryCleanupBatch, error)`.
- Enforces: `limit` is clamped to `1..500`; callers cannot pass a table name or event type.

- [ ] **Step 1: Write failing Store tests for exact deletion scope**

Create table-driven tests in `operational_history_cleanup_test.go`. Seed rows with direct package-level SQL so timestamps are deterministic, then assert old status history and old `updated` events are removed while recent history, current-state tables, and lifecycle events remain.

```go
func TestOperationalHistoryCleanupBatchesPreserveCurrentStateAndLifecycleEvents(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "aifar.db"))
	if err != nil { t.Fatal(err) }
	defer db.Close()

	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	cutoff := now.AddDate(0, 0, -7)
	old := cutoff.Add(-time.Hour)
	recent := cutoff.Add(time.Hour)
	for _, createdAt := range []time.Time{old, old, recent} {
		_, err := db.db.Exec(`insert into status_snapshot_history(scope,resource_id,server_id,status,payload,last_error,version,collected_at,created_at)
			values('app.instance','app-1','','running','{}','',1,?,?)`, createdAt, createdAt)
		if err != nil { t.Fatal(err) }
	}
	for _, event := range []struct{ name string; at time.Time }{
		{"updated", old}, {"updated", recent}, {"created", old}, {"resolved", old},
	} {
		_, err := db.db.Exec(`insert into alert_events(alert_id,fingerprint,event,actor,message,created_at) values('alt-1','fp-1',?,'system','',?)`, event.name, event.at)
		if err != nil { t.Fatal(err) }
	}
	_, err = db.db.Exec(`insert into status_snapshots(scope,resource_id,server_id,status,payload,last_error,version,collected_at,updated_at)
		values('app.instance','app-1','','running','{}','',1,?,?)`, now, now)
	if err != nil { t.Fatal(err) }
	_, err = db.db.Exec(`insert into alerts(id,fingerprint,severity,scope,resource_id,server_id,app,instance_id,status,title,message,evidence_json,required_permission,first_seen_at,last_seen_at,updated_at)
		values('alt-1','fp-1','warning','app.instance','app-1','','','','open','title','','{}','',?,?,?)`, old, now, now)
	if err != nil { t.Fatal(err) }

	statusBatch, err := db.DeleteStatusSnapshotHistoryBatch(context.Background(), cutoff, 0, 1)
	if err != nil { t.Fatal(err) }
	if statusBatch.Deleted != 1 || statusBatch.LastID == 0 || statusBatch.Done {
		t.Fatalf("unexpected status batch: %+v", statusBatch)
	}
	alertBatch, err := db.DeleteAlertUpdateEventsBatch(context.Background(), cutoff, 0, 500)
	if err != nil { t.Fatal(err) }
	if alertBatch.Deleted != 1 { t.Fatalf("unexpected alert batch: %+v", alertBatch) }

	if count, _ := db.CountRows("status_snapshots"); count != 1 { t.Fatalf("current snapshot count=%d", count) }
	if count, _ := db.CountRows("alerts"); count != 1 { t.Fatalf("current alert count=%d", count) }
	var lifecycleCount int
	if err := db.db.QueryRow(`select count(*) from alert_events where event in ('created','resolved')`).Scan(&lifecycleCount); err != nil { t.Fatal(err) }
	if lifecycleCount != 2 { t.Fatalf("lifecycle event count=%d", lifecycleCount) }
}
```

Add a second test that inserts 600 eligible rows, passes `limit=1000`, verifies only 500 are deleted, reruns from `LastID`, and finally receives `Done=true`. Add a third test using an already-cancelled context and assert no rows are deleted.

- [ ] **Step 2: Run the focused Store tests and confirm failure**

Run:

```powershell
Set-Location backend
go test ./internal/store -run OperationalHistoryCleanup -count=1
```

Expected: compile failure because `HistoryCleanupBatch` and the two batch methods do not exist.

- [ ] **Step 3: Implement context-aware keyset batches**

Add the fixed public methods and one unexported helper. Query eligible IDs in ascending primary-key order inside `BeginTx`, close the rows, delete only those IDs, and commit. An empty ID result returns `Done: true`; a non-empty result returns the last selected ID and `Done: false`, even when fewer than `limit` rows were found, so completion is confirmed by one final empty batch.

```go
type HistoryCleanupBatch struct {
	Deleted int
	LastID  int64
	Done    bool
}

const maxOperationalHistoryCleanupBatch = 500

func (s *Store) DeleteStatusSnapshotHistoryBatch(ctx context.Context, cutoff time.Time, afterID int64, limit int) (HistoryCleanupBatch, error) {
	return s.deleteOperationalHistoryBatch(ctx,
		`select id from status_snapshot_history where id > ? and created_at < ? order by id limit ?`,
		`delete from status_snapshot_history where id in (%s)`, cutoff, afterID, limit)
}

func (s *Store) DeleteAlertUpdateEventsBatch(ctx context.Context, cutoff time.Time, afterID int64, limit int) (HistoryCleanupBatch, error) {
	return s.deleteOperationalHistoryBatch(ctx,
		`select id from alert_events where id > ? and event='updated' and created_at < ? order by id limit ?`,
		`delete from alert_events where id in (%s)`, cutoff, afterID, limit)
}
```

The helper must clamp the limit, use only generated `?` placeholders for selected integer IDs, call `QueryContext`/`ExecContext`, and never accept a table name from an HTTP request.

- [ ] **Step 4: Run Store tests and formatting**

Run:

```powershell
gofmt -w internal/store/operational_history_cleanup.go internal/store/operational_history_cleanup_test.go
go test ./internal/store -run OperationalHistoryCleanup -count=1
```

Expected: PASS; the 600-row case demonstrates the 500-row hard bound and successful rerun.

- [ ] **Step 5: Commit the Store slice**

```powershell
git add backend/internal/store/operational_history_cleanup.go backend/internal/store/operational_history_cleanup_test.go
git commit -m "feat: add operational history cleanup batches"
```

---

### Task 2: Maintenance orchestration with adaptive duty cycling

**Files:**
- Modify: `backend/internal/maintenance/service.go:17-82`
- Modify: `backend/internal/maintenance/service_test.go:1-105`

**Interfaces:**
- Consumes: the two Store batch methods from Task 1.
- Produces: `OperationalHistoryCleanupPhaseResult { Deleted int; Batches int }`.
- Produces: `OperationalHistoryCleanupProgress { Phase string; Deleted int; TotalDeleted int }`.
- Produces: `OperationalHistoryCutoff(now time.Time) time.Time` using the fixed seven-day retention rule.
- Produces: `(Service).CleanupStatusHistory(ctx context.Context, cutoff time.Time, report func(OperationalHistoryCleanupProgress)) (OperationalHistoryCleanupPhaseResult, error)`.
- Produces: `(Service).CleanupAlertUpdates(ctx context.Context, cutoff time.Time, report func(OperationalHistoryCleanupProgress)) (OperationalHistoryCleanupPhaseResult, error)`.

- [ ] **Step 1: Extend the fake Store and write failing orchestration tests**

Extend `fakeStore` with scripted batch results and method-call recording. Add tests for both phases, cancellation during the injected pause, batch growth capped at 500, and immediate stop after a Store error.

```go
func TestCleanupOperationalHistoryPhasesReportTotals(t *testing.T) {
	fake := &historyCleanupFakeStore{
		statusBatches: []store.HistoryCleanupBatch{{Deleted: 100, LastID: 100}, {Done: true}},
		alertBatches:  []store.HistoryCleanupBatch{{Deleted: 3, LastID: 9}, {Done: true}},
	}
	svc := NewService(fake, RetentionConfig{})
	svc.pause = func(context.Context, time.Duration) error { return nil }
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	cutoff := OperationalHistoryCutoff(now)
	statusResult, err := svc.CleanupStatusHistory(context.Background(), cutoff, nil)
	if err != nil { t.Fatal(err) }
	alertResult, err := svc.CleanupAlertUpdates(context.Background(), cutoff, nil)
	if err != nil { t.Fatal(err) }
	if !cutoff.Equal(now.AddDate(0, 0, -7)) || statusResult.Deleted != 100 || alertResult.Deleted != 3 || statusResult.Batches+alertResult.Batches != 2 {
		t.Fatalf("unexpected results: status=%+v alert=%+v cutoff=%s", statusResult, alertResult, cutoff)
	}
}
```

For cancellation, make `pause` return `ctx.Err()` after cancelling and assert the alert phase is never called. For error handling, return a sentinel error from the second status batch and assert no later calls occur.

- [ ] **Step 2: Run focused maintenance tests and confirm failure**

Run:

```powershell
Set-Location backend
go test ./internal/maintenance -run CleanupOperationalHistory -count=1
```

Expected: compile failure because the cleanup result, progress type, Store methods, and orchestration method are missing from the maintenance package.

- [ ] **Step 3: Implement the cleanup loop and cancellable pause**

Extend the maintenance `Store` interface with the two Task 1 methods. Add unexported tuning fields to `Service` initialized by `NewService`:

```go
type Service struct {
	store Store
	cfg   RetentionConfig
	pause func(context.Context, time.Duration) error
}

func waitForCleanupPause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
```

Both phase methods must delegate to one unexported adaptive cleanup loop. That loop must:

1. Require the caller-provided immutable cutoff; `OperationalHistoryCutoff` normalizes zero `now` to `time.Now()` and subtracts exactly seven calendar days.
2. Start at cursor `0` and batch size `100` for each phase.
3. Double the batch size after a successful batch faster than 50 ms, capped at 500.
4. Halve the batch size after a batch slower than 200 ms, floored at 100.
5. After each non-empty commit, wait for `max(100ms, batchDuration*9)` using `pause`, targeting at most about 10% SQLite occupancy.
6. Check `ctx.Err()` before each batch and during each pause.
7. Call `report` at phase completion and at most once per elapsed minute during a long phase; never report every batch.
8. Return committed phase totals with the error so the handler can log partial progress.

- [ ] **Step 4: Run maintenance and Store tests**

Run:

```powershell
gofmt -w internal/maintenance/service.go internal/maintenance/service_test.go
go test ./internal/maintenance ./internal/store -count=1
```

Expected: PASS.

- [ ] **Step 5: Commit maintenance orchestration**

```powershell
git add backend/internal/maintenance/service.go backend/internal/maintenance/service_test.go
git commit -m "feat: orchestrate operational history cleanup"
```

---

### Task 3: Worker endpoint, mutual exclusion, audit, and backend i18n

**Files:**
- Modify: `backend/internal/httpapi/api.go:89-95`
- Modify: `backend/internal/httpapi/maintenance_handlers.go:21-287`
- Modify: `backend/internal/httpapi/authz_test.go:540-588`
- Modify: `backend/internal/i18n/messages.go:521-539,821-839`

**Interfaces:**
- Consumes: `OperationalHistoryCutoff`, `Service.CleanupStatusHistory`, and `Service.CleanupAlertUpdates` from Task 2.
- Produces: `POST /api/v2/maintenance/operational-history/cleanup` returning the existing accepted-task response with `taskId`.
- Produces: task type `maintenance.operational-history.cleanup`, target `control-plane`.
- Produces: operation lock `scope=maintenance`, `resource_id=control-plane`, `operation=operational-history-cleanup`.

- [ ] **Step 1: Write failing API tests**

Add these cases to `authz_test.go`:

```go
func TestOperationalHistoryCleanupStartsTask(t *testing.T) {
	api, db, secret := newAuthzTestAPI(t)
	token := issueTestToken(t, db, secret, "owner", "owner")
	req := httptest.NewRequest(http.MethodPost, "/api/v2/maintenance/operational-history/cleanup", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	api.Router().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted { t.Fatalf("expected 202, got %d body=%s", rec.Code, rec.Body.String()) }
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil { t.Fatal(err) }
	taskID, _ := body["taskId"].(string)
	if taskID == "" { t.Fatalf("expected taskId: %+v", body) }
	waitForTaskStatus(t, db, taskID, "success")
	assertAuditExists(t, db, "maintenance.operational-history.cleanup", "running", "owner", "control-plane")
}
```

Add a viewer-token case expecting `403`, action `auth.permission.denied`, and required permission `settings.manage`. Add a conflict case that creates an active `OperationLock` with the exact maintenance scope/resource/operation, expects `409 OPERATION_LOCKED`, and verifies the rejected request does not leave a pending cleanup task behind.

- [ ] **Step 2: Run focused HTTP API tests and confirm failure**

Run:

```powershell
Set-Location backend
go test ./internal/httpapi -run OperationalHistoryCleanup -count=1
```

Expected: FAIL with route-not-found or method-not-allowed because the endpoint is not registered.

- [ ] **Step 3: Register the route and implement task startup**

Register:

```go
r.Post("/maintenance/operational-history/cleanup", api.requirePermission(rbac.SettingsManage, api.runOperationalHistoryCleanup))
```

In `maintenance_handlers.go`, add constants for the task type, target, and operation-lock identity. The handler must:

1. Create the pending task and its four-step plan.
2. Acquire the existing Store operation lock with the task ID as owner and a one-hour expiry; worker heartbeats and releases it automatically.
3. On lock conflict, delete the unstarted task/plan and respond `409 OPERATION_LOCKED` with `ownerTaskId` and `expiresAt` details.
4. Start the existing task through `a.tasks.StartExistingWithLanguage`.
5. Complete `preflight`, start `cleanup-status-history`, call `CleanupStatusHistory`, record its returned total, and finish that step.
6. Start `cleanup-alert-updates`, call `CleanupAlertUpdates` with the same fixed cutoff, record its returned total, and finish that step.
7. Start `summarize`, log both totals plus the reusable-space warning, then finish the target.
8. Log cutoff and phase totals, not individual batches.
9. Finish the active step and target consistently on cancellation or failure.
10. Write the start audit only after worker start succeeds.

Use the existing runtime-diagnostic task cleanup/operation-lock pattern as the reference for safely removing unstarted tasks and releasing a lock if worker startup fails.

- [ ] **Step 4: Add bilingual backend messages**

Add exact Chinese and English keys to `backend/internal/i18n/messages.go`:

```text
maintenance.operationalHistoryPreflightStep
maintenance.operationalHistoryStatusStep
maintenance.operationalHistoryAlertStep
maintenance.operationalHistorySummaryStep
maintenance.operationalHistoryCutoff
maintenance.operationalHistoryStatusDeleted
maintenance.operationalHistoryAlertUpdatesDeleted
maintenance.operationalHistorySpaceNote
api.operationalHistoryCleanupStarted
api.operationalHistoryCleanupTaskCreateFailed
api.operationalHistoryCleanupTaskPlanFailed
api.operationalHistoryCleanupTaskStartFailed
```

Chinese space note: `在线清理仅释放 SQLite 内部可复用空间，不保证数据库文件立即缩小`.

English space note: `Online cleanup only releases reusable SQLite pages; it does not guarantee that the database file shrinks immediately`.

- [ ] **Step 5: Run backend formatting and targeted tests**

Run:

```powershell
gofmt -w internal/httpapi/api.go internal/httpapi/maintenance_handlers.go internal/httpapi/authz_test.go internal/i18n/messages.go
go test ./internal/httpapi ./internal/maintenance ./internal/store -count=1
```

Expected: PASS, including permission and lock-conflict cases.

- [ ] **Step 6: Commit the backend endpoint**

```powershell
git add backend/internal/httpapi/api.go backend/internal/httpapi/maintenance_handlers.go backend/internal/httpapi/authz_test.go backend/internal/i18n/messages.go
git commit -m "feat: expose operational history cleanup task"
```

---

### Task 4: Data-maintenance button, confirmation, and frontend tests

**Files:**
- Modify: `web/src/components/DataMaintenancePanel.vue:1-159`
- Create: `web/src/components/DataMaintenancePanel.test.ts`
- Modify: `web/src/i18n/messages.ts:1014-1038,2140-2164`
- Read before edits: `design/ant-design-system-portable202606.md`

**Interfaces:**
- Consumes: `POST /maintenance/operational-history/cleanup` through existing `apiPost`.
- Produces: independent “清理运行历史” button and warning confirmation.
- Produces: bilingual keys `settings.runOperationalHistoryCleanup`, `settings.operationalHistoryCleanupTitle`, `settings.operationalHistoryCleanupConfirm`, `settings.operationalHistoryCleanupAccepted`, and `settings.operationalHistoryCleanupFailed`.

- [ ] **Step 1: Read the portable design system completely**

Run:

```powershell
Get-Content -LiteralPath design/ant-design-system-portable202606.md
```

Apply the existing settings-panel spacing, button hierarchy, confirmation, focus, and disabled-state rules. Do not redesign the page.

- [ ] **Step 2: Write failing component tests**

Create `DataMaintenancePanel.test.ts` with mocked API and Element Plus confirmation. Add three cases:

1. A permitted user confirms, causing exactly one POST to `/maintenance/operational-history/cleanup`.
2. Confirmation rejection causes no POST.
3. `canManage=false` leaves the button disabled.

Use a stable `data-testid="operational-history-cleanup"` on the new button.

```ts
it('confirms before starting operational history cleanup', async () => {
  vi.mocked(ElMessageBox.confirm).mockResolvedValue('confirm' as never)
  const wrapper = mountPanel(true)
  await wrapper.get('[data-testid="operational-history-cleanup"]').trigger('click')
  await flushPromises()
  expect(ElMessageBox.confirm).toHaveBeenCalledOnce()
  expect(apiPost).toHaveBeenCalledWith('/maintenance/operational-history/cleanup')
})
```

Mock `apiGet` to return `{ items: [] }` because the component refreshes backups on mount. Stub `KeyValueGrid`, `DataTable`, `ConfirmAction`, `ElButton`, and `ElTooltip` without suppressing the cleanup button click.

- [ ] **Step 3: Run the component test and confirm failure**

Run:

```powershell
Set-Location web
pnpm test -- DataMaintenancePanel.test.ts
```

Expected: FAIL because the new button and handler do not exist.

- [ ] **Step 4: Implement the independent confirmed action**

Modify `DataMaintenancePanel.vue`:

- Import `ElMessageBox` beside `ElMessage`.
- Add `operationalHistoryCleanupRunning = ref(false)`.
- Add a default-style button beside the two current maintenance actions; do not change the current retention button.
- Disable it when `!canManage` and show the existing permission tooltip.
- Confirm with `type: 'warning'`, explicit cancel/confirm text, and the bilingual message.
- Treat confirmation rejection as a normal cancel with no error toast.
- POST the fixed endpoint, show accepted/failed messages, and always clear loading state.

```ts
async function runOperationalHistoryCleanup() {
  if (!props.canManage) {
    ElMessage.warning(props.disabledReason)
    return
  }
  try {
    await ElMessageBox.confirm(
      t('settings.operationalHistoryCleanupConfirm'),
      t('settings.operationalHistoryCleanupTitle'),
      { type: 'warning', confirmButtonText: t('common.confirm'), cancelButtonText: t('common.cancel') }
    )
  } catch {
    return
  }
  operationalHistoryCleanupRunning.value = true
  try {
    await apiPost('/maintenance/operational-history/cleanup')
    ElMessage.success(t('settings.operationalHistoryCleanupAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.operationalHistoryCleanupFailed'))
  } finally {
    operationalHistoryCleanupRunning.value = false
  }
}
```

- [ ] **Step 5: Add exact bilingual frontend copy**

Chinese confirmation:

```text
将渐进清理 7 天前的状态历史和告警 updated 更新事件。当前状态、当前告警及 created、acknowledged、muted、unmuted、resolved 等生命周期事件不会删除。操作会进入任务中心且可以取消。在线清理只释放 SQLite 内部可复用空间，不保证 aifar.db 文件立即缩小。建议先创建数据库备份。
```

English confirmation:

```text
This gradually removes status history and alert updated events older than 7 days. Current status, current alerts, and lifecycle events such as created, acknowledged, muted, unmuted, and resolved are preserved. The operation runs in Task Center and can be cancelled. Online cleanup only releases reusable SQLite pages and does not guarantee that aifar.db shrinks immediately. Create a database backup first.
```

- [ ] **Step 6: Run frontend tests and build**

Run from repository root:

```powershell
pnpm test:web
pnpm web:build
```

Expected: Vitest passes; Vue TypeScript checking and Vite build pass.

- [ ] **Step 7: Commit the frontend slice**

```powershell
git add web/src/components/DataMaintenancePanel.vue web/src/components/DataMaintenancePanel.test.ts web/src/i18n/messages.ts
git commit -m "feat: add operational history cleanup action"
```

---

### Task 5: Integrated verification and scope audit

**Files:**
- Verify only; modify earlier files only if a failing test exposes a defect.
- Update `memory.md` with the final reusable conclusion after verification.

**Interfaces:**
- Verifies the complete Store → maintenance Service → worker task → API → Vue action path.
- Verifies no `aifar-agent`, automatic scheduler, or VACUUM behavior was introduced.

- [ ] **Step 1: Run focused backend race-sensitive packages**

Run:

```powershell
Set-Location backend
go test ./internal/store ./internal/maintenance ./internal/httpapi -count=1
Set-Location ..
```

Expected: PASS.

- [ ] **Step 2: Run repository backend and frontend gates**

Run:

```powershell
pnpm test
pnpm test:web
pnpm web:build
```

Expected: all commands exit 0.

- [ ] **Step 3: Inspect destructive scope and forbidden behavior**

Run:

```powershell
rg -n "delete from|VACUUM|VACUUM INTO|aifar-agent|time\.Ticker|NewTicker" backend/internal/store/operational_history_cleanup.go backend/internal/maintenance/service.go backend/internal/httpapi/maintenance_handlers.go web/src/components/DataMaintenancePanel.vue
git diff --check
git status --short
```

Expected:

- DELETE statements target only `status_snapshot_history` and `alert_events`.
- No VACUUM, agent, or automatic ticker appears in the feature path.
- No whitespace errors.
- Only intended feature files plus the required `memory.md` update are changed.

- [ ] **Step 4: Perform a local API smoke check with bounded seed data**

Using the existing HTTP API test fixture or a focused Go test, seed more than 500 old status-history rows and mixed alert events, invoke the endpoint, wait for success, and assert:

- all eligible rows are eventually deleted across multiple batches;
- `status_snapshots` and `alerts` counts are unchanged;
- lifecycle events remain;
- one cleanup task and one start audit exist;
- task logs contain phase summaries rather than one row per deletion batch.

Run the named smoke test twice to prove idempotent rerun:

```powershell
Set-Location backend
go test ./internal/httpapi -run TestOperationalHistoryCleanupLargeBatchAndRerun -count=2
Set-Location ..
```

Expected: PASS both times.

- [ ] **Step 5: Commit verification-driven fixes and memory**

If verification required code fixes, stage only the corrected feature files. Always stage the concise `memory.md` conclusion, then commit:

```powershell
git add memory.md backend/internal/store/operational_history_cleanup.go backend/internal/store/operational_history_cleanup_test.go backend/internal/maintenance/service.go backend/internal/maintenance/service_test.go backend/internal/httpapi/api.go backend/internal/httpapi/maintenance_handlers.go backend/internal/httpapi/authz_test.go backend/internal/i18n/messages.go web/src/components/DataMaintenancePanel.vue web/src/components/DataMaintenancePanel.test.ts web/src/i18n/messages.ts
git commit -m "test: verify operational history cleanup"
```

If no code fixes are needed, stage and commit only `memory.md`.
