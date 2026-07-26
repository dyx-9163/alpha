# AIFAR Runtime Reconcile Entry Merge Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Keep the workspace-level “同步运行时” action and remove the duplicate Pods-tab “启动/恢复 Pods” action without changing backend or Agent behavior.

**Architecture:** Exercise the two real Vue components with a lightweight Vue custom renderer and an injected Runtime context, so the regression test asserts the user-visible action surface instead of grepping source. Then remove the duplicate Pods action from the component, view context, and i18n catalogs while preserving the existing workspace reconcile flow.

**Tech Stack:** Vue 3, TypeScript, Vitest, Element Plus, pnpm

## Global Constraints

- Modify only `web/src` production code and the implementation plan/test files.
- Keep `/api/v2/containers/aifar/runtime/reconcile`, `aifar.reconcile`, `runtime-reconcile.sh`, and `aifar-agent reconcile-runtime` unchanged.
- Keep the workspace “同步运行时” button, confirmation, disabled-state handling, task tracking, and refresh behavior unchanged.
- Keep the Pods service filter, “刷新”, and “刷新指标” controls unchanged.
- Follow `design/ant-design-system-portable202606.md`; this change removes a duplicate control and adds no new visual token or layout pattern.

---

### Task 1: Remove the Duplicate Pods Reconcile Entry

**Files:**
- Create: `web/src/containers/runtime/runtimeEntryMerge.test.ts`
- Modify: `web/src/containers/runtime/AifarRuntimePodsTab.vue`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/views/ContainersView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: `aifarRuntimeContextKey`, `AifarRuntimeWorkspace`, and `AifarRuntimePodsTab`.
- Preserves: `reconcileAifarRuntime(): Promise<void>` and its existing `submitRuntimeReconcile('containers.reconcileRuntime', 'containers.confirmReconcileRuntime')` call.
- Removes: `recoverAifarRuntimePods`, `containers.recoverRuntimePods`, and `containers.confirmRecoverRuntimePods`.

- [ ] **Step 1: Write the failing component-surface test**

Create `runtimeEntryMerge.test.ts` with a minimal Vue custom renderer. Render `AifarRuntimeWorkspace` and `AifarRuntimePodsTab` under one parent, provide a complete typed test context, register pass-through stubs for Element Plus controls, collect rendered text, and assert:

```ts
expect(renderedText.match(/同步运行时/g)).toHaveLength(1)
expect(renderedText).not.toContain('启动/恢复 Pods')
expect(renderedText).toContain('刷新')
expect(renderedText).toContain('刷新指标')
```

The production mutation caught by this test is reintroducing a second visible reconcile entry in the Pods toolbar.

- [ ] **Step 2: Run the focused test and verify RED**

Run:

```powershell
pnpm test:web -- runtimeEntryMerge.test.ts
```

Expected: FAIL because current `AifarRuntimePodsTab` renders “启动/恢复 Pods”.

- [ ] **Step 3: Implement the minimal UI merge**

Make these exact changes:

1. Delete the recovery tooltip/button block from `AifarRuntimePodsTab.vue` and remove `aifarRuntimeActionDisabledReason` plus `recoverAifarRuntimePods` from its context destructuring.
2. Delete `recoverAifarRuntimePods` from `AifarRuntimeContext` in `context.ts`.
3. Delete the `recoverAifarRuntimePods()` function and its provided context property from `ContainersView.vue`.
4. Delete `containers.recoverRuntimePods` and `containers.confirmRecoverRuntimePods` from both `zh` and `en` catalogs in `messages.ts`.
5. Leave `reconcileAifarRuntime`, `submitRuntimeReconcile`, and the Runtime API module unchanged.

- [ ] **Step 4: Run the focused test and verify GREEN**

Run:

```powershell
pnpm test:web -- runtimeEntryMerge.test.ts
```

Expected: PASS with one visible “同步运行时” entry and no “启动/恢复 Pods”.

- [ ] **Step 5: Run frontend regression verification**

Run:

```powershell
pnpm test:web
pnpm web:build
```

Expected: all Vitest tests, Vue type checking, and Vite production build PASS. Existing Rollup annotation and chunk-size warnings are allowed.

- [ ] **Step 6: Verify scope and commit**

Run:

```powershell
git diff --check
git diff --name-only HEAD -- web/src backend
```

Expected: only the five planned `web/src` files are listed; no `backend` file changes.

Commit:

```powershell
git add web/src/containers/runtime/runtimeEntryMerge.test.ts web/src/containers/runtime/AifarRuntimePodsTab.vue web/src/containers/runtime/context.ts web/src/views/ContainersView.vue web/src/i18n/messages.ts
git commit -m "fix(web): merge duplicate runtime reconcile entry"
```

