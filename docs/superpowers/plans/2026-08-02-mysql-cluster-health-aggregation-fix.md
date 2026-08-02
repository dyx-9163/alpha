# MySQL Cluster Health Aggregation Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent one failed member topology check from being displayed as a total InnoDB Cluster outage when all MySQL runtimes are online and another current check confirms the Primary.

**Architecture:** Move the MySQL cluster-card aggregation and complete-outage start eligibility into pure functions in `web/src/database/databaseHealth.ts`. `DatabaseView.vue` supplies normalized runtime/check health arrays and renders the existing `running`, `degraded`, `unavailable`, `probing`, or `unknown` states without layout or API changes.

**Tech Stack:** Vue 3, TypeScript, Vitest, existing AIFAR status snapshots and Element Plus status tags.

## Global Constraints

- Do not use `server.status` as a database health input.
- Preserve the existing backend collector, API, SSE contract, layout, and zh/en copy.
- Preserve unrelated uncommitted backup/restore edits in `DatabaseView.vue`.
- Enable complete-outage start only when all three MySQL runtimes are online and all three current cluster checks are explicitly offline.
- Do not commit or push the dirty worktree unless the user separately requests it.

---

### Task 1: Add cluster aggregation regression tests

**Files:**
- Test: `web/src/database/databaseHealth.test.ts`

**Interfaces:**
- Consumes: existing `DatabaseHealth` values.
- Produces: desired contracts for `resolveMySQLClusterServiceStatus(source)` and `canStartMySQLCluster(source)`.

- [x] **Step 1: Write the failing tests**

Add literal fixtures for:

```ts
{
  runtimeHealths: ['online', 'online', 'online'],
  checkHealths: ['offline', 'online', 'online'],
  hasPrimary: true
}
```

Assert that the service status is `degraded` and cluster start is `false`. Also assert `running` for three online checks with a Primary, and assert complete-outage start eligibility only for three online runtimes plus three offline checks.

- [x] **Step 2: Run the focused test and verify RED**

Run: `pnpm test:web -- web/src/database/databaseHealth.test.ts`

Expected: FAIL because the two new exported functions do not exist.

### Task 2: Implement pure cluster status functions

**Files:**
- Modify: `web/src/database/databaseHealth.ts`
- Test: `web/src/database/databaseHealth.test.ts`

**Interfaces:**
- Produces: `DatabaseServiceStatus`, `MySQLClusterHealthSource`, `resolveMySQLClusterServiceStatus`, and `canStartMySQLCluster`.

- [x] **Step 1: Implement the minimal aggregation**

Rules:

```text
all runtimes offline -> unavailable
any runtime probing -> probing
all runtimes online + Primary + all checks online -> running
all runtimes online + Primary + at least one check online -> degraded
all runtimes online + any successful check but no Primary -> unavailable
all runtimes online + no successful check + any check probing -> probing
all runtimes online + incomplete or unknown checks -> unknown
all runtimes online + all checks explicitly offline -> unavailable
some runtime online -> degraded
otherwise -> unknown
```

`canStartMySQLCluster` returns true only for exactly three online runtimes and exactly three offline checks.

- [x] **Step 2: Run the focused test and verify GREEN**

Run: `pnpm test:web -- web/src/database/databaseHealth.test.ts`

Expected: all tests in the file PASS.

### Task 3: Integrate the database page

**Files:**
- Modify: `web/src/views/DatabaseView.vue`
- Modify: `docs/superpowers/specs/2026-08-02-database-runtime-health-source-design.md`

**Interfaces:**
- Consumes: the two pure functions from Task 2.

- [x] **Step 1: Replace local cluster aggregation**

Make `mysqlClusterServiceStatus(group)` call `resolveMySQLClusterServiceStatus` with `mysqlRuntimeHealth`, `baseNodeHealth`, and `groupCurrentPrimaryEndpoint` evidence.

- [x] **Step 2: Tighten the start button guard**

Make `isMysqlClusterStartable(group)` call `canStartMySQLCluster`; remove the old rule that treated every degraded state with online runtimes as startable.

- [x] **Step 3: Record the clarified design boundary**

Document that partial member topology-check failure with current Primary evidence is degraded, and that complete-outage start requires all member cluster checks to be offline.

### Task 4: Verify the fix

**Files:**
- Verify only; no additional production files.

- [x] **Step 1: Run focused tests**

Run: `pnpm test:web -- web/src/database/databaseHealth.test.ts`

- [x] **Step 2: Run the complete frontend test suite**

Run: `pnpm test:web`

- [x] **Step 3: Run the production frontend build**

Run: `pnpm web:build`

- [x] **Step 4: Check whitespace and scope**

Run: `git diff --check` and inspect the diff for the four scoped files. Confirm no unrelated source deletion and no overwrite of the existing backup/restore edits.
