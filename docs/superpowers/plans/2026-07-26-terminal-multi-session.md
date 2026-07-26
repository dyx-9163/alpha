# Terminal Multi-Session Workspace Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a cached SSH terminal workspace with multiple independent connection tabs, background connection retention, and up to four simultaneously interactive split panes.

**Architecture:** Cache only `TerminalView` with Vue `KeepAlive`. Keep serializable tab, focus, and split selection in a pure workspace model owned by `TerminalView`; keep each xterm, WebSocket, and resize lifecycle inside an always-mounted `TerminalSessionPane`. Reuse the current terminal WebSocket endpoint and line-height-aware grid sizing without backend changes.

**Tech Stack:** Vue 3 script setup, TypeScript, Element Plus, xterm.js 5.5, Vue Router 4, Vitest 3, Vite 7.

## Global Constraints

- Follow `design/ant-design-system-portable202606.md`; spacing remains in 4 px increments and controls use existing AIFAR tokens.
- All user-visible text must have Chinese and English entries in `web/src/i18n/messages.ts`.
- Continue using `/api/v2/servers/{id}/terminal/ws` and `terminalProtocols()`; do not change backend APIs, permissions, database schema, or WebSocket protocol.
- The same server may have multiple independent sessions.
- At most four sessions are visible; background sessions remain connected and mounted.
- Layout is one full pane, two left/right panes, and three or four panes in a 2-by-2 grid; narrow screens stack panes vertically.
- Side-menu navigation must not disconnect sessions. Refresh, browser close, logout, and true application unmount must destroy all sessions.
- Network disconnect keeps history and requires manual reconnect; do not add automatic reconnect.
- Preserve 10,000 xterm scrollback rows and the existing line-height-aware `calculateTerminalGrid` behavior.
- Work test-first and commit each task separately. Do not stage the existing unrelated `memory.md` change.

---

### Task 1: Pure terminal workspace state

**Files:**
- Create: `web/src/terminal/sessions.ts`
- Create: `web/src/terminal/sessions.test.ts`

**Interfaces:**
- Produces: `TerminalConnectionState`, `TerminalSessionMeta`, `TerminalWorkspaceState`, `MAX_VISIBLE_TERMINALS`, `emptyTerminalWorkspace`, `nextSessionSequence`, `addSession`, `focusSession`, `addToSplit`, `removeFromSplit`, `closeSession`, and `updateSessionStatus`.
- Consumers: `TerminalView.vue` in Task 4.

- [ ] **Step 1: Write failing workspace transition tests**

Create `web/src/terminal/sessions.test.ts` with concrete cases for duplicate servers, focused-slot replacement, four-pane limit, background retention, and close repair:

```ts
import { describe, expect, it } from 'vitest'

import {
  addSession,
  addToSplit,
  closeSession,
  emptyTerminalWorkspace,
  focusSession,
  nextSessionSequence,
  removeFromSplit,
  type TerminalSessionMeta
} from './sessions'

function session(id: string, serverId = id): TerminalSessionMeta {
  return { id, serverId, label: id, sequence: 1, status: 'connecting' }
}

describe('terminal workspace sessions', () => {
  it('allows duplicate server sessions and sequences them independently', () => {
    let state = addSession(emptyTerminalWorkspace(), session('a', 'server-1'))
    state = addSession(state, { ...session('b', 'server-1'), sequence: 2 })
    expect(state.sessions.map((item) => item.serverId)).toEqual(['server-1', 'server-1'])
    expect(nextSessionSequence(state.sessions, 'server-1')).toBe(3)
  })

  it('replaces only the focused slot when a background tab is selected', () => {
    let state = addSession(emptyTerminalWorkspace(), session('a'))
    state = addToSplit(addSession(state, session('b')).state, 'a').state
    state = addSession(state, session('c'))
    expect(state.visibleIds).toEqual(['b', 'c'])
    state = focusSession(state, 'a')
    expect(state.visibleIds).toEqual(['b', 'a'])
    expect(state.focusedId).toBe('a')
  })

  it('rejects a fifth visible pane without removing its session', () => {
    let state = emptyTerminalWorkspace()
    for (const id of ['a', 'b', 'c', 'd', 'e']) state = addSession(state, session(id))
    state = { ...state, visibleIds: ['a', 'b', 'c', 'd'], focusedId: 'd' }
    const result = addToSplit(state, 'e')
    expect(result.limitReached).toBe(true)
    expect(result.state.visibleIds).toEqual(['a', 'b', 'c', 'd'])
    expect(result.state.sessions.some((item) => item.id === 'e')).toBe(true)
  })

  it('hides a pane without deleting its background session', () => {
    let state = addSession(emptyTerminalWorkspace(), session('a'))
    state = addToSplit(addSession(state, session('b')).state, 'a').state
    state = removeFromSplit(state, 'a')
    expect(state.visibleIds).toEqual(['b'])
    expect(state.sessions.map((item) => item.id)).toEqual(['a', 'b'])
  })

  it('closes a focused pane without promoting an unrelated background tab', () => {
    const state = {
      sessions: [session('a'), session('b'), session('c')],
      visibleIds: ['a', 'b'],
      focusedId: 'b'
    }
    expect(closeSession(state, 'b')).toEqual({
      sessions: [session('a'), session('c')],
      visibleIds: ['a'],
      focusedId: 'a'
    })
  })
})
```

- [ ] **Step 2: Run the web tests and confirm the new suite fails**

Run: `pnpm test:web`

Expected: FAIL because `./sessions` does not exist.

- [ ] **Step 3: Implement immutable workspace transitions**

Create `web/src/terminal/sessions.ts` with these exact public types and transition rules:

```ts
export type TerminalConnectionState = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface TerminalSessionMeta {
  id: string
  serverId: string
  label: string
  sequence: number
  status: TerminalConnectionState
}

export interface TerminalWorkspaceState {
  sessions: TerminalSessionMeta[]
  visibleIds: string[]
  focusedId: string | null
}

export const MAX_VISIBLE_TERMINALS = 4

export function emptyTerminalWorkspace(): TerminalWorkspaceState {
  return { sessions: [], visibleIds: [], focusedId: null }
}

export function nextSessionSequence(sessions: TerminalSessionMeta[], serverId: string) {
  return sessions.reduce((max, item) => item.serverId === serverId ? Math.max(max, item.sequence) : max, 0) + 1
}

function replaceFocusedSlot(state: TerminalWorkspaceState, id: string) {
  if (!state.visibleIds.length) return [id]
  const index = Math.max(0, state.visibleIds.indexOf(state.focusedId ?? ''))
  return state.visibleIds.map((visibleId, position) => position === index ? id : visibleId)
}

export function addSession(state: TerminalWorkspaceState, item: TerminalSessionMeta): TerminalWorkspaceState {
  return {
    sessions: [...state.sessions, item],
    visibleIds: replaceFocusedSlot(state, item.id),
    focusedId: item.id
  }
}

export function focusSession(state: TerminalWorkspaceState, id: string): TerminalWorkspaceState {
  if (!state.sessions.some((item) => item.id === id)) return state
  return {
    ...state,
    visibleIds: state.visibleIds.includes(id) ? state.visibleIds : replaceFocusedSlot(state, id),
    focusedId: id
  }
}

export function addToSplit(state: TerminalWorkspaceState, id: string) {
  if (state.visibleIds.includes(id)) return { state: { ...state, focusedId: id }, added: false, limitReached: false }
  if (state.visibleIds.length >= MAX_VISIBLE_TERMINALS) return { state, added: false, limitReached: true }
  return {
    state: { ...state, visibleIds: [...state.visibleIds, id], focusedId: id },
    added: true,
    limitReached: false
  }
}

export function removeFromSplit(state: TerminalWorkspaceState, id: string): TerminalWorkspaceState {
  const visibleIds = state.visibleIds.filter((visibleId) => visibleId !== id)
  return {
    ...state,
    visibleIds,
    focusedId: state.focusedId === id ? (visibleIds[0] ?? null) : state.focusedId
  }
}

export function closeSession(state: TerminalWorkspaceState, id: string): TerminalWorkspaceState {
  const visibleIds = state.visibleIds.filter((visibleId) => visibleId !== id)
  return {
    sessions: state.sessions.filter((item) => item.id !== id),
    visibleIds,
    focusedId: state.focusedId === id ? (visibleIds[0] ?? null) : state.focusedId
  }
}

export function updateSessionStatus(
  state: TerminalWorkspaceState,
  id: string,
  status: TerminalConnectionState
): TerminalWorkspaceState {
  return {
    ...state,
    sessions: state.sessions.map((item) => item.id === id ? { ...item, status } : item)
  }
}
```

- [ ] **Step 4: Run tests and confirm the workspace model passes**

Run: `pnpm test:web`

Expected: PASS, including all five `sessions.test.ts` cases.

- [ ] **Step 5: Commit the pure state model**

```powershell
git add -- web/src/terminal/sessions.ts web/src/terminal/sessions.test.ts
git commit -m "feat: add terminal workspace state"
```

---

### Task 2: Per-session WebSocket controller

**Files:**
- Create: `web/src/terminal/sessionController.ts`
- Create: `web/src/terminal/sessionController.test.ts`

**Interfaces:**
- Consumes: `TerminalConnectionState` from `sessions.ts`.
- Produces: `createTerminalSessionController(options)` returning `connect`, `disconnect`, `send`, and `dispose`.
- Consumers: `TerminalSessionPane.vue` in Task 3.

- [ ] **Step 1: Write failing controller isolation and stale-callback tests**

Create fakes inside `web/src/terminal/sessionController.test.ts` and cover two independent sockets plus disposal:

```ts
import { describe, expect, it, vi } from 'vitest'

import { createTerminalSessionController, type TerminalSocketLike } from './sessionController'

function fakeSocket(): TerminalSocketLike {
  return {
    binaryType: 'blob',
    readyState: 0,
    onopen: null,
    onmessage: null,
    onerror: null,
    onclose: null,
    send: vi.fn(),
    close: vi.fn()
  }
}

describe('terminal session controller', () => {
  it('routes socket output and state to only its own terminal', () => {
    const firstSocket = fakeSocket()
    const secondSocket = fakeSocket()
    const firstWrite = vi.fn()
    const secondWrite = vi.fn()
    const first = createTerminalSessionController({
      terminal: { write: firstWrite }, createSocket: () => firstSocket,
      onState: vi.fn(), connectionFailedText: 'failed', disconnectedText: 'closed'
    })
    const second = createTerminalSessionController({
      terminal: { write: secondWrite }, createSocket: () => secondSocket,
      onState: vi.fn(), connectionFailedText: 'failed', disconnectedText: 'closed'
    })
    first.connect()
    second.connect()
    firstSocket.onmessage?.({ data: 'first' })
    expect(firstWrite).toHaveBeenCalledWith('first')
    expect(secondWrite).not.toHaveBeenCalled()
  })

  it('ignores late messages after dispose and closes once', () => {
    const socket = fakeSocket()
    const write = vi.fn()
    const controller = createTerminalSessionController({
      terminal: { write }, createSocket: () => socket,
      onState: vi.fn(), connectionFailedText: 'failed', disconnectedText: 'closed'
    })
    controller.connect()
    controller.dispose()
    socket.onmessage?.({ data: 'late' })
    socket.onclose?.({})
    expect(write).not.toHaveBeenCalled()
    expect(socket.close).toHaveBeenCalledTimes(1)
  })
})
```

- [ ] **Step 2: Run tests and verify the missing controller failure**

Run: `pnpm test:web`

Expected: FAIL because `./sessionController` does not exist.

- [ ] **Step 3: Implement generation-guarded socket ownership**

Create `web/src/terminal/sessionController.ts`. Define a structural socket interface so tests do not need a browser WebSocket, increment a generation on every connect/disconnect/dispose, and guard every callback:

```ts
import type { TerminalConnectionState } from './sessions'

export interface TerminalSocketLike {
  binaryType: BinaryType
  readyState: number
  onopen: ((event: any) => void) | null
  onmessage: ((event: { data: any }) => void) | null
  onerror: ((event: any) => void) | null
  onclose: ((event: any) => void) | null
  send(data: string): void
  close(): void
}

interface ControllerOptions {
  terminal: { write(data: string | Uint8Array): void }
  createSocket(): TerminalSocketLike
  onState(state: TerminalConnectionState): void
  connectionFailedText: string
  disconnectedText: string
}

export function createTerminalSessionController(options: ControllerOptions) {
  let socket: TerminalSocketLike | null = null
  let generation = 0
  let disposed = false
  let currentState: TerminalConnectionState = 'disconnected'

  function setState(state: TerminalConnectionState) {
    currentState = state
    options.onState(state)
  }

  function writeData(data: unknown, expectedGeneration: number) {
    if (data instanceof ArrayBuffer) {
      if (!disposed && generation === expectedGeneration) options.terminal.write(new Uint8Array(data))
      return
    }
    if (data instanceof Blob) {
      void data.arrayBuffer().then((buffer) => {
        if (!disposed && generation === expectedGeneration) options.terminal.write(new Uint8Array(buffer))
      })
      return
    }
    if (!disposed && generation === expectedGeneration) options.terminal.write(String(data))
  }

  function closeCurrent() {
    const current = socket
    socket = null
    if (!current) return
    current.onopen = null
    current.onmessage = null
    current.onerror = null
    current.onclose = null
    current.close()
  }

  function connect() {
    if (disposed) return
    generation += 1
    closeCurrent()
    const expectedGeneration = generation
    setState('connecting')
    const current = options.createSocket()
    socket = current
    current.binaryType = 'arraybuffer'
    current.onopen = () => {
      if (!disposed && generation === expectedGeneration) setState('connected')
    }
    current.onmessage = (event) => writeData(event.data, expectedGeneration)
    current.onerror = () => {
      if (disposed || generation !== expectedGeneration) return
      options.terminal.write(`\r\n${options.connectionFailedText}\r\n`)
      setState('error')
    }
    current.onclose = () => {
      if (disposed || generation !== expectedGeneration) return
      if (currentState === 'error') return
      options.terminal.write(`\r\n${options.disconnectedText}\r\n`)
      setState('disconnected')
    }
  }

  function disconnect() {
    if (disposed) return
    generation += 1
    closeCurrent()
    setState('disconnected')
  }

  function send(data: string) {
    if (socket?.readyState === 1) socket.send(data)
  }

  function dispose() {
    if (disposed) return
    disposed = true
    generation += 1
    closeCurrent()
  }

  return { connect, disconnect, send, dispose }
}
```

- [ ] **Step 4: Add and pass error-state and deferred Blob regressions**

Add these tests before running the suite:

```ts
it('keeps an error state when the browser follows onerror with onclose', () => {
  const socket = fakeSocket()
  const onState = vi.fn()
  const controller = createTerminalSessionController({
    terminal: { write: vi.fn() }, createSocket: () => socket,
    onState, connectionFailedText: 'failed', disconnectedText: 'closed'
  })
  controller.connect()
  socket.onerror?.({})
  socket.onclose?.({})
  expect(onState).toHaveBeenLastCalledWith('error')
})

it('does not write a Blob that resolves after disposal', async () => {
  const socket = fakeSocket()
  const write = vi.fn()
  let resolveBuffer!: (value: ArrayBuffer) => void
  const payload = new Blob(['late'])
  vi.spyOn(payload, 'arrayBuffer').mockImplementation(() => new Promise<ArrayBuffer>((resolve) => { resolveBuffer = resolve }))
  const controller = createTerminalSessionController({
    terminal: { write }, createSocket: () => socket,
    onState: vi.fn(), connectionFailedText: 'failed', disconnectedText: 'closed'
  })
  controller.connect()
  socket.onmessage?.({ data: payload })
  controller.dispose()
  resolveBuffer(new ArrayBuffer(4))
  await Promise.resolve()
  expect(write).not.toHaveBeenCalled()
})
```

Run: `pnpm test:web`

Expected: PASS, with the final state remaining `error` and the disposed Blob producing no write.

- [ ] **Step 5: Commit the controller**

```powershell
git add -- web/src/terminal/sessionController.ts web/src/terminal/sessionController.test.ts
git commit -m "feat: isolate terminal session sockets"
```

---

### Task 3: Independent terminal session pane

**Files:**
- Create: `web/src/terminal/TerminalSessionPane.vue`
- Create: `web/src/terminal/TerminalSessionPane.test.ts`
- Test: `web/src/terminal/grid.test.ts`

**Interfaces:**
- Consumes: `TerminalSessionMeta`, `createTerminalSessionController`, `calculateTerminalGrid`, `terminalUrl`, and `terminalProtocols`.
- Produces: component props `session`, `serverName`, `serverMeta`, `visible`, and `focused`; emits `status` and `focus`; exposes `disconnect`, `reconnect`, and `refit`.
- Consumers: `TerminalView.vue` in Task 4.

- [ ] **Step 1: Write a failing source contract for per-pane ownership**

Create `web/src/terminal/TerminalSessionPane.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import source from './TerminalSessionPane.vue?raw'

describe('TerminalSessionPane contract', () => {
  it('owns one terminal controller and exposes focused-session actions', () => {
    expect(source).toContain('createTerminalSessionController')
    expect(source).toContain('scrollback: 10000')
    expect(source).toContain('defineExpose({ disconnect, reconnect, refit: scheduleTerminalFit })')
  })

  it('keeps hidden output alive and refits when shown or reactivated', () => {
    expect(source).toContain("watch(() => props.visible")
    expect(source).toContain('onActivated(scheduleTerminalFit)')
    expect(source).toContain('onBeforeUnmount(disposeSession)')
  })
})
```

- [ ] **Step 2: Run tests and confirm the missing component failure**

Run: `pnpm test:web`

Expected: FAIL because `TerminalSessionPane.vue` does not exist.

- [ ] **Step 3: Move one-session xterm lifecycle into the pane**

Create the component with one root `.terminal-session-pane`, a pane header, and a `.terminal-box`. Use this script shape:

```ts
<script setup lang="ts">
import { nextTick, onActivated, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Terminal } from '@xterm/xterm'
import { terminalProtocols, terminalUrl } from '../api/client'
import { useI18n } from '../i18n'
import { calculateTerminalGrid } from './grid'
import { createTerminalSessionController } from './sessionController'
import type { TerminalConnectionState, TerminalSessionMeta } from './sessions'

const props = defineProps<{
  session: TerminalSessionMeta
  serverName: string
  serverMeta: string
  visible: boolean
  focused: boolean
}>()
const emit = defineEmits<{
  status: [id: string, status: TerminalConnectionState]
  focus: [id: string]
}>()
const { t } = useI18n()
const terminalEl = ref<HTMLElement>()
let terminal: Terminal | null = null
let controller: ReturnType<typeof createTerminalSessionController> | null = null
let resizeObserver: ResizeObserver | null = null

function mountSession() {
  if (!terminalEl.value) return
  terminal = new Terminal({
    cursorBlink: true,
    fontFamily: '"Cascadia Mono", Consolas, "SFMono-Regular", monospace',
    fontSize: 13,
    lineHeight: 1.2,
    scrollback: 10000
  })
  terminal.open(terminalEl.value)
  controller = createTerminalSessionController({
    terminal,
    createSocket: () => new WebSocket(terminalUrl(props.session.serverId), terminalProtocols()),
    onState: (status) => emit('status', props.session.id, status),
    connectionFailedText: t('terminal.connectionFailed'),
    disconnectedText: t('terminal.disconnectedMessage')
  })
  terminal.onData((data) => controller?.send(data))
  resizeObserver = new ResizeObserver(scheduleTerminalFit)
  resizeObserver.observe(terminalEl.value)
  controller.connect()
  scheduleTerminalFit()
}

function disconnect() { controller?.disconnect() }
function reconnect() { controller?.connect() }

function scheduleTerminalFit() {
  if (!props.visible) return
  void nextTick(() => {
    fitTerminal()
    window.requestAnimationFrame(fitTerminal)
  })
}

function fitTerminal() {
  if (!terminal || !terminalEl.value || !props.visible) return
  const style = window.getComputedStyle(terminalEl.value)
  const width = terminalEl.value.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)
  const height = terminalEl.value.clientHeight - parseFloat(style.paddingTop) - parseFloat(style.paddingBottom)
  const measure = terminalEl.value.querySelector('.xterm-char-measure-element') as HTMLElement | null
  const rect = measure?.getBoundingClientRect()
  const size = calculateTerminalGrid({
    width,
    height,
    measuredCellWidth: rect?.width ?? 0,
    measuredCharHeight: rect?.height ?? 0,
    lineHeight: Number(terminal.options.lineHeight)
  })
  if (size.cols !== terminal.cols || size.rows !== terminal.rows) terminal.resize(size.cols, size.rows)
}

function disposeSession() {
  controller?.dispose()
  resizeObserver?.disconnect()
  terminal?.dispose()
  controller = null
  resizeObserver = null
  terminal = null
}

watch(() => props.visible, (visible) => { if (visible) scheduleTerminalFit() })
onMounted(mountSession)
onActivated(scheduleTerminalFit)
onBeforeUnmount(disposeSession)
defineExpose({ disconnect, reconnect, refit: scheduleTerminalFit })
</script>
```

Use this template so clicking any pane focuses only that session:

```vue
<template>
  <section class="terminal-session-pane" :class="{ 'is-focused': focused }" @mousedown="emit('focus', session.id)">
    <header class="terminal-session-head">
      <div><strong>{{ serverName }}</strong><span>{{ serverMeta }}</span></div>
      <span class="status-pill" :class="session.status === 'connected' ? 'success' : ''">
        {{ t(`terminal.status.${session.status}`) }}
      </span>
    </header>
    <div ref="terminalEl" class="terminal-box" />
  </section>
</template>
```

Move the current terminal CSS into the pane and add the focused boundary:

```css
.terminal-session-pane { min-width:0; min-height:0; display:grid; grid-template-rows:auto minmax(0, 1fr); gap:8px; padding:8px; border:1px solid var(--aifar-border-soft); border-radius:var(--aifar-radius-lg); }
.terminal-session-pane.is-focused { border-color:var(--aifar-primary); box-shadow:0 0 0 1px color-mix(in srgb, var(--aifar-primary) 28%, transparent); }
.terminal-box { min-height:0; height:100%; box-sizing:border-box; overflow:hidden; padding:8px 10px; background:var(--aifar-code-bg); }
.terminal-box :deep(.xterm) { width:100%; height:100%; }
.terminal-box :deep(.xterm-viewport) { overflow-y:auto; }
.terminal-box :deep(.xterm-screen) { padding-top:2px; padding-left:2px; }
```

- [ ] **Step 4: Keep grid sizing tests green after extraction**

Run: `pnpm test:web`

Expected: PASS for the pane contract, controller tests, session tests, and existing `grid.test.ts` cases.

- [ ] **Step 5: Commit the session pane**

```powershell
git add -- web/src/terminal/TerminalSessionPane.vue web/src/terminal/TerminalSessionPane.test.ts
git commit -m "feat: add independent terminal session pane"
```

---

### Task 4: Multi-tab coordinator and automatic split grid

**Files:**
- Modify: `web/src/views/TerminalView.vue`
- Modify: `web/src/views/TerminalView.test.ts`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: all Task 1 workspace transitions and the Task 3 pane component/exposed methods.
- Produces: top connection tabs, focused-session controls, maximum-four split selection, responsive pane grid, and localized user feedback.

- [ ] **Step 1: Replace the single-session source test with failing workspace contracts**

Update `web/src/views/TerminalView.test.ts` to assert the coordinator structure while retaining the viewport regression expectations in the pane test:

```ts
import { describe, expect, it } from 'vitest'

import source from './TerminalView.vue?raw'

describe('terminal multi-session workspace contract', () => {
  it('renders every session pane and hides background panes without unmounting them', () => {
    expect(source).toContain('v-for="session in workspace.sessions"')
    expect(source).toContain('v-show="workspace.visibleIds.includes(session.id)"')
    expect(source).toContain('<TerminalSessionPane')
  })

  it('supports tab focus, explicit split membership, and focused actions', () => {
    expect(source).toContain('@click="selectSession(session.id)"')
    expect(source).toContain('@click.stop="toggleSplit(session.id)"')
    expect(source).toContain('@click.stop="closeTerminalSession(session.id)"')
    expect(source).toContain('focusedPane()?.disconnect()')
    expect(source).toContain('focusedPane()?.reconnect()')
  })

  it('uses automatic one, two, and four-cell layouts with narrow-screen stacking', () => {
    expect(source).toContain(':data-pane-count="workspace.visibleIds.length"')
    expect(source).toMatch(/data-pane-count="2"[\s\S]*grid-template-columns: repeat\(2, minmax\(0, 1fr\)\)/)
    expect(source).toMatch(/data-pane-count="3"[\s\S]*data-pane-count="4"/)
    expect(source).toMatch(/@media \(max-width: 760px\)[\s\S]*grid-template-columns: 1fr/)
  })
})
```

- [ ] **Step 2: Run tests and verify the coordinator contracts fail**

Run: `pnpm test:web`

Expected: FAIL because `TerminalView.vue` still owns a single terminal and socket.

- [ ] **Step 3: Implement the coordinator state and actions**

Remove xterm/WebSocket ownership from `TerminalView.vue`. Import `TerminalSessionPane`, `ElMessageBox`, and Task 1 transitions. Use this core state and action shape:

```ts
interface ServerSummary { id: string; name: string; host?: string; port?: number }
interface PaneExpose { disconnect(): void; reconnect(): void; refit(): void }

const servers = ref<ServerSummary[]>([])
const serverId = ref('')
const workspace = ref(emptyTerminalWorkspace())
const paneRefs = new Map<string, PaneExpose>()
const focusedSession = computed(() => workspace.value.sessions.find((item) => item.id === workspace.value.focusedId))

function serverFor(id: string) {
  return servers.value.find((server) => server.id === id)
}

function serverMeta(id: string) {
  const server = serverFor(id)
  if (!server) return id
  return `${server.host || server.id}:${server.port || 22}`
}

function createSessionId() {
  return globalThis.crypto?.randomUUID?.() ?? `terminal-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function newConnection() {
  const server = servers.value.find((item) => item.id === serverId.value)
  if (!server || !canConnectTerminal.value) return
  const sequence = nextSessionSequence(workspace.value.sessions, server.id)
  const item: TerminalSessionMeta = {
    id: createSessionId(),
    serverId: server.id,
    label: `${server.name} · ${sequence}`,
    sequence,
    status: 'connecting'
  }
  workspace.value = addSession(workspace.value, item)
}

function selectSession(id: string) {
  workspace.value = focusSession(workspace.value, id)
}

function toggleSplit(id: string) {
  if (workspace.value.visibleIds.includes(id)) {
    workspace.value = removeFromSplit(workspace.value, id)
    return
  }
  const result = addToSplit(workspace.value, id)
  workspace.value = result.state
  if (result.limitReached) ElMessage.warning(t('terminal.splitLimit'))
}

function updateStatus(id: string, status: TerminalConnectionState) {
  workspace.value = updateSessionStatus(workspace.value, id, status)
}

async function closeTerminalSession(id: string) {
  const item = workspace.value.sessions.find((session) => session.id === id)
  if (!item) return
  if (item.status === 'connecting' || item.status === 'connected') {
    try {
      await ElMessageBox.confirm(t('terminal.closeConnectedConfirm', { target: item.label }), t('terminal.closeConfirmTitle'), {
        type: 'warning'
      })
    } catch {
      return
    }
  }
  paneRefs.delete(id)
  workspace.value = closeSession(workspace.value, id)
}

function setPaneRef(id: string, pane: unknown) {
  if (pane) paneRefs.set(id, pane as PaneExpose)
  else paneRefs.delete(id)
}

function focusedPane() {
  return workspace.value.focusedId ? paneRefs.get(workspace.value.focusedId) : undefined
}
function disconnectFocused() { focusedPane()?.disconnect() }
function reconnectFocused() { focusedPane()?.reconnect() }
```

- [ ] **Step 4: Render tabs and keep every pane mounted**

Replace the current single-session buttons with focused-session actions:

```vue
<el-button type="primary" :disabled="!canConnectTerminal || !serverId" @click="newConnection">
  {{ t('terminal.newConnection') }}
</el-button>
<el-button :disabled="focusedSession?.status !== 'connected'" @click="disconnectFocused">
  {{ t('terminal.disconnect') }}
</el-button>
<el-button :disabled="!focusedSession || focusedSession.status === 'connected' || focusedSession.status === 'connecting'" @click="reconnectFocused">
  {{ t('terminal.reconnect') }}
</el-button>
```

Use one horizontally scrollable tab strip. Each tab shows label and status dot, calls `selectSession`, and contains split and close icon buttons with stopped propagation. Render the pane list with `v-for` over all sessions and `v-show` for split visibility:

```vue
<div class="terminal-tabs" role="tablist">
  <div
    v-for="session in workspace.sessions"
    :key="session.id"
    class="terminal-tab"
    :class="{ 'is-active': workspace.focusedId === session.id, 'is-error': session.status === 'error' }"
    role="tab"
    :tabindex="workspace.focusedId === session.id ? 0 : -1"
    @click="selectSession(session.id)"
    @keydown.enter="selectSession(session.id)"
  >
    <span class="terminal-tab-status" :data-status="session.status" />
    <span>{{ session.label }}</span>
    <button
      type="button"
      :aria-label="t(workspace.visibleIds.includes(session.id) ? 'terminal.removeFromSplit' : 'terminal.addToSplit')"
      @click.stop="toggleSplit(session.id)"
    >{{ workspace.visibleIds.includes(session.id) ? '−' : '+' }}</button>
    <button type="button" :aria-label="t('terminal.closeSession')" @click.stop="closeTerminalSession(session.id)">×</button>
  </div>
</div>

<div class="terminal-grid" :data-pane-count="workspace.visibleIds.length">
  <TerminalSessionPane
    v-for="session in workspace.sessions"
    v-show="workspace.visibleIds.includes(session.id)"
    :key="session.id"
    :ref="(pane) => setPaneRef(session.id, pane)"
    :session="session"
    :server-name="serverFor(session.serverId)?.name ?? session.serverId"
    :server-meta="serverMeta(session.serverId)"
    :visible="workspace.visibleIds.includes(session.id)"
    :focused="workspace.focusedId === session.id"
    @focus="selectSession"
    @status="updateStatus"
  />
</div>
```

Preserve the existing server workbench link and permission tooltip.

- [ ] **Step 5: Add the exact automatic grid CSS**

```css
.terminal-tabs { display:flex; gap:8px; overflow-x:auto; min-height:40px; }
.terminal-tab { display:flex; align-items:center; gap:8px; flex:0 0 auto; padding:8px 12px; border:1px solid var(--aifar-border-soft); border-radius:var(--aifar-radius); background:#fff; color:var(--aifar-text-secondary); }
.terminal-tab.is-active { border-color:var(--aifar-primary); background:var(--aifar-primary-soft); color:var(--aifar-primary-strong); }
.terminal-tab-status { width:8px; height:8px; border-radius:50%; background:var(--aifar-text-tertiary); }
.terminal-tab-status[data-status="connecting"] { background:var(--aifar-warning); }
.terminal-tab-status[data-status="connected"] { background:var(--aifar-success); }
.terminal-tab-status[data-status="error"] { background:var(--aifar-danger); }
.terminal-grid { flex:1 1 auto; min-height:360px; display:grid; grid-template-columns:1fr; gap:8px; overflow:hidden; }
.terminal-grid[data-pane-count="2"] { grid-template-columns:repeat(2, minmax(0, 1fr)); }
.terminal-grid[data-pane-count="3"],
.terminal-grid[data-pane-count="4"] { grid-template-columns:repeat(2, minmax(0, 1fr)); grid-template-rows:repeat(2, minmax(0, 1fr)); }

@media (max-width:760px) {
  .terminal-grid,
  .terminal-grid[data-pane-count="2"],
  .terminal-grid[data-pane-count="3"],
  .terminal-grid[data-pane-count="4"] {
    grid-template-columns:1fr;
    grid-template-rows:none;
    overflow:visible;
  }
}
```

- [ ] **Step 6: Add Chinese and English messages**

Add matching keys in both locale blocks:

```ts
'terminal.newConnection': '新建连接',
'terminal.reconnect': '重新连接',
'terminal.addToSplit': '加入分屏',
'terminal.removeFromSplit': '移出分屏',
'terminal.closeSession': '关闭会话',
'terminal.closeConfirmTitle': '关闭终端会话',
'terminal.splitLimit': '最多同时显示 4 个终端。',
'terminal.closeConnectedConfirm': ({ target } = {}) => `关闭 ${target ?? '当前终端'} 会断开 SSH 连接，是否继续？`,
'terminal.status.connecting': '连接中',
'terminal.status.connected': '已连接',
'terminal.status.disconnected': '已断开',
'terminal.status.error': '连接失败',
```

```ts
'terminal.newConnection': 'New connection',
'terminal.reconnect': 'Reconnect',
'terminal.addToSplit': 'Add to split',
'terminal.removeFromSplit': 'Remove from split',
'terminal.closeSession': 'Close session',
'terminal.closeConfirmTitle': 'Close terminal session',
'terminal.splitLimit': 'Up to 4 terminals can be visible at once.',
'terminal.closeConnectedConfirm': ({ target } = {}) => `Closing ${target ?? 'this terminal'} disconnects its SSH session. Continue?`,
'terminal.status.connecting': 'Connecting',
'terminal.status.connected': 'Connected',
'terminal.status.disconnected': 'Disconnected',
'terminal.status.error': 'Connection failed',
```

- [ ] **Step 7: Run web tests and production build**

Run: `pnpm test:web`

Expected: PASS for all session, controller, pane, grid, and view contracts.

Run: `pnpm web:build`

Expected: PASS for `vue-tsc --noEmit` and Vite production build. Existing Rollup annotation and chunk-size warnings are acceptable; new TypeScript or Vue template errors are not.

- [ ] **Step 8: Commit the terminal workspace UI**

```powershell
git add -- web/src/views/TerminalView.vue web/src/views/TerminalView.test.ts web/src/i18n/messages.ts
git commit -m "feat: add terminal tabs and split panes"
```

---

### Task 5: Cache only TerminalView and close the implementation

**Files:**
- Modify: `web/src/App.vue`
- Create: `web/src/App.test.ts`
- Modify: `web/src/views/TerminalView.vue`
- Modify: `memory.md` only for the required final project note; do not stage it unless the user separately authorizes committing project memory.

**Interfaces:**
- Consumes: `TerminalView` and all pane cleanup hooks from Tasks 3–4.
- Produces: side-menu persistence through `KeepAlive`, deterministic logout cleanup, and final verification evidence.

- [ ] **Step 1: Write the failing private-route cache contract**

Create `web/src/App.test.ts`:

```ts
import { describe, expect, it } from 'vitest'

import source from './App.vue?raw'

describe('private route cache contract', () => {
  it('caches only TerminalView inside the authenticated layout', () => {
    expect(source).toContain('<router-view v-slot="{ Component }">')
    expect(source).toContain('<keep-alive include="TerminalView">')
    expect(source).toContain('<component :is="Component" />')
  })

  it('keeps the public route outside the terminal cache', () => {
    expect(source).toContain('<router-view v-if="$route.meta.public" />')
    expect(source.indexOf('v-if="$route.meta.public"')).toBeLessThan(source.indexOf('<keep-alive include="TerminalView">'))
  })
})
```

- [ ] **Step 2: Run tests and verify the KeepAlive contract fails**

Run: `pnpm test:web`

Expected: FAIL because the private content body still contains a plain `<router-view />`.

- [ ] **Step 3: Cache only the named terminal component**

Replace the private content outlet in `web/src/App.vue`:

```vue
<div class="content-body">
  <router-view v-slot="{ Component }">
    <keep-alive include="TerminalView">
      <component :is="Component" />
    </keep-alive>
  </router-view>
</div>
```

Add the explicit component name at the top of `TerminalView.vue` script setup so `KeepAlive#include` is not dependent on filename inference:

```ts
defineOptions({ name: 'TerminalView' })
```

Do not add `onDeactivated` socket cleanup. Keep per-pane cleanup in `onBeforeUnmount` so side-menu navigation preserves sessions while logout destroys them when the private layout is removed.

- [ ] **Step 4: Run all required frontend gates**

Run: `pnpm test:web`

Expected: PASS, including `App.test.ts` and all terminal suites.

Run: `pnpm web:build`

Expected: PASS with no new TypeScript or Vue build errors.

Run: `git diff --check`

Expected: no output and exit code 0.

- [ ] **Step 5: Perform a local manual smoke test when the panel is available**

Verify these exact behaviors without changing any remote server state beyond interactive SSH commands chosen for the smoke test:

1. Open two independent sessions to the same server and one session to another server.
2. Run `printf 'session-one\n'`, `printf 'session-two\n'`, and `printf 'session-three\n'` in their respective terminals and verify output does not cross panes.
3. Add all three to the split workspace and verify the 2-by-2 layout leaves one unused cell.
4. Switch to Servers and back; verify all three prompts and scrollback buffers remain and connections did not reopen.
5. Disconnect one focused session and verify the other two remain interactive.
6. Reconnect the disconnected tab manually and verify its existing scrollback remains above the new connection output.
7. Close one connected tab, cancel the confirmation, and verify it remains connected; confirm on the second attempt and verify only that socket closes.
8. Log out and verify all remaining sockets close.

If no authenticated local panel/backend is running, report this smoke test as not performed rather than claiming it passed.

- [ ] **Step 6: Commit the route cache and contract test**

```powershell
git add -- web/src/App.vue web/src/App.test.ts web/src/views/TerminalView.vue
git commit -m "fix: keep terminal sessions across navigation"
```

- [ ] **Step 7: Append the required project memory note without staging unrelated history**

Append a concise question/conclusion entry to `memory.md` recording multi-session tabs, four-pane split behavior, KeepAlive navigation persistence, test counts, build status, and whether manual smoke was performed. Leave `memory.md` unstaged unless the user explicitly asks to commit it.

## Completion Criteria

- Multiple same-server and cross-server terminal tabs operate independently.
- Up to four selected panes remain simultaneously interactive; all other tabs stay connected in the background.
- Side-menu navigation preserves sockets, history, focus, and split selection.
- Manual disconnect/reconnect and confirmed close affect only the focused or selected session.
- Refresh/logout/true unmount dispose all xterm, observer, and WebSocket resources.
- No backend API, database, permission, or WebSocket protocol changes are present.
- `pnpm test:web`, `pnpm web:build`, and `git diff --check` pass.
- Manual smoke status is reported truthfully.
