# AIFAR Artifact Upgrade and Rollback Enterprise Design

Status: Draft 0.1
Date: 2026-07-09
Audience: AIFAR Deployment backend, frontend, installer, runtime-agent, and QA maintainers

本文定义 AIFAR Runtime v2 中 jar 包和 Vue 包的企业级升级与回滚方案。范围先聚焦 AIFAR 应用服务制品发布，包括单服务 jar/web 包升级、批量 bundle 升级、失败自动恢复、人工回滚、发布历史、审计、保留策略和后续扩展到通用应用生命周期的接口形态。

## 1. 结论

当前升级链路可以作为 forward rollout 的第一版继续使用，但不能作为企业级回滚能力交付。主要原因是 jar 和 Vue 制品会覆盖当前目录，旧制品没有按 release 持久保存；`app_releases` 记录存在，但不包含完整的可执行回滚输入；系统也没有正式 rollback API、rollback 脚本和前端发布历史操作。

推荐方案：

- 把每次升级定义为不可变 release，release 内保存原始制品、构建上下文、镜像信息、运行时 spec、服务 env 快照和每服务 before/after revision。
- 升级不是“覆盖文件”，而是“创建 release -> 构建/校验 -> 切换 active revision -> 健康检查 -> 激活记录”。
- 回滚不是“删除新版本”，而是创建一个新的 rollback release，把指定服务切回目标 revision，并完整记录回滚来源、目标、操作者和原因。
- 远端文件、数据库发布记录和 runtime-agent 状态必须能相互校验和自愈，不能只依赖其中一个。
- 第一阶段优先实现单服务和 bundle 的可靠回滚；第二阶段补 release GC、灰度/金丝雀、审批和通用应用生命周期。

## 2. 目标

1. jar 包和 Vue 包升级可重复执行、可审计、可观测。
2. 任意成功 release 都能判断是否可回滚。
3. 回滚不依赖用户重新上传旧包。
4. 单服务升级、批量 bundle 升级都保存每个服务的 before/after revision。
5. 失败时可以自动恢复到升级前运行态，至少做到容器、env、runtime-spec 一致。
6. 数据库记录、远端 release 目录和 Docker 镜像标签可互相对账。
7. 发布历史能展示差异、状态、操作者、任务、制品 hash 和回滚入口。
8. 保留策略不会删除当前运行版本和可回滚链路依赖的制品。

## 3. 非目标

1. 第一阶段不做完整 Kubernetes 发布系统。
2. 第一阶段不强制引入外部制品仓库。
3. 第一阶段不要求所有 Docker/MySQL/Redis/MinIO/Nacos 都支持升级回滚。
4. 回滚不保证业务数据 schema 自动降级。若 jar 升级包含数据库迁移，需要额外的应用级 migration/backup 机制。
5. 不允许前端或 API 提交任意 shell 执行。

## 4. 当前问题

当前代码已经具备一些基础能力：

- worker task、target、step、SSE 日志。
- AIFAR 编排锁。
- 单服务和 bundle 上传升级接口。
- 远端脚本执行和 `aifar-agent reconcile-runtime`。
- `app_releases` 发布记录。
- `aifar_deployments`、`aifar_replicasets`、`aifar_pods` 控制面记录。
- runtime-agent 在创建新容器失败时能删除新建容器。

但回滚闭环缺失：

- jar 更新覆盖 `app.jar`，并删除旧 `target/*.jar`。
- Vue 更新删除旧 `dist/html`，再解压新包。
- 旧制品没有持久 release artifact 路径。
- `app_releases.manifest_json` 只记录摘要，缺少 env/spec/image/artifact 的完整 before/after。
- bundle 只有一个 `previousRevision`，不能表达多个服务各自原来的 revision。
- 没有正式 rollback API、rollback task、rollback script 和 UI。
- 远端成功但本地记录失败时，可能出现控制面与实际运行态不一致。
- `DeleteOldAppReleases` 只清数据库记录，不清理远端制品，也无法保护远端可回滚链路。

## 5. 核心原则

### 5.1 Release 不可变

release 一旦创建，其 releaseId、制品 hash、服务列表、构建输入和快照不可修改。后续回滚或修复必须创建新 release。

### 5.2 Active 是指针，不是文件覆盖结果

当前运行版本由 active revision 指向 release。`app.jar`、`dist`、env 和 runtime-spec 可以作为运行时副本，但源头必须是 release artifact。

### 5.3 每服务独立 revision

AIFAR 是多服务应用，不能只用一个全局 revision 表达所有服务。单服务升级时只改变该服务 revision；bundle 升级时也要保存每个 changed service 的 before/after。

### 5.4 回滚也是发布

rollback 生成新的 release 记录：

- `kind=rollback`
- `rollbackFrom=<当前 release>`
- `rollbackTo=<目标 release>`
- `changedServices=[...]`
- `reason=<用户填写原因>`

这样审计链路完整，也能再次从 rollback release 回到其它 release。

### 5.5 先记录意图，再改远端

任务开始后先写入 `pending/staging` release 记录，包含足够恢复的信息。远端执行完成后再把 release 标记为 `active` 或 `failed`。

### 5.6 失败要恢复一致性

失败恢复不能只删除新容器，还要恢复：

- 服务 env 文件。
- `runtime/agent/runtime-spec.json`。
- active release 指针。
- 控制面 metadata。
- 新建但未激活的容器和镜像。

### 5.7 GC 不能破坏回滚

清理策略必须保护：

- 当前 active revision。
- 每服务当前 active revision。
- 最近 N 个成功 release。
- 被 pin 的 release。
- rollback 链路依赖的 base release。

## 6. 总体架构

```text
Vue 发布历史 / 升级弹窗 / 回滚确认
  |
  | /api/v2/apps/instances/{id}/aifar/releases
  | /api/v2/apps/instances/{id}/aifar/update-artifact
  | /api/v2/apps/instances/{id}/aifar/update-artifact-bundle
  | /api/v2/apps/instances/{id}/aifar/rollback
  v
Go httpapi
  |
  | parse request, RBAC, audit, worker task
  v
apps/aifar release service
  |
  | plan, validate, acquire lock, create pending release
  v
worker task
  |
  | upload artifact/script, execute trusted template
  v
target server
  |
  | immutable release dir, snapshot, build image, reconcile runtime
  v
aifar-agent
  |
  | create/replace containers, health gate, endpoint refresh
  v
store control plane
  |
  | app_releases, app_instances metadata,
  | aifar_deployments, aifar_replicasets, aifar_pods, audit
```

## 7. 远端目录结构

推荐在每个 AIFAR 实例的 `installRoot` 下增加 release 仓库：

```text
$INSTALL_ROOT/
  apps/
    gateway/
    web-vue3/
    system/
  runtime/
    env/
      gateway.env
      web-vue3.env
      java-common.env
    agent/
      runtime-spec.json
  releases/
    <releaseId>/
      manifest.json
      services/
        gateway/
          artifact/
            original.jar
            sha256
          build-context/
            app.jar
            target/
          snapshot/
            before.env
            before-runtime-spec.json
            before-image.txt
            before-revision.txt
        web-vue3/
          artifact/
            original.zip
            sha256
          build-context/
            dist/
          snapshot/
            before.env
            before-runtime-spec.json
            before-image.txt
            before-revision.txt
      result/
        after-runtime-spec.json
        after-env/
        health.json
```

第一阶段为了减少脚本改动，可以保留现有 `$INSTALL_ROOT/apps/<service>` 作为 Docker build context，但升级时必须先把制品保存到 `$INSTALL_ROOT/releases/<releaseId>/...`，再从 release 目录复制到当前 build context。

第二阶段可以把 `$INSTALL_ROOT/apps/<service>` 改成指向 active release build-context 的软链接，进一步减少覆盖式状态。

## 8. Release Manifest

每个 release 都要有本地 DB manifest 和远端 `manifest.json`。二者内容应基本一致，远端 manifest 用于灾难恢复和对账。

建议结构：

```json
{
  "schema": "aifar-release-v2",
  "app": "aifar",
  "instanceId": "app_xxx",
  "releaseId": "20260709T123000.000000000Z-rollout-gateway",
  "kind": "rollout",
  "status": "active",
  "version": "runtime-v2",
  "createdAt": "2026-07-09T12:30:00Z",
  "activatedAt": "2026-07-09T12:31:00Z",
  "actor": "admin",
  "taskId": "task_xxx",
  "reason": "",
  "rollbackFrom": "",
  "rollbackTo": "",
  "changedServices": ["gateway"],
  "serviceRevisionsBefore": {
    "gateway": "old-revision"
  },
  "serviceRevisionsAfter": {
    "gateway": "20260709T123000.000000000Z-rollout-gateway"
  },
  "artifacts": {
    "gateway": {
      "type": "java",
      "file": "gateway.jar",
      "size": 123456,
      "sha256": "abc",
      "remotePath": "/aifar/apps/admin/releases/.../gateway/artifact/original.jar"
    }
  },
  "images": {
    "gateway": {
      "before": "aifar-gateway:old-revision",
      "after": "aifar-gateway:20260709T123000.000000000Z-rollout-gateway"
    }
  },
  "snapshots": {
    "runtimeSpecBefore": "/aifar/apps/admin/releases/.../snapshot/before-runtime-spec.json",
    "runtimeSpecAfter": "/aifar/apps/admin/releases/.../result/after-runtime-spec.json",
    "envBefore": {
      "gateway": "/aifar/apps/admin/releases/.../gateway/snapshot/before.env"
    }
  },
  "health": {
    "status": "passed",
    "checks": []
  }
}
```

## 9. 数据模型

### 9.1 第一阶段：复用现有表

先复用 `app_releases.manifest_json` 保存 v2 manifest，减少迁移风险。

需要调整的约定：

- `status` 支持 `pending/staging/deploying/verifying/active/failed/superseded/rolled_back/gc_deleted`。
- `kind` 放入 manifest：`full/rollout/rollout-bundle/rollback/rollback-bundle`。
- `release_id` 继续唯一。
- 新 release 先保存为 `pending`，任务成功后改为 `active`。
- 旧 active release 不删除，标记为 `superseded` 或保留 `success`，实际 active 以 `app_instances.metadata.currentRevision` 和每服务 revision map 为准。

### 9.2 第二阶段：新增规范化表

企业版建议补充：

```text
app_release_services
  id
  instance_id
  release_id
  service_name
  before_revision
  after_revision
  artifact_type
  artifact_file
  artifact_sha256
  artifact_size
  artifact_remote_path
  image_before
  image_after
  status
  created_at
  updated_at

app_release_artifacts
  id
  instance_id
  release_id
  service_name
  kind
  file_name
  sha256
  size
  remote_path
  retained
  created_at

app_release_gc_runs
  id
  instance_id
  task_id
  dry_run
  deleted_releases_json
  protected_releases_json
  status
  created_at
```

所有迁移继续集中在 `backend/internal/store.Store.migrate()`，并补 store 测试。

## 10. API 设计

### 10.1 发布历史

```http
GET /api/v2/apps/instances/{id}/aifar/releases
```

返回：

```json
{
  "items": [
    {
      "releaseId": "20260709T123000Z-rollout-gateway",
      "kind": "rollout",
      "status": "active",
      "createdAt": "...",
      "activatedAt": "...",
      "changedServices": ["gateway"],
      "rollbackAvailable": true,
      "artifactMissing": false,
      "taskId": "task_xxx"
    }
  ]
}
```

### 10.2 发布详情

```http
GET /api/v2/apps/instances/{id}/aifar/releases/{releaseId}
```

返回完整 manifest、服务 before/after、制品 hash、健康检查结果和相关任务。

### 10.3 单服务升级

沿用现有：

```http
POST /api/v2/apps/instances/{id}/aifar/update-artifact
```

新增可选字段：

- `strategy`: `rolling/recreate/canary`
- `reason`: 发布原因
- `dryRun`: 只校验和生成计划

### 10.4 Bundle 升级

沿用现有：

```http
POST /api/v2/apps/instances/{id}/aifar/update-artifact-bundle
```

bundle manifest 建议增加：

```json
{
  "schema": "aifar-artifact-bundle-v2",
  "app": "aifar",
  "version": "runtime-v2",
  "reason": "monthly release",
  "services": [
    {
      "name": "gateway",
      "artifact": "gateway.jar",
      "type": "java",
      "sha256": "..."
    }
  ]
}
```

### 10.5 回滚

```http
POST /api/v2/apps/instances/{id}/aifar/rollback
```

请求：

```json
{
  "targetReleaseId": "20260709T120000Z-rollout-gateway",
  "services": ["gateway"],
  "strategy": "rolling",
  "reason": "new gateway has login regression",
  "force": false
}
```

规则：

- `reason` 必填。
- 默认只能回滚 changed services。
- 如果目标 release 的 artifact 缺失，拒绝并提示不可回滚。
- 如果目标 release 包含数据库 schema 风险，必须二次确认或阻止。
- 所有回滚返回 task id，不做同步远程变更。

## 11. 任务步骤

### 11.1 单服务升级步骤

```text
load-instance
load-server
acquire-lock
validate-artifact
create-pending-release
prepare-remote-release
upload-artifact
snapshot-current-state
stage-artifact
build-image
write-next-runtime-spec
reconcile-runtime
verify-health
activate-release
record-control-plane
cleanup-workdir
release-lock
```

### 11.2 Bundle 升级步骤

```text
load-instance
load-server
acquire-lock
validate-bundle
create-pending-release
prepare-remote-release
upload-artifacts
snapshot-current-state
stage-artifacts
build-images
write-next-runtime-spec
reconcile-runtime
verify-health
activate-release
record-control-plane
cleanup-workdir
release-lock
```

### 11.3 回滚步骤

```text
load-instance
load-target-release
load-server
acquire-lock
validate-rollback
create-pending-rollback-release
snapshot-current-state
restore-target-artifacts
write-rollback-runtime-spec
reconcile-runtime
verify-health
activate-rollback-release
record-control-plane
mark-rollback-source
cleanup-workdir
release-lock
```

## 12. 远端脚本状态机

脚本必须幂等，同一个 `releaseId` 重试不能破坏已有状态。

推荐状态：

```text
INIT
  -> SNAPSHOT_DONE
  -> ARTIFACT_STAGED
  -> IMAGE_BUILT
  -> SPEC_WRITTEN
  -> RECONCILE_DONE
  -> HEALTH_PASSED
  -> ACTIVATED
```

每个状态写入：

```text
$INSTALL_ROOT/releases/<releaseId>/.state
```

失败恢复：

```text
if failed before ACTIVATED:
  restore env from snapshot
  restore runtime-spec from snapshot
  run aifar-agent reconcile-runtime --spec old-spec
  remove new revision pods
  keep failed release dir for diagnosis
```

脚本结束时写：

```text
$INSTALL_ROOT/.aifar/active-release.json
$INSTALL_ROOT/.aifar/last-rollout.json
```

## 13. 健康检查门禁

升级和回滚必须通过 health gate 才能激活。

基础检查：

- Docker image 存在。
- 目标服务容器 running。
- `aifar.spec-hash` 与 runtime-spec 一致。
- 容器健康检查成功。
- `aifar-agent status` 返回服务 ready。
- gateway/web-vue3 对应入口可访问。

服务扩展检查：

- Java 服务可通过 `/actuator/health` 或配置的健康路径。
- web-vue3 可验证首页或静态资源响应。
- Nacos 注册不应在本地 runtime 发布中被重新打开，保持当前 agent-runtime-v2 模式。

健康检查失败时：

- 默认自动恢复升级前状态。
- 任务标记 failed。
- release 标记 failed。
- 保留失败 release 目录用于诊断。

## 14. 一致性与自愈

可能出现的问题：

- 远端发布成功，数据库记录失败。
- 数据库记录 active，但远端目录丢失。
- Docker 镜像被手动删除。
- `runtime-spec.json` 与 `app_instances.metadata` 不一致。

解决方案：

1. 发布前先写 pending release。
2. 远端成功后写远端 `active-release.json`。
3. DB 激活失败时，任务标记 `degraded`，并提示需要执行对账。
4. 新增只读对账能力：

```http
POST /api/v2/apps/instances/{id}/aifar/reconcile-release-state
```

第一阶段可以先作为内部 service 方法，不暴露 UI：

- 读取 DB release。
- 读取远端 `.aifar/active-release.json`。
- 读取 `aifar-agent status`。
- 对比服务 revision、容器、镜像、artifact hash。
- 给出修复建议或创建 repair task。

## 15. 锁与并发

锁粒度：

- 单服务升级：`instance_id + service_name`。
- bundle 升级：锁所有 changed services，也可以用 `service_name="*"` 阻塞整个实例。
- 回滚：锁目标 services。
- 扩缩容与升级同服务互斥。
- 检查任务只读，不抢写锁。

锁记录继续复用 `aifar_orchestration_locks`，但要把 `operation` 区分清楚：

- `update-artifact`
- `update-artifact-bundle`
- `rollback-artifact`
- `rollback-artifact-bundle`
- `release-gc`

## 16. 前端设计

入口建议放在 AIFAR Runtime 实例详情或 Containers 的 AIFAR runtime 区域。

### 16.1 发布历史

字段：

- releaseId
- 类型：完整安装、单服务升级、bundle 升级、回滚
- 状态：active、success、failed、superseded
- 变更服务
- 制品名称和 hash
- 操作者
- 发布时间
- 任务入口
- 是否可回滚

### 16.2 升级弹窗

继续支持：

- 单服务 jar/web 包上传。
- bundle zip 上传。

新增：

- 发布原因。
- 策略选择：rolling/recreate，canary 可后续开放。
- dry run。
- 变更预览：将改变哪些服务、当前 revision、新 revision。

### 16.3 回滚确认

回滚前展示：

- 当前 active revision。
- 目标 release revision。
- 将回滚的服务列表。
- 制品 hash。
- 相关任务。
- 风险提示：不回滚数据库数据。
- 必填回滚原因。

不可回滚时显示具体原因：

- 制品已被 GC。
- 远端 release 目录缺失。
- 目标 release 状态不是 success/active/superseded。
- 服务不在目标 release 范围内。
- 当前存在运行中的升级/回滚锁。

## 17. 审计与权限

权限：

- 查看发布历史：`AppsView` 或新增 `AppsReleaseView`。
- 升级：`AppsManage`。
- 回滚：`AppsManage`，企业版可拆为 `AppsRollback`。
- GC：管理员权限。

审计事件：

- `aifar.release.create`
- `aifar.release.activate`
- `aifar.release.fail`
- `aifar.release.rollback`
- `aifar.release.gc`
- `aifar.release.reconcile`

审计 details 只允许记录 releaseId、service、hash、taskId、reason 摘要，不记录敏感环境变量。

## 18. 保留与清理

默认策略：

- 每个实例保留最近 5 个成功 release。
- 每个服务至少保留当前 active revision 和前 2 个 revision。
- 失败 release 保留 7 天。
- pin 的 release 永不自动删除。
- 远端 artifact、Docker image、DB release 必须一起参与 GC。

GC 流程：

```text
load releases
load active revisions by service
load pinned releases
build protected set
dry-run report
delete unprotected remote release dirs
delete unprotected images
mark DB releases gc_deleted
write audit
```

第一阶段可以只实现 dry-run 和 DB 保护，不自动删除远端文件。

## 19. 文件结构建议

后端：

```text
backend/internal/apps/aifar/
  release.go                    # release id、manifest、公共 release helper
  release_manifest.go           # v2 manifest 结构和校验
  release_repository.go         # app_releases 读写封装
  artifact_repository.go        # 远端 release artifact 路径和校验
  update_artifact.go            # 单服务升级 service
  update_bundle.go              # bundle 升级 service
  rollback.go                   # rollback service
  release_gc.go                 # release 保留和清理
  release_reconcile.go          # DB/远端/agent 对账
  templates/
    update-artifact.sh
    update-artifact-bundle.sh
    rollback-artifact.sh
    rollback-artifact-bundle.sh
    release-gc.sh
```

HTTP：

```text
backend/internal/httpapi/
  apps_artifact_handlers.go     # 保留升级入口
  apps_release_handlers.go      # 发布历史、详情、回滚、对账、GC
```

Store：

```text
backend/internal/store/
  app_instances.go              # 现有 app_releases 基础 CRUD
  app_releases.go               # 建议后续拆出 release 专用 CRUD
  aifar_release_services.go     # 第二阶段规范化服务级 release 表
```

前端：

```text
web/src/apps/aifar/
  releases.ts                   # release API 类型和请求
  releaseLabels.ts              # 状态、类型展示文案

web/src/views/
  ContainersView.vue            # 短期可继续承载 AIFAR runtime 发布入口

web/src/components/
  AifarReleaseHistory.vue
  AifarArtifactUpgradeDialog.vue
  AifarRollbackDialog.vue
```

## 20. 实施阶段

### Phase 1：可靠 release 记录和制品保留

- 升级前创建 pending release。
- 保存 v2 manifest 到 `app_releases.manifest_json`。
- 远端创建 `$INSTALL_ROOT/releases/<releaseId>`。
- jar/vue 原始制品持久保存到 release 目录。
- 保存 env 和 runtime-spec before snapshot。
- bundle 保存每服务 before/after revision。
- 发布历史 API 和 UI 只读展示。

### Phase 2：单服务回滚

- 新增 rollback API。
- 新增 `rollback-artifact.sh`。
- 支持从目标 release 恢复单服务 artifact/env/spec/image。
- 回滚成功创建 `kind=rollback` release。
- 回滚失败自动恢复回滚前状态。

### Phase 3：Bundle 回滚

- 支持多个服务同时回滚。
- 按依赖顺序处理：普通 Java 服务 -> gateway -> web-vue3。
- 每服务独立恢复 before/after。
- 支持部分服务不可回滚时 fail-fast。

### Phase 4：企业治理

- release pin。
- GC dry-run 和实际清理。
- 对账/repair task。
- 灰度/canary。
- 审批流或二次确认。
- 更细 RBAC：升级、回滚、GC 分权。

## 21. 测试计划

后端单元测试：

- release manifest 生成和校验。
- bundle 每服务 before/after 记录。
- rollback request 校验。
- app_releases 状态流转。
- GC protected set。

fake remote 测试：

- 单服务升级会上传 artifact、生成 release 目录、执行脚本。
- 升级失败时 release 标记 failed。
- 单服务 rollback 会使用目标 release artifact。
- bundle rollback 会按 changed services 恢复。

脚本测试：

- jar artifact staging 不删除旧 release。
- Vue artifact staging 保留原始 zip/tar 和解压结果。
- reconcile 失败恢复 before env/spec。
- 重复执行同一 releaseId 幂等。

集成 smoke：

- dry-run 发布计划。
- 上传 jar 后 active revision 改变。
- 回滚到上一 release 后 active revision 恢复。
- 发布历史展示 release 链路。

真实 mutating E2E：

- 只在显式授权和服务器白名单下执行。
- 准备两个测试 jar/web 包。
- 执行升级 -> 健康检查 -> 回滚 -> 健康检查 -> 清理。

## 22. 与通用应用生命周期的关系

这个设计先用于 AIFAR Runtime v2，因为它已经具备 agent-runtime、服务 revision、runtime-spec 和发布入口。后续可以抽象成通用应用生命周期：

```go
type UpdateModule interface {
    PlanUpdate(req UpdateRequest) (UpdatePlan, error)
    ValidateUpdate(req UpdateRequest) error
    Update(ctx context.Context, req UpdateRequest, log Logger, targetLog TargetLogger) error
}

type RollbackModule interface {
    PlanRollback(req RollbackRequest) (RollbackPlan, error)
    ValidateRollback(req RollbackRequest) error
    Rollback(ctx context.Context, req RollbackRequest, log Logger, targetLog TargetLogger) error
}
```

MySQL/Redis/MinIO/Nacos 后续如果要支持升级回滚，必须先定义各自的数据备份、配置快照和版本兼容规则，不能直接套 jar/Vue 的制品回滚。

## 23. 推荐落地顺序

最小可交付闭环：

1. 改升级脚本：先保存 release 目录和 before snapshot，再覆盖当前 build context。
2. 改 Go service：升级前保存 pending release，成功后 active，失败后 failed。
3. manifest 增加每服务 before/after revision 和 artifact remote path。
4. 增加发布历史只读 API/UI。
5. 增加单服务 rollback API 和脚本。
6. 增加 bundle rollback。
7. 最后做 GC、对账和灰度。

这样第一阶段不会大改现有 runtime-agent，但能先解决最关键的问题：旧 jar/Vue 包可找回、回滚可执行、失败可恢复、发布历史可信。
