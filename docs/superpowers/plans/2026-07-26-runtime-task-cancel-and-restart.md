# Runtime Task Cancellation and Rolling Restart Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a single-task stop action in Task Center and an AIFAR Runtime action that sequentially rebuilds every currently enabled service so edited environment files are loaded.

**Architecture:** The control plane creates an auditable worker task, the AIFAR module invokes the installed agent, and the agent serially replaces Runtime containers in spec order. Each replacement starts from the current spec and env files, must become healthy before it is promoted, and preserves the old container when startup, health checking, or cancellation fails.

**Tech Stack:** Go 1.24, Chi, worker tasks, embedded shell templates, Vue 3, TypeScript, Element Plus, Vitest.

## Global Constraints

- Only the selected AIFAR Runtime instance is affected.
- Restart only Deployments whose `desiredReplicas` is greater than zero; keep zero-replica services offline.
- Process services in Runtime spec order and replicas in ascending ordinal order.
- Stop at the first failed replacement. Do not start later replacements.
- On cancellation, stop future work and health waiting, remove only the unpromoted temporary container, retain the old container, and keep already promoted replacements.
- Every control-plane mutation runs through worker tasks, task steps, targets, audit, authorization, and zh/en i18n.
- Task Center cancellation applies only to the currently selected `pending` or `running` task.
- Follow test-first RED/GREEN commits and keep unrelated existing changes, including `memory.md`, out of feature commits.

---

## Task 1: Add the Runtime agent rolling-replacement core

**Files:**

- Modify: `backend/internal/runtimeagent/ingress.go`
- Modify: `backend/internal/runtimeagent/ingress_test.go`

- [ ] Add failing tests for `Manager.RestartAll` proving spec-order execution, ascending replica order, and skipping Deployments with `desiredReplicas == 0`.
- [ ] Add a failing test proving a health-check failure removes the new temporary container, leaves the original named container active, and prevents later replicas/services from starting.
- [ ] Add a failing cancellation test proving cancellation interrupts health waiting, cleans up the unpromoted temporary container, and does not revert already promoted containers.
- [ ] Run `go test ./internal/runtimeagent -run 'TestManagerRestartAll|TestReplaceContainer'` from `backend/` and confirm RED failures describe missing behavior.
- [ ] Implement `Manager.RestartAll(ctx, spec)`: acquire `reconcileMu`, normalize and validate the spec, iterate enabled Deployments and replicas deterministically, require each expected old container, and refresh endpoint state after successful promotions.
- [ ] Generalize `replaceDriftedContainer` into a forced-capable replacement helper shared by reconcile and restart-all.
- [ ] Make temporary-container cleanup cancellation-safe by using a bounded cleanup context derived with `context.WithoutCancel`; only rename/promote after the new container passes health checks.
- [ ] Preserve the current reconcile behavior: hash drift still determines whether normal reconciliation replaces a container, while restart-all always replaces enabled replicas.
- [ ] Run `go test ./internal/runtimeagent` and confirm GREEN.
- [ ] Commit only these files with message `feat: add runtime rolling restart core`.

## Task 2: Expose restart-all through the installed agent

**Files:**

- Modify: `backend/cmd/aifar-agent/main.go`
- Modify: `backend/cmd/aifar-agent/main_test.go`

- [ ] Add failing tests proving the client posts a Runtime spec to `/runtime/restart-all`, transient EOF/network failures are retried consistently, and agent status advertises `restart-runtime` support.
- [ ] Add a failing handler test proving `POST /runtime/restart-all` decodes the spec, calls `Manager.RestartAll`, and returns a non-success response when replacement fails.
- [ ] Run `go test ./cmd/aifar-agent -run 'TestPostRuntimeRestart|TestRuntimeRestartHandler|TestAgentStatus'` and confirm RED.
- [ ] Add CLI command `restart-runtime --spec <path>` and update usage text.
- [ ] Register `POST /runtime/restart-all`, reuse the existing Runtime spec validation/decoding path, and call `Manager.RestartAll` with the request context so worker cancellation propagates.
- [ ] Refactor the retry helper only as needed so reconcile and restart-all share request behavior without changing their endpoint paths.
- [ ] Add `restart-runtime` to the agent feature list returned by status.
- [ ] Run `go test ./cmd/aifar-agent` and confirm GREEN.
- [ ] Commit only these files with message `feat: expose runtime restart through agent`.

## Task 3: Add the AIFAR registry and remote execution contract

**Files:**

- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Create: `backend/internal/apps/aifar/runtime_restart.go`
- Create: `backend/internal/apps/aifar/templates/runtime-restart.sh`
- Modify: `backend/internal/apps/aifar/service_test.go`
- Modify: `backend/internal/i18n/messages.go`

- [ ] Add failing service tests proving restart-all takes the instance operation lock, uploads/runs the embedded restart script, passes the persisted Runtime spec path, and releases the lock on success and failure.
- [ ] Add a failing module-adapter test, or extend the closest existing module test, proving registry request fields are mapped without loss.
- [ ] Run `go test ./internal/apps/aifar -run 'Test.*RestartRuntime'` and confirm RED.
- [ ] Add `registry.RuntimeRestartRequest` and `registry.RuntimeRestartModule`; include instance, server, language, actor, and reason in the request.
- [ ] Add the corresponding AIFAR service request with `TaskID`, and expose `RestartRuntime` through `module.go`.
- [ ] Implement `Service.RestartRuntime` using the existing operation-lock and remote-run patterns used by Runtime reconciliation.
- [ ] Embed `runtime-restart.sh`; make it validate agent/spec/env prerequisites and execute `aifar-agent restart-runtime --spec "$SPEC_PATH"` without accepting arbitrary shell input.
- [ ] Add zh/en backend messages for restart progress, preflight failures, cancellation, and replacement failure.
- [ ] Run `go test ./internal/apps/aifar ./internal/apps/registry` and confirm GREEN.
- [ ] Commit only these files with message `feat: add aifar runtime restart module`.

## Task 4: Add the control-plane task endpoint

**Files:**

- Modify: `backend/internal/httpapi/aifar_runtime_controller.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`
- Modify: `backend/internal/i18n/messages.go`

- [ ] Add failing API tests for `POST /api/v2/containers/aifar/runtime/restart-all`: authorized requests return `202` and a task ID; the task type is `aifar.runtime.restart-all`; the selected instance/server are recorded as the target; and only that instance reaches the fake restart module.
- [ ] Assert the task exposes steps `load-instance`, `preflight-runtime`, `rolling-restart`, and `verify-runtime`, and writes audit action `containers.aifar.runtime.restart-all`.
- [ ] Add failing tests for missing instance, non-running/unavailable agent, insufficient permission, module failure, and cancellation propagation.
- [ ] Run `go test ./internal/httpapi -run 'TestAIFARRuntimeRestartAll'` and confirm RED.
- [ ] Mount the endpoint with `AppsManage` authorization and parse the existing instance action request shape (`instanceId`, optional `reason`).
- [ ] Resolve the selected Runtime target using the existing agent-required resolver, create the task/target/steps, and run the registry restart module inside the worker callback.
- [ ] Keep the handler limited to validation, task creation, audit, and response; place no SSH or Docker implementation in `httpapi`.
- [ ] Add all new user-visible backend messages to zh/en i18n.
- [ ] Run `go test ./internal/httpapi` and confirm GREEN.
- [ ] Commit only these files with message `feat: add runtime restart task endpoint`.

## Task 5: Add single-task cancellation to Task Center

**Files:**

- Create: `web/src/tasks/actions.ts`
- Create: `web/src/tasks/actions.test.ts`
- Modify: `web/src/components/TaskLogPane.vue`
- Modify: the closest existing `TaskLogPane` source/DOM test, or create `web/src/components/TaskLogPane.test.ts`
- Modify: `web/src/i18n/messages.ts`

- [ ] Add a failing unit test for `isTaskCancellable`, covering `pending`, `running`, terminal statuses, empty input, whitespace, and case normalization.
- [ ] Add a failing component/source test proving the toolbar contains one stop action, uses a confirmation prompt, is disabled for terminal/no selection state, and posts only the selected task ID to `/tasks/{id}/cancel`.
- [ ] Run `pnpm --dir web exec vitest run src/tasks/actions.test.ts src/components/TaskLogPane.test.ts` and confirm RED.
- [ ] Implement `isTaskCancellable(status?: string)` and a typed `CancelTaskResponse { taskId: string; cancelled: boolean }`.
- [ ] Add the stop button to `TaskLogPane.vue` using the existing confirmation component/style; show loading while the request is active.
- [ ] Call `apiPost<CancelTaskResponse>(\`/tasks/${selectedTaskId.value}/cancel\`)`; show success only when `cancelled` is true, otherwise warn that the task already finished or could not be cancelled.
- [ ] Reload task detail/list after the response and let the existing status logic stop polling when the task reaches a terminal state.
- [ ] Add concise zh/en labels, confirmation, success, warning, and error messages.
- [ ] Run the focused Vitest command and `pnpm --dir web exec vue-tsc --noEmit` and confirm GREEN.
- [ ] Commit only these files with message `feat: add task center stop action`.

## Task 6: Add the Runtime restart-all UI action

**Files:**

- Modify: `web/src/containers/runtime/api.ts`
- Modify: `web/src/containers/runtime/api.test.ts`
- Modify: `web/src/containers/runtime/selectors.ts`
- Modify: `web/src/containers/runtime/runtimeRules.test.ts`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeWorkspace.vue`
- Modify: `web/src/views/ContainersView.vue`
- Modify: the closest Runtime workspace/view source test
- Modify: `web/src/i18n/messages.ts`

- [ ] Add failing API tests proving `restartAllRuntime(query, instanceId, reason?)` posts exactly to `/containers/aifar/runtime/restart-all` with the selected instance.
- [ ] Add failing selector tests proving the confirmation count includes only Deployments with `desiredReplicas > 0` and sums their replicas.
- [ ] Add a failing workspace/view test proving there is one restart-all button, it is scoped to the selected AIFAR Runtime instance, asks for confirmation, and tracks the returned task through the existing task scheduler/UI flow.
- [ ] Run `pnpm --dir web exec vitest run src/containers/runtime/api.test.ts src/containers/runtime/runtimeRules.test.ts` plus the selected workspace/view test and confirm RED.
- [ ] Add the typed API wrapper and online-deployment/replica count selector.
- [ ] Extend the Runtime context with restart availability, loading state, and handler.
- [ ] Place the restart-all action in the existing Runtime top toolbar. Disable it when no instance is selected, no enabled Deployments exist, another Runtime mutation is active, or the agent is unavailable.
- [ ] In `ContainersView.vue`, show a destructive-operation confirmation explaining sequential replacement, new env-file loading, health gating, zero-replica exclusion, first-failure stop, and partial completion on cancellation.
- [ ] Submit the selected instance only, register the returned task with the existing scheduler/detail refresh mechanism, and surface API errors through the standard message path.
- [ ] Add all user-visible zh/en strings.
- [ ] Run focused Vitest tests, `pnpm test:web`, and `pnpm web:build`; confirm GREEN.
- [ ] Commit only these files with message `feat: add runtime restart all action`.

## Task 7: Verify the integrated behavior and record the result

**Files:**

- Modify: `memory.md` (leave out of feature commits)

- [ ] Use `superpowers:verification-before-completion` before making completion claims.
- [ ] Run `go test ./internal/runtimeagent ./cmd/aifar-agent ./internal/apps/aifar ./internal/apps/registry ./internal/httpapi` from `backend/`.
- [ ] Run `pnpm test` from the repository root.
- [ ] Run `pnpm test:web`.
- [ ] Run `pnpm test:scripts`.
- [ ] Run `pnpm web:build`.
- [ ] Inspect `git diff --check`, `git status --short`, and the feature commit list; verify pre-existing unrelated changes were preserved and `memory.md` was not committed.
- [ ] Append a concise reusable problem/conclusion entry to `memory.md`, including exact verification results and any real-server acceptance still outstanding.
- [ ] Report that automated fake-remote/UI validation passed without claiming a real Docker rolling restart unless it was actually exercised on an openEuler target.
