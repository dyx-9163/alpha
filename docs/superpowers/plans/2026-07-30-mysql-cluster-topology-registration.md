# MySQL Cluster Topology Registration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ensure every installed MySQL InnoDB Cluster has an authoritative, maintenance-safe control-plane topology and repair the current strictly valid legacy group.

**Architecture:** Add one transactional store boundary for cluster, instance, and member registration. Add a narrow startup migration for legacy `mysql_cluster_*` groups; keep the existing maintenance validator strict.

**Tech Stack:** Go 1.24, SQLite via `modernc.org/sqlite`, existing MySQL application service and store migrations.

## Global Constraints

- Preserve all existing dirty-worktree changes.
- Do not weaken MySQL maintenance or reconciliation gates.
- Do not modify remote MySQL data during control-plane repair.
- Follow red-green-refactor and run the complete backend verification before handoff.

---

### Task 1: Transactional topology registration

**Files:**
- Modify: `backend/internal/store/app_clusters.go`
- Test: `backend/internal/store/enterprise_foundations_test.go`
- Modify: `backend/internal/apps/mysql/service.go`
- Test: `backend/internal/apps/mysql/service_test.go`

**Interfaces:**
- Produces: `SaveAppClusterDeployment(AppCluster, []AppInstance, []AppClusterMember) ([]AppInstance, error)`.
- Consumes: existing `AppCluster`, `AppInstance`, and `AppClusterMember` models.

- [ ] Write a failing store test proving all six topology records commit together and roll back on invalid membership.
- [ ] Run the focused store test and confirm the missing method/behavior fails.
- [ ] Implement the minimal transactional store method.
- [ ] Write a failing MySQL install test proving the generated ID uses `cluster_` and the authoritative membership is complete.
- [ ] Run the focused MySQL test and confirm the legacy install path fails it.
- [ ] Change cluster installation to register all three instances and topology through the transactional boundary.
- [ ] Run the focused store and MySQL tests until green.

### Task 2: Strict legacy topology repair

**Files:**
- Modify: `backend/internal/store/migrations.go`
- Create: `backend/internal/store/mysql_cluster_topology.go`
- Test: `backend/internal/store/enterprise_foundations_test.go`

**Interfaces:**
- Produces: migration `2026073001`, calling `backfillLegacyMySQLClusterTopologies(*sql.Tx) error`.

- [ ] Write a failing migration test with three valid legacy MySQL nodes plus three Router records.
- [ ] Assert the migration creates one `cluster_*` row, three members, and rewrites all six metadata documents.
- [ ] Add malformed, duplicate-server, and maintenance-marker cases that must remain untouched.
- [ ] Run the focused migration tests and confirm failure before implementation.
- [ ] Implement strict grouping, validation, and transactional metadata rewrite.
- [ ] Run focused migration and maintenance tests until green.

### Task 3: Verification and live repair

**Files:**
- Modify: `memory.md`

- [ ] Run `go test ./internal/store ./internal/apps/mysql` with a worktree-local `GOCACHE`.
- [ ] Run `pnpm test` and `pnpm backend:build`.
- [ ] Restart the local development backend so migration `2026073001` runs against the current SQLite database.
- [ ] Verify the collector no longer reports `MYSQL_MAINTENANCE_STATE_INVALID` for the three cluster instances.
- [ ] Verify the database page shows the actual three-node state and Router endpoints.
- [ ] Record the result in `memory.md` without secrets.
