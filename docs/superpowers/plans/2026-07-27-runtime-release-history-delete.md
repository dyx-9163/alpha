# AIFAR Runtime Release History Delete Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Simplify the Runtime discovery table, move release history to the final tab, and add guarded control-plane release-record deletion.

**Architecture:** Keep the UI layout policy in the Runtime frontend module. Add a transactional store deletion method and a permission-protected HTTP DELETE handler that validates current, active, and referenced releases before deleting only SQLite control-plane rows. The frontend confirms deletion, invokes the typed API client, and refreshes the release cache.

**Tech Stack:** Go 1.24, Chi, SQLite, Vue 3, TypeScript, Element Plus, Vitest

## Global Constraints

- Do not delete remote release directories, artifacts, containers, or Runtime desired state.
- Reject current, pending/running, or referenced release deletion with HTTP 409.
- Require `apps.manage` and write `aifar.release.delete` audit on success.
- Keep rollback behavior unchanged.
- Provide Chinese and English user-visible messages.

---

### Task 1: Runtime surface order and visible discovery columns

**Files:**
- Create: `web/src/containers/runtime/surface.ts`
- Create: `web/src/containers/runtime/surface.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeWorkspace.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeIngressTab.vue`

**Interfaces:**
- Produces: `runtimeResourceTabOrder` with `releases` last and `runtimeIngressColumns` without `nacos` or `lastError`.
- Consumes: Runtime templates use these policies to render/order visible UI.

- [ ] **Step 1: Write failing surface tests**

Assert literal tab order `['deployments', 'services', 'pods', 'logs', 'ingress', 'releases']` and literal columns `['service', 'app', 'discoveryTarget', 'endpoint']`.

- [ ] **Step 2: Run RED**

Run `node node_modules/vitest/vitest.mjs run src/containers/runtime/surface.test.ts` from `web/`.

Expected: FAIL because `surface.ts` does not exist.

- [ ] **Step 3: Implement minimal surface policy**

Export both readonly lists. Reorder `AifarRuntimeWorkspace.vue` so the releases pane is last. Guard the four retained table columns with `runtimeIngressColumns.includes(...)` and remove the Nacos/error columns.

- [ ] **Step 4: Run GREEN**

Run the focused Vitest file and expect 2 passing tests.

### Task 2: Transactional and guarded release deletion API

**Files:**
- Modify: `backend/internal/store/app_instances.go`
- Modify: `backend/internal/store/store_test.go`
- Modify: `backend/internal/httpapi/api.go`
- Modify: `backend/internal/httpapi/apps_release_handlers.go`
- Create: `backend/internal/httpapi/apps_release_handlers_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Produces: `Store.DeleteAppRelease(instanceID, releaseID string) error`.
- Produces: `DELETE /api/v2/apps/instances/{id}/aifar/releases/{releaseId}` returning `{ "releaseId": "..." }`.

- [ ] **Step 1: Write failing store and deletion-policy tests**

Store test saves one release with artifact/snapshot rows, deletes it, then asserts all three row sets are empty while another release remains. Handler-policy table tests assert current, pending/running, and `baseReleaseId`/`rollbackTo` referenced targets are rejected while an unreferenced failed target is allowed.

- [ ] **Step 2: Run RED**

Run `go test ./internal/store ./internal/httpapi` from `backend/`.

Expected: FAIL because the store method, route, and policy helper do not exist.

- [ ] **Step 3: Implement store transaction and HTTP handler**

Delete auxiliary rows and the release row in one SQLite transaction. The handler validates instance type, loads release history, applies the approved safety rules, calls the store method, writes `aifar.release.delete` audit, and returns the deleted release ID. Add localized zh/en conflict and failure messages.

- [ ] **Step 4: Run GREEN**

Run `go test ./internal/store ./internal/httpapi` and expect both packages to pass.

### Task 3: Frontend release delete action

**Files:**
- Modify: `web/src/containers/runtime/api.ts`
- Modify: `web/src/containers/runtime/api.test.ts`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeReleasesTab.vue`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Produces: `deleteAifarRelease(instanceId, releaseId)` using authenticated `apiDelete`.
- Produces: context fields `releaseDeletingId` and `deleteAifarRelease(row)`.

- [ ] **Step 1: Write failing frontend API test**

Mock `apiDelete`, call `deleteAifarRelease('instance 1', 'release/1')`, and assert `/apps/instances/instance%201/aifar/releases/release%2F1`.

- [ ] **Step 2: Run RED**

Run `node node_modules/vitest/vitest.mjs run src/containers/runtime/api.test.ts` from `web/`.

Expected: FAIL because the delete function is not exported.

- [ ] **Step 3: Implement confirmation, loading state, refresh, and i18n**

Add a danger-style Delete button after Rollback. Confirm that only the panel record is removed, set row loading by release ID, call the API, clear the release cache, refresh immediately, and show localized success/failure messages.

- [ ] **Step 4: Run complete verification**

Run from repository root:

```powershell
pnpm test
pnpm test:web
pnpm web:build
git diff --check
```

Expected: all commands exit 0 with no failed tests or build errors.
