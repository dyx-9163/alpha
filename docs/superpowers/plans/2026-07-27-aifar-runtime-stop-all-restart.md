# AIFAR Runtime Stop-All Restart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Change “全部重启（读取新配置）” from per-replica rolling replacement to a maintenance-window operation that preflights, removes every Runtime Pod, starts every desired Pod without per-Pod health gating, and then verifies the whole instance.

**Architecture:** Keep the existing panel API, SSH script, Agent CLI command, and `/runtime/restart-all` endpoint. Replace only `runtimeagent.Manager.RestartAll` semantics, extracting a detached container-start primitive so all Docker runs are submitted before health waits. The panel backend records five task steps and the frontend warns about full downtime; single-service rolling release continues to use `ensureDeployment`/`replaceContainer` unchanged.

**Tech Stack:** Go 1.24, Chi, worker/task store, SSH installer templates, Vue 3, TypeScript, Element Plus, Vitest.

## Global Constraints

- Existing HTTP path, request body, permission, audit action, button label, and Agent CLI command remain compatible.
- `aifar-agent`, proxy listeners, and Docker remain running; only Runtime Pod containers are removed.
- A startup failure does not stop later Pod start attempts; errors are aggregated after all attempts.
- Successful new Pods remain running on failure or cancellation; no automatic rollback is added.
- Deployments with `replicas: 0` remain offline.
- Single-service artifact update retains its existing rolling-update behavior.
- New user-visible backend and frontend text must exist in both Chinese and English.
- Follow strict TDD: write each behavior test, observe the expected failure, then write production code.

---

## File Structure

- Modify `backend/internal/runtimeagent/ingress.go`: stop-all restart orchestration, preflight, instance-wide Pod removal, detached start, aggregate verification.
- Modify `backend/internal/runtimeagent/ingress_test.go`: stateful Docker fake and stop-all restart behavior tests.
- Modify `backend/internal/apps/aifar/runtime_restart.go`: five worker steps and final status handling.
- Modify `backend/internal/apps/aifar/service_test.go`: real service step-contract tests.
- Modify `backend/internal/httpapi/containers_aifar_runtime.go`: five planned task steps.
- Modify `backend/internal/httpapi/containers_aifar_runtime_test.go`: API task-plan contract and fake module steps.
- Modify `backend/internal/i18n/messages.go`: stop-all restart step titles and logs in zh/en.
- Modify `web/src/i18n/messages.ts`: full-downtime confirmation text in zh/en.
- Modify `web/src/containers/runtime/runtimeRules.test.ts`: generated confirmation-message contract.

---

### Task 1: Split Docker start from readiness waiting

**Files:**
- Modify: `backend/internal/runtimeagent/ingress.go:845-934`
- Test: `backend/internal/runtimeagent/ingress_test.go`

**Interfaces:**
- Produces: `runContainerDetached(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name string) error`.
- Preserves: `runContainer` continues to start and wait for readiness for reconcile and rolling-update callers.

- [ ] **Step 1: Write the failing detached-start test**

Add `TestManagerRunContainerDetachedDoesNotWaitForHealth`. Use a runner that records Docker calls, returns success for `docker run`, and fails the test if it receives the readiness inspect format. Assert that `runContainerDetached` returns after exactly one Docker run and retains the existing labels, env-file, mount, logical container name, resource, and health-check arguments.

```go
if err := manager.runContainerDetached(context.Background(), spec, deployment, 1, name); err != nil {
    t.Fatal(err)
}
if strings.Contains(runner.callsString(), ".State.Health") {
    t.Fatalf("detached start must not wait for readiness:\n%s", runner.callsString())
}
```

- [ ] **Step 2: Run the focused test and confirm RED**

Run from `backend/`:

```powershell
go test ./internal/runtimeagent -run TestManagerRunContainerDetachedDoesNotWaitForHealth -count=1
```

Expected: build failure because `runContainerDetached` does not exist.

- [ ] **Step 3: Extract the minimal detached-start primitive**

Move the Docker argument construction and `runner.Run("docker", "run", ...)` portion from `runContainerNamed` into:

```go
func (m *Manager) runContainerDetached(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name string) error {
    return m.runContainerDetachedNamed(ctx, spec, deployment, replica, name, name)
}

func (m *Manager) runContainerDetachedNamed(ctx context.Context, spec RuntimeSpec, deployment DeploymentSpec, replica int, name, logicalName string) error
```

Keep `runContainerNamed` as a compatibility wrapper:

```go
if err := m.runContainerDetachedNamed(ctx, spec, deployment, replica, name, logicalName); err != nil {
    return err
}
if err := m.waitContainerReady(ctx, name); err != nil {
    return err
}
```

- [ ] **Step 4: Run the focused and existing container tests**

```powershell
go test ./internal/runtimeagent -run 'TestManagerRunContainer|TestManagerReplaces|TestManagerStartsStopped' -count=1
```

Expected: PASS; existing rolling replacement still waits before promotion.

- [ ] **Step 5: Commit Task 1**

```powershell
git add backend/internal/runtimeagent/ingress.go backend/internal/runtimeagent/ingress_test.go
git commit -m "refactor(runtime): split detached pod start"
```

---

### Task 2: Replace rolling RestartAll with stop-all orchestration

**Files:**
- Modify: `backend/internal/runtimeagent/ingress.go:221-280`
- Test: `backend/internal/runtimeagent/ingress_test.go:46-169`

**Interfaces:**
- Produces: `restartReplicaPlan`, `preflightRestartAll`, `listInstanceRuntimePods`, `stopAllRuntimePods`, and `verifyRestartedRuntime` as private runtimeagent helpers.
- Consumes: `runContainerDetached` from Task 1.
- Preserves: public `RestartAll(ctx context.Context, spec RuntimeSpec) error`.

- [ ] **Step 1: Replace the old rolling-order test with a failing stop-before-start test**

Create a stateful `restartAllRunner` whose container map initially includes current, old-revision, `-next-`, and extra-replica Pods for the same install root. Assert every `docker rm -f` occurs before the first `docker run -d`, and assert no `docker rename`, `-next-`, or `-old-` operation appears.

```go
lastRemove := strings.LastIndex(calls, "docker rm -f")
firstRun := strings.Index(calls, "docker run -d")
if lastRemove < 0 || firstRun < 0 || lastRemove > firstRun {
    t.Fatalf("all old pods must be removed before any new pod starts:\n%s", calls)
}
```

- [ ] **Step 2: Run the test and confirm RED**

```powershell
go test ./internal/runtimeagent -run TestManagerRestartAllStopsEveryPodBeforeStartingAnyPod -count=1
```

Expected: FAIL because current `RestartAll` creates replacement containers before removing all originals.

- [ ] **Step 3: Add full preflight before mutation**

Implement `preflightRestartAll` so it:

- normalizes each enabled Deployment strategy;
- checks `docker network inspect <spec.Network>`;
- checks each unique enabled image with `docker image inspect`;
- validates every non-empty env file and bind-mount source with `os.Stat`;
- builds desired replica names without requiring current containers to exist;
- returns before any removal on error.

Errors must identify the service and failing image/path/network.

- [ ] **Step 4: Add instance-wide Pod listing and removal**

Implement `listInstanceRuntimePods` using Docker labels:

```go
docker ps -a \
  --filter label=aifar.app=aifar \
  --filter label=aifar.install-root=<installRoot> \
  --filter label=aifar.component=pod \
  --format {{.Names}}
```

`stopAllRuntimePods` removes every returned name, continues after individual removal failures, re-lists afterward, and refuses to start if any Runtime Pod remains. After successful removal call `refreshInstanceEndpoints` so the proxy cache has no stale container addresses.

- [ ] **Step 5: Start all desired Pods without health gating**

Loop through the precomputed plan in RuntimeSpec order. Call `runContainerDetached` for every desired replica. Append errors as:

```go
fmt.Errorf("start AIFAR deployment %s replica %d: %w", service, replica, err)
```

Do not return on an individual startup failure. Check `ctx.Err()` between Docker operations; cancellation stops unsubmitted work and keeps completed work.

- [ ] **Step 6: Add unified verification**

After all start attempts, call `waitContainerReady` for every successfully submitted Pod, using each Deployment's `progressDeadlineSeconds` context. Refresh Endpoint and DeploymentStatus for every Deployment, then require:

```text
current == desired
ready == desired
updated == desired
available == desired
```

For `replicas: 0`, require no matching running Pod. Aggregate startup and verification failures with stable service/replica context and return one error after all verifications.

- [ ] **Step 7: Add the remaining failing behavior tests one at a time**

Add and individually run:

- `TestManagerRestartAllRestoresMissingDesiredPod`
- `TestManagerRestartAllLeavesZeroReplicaDeploymentOffline`
- `TestManagerRestartAllContinuesStartingAfterOnePodFails`
- `TestManagerRestartAllDoesNotStartWhenOldPodRemovalLeavesResidue`
- `TestManagerRestartAllAggregatesHealthFailures`
- `TestManagerRestartAllCancellationKeepsPartialResultWithoutRollback`
- `TestManagerRestartAllPreflightFailureKeepsExistingPods`

For each test, first run it against the incomplete implementation and confirm the assertion fails for the intended missing behavior, then implement only enough to pass it.

- [ ] **Step 8: Run all runtimeagent tests**

```powershell
go test ./internal/runtimeagent -count=1
```

Expected: PASS. Existing `Apply`, `ensureDeployment`, rolling artifact update, proxy, discovery, and resync tests remain green.

- [ ] **Step 9: Commit Task 2**

```powershell
git add backend/internal/runtimeagent/ingress.go backend/internal/runtimeagent/ingress_test.go
git commit -m "feat(runtime): restart all pods with full downtime"
```

---

### Task 3: Update backend task steps and restart language

**Files:**
- Modify: `backend/internal/apps/aifar/runtime_restart.go:27-105`
- Modify: `backend/internal/apps/aifar/service_test.go:1882-2015`
- Modify: `backend/internal/httpapi/containers_aifar_runtime.go:247-250`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go:849-1028`
- Modify: `backend/internal/i18n/messages.go:139-146,343-349`

**Interfaces:**
- Produces stable task step codes: `load-instance`, `preflight-runtime`, `stop-all-pods`, `start-all-pods`, `verify-runtime`.
- Preserves `registry.RuntimeRestartRequest`, the API route, permission, task type, target, audit action, and remote command.

- [ ] **Step 1: Change API task-plan expectations first**

Update `TestAIFARRuntimeRestartAllCreatesPlannedTaskForSelectedInstance` and the fake action module to require exactly:

```go
[]string{"load-instance", "preflight-runtime", "stop-all-pods", "start-all-pods", "verify-runtime"}
```

Run:

```powershell
go test ./internal/httpapi -run TestAIFARRuntimeRestartAllCreatesPlannedTaskForSelectedInstance -count=1
```

Expected: FAIL because production still plans `rolling-restart`.

- [ ] **Step 2: Update HTTP planning and backend i18n**

Replace the rolling step key with the two new step keys in zh/en. Change start/completion logs to describe stopping all Runtime Pods, starting all desired Pods, and reading the latest env configuration. Do not change the task type or audit action.

- [ ] **Step 3: Change the real service step-contract test first**

Update `TestModuleRestartRuntimeInvokesAgentWithPersistedSpecAndReleasesLock` to require all five success entries and to reject `rolling-restart`. Keep the assertion that the generated script calls:

```sh
aifar-agent restart-runtime --spec "$SPEC_PATH"
```

Run:

```powershell
go test ./internal/apps/aifar -run 'TestModuleRestartRuntime|TestServiceRestartRuntime' -count=1
```

Expected: FAIL until `runtime_restart.go` uses the five-step contract.

- [ ] **Step 4: Implement the five-step recorder sequence**

Keep instance loading and Agent preflight as the first two steps. The blocking Agent command owns stop/start/verify internally; record `stop-all-pods` as active while it executes, and after a successful response complete `stop-all-pods`, `start-all-pods`, and `verify-runtime` in order. On a remote error mark the active step failed/cancelled and preserve the detailed Agent error in target/task logs. Do not report overall success unless the Agent command returned success, because Agent success now includes unified verification.

- [ ] **Step 5: Run backend restart tests**

```powershell
go test ./internal/apps/aifar ./internal/httpapi -run 'RestartRuntime|RestartAll' -count=1
```

Expected: PASS, including permission, running-Agent requirement, cancellation, lock release, and audit tests.

- [ ] **Step 6: Commit Task 3**

```powershell
git add backend/internal/apps/aifar/runtime_restart.go backend/internal/apps/aifar/service_test.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
git commit -m "feat(runtime): expose stop-all restart task phases"
```

---

### Task 4: Replace rolling confirmation copy with downtime warning

**Files:**
- Modify: `web/src/i18n/messages.ts:295-298,1219`
- Test: `web/src/containers/runtime/runtimeRules.test.ts`

**Interfaces:**
- Consumes existing `{ services, replicas }` interpolation.
- Preserves the button label `containers.restartAllRuntime` and existing API call.

- [ ] **Step 1: Write the failing message-contract test**

Render `messages.zh['containers.confirmRestartAllRuntime']` and its English equivalent with `{ services: 10, replicas: 10 }`. Assert the output contains both counts, explicitly states full business downtime and stop-all-before-start, explains that later starts continue after one startup failure, and states there is no automatic rollback. Assert it no longer describes rolling replacement.

- [ ] **Step 2: Run the frontend test and confirm RED**

```powershell
pnpm test:web -- runtimeRules.test.ts
```

Expected: FAIL because current text promises rolling replacement and availability.

- [ ] **Step 3: Implement zh/en confirmation text**

Use concise customer-visible wording matching the approved design. Keep the existing title and button label. Do not modify layout, component structure, or API behavior.

- [ ] **Step 4: Run frontend logic tests**

```powershell
pnpm test:web
```

Expected: all frontend tests PASS.

- [ ] **Step 5: Commit Task 4**

```powershell
git add web/src/i18n/messages.ts web/src/containers/runtime/runtimeRules.test.ts
git commit -m "fix(web): warn about stop-all runtime restart"
```

---

### Task 5: Full verification and Agent deliverable

**Files:**
- Verify only; do not add generated binaries to Git unless the repository's existing build flow already tracks them.

**Interfaces:**
- Produces a verified source change and a locally built Linux amd64 Agent artifact through the existing backend build flow.

- [ ] **Step 1: Format and inspect the diff**

```powershell
gofmt -w backend/internal/runtimeagent/ingress.go backend/internal/runtimeagent/ingress_test.go backend/internal/apps/aifar/runtime_restart.go backend/internal/apps/aifar/service_test.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
git diff --check
```

Confirm the diff does not modify single-service rolling-update code except shared `runContainer` extraction with unchanged behavior.

- [ ] **Step 2: Run all backend tests**

```powershell
pnpm test
```

Expected: all Go tests PASS.

- [ ] **Step 3: Run all frontend tests**

```powershell
pnpm test:web
```

Expected: all Vitest tests PASS.

- [ ] **Step 4: Build frontend and backend**

```powershell
pnpm web:build
pnpm backend:build
```

Expected: Vue type checking, Vite build, and Linux/Windows amd64 Go builds PASS. Record the generated Linux Agent artifact path and checksum for deployment handoff.

- [ ] **Step 5: Run focused repeat tests for ordering-sensitive behavior**

```powershell
go test ./internal/runtimeagent -run 'TestManagerRestartAll' -count=10
```

Expected: ten clean passes with no ordering or cancellation flakes.

- [ ] **Step 6: Commit any verification-only formatting changes**

If formatting changed tracked files after Task 4:

```powershell
git add backend web/src
git commit -m "chore: format stop-all runtime restart"
```

Do not commit unrelated workspace files or generated release packages.
