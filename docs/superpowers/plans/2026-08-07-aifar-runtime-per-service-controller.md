# AIFAR Runtime Per-Service Controller Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 AIFAR Runtime 从实例级全量编排改造成每服务独立接收、调和、失败和恢复的控制器，同时分离安装状态与容器运行状态。

**Architecture:** SQLite 的 `aifar_deployments` 是控制面每服务 desired state 权威来源；Agent 使用实例共享 `instance.json` 和每服务 Manifest 作为节点侧执行缓存，通过 generation/hash 幂等接收。不同服务使用独立锁和队列并行调和，Endpoint 变化异步驱动 Nacos 代理注册；批量操作只聚合多个服务意图，不形成跨服务事务。

**Tech Stack:** Go 1.24、Chi、SQLite (`modernc.org/sqlite`)、现有 worker/task/audit、SSH adapter、Docker CLI、Vue 3、TypeScript、Vitest、Element Plus。

## Global Constraints

- 设计依据：`docs/superpowers/specs/2026-08-07-aifar-runtime-per-service-controller-design.md`。
- 不引入 K3s、Kubernetes API Server、CNI、CSI、Docker Go client 或新的外部依赖。
- API 前缀保持 `/api/v2`；所有用户触发的写操作继续返回 task ID，并记录 task target/step 与审计。
- 不增加自由 Shell API；Agent HTTP 只监听 `127.0.0.1`，Server 仅通过受控 CLI/SSH adapter 调用。
- `app_instances.status` 只表达安装生命周期；容器失败只能改变目标 Deployment Condition。
- RuntimeSpec/API/UI 移除 Nacos 状态；Agent 代注册、注销和心跳继续后台运行，且失败不改变 Deployment Condition。
- 不同服务并行，同一 `(instanceId, serviceName)` 串行；Agent 升级、网络、公共 env、迁移和卸载使用实例级维护锁。
- 每个 Agent Manifest 写入必须使用同目录临时文件、`fsync` 和原子 `rename`；generation 相同但 hash 不同必须返回冲突。
- 机器码使用英文；新增后端和前端用户可见文案都提供 zh/en，日志与审计不得包含凭据、完整 env 或完整 Manifest。
- 修改 `web/src` 前必须先阅读 `design/ant-design-system-portable202606.md`。
- 所有生产代码遵循 TDD：先运行定向测试确认失败，再写最小实现，再运行定向与相邻测试。

## File and Interface Map

- `backend/internal/store/models.go`：扩展 Deployment generation、spec 和 Conditions 数据模型。
- `backend/internal/store/store.go`：集中前向迁移，不在 handler 建表。
- `backend/internal/store/aifar_orchestration.go`：Deployment CAS/observe 与分层锁、续租。
- `backend/internal/runtimeagent/manifest.go`：新每服务 Manifest、Condition、Acceptance 类型及规范化/校验。
- `backend/internal/runtimeagent/manifest_store.go`：`instance.json`、`deployments/*.json` 原子持久化和 legacy split。
- `backend/internal/runtimeagent/controller.go`：每服务队列、generation 取消、退避和 Condition。
- `backend/internal/runtimeagent/discovery.go`：Endpoint 事件与独立 Nacos 同步队列。
- `backend/internal/runtimeagent/ingress.go`：保留 Docker/代理底层能力，逐步移除实例级调和职责。
- `backend/cmd/aifar-agent/main.go`：新 Agent HTTP/CLI、feature discovery 和 legacy 只读兼容。
- `backend/internal/apps/aifar/deployment_control.go`：Server 侧单服务 mutation、Agent acceptance/readback 和控制面对账。
- `backend/internal/apps/aifar/runtime_manifest.go`：从 catalog、release、env 路径构造每服务 Manifest。
- `backend/internal/apps/aifar/runtime_migration.go`：`agent-runtime-v2` 到 `agent-service-controller-v1` 的无主动重启迁移。
- `backend/internal/apps/aifar/service.go`、`service_install.go`、`scale.go`、`runtime_config.go`、`runtime_restart.go`、`release.go`、`rollback.go`：调用统一 mutation service。
- `backend/internal/httpapi/aifar_runtime_controller.go`、`containers_aifar_runtime.go`：服务级 API、锁、task target 和 Runtime 响应。
- `web/src/containers/runtime/*`：删除 Nacos 状态，展示 generation/Condition，并改用服务级门禁。

---

### Task 1: Add Deployment generation and Condition persistence

**Files:**
- Modify: `backend/internal/store/models.go`
- Modify: `backend/internal/store/store.go`
- Modify: `backend/internal/store/aifar_orchestration.go`
- Create: `backend/internal/store/aifar_deployment_generation_test.go`

**Interfaces:**
- Produces: `SaveAIFARDeploymentGeneration(next AIFARDeployment, expectedGeneration int64) (AIFARDeployment, error)`。
- Produces: `ObserveAIFARDeployment(instanceID, serviceName string, generation int64, status, conditionsJSON string, at time.Time) (AIFARDeployment, error)`。
- Produces: sentinel errors `ErrAIFARDeploymentGenerationConflict` and `ErrAIFARDeploymentNotFound`。

- [ ] **Step 1: Write failing migration and CAS tests**

```go
func TestSaveAIFARDeploymentGenerationRejectsStaleWriter(t *testing.T) {
    db := openTestStore(t)
    first, err := db.SaveAIFARDeploymentGeneration(store.AIFARDeployment{
        InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 1,
        CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":1}}`,
    }, 0)
    if err != nil { t.Fatal(err) }
    if first.Generation != 1 { t.Fatalf("generation=%d, want 1", first.Generation) }

    _, err = db.SaveAIFARDeploymentGeneration(store.AIFARDeployment{
        InstanceID: "instance-1", ServiceName: "permission", DesiredReplicas: 0,
        CurrentRevision: "r1", SpecJSON: `{"metadata":{"generation":2}}`,
    }, 0)
    if !errors.Is(err, store.ErrAIFARDeploymentGenerationConflict) {
        t.Fatalf("error=%v, want generation conflict", err)
    }
}

func TestObserveAIFARDeploymentCannotAdvancePastDesiredGeneration(t *testing.T) {
    // Insert generation 2, then assert observing generation 3 returns
    // ErrAIFARDeploymentGenerationConflict and leaves observed_generation unchanged.
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `cd backend; go test ./internal/store -run 'TestSaveAIFARDeploymentGeneration|TestObserveAIFARDeployment' -count=1`

Expected: FAIL because the new fields and Store methods do not exist.

- [ ] **Step 3: Add schema fields and model fields**

Add forward migrations for:

```sql
alter table aifar_deployments add column spec_json text;
alter table aifar_deployments add column generation integer not null default 1;
alter table aifar_deployments add column observed_generation integer not null default 0;
alter table aifar_deployments add column conditions_json text;
alter table aifar_deployments add column last_transition_at datetime;
```

Extend `AIFARDeployment` with `SpecJSON string`, `Generation int64`, `ObservedGeneration int64`, `ConditionsJSON string`, and `LastTransitionAt time.Time`. Update every insert/select/scan in `aifar_orchestration.go`.

- [ ] **Step 4: Implement transactional CAS and observation**

Use `insert ... on conflict(instance_id,service_name) do update ... where aifar_deployments.generation=?`; require one affected row. `ObserveAIFARDeployment` must update only when `generation <= desired generation` and never reduce `observed_generation`.

- [ ] **Step 5: Run Store tests**

Run: `cd backend; go test ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/store/models.go backend/internal/store/store.go backend/internal/store/aifar_orchestration.go backend/internal/store/aifar_deployment_generation_test.go
git commit -m "feat: persist per-service deployment generations"
```

### Task 2: Make orchestration locks hierarchical and renewable

**Files:**
- Modify: `backend/internal/store/aifar_orchestration.go`
- Create: `backend/internal/store/aifar_orchestration_lock_test.go`
- Modify: `backend/internal/apps/aifar/service.go`

**Interfaces:**
- Consumes: existing `AIFAROrchestrationLock` with empty `ServiceName` representing instance maintenance.
- Produces: `RenewAIFAROrchestrationLock(id string, expiresAt time.Time) (bool, error)`.
- Produces conflict matrix: maintenance conflicts with every active lock in the instance; service lock conflicts only with same service or maintenance.

- [ ] **Step 1: Write failing lock-matrix tests**

```go
func TestAIFAROrchestrationLocksAllowDifferentServices(t *testing.T) {
    db := openTestStore(t)
    _, err := db.AcquireAIFAROrchestrationLock(lock("i1", "permission", "scale"))
    if err != nil { t.Fatal(err) }
    _, err = db.AcquireAIFAROrchestrationLock(lock("i1", "file", "offline"))
    if err != nil { t.Fatal(err) }
}

func TestAIFARMaintenanceLockConflictsWithAnyService(t *testing.T) {
    db := openTestStore(t)
    _, err := db.AcquireAIFAROrchestrationLock(lock("i1", "permission", "scale"))
    if err != nil { t.Fatal(err) }
    _, err = db.AcquireAIFAROrchestrationLock(lock("i1", "", "migrate"))
    var conflict store.AIFAROrchestrationLockConflict
    if !errors.As(err, &conflict) { t.Fatalf("error=%v, want lock conflict", err) }
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/store -run 'TestAIFAROrchestrationLocks' -count=1`

Expected: the different-service test fails because current conflict lookup ignores `service_name`.

- [ ] **Step 3: Implement the conflict query and renewal**

For a maintenance request query any active lock in the instance. For a service request query `(service_name='' OR service_name=?)`. Add renewal guarded by `id` and `status='active'`; do not revive expired/released locks.

- [ ] **Step 4: Extend the AIFAR Store interface and task heartbeat helper**

Add `RenewAIFAROrchestrationLock` to `aifarOrchestrationLockStore`. The worker-owned helper renews at one-third TTL and stops before release; renewal failure cancels only the owning task.

- [ ] **Step 5: Run Store and AIFAR package tests**

Run: `cd backend; go test ./internal/store ./internal/apps/aifar -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/store/aifar_orchestration.go backend/internal/store/aifar_orchestration_lock_test.go backend/internal/apps/aifar/service.go
git commit -m "feat: isolate AIFAR service operation locks"
```

### Task 3: Define and atomically persist per-service Agent manifests

**Files:**
- Create: `backend/internal/runtimeagent/manifest.go`
- Create: `backend/internal/runtimeagent/manifest_store.go`
- Create: `backend/internal/runtimeagent/manifest_test.go`
- Modify: `backend/internal/runtimeagent/spec.go`

**Interfaces:**
- Produces: `InstanceConfig`, `DeploymentManifest`, `DeploymentMetadata`, `DeploymentCondition`, `DeploymentAcceptance`, and `DeploymentState`.
- Produces: `ManifestStore.Put(DeploymentManifest) (DeploymentAcceptance, error)`, `Get(instanceID, serviceName string)`, `List(instanceID string)`, and `PutInstance(InstanceConfig)`.
- Keeps `LegacyRuntimeSpec` solely for dual-read/bootstrap; new Manifest types contain no Nacos status.

- [ ] **Step 1: Write failing normalization, stale and atomicity tests**

```go
func TestManifestStoreRejectsSameGenerationDifferentHash(t *testing.T) {
    s := ManifestStore{StateDir: t.TempDir()}
    first := testManifest("permission", 7, 1)
    _, err := s.Put(first)
    if err != nil { t.Fatal(err) }
    changed := first
    changed.Spec.Replicas = 0
    _, err = s.Put(changed)
    if !errors.Is(err, ErrDeploymentGenerationConflict) {
        t.Fatalf("error=%v, want generation conflict", err)
    }
}

func TestManifestStoreFailureLeavesPreviousManifestReadable(t *testing.T) {
    // Inject a rename failure, assert generation 1 remains readable and no
    // partially written deployment file is accepted.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/runtimeagent -run 'TestManifest' -count=1`

Expected: FAIL because the Manifest store does not exist.

- [ ] **Step 3: Implement exact resource types and validation**

```go
type DeploymentManifest struct {
    APIVersion string             `json:"apiVersion"`
    Kind       string             `json:"kind"`
    Metadata   DeploymentMetadata `json:"metadata"`
    Spec       DeploymentSpec     `json:"spec"`
    Service    ServiceSpec        `json:"service"`
}

type DeploymentMetadata struct {
    InstanceID string `json:"instanceId"`
    Name       string `json:"name"`
    Generation int64  `json:"generation"`
}

type DeploymentAcceptance struct {
    Accepted   bool   `json:"accepted"`
    Generation int64  `json:"generation"`
    SpecHash   string `json:"specHash"`
}

type InstanceConfig struct {
    APIVersion  string      `json:"apiVersion"`
    InstanceID  string      `json:"instanceId"`
    InstallRoot string      `json:"installRoot"`
    Network     string      `json:"network"`
    Ingress     IngressSpec `json:"ingress"`
}

type DeploymentCondition struct {
    Type               string    `json:"type"`
    Status             bool      `json:"status"`
    Reason             string    `json:"reason"`
    Message            string    `json:"message,omitempty"`
    Generation         int64     `json:"generation"`
    LastTransitionTime time.Time `json:"lastTransitionTime"`
}

type DeploymentState struct {
    InstanceID         string                `json:"instanceId"`
    ServiceName        string                `json:"serviceName"`
    Generation         int64                 `json:"generation"`
    ObservedGeneration int64                 `json:"observedGeneration"`
    SpecHash           string                `json:"specHash"`
    DesiredReplicas    int                   `json:"desiredReplicas"`
    CurrentReplicas    int                   `json:"currentReplicas"`
    ReadyReplicas      int                   `json:"readyReplicas"`
    Conditions         []DeploymentCondition `json:"conditions"`
}
```

Extend `DeploymentSpec` with an `int64` field named `RestartGeneration` using JSON name `restartGeneration,omitempty`. Validate service/instance names, positive generation, allowed paths under install root, ports, volume sources, and schema constants `aifar.io/v1alpha1`/`Deployment`.

- [ ] **Step 4: Implement atomic persistence**

Write `instance.json` and `deployments/<service>.json` with mode `0600`, sync the temporary file, rename, then sync the containing directory. Return idempotent success for same generation/hash, `ErrStaleDeploymentGeneration` for lower generation, and conflict for equal generation/different hash.

- [ ] **Step 5: Run runtimeagent tests**

Run: `cd backend; go test ./internal/runtimeagent -count=1`

Expected: PASS, including existing legacy spec tests.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/runtimeagent/manifest.go backend/internal/runtimeagent/manifest_store.go backend/internal/runtimeagent/manifest_test.go backend/internal/runtimeagent/spec.go
git commit -m "feat: add per-service agent manifests"
```

### Task 4: Add independent service queues, Conditions and retry

**Files:**
- Create: `backend/internal/runtimeagent/controller.go`
- Create: `backend/internal/runtimeagent/controller_test.go`
- Modify: `backend/internal/runtimeagent/ingress.go`
- Modify: `backend/internal/runtimeagent/ingress_test.go`

**Interfaces:**
- Consumes: `ManifestStore` and existing Docker runner/container helpers.
- Produces: `Manager.AcceptDeployment(ctx, manifest) (DeploymentAcceptance, error)`.
- Produces: `Manager.ReconcileDeployment(instanceID, serviceName string)` and `Manager.DeploymentState(instanceID, serviceName string) (DeploymentState, bool)`.

- [ ] **Step 1: Write failing concurrency and supersession tests**

```go
func TestControllersDoNotBlockDifferentServices(t *testing.T) {
    runner := newBlockingRunner("permission")
    manager := newTestManager(t, runner)
    _, _ = manager.AcceptDeployment(context.Background(), testManifest("permission", 1, 1))
    _, _ = manager.AcceptDeployment(context.Background(), testManifest("file", 1, 1))
    deadline := time.Now().Add(time.Second)
    for !manager.available("file") && time.Now().Before(deadline) { time.Sleep(10 * time.Millisecond) }
    if !manager.available("file") { t.Fatal("file did not become available") }
    if manager.available("permission") { t.Fatal("blocked permission unexpectedly became available") }
}

func TestNewGenerationSupersedesOnlySameService(t *testing.T) {
    // generation 2 cancels permission generation 1; file generation 1 remains running.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/runtimeagent -run 'TestControllers|TestNewGeneration' -count=1`

Expected: FAIL because reconciliation still uses an instance-level mutex/batch.

- [ ] **Step 3: Implement queue ownership and instance maintenance RW lock**

Maintain one controller entry per `(instanceID, serviceName)` with a single wake channel and cancel function. `AcceptDeployment` persists first, updates the cache, cancels only the older same-service context, sends a non-blocking wake, and immediately returns acceptance.

- [ ] **Step 4: Implement Condition transitions and backoff**

Emit `Accepted`, `Progressing`, `Available`, `Degraded`, or `Offline` with reasons `ImageMissing`, `ContainerCreateFailed`, `ContainerStartFailed`, `ReadinessFailed`, `CrashLoopBackOff`, `NodeResourcePressure`, `SpecRejected`, `AgentUnavailable`. Retry container failures at `1s,2s,4s,8s,16s,30s,60s`; reset after stable Available; do not retry `SpecRejected` until a new generation.

- [ ] **Step 5: Refactor existing Docker reconciliation to one manifest**

Reuse `ensureDeployment`, pod naming, readiness and endpoint discovery, but pass `InstanceConfig + DeploymentManifest`; remove use of the global `reconcileMu` from ordinary service work. Keep an instance write lock for remove/network/shared maintenance.

- [ ] **Step 6: Run runtimeagent tests with race detection**

Run: `cd backend; go test -race ./internal/runtimeagent -count=1`

Expected: PASS with no data races.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/runtimeagent/controller.go backend/internal/runtimeagent/controller_test.go backend/internal/runtimeagent/ingress.go backend/internal/runtimeagent/ingress_test.go
git commit -m "feat: reconcile AIFAR services independently"
```

### Task 5: Drive Endpoint and Nacos discovery independently

**Files:**
- Create: `backend/internal/runtimeagent/discovery.go`
- Create: `backend/internal/runtimeagent/discovery_test.go`
- Modify: `backend/internal/runtimeagent/nacos.go`
- Modify: `backend/internal/runtimeagent/nacos_test.go`
- Modify: `backend/internal/runtimeagent/controller.go`

**Interfaces:**
- Produces: `DiscoveryController.EndpointChanged(EndpointEvent)`.
- Produces: `EndpointEvent{InstanceID, ServiceName, AppName string; ListenPort int; Ready []ReadyEndpoint}`.
- Nacos connection values are read from the instance env files/defaults, not from Deployment Manifest status.

Use these exact public event types:

```go
type ReadyEndpoint struct {
    PodID         string `json:"podId"`
    ContainerName string `json:"containerName"`
    Revision      string `json:"revision"`
    Port          int    `json:"port"`
}

type EndpointEvent struct {
    InstanceID  string
    ServiceName string
    AppName     string
    ListenPort  int
    Ready       []ReadyEndpoint
}
```

- [ ] **Step 1: Write failing independent-discovery tests**

```go
func TestReadyServiceQueuesRegistrationWithoutWaitingForPeers(t *testing.T) {
    syncer := &fakeDiscoverySyncer{failFor: map[string]error{"file": errors.New("nacos down")}}
    d := newTestDiscoveryController(syncer)
    d.EndpointChanged(readyEvent("permission"))
    d.EndpointChanged(readyEvent("file"))
    deadline := time.Now().Add(time.Second)
    for !syncer.registered("permission") && time.Now().Before(deadline) { time.Sleep(10 * time.Millisecond) }
    if !syncer.registered("permission") { t.Fatal("permission registration was not queued independently") }
}

func TestNacosFailureDoesNotDegradeDeployment(t *testing.T) {
    // Fail discovery sync and assert DeploymentState remains Available.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/runtimeagent -run 'TestReadyService|TestNacosFailure' -count=1`

Expected: FAIL because Nacos sync is currently called after instance reconciliation.

- [ ] **Step 3: Implement per-service discovery workers**

Deduplicate by `(instance,service,endpoint hash)`. Ready `0 -> >0` queues register, ready set change refreshes proxy routes, and `>0 -> 0` queues deregister. Discovery retries use their own backoff and log only service/reason, never secrets.

- [ ] **Step 4: Remove Nacos result mutation from Runtime status**

Delete calls to `MarkNacosProxyStatus`; retain heartbeat and startup replay by enumerating `instance.json` plus deployment manifests. A Nacos error updates only discovery worker diagnostics/logs.

- [ ] **Step 5: Run runtimeagent tests**

Run: `cd backend; go test -race ./internal/runtimeagent -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/runtimeagent/discovery.go backend/internal/runtimeagent/discovery_test.go backend/internal/runtimeagent/nacos.go backend/internal/runtimeagent/nacos_test.go backend/internal/runtimeagent/controller.go
git commit -m "feat: decouple runtime discovery from service reconcile"
```

### Task 6: Expose Agent manifest HTTP/CLI and compatibility features

**Files:**
- Modify: `backend/cmd/aifar-agent/main.go`
- Modify: `backend/cmd/aifar-agent/main_test.go`
- Create: `backend/internal/runtimeagent/legacy.go`
- Create: `backend/internal/runtimeagent/legacy_test.go`

**Interfaces:**
- `PUT /runtime/instances/{instance}/deployments/{service}` returns `202 DeploymentAcceptance`.
- `GET /runtime/instances/{instance}/deployments/{service}` returns `200 DeploymentState`.
- `POST /runtime/instances/{instance}/deployments/{service}/reconcile` queues an immediate retry.
- CLI: `apply-deployment --manifest`, `get-deployment --instance --service`, `reconcile-deployment --instance --service`, and one-time `bootstrap-runtime --spec`.
- `/status.features` includes `service-manifest-v1`, `service-generation-v1`, `per-service-reconcile`, `per-service-restart`, and `service-conditions-v1`.

- [ ] **Step 1: Write failing handler and CLI transport tests**

```go
func TestPutDeploymentReturnsAcceptedBeforeReadiness(t *testing.T) {
    manager := newBlockedManager(t)
    handler := newAgentHandler(manager, healthy)
    req := httptest.NewRequest(http.MethodPut, "/runtime/instances/admin/deployments/permission", manifestBody(1))
    rec := httptest.NewRecorder()
    handler.ServeHTTP(rec, req)
    if rec.Code != http.StatusAccepted { t.Fatalf("status=%d, want 202", rec.Code) }
    if !strings.Contains(rec.Body.String(), `"accepted":true`) { t.Fatalf("body=%s", rec.Body.String()) }
}
```

Also test URL/body identity mismatch as `400`, stale generation as `409 STALE_DEPLOYMENT_GENERATION`, same generation/different hash as `409 DEPLOYMENT_GENERATION_CONFLICT`, and `/runtime/reconcile` rejecting writes after the model marker is switched.

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./cmd/aifar-agent ./internal/runtimeagent -run 'TestPutDeployment|TestBootstrapRuntime|TestLegacy' -count=1`

Expected: FAIL because routes and compatibility split are absent.

- [ ] **Step 3: Add typed error responses and bounded request bodies**

Decode through `http.MaxBytesReader`, respond with `{code,message,details}`, and never echo Manifest/env content. `bootstrap-runtime` may split a legacy spec only when the instance has no new-model marker; after switch, return `LEGACY_RUNTIME_SPEC_DISABLED`.

- [ ] **Step 4: Implement CLI commands through localhost HTTP**

CLI only reads a specified JSON file and sends the typed request. It must not accept command fragments or arbitrary paths from HTTP callers.

- [ ] **Step 5: Run Agent tests and build**

Run: `cd backend; go test ./cmd/aifar-agent ./internal/runtimeagent -count=1`

Run: `pnpm backend:build`

Expected: both PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/cmd/aifar-agent/main.go backend/cmd/aifar-agent/main_test.go backend/internal/runtimeagent/legacy.go backend/internal/runtimeagent/legacy_test.go
git commit -m "feat: expose per-service agent control API"
```

### Task 7: Add Server-side deployment mutation and acceptance repair

**Files:**
- Create: `backend/internal/apps/aifar/deployment_control.go`
- Create: `backend/internal/apps/aifar/deployment_control_test.go`
- Create: `backend/internal/apps/aifar/runtime_manifest.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/service_catalog.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- Consumes Store CAS methods from Task 1 and Agent CLI from Task 6.
- Produces: `MutateDeployment(ctx context.Context, req DeploymentMutationRequest, log Logger) (store.AIFARDeployment, error)`.
- Produces: `AcceptDeployment(ctx context.Context, server store.Server, manifest runtimeagent.DeploymentManifest) (runtimeagent.DeploymentAcceptance, error)`.
- Produces: stable repair code `AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED`.

Use this request contract so all operation paths share one mutation boundary:

```go
type DeploymentMutationRequest struct {
    Instance           store.AppInstance
    Server             store.Server
    ServiceName        string
    ExpectedGeneration int64
    Actor               string
    TaskID              string
    Operation           string
    Mutate              func(*runtimeagent.DeploymentManifest) error
}
```

- [ ] **Step 1: Write failing acceptance/readback tests**

```go
func TestMutateDeploymentAcceptsManifestBeforeReturning(t *testing.T) {
    agent := &fakeDeploymentAgent{accept: acceptance(2)}
    svc := newDeploymentControlTestService(t, agent)
    got, err := svc.MutateDeployment(context.Background(), mutation("permission", 1, func(m *DeploymentManifest) {
        m.Spec.Replicas = 0
    }), discardLog)
    if err != nil { t.Fatal(err) }
    if got.Generation != 2 { t.Fatalf("generation=%d, want 2", got.Generation) }
    if got.ObservedGeneration != 0 { t.Fatalf("observed=%d, want 0", got.ObservedGeneration) }
}

func TestLostAcceptanceResponseUsesGenerationHashReadback(t *testing.T) {
    // Accept returns a transport error, Get returns matching generation/hash;
    // mutation succeeds without resubmitting or restarting the container.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/apps/aifar -run 'TestMutateDeployment|TestLostAcceptance' -count=1`

Expected: FAIL because the service does not exist.

- [ ] **Step 3: Implement Manifest construction**

Build each manifest from `serviceDefinition`, current release/revision, desired replicas, allowed env files, volumes, resource values and health check. Do not read or write aggregated `metadata.desiredReplicas`. Use `restartGeneration` only for restart intent.

- [ ] **Step 4: Implement the mutation transaction boundary**

Read current deployment, build generation `N+1`, save `pending_acceptance` by CAS, upload a temporary manifest through the existing `Remote`, invoke `aifar-agent apply-deployment`, parse acceptance, then mark `Accepted`. If the response is lost, call `get-deployment` and compare generation/hash. Never compensate by restoring an old Manifest after Agent acceptance.

- [ ] **Step 5: Add i18n messages and sanitization tests**

Add zh/en messages for stale/conflict, Agent unavailable, acceptance/readback mismatch, and control-plane repair. Tests must assert errors and task logs omit env values and full Manifest JSON.

- [ ] **Step 6: Run AIFAR package tests**

Run: `cd backend; go test ./internal/apps/aifar ./internal/i18n -count=1`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/apps/aifar/deployment_control.go backend/internal/apps/aifar/deployment_control_test.go backend/internal/apps/aifar/runtime_manifest.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/service_catalog.go backend/internal/i18n/messages.go
git commit -m "feat: accept AIFAR deployment mutations per service"
```

### Task 8: Separate installation success from container readiness

**Files:**
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/service_test.go`
- Modify: `backend/internal/apps/aifar/templates/install.sh`
- Modify: `backend/internal/apps/aifar/templates/service-install.sh`
- Modify: `backend/internal/apps/aifar/release.go`

**Interfaces:**
- Consumes: Agent `bootstrap-runtime` for first install and per-service mutation for adding services.
- Produces: install success after every selected Manifest is atomically accepted, without waiting for Docker readiness.

- [ ] **Step 1: Write failing installation-boundary tests**

```go
func TestInstallSucceedsAfterManifestAcceptanceWhenContainerIsUnready(t *testing.T) {
    remote := newInstallRemote()
    remote.agentAcceptAll = true
    remote.containerReady = false
    instance, err := runInstall(t, remote)
    if err != nil { t.Fatal(err) }
    if instance.Status != "installed" { t.Fatalf("status=%s, want installed", instance.Status) }
    deployments := loadDeployments(t, instance.ID)
    if deployments[0].ObservedGeneration != 0 { t.Fatalf("observed=%d, want 0", deployments[0].ObservedGeneration) }
}

func TestInstallFailsWhenAgentCannotPersistOneManifest(t *testing.T) {
    // Assert install_failed, accepted peers remain persisted, and retry only
    // resubmits the unaccepted service with the same idempotency key.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/apps/aifar -run 'TestInstallSucceedsAfterManifest|TestInstallFailsWhenAgent' -count=1`

Expected: the unready-container test fails because `install.sh` currently waits for all pods/ports.

- [ ] **Step 3: Change scripts to stop at acceptance**

Keep package verification, extraction, image build, env generation and Agent capability checks. Replace `reconcile_runtime; wait_runtime_pods; wait_runtime_ports` with `bootstrap-runtime`/`apply-deployment` acceptance output. Do not pre-populate ready pods/endpoints from desired replicas.

- [ ] **Step 4: Persist truthful initial control-plane state**

Save Deployment generation/spec with `Accepted`; save ReplicaSet desired count but `ready_pods=0`; create no Pod or Endpoint until observed by status collection. Mark the instance `installed` only after all selected manifests are accepted.

- [ ] **Step 5: Run install tests and script tests**

Run: `cd backend; go test ./internal/apps/aifar -count=1`

Run: `pnpm test:scripts`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/apps/aifar/service.go backend/internal/apps/aifar/service_test.go backend/internal/apps/aifar/templates/install.sh backend/internal/apps/aifar/templates/service-install.sh backend/internal/apps/aifar/release.go
git commit -m "feat: complete AIFAR install at manifest acceptance"
```

### Task 9: Route scale, update, config, reconcile and restart through one mutation service

**Files:**
- Modify: `backend/internal/apps/aifar/scale.go`
- Modify: `backend/internal/apps/aifar/runtime.go`
- Modify: `backend/internal/apps/aifar/runtime_restart.go`
- Modify: `backend/internal/apps/aifar/runtime_config.go`
- Modify: `backend/internal/apps/aifar/release.go`
- Modify: `backend/internal/apps/aifar/rollback.go`
- Modify: `backend/internal/apps/aifar/artifact_bundle.go`
- Modify: `backend/internal/apps/aifar/service_test.go`
- Modify: `backend/internal/apps/aifar/templates/scale-service.sh`
- Modify: `backend/internal/apps/aifar/templates/runtime-restart.sh`
- Modify: `backend/internal/apps/aifar/templates/runtime-reconcile.sh`
- Modify: `backend/internal/apps/aifar/templates/runtime-config.sh`
- Modify: `backend/internal/apps/aifar/templates/update-artifact.sh`
- Modify: `backend/internal/apps/aifar/templates/update-artifact-bundle.sh`
- Modify: `backend/internal/apps/aifar/templates/rollback-artifact.sh`

**Interfaces:**
- Consumes: `MutateDeployment` from Task 7.
- A single-service action modifies only that service generation/spec.
- `restart-all` enumerates online deployments and increments each `restartGeneration`; it never invokes stop-all.

- [ ] **Step 1: Write failing isolation tests**

```go
func TestScalePermissionDoesNotRewriteFileDeployment(t *testing.T) {
    before := seedTwoDeployments(t)
    if err := service.ScaleService(ctx, scale("permission", 2), log, nil); err != nil { t.Fatal(err) }
    after := listDeployments(t)
    if before["file"].Generation != after["file"].Generation { t.Fatal("file generation changed") }
    if before["file"].SpecJSON != after["file"].SpecJSON { t.Fatal("file spec changed") }
}

func TestRestartAllFansOutWithoutStopAll(t *testing.T) {
    // Assert one accepted mutation per online service, no mutation for replicas=0,
    // and no remote command contains "restart-runtime" or removes every pod.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/apps/aifar -run 'TestScalePermission|TestRestartAllFansOut' -count=1`

Expected: FAIL because current operations rewrite a full runtime spec and restart-all removes all pods.

- [ ] **Step 3: Convert single-service operations**

Scale changes only `spec.replicas`; artifact update/rollback changes only target revision/image and release metadata; service config changes only the affected services; manual reconcile queues the selected service without increasing generation.

- [ ] **Step 4: Convert batch operations to fan-out aggregation**

Create one task target per `instanceId:serviceName`. Submit with bounded concurrency. Parent success means every target was accepted; if one target fails acceptance, mark the parent failed with per-target detail while already accepted targets continue independently.

- [ ] **Step 5: Delete stop-all behavior and aggregated spec promotion from scripts**

Scripts may prepare artifacts/env files but must call typed per-service Agent commands. Remove rollback that overwrites `runtime-spec.json`; rollback creates a newer target-service generation instead.

- [ ] **Step 6: Run AIFAR and script tests**

Run: `cd backend; go test ./internal/apps/aifar -count=1`

Run: `pnpm test:scripts`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add backend/internal/apps/aifar/scale.go backend/internal/apps/aifar/runtime.go backend/internal/apps/aifar/runtime_restart.go backend/internal/apps/aifar/runtime_config.go backend/internal/apps/aifar/release.go backend/internal/apps/aifar/rollback.go backend/internal/apps/aifar/artifact_bundle.go backend/internal/apps/aifar/service_test.go backend/internal/apps/aifar/templates
git commit -m "feat: isolate AIFAR runtime service mutations"
```

### Task 10: Add service-level HTTP tasks and remove Runtime Nacos status

**Files:**
- Modify: `backend/internal/apps/registry/contract.go`
- Modify: `backend/internal/apps/aifar/module.go`
- Modify: `backend/internal/httpapi/aifar_runtime_controller.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime.go`
- Modify: `backend/internal/httpapi/containers_aifar_runtime_test.go`
- Modify: `backend/internal/i18n/messages.go`

**Interfaces:**
- `PUT /api/v2/apps/instances/{id}/runtime/deployments/{service}` creates a worker task for typed mutation.
- `POST /api/v2/apps/instances/{id}/runtime/deployments/{service}/reconcile` creates an immediate per-service reconcile task.
- Existing scale/offline routes remain compatibility aliases to the same module service.
- Runtime response deployment fields include `generation`, `observedGeneration`, `conditions`, and `lastTransitionAt`; no Nacos status fields.

Add the registry request without exposing free-form commands:

```go
type RuntimeDeploymentMutationRequest struct {
    InstanceID         string
    ServiceName        string
    ExpectedGeneration int64
    Operation          string
    Replicas           *int
    Restart            bool
    Reason             string
}
```

`Operation` is validated against the fixed set `apply`, `scale`, `offline`, `restart`, and `reconcile`; handlers never pass it to a shell.

- [ ] **Step 1: Write failing API contract tests**

```go
func TestRuntimeResponseOmitsNacosStatusAndIncludesConditions(t *testing.T) {
    body := getRuntime(t, seededRuntime())
    if strings.Contains(body, "nacosRegistered") || strings.Contains(body, "lastNacosError") {
        t.Fatalf("response still exposes Nacos runtime status: %s", body)
    }
    if !strings.Contains(body, `"generation":2`) || !strings.Contains(body, `"observedGeneration":1`) {
        t.Fatalf("response is missing generation fields: %s", body)
    }
}

func TestDifferentServiceTasksAcquireDifferentLocks(t *testing.T) {
    // Start blocked permission mutation, then assert file mutation returns 202;
    // a second permission mutation returns 409 with owner task ID.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/httpapi -run 'TestRuntimeResponseOmitsNacos|TestDifferentServiceTasks' -count=1`

Expected: FAIL because Runtime still exposes Nacos fields and HTTP locks are instance scoped.

- [ ] **Step 3: Add typed registry contracts and routes**

Handlers only parse/validate, acquire service or maintenance lock before task start, create task plan/targets, audit, and invoke module methods. Return existing `{code,message,details}` errors with `409` including active owner task ID.

- [ ] **Step 4: Make status reconciliation service-local**

Docker observations update only the matching deployment/replicaset/pods/endpoints and `observed_generation`. A failed or missing service does not rewrite peer rows or `app_instances.status`. Remove Nacos status fields from response assembly.

- [ ] **Step 5: Run HTTP, registry and AIFAR tests**

Run: `cd backend; go test ./internal/httpapi ./internal/apps/registry ./internal/apps/aifar -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/apps/registry/contract.go backend/internal/apps/aifar/module.go backend/internal/httpapi/aifar_runtime_controller.go backend/internal/httpapi/containers_aifar_runtime.go backend/internal/httpapi/containers_aifar_runtime_test.go backend/internal/i18n/messages.go
git commit -m "feat: expose per-service runtime control tasks"
```

### Task 11: Migrate legacy instances without intentional container restart

**Files:**
- Create: `backend/internal/apps/aifar/runtime_migration.go`
- Create: `backend/internal/apps/aifar/runtime_migration_test.go`
- Modify: `backend/internal/apps/aifar/release.go`
- Modify: `backend/internal/apps/aifar/service.go`
- Modify: `backend/internal/apps/aifar/templates/install.sh`

**Interfaces:**
- Produces: `MigrateRuntimeModel(ctx context.Context, req RuntimeMigrationRequest, log Logger) error`.
- New marker: `orchestrationModel=agent-service-controller-v1`.
- Consumes Agent feature gate and legacy split/readback from Task 6.

```go
type RuntimeMigrationRequest struct {
    Instance store.AppInstance
    Server   store.Server
    Actor    string
    TaskID   string
    Reason   string
}
```

- [ ] **Step 1: Write failing migration and recovery tests**

```go
func TestRuntimeMigrationAdoptsExistingContainersWithoutRestart(t *testing.T) {
    remote := legacyRuntimeRemoteWithRunningPods()
    err := service.MigrateRuntimeModel(ctx, migrationRequest(), log)
    if err != nil { t.Fatal(err) }
    commands := remote.commands()
    if strings.Contains(commands, "docker restart") || strings.Contains(commands, "docker rm") {
        t.Fatalf("migration restarted or removed containers: %s", commands)
    }
    if got := loadModel(t); got != "agent-service-controller-v1" { t.Fatalf("model=%s", got) }
}

func TestMigrationResumesAfterAgentSwitchBeforeServerCommit(t *testing.T) {
    // First run fails after Agent switch; second run reads generation/hash and
    // completes SQLite/model metadata idempotently.
}
```

- [ ] **Step 2: Run tests and verify RED**

Run: `cd backend; go test ./internal/apps/aifar -run 'TestRuntimeMigration' -count=1`

Expected: FAIL because migration support does not exist.

- [ ] **Step 3: Implement fail-closed preflight and staged conversion**

Under an instance maintenance lock, verify all Agent features, read legacy spec/SQLite/app metadata/Agent status, and compare service, replicas, revision, image, port and env paths. Any unexplained divergence stops before switching.

- [ ] **Step 4: Implement atomic Agent switch and Server repair**

Stage all new files, validate all hashes, atomically set the new model marker, adopt existing labeled containers, then read back every generation/hash into SQLite. Archive legacy spec read-only. After any new generation is accepted, never downgrade automatically.

- [ ] **Step 5: Run migration and AIFAR tests**

Run: `cd backend; go test ./internal/apps/aifar -count=1`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/apps/aifar/runtime_migration.go backend/internal/apps/aifar/runtime_migration_test.go backend/internal/apps/aifar/release.go backend/internal/apps/aifar/service.go backend/internal/apps/aifar/templates/install.sh
git commit -m "feat: migrate AIFAR runtime to service controllers"
```

### Task 12: Update Runtime UI for service Conditions and service-level gates

**Files:**
- Read first: `design/ant-design-system-portable202606.md`
- Modify: `web/src/containers/runtime/types.ts`
- Modify: `web/src/containers/runtime/api.ts`
- Modify: `web/src/containers/runtime/context.ts`
- Modify: `web/src/containers/runtime/useAifarRuntimeProvider.ts`
- Modify: `web/src/containers/runtime/selectors.ts`
- Modify: `web/src/containers/runtime/format.ts`
- Modify: `web/src/containers/runtime/AifarRuntimeSummary.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeDeploymentsTab.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeServicesTab.vue`
- Modify: `web/src/containers/runtime/AifarRuntimeDialogs.vue`
- Modify: `web/src/containers/runtime/runtime.css`
- Modify: `web/src/i18n/messages.ts`
- Modify: matching `*.test.ts` files under `web/src/containers/runtime/`

**Interfaces:**
- Consumes Runtime API fields from Task 10.
- Produces `AifarDeploymentCondition` and service action gate helpers.
- Removes `nacosRegistered`, `nacosReady`, `lastNacosHeartbeatAt`, `lastNacosError`, and `runtimeNacosStatus` from Runtime UI types/context.

```ts
export type AifarDeploymentCondition = {
  type: 'Accepted' | 'Progressing' | 'Available' | 'Degraded' | 'Offline'
  status: boolean
  reason: string
  message?: string
  generation: number
  lastTransitionTime: string
}
```

- [ ] **Step 1: Read the required design system**

Run: `Get-Content -LiteralPath design/ant-design-system-portable202606.md -Raw -Encoding utf8`

Expected: complete document reviewed before editing `web/src`.

- [ ] **Step 2: Write failing type/selector/component tests**

```ts
it('does not disable a healthy service when another service is degraded', () => {
  const permission = deployment({ serviceName: 'permission', phase: 'Available' })
  const file = deployment({ serviceName: 'file', phase: 'Degraded' })
  expect(runtimeServiceActionGate(permission, [permission, file], [])).toEqual({ disabled: false, reason: '' })
})

it('renders generation and condition reason without Nacos status', () => {
  const wrapper = mountRuntimeDeployments({ generation: 7, observedGeneration: 6, reason: 'ReadinessFailed' })
  expect(wrapper.text()).toContain('7 / 6')
  expect(wrapper.text()).toContain('ReadinessFailed')
  expect(wrapper.text()).not.toContain('Nacos')
})
```

- [ ] **Step 3: Run focused web tests and verify RED**

Run: `pnpm --dir web test -- runtimeRules.test.ts AifarRuntimeDeploymentsTab.test.ts AifarRuntimeSummary.test.ts`

Expected: FAIL because current types and gates still use aggregate degraded/Nacos state.

- [ ] **Step 4: Update types, API and action gates**

Add condition fields and service-specific action requests. Disable only for the same-service active task, instance maintenance, Agent unavailable/capability failure, or missing permission. Keep aggregate summary counts as display-only.

- [ ] **Step 5: Update components and bilingual copy**

Display Available/Progressing/Degraded/Offline counts; per row display desired/current/ready, revision, generation/observed generation, Condition reason and last transition. Explain that “全部重启” submits independent rolling restart intents and does not stop all services first.

- [ ] **Step 6: Run web tests and build**

Run: `pnpm test:web`

Run: `pnpm web:build`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add web/src/containers/runtime web/src/i18n/messages.ts
git commit -m "feat: show independent AIFAR service runtime states"
```

### Task 13: Remove duplicate desired-state writers and close compatibility paths

**Files:**
- Modify: `backend/internal/apps/aifar/release.go`
- Modify: `backend/internal/apps/aifar/status.go`
- Modify: `backend/internal/apps/aifar/autoscale.go`
- Modify: `backend/internal/apps/aifar/templates/install.sh`
- Modify: `backend/internal/apps/aifar/templates/service-install.sh`
- Modify: `backend/internal/apps/aifar/templates/autoscale-out.sh`
- Modify: `backend/internal/apps/aifar/templates/scale-service.sh`
- Modify: `backend/internal/runtimeagent/spec.go`
- Modify: `backend/internal/runtimeagent/legacy.go`
- Modify: tests covering metadata/env/spec generation

**Interfaces:**
- `aifar_deployments.spec_json/generation` is the sole control-plane desired authority.
- Agent per-service files are the sole node execution cache.
- Legacy `runtime-spec.json` is read-only migration backup; legacy writes return `409 LEGACY_RUNTIME_SPEC_DISABLED` after switch.

- [ ] **Step 1: Write failing duplicate-authority tests**

```go
func TestNewModelDoesNotWriteAggregatedDesiredReplicas(t *testing.T) {
    metadata := buildInstalledMetadata(t)
    if _, ok := metadata["desiredReplicas"]; ok { t.Fatal("metadata still contains desiredReplicas") }
    env := renderComposeEnv(t)
    if strings.Contains(env, "AIFAR_DESIRED_REPLICAS") { t.Fatal("compose env still contains aggregated replicas") }
}

func TestNewGenerationRejectsLegacyFullSpecWrite(t *testing.T) {
    // Switch model, accept generation 2, POST legacy reconcile, expect 409 and
    // assert generation 2 remains unchanged.
}
```

- [ ] **Step 2: Run focused tests and verify RED**

Run: `cd backend; go test ./internal/apps/aifar ./internal/runtimeagent ./cmd/aifar-agent -run 'TestNewModelDoesNotWrite|TestNewGenerationRejects' -count=1`

Expected: FAIL while metadata/env and legacy writers still exist.

- [ ] **Step 3: Remove new-model reads/writes of aggregated replicas and Runtime Nacos**

Delete generation of `AIFAR_DESIRED_REPLICAS`; stop writing `app_instances.metadata.desiredReplicas`; make status, autoscale and release code read per-service Deployment rows. Keep `java-common.env`/`java-secrets.env` Nacos connection values because business containers and discovery still need them.

- [ ] **Step 4: Restrict legacy code to migration-only types**

No ordinary install/update/scale/config/restart path may write or promote `runtime-spec.json`. Retain legacy parsing only in `legacy.go` and migration tests; remove obsolete stop-all helpers once no callers remain.

- [ ] **Step 5: Run backend and script regression tests**

Run: `pnpm test`

Run: `pnpm test:scripts`

Run: `pnpm backend:build`

Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add backend/internal/apps/aifar backend/internal/runtimeagent backend/cmd/aifar-agent
git commit -m "refactor: retire aggregate AIFAR runtime desired state"
```

### Task 14: Complete integration, race, build and release verification

**Files:**
- Create: `backend/internal/apps/aifar/runtime_independence_integration_test.go`
- Create: `backend/internal/runtimeagent/recovery_integration_test.go`
- Update: `docs/superpowers/specs/2026-08-07-aifar-runtime-per-service-controller-design.md` status to implemented only after all gates pass.

**Interfaces:**
- Verifies every acceptance criterion in design section 19.
- Produces no new feature surface.

- [ ] **Step 1: Add end-to-end fake-remote acceptance tests**

Cover these exact scenarios:

```text
1. file remains unready for five minutes while permission reaches Available.
2. file offline and permission start are accepted concurrently.
3. two permission writes conflict before the second task starts.
4. install succeeds at Manifest acceptance and leaves an unready service Degraded.
5. restart-all issues no stop-all command.
6. one-service upload/config/scale/offline/rollback changes no peer generation.
7. one corrupt Manifest does not block Agent recovery of peers.
8. resync and Docker events do not resurrect replicas=0.
9. Runtime API/UI contain no Nacos status while proxy registration still runs.
10. migration preserves desired state and performs no intentional container restart.
```

- [ ] **Step 2: Run focused race tests**

Run: `cd backend; go test -race ./internal/runtimeagent ./internal/store -count=1`

Expected: PASS.

- [ ] **Step 3: Run all normal test suites**

Run: `pnpm test`

Run: `pnpm test:web`

Run: `pnpm test:scripts`

Expected: all PASS with zero failures.

- [ ] **Step 4: Run builds**

Run: `pnpm web:build`

Run: `pnpm backend:build`

Expected: both PASS.

- [ ] **Step 5: Run release gate**

Run: `pnpm test:local`

Expected: backend tests, frontend tests, script tests, builds, package and `release:verify` all PASS.

- [ ] **Step 6: Inspect repository diff and security boundaries**

Run: `git diff --check`

Run: `rg -n 'nacosRegistered|nacosReady|lastNacosHeartbeatAt|lastNacosError|AIFAR_DESIRED_REPLICAS|restart-runtime --spec|reconcile-runtime --spec' backend web/src`

Expected: `git diff --check` has no output; remaining legacy strings occur only in migration compatibility tests/code explicitly named `legacy`.

- [ ] **Step 7: Update design status and commit final integration**

```bash
git add backend/internal/apps/aifar/runtime_independence_integration_test.go backend/internal/runtimeagent/recovery_integration_test.go docs/superpowers/specs/2026-08-07-aifar-runtime-per-service-controller-design.md
git commit -m "test: verify independent AIFAR service controllers"
```

## Execution Checkpoints

- After Tasks 1-2: review Store migrations, CAS and lock conflict matrix before Agent work.
- After Tasks 3-6: review Agent persistence, concurrency, Conditions, discovery and compatibility before Server cutover.
- After Tasks 7-10: review acceptance boundary, install semantics, operation isolation and HTTP contract before migration/UI.
- After Tasks 11-13: run a legacy-instance migration rehearsal using fake remote evidence before removing remaining ordinary legacy writers.
- After Task 14: do not deploy to a real target automatically; a real migration requires a separately approved, read-only preflight and maintenance window.
