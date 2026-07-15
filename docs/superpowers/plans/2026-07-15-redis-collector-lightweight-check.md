# Redis Collector Lightweight Check Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make background Redis health collection complete within the existing five-second deadline by replacing the full Sentinel workflow with one read-only SSH command.

**Architecture:** Propagate `registry.CheckRequest.Actor` into the Redis service. When the actor is `collector`, build one compound command that checks the local Redis data service, local Sentinel service, and local data role, parse stable result markers, and update only the current instance. All non-collector checks retain the existing full topology workflow.

**Tech Stack:** Go 1.24, existing Redis registry module/service, fake SSH remote tests, SQLite-backed collector status flow.

## Global Constraints

- Keep the collector timeout at five seconds.
- Collector checks must be read-only and execute exactly one remote command.
- Use the instance-bound Redis credential; fall back to the panel default only when no Redis credential is bound.
- Do not change the manual Redis check workflow or the frontend.
- Do not modify unrelated staged or working-tree changes.

---

### Task 1: Add the lightweight Redis collector check

**Files:**
- Create: `backend/internal/apps/redis/collector_check.go`
- Modify: `backend/internal/apps/redis/service.go:50-55,539-645`
- Modify: `backend/internal/apps/redis/module.go:185-199`
- Test: `backend/internal/apps/redis/service_test.go`

**Interfaces:**
- Consumes: `registry.CheckRequest.Actor`, `Service.redisCheckPassword`, `Remote.Run`, `redisRuntimeCheckResult`, and the existing instance status writers.
- Produces: `CheckRequest.Actor string`, `Service.checkRedisCollector(ctx, req, password) (CheckResult, error)`, `redisCollectorCheckCommand(server, instance, password) string`, and `parseRedisCollectorCheckOutput(stdout, expectsData, expectsSentinel) (redisRuntimeCheckResult, string, error)`.

- [ ] **Step 1: Write the failing collector-path regression test**

Add `TestModuleCollectorCheckUsesOneReadOnlyCommand` to `service_test.go`. Construct a Sentinel instance that runs both data Redis and Sentinel, bind a Redis credential, configure the fake remote response for the marker `AIFAR_REDIS_CHECK_V1`, call `NewModule(...).Check` with `Actor: "collector"`, and assert:

```go
if got := len(remote.commands); got != 1 {
	t.Fatalf("collector Redis check should use one remote command, got %d: %s", got, remote.joinedCommands())
}
command := remote.joinedCommands()
for _, forbidden := range []string{"open_firewall_ports", "SENTINEL replicas", "SENTINEL sentinels", "get-master-addr-by-name"} {
	if strings.Contains(command, forbidden) {
		t.Fatalf("collector command must remain lightweight and read-only; found %q in %s", forbidden, command)
	}
}
if !strings.Contains(command, "custom-redis-password") || strings.Contains(command, "panel-default-password") {
	t.Fatalf("collector command must use the bound Redis credential: %s", command)
}
if status.Status != "running" {
	t.Fatalf("expected running collector status, got %+v", status)
}
```

The fake response must be:

```text
AIFAR_REDIS_CHECK_V1
data=running
sentinel=running
role=master
```

- [ ] **Step 2: Run the regression test and verify RED**

Run from `backend/`:

```powershell
go test ./internal/apps/redis -run TestModuleCollectorCheckUsesOneReadOnlyCommand -count=1
```

Expected: FAIL because the actor is not propagated and the existing full workflow executes multiple remote commands.

- [ ] **Step 3: Add focused parser tests**

Add table-driven tests for `parseRedisCollectorCheckOutput` covering:

```go
{
	name: "data and sentinel running",
	stdout: "AIFAR_REDIS_CHECK_V1\ndata=running\nsentinel=running\nrole=master\n",
	expectsData: true,
	expectsSentinel: true,
	wantStatus: "running",
	wantRole: "master",
},
{
	name: "data failed and sentinel running",
	stdout: "AIFAR_REDIS_CHECK_V1\ndata=failed\nsentinel=running\nrole=replica\n",
	expectsData: true,
	expectsSentinel: true,
	wantStatus: "degraded",
	wantRole: "replica",
},
```

Also assert that missing `AIFAR_REDIS_CHECK_V1` or a missing expected component marker returns an error.

- [ ] **Step 4: Run parser tests and verify RED**

Run from `backend/`:

```powershell
go test ./internal/apps/redis -run 'TestParseRedisCollectorCheckOutput|TestModuleCollectorCheckUsesOneReadOnlyCommand' -count=1
```

Expected: FAIL to compile because the parser and collector request field do not exist.

- [ ] **Step 5: Implement actor propagation and collector dispatch**

Add the actor to the service request:

```go
type CheckRequest struct {
	Instance        store.AppInstance
	Server          store.Server
	Language        string
	DefaultPassword string
	Actor           string
}
```

Propagate it in `Module.Check`:

```go
Actor: req.Actor,
```

In `Service.Check`, immediately after resolving the bound password, dispatch collector calls:

```go
if strings.EqualFold(strings.TrimSpace(req.Actor), "collector") {
	return s.checkRedisCollector(ctx, req, password)
}
```

- [ ] **Step 6: Implement the one-command collector helper**

Create `collector_check.go`. `redisCollectorCheckCommand` must emit exactly one shell program with the following stable output contract:

```text
AIFAR_REDIS_CHECK_V1
data=running|failed
sentinel=running|failed
role=master|replica|unknown
```

Only emit `data` or `sentinel` when that component is expected for the instance. The shell must resolve the current/legacy install root, use `REDISCLI_AUTH`, check `systemctl` plus `PING`, and use local `ROLE` output for data nodes. Component failures must still leave the shell exit code at zero so partial `degraded` state can be parsed; transport/SSH/context errors continue to come from `Remote.Run`.

`parseRedisCollectorCheckOutput` must reject a missing version marker, missing expected component markers, and values outside `running|failed`. Normalize Redis `slave` to `replica`; accept `master`, `replica`, and `unknown`.

`checkRedisCollector` must:

```go
result, err := s.remote.Run(ctx, req.Server, redisCollectorCheckCommand(req.Server, req.Instance, password))
if err != nil {
	return failCollectorCheck(req, err)
}
runtime, role, err := parseRedisCollectorCheckOutput(result.Stdout, expectsData, expectsSentinel)
runtime.applyDetails(details)
status := runtime.aggregateStatus()
```

Use `markRedisSentinelRuntimeStatus` for Sentinel topology and `markRedisInstanceStatus` otherwise. Preserve the previous role when the parser returns `unknown`. Return an error only for SSH/parsing failures or when every expected component failed; return `degraded` without an error when at least one local component is healthy.

- [ ] **Step 7: Run focused Redis tests and verify GREEN**

Run from `backend/`:

```powershell
go test ./internal/apps/redis -run 'TestParseRedisCollectorCheckOutput|TestModuleCollectorCheckUsesOneReadOnlyCommand|TestServiceChecksRedisSentinelAndUpdatesCurrentMasterRoles|TestServiceCheckUsesBoundRedisCredential' -count=1
```

Expected: PASS. The existing full Sentinel and bound-credential tests prove manual behavior remains intact.

- [ ] **Step 8: Run the Redis package test suite**

Run from `backend/`:

```powershell
go test ./internal/apps/redis -count=1
```

Expected: PASS.

- [ ] **Step 9: Commit the implementation only**

```powershell
git add backend/internal/apps/redis/collector_check.go backend/internal/apps/redis/service.go backend/internal/apps/redis/module.go backend/internal/apps/redis/service_test.go
git commit --only -m "fix(redis): use lightweight collector health check" -- backend/internal/apps/redis/collector_check.go backend/internal/apps/redis/service.go backend/internal/apps/redis/module.go backend/internal/apps/redis/service_test.go
```

### Task 2: Verify the backend and record the reusable conclusion

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: the completed collector-specific Redis check.
- Produces: validated backend behavior and a concise project-memory entry.

- [ ] **Step 1: Run collector and adapter regression tests**

Run from `backend/`:

```powershell
go test ./internal/collector ./internal/adapter -count=1
```

Expected: PASS, including the five-second timeout and context-aware SSH cancellation tests.

- [ ] **Step 2: Run the full backend test suite**

Run from `backend/` with the repository-local cache if needed:

```powershell
$env:GOCACHE='D:\workspace\aifar-deployment\.cache\go-build'
go test ./... -count=1
```

Expected: PASS.

- [ ] **Step 3: Build the backend**

Run from the repository root:

```powershell
pnpm backend:build
```

Expected: Linux and Windows backend binaries build successfully.

- [ ] **Step 4: Check formatting and patch hygiene**

```powershell
gofmt -w backend/internal/apps/redis/collector_check.go backend/internal/apps/redis/service.go backend/internal/apps/redis/module.go backend/internal/apps/redis/service_test.go
git diff --check
```

Expected: no output from `git diff --check`.

- [ ] **Step 5: Append the result to project memory**

Add one concise problem/conclusion pair under `## 2026-07-15` stating that Redis Collector checks now use one read-only SSH command, while manual checks retain full topology discovery, and list the successful verification commands.

- [ ] **Step 6: Commit the memory update only**

Because `memory.md` already contains project-required session notes, commit only this path without including unrelated staged changes:

```powershell
git add memory.md
git commit --only -m "docs: record Redis collector timeout fix" -- memory.md
```
