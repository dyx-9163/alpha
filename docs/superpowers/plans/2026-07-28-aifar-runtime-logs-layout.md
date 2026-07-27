# AIFAR Runtime Logs Workspace Layout Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Reorganize the AIFAR Runtime logs page so realtime logs and diagnostic archives are separate focused surfaces, while compacting the Runtime toolbar and instance summary without changing backend behavior.

**Architecture:** Keep all existing Runtime state and actions in the current injected `AifarRuntimeContext`. Add small presentation contracts for the logs sub-tabs and diagnostic service summaries, then update the existing Vue components to render a focused `live/archives` workspace and a compact toolbar/summary. No API, SSE, database, or agent changes are required.

**Tech Stack:** Vue 3 Composition API, TypeScript 5.8, Element Plus 2.10, Vitest 3, Vue server renderer, project i18n dictionaries, existing AIFAR design tokens.

## Global Constraints

- Follow `design/ant-design-system-portable202606.md` and reuse the current AIFAR visual tokens and Element Plus components.
- Keep `/api/v2`, Runtime SSE behavior, diagnostic export/download/delete behavior, permissions, task tracking, and audit behavior unchanged.
- Do not modify the backend, database schema, or `aifar-agent`.
- All new user-visible copy must be present in both Chinese and English in `web/src/i18n/messages.ts`.
- Default the logs workspace to realtime logs; reset it to realtime logs when the selected server or Runtime instance changes.
- Preserve all existing disabled reasons, loading states, confirmation flows, and task-driven refresh behavior.
- Validate 1920×1080 and 1440×900 desktop layouts, keyboard operation, 200% zoom/reflow, and Chinese/English copy.
- Run `pnpm test:web` and `pnpm web:build`; do not run offline packaging and do not push.

---

## File Structure

- Modify `web/src/containers/runtime/surface.ts`: own stable Runtime logs sub-tab identifiers, order, and i18n keys.
- Modify `web/src/containers/runtime/surface.test.ts`: verify the logs sub-tab public contract.
- Modify `web/src/containers/runtime/runtimeDiagnostics.ts`: own the compact service-list presentation helper used by the archive table.
- Modify `web/src/containers/runtime/runtimeDiagnostics.test.ts`: verify compact service preview and full tooltip output.
- Create `web/src/containers/runtime/AifarRuntimeSummary.vue`: render the compact Runtime instance summary without changing the shared `KeyValueGrid`.
- Create `web/src/containers/runtime/AifarRuntimeSummary.test.ts`: SSR-test summary text, status text, long-value title, and item semantics.
- Modify `web/src/containers/runtime/AifarRuntimeWorkspace.vue`: regroup status, instance selection, direct actions, overflow actions, refresh, and summary.
- Modify `web/src/containers/runtime/runtimeEntryMerge.test.ts`: verify direct/overflow action placement and prevent duplicated Runtime actions.
- Modify `web/src/containers/runtime/AifarRuntimeLogsTab.vue`: add `live/archives` sub-tabs and reset behavior.
- Create `web/src/containers/runtime/AifarRuntimeLogsTab.test.ts`: source-contract and state-helper coverage for focused rendering.
- Modify `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`: render the six-column full-height archive table and record count.
- Modify `web/src/containers/runtime/runtime.css`: implement compact toolbar, summary, sub-tabs, full-height panels, and responsive layout.
- Modify `web/src/i18n/messages.ts`: add Chinese and English labels for realtime logs, diagnostic archives, record count, more actions, lifecycle labels, and refresh accessibility.

---

### Task 1: Define Focused Logs Workspace Contracts

**Files:**
- Modify: `web/src/containers/runtime/surface.ts`
- Modify: `web/src/containers/runtime/surface.test.ts`
- Modify: `web/src/containers/runtime/runtimeDiagnostics.ts`
- Modify: `web/src/containers/runtime/runtimeDiagnostics.test.ts`

**Interfaces:**
- Produces: `RuntimeLogWorkspaceTab = 'live' | 'archives'`.
- Produces: `runtimeLogWorkspaceTabOrder: readonly RuntimeLogWorkspaceTab[]`.
- Produces: `runtimeLogWorkspaceTabLabels: Record<RuntimeLogWorkspaceTab, string>`.
- Produces: `runtimeDiagnosticServicePreview(services: string[], visibleLimit?: number): { visibleText: string; hiddenCount: number; tooltip: string }`.
- Consumes: existing `RuntimeResourceTab` and `RuntimeDiagnosticExport.services` values.

- [ ] **Step 1: Add failing logs surface tests**

Extend `surface.test.ts` with the exact expected tab contract:

```ts
import {
  runtimeIngressColumns,
  runtimeLogWorkspaceTabLabels,
  runtimeLogWorkspaceTabOrder,
  runtimeResourceTabOrder
} from './surface'

it('keeps realtime logs and diagnostic archives as focused sub-tabs', () => {
  expect(runtimeLogWorkspaceTabOrder).toEqual(['live', 'archives'])
  expect(runtimeLogWorkspaceTabLabels).toEqual({
    live: 'containers.realtimeLogs',
    archives: 'containers.diagnosticArchives'
  })
})
```

- [ ] **Step 2: Add failing diagnostic service preview tests**

Extend `runtimeDiagnostics.test.ts`:

```ts
import { runtimeDiagnosticServicePreview } from './runtimeDiagnostics'

it('builds a compact service preview without losing the full list', () => {
  expect(runtimeDiagnosticServicePreview(['contacts', 'file', 'gateway', 'im'], 3)).toEqual({
    visibleText: 'contacts, file, gateway',
    hiddenCount: 1,
    tooltip: 'contacts, file, gateway, im'
  })
  expect(runtimeDiagnosticServicePreview([], 3)).toEqual({
    visibleText: '-',
    hiddenCount: 0,
    tooltip: '-'
  })
})
```

- [ ] **Step 3: Run the focused tests and verify RED**

Run:

```powershell
pnpm test:web
```

Expected: FAIL because the new exports do not exist.

- [ ] **Step 4: Implement the sub-tab and service-preview contracts**

Add to `surface.ts`:

```ts
export type RuntimeLogWorkspaceTab = 'live' | 'archives'

export const runtimeLogWorkspaceTabOrder = ['live', 'archives'] as const satisfies readonly RuntimeLogWorkspaceTab[]

export const runtimeLogWorkspaceTabLabels: Record<RuntimeLogWorkspaceTab, string> = {
  live: 'containers.realtimeLogs',
  archives: 'containers.diagnosticArchives'
}
```

Add to `runtimeDiagnostics.ts`:

```ts
export function runtimeDiagnosticServicePreview(services: string[], visibleLimit = 3) {
  const normalized = services.map((service) => service.trim()).filter(Boolean)
  const visible = normalized.slice(0, Math.max(1, visibleLimit))
  return {
    visibleText: visible.join(', ') || '-',
    hiddenCount: Math.max(0, normalized.length - visible.length),
    tooltip: normalized.join(', ') || '-'
  }
}
```

- [ ] **Step 5: Run focused tests and verify GREEN**

Run:

```powershell
pnpm test:web
```

Expected: both files pass.

- [ ] **Step 6: Commit the presentation contracts**

```powershell
git add web/src/containers/runtime/surface.ts web/src/containers/runtime/surface.test.ts web/src/containers/runtime/runtimeDiagnostics.ts web/src/containers/runtime/runtimeDiagnostics.test.ts
git commit -m "test: define runtime logs layout contracts"
```

---

### Task 2: Build the Focused Realtime Logs and Diagnostic Archive Surfaces

**Files:**
- Modify: `web/src/containers/runtime/AifarRuntimeLogsTab.vue`
- Create: `web/src/containers/runtime/AifarRuntimeLogsTab.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: `RuntimeLogWorkspaceTab`, `runtimeLogWorkspaceTabOrder`, and `runtimeLogWorkspaceTabLabels` from Task 1.
- Consumes: `runtimeDiagnosticServicePreview()` from Task 1.
- Produces: local `runtimeLogWorkspaceTab: Ref<RuntimeLogWorkspaceTab>` defaulting to `live`.
- Produces: six-column archive table with status, range, services, archive, lifecycle, and operations.

- [ ] **Step 1: Add the failing component source contract**

Create `AifarRuntimeLogsTab.test.ts`:

```ts
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const source = readFileSync(new URL('./AifarRuntimeLogsTab.vue', import.meta.url), 'utf8')
const diagnosticsSource = readFileSync(new URL('./AifarRuntimeDiagnosticsPanel.vue', import.meta.url), 'utf8')

describe('AIFAR Runtime focused logs workspace', () => {
  it('separates realtime logs and diagnostic archives', () => {
    expect(source).toContain('v-model="runtimeLogWorkspaceTab"')
    expect(source).toContain('runtimeLogWorkspaceTabOrder')
    expect(source).toContain(':name="tabName"')
    expect(source).toContain("tabName === 'archives'")
    expect(source).toContain('<AifarRuntimeDiagnosticsPanel')
  })

  it('uses a compact six-column diagnostic table', () => {
    expect(diagnosticsSource.match(/<el-table-column/g) ?? []).toHaveLength(6)
    expect(diagnosticsSource).toContain('runtimeDiagnosticServicePreview')
    expect(diagnosticsSource).not.toContain('max-height="280"')
  })
})
```

- [ ] **Step 2: Run the new test and verify RED**

Run:

```powershell
pnpm test:web
```

Expected: FAIL because the sub-tabs and compact table do not exist.

- [ ] **Step 3: Add the local sub-tab state and reset behavior**

In `AifarRuntimeLogsTab.vue`, import `ref` and `watch`, then add:

```ts
import { ref, watch } from 'vue'
import {
  runtimeLogWorkspaceTabLabels,
  runtimeLogWorkspaceTabOrder,
  type RuntimeLogWorkspaceTab
} from './surface'

const runtimeLogWorkspaceTab = ref<RuntimeLogWorkspaceTab>('live')

watch(
  () => [selectedRuntimeInstanceId.value, runtimeTargetQuery()],
  () => { runtimeLogWorkspaceTab.value = 'live' }
)
```

Delete the top-level diagnostics component before `.runtime-tab-toolbar`. Add `el-tabs.runtime-log-workspace-tabs` immediately inside `.runtime-log-panel`, render `runtimeLogWorkspaceTabOrder` with `v-for`, bind each pane name and label from the Task 1 contracts, and set `:lazy="tabName === 'archives'"`. For the `archives` branch, render only the existing `AifarRuntimeDiagnosticsPanel` with the same three props. For the `live` branch, wrap the current toolbar, warning alert, lazy states, and log workbench in `.runtime-log-live-surface`. Keep every existing realtime log action exactly once.

- [ ] **Step 4: Convert diagnostics to the six-column table**

In `AifarRuntimeDiagnosticsPanel.vue`, import `runtimeDiagnosticServicePreview` and expose a local wrapper:

```ts
function servicePreview(row: RuntimeDiagnosticExport) {
  return runtimeDiagnosticServicePreview(row.services, 3)
}
```

Render exactly six columns:

```vue
<el-table-column :label="t('common.status')" width="176">
  <template #default="{ row }">
    <div class="runtime-diagnostics-status">
      <el-tag size="small" :type="diagnosticStatusType(row)">{{ t(runtimeDiagnosticStatusKey(row)) }}</el-tag>
      <el-tooltip v-if="row.warnings?.length" :content="row.warnings.join('；')" placement="top">
        <span class="runtime-diagnostics-warning">{{ t('containers.diagnosticWarnings', { count: row.warningCount }) }}</span>
      </el-tooltip>
    </div>
  </template>
</el-table-column>
<el-table-column :label="t('containers.diagnosticsTimeRange')" min-width="210">
  <template #default="{ row }"><div class="runtime-diagnostics-stacked"><span>{{ formatDate(row.sinceAt) }}</span><span>{{ formatDate(row.untilAt) }}</span></div></template>
</el-table-column>
<el-table-column :label="t('containers.diagnosticsServices')" min-width="190">
  <template #default="{ row }">
    <el-tooltip :content="servicePreview(row).tooltip" placement="top">
      <span>{{ servicePreview(row).visibleText }}<template v-if="servicePreview(row).hiddenCount"> +{{ servicePreview(row).hiddenCount }}</template></span>
    </el-tooltip>
  </template>
</el-table-column>
<el-table-column :label="t('containers.diagnosticArchive')" width="160">
  <template #default="{ row }"><div class="runtime-diagnostics-stacked"><span>{{ storageKindLabel(row.storageKind) }}</span><strong>{{ formatBytes(row.archiveBytes) }}</strong></div></template>
</el-table-column>
<el-table-column :label="t('containers.diagnosticLifecycle')" min-width="180">
  <template #default="{ row }"><div class="runtime-diagnostics-stacked"><span>{{ formatDate(row.createdAt) }}</span><span>{{ formatDate(row.expiresAt) }}</span></div></template>
</el-table-column>
<el-table-column :label="t('common.operation')" width="192" fixed="right">
  <template #default="{ row }">
    <div class="row-actions runtime-diagnostics-actions">
      <el-button v-if="row.taskId" size="small" text @click="openTask(row.taskId)">{{ t('common.details') }}</el-button>
      <el-button size="small" text type="primary" :disabled="row.status !== 'ready'" @click="download(row)">{{ t('common.download') }}</el-button>
      <el-button size="small" text type="danger" :disabled="row.status === 'deleted'" @click="remove(row)">{{ t('common.delete') }}</el-button>
    </div>
  </template>
</el-table-column>
```

Remove `max-height="280"`. Show `exportsPage.total` in the panel header with `t('containers.diagnosticArchiveCount', { count: exportsPage.total })`.

- [ ] **Step 5: Add Chinese and English copy**

Add equivalent keys to both dictionaries in `messages.ts`:

```ts
'containers.realtimeLogs': '实时日志',
'containers.diagnosticArchives': '诊断归档',
'containers.diagnosticArchive': '归档',
'containers.diagnosticLifecycle': '生命周期',
'containers.diagnosticArchiveCount': ({ count } = {}) => `${count ?? 0} 个归档`,
'containers.diagnosticWarnings': ({ count } = {}) => `${count ?? 0} 条警告`,
```

```ts
'containers.realtimeLogs': 'Realtime logs',
'containers.diagnosticArchives': 'Diagnostic archives',
'containers.diagnosticArchive': 'Archive',
'containers.diagnosticLifecycle': 'Lifecycle',
'containers.diagnosticArchiveCount': ({ count } = {}) => `${count ?? 0} archives`,
'containers.diagnosticWarnings': ({ count } = {}) => `${count ?? 0} warnings`,
```

- [ ] **Step 6: Add focused full-height styles**

Add to `runtime.css`:

```css
.runtime-log-workspace-tabs,
.runtime-log-workspace-tabs .el-tabs__content,
.runtime-log-workspace-tabs .el-tab-pane {
  min-height: 0;
  height: 100%;
}

.runtime-log-workspace-tabs {
  flex: 1 1 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
}

.runtime-log-live-surface,
.runtime-diagnostics-panel {
  min-height: 0;
  height: 100%;
}

.runtime-diagnostics-table {
  flex: 1 1 auto;
  min-height: 0;
}

.runtime-diagnostics-stacked,
.runtime-diagnostics-status {
  display: grid;
  gap: 4px;
}
```

Retain the existing virtual-list sizing and scrollbar behavior.

- [ ] **Step 7: Run focused and full web tests**

Run:

```powershell
pnpm test:web
pnpm test:web
```

Expected: all tests pass.

- [ ] **Step 8: Commit the focused logs workspace**

```powershell
git add web/src/containers/runtime/AifarRuntimeLogsTab.vue web/src/containers/runtime/AifarRuntimeLogsTab.test.ts web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue web/src/containers/runtime/runtime.css web/src/i18n/messages.ts
git commit -m "feat: separate runtime logs and diagnostic archives"
```

---

### Task 3: Compact the Runtime Toolbar and Instance Summary

**Files:**
- Create: `web/src/containers/runtime/AifarRuntimeSummary.vue`
- Create: `web/src/containers/runtime/AifarRuntimeSummary.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeWorkspace.vue`
- Modify: `web/src/containers/runtime/runtimeEntryMerge.test.ts`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: existing `runtimeSummaryItems: ComputedRef<KeyValueItem[]>` without changing `AifarRuntimeContext`.
- Produces: `AifarRuntimeSummary` prop `items: KeyValueItem[]`.
- Produces: overflow commands `'install' | 'reconcile' | 'cleanup'` mapped to the existing context actions.
- Keeps direct actions: Runtime configuration, bundle update, restart all, refresh.

- [ ] **Step 1: Add a failing SSR test for the compact summary**

Create `AifarRuntimeSummary.test.ts`:

```ts
import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it } from 'vitest'
import AifarRuntimeSummary from './AifarRuntimeSummary.vue'

it('renders every summary label and full value for tooltip access', async () => {
  const app = createSSRApp({
    render: () => h(AifarRuntimeSummary, {
      label: '运行时实例摘要',
      items: [
        { label: '实例', value: 'runtime-v2 / admin' },
        { label: '安装目录', value: '/aifar/apps/admin' },
        { label: 'Agent', value: '运行中', status: 'running' }
      ]
    })
  })
  const html = await renderToString(app)
  expect(html).toContain('runtime-v2 / admin')
  expect(html).toContain('title="/aifar/apps/admin"')
  expect(html).toContain('运行中')
})
```

- [ ] **Step 2: Run the summary test and verify RED**

Run:

```powershell
pnpm test:web
```

Expected: FAIL because the component does not exist.

- [ ] **Step 3: Implement the compact Runtime summary**

Create `AifarRuntimeSummary.vue`:

```vue
<template>
  <dl class="runtime-summary" :aria-label="label">
    <div v-for="item in items" :key="item.key || item.label" class="runtime-summary-item">
      <dt>{{ item.label }}</dt>
      <dd :title="String(item.value ?? '-')">
        <StatusTag v-if="item.status" :status="item.status" :label="String(item.value ?? '-')" />
        <span v-else>{{ item.value ?? '-' }}</span>
      </dd>
    </div>
  </dl>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import type { KeyValueItem } from './context'

defineProps<{ items: KeyValueItem[]; label: string }>()
</script>
```

- [ ] **Step 4: Extend the workspace SSR contract before changing the toolbar**

Update `runtimeEntryMerge.test.ts` to register `ElDropdown`, `ElDropdownItem`, `ElDropdownMenu`, and `ElIcon` stubs, provide non-empty summary items, and assert:

```ts
expect(renderedText).toContain('运行参数')
expect(renderedText).toContain('批量更新')
expect(renderedText).toContain('全部重启')
expect(renderedText).toContain('更多操作')
expect(renderedText.match(/同步运行时/g) ?? []).toHaveLength(1)
expect(renderedText).toContain('/aifar/apps/admin')
```

Run:

```powershell
pnpm test:web
```

Expected: FAIL on “更多操作” and the compact summary contract.

- [ ] **Step 5: Regroup direct and overflow Runtime actions**

In `AifarRuntimeWorkspace.vue`:

1. Move the instance selector into `.runtime-status` after the two status tags.
2. Keep direct buttons for `openRuntimeConfigDialog`, `openAifarRuntimeBundleUpdate`, and `restartAllAifarRuntime`.
3. Add an Element Plus dropdown for install, reconcile, and cleanup.
4. Replace the text refresh button with a `Refresh` icon button wrapped in a tooltip and carrying `:aria-label="t('common.refresh')"`.
5. Replace `KeyValueGrid` with `AifarRuntimeSummary`.

Use an explicit command union:

```ts
type RuntimeOverflowCommand = 'install' | 'reconcile' | 'cleanup'

function handleRuntimeOverflowCommand(command: RuntimeOverflowCommand) {
  if (command === 'install') return openServiceInstallDialog()
  if (command === 'reconcile') return reconcileAifarRuntime()
  return cleanupAifarRuntimeStale()
}
```

Use titles on disabled menu item content so the existing disabled reason remains inspectable:

```vue
<el-dropdown-item command="reconcile" :disabled="Boolean(aifarRuntimeActionDisabledReason)">
  <span :title="aifarRuntimeActionDisabledReason || undefined">{{ t('containers.reconcileRuntime') }}</span>
</el-dropdown-item>
```

- [ ] **Step 6: Add toolbar and summary copy**

Add to both language dictionaries:

```ts
'containers.moreRuntimeActions': '更多操作',
'containers.runtimeSummary': '运行时实例摘要',
```

```ts
'containers.moreRuntimeActions': 'More actions',
'containers.runtimeSummary': 'Runtime instance summary',
```

Pass `t('containers.runtimeSummary')` into the required `label` prop on `AifarRuntimeSummary`, ensuring the summary is localized.

- [ ] **Step 7: Add compact toolbar and summary styles**

Add to `runtime.css`:

```css
.runtime-toolbar {
  align-items: flex-start;
}

.runtime-status {
  flex: 1 1 420px;
  flex-wrap: wrap;
}

.runtime-summary {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  gap: 1px;
  margin: 0;
  overflow: hidden;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: var(--aifar-border-soft);
}

.runtime-summary-item {
  min-width: 0;
  padding: 8px 10px;
  background: #fff;
}

.runtime-summary-item dt {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 18px;
}

.runtime-summary-item dd {
  min-width: 0;
  margin: 2px 0 0;
  overflow: hidden;
  color: var(--aifar-ink);
  text-overflow: ellipsis;
  white-space: nowrap;
}
```

At `max-width: 1440px`, use three summary columns; at `max-width: 900px`, use one column and allow the toolbar action group to wrap beneath the status group.

- [ ] **Step 8: Run focused and full web tests**

Run:

```powershell
pnpm test:web
pnpm test:web
```

Expected: all tests pass.

- [ ] **Step 9: Commit the compact Runtime chrome**

```powershell
git add web/src/containers/runtime/AifarRuntimeSummary.vue web/src/containers/runtime/AifarRuntimeSummary.test.ts web/src/containers/runtime/AifarRuntimeWorkspace.vue web/src/containers/runtime/runtimeEntryMerge.test.ts web/src/containers/runtime/runtime.css web/src/i18n/messages.ts
git commit -m "feat: compact runtime workspace controls"
```

---

### Task 4: Visual, Responsive, Accessibility, and Regression Verification

**Files:**
- Modify if verification finds defects: `web/src/containers/runtime/AifarRuntimeWorkspace.vue`
- Modify if verification finds defects: `web/src/containers/runtime/AifarRuntimeLogsTab.vue`
- Modify if verification finds defects: `web/src/containers/runtime/AifarRuntimeDiagnosticsPanel.vue`
- Modify if verification finds defects: `web/src/containers/runtime/AifarRuntimeSummary.vue`
- Modify if verification finds defects: `web/src/containers/runtime/runtime.css`
- Modify if verification finds copy defects: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: all components and contracts from Tasks 1–3.
- Produces: verified layouts at 1920×1080 and 1440×900 with no backend changes.

- [ ] **Step 1: Run the complete automated frontend gate**

Run:

```powershell
pnpm test:web
pnpm web:build
```

Expected: all Vitest tests pass; Vue type checking and Vite build exit 0.

- [ ] **Step 2: Start or reuse the local development server**

Run:

```powershell
pnpm dev
```

Use the existing configured local development address. Do not edit `config/defaults.env`; use process environment `AIFAR_DEV_ADDR` only if a port override is required.

- [ ] **Step 3: Verify the realtime logs surface at 1920×1080**

Open the existing Containers > AIFAR Runtime > Logs state with real local data and verify:

- Realtime logs is selected by default.
- Diagnostic archives are not rendered in the realtime logs content area.
- The log virtual list receives the majority of the remaining vertical height.
- Runtime configuration, bundle update, restart all, more actions, and refresh remain reachable.
- No button, summary value, tab, or log control is clipped.

Capture the accepted screenshot as `.tmp/ui-qa/runtime-logs-1920x1080.png` and inspect it before acceptance.

- [ ] **Step 4: Verify the diagnostic archives surface at 1920×1080**

Switch to Diagnostic archives and verify:

- The header shows the archive count and export action.
- The table has six visible columns and uses the full available height.
- Warning detail, complete service list, download, delete, and task details remain accessible.
- The archive storage and lifecycle cells are readable without the original eight-column crowding.

Capture `.tmp/ui-qa/runtime-archives-1920x1080.png` and inspect it.

- [ ] **Step 5: Verify 1440×900 and accessibility behavior**

At 1440×900 and at 200% browser zoom, verify:

- Runtime actions wrap as a group without overlap.
- Summary becomes three columns and then one column when space requires it.
- Log filters and stream controls wrap without hiding actions.
- Archive table retains a usable fixed operation column and horizontal scrolling when required.
- Keyboard focus reaches the logs sub-tabs, more-actions trigger/menu items, refresh, and archive operations in a logical order.
- Selected tab and all statuses remain understandable without color.

Capture `.tmp/ui-qa/runtime-logs-1440x900.png` and inspect it.

- [ ] **Step 6: Verify Chinese and English copy**

Switch the existing language control and verify both languages for:

- Realtime logs and Diagnostic archives tab labels.
- More actions and all overflow menu items.
- Archive count, warning count, Archive, and Lifecycle columns.
- Refresh accessible label/tooltip.

Fix any overflow or untranslated key before continuing.

- [ ] **Step 7: Inspect console and final diff**

Confirm the local page has no new console errors, then run:

```powershell
git diff --check
git status --short
git diff --stat
```

Expected: no whitespace errors; only the planned frontend files, tests, and project `memory.md` are modified. Leave unrelated `tmp/` content untouched and uncommitted.

- [ ] **Step 8: Commit verification-driven adjustments**

If verification required changes:

```powershell
git add web/src/containers/runtime web/src/i18n/messages.ts
git commit -m "fix: polish runtime logs workspace layout"
```

If verification required no changes, do not create an empty commit.

- [ ] **Step 9: Record the reusable project conclusion**

Append to `memory.md` without credentials or long logs:

```markdown
- 问题：优化 AIFAR Runtime 日志页拥挤问题。
- 结论：日志页采用实时日志/诊断归档二级标签互斥展示，顶部操作与实例摘要压缩；前端测试、构建及关键桌面视口验收通过。未修改后端、aifar-agent，未离线打包、未推送。
```

Do not stage `memory.md` unless the user explicitly requests it.
