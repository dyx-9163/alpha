# MinIO Realtime Monitoring Status Line Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add the Nacos-style realtime monitoring status line to the MinIO object-storage page.

**Architecture:** Keep the UI local to `StorageView.vue` and derive its values from the existing realtime Pinia store. Put the small, deterministic MinIO filtering and latest-snapshot selection rules in a focused storage helper so they can be tested without mounting the large page component.

**Tech Stack:** Vue 3, TypeScript, Pinia realtime snapshots, Vue I18n message catalog, Vitest, Vite.

## Global Constraints

- Use the existing `aifar-panel status-line`, `status-pill`, and `subtle-note` classes; add no new visual tokens.
- Count only instances whose `app` is `minio`.
- Prefer snapshot `collectedAt`, fall back to `updatedAt`, and hide the timestamp when no valid value exists.
- Show `等待后端推送` only when the current account has `apps.manage`; otherwise show the storage monitoring permission message.
- Add both Chinese and English user-visible messages.
- Do not change backend APIs, realtime events, Nacos, instance cards, cleanup controls, or deployment behavior.

---

### Task 1: Test and implement MinIO monitoring derivation

**Files:**
- Create: `web/src/storage/monitoringStatus.ts`
- Create: `web/src/storage/monitoringStatus.test.ts`

**Interfaces:**
- Produces: `filterMinioInstances<T extends { app?: string }>(instances: T[]): T[]`
- Produces: `latestSnapshotTime(snapshots: Array<{ collectedAt?: string; updatedAt?: string } | undefined>): string`

- [ ] **Step 1: Write the failing tests**

```ts
import { describe, expect, it } from 'vitest'
import { filterMinioInstances, latestSnapshotTime } from './monitoringStatus'

describe('MinIO monitoring status', () => {
  it('counts only MinIO instances', () => {
    const instances = filterMinioInstances([
      { id: 'minio-1', app: 'minio' },
      { id: 'nacos-1', app: 'nacos' },
      { id: 'minio-2', app: 'MINIO' }
    ])
    expect(instances.map((item) => item.id)).toEqual(['minio-1', 'minio-2'])
  })

  it('uses the latest valid collected or updated snapshot time', () => {
    expect(latestSnapshotTime([
      { collectedAt: '2026-07-27T03:59:09Z' },
      { updatedAt: '2026-07-27T04:01:10Z' },
      { collectedAt: 'invalid' }
    ])).toBe(new Date('2026-07-27T04:01:10Z').toLocaleTimeString())
    expect(latestSnapshotTime([undefined, { collectedAt: 'invalid' }])).toBe('')
  })
})
```

- [ ] **Step 2: Run the focused test and verify RED**

Run from `web/`:

```powershell
node node_modules/vitest/vitest.mjs run src/storage/monitoringStatus.test.ts
```

Expected: FAIL because `./monitoringStatus` does not exist.

- [ ] **Step 3: Implement the minimal helper**

```ts
export function filterMinioInstances<T extends { app?: string }>(instances: T[]) {
  return instances.filter((instance) => String(instance.app || '').trim().toLowerCase() === 'minio')
}

export function latestSnapshotTime(snapshots: Array<{ collectedAt?: string; updatedAt?: string } | undefined>) {
  const latest = snapshots
    .map((snapshot) => snapshot?.collectedAt || snapshot?.updatedAt || '')
    .map((value) => new Date(value).getTime())
    .filter((value) => Number.isFinite(value) && value > 0)
    .sort((a, b) => b - a)[0]
  return latest ? new Date(latest).toLocaleTimeString() : ''
}
```

- [ ] **Step 4: Run the focused test and verify GREEN**

Run the same Vitest command. Expected: 2 tests pass.

### Task 2: Render the status line and verify the page

**Files:**
- Modify: `web/src/views/StorageView.vue`
- Modify: `web/src/i18n/messages.ts`
- Test: `web/src/storage/monitoringStatus.test.ts`
- Modify: `memory.md`

**Interfaces:**
- Consumes: `filterMinioInstances` and `latestSnapshotTime` from Task 1.
- Produces: a status line between `.page-head` and `.tab-strip` with instance count, monitoring status, and optional latest time.

- [ ] **Step 1: Add the status-line template**

```vue
<div class="aifar-panel status-line">
  <span class="subtle-note">{{ t('storage.instanceCount', { count: minioInstances.length }) }}</span>
  <span class="status-pill" :class="{ success: canManageApps }">{{ monitoringStatusLabel }}</span>
  <span v-if="lastMonitorAt" class="subtle-note">{{ t('storage.lastMonitoredAt') }} {{ lastMonitorAt }}</span>
</div>
```

- [ ] **Step 2: Wire the existing realtime snapshots**

Import Task 1 helpers, then add:

```ts
const minioInstances = computed(() => filterMinioInstances(liveInstances.value))
const lastMonitorAt = computed(() => latestSnapshotTime(
  minioInstances.value.map((instance) => realtime.appInstanceSnapshot(instance.id))
))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) return t('storage.monitorPermissionRequired')
  return t('storage.backendPushReady')
})
```

Use `minioInstances.value` when building storage groups so the count and cards share the same MinIO-only scope.

- [ ] **Step 3: Add the bilingual messages**

Chinese:

```ts
'storage.instanceCount': ({ count } = {}) => `已登记 ${count ?? 0} 个 MinIO 实例`,
'storage.backendPushReady': '等待后端推送',
'storage.lastMonitoredAt': '最近监测',
'storage.monitorPermissionRequired': '需要应用管理权限',
```

English:

```ts
'storage.instanceCount': ({ count } = {}) => `${count ?? 0} MinIO instance(s) registered`,
'storage.backendPushReady': 'Waiting for backend push',
'storage.lastMonitoredAt': 'Last monitored',
'storage.monitorPermissionRequired': 'App management permission required',
```

- [ ] **Step 4: Run focused and full frontend verification**

```powershell
cd web
node node_modules/vitest/vitest.mjs run src/storage/monitoringStatus.test.ts
cd ..
pnpm test:web
pnpm web:build
```

Expected: focused test passes, all frontend tests pass, and the production build exits 0.

- [ ] **Step 5: Perform local browser verification**

Open `http://127.0.0.1:5173/storage` at the reference desktop viewport and verify:

- the status line appears between the page head and the Instances tab;
- it reads `已登记 1 个 MinIO 实例 / 等待后端推送 / 最近监测 <time>` for the current fixture state;
- the existing search, cleanup controls, card layout, and refresh button remain unchanged;
- browser console has no error or warning.

- [ ] **Step 6: Record the result and commit**

Append a concise problem/conclusion entry to `memory.md`, then stage only task files and commit:

```powershell
git add -- web/src/storage/monitoringStatus.ts web/src/storage/monitoringStatus.test.ts web/src/views/StorageView.vue web/src/i18n/messages.ts
git commit -m "feat: add storage monitoring status line"
```
