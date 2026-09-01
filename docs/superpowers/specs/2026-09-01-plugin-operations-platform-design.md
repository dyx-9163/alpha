# 插件化部署运维平台标准架构与演进规范

**文档版本：** V1.0  
**日期：** 2026-09-01  
**状态：** 架构基线  
**适用范围：** 面向已准备服务器、Docker/Compose、Kubernetes 及后续云资源的私有化部署运维平台

## 1. 执行摘要

本平台定位为“部署与运维微内核”，不内置 MySQL、Redis、Kubernetes 等具体产品知识。平台提供插件运行所需的公共能力；服务插件以声明配置、工作流和独立代码 Runner 的形式，提供连接、部署、升级、状态、备份、恢复、页面等产品能力。

第一版聚焦于验证插件体系的完整闭环：官方可信插件可注册、启停并以独立子进程运行；插件通过版本化 gRPC 协议注册配置、动作和基础过程；Panel 负责配置管理、Runner 生命周期、任务与步骤状态、SSH/SFTP 等公共能力及基础页面。第一版不建设多用户、RBAC、审计中心、集中日志、监控告警、第三方插件沙箱和插件市场。

核心原则如下：

1. Panel 管治理与可靠执行，插件管具体服务语义。
2. 插件代码不得加载到 Panel 主进程。
3. 工作流声明顺序、条件和验收；Runner 实现类型化动作。
4. 任务步骤、错误和检查点属于工作流状态，不属于可省略的日志产品。
5. 第一版只信任官方插件，但协议预留第三方隔离和能力代理。
6. 状态、备份和恢复均由插件定义契约，Panel 只提供通用基础设施。

## 2. 产品目标与非目标

### 2.1 产品目标

- 管理已准备好的 Linux 服务器以及后续接入的 Docker、Kubernetes 和云资源。
- 允许在不修改 Panel 核心代码的情况下增加一种新服务。
- 支持插件声明配置表单、动作、工作流、状态采集和后续页面入口。
- 支持离线私有化交付，同时为联网同步和插件市场预留能力。
- 从几十台服务器起步，架构可演进至数千节点和多区域 Worker。
- 所有长时间变更均通过可持久化任务执行，不由 HTTP 请求同步阻塞完成。

### 2.2 第一版非目标

- 不提供多租户、多用户、RBAC、SSO、审批流和审计中心。
- 不提供集中式业务日志、日志全文检索和监控告警平台。
- 不允许客户或第三方上传不可信代码插件。
- 不提供容器级插件沙箱、插件市场、在线自动更新。
- 不提供可视化拖拽工作流设计器。
- 不追求首版覆盖所有服务类型；只验证插件机制和一个参考插件闭环。

## 3. 总体架构

平台采用微内核与全栈插件架构。

```text
浏览器
  |
  v
Panel UI Shell
  |
  v
Panel API / 微内核
  +-- 插件注册与版本
  +-- 配置与 Schema
  +-- 工作流与任务
  +-- Runner Runtime
  +-- SSH / SFTP / HTTP 公共能力
  +-- Secret 与插件数据空间
  +-- 状态快照与事件接口
  |
  +-- 本机 gRPC --> Kubernetes Plugin Runner
  +-- 本机 gRPC --> Docker Plugin Runner
  +-- 本机 gRPC --> MySQL Plugin Runner
  +-- 本机 gRPC --> 其他官方 Plugin Runner
                         |
                         +--> SSH / Agent / 产品 API / Kubernetes API
```

### 3.1 Panel 微内核职责

- 插件包安装、校验、注册、启用、禁用、升级和卸载。
- Manifest、JSON Schema、Workflow 的解析、版本管理和兼容性检查。
- Runner 子进程启动、停止、健康检查、崩溃重启和通信。
- 工作流解释、任务调度、步骤持久化、重试、超时和检查点。
- SSH、SFTP、HTTP、TCP、模板和制品等通用能力。
- 插件配置、实例索引、任务状态、插件独立数据空间。
- UI Shell、公共表单、任务页面和后续插件页面容器。

### 3.2 插件职责

- 声明插件身份、版本、兼容范围和所需能力。
- 定义配置 Schema、默认配置和操作输入。
- 注册类型化 Action 及其输入输出 Schema。
- 提供安装、检查、升级、备份、恢复等工作流。
- 实现产品 API 调用、复杂判断、状态归一化和验收逻辑。
- 后续贡献菜单、路由、页面、指标、告警和推荐动作。

## 4. 第一版能力基线

### 4.1 插件包管理

第一版支持本地上传或指定目录导入插件包，插件包为压缩归档，必须包含 Manifest、摘要清单以及当前平台对应的 Runner 二进制。

```text
sample-plugin-1.0.0/
  manifest.yaml
  checksums.txt
  schemas/
    plugin-config.schema.json
    install.schema.json
  workflows/
    install.yaml
    check.yaml
  runner/
    linux-amd64/plugin-runner
  templates/
  artifacts/
  i18n/
```

插件状态至少包括：`installed`、`enabled`、`disabled`、`failed`、`upgrading`。

### 4.2 公共配置

配置分为五层，后层覆盖前层：

1. Panel 默认配置。
2. 插件默认配置。
3. 环境配置。
4. 服务实例配置。
5. 本次动作输入。

Secret 不进入普通配置合并，只保存 `credentialRef` 或 `secretRef`。第一版即使不做登录，也必须避免插件配置和任务输出直接保存明文密码、Token 和私钥。

### 4.3 配置 Schema

采用 JSON Schema 2020-12 描述数据类型、必填项、枚举、范围和条件。Panel 允许使用 `x-ui-*` 扩展描述服务器选择器、文件选择器、分组、顺序和帮助信息。前端按 Schema 生成公共配置页和动作表单，后端必须使用同一 Schema 二次校验。

### 4.4 独立代码 Runner

第一版仅支持官方可信 Runner，采用 Panel 管理的独立子进程：

- Panel 为每个启用的插件版本启动独立 Runner。
- Panel 分配本地回环 TCP 端口或 Unix Domain Socket。
- Panel 生成一次性启动令牌，Runner 完成握手后失效。
- Runner 通过 gRPC 暴露元数据、动作执行、取消和健康检查。
- Runner 标准输出和错误输出只用于本机诊断，不作为业务协议。
- Runner 崩溃不得导致 Panel 主进程退出。
- Runner 连续崩溃达到阈值后进入 `failed`，停止自动重启。

第一版不承诺抵御恶意官方插件。未来第三方插件必须切换为容器或其他强隔离 Runtime。

### 4.5 基础工作流

第一版支持以下控制结构：

- `sequence`：顺序执行。
- `parallel`：并行执行。
- `condition`：结构化条件判断。
- `retry`：按策略重试。
- `timeout`：步骤超时。
- `foreach`：对有限集合循环，可视范围决定是否首版启用。
- 子工作流：引用同一插件已注册的工作流。
- 失败策略：停止、重试或进入人工处理。

第一版不提供任意表达式脚本，变量只允许引用 `config`、`input`、`target` 和已完成步骤输出。

### 4.6 公共原子能力

Panel 第一版提供以下能力供 Runner 或声明式工作流调用：

- `ssh.execute`：在已登记服务器执行受控命令或脚本。
- `sftp.upload`、`sftp.download`：文件传输。
- `http.request`：HTTP/HTTPS 请求。
- `tcp.probe`：TCP 端口探测。
- `template.render`：受限模板渲染。
- `artifact.open`：读取插件制品。
- `core.wait`：等待。
- `plugin.storage`：插件命名空间数据读写。

所有公共能力必须使用结构化请求和结果，不把自由命令字符串当成跨插件公共 API。

### 4.7 最小任务状态

第一版不建设日志产品，但必须持久化：

- 任务 ID、插件和工作流版本。
- 输入快照和目标快照。
- 当前步骤、步骤状态和尝试次数。
- 开始/结束时间、错误码和错误摘要。
- 结构化输出和检查点。

状态至少包含：`pending`、`running`、`success`、`failed`、`cancelled`、`needs_attention`。

### 4.8 第一版基础页面

- 插件列表与详情。
- 插件上传、启用、禁用和删除。
- 插件公共配置页面。
- 插件工作流列表与动态输入表单。
- 服务器和 SSH 凭证基础配置。
- 任务列表与任务步骤详情。
- 系统设置。

第一版插件不提供自定义页面，页面由 Panel 根据贡献元数据和 Schema 渲染。

## 5. 插件 Manifest 标准

```yaml
apiVersion: panel.io/plugin/v1
kind: Plugin

metadata:
  id: io.example.kubernetes
  name: Kubernetes
  version: 1.0.0

requires:
  panelVersion: ">=0.1.0"
  protocolVersion: "1"
  capabilities:
    - ssh.execute
    - sftp.upload
    - http.request
    - plugin.storage

runner:
  runtime: process
  protocol: grpc
  platforms:
    linux-amd64: runner/linux-amd64/plugin-runner
  startTimeout: 15s
  healthInterval: 30s
  stopTimeout: 10s

configuration:
  schema: schemas/plugin-config.schema.json

actions:
  - id: cluster.connect
    inputSchema: schemas/connect-input.schema.json
    outputSchema: schemas/connect-output.schema.json
    riskLevel: medium

workflows:
  - id: cluster.register
    version: 1
    file: workflows/register.yaml
```

规范要求：插件 ID 全局唯一；版本使用语义化版本；Manifest 与协议均版本化；未知必需字段导致拒绝安装，未知可选字段允许忽略；插件任务固定使用启动时解析出的插件与工作流版本。

## 6. Runner 生命周期与 gRPC 协议

### 6.1 生命周期

```text
安装插件
  -> 校验包
  -> 解压至版本目录
  -> 注册元数据
  -> 启动 Runner
  -> 握手
  -> 健康检查
  -> 启用贡献点
```

禁用时停止接收新任务，等待当前任务结束或按策略取消，然后停止 Runner。升级时并行准备新版本 Runner，新任务切换到新版本；旧任务继续使用旧版本直至完成，之后回收旧 Runner 和制品。

### 6.2 第一版 RPC

- `Handshake`：协议版本、插件 ID、插件版本、启动令牌。
- `GetMetadata`：Action、Schema 和能力元数据。
- `ValidateConfig`：执行插件级语义校验。
- `ExecuteAction`：流式返回 ActionEvent。
- `CancelAction`：尽力取消动作。
- `Health`：Runner 健康检查。
- `Shutdown`：优雅停止。

请求必须携带 `task_id`、`step_id`、插件版本、截止时间和关联 ID。Runner 返回结构化事件：`started`、`progress`、`output`、`warning`、`completed`、`failed`。

## 7. 工作流标准

工作流由插件提供，但由 Panel 解释和持久化。示例：

```yaml
apiVersion: panel.io/workflow/v1
kind: Workflow

metadata:
  id: service.install
  version: 1

inputSchema: schemas/install.schema.json

locking:
  scope: "target:${input.serverId}"
  mode: exclusive

steps:
  - id: preflight
    action: plugin.preflight
    timeout: 2m

  - id: prepare
    parallel:
      - id: upload
        capability: sftp.upload
      - id: render
        capability: template.render

  - id: install
    action: plugin.install
    retry:
      attempts: 2

  - id: verify
    action: plugin.verify
    timeout: 5m
```

Action 必须声明是否只读、是否可重试、是否可取消、是否可恢复以及是否可逆。不可逆动作失败时默认进入 `needs_attention`，不得伪装成自动回滚。

## 8. 配置与数据模型

核心数据实体：

- `plugins`：插件身份与当前状态。
- `plugin_versions`：每个已安装版本、Manifest 和文件摘要。
- `plugin_configs`：按插件、环境或实例保存配置。
- `plugin_actions`：Action 元数据及 Schema。
- `plugin_workflows`：规范化工作流定义。
- `plugin_runner_instances`：Runner 进程、端点和健康状态。
- `servers`：目标服务器基础信息。
- `credentials`：加密凭证引用。
- `tasks`：任务快照与最终状态。
- `task_steps`：步骤状态、尝试、输入输出和检查点。
- `plugin_objects`：插件独立命名空间的通用 JSON 对象。

第一版采用 PostgreSQL。Manifest、Schema、配置和步骤输入输出使用 JSONB；稳定查询字段使用独立列。任务领取使用事务与行锁，不依赖进程内队列。

## 9. 状态收集能力

状态收集在后续阶段启用，但协议从第一版预留。Panel 负责调度、接收、保存、过期判断和推送；插件负责采集、归一化和健康评价。

状态分为：

- 连接状态：`reachable`、`unreachable`、`unauthorized`、`stale`。
- 运行状态：`running`、`stopped`、`starting`、`failed`、`unknown`。
- 拓扑状态：插件自定义详情，统一聚合为 `healthy`、`degraded`、`unhealthy`、`unknown`。
- 业务健康：插件业务探针的评价结果。
- 操作状态：`installing`、`upgrading`、`backing_up`、`restoring` 等。

采集方式组合事件推送、周期采集、按需刷新和外部监控告警。最后一次健康结果过期后显示 `stale`，不能继续永久显示健康，也不能直接推断服务停止。

## 10. 备份与恢复能力

备份恢复由服务插件提供 Backup/Restore Contract。Panel 管仓库、调度、加密、校验、保留和审计；插件管数据一致性、拓扑、恢复顺序和业务验收。

完整恢复点至少描述：

- 业务数据和增量链。
- 服务配置和配置版本。
- Secret 引用及独立恢复路径。
- 服务拓扑和目标关系。
- 软件、镜像、Chart 与插件版本。
- 外部依赖及恢复顺序。
- 一致性坐标、恢复时间点和兼容范围。
- 产物摘要、签名和恢复验证结果。

备份状态分为产物完成与可恢复性验证。只有隔离恢复测试和业务验收通过，才能标记为“已验证可恢复”。恢复默认优先恢复到新实例，原地覆盖和替换恢复属于高风险动作。

## 11. Kubernetes 插件能力

Kubernetes 连接、资源模型、状态、升级、备份、恢复和页面均由 Kubernetes 插件提供，而非 Panel 内核。

插件后端 Runner 可使用 Kubernetes 原生 SDK，实现：

- kubeconfig/Token/证书连接。
- 集群版本、节点和 Workload 查询。
- Watch 资源事件。
- Apply、Scale、Restart、日志等动作。
- Helm 和 Operator 集成。
- 废弃 API扫描和版本兼容检查。
- 控制面与节点池升级工作流。
- 资源及持久卷备份恢复。

大版本跨度必须由版本兼容矩阵计算合法路径。升级流程声明化，具体 API 调用由类型化 Action 实现。控制面或 kubelet 不可回退时，工作流只能暂停、停止后续批次或迁移到新集群，不能承诺虚假的自动降级。

## 12. 后续全栈页面插件

后续插件可以贡献菜单、路由、凭证类型、资源类型、页面和设计组件。

第三方页面默认运行在 sandbox iframe，通过 Host SDK 调用导航、任务、通知、主题、国际化和插件 API。官方可信插件可以选择 Web Components 或更深集成，但不得直接读取 Panel Token、主数据库或长期 Secret。

插件 API 统一经过：

```text
/api/plugins/{pluginId}/v1/*
```

Panel Gateway 负责认证、插件状态、权限、限流、审计和路由，插件 Runner 不直接暴露外部端口。

## 13. 安全演进

### 13.1 第一版

- 仅官方可信插件。
- 无登录模式仅允许本机或明确受控网络使用。
- 凭证加密保存，前端不回显，任务输出脱敏。
- Runner 独立子进程，不加载到 Panel 主进程。
- 预留 `ActorContext`、`AuthorizationService`、`AuditSink` 接口，使用 Noop/AllowAll 实现。

### 13.2 产品化阶段

- JWT/OIDC、用户、角色、资源范围和审批。
- 插件发布者签名、SBOM、漏洞扫描和撤销列表。
- 短期 Secret 租约和能力代理。
- 容器沙箱、只读文件系统、资源限制和网络策略。
- 插件 API审计、任务审计、数据保留和导出。

HTTP 使用 Middleware，gRPC 使用 Interceptor，实现认证、授权、审计和追踪等横切能力；业务 Handler 和插件 Action 不直接实现这些规则。

## 14. 技术选型

### 14.1 第一版推荐栈

| 层次 | 选择 | 说明 |
|---|---|---|
| Panel 后端 | Go 模块化单体 | 单二进制、并发和私有化交付友好 |
| HTTP | Chi / `net/http` | 轻量、中间件清晰 |
| 插件协议 | Protocol Buffers + gRPC | 版本化、跨语言、流式事件 |
| 数据库 | PostgreSQL | JSONB、事务、任务锁、后续横向扩展 |
| 前端 | Vue 3 + TypeScript + Vite | UI Shell 与动态 Schema 表单 |
| 配置契约 | JSON Schema 2020-12 | 前后端统一校验 |
| 插件声明 | YAML，注册后规范化为 JSON | 便于作者阅读，运行时保持稳定 |
| 状态推送 | SSE | 任务和状态事件单向推送 |
| SSH/SFTP | Go SSH/SFTP 库 | 公共远程能力 |
| 模板 | Go `text/template` 的受限封装 | 配置和脚本模板 |
| 制品摘要 | SHA-256 | 插件与制品完整性 |

第一版不引入 Redis、Kafka、Temporal、Elasticsearch、Kubernetes 微服务部署或微前端框架。

### 14.2 扩展阶段

- 多 Worker 与事件总线：优先 NATS JetStream；已有企业 Kafka 时可适配 Kafka。
- 指标与追踪：OpenTelemetry、Prometheus。
- 日志：Loki 或现有企业日志平台。
- Secret：外接 Vault 或增强内置信封加密。
- 插件运行：进程 Runtime 扩展为容器 Runtime；需要更强隔离时评估 WASM。
- 大规模制品：S3兼容对象存储和 OCI Registry。

## 15. API 与错误规范

核心 API 示例：

```text
POST   /api/v1/plugins/install
GET    /api/v1/plugins
POST   /api/v1/plugins/{id}/enable
POST   /api/v1/plugins/{id}/disable
PUT    /api/v1/plugins/{id}/config
GET    /api/v1/plugins/{id}/workflows
POST   /api/v1/plugins/{id}/workflows/{workflowId}/runs
GET    /api/v1/tasks/{taskId}
GET    /api/v1/tasks/{taskId}/events
POST   /api/v1/tasks/{taskId}/cancel
```

错误统一返回：

```json
{
  "code": "PLUGIN_PROTOCOL_INCOMPATIBLE",
  "message": "插件协议版本与当前 Panel 不兼容",
  "details": {}
}
```

稳定机器码使用英文，展示层负责国际化。

## 16. 第一版实施顺序

1. 定义 Manifest、Action、Workflow 和 RPC 的 V1 契约及兼容规则。
2. 建立 Go 模块化单体、PostgreSQL迁移和插件目录约束。
3. 实现插件包校验、安装、启停和版本目录。
4. 实现 Runner Runtime、握手、健康检查、重启和 gRPC流式事件。
5. 实现配置 Schema、公共配置页和配置持久化。
6. 实现任务、步骤、检查点和最小工作流解释器。
7. 实现 SSH/SFTP/HTTP等公共能力代理。
8. 实现一个官方参考插件，走通配置、Runner、工作流和任务闭环。
9. 完成进程崩溃、Panel 重启、插件升级和失败恢复测试。
10. 固化 V1协议，作为后续 Kubernetes、备份和页面插件的基础。

## 17. 第一版验收标准

- 不修改 Panel 核心代码即可安装一个新的官方插件包。
- 插件 Runner 独立运行，崩溃不会导致 Panel 主进程退出。
- Manifest、Schema、Workflow 和 RPC版本不兼容时 fail closed。
- 插件配置可生成表单、校验并持久化。
- 插件可以注册至少两个 Action 和一个多步骤工作流。
- 任务可异步执行、取消并显示步骤状态和结构化错误。
- Panel 重启后不会把未完成任务误标为成功，并可按策略恢复或转人工处理。
- 插件升级后，运行中的旧任务继续绑定旧插件和工作流版本。
- Secret 不以明文出现在插件配置、任务输出和前端响应中。
- 参考插件能够完成一次真实但低风险的远程操作闭环。

## 18. 能力路线图

### 阶段 1：插件内核 MVP

官方可信独立 Runner、Manifest、Schema、基础工作流、任务状态、SSH/SFTP、基础页面和参考插件。

### 阶段 2：服务生命周期

服务实例模型、状态采集、健康评价、持续调度、配置漂移、安装/升级/卸载标准动作和 Docker/Kubernetes 插件。

### 阶段 3：备份与恢复

备份仓库、恢复点目录、保留策略、增量链、插件 Backup/Restore Contract、隔离恢复演练和业务验收。

### 阶段 4：全栈插件与企业安全

iframe Host SDK、插件页面、登录、RBAC、审批、审计、签名、Secret 租约、容器沙箱和第三方插件准入。

### 阶段 5：规模化与生态

多区域 Worker、Agent Gateway、NATS/Kafka 事件总线、对象存储、在线/离线插件仓库、插件市场、监控日志集成和多租户。

## 19. 关键决策记录

1. Panel 不内置任何具体服务能力。
2. 第一版即支持官方可信独立代码插件。
3. 插件后端使用独立子进程，不使用进程内动态库。
4. 第一版插件页面由 Panel 根据 Schema 生成，自定义页面后置。
5. 工作流配置化，原子动作类型化，不允许配置直接拼接自由 Shell 作为公共协议。
6. 第一版不建设登录、权限、审计和集中日志产品，但预留横切接口。
7. 任务状态和检查点不可删除，因为它们属于可靠执行模型。
8. 第一版使用 PostgreSQL持久化任务，不引入专用消息队列。
9. 第三方插件上线前必须补齐签名、能力代理和强隔离 Runtime。

## 20. 结论

该架构以最小微内核承载插件治理和可靠执行，以独立 Runner 承载具体产品代码，以声明式 Schema 和工作流维持可审查、可版本化的插件契约。第一版应优先证明“插件可以安全启动、注册配置和动作、执行持久工作流并在故障后保持确定状态”，而不是追求服务数量。后续状态、备份、Kubernetes、页面、安全和规模化能力均沿既定贡献点扩展，不需要将具体服务逻辑重新写回 Panel 内核。
