# AIFAR Runtime Batch Offline, Pod Metrics, and Log Rendering Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement the approved Runtime 1–4 changes: one-task multi-select offline, remove service CPU/memory columns, load Pod metrics automatically, and make the live-log virtual viewport resilient to invalid scroll state.

**Architecture:** Extend the existing Runtime scale boundary with an optional batch scale capability that performs one instance-locked staged spec commit and one control-plane update pass. Keep metric loading event-driven by changing Pods call sites to request stats. Fix log rendering inside the existing viewport composable, leaving SSE and parsing unchanged.

**Tech Stack:** Go 1.24, Chi, worker/tasks, SQLite control-plane store, embedded shell templates, Vue 3, TypeScript, Element Plus, Vitest.

## Global Constraints

- Do not modify artifact update, readiness, rollback, failed-revision isolation, or bad-package restart behavior.
- Every backend mutation returns a task id and records task steps, target state, and audit.
- Preserve `/api/v2`, existing single-service offline API, Runtime instance mutation lock, staged spec commit, and `409` conflict behavior.
- Preserve service CPU/memory API fields; remove only their Services-tab presentation.
- Use existing global SSE refresh; do not add browser polling.
- Add all user-visible text in both Chinese and English.
- Use fake remotes in automated tests; do not connect to real servers.

---

### Task 1: Harden the live-log virtual viewport

**Files:**
- Modify: `web/src/containers/runtime/useAifarRuntimeLogViewport.test.ts`
- Modify: `web/src/containers/runtime/useAifarRuntimeLogViewport.ts`
- Modify: `web/src/views/ContainersView.vue`

**Interfaces:**
- Consumes: `RuntimeLogRow[]`, `{ rowHeight, visibleCount }`, and DOM scroll events.
- Produces: finite `runtimeLogTopSpacer`/`runtimeLogBottomSpacer`, a non-empty legal row window when rows exist, and `resetRuntimeLogViewport()` for state plus DOM reset.

- [ ] **Step 1: Write failing viewport regression tests**

Add table-driven tests that pass `NaN`, `Infinity`, `-20`, and a very large scroll offset through `handleRuntimeLogScroll`. Assert literal visible sequences and `Number.isFinite(spacer) && spacer >= 0`. Add a reset test:

```ts
viewport.runtimeLogViewport.value = elementWithScrollTop(80)
viewport.handleRuntimeLogScroll({ target: { scrollTop: 80 } } as unknown as Event)
viewport.resetRuntimeLogViewport()
expect(viewport.runtimeLogScrollTop.value).toBe(0)
expect(element.scrollTop).toBe(0)
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `pnpm test:web -- useAifarRuntimeLogViewport.test.ts`  
Expected: FAIL because invalid offsets currently generate an empty slice or non-finite spacer and no reset function exists.

- [ ] **Step 3: Implement finite normalization and reset**

Normalize all numeric inputs before computing the window:

```ts
const rowHeight = positiveFinite(options.rowHeight, 1)
const visibleCount = Math.max(1, Math.floor(positiveFinite(options.visibleCount, 1)))
const scrollTop = nonNegativeFinite(runtimeLogScrollTop.value)
const requestedStart = Math.max(0, Math.floor(scrollTop / rowHeight) - 8)
```

Return a `resetRuntimeLogViewport()` method that clears tracked scroll state and sets the mounted element's `scrollTop` to zero. Use it from `resetRuntimeLogView()` and `clearRuntimeLogView()`.

- [ ] **Step 4: Re-run focused tests and verify GREEN**

Run: `pnpm test:web -- useAifarRuntimeLogViewport.test.ts`  
Expected: PASS with all invalid-scroll cases rendering a legal window.

- [ ] **Step 5: Commit Task 1**

```text
git add web/src/containers/runtime/useAifarRuntimeLogViewport.ts web/src/containers/runtime/useAifarRuntimeLogViewport.test.ts web/src/views/ContainersView.vue
git commit -m "fix: keep runtime logs visible with invalid scroll state"
```

### Task 2: Simplify Services and load Pod stats automatically

**Files:**
- Modify: `web/src/containers/runtime/runtimeEntryMerge.test.ts`
- Create: `web/src/containers/runtime/runtimePodLoading.test.ts`
- Create: `web/src/containers/runtime/runtimePodLoading.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeServicesTab.vue`
- Modify: `web/src/containers/runtime/AifarRuntimePodsTab.vue`
- Modify: `web/src/views/ContainersView.vue`

**Interfaces:**
- Consumes: active Runtime resource tab and existing `ensureRuntimePodsLoaded(force, includeStats)`.
- Produces: `runtimePodsLoadArgs(trigger)` returning literal `[force, includeStats]` pairs used by all Pods entry/refresh call sites.

- [ ] **Step 1: Write failing rendering and loading-policy tests**

Extend the SSR contract to render Services and assert that `containers.cpu` and `containers.memory` are absent while Service, Endpoint, proxy, and image headers remain. Add a literal loading-policy table:

```ts
expect(runtimePodsLoadArgs('enter')).toEqual([false, true])
expect(runtimePodsLoadArgs('scope-change')).toEqual([false, true])
expect(runtimePodsLoadArgs('refresh')).toEqual([true, true])
expect(runtimePodsLoadArgs('status-event')).toEqual([true, true])
```

- [ ] **Step 2: Run focused frontend tests and verify RED**

Run: `pnpm test:web -- runtimeEntryMerge.test.ts runtimePodLoading.test.ts`  
Expected: FAIL because Services still renders CPU/memory and the loading policy does not exist.

- [ ] **Step 3: Implement the minimal presentation and loading changes**

Remove the two Services table columns. Add the small pure loading-policy helper and use its values when entering Pods, switching server/instance while Pods is active, clicking normal refresh, clicking refresh metrics, and processing an `aifar.runtime` event while Pods is visible. Do not change log-tab Pod metadata loading to request stats.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run: `pnpm test:web -- runtimeEntryMerge.test.ts runtimePodLoading.test.ts runtimeStatusRefresh.test.ts`  
Expected: PASS; scheduler coalescing remains unchanged.

- [ ] **Step 5: Commit Task 2**

```text
git add web/src/containers/runtime/runtimeEntryMerge.test.ts web/src/containers/runtime/runtimePodLoading.test.ts web/src/containers/runtime/runtimePodLoading.ts web/src/containers/runtime/AifarRuntimeServicesTab.vue web/src/containers/runtime/AifarRuntimePodsTab.vue web/src/views/ContainersView.vue
git commit -m "feat: load runtime pod metrics automatically"
```

### Task 3: Add one-commit backend batch offline capability

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Modify: `backend/internal/apps/aifar/scale.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/templates/scale-service.sh`
- Modify: `backend/internal/apps/aifar/service_test.go`
- Modify: `backend/internal/httpapi/aifar_runtime_controller.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Produces: registry `ServiceBatchScaleModule.ScaleServices(context.Context, ServiceBatchScaleRequest, RunContext) error`.
- Produces: `POST /api/v2/containers/aifar/services/batch-offline?serverId=...` with `{ instanceId, services }` and `{ taskId }` response.
- Preserves: `ServiceScaleModule.ScaleService` and the single-service route.

- [ ] **Step 1: Write failing service tests for one batch commit**

Create tests with literal desired replicas such as `file=1`, `gateway=2`, `permission=0`. Request `file` and `gateway` at zero, then assert the fake remote saw one scale script, agent readback observes both at zero, `permission` remains zero, and metadata/control-plane rows change only after remote success. Add failure and response-loss/readback cases.

- [ ] **Step 2: Run the focused service test and verify RED**

Run from `backend`: `go test ./internal/apps/aifar -run "TestScaleServices" -count=1`  
Expected: FAIL because the batch request and method do not exist.

- [ ] **Step 3: Implement registry/module/service batch scale**

Add:

```go
type ServiceBatchScaleRequest struct {
    Server store.Server
    Instance store.AppInstance
    Language, Actor, Reason string
    DesiredReplicas map[string]int
}

type ServiceBatchScaleModule interface {
    ScaleServices(context.Context, ServiceBatchScaleRequest, RunContext) error
}
```

Refactor the scale service internals so single and batch paths share one implementation. It must acquire one instance orchestration lock, read metadata once, render one staged spec with all requested assignments, call the agent once, validate every requested deployment in readback, promote once, save metadata once, and update/prune control-plane rows for every requested service. Keep existing transaction repair semantics.

- [ ] **Step 4: Run service tests and verify GREEN**

Run from `backend`: `go test ./internal/apps/aifar -run "TestScale(Service|Services)" -count=1`  
Expected: PASS for existing single scale and new batch scale behavior.

- [ ] **Step 5: Write failing handler tests**

Add request tests for empty `services`, duplicate normalization, unknown service rejection, existing instance lock conflict, one task response, four stored steps, target record, and audit operation `aifar.scale.batch-offline`.

- [ ] **Step 6: Run handler tests and verify RED**

Run from `backend`: `go test ./internal/httpapi -run "TestBatchOffline" -count=1`  
Expected: FAIL because the route and handler are not registered.

- [ ] **Step 7: Implement route, task, audit, and localized logs**

Register the fixed route before parameterized service routes. Normalize and validate the full selection before task creation. Create one `aifar.scale.batch-offline` task with steps `load-runtime`, `validate-services`, `apply-batch-offline`, and `record-runtime-state`, then invoke `ServiceBatchScaleModule` with every service mapped to zero.

- [ ] **Step 8: Run backend focused tests and verify GREEN**

Run from `backend`:

```text
go test ./internal/apps/aifar -run "TestScale(Service|Services)" -count=1
go test ./internal/httpapi -run "TestBatchOffline" -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit Task 3**

```text
git add backend/internal/apps/registry/contract.go backend/internal/apps/aifar/module.go backend/internal/apps/aifar/scale.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/templates/scale-service.sh backend/internal/apps/aifar/service_test.go backend/internal/httpapi/aifar_runtime_controller.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
git commit -m "feat: add transactional runtime batch offline"
```

### Task 4: Add Deployments multi-select UI and complete integration verification

**Files:**
- Modify: `web/src/containers/runtime/api.test.ts`
- Modify: `web/src/containers/runtime/api.ts`
- Create: `web/src/containers/runtime/AifarRuntimeDeploymentsTab.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeDeploymentsTab.vue`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: `offlineRuntimeServices(query, instanceId, services)` and existing Runtime context.
- Produces: selection-column UI and `offlineAifarServices(rows: AifarRuntimeService[]): Promise<void>`.

- [ ] **Step 1: Write failing API and component tests**

Assert one API call emits:

```ts
expect(post).toHaveBeenCalledWith(
  '/containers/aifar/services/batch-offline?serverId=server-1',
  { instanceId: 'runtime-1', services: ['file', 'gateway'] }
)
```

Mount the real Deployments component with the existing injected context pattern. Assert offline rows are not selectable, selecting two online rows renders “批量下线（2）”, and confirming calls `offlineAifarServices` once with the two services.

- [ ] **Step 2: Run focused frontend tests and verify RED**

Run: `pnpm test:web -- api.test.ts AifarRuntimeDeploymentsTab.test.ts`  
Expected: FAIL because the helper, context action, selection UI, and localized copy do not exist.

- [ ] **Step 3: Implement API, context action, confirmation, and selection UI**

Use Element Plus table selection with a `selectable` predicate based on `aifarRuntimeOfflineDisabledReason`. Deduplicate service names before calling the batch API. The confirmation message includes the count and joined names; task tracking uses one label and clears selection after accepted submission. Keep the row-level offline action unchanged.

- [ ] **Step 4: Run focused frontend tests and verify GREEN**

Run: `pnpm test:web -- api.test.ts AifarRuntimeDeploymentsTab.test.ts runtimeEntryMerge.test.ts runtimePodLoading.test.ts useAifarRuntimeLogViewport.test.ts`  
Expected: PASS.

- [ ] **Step 5: Run repository verification**

Run:

```text
pnpm test:web
pnpm test
pnpm web:build
pnpm backend:build
git diff --check
```

Expected: all commands exit 0 with no new warnings or failures.

- [ ] **Step 6: Perform local browser regression**

On the local authenticated Runtime page, verify Services has no CPU/memory columns, Pods shows metrics on first entry, a 200-line log stream renders `.runtime-log-row`, and multi-select batch offline reaches the confirmation dialog. Do not confirm a real server mutation.

- [ ] **Step 7: Commit Task 4**

```text
git add web/src/containers/runtime/api.test.ts web/src/containers/runtime/api.ts web/src/containers/runtime/AifarRuntimeDeploymentsTab.test.ts web/src/containers/runtime/AifarRuntimeDeploymentsTab.vue web/src/containers/runtime/context.ts web/src/views/ContainersView.vue web/src/i18n/messages.ts
git commit -m "feat: add runtime deployment batch offline UI"
```
