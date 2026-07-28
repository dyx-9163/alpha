# Runtime Pod Progressive Metrics Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Render Runtime Pod base state before Docker metrics, then refresh CPU and memory silently every 10 seconds while the Pods tab is active.

**Architecture:** Keep the existing Runtime endpoint and split client loading into base and metrics phases. Pure helpers preserve previous metrics across base responses, and a lifecycle-owned scheduler provides immediate plus periodic single-flight metric refreshes. `ContainersView.vue` owns request scope validation and activation; `AifarRuntimePodsTab.vue` always renders the table.

**Tech Stack:** Vue 3 Composition API, TypeScript, Vitest, Element Plus, existing `/api/v2/containers/aifar/runtime` API.

## Global Constraints

- Metrics interval is exactly 10 seconds.
- Metrics refresh is active only when the top-level tab is `aifar-runtime`, the resource tab is `pods`, and a Docker target plus Runtime instance are selected.
- Background metrics requests must not increment the global loading counter.
- Base responses must retain the last successful CPU and memory values for matching Pods.
- Stale responses from a previous server scope must not overwrite the current view.
- No backend, rollback, health-check, or Services-table changes.

---

### Task 1: Pod loading policy and metric preservation

**Files:**
- Modify: `web/src/containers/runtime/runtimePodLoading.ts`
- Modify: `web/src/containers/runtime/runtimePodLoading.test.ts`
- Create: `web/src/containers/runtime/runtimePodMetrics.ts`
- Create: `web/src/containers/runtime/runtimePodMetrics.test.ts`

**Interfaces:**
- Produces: `runtimePodLoadArgs(trigger): [force, includeStats, background]`.
- Produces: `mergeRuntimePodMetrics(previous, incoming): AifarRuntimePod[]`.

- [ ] **Step 1: Write failing policy and merge tests**

```ts
expect(runtimePodLoadArgs('enter')).toEqual([false, false, false])
expect(runtimePodLoadArgs('metrics')).toEqual([true, true, true])
expect(mergeRuntimePodMetrics(previous, base)).toEqual([
  expect.objectContaining({ containerName: 'pod-1', cpuPercent: 1.5, memoryUsage: '512 MiB' })
])
```

- [ ] **Step 2: Run tests and verify RED**

Run: `pnpm test:web`

Expected: policy expectations fail and `runtimePodMetrics` cannot be imported.

- [ ] **Step 3: Implement the minimal policy and merge helper**

```ts
export type RuntimePodLoadTrigger = 'enter' | 'scope-change' | 'refresh' | 'metrics' | 'status-event' | 'logs'

export function runtimePodLoadArgs(trigger: RuntimePodLoadTrigger): [boolean, boolean, boolean] {
  if (trigger === 'metrics') return [true, true, true]
  if (trigger === 'refresh') return [true, false, false]
  if (trigger === 'enter' || trigger === 'scope-change') return [false, false, false]
  if (trigger === 'status-event') return [true, false, true]
  return [false, false, true]
}
```

`mergeRuntimePodMetrics` keys Pods by `instanceId + containerName`, copies only missing metric fields from the previous matching Pod, and always keeps incoming base/status fields authoritative.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `pnpm test:web`

Expected: all frontend tests pass.

- [ ] **Step 5: Commit**

```powershell
git add web/src/containers/runtime/runtimePodLoading.ts web/src/containers/runtime/runtimePodLoading.test.ts web/src/containers/runtime/runtimePodMetrics.ts web/src/containers/runtime/runtimePodMetrics.test.ts
git commit -m "fix: separate runtime pod state from metrics"
```

### Task 2: Single-flight periodic metrics scheduler

**Files:**
- Create: `web/src/containers/runtime/runtimePodMetricsScheduler.ts`
- Create: `web/src/containers/runtime/runtimePodMetricsScheduler.test.ts`

**Interfaces:**
- Produces: `createRuntimePodMetricsScheduler(refresh, intervalMs = 10000)`.
- Returned methods: `start()`, `request()`, `stop()`, `dispose()`.

- [ ] **Step 1: Write failing fake-timer tests**

```ts
const scheduler = createRuntimePodMetricsScheduler(refresh, 10_000)
scheduler.start()
expect(refresh).toHaveBeenCalledTimes(1)
await vi.advanceTimersByTimeAsync(10_000)
expect(refresh).toHaveBeenCalledTimes(2)
```

Also assert that an unresolved refresh suppresses interval overlap, `stop()` prevents later calls, and a subsequent `start()` creates a fresh immediate refresh.

- [ ] **Step 2: Run tests and verify RED**

Run: `pnpm test:web`

Expected: module import fails.

- [ ] **Step 3: Implement scheduler lifecycle**

Use one `setInterval`, an `active` flag, a `running` flag, and a coalesced `pending` flag. `start()` activates and requests immediately; `request()` is ignored after stop/dispose and coalesces while running; `stop()` clears the timer and pending state; `dispose()` permanently disables the scheduler.

- [ ] **Step 4: Run tests and verify GREEN**

Run: `pnpm test:web`

Expected: all scheduler and frontend tests pass.

- [ ] **Step 5: Commit**

```powershell
git add web/src/containers/runtime/runtimePodMetricsScheduler.ts web/src/containers/runtime/runtimePodMetricsScheduler.test.ts
git commit -m "feat: schedule silent runtime pod metrics"
```

### Task 3: Integrate progressive loading into the Runtime page

**Files:**
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/containers/runtime/AifarRuntimePodsTab.vue`
- Create: `web/src/containers/runtime/AifarRuntimePodsTab.test.ts`

**Interfaces:**
- Consumes: `runtimePodLoadArgs`, `mergeRuntimePodMetrics`, `createRuntimePodMetricsScheduler`.
- Changes: `ensureRuntimePodsLoaded(force?, includeStats?, background?)`.
- Produces: `refreshRuntimePodBase()` and `refreshRuntimePodMetrics()` context actions.

- [ ] **Step 1: Write failing component render test**

Render `AifarRuntimePodsTab` with `runtimePodsLoadedForCurrentScope=false` and assert that column labels are present while `containers.loadPods` is absent. Assert the two buttons call base refresh and metrics refresh separately.

- [ ] **Step 2: Run tests and verify RED**

Run: `pnpm test:web`

Expected: current component renders the large lazy state and lacks separate refresh actions.

- [ ] **Step 3: Implement scoped two-phase loading**

In `loadAifarRuntime`:

- capture the request cache key before awaiting;
- on background failure, set the error and return without replacing `aifarRuntime`;
- ignore the response if its captured server cache key no longer matches;
- apply `mergeRuntimePodMetrics(currentPods, next.pods)` to non-stat base responses;
- do not reset the stats-loaded marker to false when a base response retained previous stats.

Add a `runtimePodMetricsScheduler` whose refresh callback validates current tab, resource tab, target query, and selected instance before calling `ensureRuntimePodsLoaded(...runtimePodLoadArgs('metrics'))`.

Activation flow:

```ts
async function activateRuntimePods(trigger: 'enter' | 'scope-change') {
  runtimePodMetricsScheduler.stop()
  await ensureRuntimePodsLoaded(...runtimePodLoadArgs(trigger))
  if (runtimePodsActive()) runtimePodMetricsScheduler.start()
}
```

Stop it when leaving Pods, changing server before reload, or unmounting.

- [ ] **Step 4: Keep the table visible**

Remove the `runtime-lazy-state` branch. Always render `el-table`, apply a local loading state only while base Pods are unavailable, show `-` through existing formatters until metrics arrive, and wire “刷新” to base refresh plus scheduler request while “刷新指标” only requests metrics.

- [ ] **Step 5: Run frontend verification**

Run: `pnpm test:web`

Expected: all tests pass.

Run: `pnpm web:build`

Expected: Vue typecheck and Vite production build pass.

- [ ] **Step 6: Review and commit exact files**

Run: `git diff --check`

```powershell
git add web/src/views/ContainersView.vue web/src/containers/runtime/context.ts web/src/containers/runtime/AifarRuntimePodsTab.vue web/src/containers/runtime/AifarRuntimePodsTab.test.ts
git commit -m "fix: progressively load runtime pod metrics"
```

### Task 4: Final regression gate

**Files:**
- Modify only if a scoped regression is found.

- [ ] **Step 1: Run complete frontend tests**

Run: `pnpm test:web`

Expected: all test files pass with zero failures.

- [ ] **Step 2: Run production frontend build**

Run: `pnpm web:build`

Expected: exit code 0.

- [ ] **Step 3: Confirm scope**

Run: `git status --short` and `git diff --check`.

Expected: only the pre-existing `.gitignore`, diagnostic cleaner test, and `memory.md` changes remain outside this feature's commits.
