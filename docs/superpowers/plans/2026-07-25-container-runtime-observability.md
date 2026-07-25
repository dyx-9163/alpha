# Container Runtime Observability Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make AIFAR Runtime status refresh from backend SSE events, classify complete runtime error stacks, and keep SSH terminal output visible and scrollable.

**Architecture:** Reuse the existing global realtime stream and add a small coalescing refresh scheduler at the Containers view boundary. Keep log transport unchanged while moving richer stateful parsing into the runtime log utility. Replace terminal viewport arithmetic with the existing flex layout contract and verify it with a source-level UI contract test.

**Tech Stack:** Vue 3, TypeScript, Pinia, Vitest, Element Plus, xterm.js, Go collector tests.

## Global Constraints

- Keep API prefix `/api/v2` and the existing authenticated global SSE connection.
- Keep `AIFAR_COLLECTOR_INTERVAL_SECONDS=15` as the backend cadence.
- Do not add browser-side interval polling or a second Runtime status SSE connection.
- Keep all visible copy localized in both `zh` and `en`.
- Do not change Runtime log transport, Docker collection, task execution, or terminal WebSocket contracts.
- Preserve the user's existing uncommitted `memory.md` content and do not push.

---

### Task 1: Coalesced Runtime Status Refresh

**Files:**
- Create: `web/src/containers/runtime/runtimeStatusRefresh.ts`
- Create: `web/src/containers/runtime/runtimeStatusRefresh.test.ts`
- Modify: `web/src/views/ContainersView.vue`

**Interfaces:**
- Produces: `createRuntimeStatusRefreshScheduler(refresh: () => Promise<void>, delayMs?: number): { request(): void; dispose(): void }`.
- Produces: `isRuntimeStatusEventForSelection(event, selectedServerId, runtimeActive): boolean` for a directly testable event-routing rule.
- Consumes: existing `realtime.lastEvent`, `selectedServerId`, `tab`, `loadAifarRuntime`, and component unmount lifecycle.

- [ ] **Step 1: Write the failing scheduler tests**

```ts
import { afterEach, describe, expect, it, vi } from 'vitest'
import { createRuntimeStatusRefreshScheduler, isRuntimeStatusEventForSelection } from './runtimeStatusRefresh'

describe('runtime status refresh scheduler', () => {
  afterEach(() => vi.useRealTimers())

  it('coalesces multiple status events into one refresh', async () => {
    vi.useFakeTimers()
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimeStatusRefreshScheduler(refresh, 25)
    scheduler.request()
    scheduler.request()
    await vi.advanceTimersByTimeAsync(25)
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('runs one follow-up refresh when an event arrives in flight', async () => {
    vi.useFakeTimers()
    let finish!: () => void
    const refresh = vi.fn()
      .mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve }))
      .mockResolvedValue(undefined)
    const scheduler = createRuntimeStatusRefreshScheduler(refresh, 1)
    scheduler.request()
    await vi.advanceTimersByTimeAsync(1)
    scheduler.request()
    finish()
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(1)
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('cancels pending work when disposed', async () => {
    vi.useFakeTimers()
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimeStatusRefreshScheduler(refresh, 10)
    scheduler.request()
    scheduler.dispose()
    await vi.runAllTimersAsync()
    expect(refresh).not.toHaveBeenCalled()
  })

  it('accepts only runtime events for the active selected server', () => {
    expect(isRuntimeStatusEventForSelection({ resource: 'aifar.runtime', serverId: 'server-1' }, 'server-1', true)).toBe(true)
    expect(isRuntimeStatusEventForSelection({ resource: 'aifar.runtime', serverId: 'server-2' }, 'server-1', true)).toBe(false)
    expect(isRuntimeStatusEventForSelection({ resource: 'docker.summary', serverId: 'server-1' }, 'server-1', true)).toBe(false)
    expect(isRuntimeStatusEventForSelection({ resource: 'aifar.runtime', serverId: 'server-1' }, 'server-1', false)).toBe(false)
  })
})
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `cd web && pnpm exec vitest run src/containers/runtime/runtimeStatusRefresh.test.ts`

Expected: FAIL because `./runtimeStatusRefresh` does not exist.

- [ ] **Step 3: Implement the scheduler**

```ts
export function createRuntimeStatusRefreshScheduler(refresh: () => Promise<void>, delayMs = 50) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  let pending = false
  let disposed = false

  async function run() {
    timer = undefined
    if (disposed || !pending || running) return
    pending = false
    running = true
    try {
      await refresh()
    } finally {
      running = false
      if (pending && !disposed) schedule()
    }
  }

  function schedule() {
    if (disposed || running || timer !== undefined) return
    timer = globalThis.setTimeout(() => void run(), delayMs)
  }

  return {
    request() {
      if (disposed) return
      pending = true
      schedule()
    },
    dispose() {
      disposed = true
      pending = false
      if (timer !== undefined) globalThis.clearTimeout(timer)
      timer = undefined
    }
  }
}

export type RuntimeStatusEvent = { resource?: string; serverId?: string }

export function isRuntimeStatusEventForSelection(
  event: RuntimeStatusEvent | null | undefined,
  selectedServerId: string,
  runtimeActive: boolean
) {
  if (!runtimeActive || event?.resource !== 'aifar.runtime') return false
  return !event.serverId || event.serverId === selectedServerId
}
```

- [ ] **Step 4: Run the focused test and verify GREEN**

Run: `cd web && pnpm exec vitest run src/containers/runtime/runtimeStatusRefresh.test.ts`

Expected: 4 tests PASS.

- [ ] **Step 5: Integrate background refresh in `ContainersView.vue`**

Refactor `loadAifarRuntime` so its body can run through `withLoading` for manual actions or directly for background events. Change its signature to add `background = false`; replace its current opening `return withLoading(async () => {` with `const execute = async () => {`; replace the matching closing `})` with `}` and add `return background ? execute() : withLoading(execute)` before the function closes. Do not alter the statements inside the existing load body.

Then add the scheduler instance:

```ts
const runtimeStatusRefresh = createRuntimeStatusRefreshScheduler(
  () => loadAifarRuntime(
    true,
    runtimeResourceTab.value === 'pods',
    runtimeResourceTab.value === 'pods' && runtimePodStatsLoadedForCurrentScope.value,
    true
  )
)
```

Change the existing realtime watcher branch to schedule a reload only for the selected server while the Runtime tab is active:

```ts
if (isRuntimeStatusEventForSelection(event, selectedServerId.value, tab.value === 'aifar-runtime')) {
  runtimeCache.value = {}
  runtimeStatusRefresh.request()
}
```

Call `runtimeStatusRefresh.dispose()` from the existing `onBeforeUnmount` block.

- [ ] **Step 6: Run focused tests and commit**

Run: `cd web && pnpm exec vitest run src/containers/runtime/runtimeStatusRefresh.test.ts src/containers/runtime/api.test.ts`

Expected: all selected tests PASS.

```bash
git add web/src/containers/runtime/runtimeStatusRefresh.ts web/src/containers/runtime/runtimeStatusRefresh.test.ts web/src/views/ContainersView.vue
git commit -m "fix: refresh runtime status from realtime events"
```

---

### Task 2: Stateful Runtime Error Classification

**Files:**
- Create: `web/src/containers/runtime/logs.test.ts`
- Modify: `web/src/containers/runtime/logs.ts`
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeLogsTab.vue`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Produces: `parseRuntimeLogLines(lines: string[], context?: RuntimeLogParseContext): { lines: ParsedRuntimeLogLine[]; context: RuntimeLogParseContext }`.
- Produces: `RuntimeLogParseContext = { error: boolean }` and parsed rows with `errorContext: boolean`.
- Consumes: existing log response groups and `RuntimeLogRow` rendering/filtering.

- [ ] **Step 1: Write failing parser tests**

```ts
import { describe, expect, it } from 'vitest'
import { parseRuntimeLogLine, parseRuntimeLogLines } from './logs'

describe('runtime log parsing', () => {
  it('parses Spring timestamps and bracketed error levels', () => {
    expect(parseRuntimeLogLine('2026-07-25 10:20:30.123 [ERROR] gateway failed')).toMatchObject({
      time: '2026-07-25 10:20:30.123', level: 'ERROR', message: 'gateway failed'
    })
  })

  it('parses structured JSON logs', () => {
    expect(parseRuntimeLogLine('{"timestamp":"2026-07-25T10:20:30Z","level":"error","message":"database unavailable"}')).toMatchObject({
      time: '2026-07-25T10:20:30Z', level: 'ERROR', message: 'database unavailable'
    })
  })

  it('keeps a complete exception stack in error context', () => {
    const result = parseRuntimeLogLines([
      '2026-07-25 10:20:30.123 ERROR request failed',
      'java.lang.IllegalStateException: unavailable',
      '  at com.aifar.Service.run(Service.java:42)',
      'Caused by: java.net.ConnectException: refused',
      '2026-07-25 10:20:31.000 INFO recovered'
    ])
    expect(result.lines.map((line) => line.level)).toEqual(['ERROR', 'ERROR', 'ERROR', 'ERROR', 'INFO'])
    expect(result.context.error).toBe(false)
  })
})
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `cd web && pnpm exec vitest run src/containers/runtime/logs.test.ts`

Expected: FAIL on Spring/JSON parsing and missing `parseRuntimeLogLines`.

- [ ] **Step 3: Implement deterministic parsing and context inheritance**

Implement JSON extraction before text parsing; accept ISO and space-separated timestamps; normalize `WARNING` to `WARN` and `FATAL`/`SEVERE` to `ERROR`. Add `isRuntimeErrorContinuation` for exception names, `Caused by`, `Suppressed`, `at ...(...)`, and `... n more`. Only inherit error state for those known continuation patterns. Reset error context on a timestamped or explicitly levelled non-error record.

The public implementation must follow this shape:

```ts
export type RuntimeLogParseContext = { error: boolean }

export function parseRuntimeLogLines(lines: string[], context: RuntimeLogParseContext = { error: false }) {
  const next = { ...context }
  const parsed = lines.map((line) => {
    const row = parseRuntimeLogLine(line)
    const continuation = isRuntimeErrorContinuation(line)
    if (!row.level && next.error && continuation) row.level = 'ERROR'
    if (row.level) next.error = row.level === 'ERROR'
    else if (row.time !== '-') next.error = false
    return { ...row, errorContext: row.level === 'ERROR' }
  })
  return { lines: parsed, context: next }
}
```

- [ ] **Step 4: Run parser tests and verify GREEN**

Run: `cd web && pnpm exec vitest run src/containers/runtime/logs.test.ts`

Expected: all parser tests PASS.

- [ ] **Step 5: Preserve context per pod and expose error-only UI**

In `ContainersView.vue`, keep a `Map<string, RuntimeLogParseContext>` keyed by container name. Use `parseRuntimeLogLines` inside `runtimeLogRowsFromResponse`, save the returned context, and clear the map in `resetRuntimeLogView` and `clearRuntimeLogView`.

Add:

```ts
const runtimeLogErrorCount = computed(() => runtimeLogRows.value.filter((row) => row.level === 'ERROR').length)
function showRuntimeLogErrorsOnly() {
  runtimeLogLevelFilter.value = ['ERROR']
}
```

Expose both through the runtime provider context. Add a localized toolbar button and count using keys `containers.errorsOnly` and `containers.errorLogRows` in Chinese and English. Apply `is-error` to error rows and add a semantic red border/background without changing row height.

- [ ] **Step 6: Run focused tests and commit**

Run: `cd web && pnpm exec vitest run src/containers/runtime/logs.test.ts src/containers/runtime/useAifarRuntimeLogViewport.test.ts src/containers/runtime/runtimeRules.test.ts`

Expected: all selected tests PASS.

```bash
git add web/src/containers/runtime/logs.ts web/src/containers/runtime/logs.test.ts web/src/containers/runtime/types.ts web/src/views/ContainersView.vue web/src/containers/runtime/AifarRuntimeLogsTab.vue web/src/containers/runtime/runtime.css web/src/i18n/messages.ts
git commit -m "feat: distinguish runtime error stacks"
```

---

### Task 3: Terminal Flex Layout and Scrollback

**Files:**
- Create: `web/src/views/TerminalView.test.ts`
- Modify: `web/src/views/TerminalView.vue`

**Interfaces:**
- Consumes: existing `.content-body > section` flex behavior and xterm `Terminal` constructor.
- Produces: a terminal card that fills remaining desktop space, contains overflow, and retains at least 10,000 scrollback rows.

- [ ] **Step 1: Write a failing source contract test**

```ts
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const source = readFileSync(fileURLToPath(new URL('./TerminalView.vue', import.meta.url)), 'utf8')

describe('terminal viewport contract', () => {
  it('uses remaining flex space instead of viewport subtraction', () => {
    expect(source).toContain('flex: 1 1 auto;')
    expect(source).toContain('min-height: 360px;')
    expect(source).not.toContain('calc(100dvh - 176px)')
  })

  it('keeps large command output scrollable', () => {
    expect(source).toContain('scrollback: 10000')
    expect(source).toMatch(/\.terminal-box :deep\(\.xterm-viewport\)[\s\S]*overflow-y: auto/)
  })
})
```

- [ ] **Step 2: Run the focused test and verify RED**

Run: `cd web && pnpm exec vitest run src/views/TerminalView.test.ts`

Expected: FAIL because the terminal still contains viewport subtraction and no explicit scrollback.

- [ ] **Step 3: Implement the flex and scrollback contract**

Add `scrollback: 10000` to the xterm options. Change desktop layout to:

```css
.terminal-page {
  min-height: 0;
  overflow: hidden;
}

.terminal-panel {
  flex: 1 1 auto;
  height: auto;
  min-height: 360px;
  overflow: hidden !important;
}
```

Keep `.terminal-box` at `height: 100%`, `.xterm` at full size, and `.xterm-viewport { overflow-y: auto; }`. In the existing mobile media query, restore `overflow: visible` on the page and keep the explicit `560px` terminal card height.

- [ ] **Step 4: Run the focused test and commit**

Run: `cd web && pnpm exec vitest run src/views/TerminalView.test.ts`

Expected: 2 tests PASS.

```bash
git add web/src/views/TerminalView.vue web/src/views/TerminalView.test.ts
git commit -m "fix: keep terminal output visible"
```

---

### Task 4: Regression Verification and Project Memory

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: all changes from Tasks 1-3.
- Produces: fresh verification evidence and a concise reusable project conclusion.

- [ ] **Step 1: Run all frontend tests**

Run: `pnpm test:web`

Expected: exit code 0 with no failed Vitest tests.

- [ ] **Step 2: Run the production frontend build**

Run: `pnpm web:build`

Expected: `vue-tsc --noEmit` and Vite production build both exit 0.

- [ ] **Step 3: Verify the backend realtime collector contract**

Run from `backend/`: `go test ./internal/collector ./internal/httpapi`

Expected: both packages PASS, confirming snapshot events remain compatible.

- [ ] **Step 4: Check the final diff**

Run: `git diff --check`

Expected: no output and exit code 0.

Review `git diff --stat` and `git status --short`. Confirm only scoped source, test, documentation, and the pre-existing `memory.md` modifications are present.

- [ ] **Step 5: Append the reusable conclusion to `memory.md`**

Append under `## 2026-07-25`:

```markdown
- 问题：用户要求容器 Runtime 状态由后端事件自动更新、日志能识别完整错误堆栈，并修复 SSH 终端大量输出被底部遮挡。
- 结论：Containers 页面收到当前服务器的 `aifar.runtime` SSE 事件后会合并触发后台详情刷新；Runtime 日志支持常见文本/JSON 级别及异常续行继承并可一键只看错误；Terminal 改为剩余空间 flex 布局并保留 10000 行 scrollback。前端测试、构建及 collector/httpapi 后端测试通过。
```

- [ ] **Step 6: Commit the final memory update**

Stage only the exact intended memory hunk after inspecting it, preserving unrelated pre-existing content:

```bash
git add -p memory.md
git commit -m "docs: record runtime observability improvements"
```
