# Hide Secondary Management Entries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Hide the requested secondary tabs and MinIO cleanup-policy mutation buttons, and align Database and Nacos header actions with Storage while preserving all backend capabilities and stored data.

**Architecture:** Add a tiny typed UI-entry policy module that exposes the visible tab and header-action lists for Database, Nacos, and Storage. Render each page's tabs from that policy, align their headers on connected status plus refresh, and remove only the two requested Storage action buttons from the template; all dormant content branches and service calls remain unchanged.

**Tech Stack:** Vue 3, TypeScript, Element Plus, Vitest

## Global Constraints

- Keep only the `instances` tab visible on Database, Nacos, and Storage.
- Keep Storage cleanup estimation controls visible.
- Hide Storage cleanup-policy apply and disable buttons.
- Show only connected status and refresh in all three page headers.
- Remove the Database and Nacos application-store deployment shortcuts without deleting the route or deployment capability.
- Do not change APIs, persistence, permissions, tasks, or remote services.
- Preserve dormant implementation so the entries can be restored later.

---

### Task 1: Visible management entry policy and page templates

**Files:**
- Create: `web/src/views/managementEntries.ts`
- Test: `web/src/views/managementEntries.test.ts`
- Modify: `web/src/views/DatabaseView.vue`
- Modify: `web/src/views/NacosView.vue`
- Modify: `web/src/views/StorageView.vue`

**Interfaces:**
- Produces: `visibleManagementTabs: Readonly<Record<'database' | 'nacos' | 'storage', readonly ['instances']>>`
- Consumes: Vue template iteration over `visibleManagementTabs.<page>` and existing page i18n keys.

- [x] **Step 1: Write the failing test**

```ts
import { describe, expect, it } from 'vitest'

import { visibleManagementTabs } from './managementEntries'

describe('visible management tabs', () => {
  it('exposes only instance tabs on database, Nacos, and storage pages', () => {
    expect(visibleManagementTabs).toEqual({
      database: ['instances'],
      nacos: ['instances'],
      storage: ['instances']
    })
  })
})
```

- [x] **Step 2: Run the focused test and verify RED**

Run: `pnpm exec vitest run src/views/managementEntries.test.ts` from `web/`.

Expected: FAIL because `./managementEntries` does not exist.

- [x] **Step 3: Add the minimal policy and render tabs from it**

Create the typed constant:

```ts
export const visibleManagementTabs = {
  database: ['instances'],
  nacos: ['instances'],
  storage: ['instances']
} as const
```

Import it into each page and replace hard-coded secondary `el-tab-pane` nodes with a single `v-for` driven by the relevant list. Use the page's existing instance label key. In `StorageView.vue`, remove only the two tooltip/button nodes that call `applyVisibleStorageCleanupPolicy` and `disableVisibleStorageCleanupPolicy`.

- [x] **Step 4: Run the focused test and verify GREEN**

Run: `pnpm exec vitest run src/views/managementEntries.test.ts` from `web/`.

Expected: PASS with 1 test.

- [x] **Step 5: Run complete frontend verification**

Run from repository root:

```powershell
pnpm test:web
pnpm web:build
git diff --check
```

Expected: all commands exit 0 with no failed tests or TypeScript/build errors.

- [x] **Step 6: Record the reusable conclusion**

Append a concise entry to `memory.md` stating which UI entries are hidden and that underlying APIs/data remain intact.

### Task 2: Align Database and Nacos header actions with Storage

**Files:**
- Modify: `web/src/views/managementEntries.ts`
- Test: `web/src/views/managementEntries.test.ts`
- Modify: `web/src/views/DatabaseView.vue`
- Modify: `web/src/views/NacosView.vue`

**Interfaces:**
- Produces: `visibleManagementHeaderActions: Readonly<Record<'database' | 'nacos' | 'storage', readonly ['connected', 'refresh']>>`
- Consumes: Database and Nacos page headers render the existing `common.connected` label and refresh button from this policy.

- [x] **Step 1: Extend the failing policy test**

```ts
expect(visibleManagementHeaderActions).toEqual({
  database: ['connected', 'refresh'],
  nacos: ['connected', 'refresh'],
  storage: ['connected', 'refresh']
})
```

- [x] **Step 2: Run the focused test and verify RED**

Run: `node node_modules/vitest/vitest.mjs run src/views/managementEntries.test.ts` from `web/`.

Expected: FAIL because `visibleManagementHeaderActions` is not exported.

- [x] **Step 3: Implement the minimal header policy and template changes**

Export the action policy from `managementEntries.ts`. In `DatabaseView.vue` and `NacosView.vue`, replace the application-store deployment button with the same successful connection pill used by `StorageView.vue`, then keep the existing refresh button. Do not remove `useRouter`, because both pages still navigate to task details through the router.

- [x] **Step 4: Run focused and complete verification**

Run from repository root:

```powershell
node web/node_modules/vitest/vitest.mjs run web/src/views/managementEntries.test.ts
pnpm test:web
pnpm web:build
git diff --check
```

Expected: all commands exit 0; the focused file has 2 passing tests and the complete frontend suite has no failures.
