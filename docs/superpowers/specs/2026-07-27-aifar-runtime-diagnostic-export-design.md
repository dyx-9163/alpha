# AIFAR Runtime 一键故障诊断包导出设计

## 背景

AIFAR Runtime 由多个微服务组成。当前页面日志主要通过 Docker `json-file` 的 stdout/stderr 聚合并使用 SSE 展示，而目标服务器还在 `/aifar/apps/admin/runtime/logs/<service>/` 保存各服务的持久文件日志。浏览器当前已加载的日志既不完整，也缺少服务状态、容器健康、Agent 和主机资源等定位故障所需信息，因此不能直接把页面文本保存为正式诊断包。

首版诊断导出必须在不修改 `aifar-agent`、不修改 Java 微服务的前提下完成。AIFAR 面板继续负责权限、任务、审计和结果登记，通过现有 SSH 能力在日志所在服务器采集并压缩，避免把最多 3 GB 的未压缩数据拉回面板服务器。

## 目标

1. 在当前选中的 AIFAR Runtime 实例上生成一键故障诊断包。
2. 默认采集最近 2 小时，允许自定义开始和结束时间。
3. 默认选择全部已启用服务，允许用户取消部分服务；已下线服务不进入首版可选范围。
4. 同时采集文件日志、Docker 容器日志和脱敏运行诊断摘要。
5. 任务开始前预估采集量和目标服务器磁盘空间；未压缩内容硬上限 3 GB，压缩包硬上限 1 GB。
6. 单个服务、Pod 或诊断项失败时继续生成部分诊断包，并明确记录缺失内容。
7. 诊断包保留 24 小时，支持下载完成后立即删除和手工删除。
8. 所有生成、取消、下载、删除和自动清理行为均受权限控制并可审计。

## 非目标

- 不修改或升级 `aifar-agent`。
- 不修改 Java/Web 微服务的日志实现。
- 不做长期日志归档、检索平台或跨实例集中日志库。
- 不导出配置文件正文、`.env`、数据库文件、SSH 凭据或自由选择的服务器目录。
- 不允许 API 接收并执行自由 Shell、任意命令、任意容器名或任意文件路径。
- 不把最大 1 GB 的最终归档持久复制到 `aifar-server` 本地磁盘。
- 首版下载不支持 HTTP Range、断点续传或多节点合并为一个诊断包。

## 方案比较

### 方案一：修改 Agent，由 Agent 本地采集

Agent 最接近日志和 Docker，能够提供结构化能力和较强取消语义，但需要同步升级所有目标节点的 Agent。用户明确要求不修改 `aifar-agent`，因此不采用。

### 方案二：SSH 执行内置受信脚本并在远端打包

`aifar-server` worker 通过 SSH 执行使用 `go:embed` 编译进后端的固定脚本，在目标服务器受控目录采集、脱敏并生成 `tar.gz`；下载时再通过二进制安全 SSH 流转发给浏览器。该方案复用现有服务器凭据和任务基础设施，不增加 Agent 发布依赖，是本设计采用的方案。

### 方案三：把原始日志拉到面板后打包

实现路径直观，但会把最多 3 GB 的未压缩日志传输到面板并占用面板磁盘、网络和 CPU；多用户并发时风险更高，因此不采用。

## 总体架构

### 前端

在 Runtime 日志页增加“导出诊断包”入口和诊断包记录区域。弹窗收集时间范围和已启用服务选择，先执行只读预估，再创建导出任务。任务进度复用现有 SSE；任务进入终态后仅刷新一次诊断包记录，不增加浏览器轮询。

### HTTP 控制层

handler 只负责鉴权、请求解析、实例解析、参数校验、worker 任务创建、审计和响应。客户端只提交实例 ID、时间范围、服务机器码及可选的“下载完成后删除”选择，不提交安装根目录、日志路径或 Shell 内容。

### 应用服务

AIFAR 应用模块提供独立的 Runtime 诊断导出 service，负责：

- 从 `app_instances` 和 Runtime spec 解析目标服务器、安装根目录及已启用服务。
- 使用实例级诊断导出锁保证同一实例同一时间只生成一个诊断包，控制临时空间占用。诊断采集不占用 Runtime 生命周期编排锁，也不阻止紧急升级、回滚或重启；采集期间容器被替换时，按局部采集失败记录告警并继续。
- 渲染并执行内置受信采集脚本。
- 更新任务步骤、诊断包记录和审计。
- 通过新的二进制安全 SSH 文件流接口下载归档。

### 远端采集器

采集脚本使用 `go:embed` 随后端发布，由后端填入经过严格校验和 Shell 转义的固定参数。远端工作目录固定为：

```text
<installRoot>/runtime/diagnostics/<exportId>.partial/
```

完成全部校验、清单和 SHA256 后，归档才原子提升为：

```text
<installRoot>/runtime/diagnostics/<exportId>/aifar-diagnostics-<instance>-<timestamp>.tar.gz
```

API 和数据库只保存受控根目录下的相对文件名，不接受或回传可执行路径。

### SQLite Store

新增集中迁移表 `diagnostic_exports`，不把生命周期复杂的导出记录塞入 `app_instances.metadata`。建议字段：

- `id`、`task_id`、`instance_id`、`server_id`
- `status`
- `services_json`、`since_at`、`until_at`
- `remote_relative_path`、`archive_name`
- `archive_bytes`、`uncompressed_bytes`、`sha256`
- `warning_count`、`warnings_json`、`error_text`
- `created_by`、`created_at`、`ready_at`、`expires_at`
- `downloaded_at`、`deleted_at`
- `cleanup_status`、`cleanup_error`、`cleanup_attempted_at`

所有字段通过 `Store.migrate()` 前向迁移并配套 store 测试。记录不得包含密码、Token、环境变量值或完整 SSH 错误输出。

## API 契约

路由统一位于 `/api/v2/containers/aifar/runtime/diagnostics`：

### 预估

`POST /estimate`

只读同步接口，使用受限 SSH 命令统计所选时间和服务的候选文件、Docker 日志保守估值及可用空间，并设置服务端超时。返回分服务估值、总估值、所需临时空间、可用空间、告警和是否允许提交。预估失败或超限时不能创建导出任务。

Docker `json-file` 很难在不读取内容的情况下精确按时间计算，预估应明确标记为保守估值；真正生成时仍执行 3 GB 和 1 GB 硬限制。

### 创建导出

`POST /exports`

服务端创建 `aifar.runtime.diagnostics.export` worker 任务，返回 `202 Accepted`、`taskId` 和预先生成的 `exportId`。worker 在执行 `estimate-size` 步骤时重新预估并决定是否继续，不接收也不信任浏览器提交的预估大小。

### 查询记录

`GET /exports`

按当前实例分页返回导出记录。服务端按 `expires_at` 计算是否允许下载，不依赖浏览器时间。

### 下载

`GET /exports/{id}/download?deleteAfterDownload=true|false`

后端按 export ID 查库，验证记录属于请求实例和服务器、状态为 `ready`、尚未过期，且相对文件名仍位于受控目录。通过新 adapter 能力 `StreamSSHFile` 将远端文件以二进制流转发，设置安全的 `Content-Disposition` 和已确认的 `Content-Length`。客户端断开时立即取消 SSH 流。

仅当文件全部写入 HTTP 响应且没有传输错误时，`deleteAfterDownload=true` 才创建后续删除动作；HTTP 发送完成只能说明服务器已完整发送，不能证明浏览器用户最终保存成功，因此该选项默认关闭。

### 手工删除

`DELETE /exports/{id}`

状态变更必须走 worker 和审计。接口返回 `202 Accepted` 和删除任务 ID；worker 只删除查库后解析出的受控归档和对应工作目录，成功后把记录标记为 `deleted`。

所有接口错误保持 `{ code, message, details }`，用户可见文案进入前后端 zh/en i18n。

## 诊断包内容

```text
aifar-diagnostics-<instance>-<timestamp>/
├── README.txt
├── manifest.json
├── collection-errors.txt
├── services/
│   └── <service>/
│       ├── file-logs/
│       └── container-logs/<container-name>.log
└── diagnostics/
    ├── runtime-summary.json
    ├── deployments.json
    ├── pods.json
    ├── containers.txt
    ├── health-checks.txt
    ├── agent-status.txt
    ├── host-resources.txt
    └── release-summary.json
```

### 文件日志

- 来源限定为 `<installRoot>/runtime/logs/<service>/`。
- 保留该服务目录内的相对路径。
- 使用文件修改时间筛选时间范围，并在 `manifest.json` 明确标记时间语义为 `file-mtime`。
- 只采集普通文件，不使用 `find -L`，不跟随软链接，不跨文件系统，不读取目录外文件。

### 容器日志

- 只选择同时匹配当前实例 `aifar.instance` 和所选 `aifar.service` 标签的现存容器。
- 包含运行中或已停止但仍存在的匹配容器，不允许客户端指定容器名称。
- 使用 `docker logs --since <start> --until <end> --timestamps` 获取准确时间窗口。
- 单个容器失败只产生告警，不中断其他容器和服务。

### 运行诊断

包含服务和 Pod 状态、版本与发布摘要、容器健康状态、Agent 运行状态、主机磁盘与内存摘要以及采集清单。配置文件正文、环境变量、数据库内容和 SSH 凭据一律排除。

### 清单与错误

`manifest.json` 至少记录：导出 ID、实例、服务器标识、请求时间范围、实际采集时间、所选服务、每个文件的来源类型、相对路径、原始大小、归档大小或可得大小、SHA256、告警和缺失项。

`collection-errors.txt` 使用脱敏后的可读文本记录采集失败项。无错误时文件仍存在并声明未发现采集错误，便于自动化消费。

`README.txt` 解释两种日志时间语义、告警含义、上限和脱敏边界，并提醒业务日志仍可能包含用户输入或业务数据，诊断包应按敏感资料管理。

## 任务步骤

导出任务使用以下稳定步骤：

1. `load-instance`：加载实例、服务器、Runtime spec 和安装根目录。
2. `validate-request`：验证权限、时间范围和已启用服务集合。
3. `estimate-size`：重新预估候选内容和目标服务器空间。
4. `collect-file-logs`：收集按修改时间命中的文件日志。
5. `collect-container-logs`：按标签和时间范围采集 Docker 日志。
6. `collect-diagnostics`：采集运行状态、版本、健康和主机资源。
7. `redact-and-manifest`：执行脱敏并生成清单和错误文件。
8. `create-archive`：生成、校验并原子提升归档。
9. `record-export`：记录大小、SHA256、告警、就绪和过期时间。

单个服务、容器或诊断命令失败时记录告警并继续。只要归档结构、清单和最终校验成功，worker 状态仍为 `success`，导出记录为 `ready`，页面通过 `warning_count` 显示“可下载（有告警）”；不引入 `success_with_warnings` worker 机器状态。

## 状态模型

导出状态使用：

- `pending`
- `building`
- `ready`
- `failed`
- `cancelled`
- `expired`
- `deleted`

清理进度独立使用 `cleanup_status`：`none`、`pending`、`failed`、`complete`。这样“已过期但服务器离线、尚未删除”可以稳定表达为 `status=expired` 与 `cleanup_status=pending|failed`，且下载接口只需在 `expires_at <= now` 时拒绝下载。

## 大小和磁盘控制

### 生成前

- 时间范围必须合法且至少选择一个已启用服务。
- 预估未压缩内容不得超过 3 GB。
- 目标服务器可用空间必须覆盖预计生成的临时容器日志、最大 1 GB 归档，以及不小于 512 MB 或预计临时占用 20% 中较大的安全余量。
- 服务器端在创建任务时重新检查，不能依赖浏览器预估结果。

### 生成中

- 每加入一个文件或一段容器日志都累计未压缩字节；达到 3 GB 时停止并失败。
- 压缩输出在写入期间监控；达到 1 GB 时终止压缩并失败。
- 超限、清单失败、归档校验失败或最终 SHA256 失败都删除 `.partial`，不提供截断包下载。

## 安全设计

- 创建、列表、下载和删除均要求 `apps.manage`。
- 实例 ID 必须解析到当前服务器上的非 legacy AIFAR Runtime 实例。
- 服务机器码必须属于 Runtime spec 中期望副本数大于 0 的 Deployment，并使用严格标识符校验。
- 安装根目录只从受控实例数据解析；规范化后确认诊断目录仍位于该根目录下。
- export ID 由服务端生成 UUID；相对文件名由服务端生成并在读取、删除前再次校验。
- 采集前检查 `docker`、`find`、`tar`、`gzip`、`sha256sum`、`df` 和 `stat` 等固定依赖，不依赖 `jq`。
- 脚本参数使用统一 Shell 引号函数；不得字符串拼接来自客户端的命令片段。
- 默认脱敏 Authorization/Bearer、password/passwd、secret、token、access key、secret key 和 URL userinfo 等常见模式；脱敏规则不承诺识别所有业务隐私，限制在 README 和 UI 中明确提示。
- 任务日志、审计和数据库错误只保存脱敏摘要；原始 SSH stderr 不直接返回浏览器。

## 取消语义

- 采集脚本在受控临时目录记录数字 PID/进程组信息，并尽可能使用独立进程组运行。
- worker 上下文取消后先关闭当前 SSH session，再使用独立、固定的清理命令终止该 export ID 对应的进程组；PID 必须是从受控 PID 文件读取并通过纯数字校验的值。
- 取消只删除当前导出的 `.partial` 目录，不删除原始日志、不停止业务容器、不删除其他诊断包。
- 脚本在正式归档提升前再次检查取消标记。未提升时取消，任务为 `cancelled`；一旦归档完成、SHA256 已生成并进入不可中断的登记小临界区，则导出按成功处理，避免出现“文件已就绪但数据库显示已取消”。
- 远端进程或临时目录清理失败时，在任务日志和导出记录中留下脱敏告警，并由后续清理任务重试。

## 下载、删除和保留

- 诊断包在 `ready_at + 24h` 过期。
- 下载开始前和远端打开文件前都检查过期时间、状态、文件大小和受控相对路径。
- 下载流不落地到面板磁盘；浏览器中断会取消 SSH 读取。
- `deleteAfterDownload` 默认关闭，仅在服务器完整发送响应后排队执行删除任务。
- 手工删除和自动删除都必须可审计，并且只使用数据库记录解析受控目标。
- 后台调度器在服务启动时和此后每小时合并创建一个 `aifar.runtime.diagnostics.cleanup` worker 任务，避免为每条记录制造独立定时任务。
- 到期时先将记录标记为 `expired` 并禁止下载。目标服务器离线或删除失败时将 `cleanup_status` 设为 `pending` 或 `failed`，后续周期继续重试；只有远端确认文件和工作目录已不存在后才标记 `deleted/complete`。

## 页面交互

### 导出弹窗

- 当前实例固定显示，不允许在弹窗临时切换服务器。
- 时间默认“最近 2 小时”，可切换自定义起止时间。
- 默认勾选全部已启用服务，允许取消部分服务；已下线服务不进入首版可选范围。
- “预估大小”显示文件日志估值、Docker 日志保守估值、可用空间、所需空间和告警。
- 只有预估成功、未超限且空间足够时才能点击“开始导出”。
- 提交成功后关闭弹窗并提供“查看任务”入口。

### 诊断包记录

Runtime 日志页显示当前实例的记录列表：状态、时间范围、服务数量、文件大小、告警数、创建时间和过期时间。可用动作包括查看任务、下载、下载完成后删除和手工删除。

展示状态使用：正在预估、正在生成、可下载、可下载（有告警）、生成失败、已取消、已过期待清理、已删除。任务状态变化继续使用 SSE；终态事件触发一次记录刷新。

## 错误处理

- 目标服务器无法连接：预估或任务失败，不生成记录或半成品。
- 预估超过 3 GB：阻止提交，并提示缩短时间或减少服务。
- 磁盘不足：显示所需空间与当前可用空间。
- 单服务或单容器失败：继续采集，归档就绪并显示告警。
- 归档、上限或 SHA256 校验失败：删除半成品，记录为 `failed`。
- 下载中断：保留归档，不触发下载后删除。
- 归档过期但服务器离线：禁止下载，保留待清理状态并自动重试。
- 所有用户可见错误使用可操作的中英文文案；技术细节进入脱敏任务日志和 `collection-errors.txt`。

## 测试设计

### Store

- `diagnostic_exports` 前向迁移和 CRUD。
- 状态、过期、下载、删除和清理字段更新。
- 重启后能恢复待清理记录。

### 远端采集与安全

- 使用临时目录和 fake remote 测试 2 小时修改时间筛选、相对路径保留和文件清单。
- 软链接、路径穿越、非法服务名和任意容器名均被拒绝。
- Docker 命令只使用服务端解析的标签，并包含正确的 `--since`、`--until` 和 `--timestamps`。
- 单服务、单容器和单诊断命令失败后继续生成。
- 常见密码、Token 和 URL userinfo 脱敏，配置文件正文不进入归档。
- 3 GB 未压缩与 1 GB 压缩硬限制会终止任务并清理半成品。
- 取消只终止当前导出进程组并清理当前 `.partial`。

### HTTP 与 worker

- 所有接口要求 `apps.manage`，错误保持标准结构。
- 创建接口生成正确的任务、九个步骤、目标和审计。
- worker 成功、带告警成功、失败和取消状态正确。
- export ID 不能访问其他实例、服务器或目录的文件。
- `StreamSSHFile` 保持二进制完整性和 SHA256，一旦客户端断开即取消远端读取。
- 下载中断不删除；完整发送后才排队执行可选删除。
- 手工删除和每小时清理均走任务与审计，离线服务器可重试。

### 前端

- 默认 2 小时、已启用服务全选、自定义时间和至少一个服务校验。
- 预估中、超限、空间不足和可提交状态正确。
- 创建后能够跳转任务，SSE 终态触发一次记录刷新。
- 可下载、有告警、失败、取消、过期待清理和删除状态显示正确。
- 下载后删除默认关闭，删除操作具有确认提示。
- 新增全部 zh/en 文案键。

### 验证命令

- 后端：在 `backend/` 运行 `go test ./...`。
- 前端逻辑：运行 `pnpm test:web`。
- 前端类型和生产构建：运行 `pnpm web:build`。
- 脚本契约：运行 `pnpm test:scripts`。
- 收口前运行 `git diff --check`，确认未纳入用户无关改动。

真实 SSH/Docker 自动测试必须使用 fake remote，不连接真实服务器。

## 真实 openEuler 验收

1. 多个运行中微服务可以生成结构正确的诊断包。
2. 单个服务或容器日志采集失败时仍生成带告警的包。
3. 导出过程中取消不会影响原始日志和业务容器，且无残留半成品。
4. 自定义时间和服务选择只包含预期内容。
5. 下载文件 SHA256 与服务器登记值一致。
6. 下载中断保留文件，完整下载后的可选删除生效。
7. 24 小时过期和目标服务器离线后的恢复清理生效。
8. 包内不存在 `.env`、配置正文、密码、Token 或服务器保存的 SSH 凭据。

## 验收标准

- 用户可在当前 Runtime 实例一键创建最近 2 小时的多服务故障诊断包，并可调整时间与服务范围。
- 生成前有可靠的保守预估，生成中严格执行 3 GB 未压缩和 1 GB 压缩硬限制。
- 文件日志、Docker 日志和运行诊断具有明确来源、时间语义、清单、SHA256 与缺失说明。
- 局部采集失败不阻断可用诊断包，关键归档失败不会暴露半成品。
- 取消、下载、下载后删除、手工删除和 24 小时清理语义明确且可恢复。
- 功能不修改 `aifar-agent`，不向 API 暴露自由 Shell 或任意路径，不把归档持久复制到面板磁盘。
- 权限、任务、步骤、目标、审计、前后端 i18n 和自动化测试完整覆盖。
