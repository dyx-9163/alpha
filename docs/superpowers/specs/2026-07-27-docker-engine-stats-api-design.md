# Docker Engine Stats API 指标采集设计

## 背景

AIFAR Runtime 的 Pods 页在用户点击“刷新指标”后，请求 Runtime API 并启用 `includeStats=1`。后端先取得目标容器列表，再通过 `adapter.DockerContainerStatsForServer` 读取 CPU 和内存指标。

当前 `tcp://`、`http://`、`https://` Docker Host 的探测、容器列表、镜像、网络、卷和日志等能力已经直接调用 Docker Engine HTTP API；唯独指标采集仍调用面板宿主机上的 `docker -H <host> stats`。因此面板机没有安装 Docker CLI 时，容器管理可以工作，但“刷新指标”失败。

## 目标

- `tcp/http/https` Docker Host 的指标采集不再依赖面板宿主机 Docker CLI。
- 保持现有 Runtime API、前端按钮和响应字段不变。
- 单个容器采集失败时保留其他容器的有效指标。
- 限制并发和请求时间，避免大量 Pod 同时刷新时压垮 Docker Engine API。
- 不修改或重新发布 `aifar-agent`。

## 非目标

- 本次不关闭或改造现有 Docker Remote API 监听方式。
- 本次不把指标采集迁移到 `aifar-agent`。
- 本次不增加历史指标、图表、定时采样或数据库存储。
- 本次不改变自动扩缩容当前独立的远端采集逻辑。
- 本次不修改 SSH 模式下的远端 `docker stats` fallback。

## 方案选择

采用 Docker Engine Stats API：对目标容器调用 `GET /containers/{id}/stats?stream=false`，由后端解析原始计数器并生成现有 `DockerContainerStat`。

没有采用以下方案：

- 全部改为 SSH 远端 CLI：无需面板机 Docker，但每次刷新都增加 SSH 开销并继续依赖 CLI 输出格式。
- 由 Agent 采集：长期边界更合理，但需要扩展 Agent 协议、重新构建和部署二进制，超出本次最小修复范围。

## 架构与代码边界

### Adapter 层

在 `backend/internal/adapter/docker_api.go` 增加 Engine API stats 读取和计算函数。该函数只负责：

- 构造并发送单容器非流式 stats 请求。
- 解析 Docker Engine 返回的 CPU、内存、容器 ID 和名称字段，并移除名称开头的 `/`，保持与容器列表名称一致。
- 将原始计数器转换为现有 `DockerContainerStat`。

在 `backend/internal/adapter/docker.go` 调整 `DockerContainerStatsForServer`：

- `tcp://`、`http://`、`https://`：调用新的 Engine API 批量采集函数，不执行本机 `docker`。
- `ssh://`、空 Docker Host 或其他现有 SSH 路径：保持通过 `dockerSSHOutput` 在目标机运行 `docker stats`。
- 不为 Engine API 失败增加本机 CLI fallback，避免重新引入隐式本地依赖。

### HTTP API 层

`buildAIFARRuntime` 保持现有 `includeStats` 契约。它接收 Adapter 返回的“已成功指标 + 汇总错误”：

- 无论是否存在部分错误，都先把成功结果写入 `statsByName`。
- 若存在错误，追加 Runtime warning。
- 不因指标缺失把 Runtime 或 Pod 状态改为 degraded；指标属于可选观测数据，容器状态仍由现有状态链路决定。

### 前端和 Agent

- `web/src` 不修改；“刷新指标”按钮、加载状态和 `CPU/内存` 列保持现状。
- `aifar-agent` 不修改，因此无需更新 Agent 二进制。

## 数据流

1. 用户点击 Pods 页“刷新指标”。
2. 前端请求 Runtime API，并设置 `includePods=1&includeStats=1`。
3. 后端读取目标服务器及 AIFAR Runtime 容器列表。
4. Adapter 对规范化、去重后的容器标识执行有界并发采集，最大并发数固定为 4。
5. 每个 Engine API 请求使用现有 HTTP client 的超时和请求上下文，并携带 `stream=false`。
6. 成功结果按容器名称和 ID 映射到对应 Pod；失败容器的指标字段保持零值，前端继续显示 `-`。
7. 任一失败被汇总为一条可诊断 warning，其他成功指标仍返回。

## 指标计算

### CPU

使用 Docker CLI 同类计算方式：

```text
cpuDelta    = cpu_stats.cpu_usage.total_usage - precpu_stats.cpu_usage.total_usage
systemDelta = cpu_stats.system_cpu_usage - precpu_stats.system_cpu_usage
cpuCount    = cpu_stats.online_cpus；若为 0，则回退到 percpu_usage 数量
cpuPercent  = cpuDelta / systemDelta * cpuCount * 100
```

仅当两个增量均大于 0 且 `cpuCount > 0` 时返回正值；计数器缺失、倒退或除数为 0 时返回 0，不产生 NaN 或 Infinity。

### 内存

基础值来自 `memory_stats.usage` 和 `memory_stats.limit`。为接近 Linux Docker CLI 展示语义，优先扣除可回收 page cache：

- cgroup v1 优先使用 `total_inactive_file`。
- cgroup v2 回退使用 `inactive_file`。
- cache 大于 usage 时按 0 处理。

```text
effectiveUsage = max(usage - cache, 0)
memoryPercent  = effectiveUsage / limit * 100
memoryUsage    = "<formatted effectiveUsage> / <formatted limit>"
```

当 limit 为 0 时，内存占比返回 0；原始百分比字符串使用与当前 CLI 解析结果一致的百分号格式。

## 并发与错误处理

- 最大并发固定为 4，不增加配置项。
- 输入容器标识继续使用现有 `normalizeDockerArgs` 去空、去重。
- 每个请求对容器标识进行 URL path escape。
- 404 视为容器在列表读取后被删除或替换，仅记录该容器失败。
- 超时、连接失败、非 2xx 和 JSON 错误均不得导致 panic；合法的全零计数器按空闲容器处理，不误报为结构缺失。
- 批量函数返回所有成功结果，并通过汇总错误报告失败容器数量及简短原因。
- warning 不包含凭据、请求头或完整敏感连接信息。
- 全部失败时返回空指标和汇总错误；HTTP Runtime 响应仍可返回 Pod 状态及 warning。

## 兼容性

- 保持 `DockerContainerStat` 字段及 Runtime JSON 响应结构不变。
- 保持 SSH 远端 CLI 的现有解析行为。
- 保持 Docker Engine API 的 `tcp/http/https` URL 处理方式不变。
- 不引入 Docker SDK 依赖，继续复用当前轻量 HTTP Adapter。
- 不新增数据库字段、迁移、任务或审计类型；“刷新指标”仍为只读请求。

## 测试设计

### Adapter 单元测试

- 使用 `httptest.Server` 验证请求路径和 `stream=false`。
- 验证多个容器均通过 HTTP API 获取，且无需本机 Docker CLI。
- 验证 CPU 正常计算、零增量、缺失 `online_cpus` 时的回退。
- 验证 cgroup v1 `total_inactive_file` 和 cgroup v2 `inactive_file` 的内存计算。
- 验证内存 limit 为 0、cache 大于 usage 时不会出现非法数值。
- 验证一个容器成功、一个容器 404 时仍返回成功指标和汇总错误。
- 验证全部失败、超时和非法 JSON。
- 验证重复及空容器标识只请求一次或被忽略。

### HTTP API 测试

- `includeStats=1` 时成功指标映射到对应 Pod。
- 部分失败时成功 Pod 仍有指标，失败 Pod 指标为空，同时响应包含 warning。
- 指标失败不会覆盖 Agent、Deployment 或 Pod 的健康状态。

### 回归验证

- 运行后端完整测试 `pnpm test`。
- 运行 `git diff --check`。
- 确认本次无 `web/src` 和 `backend/cmd/aifar-agent` 变更。
- 可选真实环境验收：在未安装 Docker CLI 的面板机上点击“刷新指标”，确认远端 Pod 的 CPU/内存正常显示。

## 验收标准

- `tcp/http/https` Docker Host 刷新指标时不启动面板机 `docker` 进程。
- 面板机没有 Docker CLI 时，远端健康容器仍能显示 CPU 和内存指标。
- 单容器失败不清空其他容器指标，也不改变 Runtime 健康状态。
- SSH Docker Host 的既有行为不回归。
- 不需要更新前端静态资源或 `aifar-agent` 二进制即可生效。
