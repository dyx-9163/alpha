# AIFAR Runtime Transactional Mutation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make AIFAR Runtime service offline and other mutations instance-serialized, non-cancellable after remote commit begins, transactional on remote mirror files, and prioritized over periodic full reconciliation.

**Architecture:** The panel uses one mutation lock per Runtime instance and a worker-level atomic commit barrier. Scale prepares task-scoped mirror files, invokes an interactive delta apply only after entering commit, and promotes mirrors after the agent persists the new spec. The agent cancels background resync when an interactive apply arrives and reconciles only changed deployments, with zero-replica changes first.

**Tech Stack:** Go 1.24, SQLite, Chi, embedded POSIX shell templates, Go unit/integration tests, fake SSH and Docker runners.

## Global Constraints

- Keep all existing `/api/v2` paths and task response shapes compatible.
- Do not add a database schema migration.
- Do not connect to real SSH or Docker in automated tests.
- Do not log full Runtime specs, environment contents, credentials, or connection strings.
- Preserve ordinary worker task cancellation semantics unless a task explicitly enters the Runtime commit phase.
- Preserve existing API, database, and old-spec compatibility; missing agent features trigger the existing upgrade path before commit.
- Do not modify or restart the real target server or its `aifar-agent` during local implementation.

---

### Task 1: Atomic Worker Commit Boundary

**Files:**
- Modify: `backend/internal/worker/manager.go`
- Modify: `backend/internal/worker/manager_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Produces: `func (l Logger) TryEnterCommit() bool`
- Produces: `func (m *Manager) tryEnterCommit(taskID string) bool`
- State: `Manager.committing map[string]bool`

- [ ] **Step 1: Write failing worker tests**

Add tests that start real worker jobs and assert observable task outcomes:

```go
func TestManagerRejectsCancellationAfterCommitStarts(t *testing.T) {
    // Start a job, call log.TryEnterCommit(), block, then assert Cancel is false.
    // Release the job and assert the persisted task status is success.
}

func TestManagerCancellationWinsBeforeCommitStarts(t *testing.T) {
    // Cancel a blocked job first, then assert log.TryEnterCommit() is false
    // and the persisted task status is cancelled.
}

func TestManagerCommitAndCancellationRaceHasOneWinner(t *testing.T) {
    // Race TryEnterCommit and Cancel repeatedly. Accepted cancellation must
    // end cancelled; accepted commit must reject cancellation and end success.
}
```

- [ ] **Step 2: Run the focused tests and confirm RED**

Run:

```powershell
go test ./internal/worker -run 'TestManager(RejectsCancellationAfterCommitStarts|CancellationWinsBeforeCommitStarts|CommitAndCancellationRaceHasOneWinner)$' -count=1 -v
```

Expected: compile failure because `Logger.TryEnterCommit` does not exist.

- [ ] **Step 3: Implement the atomic commit transition**

Under `Manager.mu`, let cancel and commit compete:

```go
func (l Logger) TryEnterCommit() bool {
    return l.manager.tryEnterCommit(l.taskID)
}

func (m *Manager) tryEnterCommit(taskID string) bool {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.cancelRequested[taskID] || m.cancels[taskID] == nil {
        return false
    }
    delete(m.cancels, taskID)
    m.committing[taskID] = true
    return true
}
```

Initialize and clean `committing`, make `Cancel` return false once commit owns the task, and make `claimJobOutcome` ignore the original cancellable context after commit. Log the new `worker.taskCommitStarted` i18n message in Chinese and English when the transition succeeds.

- [ ] **Step 4: Run focused and full worker tests**

Run:

```powershell
go test ./internal/worker -count=1
```

Expected: PASS with ordinary cancellation tests unchanged.

- [ ] **Step 5: Commit Task 1**

```powershell
git add -- backend/internal/worker/manager.go backend/internal/worker/manager_test.go backend/internal/i18n/messages.go
git commit -m "fix: add runtime task commit boundary"
```

---

### Task 2: Runtime Instance Single Writer and Immediate API Conflict

**Files:**
- Modify: `backend/internal/store/aifar_orchestration.go`
- Modify: `backend/internal/store/store_test.go`
- Modify: `backend/internal/apps/aifar/autoscale.go`
- Modify: `backend/internal/apps/aifar/service_test.go`
- Modify: `backend/internal/httpapi/task_plan_helpers.go`
- Modify: `backend/internal/httpapi/operation_lock_helpers.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`

**Interfaces:**
- Produces: `func aifarRuntimeMutationLockSpec(action string, instance store.AppInstance) operationLockSpec`
- Produces: `func (a *API) startSimplePlannedTaskWithLocks(...) (store.Task, error, bool)`
- Changes: every active `AIFAROrchestrationLock` on an instance conflicts with every new mutation on that instance, regardless of service name.

- [ ] **Step 1: Change existing lock expectations to the required behavior**

Rename and rewrite the current different-service tests so a `file` mutation blocks a `permission` mutation. Add an autoscaler assertion that any active instance mutation causes the autoscaler to skip task creation. Add an HTTP test that starts one Runtime mutation and asserts a second mutation returns `409 OPERATION_LOCKED` with the first task ID.

- [ ] **Step 2: Run lock, service, and HTTP tests and confirm RED**

Run:

```powershell
go test ./internal/store ./internal/apps/aifar ./internal/httpapi -run 'AIFAROrchestrationLock|ServiceOrchestrationLocks|Autoscal|RuntimeMutationLock' -count=1 -v
```

Expected: different-service acquisition succeeds or the second HTTP request starts a task, contradicting the new assertions.

- [ ] **Step 3: Make structured and legacy locks instance-global**

Change `findAIFAROrchestrationLockConflict` to select the oldest active lock by `instance_id` only. In the metadata fallback, reject acquisition when either `orchestrationLock` or any active entry in `orchestrationLocks` exists. Change `Autoscaler.orchestrationLocked` to return true for any unexpired lock on the instance.

- [ ] **Step 4: Acquire the API operation lock before worker start**

Create the pending task and plan, acquire this lock, then start the worker:

```go
operationLockSpec{
    Scope:      "aifar-runtime",
    ResourceID: instance.ID,
    Operation:  operationLockMutation,
    Metadata: operationLockMetadata(map[string]any{
        "action": action, "instanceId": instance.ID, "serverId": instance.ServerID,
    }),
}
```

Use the locked helper for reconcile, restart-all, stale-pod cleanup, agent uninstall, Runtime config, service install, scale-out, scale-in, and offline. If planning, lock acquisition, or worker start fails, release/delete only the newly created task and lock. Successful workers keep the existing task-owned heartbeat/release behavior.

- [ ] **Step 5: Run focused package tests**

Run:

```powershell
go test ./internal/store ./internal/apps/aifar ./internal/httpapi -count=1
```

Expected: PASS.

- [ ] **Step 6: Commit Task 2**

```powershell
git add -- backend/internal/store/aifar_orchestration.go backend/internal/store/store_test.go backend/internal/apps/aifar/autoscale.go backend/internal/apps/aifar/service_test.go backend/internal/httpapi/task_plan_helpers.go backend/internal/httpapi/operation_lock_helpers.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go
git commit -m "fix: serialize runtime instance mutations"
```

---

### Task 3: Transactional Scale Commit and Desired-State Preservation

**Files:**
- Modify: `backend/internal/apps/aifar/scale.go`
- Modify: `backend/internal/apps/aifar/autoscale.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/templates/scale-service.sh`
- Modify: `backend/internal/apps/aifar/service_test.go`

**Interfaces:**
- Consumes: optional logger interface `interface { TryEnterCommit() bool }` from Task 1.
- Adds to `scaleServiceScriptData`: `TaskID string`, `DesiredReplicas string`.
- Produces: task-scoped files `runtime-spec.json.<task>.staged` and `compose.env.<task>.staged`.

- [ ] **Step 1: Write failing desired-state and shell behavior tests**

Add a service test where metadata says `file=0`, `permission=3`, and collected live endpoints contain only one permission replica; after scaling another service, assert the saved desired values remain `file=0` and `permission=3`.

Add a rendered-script execution fixture using a temporary install root plus fake `docker` and `aifar-agent` commands. Assert:

1. while fake agent is blocked, canonical `compose.env` and `runtime-spec.json` are byte-identical to their originals;
2. fake agent failure leaves canonical files unchanged and removes `.staged` files;
3. fake agent success promotes both staged files and preserves non-target desired replicas exactly.

Add a service test with a logger whose `TryEnterCommit` returns false and assert the remote scale script is never executed.

Add a connection-loss test where the scale command returns an SSH error after the fake agent has persisted `desiredReplicas=0`; assert the service performs an independent readback, finalizes the staged mirrors, and records the operation as successful. Add the inverse test where agent desired state is still the old value and assert the task fails without updating control-plane metadata.

Add a control-plane persistence failure test after successful remote readback and assert the returned error starts with `AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED`; also assert no compensating scale-up/restart command is executed.

- [ ] **Step 2: Run the scale tests and confirm RED**

Run:

```powershell
go test ./internal/apps/aifar -run 'TestScaleService(PreservesAllNonTargetDesiredReplicas|DoesNotRunWhenCommitIsCancelled|ScriptStagesCanonicalFiles|ScriptFailureLeavesCanonicalFilesUntouched|RecoversCommittedResultAfterSSHDisconnect|RejectsUncommittedResultAfterSSHDisconnect|MarksControlPlaneRepairWithoutCompensation)$' -count=1 -v
```

Expected: existing code overwrites non-target desired values or canonical files before agent success.

- [ ] **Step 3: Preserve control-plane desired replicas**

Remove the live-endpoint loop that rewrites non-target entries in `metadataAfterServiceScale`. Render the complete metadata-derived desired assignment into the script, sorted by service name, and change `desired_replicas_for_service` to use only that assignment plus the requested target override.

- [ ] **Step 4: Add the commit boundary and independent commit context**

After agent capability preparation and before the first remote mutation:

```go
if boundary, ok := log.(interface{ TryEnterCommit() bool }); ok && !boundary.TryEnterCommit() {
    return context.Canceled
}
commitCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Minute)
defer cancel()
```

Run the scale script and target-only status readback with `commitCtx`. Ordinary test loggers without the optional interface remain compatible.

- [ ] **Step 5: Stage and promote remote mirror files**

Copy canonical `compose.env` into its task-scoped staged peer, render the spec to its staged path, run `aifar-agent reconcile-runtime --spec "$STAGED_SPEC"`, and only then rename staged files over canonical files. Use a cleanup trap that removes staged and rollback files on every exit. Never echo file contents.

- [ ] **Step 6: Resolve ambiguous SSH results from agent authority**

Extend the status collector to parse the full agent status JSON into a per-service desired/current/ready map. When the scale command returns a connection or timeout error, reconnect with the independent commit context. If agent desired state and target observations match the requested replicas, run a narrow finalize command that promotes any surviving task-scoped staged mirrors and continue to control-plane persistence. Otherwise return the original failure without changing panel metadata.

After remote success, wrap any metadata or orchestration persistence failure as:

```go
fmt.Errorf("AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED: %w", err)
```

Do not issue any compensating scale or restart. The existing task error and audit lifecycle carry the stable marker without a schema change.

- [ ] **Step 7: Run the AIFAR application tests**

Run:

```powershell
go test ./internal/apps/aifar -count=1
```

Expected: PASS.

- [ ] **Step 8: Commit Task 3**

```powershell
git add -- backend/internal/apps/aifar/scale.go backend/internal/apps/aifar/autoscale.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/templates/scale-service.sh backend/internal/apps/aifar/service_test.go
git commit -m "fix: make runtime scale commit transactional"
```

---

### Task 4: Interactive Agent Priority and Delta Apply

**Files:**
- Modify: `backend/internal/runtimeagent/ingress.go`
- Modify: `backend/internal/runtimeagent/ingress_test.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/service_test.go`

**Interfaces:**
- Produces agent features: `interactive-reconcile-priority`, `runtime-delta-apply`.
- Produces: `func changedDeployments(current RuntimeSpec, next RuntimeSpec) []DeploymentSpec`.
- Produces: background resync registration/cancellation helpers owned by `runtimeagent.Manager`.

- [ ] **Step 1: Write failing agent behavior tests**

Add tests proving:

1. an active `Resync` blocked in an unrelated deployment is canceled when `Apply` arrives;
2. `Apply` does not reconcile an unchanged unhealthy deployment;
3. a changed zero-replica deployment is reconciled before a changed positive-replica deployment;
4. an interactive `Apply` already holding `reconcileMu` is not canceled by another interactive `Apply`;
5. status exposes both new features and panel capability checks require them.

- [ ] **Step 2: Run runtimeagent and capability tests and confirm RED**

Run:

```powershell
go test ./internal/runtimeagent ./internal/apps/aifar -run 'TestManager(ApplyPreemptsBackgroundResync|ApplySkipsUnchangedDeployment|ApplyPrioritizesOfflineDeployment|SerializesInteractiveApplies)|TestRuntimeAgent.*Features' -count=1 -v
```

Expected: background resync blocks apply, unchanged deployments are reconciled, or feature assertions fail.

- [ ] **Step 3: Register and preempt background resync operations**

Track every background resync context in a mutex-protected cancel map. `Resync` registers before waiting for `reconcileMu` and unregisters on return. `Apply` cancels all registered background resyncs before taking `reconcileMu`. `StartRuntimeResync` and Docker-event sync treat a child `context.Canceled` with a live parent context as normal preemption and do not log it as a runtime failure.

- [ ] **Step 4: Reconcile only the interactive change set**

Normalize both specs, compare deployments by service name using replicas plus `deploymentSpecHash`, and pass only new or changed deployments to the interactive reconcile. Reconcile zero-replica changes as the first batch, then reconcile remaining changes concurrently. Keep periodic `Resync` as a full drift repair. After a successful apply, refresh endpoint snapshots, routes, persisted spec, and Nacos proxy state using the full new spec.

- [ ] **Step 5: Publish and require the new features**

Add the two feature strings to agent status and `requiredRuntimeAgentFeatures`. Extend the post-upgrade status command so it verifies every required feature before returning success.

- [ ] **Step 6: Run focused packages**

Run:

```powershell
go test ./internal/runtimeagent ./internal/apps/aifar -count=1
```

Expected: PASS.

- [ ] **Step 7: Commit Task 4**

```powershell
git add -- backend/internal/runtimeagent/ingress.go backend/internal/runtimeagent/ingress_test.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/service_test.go
git commit -m "fix: prioritize interactive runtime reconciliation"
```

---

### Task 5: End-to-End Regression Gates and Documentation

**Files:**
- Modify: `memory.md` (leave unstaged unless explicitly requested)
- Verify all files changed in Tasks 1-4.

**Interfaces:**
- No new production interface.
- Acceptance proof is local and fake-backed; no target-server mutation is authorized by this plan.

- [ ] **Step 1: Run focused regression suites without cache**

Run:

```powershell
go test ./internal/worker ./internal/store ./internal/runtimeagent ./internal/apps/aifar ./internal/httpapi -count=1
```

Expected: PASS.

- [ ] **Step 2: Run the full backend gate**

Run:

```powershell
pnpm test
```

Expected: PASS. If the two known diagnostic-cleaner timing tests fail only under whole-suite load, rerun those exact tests in isolation, report both results, and do not describe the full gate as passing.

- [ ] **Step 3: Build both backend targets**

Run:

```powershell
pnpm build:backend
```

Expected: Linux and Windows amd64 backend/agent binaries build successfully.

- [ ] **Step 4: Check formatting, race-prone packages, and diff hygiene**

Run:

```powershell
gofmt -w backend/internal/worker/manager.go backend/internal/worker/manager_test.go backend/internal/store/aifar_orchestration.go backend/internal/store/store_test.go backend/internal/apps/aifar/autoscale.go backend/internal/apps/aifar/scale.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/service_test.go backend/internal/runtimeagent/ingress.go backend/internal/runtimeagent/ingress_test.go backend/internal/httpapi/task_plan_helpers.go backend/internal/httpapi/operation_lock_helpers.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
go test -race ./internal/worker ./internal/store
git diff --check
```

Expected: all commands exit 0.

- [ ] **Step 5: Compare implementation against every design acceptance criterion**

Confirm each design item has code plus a regression test: instance serialization, pre-commit cancellation, post-commit rejection, staged mirrors, exact non-target desired replicas, interactive preemption, delta apply, offline-first order, feature upgrade, and unchanged API/schema.

- [ ] **Step 6: Record the reusable result**

Append a concise Chinese issue/conclusion entry to `memory.md` without credentials, full connection strings, or long logs. State explicitly that the real target agent was not changed and live write verification still requires separate confirmation.
