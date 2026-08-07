# AIFAR Runtime 每服务独立控制器设计

日期：2026-08-07
状态：已确认，待实施计划

## 1. 摘要

本设计将当前“一个 Runtime 实例一份全量 `runtime-spec.json`、一次调和覆盖全部服务”的模型，改为“实例级共享基础配置 + 每服务独立 Manifest + 每服务独立控制器”。目标行为参考 Kubernetes Deployment/Service：业务服务之间没有编排启动依赖，服务的启动、下线、扩缩容、更新、重启、失败和恢复均以 `(instanceId, serviceName)` 为边界；业务依赖由业务自身通过配置、重试、熔断或降级定义。

选定方案继续使用现有 Docker 与 `aifar-agent`，不在本阶段引入 K3s/Kubernetes。Nacos 从 RuntimeSpec、Runtime API 状态和 UI 中移除，但 Agent 代注册、注销和心跳继续作为后台服务发现职责，以支持当前多节点宿主机代理链路。

安装状态与运行状态分离：安装资源和每服务 Manifest 被 Agent 原子持久化并入队后，AIFAR 实例即为 `installed`；容器创建、启动、健康检查或业务配置失败只影响对应 Deployment 的 Condition，不把应用实例改成安装失败。

## 2. 当前问题与代码事实

当前实现存在以下耦合：

1. `RuntimeSpec` 同时包含实例下的全部 Deployment、Service、Ingress 和 Nacos 字段。
2. `compose.env` 的 `AIFAR_DESIRED_REPLICAS` 与 `app_instances.metadata.desiredReplicas` 也保存全部服务期望副本，形成多份期望状态。
3. Runtime API 和 Store 编排锁当前以实例为冲突边界，不同服务不能真正并发变更。
4. `Manager.Apply` 通过一个实例级 `reconcileMu` 执行全量或增量 Apply；虽然增量 Apply 会跳过未变化 Deployment，但提交和持久化仍以完整 RuntimeSpec 为单位。
5. 初始化安装一次提交全量 spec；`Reconciler.ReconcileRuntime` 只有在全部目标 Deployment 调和成功后才触发 Nacos 代理同步。一个慢或失败服务会延迟其他已就绪服务注册。
6. 当前 `RestartAll` 先删除实例全部 Runtime Pod，再统一启动和验证，形成实例级停机窗口。
7. Runtime UI 暴露 Nacos 注册状态，并在 Runtime 整体 `degraded` 时禁用全部变更，导致无关服务相互影响。
8. 安装脚本等待全量容器调和完成后才把实例从 `installing` 更新为 `installed`；容器初始化失败因此被误归类为安装失败。

## 3. 目标

1. 不同服务可以同时启动、下线、扩缩容、更新、重启和恢复。
2. 同一服务的变更保持串行，旧请求不能覆盖新 generation。
3. 一个服务失败、阻塞或持续 CrashLoop 时，不取消、不回滚、不阻塞其他服务。
4. 服务没有启动顺序或业务依赖图；Agent 只处理声明式期望状态、容器健康和 Endpoint。
5. 每个 Ready 服务立即发布 Endpoint 并独立触发后台服务发现注册，不等待其他服务。
6. 安装成功边界是资源与 Manifest 已可靠建立，不是全部容器 Ready。
7. `restart-all` 成为多个独立服务重启意图的聚合入口，不再 stop-all。
8. 删除 Runtime 配置和 UI 中的 Nacos 状态，同时保留 Agent 代理服务发现链路。
9. 现有 `agent-runtime-v2` 实例可在不主动重启业务容器的前提下迁移到新模型。
10. 所有用户触发的变更继续走 worker、task target/step、权限和审计，不引入自由 Shell API。

## 4. 非目标

- 不在本阶段引入 K3s、Kubernetes API Server、CNI、CSI 或镜像仓库。
- 不实现跨主机副本调度；当前仍是每台目标服务器上的本地 Docker Runtime。
- 不删除 Agent 的 Nacos 代注册、注销和心跳实现。
- 不把业务依赖、服务启动顺序、数据库就绪顺序或 Feign 依赖写入 Agent。
- 不保证 Docker daemon、宿主机磁盘、网络或 Agent 进程等节点级故障只影响一个服务。
- 不把聚合操作包装成跨服务原子事务；批量操作只聚合提交和展示，每个服务独立收敛。
- 不开放任意 Docker 参数、任意命令或自由 Shell。

## 5. 选定架构

采用“实例基础配置 + 每服务独立 Manifest + 每服务控制器 + 独立服务发现控制器”。

```text
aifar-server control plane
├── app_instances                  安装生命周期
├── aifar_deployments              每服务期望 spec/generation/conditions
├── aifar_replicasets              每服务 revision 历史
├── aifar_pods                     每服务观察到的 Pod
└── aifar_service_endpoints        每服务 Ready Endpoint

aifar-agent node state
└── /var/lib/aifar-agent/instances/<instanceId>/
    ├── instance.json              实例共享基础配置
    └── deployments/
        ├── gateway.json
        ├── permission.json
        ├── file.json
        └── ...
```

控制面 SQLite 是用户期望状态的权威来源；Agent 每服务持久 Manifest 是目标节点执行态的权威缓存。二者通过 generation 和幂等接收协议对账。面板安装目录中的发布制品与环境文件仍是容器输入，但不再保存可覆盖全部服务的权威全量 spec。

## 6. 资源模型

### 6.1 实例基础配置

`instance.json` 只包含真正跨服务共享的字段：

- schema/model version；
- instance ID；
- install root；
- Docker network；
- gateway/web Ingress 映射；
-共享代理监听配置。

`instance.json` 不包含 Deployment 列表、desired replicas、service revision、服务健康状态或 Nacos 状态。修改它属于实例共享维护操作。

### 6.2 每服务 Manifest

每个服务 Manifest 至少包含：

```json
{
  "apiVersion": "aifar.io/v1alpha1",
  "kind": "Deployment",
  "metadata": {
    "instanceId": "admin",
    "name": "permission",
    "generation": 7
  },
  "spec": {
    "replicas": 1,
    "image": "aifar-permission:revision",
    "revision": "revision",
    "restartGeneration": 2,
    "strategy": {
      "type": "RollingUpdate",
      "maxSurge": 1,
      "maxUnavailable": 0,
      "progressDeadlineSeconds": 300,
      "rollbackOnFailure": true
    },
    "ports": [],
    "envFiles": [],
    "volumes": [],
    "resources": {},
    "healthCheck": {},
    "entrypoint": [],
    "command": [],
    "environment": {},
    "labels": {}
  },
  "service": {
    "appName": "alpha-permission",
    "listenPort": 38010,
    "targetPort": 38010,
    "affinityPolicy": "round-robin"
  }
}
```

Manifest 不包含其他服务，也不包含 Nacos 状态。`restartGeneration` 允许在镜像、revision 和副本数不变时触发该服务独立滚动重启。

### 6.3 SQLite 迁移

继续通过 `backend/internal/store.Store.migrate()` 做集中前向迁移。`aifar_deployments` 增加：

- `spec_json text`：完整服务 Manifest；
- `generation integer not null default 1`；
- `observed_generation integer not null default 0`；
- `conditions_json text`；
- `last_transition_at datetime`。

`unique(instance_id, service_name)` 保持不变。新增 Store 方法必须使用 compare-and-swap 语义更新 generation，并拒绝陈旧写入。

`app_instances.status` 只表达 `installing`、`installed`、`install_failed` 等安装生命周期。Runtime 健康汇总不反向覆盖安装状态。

### 6.4 删除重复权威值

- 从 `compose.env` 删除 `AIFAR_DESIRED_REPLICAS`。
- 不再读取或写入 `app_instances.metadata.desiredReplicas`。
- `compose.env` 只保留实例共享默认值；服务专属配置继续位于 `<service>.env` 和 `resource.<service>.env`。
- `java-common.env` 与 `java-secrets.env` 继续保存业务容器和 Agent 服务发现所需的 Nacos 连接参数。
- 旧 `runtime-spec.json` 迁移后只作为备份，不再参与生成新期望状态。

## 7. Generation 与接收协议

每次修改服务 spec、replicas、revision、资源、配置摘要或 `restartGeneration`，控制面都增加该服务 generation。协议如下：

1. Server 在 SQLite 中写入 generation N 和 `pending_acceptance` Condition。
2. Server 通过受控远程命令调用 Agent 本地 API或 CLI，提交单服务 Manifest及 generation N。
3. Agent 校验 schema、instance/service 身份和全部路径。
4. generation 小于 Agent 当前 generation 时返回 `409 STALE_DEPLOYMENT_GENERATION`，并返回当前 generation。
5. generation 等于当前值且 spec hash 相同，按幂等成功返回。
6. generation 等于当前值但 spec hash 不同，返回 `409 DEPLOYMENT_GENERATION_CONFLICT`。
7. generation 更大时，Agent在同目录写临时文件、fsync、原子 rename，更新内存索引并入队，然后立即返回 `202 Accepted`。
8. Server 将 Deployment Condition 更新为 `Accepted`。若此落库失败，使用稳定机器码 `AIFAR_RUNTIME_CONTROL_PLANE_REPAIR_REQUIRED`，后续修复读取 Agent generation 完成对账，不反向重启容器。

Agent 接收成功只证明期望状态已可靠持久化并进入队列，不证明容器 Ready。

## 8. 锁与并发边界

### 8.1 控制面锁

Runtime 变更使用 `aifar_orchestration_locks` 作为分层权威锁，并把获取时机前移到任务启动前。冲突规则调整为：

- 服务操作请求 `(instanceId, serviceName)`：只与同服务锁或该实例的维护锁冲突。
- 实例共享维护请求 `(instanceId, "")`：与该实例任意活动服务锁或维护锁冲突。
- 不同服务锁彼此不冲突。

锁继续记录 actor、task ID、operation、TTL 和审计信息，并增加与任务一致的 heartbeat/释放恢复。Runtime 不再同时维护一套语义不同的实例级 API operation lock 和一套服务层锁。

### 8.2 Agent 锁

Agent 使用两层锁：

- 实例维护 `RWMutex`：普通服务调和持有读锁；共享维护持有写锁。
- `(instanceId, serviceName)` 互斥锁或单消费者队列：同服务严格串行，不同服务并行。

新 generation 只取消同服务正在执行的旧 generation；不得取消其他服务。周期 resync 与 Docker Event 也只能入队受影响服务。

### 8.3 共享维护操作

以下操作仍使用实例级独占维护锁：

- Agent 升级或替换；
- Docker network、共享 Ingress 或共享端口修改；
- 实例卸载或 Agent 卸载；
- 新旧编排模型迁移；
- 会同时改变全部服务公共 env 的配置更新。

`restart-all`、批量下线、批量更新不是共享维护；它们是多个独立服务意图的聚合提交。

## 9. 安装生命周期

### 9.1 安装成功边界

安装流程分为：

1. 校验包结构、manifest、SHA-256、服务选择和参数。
2. 校验目标服务器、磁盘、Docker network、共享目录和 Agent能力。
3. 上传并解压发布资源与环境文件。
4. 创建或更新 `app_instances.status=installing`。
5. 在 SQLite 为每个选中服务保存初始 generation 和 Manifest。
6. 并发提交每服务 Manifest 到 Agent。
7. Agent逐个原子持久化并返回 Accepted。
8. 全部选中 Manifest 被接受后，将实例更新为 `installed`，安装任务成功。

安装任务不等待容器启动或健康检查。初始 `aifar_replicasets.ready_pods`、Pod和 Endpoint 必须从真实观察值 `0` 开始，禁止按 desired replicas 预先伪造 Ready 状态。

### 9.2 仍属于安装失败的错误

- 包不存在、结构非法或 SHA-256 不匹配；
- 上传、解压或必要目录创建失败；
- 实例基础配置无法持久化；
- 服务 Manifest 无法写入 SQLite；
- Agent拒绝或无法原子持久化服务 Manifest；
- Agent能力门禁不满足。

上述错误意味着期望对象尚未可靠建立。安装重试使用相同 install attempt/idempotency key 对已接受 generation 做幂等确认，只补齐未接受服务；已接受服务不回滚。

### 9.3 不属于安装失败的错误

- 容器创建或启动失败；
- 业务进程退出、OOM或 CrashLoop；
- readiness/health check 失败；
- 业务容器因 Nacos或其他业务配置不可用而无法 Ready；
- Agent代理的 Nacos注册/心跳失败。

这些错误只更新对应 Deployment Condition。应用实例保持 `installed`。

## 10. 每服务控制器

每个服务控制器执行以下循环：

1. 读取最新持久 Manifest与generation。
2. 观察该服务的 Docker Pod、revision、spec hash、健康与 Endpoint。
3. 比较 desired 与 observed。
4. 只对目标服务执行创建、替换、扩缩容、下线、残留清理或重启。
5. 更新内存状态、Pod与 Endpoint 快照。
6. 设置 `observedGeneration` 和 Condition。
7. 若执行期间收到更高 generation，结束旧循环并立即处理新 generation。

Agent不读取业务依赖图，不等待其他服务 Ready。一个服务的 progress deadline、重试或诊断命令不占用其他服务队列。

Agent启动恢复逐个读取 `deployments/*.json`。某个文件损坏时只为该服务记录 `SpecRejected`；其他服务继续加载和调和。周期 resync逐服务执行，不因首个错误中断整个实例。

## 11. Endpoint 与服务发现控制器

Endpoint 控制器在单服务容器 Ready 状态变化后立即刷新该服务 Endpoint：

- Ready Endpoint 从 0 变为正数：服务发现控制器异步注册 Agent宿主机代理。
- Ready Endpoint 仍为正数但集合变化：更新本机代理缓存，不依赖重新注册。
- Ready Endpoint 变为 0：异步注销该服务 Agent代理。

Nacos注册、注销和心跳由独立工作队列处理，失败按独立退避策略重试。Agent代理同步错误可写日志和独立告警，但不得：

- 阻止 Deployment Accepted；
- 把 Deployment改成 Degraded；
- 取消其他服务调和；
- 写入 Runtime API/UI 的 Nacos状态字段。

业务容器自身因 Nacos配置不可用而健康失败时，仍可通过该容器的 readiness 结果把对应服务标记为 Degraded。这与 Agent代理注册状态是两个边界。

## 12. 全部重启与批量操作

### 12.1 全部重启

`restart-all` 调整为聚合入口：

1. Server列出 `desiredReplicas > 0` 的 Deployment。
2. 为每个服务独立增加 `restartGeneration` 和 generation。
3. 并发提交每个服务 Manifest。
4. 每个 Agent接收结果写入独立 task target/step。
5. 所有目标意图被 Agent接受后，父任务成功并显示“所有在线服务的独立重启请求已接受”。

Agent不再先删除全部 Pod。每个服务独立执行现有 RollingUpdate 策略，谁先 Ready，谁先更新 Endpoint和服务发现。后续容器失败只改变该服务 Condition，不反向修改已成功的重启提交任务。

### 12.2 批量下线与批量更新

批量操作同样拆成独立服务 generation。父任务只聚合目标结果，不提供跨服务回滚或全有全无保证。某个服务接收失败时，父任务失败并明确失败 target；其他已接受服务继续收敛。

## 13. Condition、重试和诊断

### 13.1 Condition 结构

Condition至少包含：

- type；
- status；
- reason；
- message；
- generation；
- lastTransitionTime。

稳定 phase 从 Condition与副本数派生：

- `Accepted`：Manifest已持久化；
- `Progressing`：正在创建、替换或等待健康；
- `Available`：ready replicas达到desired；
- `Degraded`：运行态异常；
- `Offline`：desired replicas为0且无运行Pod。

稳定 Reason包括：

- `ImageMissing`；
- `ContainerCreateFailed`；
- `ContainerStartFailed`；
- `ReadinessFailed`；
- `CrashLoopBackOff`；
- `NodeResourcePressure`；
- `SpecRejected`；
- `AgentUnavailable`。

机器码使用英文，message走后端中英文 i18n 并在日志中脱敏。

### 13.2 自动重试

容器运行类错误按 `1s、2s、4s、8s、16s、30s、60s` 退避，最大间隔60秒；服务稳定Available后清零失败计数。不可通过重试修复的SpecRejected等待新 generation。NodeResourcePressure暂停高频重试并产生告警。Nacos代理同步使用独立退避器。

### 13.3 人工修复

- 立即重新调和：不增加generation，只清除该服务退避并立即入队。
- 重新传包：为目标服务创建新revision和generation。
- 更新配置并重启：更新服务配置摘要与generation。
- 重启服务：增加`restartGeneration`。
- 下线：设置`desiredReplicas=0`。
- 初始化诊断：展示容器状态、退出码、OOM、健康日志和最近Condition，不开放自由Shell。

## 14. API、任务与审计

面板对外API继续使用`/api/v2`、apps.manage权限、worker任务和审计。建议新增或收敛为以下类型化服务操作：

- `PUT /api/v2/apps/instances/{id}/runtime/deployments/{service}`；
- `POST /api/v2/apps/instances/{id}/runtime/deployments/{service}/reconcile`；
- `POST /api/v2/apps/instances/{id}/runtime/deployments/{service}/restart`；
- 现有scale-out、scale-in、offline路由可保留兼容并调用统一Deployment mutation service。

所有写接口仍返回task ID。任务提交成功表示Agent已接受期望状态；Runtime页面展示异步容器状态。任务target使用`instanceId:serviceName`，批量任务必须保留每服务target结果。

Agent本地控制API只监听`127.0.0.1`，由受控CLI/SSH适配层调用。建议Agent本地接口为：

- `PUT /runtime/instances/{instance}/deployments/{service}`；
- `POST /runtime/instances/{instance}/deployments/{service}/reconcile`；
- `GET /runtime/instances/{instance}/deployments/{service}`。

请求必须校验URL身份与Manifest身份一致，不接受自由路径或自由命令。

## 15. Runtime UI

### 15.1 页面状态

Runtime顶部展示Available、Progressing、Degraded、Offline服务数量。每个服务行展示：

- desired/current/ready replicas；
- revision；
- generation/observed generation；
- phase与Condition reason；
- 最近状态变化时间；
- 单服务恢复操作。

AIFAR应用实例保持“已安装”；运行异常在Runtime服务列表中表达。

### 15.2 删除Nacos状态

删除Runtime API与前端类型中的：

- `nacosRegistered`；
- `nacosReady`；
- `lastNacosHeartbeatAt`；
- `lastNacosError`；
- Nacos状态格式化函数、列、标签和相关测试。

不删除独立Nacos应用、Nacos配置发布/回滚或业务连接配置能力。

### 15.3 操作门禁

删除“Runtime整体Degraded就禁用所有变更”的全局规则。仅在以下情况禁用目标服务操作：

- 同服务已有活动变更；
- 实例正在执行共享维护；
- Agent不可用；
- Server/Agent能力或完整性门禁失败；
- 当前用户无权限。

其他服务Degraded不影响当前服务按钮。“全部重启”文案必须说明它提交独立滚动重启意图，不会先停止全部服务。

## 16. 兼容迁移

### 16.1 能力门禁

新Agent发布以下feature：

- `service-manifest-v1`；
- `service-generation-v1`；
- `per-service-reconcile`；
- `per-service-restart`；
- `service-conditions-v1`。

Agent先升级为可同时读取旧全量spec和新每服务Manifest的兼容版本。Server只有在全部feature存在时才允许迁移或创建新模型实例。

### 16.2 迁移流程

1. 获取实例级独占维护锁。
2. 读取旧`runtime-spec.json`、`aifar_deployments`、`app_instances.metadata`和Agent状态。
3. 对账服务、副本数、revision、镜像、端口和env路径；存在无法解释的分歧时fail-closed停止。
4. 以控制面明确desired state为基础生成`instance.json`和每服务Manifest，保留副本数0。
5. 在相邻临时目录写入全部文件并完整校验。
6. Agent原子切换状态目录和`orchestrationModel=agent-service-controller-v1`。
7. Agent按现有容器label接管观察状态；spec无变化时不得重启容器。
8. Server读回全部generation与spec hash后更新SQLite和app metadata。
9. 旧spec归档为迁移备份，并停止作为写入源。

### 16.3 防止双写与恢复

新模型启用后，旧全量`reconcile-runtime`写接口返回`409 LEGACY_RUNTIME_SPEC_DISABLED`。迁移在Agent切换前失败时，旧模型继续有效。Agent已切换而Server落库失败时，重试迁移必须识别已切换状态并幂等完成控制面对账。

一旦新模型接收过更高的服务generation，不自动降级回旧模型，因为旧全量spec已经陈旧。此时只能向前修复控制面或Agent状态。

## 17. 错误处理与安全

- spec、env、凭据和完整Docker命令不得写入任务日志或审计。
- Manifest路径、env文件与volume源必须限定在允许的Runtime目录或显式允许路径。
- 服务名、instance ID、generation和revision必须规范化并校验，防止路径穿越和命令注入。
- Agent持久化采用同文件系统临时文件、fsync和原子rename。
- SQLite更新使用集中Store事务和CAS，不在handler或脚本中散落写表。
- Agent响应丢失时，Server读回该服务generation与spec hash；一致则按Accepted完成，不一致则失败。
- Agent Accepted后的控制面修复不得通过重启或恢复旧spec做反向补偿。
- 所有服务操作继续写任务、target、step和审计；可见错误文案进入中英文i18n。

## 18. 测试策略

所有生产代码先写失败回归，再做最小实现。真实SSH、Docker和Nacos继续使用fake remote/runner/server。

### 18.1 Store

- schema前向迁移和旧数据默认值；
- generation CAS与陈旧写拒绝；
- observed generation约束；
- 更新一个服务不修改其他Deployment；
-实例维护锁、同服务锁和跨服务并发冲突矩阵；
-Agent Accepted后控制面落库失败的幂等修复。

### 18.2 Agent

- 服务A阻塞或失败时服务B完成；
- 同服务新generation取消旧generation；
- 陈旧/同generation不同hash请求返回409；
- 原子文件持久化失败不替换正式Manifest；
- 一个Manifest损坏不阻塞其他服务加载；
- Docker Event只入队对应服务；
- 周期resync不因一个服务失败停止其他服务；
- Endpoint首次Ready立即触发服务发现队列；
- Nacos代理同步失败不改变Deployment Condition；
- Restart All不执行stop-all；
- Agent重启后恢复每服务状态与退避。

### 18.3 应用服务、HTTP与Worker

- 不同服务任务可同时Accepted；
- 同服务第二任务在创建前返回409和owner task ID；
- 共享维护与全部服务任务冲突；
- 安装任务在全部Manifest Accepted后成功，不等待容器Ready；
- 容器初始化失败保持实例installed并只更新服务Degraded；
- 批量操作记录每服务target结果；
- Agent响应丢失通过generation/hash readback确认；
- Runtime状态响应不再包含Nacos字段。

### 18.4 前端

- 删除Nacos状态显示与类型；
- Runtime整体Degraded不禁用健康服务操作；
- 同服务锁、共享维护、Agent门禁仍正确禁用；
-安装状态与服务运行状态分开展示；
- 全部重启确认和结果文案使用独立重启语义；
- 服务Condition、generation和诊断入口正确渲染；
- 中英文文案完整。

### 18.5 迁移

- 保留全部revision、副本数和Offline状态；
- 正常迁移不重启容器；
- 临时文件、校验、Agent切换和Server落库任一步失败不产生双权威；
- Agent已切换、Server未落库时可幂等完成；
- 新模型启用后旧全量写被拒绝；
- 已产生新generation后禁止自动降级。

### 18.6 验证命令

- 受影响Go包测试；
- `pnpm test`；
- `pnpm test:web`；
- `pnpm test:scripts`；
- `pnpm web:build`；
- `pnpm backend:build`；
- `git diff --check`；
- 收口前运行`pnpm test:local`。

## 19. 验收标准

1. `file`连续初始化失败5分钟时，`permission`仍能在自身progress deadline内启动、Ready并完成Agent代理注册。
2. 同时下线`file`并启动`permission`，两个请求均Accepted并独立完成。
3. 同时修改同一个`permission`时，第二个请求明确返回409。
4. 初始化安装在全部Manifest被Agent接受后显示installed；失败容器只显示对应服务Degraded。
5. 全部重启不调用stop-all，不出现所有服务被人为同时停止的阶段。
6. 单服务重新传包、修改配置、扩缩容、下线或回滚不改变其他服务generation、revision、spec文件或容器。
7. Agent重启后，一个损坏或错误服务不阻塞其他服务恢复。
8. 周期resync和Docker Event不会把已下线服务从旧全量状态拉起。
9. Runtime API/UI不再暴露Nacos状态；Agent代理注册、注销和心跳仍工作。
10. Runtime整体Degraded不禁用无关服务操作。
11. 正常旧模型迁移保留全部desired state且不重启业务容器。
12. 日志、任务和审计中不泄露env、密码、token、完整spec或自由命令。

## 20. 实施分段

后续实施计划按以下顺序拆分，避免同时改动全部路径：

1. Store schema、generation/CAS、Condition与分层锁。
2. Agent每服务Manifest协议、持久化、队列和状态。
3. Endpoint事件与独立Nacos服务发现控制器。
4. Server单服务mutation service、Agent接收边界和控制面修复。
5. 初始化安装语义与真实observed state写入。
6. scale、offline、更新、回滚、配置更新和restart-all统一迁移。
7. Runtime API/UI删除Nacos状态并改用服务级门禁。
8. 旧模型无重启迁移、防双写与兼容清理。
9. 完整测试、构建、打包验证和受控现场迁移验收。
