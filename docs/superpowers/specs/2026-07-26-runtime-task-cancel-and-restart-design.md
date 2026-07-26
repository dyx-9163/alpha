# AIFAR Runtime 任务停止与全量配置重建设计

## 背景

当前任务后端已经提供 `POST /api/v2/tasks/{id}/cancel`，worker 也能取消等待中或运行中的任务，但任务中心前端没有停止入口。

AIFAR Runtime 的 Java 和 Web 容器在创建时通过 Docker `--env-file` 读取 `runtime/env` 下的环境文件。`docker restart`、`docker compose restart` 和 Docker daemon 重启都只复用容器创建时保存的环境变量，不会重新读取这些文件。现有 Runtime 页面也没有“重建全部在线服务并加载最新配置”的入口。

## 目标

1. 在任务中心为当前选中的单个 `pending` 或 `running` 任务提供停止动作。
2. 在当前选中的 AIFAR Runtime 实例上，为所有期望副本数大于 0 的服务提供全量滚动重建动作。
3. 新容器必须在创建时重新读取最新环境文件和挂载配置。
4. 任一服务重建失败时保留旧容器、停止后续服务，并留下完整任务与审计记录。

## 非目标

- 不提供批量停止任务。
- 不通过普通 `docker restart` 伪装成配置重新加载。
- 不启动期望副本数为 0 的已下线服务。
- 不把该动作作为镜像发布、端口调整、拓扑变更或扩缩容的替代入口；这些结构性变更仍走现有受控流程。
- 不在整批任务取消或失败后反向回滚已经成功完成的服务。

## 方案选择

### 方案一：普通容器重启

直接调用现有容器 restart API。实现简单，但 Docker 不会重新读取 `--env-file`，无法满足核心目标，因此不采用。

### 方案二：删除全部容器后调和

先删除当前实例所有业务容器，再由 agent 按期望态恢复。新容器能够加载配置，但会造成整实例中断，失败后也没有旧容器兜底，因此不采用。

### 方案三：agent 原生滚动重建

由 agent 按服务和副本顺序创建临时替代容器，健康后切换并删除旧容器。失败时旧容器继续运行，且可以立即停止后续重建。该方案复用现有 Runtime spec、容器创建、健康等待、端点刷新和容器替换能力，是本设计采用的方案。

## 任务中心停止动作

### 页面行为

- 在 `TaskLogPane` 工具栏增加红色描边“停止任务”按钮。
- 按钮只作用于当前正在查看的单个任务。
- 仅当当前任务状态为 `pending` 或 `running` 且用户拥有 `tasks.manage` 权限时启用。
- 点击后显示确认框；确认后调用现有 `POST /api/v2/tasks/{id}/cancel`。
- 接口接受取消请求后提示“停止请求已提交”，并禁用按钮，等待 SSE 或详情刷新把任务推进到终态。
- 如果接口返回 `cancelled=false`，提示任务已经结束或当前进程无法再接受取消，不显示成功消息。

### 取消语义

- 取消属于尽力取消，不承诺中断已经由远端系统接收且不支持上下文取消的外部操作。
- worker 已有的上下文取消、状态终结、操作锁释放和审计行为保持不变。
- 终态任务不能再次停止。

## Runtime 全量重建动作

### 页面入口

- 在 AIFAR Runtime 顶部工具栏、当前实例选择器旁增加警告样式按钮“全部重建并加载配置”。
- 使用现有 Runtime 管理禁用条件：必须选中非 legacy 实例、Runtime 为 ready、agent 为 running，并拥有 `apps.manage` 权限。
- 确认框展示当前实例、需要处理的在线服务数量，并明确：
  - 只处理期望副本数大于 0 的服务；
  - 已下线服务不会启动；
  - 任一服务失败后停止后续服务；
  - 新容器将重新读取环境文件。
- 提交成功后关联任务进度，并允许用户进入任务中心查看详情。

### 控制面 API

- 新增 `POST /api/v2/containers/aifar/runtime/restart-all`。
- 请求体只接收 `instanceId`；服务器范围继续由现有容器页查询参数和实例绑定关系确定。
- 权限使用 `apps.manage`。
- handler 只负责请求解析、实例与模块解析、任务创建、初始步骤和审计，不直接执行 SSH 或 Docker 命令。
- 任务类型使用稳定英文机器码 `aifar.runtime.restart-all`。
- 审计动作使用稳定英文机器码 `containers.aifar.runtime.restart-all`。

### 应用模块与服务层

- 在应用 registry 增加可选 Runtime 全量重建能力接口，请求包含实例、服务器、语言、操作者、任务 ID 和原因。
- AIFAR 模块实现该接口并委托 service。
- service 获取实例级 orchestration lock，避免与升级、回滚、扩缩容、配置应用或其他重建任务并发。
- service 验证非 legacy Runtime、安装根目录、Runtime spec 和 agent 能力后，通过 SSH 调用 agent 的滚动重建命令。
- 所有日志使用 backend i18n 的中英文文案，不记录环境变量值或 secret。

### agent 能力

- agent 增加全量滚动重建入口；CLI 通过本机 agent HTTP API提交 Runtime spec，实际操作由正在运行的 agent Manager 完成。
- Manager 复用现有 `reconcileMu`，避免周期调和、Docker event 调和与手工重建并发修改同一实例。
- 重建按 Runtime spec 中的 Deployment 顺序串行执行；期望副本数为 0 的 Deployment 直接跳过。
- 每个 Deployment 内按副本序号逐个执行：
  1. 使用当前 Runtime spec 和磁盘上最新 env 文件创建临时容器；
  2. 等待临时容器通过既有健康门禁；
  3. 刷新服务 endpoint；
  4. 将旧容器改名为备份，将临时容器提升为正式名称；
  5. 再次刷新 endpoint；
  6. 删除旧容器备份。
- 创建临时容器继续复用 `docker run --env-file`，因此会重新读取 `java-common.env`、`java-secrets.env` 和各服务 `.env`。
- Java 容器继续挂载 Runtime env 目录，启动脚本会读取最新 `java-jvm.<service>.options` 或全局 `java-jvm.options`。

## 失败与取消处理

- 在创建任何替代容器前完成 agent、spec 和实例预检。
- 临时容器启动或健康检查失败：删除临时容器，保留旧正式容器，返回失败并停止后续服务。
- 旧容器备份或临时容器提升失败：尽力恢复正式名称并清理临时容器；恢复失败时返回包含容器名的脱敏诊断，但不输出配置内容。
- 删除旧备份失败：任务失败并保留可识别的备份容器，后续可通过现有 stale cleanup 处理；不继续后续服务。
- 用户取消任务：上下文终止后续服务和健康等待，清理尚未完成切换的临时容器；已成功完成的服务保持新容器，不执行整批回滚。
- 任一失败或取消都必须释放 orchestration lock。

## 任务步骤与状态反馈

任务至少包含以下稳定步骤：

1. `load-instance`：加载并验证 Runtime 实例。
2. `preflight-runtime`：检查 agent、spec 和在线服务集合。
3. `rolling-restart`：逐服务逐副本执行重建，并在日志中记录服务级进度。
4. `verify-runtime`：刷新并验证 Runtime 最终状态。

状态操作继续使用 `pending`、`running`、`success`、`failed`、`cancelled`。服务级日志记录开始、跳过、成功和失败，不记录任何环境变量值。

## 权限、并发与安全

- 停止任务使用 `tasks.manage`。
- Runtime 全量重建使用 `apps.manage`。
- handler 不接收自由 shell、容器名、env 路径或任意命令。
- 实例 ID 必须解析到当前服务器绑定的 AIFAR Runtime 实例。
- service 使用实例级 orchestration lock；agent 使用 `reconcileMu`。
- API 错误继续使用 `{ code, message, details }`。
- 所有用户可见前后端文案提供 zh/en。

## 测试设计

### 前端

- 当前任务为 `pending` 或 `running` 时停止按钮可用。
- 终态任务、未选任务和权限不足时按钮禁用并提供正确原因。
- 确认后调用单任务 cancel API；`cancelled=true` 和 `cancelled=false` 分别显示正确反馈。
- Runtime 全量重建按钮的权限和状态禁用条件正确。
- 确认框展示当前实例、在线服务数量和失败策略。
- 提交后跟踪 `aifar.runtime.restart-all` 任务并刷新 Runtime 状态。
- 新增中英文文案键均存在。

### HTTP 与应用服务

- 路由要求 `apps.manage` 权限。
- 缺失或非法实例返回标准错误。
- handler 创建正确任务类型、步骤、目标和审计。
- orchestration lock 冲突时不启动远端操作。
- 成功、失败和取消均释放锁并记录终态。

### runtime agent

- 期望副本数为 0 的 Deployment 被跳过。
- 在线服务按 spec 顺序、每个副本逐个替换。
- 新容器命令继续包含最新 env 文件路径。
- 新容器健康后才切换名称和删除旧容器。
- 健康失败时临时容器被删除、旧容器保留，后续服务不执行。
- 取消时停止后续服务并清理未切换的临时容器。
- 已完成服务在后续失败或取消时不回滚。
- 周期调和与手工重建由同一互斥锁串行化。

## 验证门禁

- 后端：在 `backend/` 运行 `go test ./...`。
- 前端逻辑：运行 `pnpm test:web`。
- 前端类型与生产构建：运行 `pnpm web:build`。
- 若修改脚本或 agent CLI 契约，运行 `pnpm test:scripts`。
- 收口前检查 `git diff`，确保不包含用户已有的 `memory.md` 改动或无关文件。

## 验收标准

- 用户可以在任务中心停止当前单个 pending/running 任务，并看到准确的请求与终态反馈。
- 用户可以对当前选中的 Runtime 实例发起一次全量在线服务滚动重建。
- 新容器使用磁盘上的最新 env 文件和 JVM options 启动。
- 已下线服务不会被启动。
- 任一新容器健康失败时旧容器仍可用，后续服务不再执行。
- 操作具备任务、步骤、目标、审计、权限检查、中英文文案和自动化测试。
