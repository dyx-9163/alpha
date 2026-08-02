# MySQL Backup And Restore Record Actions Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the MySQL instance card's four ordinary backup actions with `Back up now`, `Backup records`, and `Restore records`, with restore history scoped to the selected instance.

**Architecture:** Keep backup creation and backup-record behavior unchanged. Route restore history to Task Center with a restore task-type prefix and exact instance target, then let the shared task pane apply both filters before rendering or auto-selecting a task.

**Tech Stack:** Vue 3, TypeScript, Vue Router, Element Plus, Vitest, Vue Test Utils.

## Global Constraints

- Keep actual restore submission inside the backup-record drawer.
- Keep verification available inside the backup-record drawer.
- Add both Chinese and English copy through `web/src/i18n/messages.ts`.
- Preserve conditional maintenance, disaster-rebuild, reconciliation, and maintenance-clear actions.

---

### Task 1: Lock the instance-card contract with a failing test

**Files:**
- Modify: `web/src/database/mysqlBackup.components.test.ts`

**Interfaces:**
- Consumes: the existing mounted `DatabaseView` and its memory router.
- Produces: a regression assertion for three ordinary actions and the restore-history route query.

- [x] **Step 1: Write the failing test assertions**

Assert that `.mysql-backup-actions` starts with `Back up now`, `Backup records`, and `Restore records`, does not expose `Verify backup` or `Restore data`, and routes `Restore records` to:

```ts
{
  path: '/tasks',
  query: { typePrefix: 'apps.mysql.restore', target: instanceId }
}
```

- [x] **Step 2: Run the focused test and verify it fails**

Run: `pnpm test:web -- web/src/database/mysqlBackup.components.test.ts`

Expected: FAIL because `Restore records` does not exist yet.

### Task 2: Implement the three card actions and route scope

**Files:**
- Modify: `web/src/views/DatabaseView.vue`
- Modify: `web/src/i18n/messages.ts`

**Interfaces:**
- Consumes: `mysqlRestoreTarget(group).instanceId`.
- Produces: `openMySQLRestoreRecords(group: DatabaseGroup): void` and the i18n key `database.mysqlBackup.restoreRecordsAction`.

- [x] **Step 1: Replace the ordinary card actions**

Remove only the standalone verify and direct restore buttons. Add:

```vue
<el-button @click="openMySQLRestoreRecords(group)">{{ t('database.mysqlBackup.restoreRecordsAction') }}</el-button>
```

- [x] **Step 2: Add the scoped navigation function**

```ts
function openMySQLRestoreRecords(group: DatabaseGroup) {
  const target = mysqlRestoreTarget(group).instanceId
  if (!target) return
  void router.push({ path: '/tasks', query: { typePrefix: 'apps.mysql.restore', target } })
}
```

- [x] **Step 3: Add bilingual copy**

Use `恢复记录` and `Restore records` for `database.mysqlBackup.restoreRecordsAction`.

### Task 3: Apply the restore-history scope in Task Center

**Files:**
- Create: `web/src/tasks/taskScope.ts`
- Create: `web/src/tasks/taskScope.test.ts`
- Modify: `web/src/components/TaskLogPane.vue`
- Modify: `web/src/views/TasksView.vue`

**Interfaces:**
- Produces: `filterTasksByScope<T extends { type: string; target: string }>(tasks: T[], typePrefix?: string, target?: string): T[]`.
- Consumes: Task Center query parameters `typePrefix` and `target`.

- [x] **Step 1: Write the failing pure filter test**

Cover no filters, type-prefix filtering, and exact target filtering together.

- [x] **Step 2: Run the focused test and verify it fails**

Run: `pnpm test:web -- web/src/tasks/taskScope.test.ts`

Expected: FAIL because `taskScope.ts` does not exist.

- [x] **Step 3: Implement the minimal scope helper and wire it into Task Center**

`TasksView` reads the two query strings and passes them to `TaskLogPane`; `TaskLogPane` passes only the scoped list to `TaskListPanel` and uses the same list for auto-selection.

- [x] **Step 4: Run the focused tests and verify they pass**

Run: `pnpm test:web -- web/src/tasks/taskScope.test.ts web/src/database/mysqlBackup.components.test.ts`

Expected: both test files pass.

### Task 4: Verify the complete frontend change

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: all changes above.
- Produces: current test/build evidence and a reusable project-memory note.

- [x] **Step 1: Run the full frontend test suite**

Run: `pnpm test:web`

Expected: all tests pass.

- [x] **Step 2: Run the production frontend build**

Run: `pnpm web:build`

Expected: TypeScript checks and Vite build exit successfully.

- [x] **Step 3: Check the patch and record the conclusion**

Run: `git diff --check`

Append a concise, secret-free problem and conclusion to `memory.md`.
