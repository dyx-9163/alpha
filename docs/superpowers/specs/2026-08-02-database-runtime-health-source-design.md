# 数据库实例在线状态数据源修复设计

## 1. 背景与问题

数据库实例页同时展示服务器、MySQL、Redis、MySQL Router 等对象。当前页面在计算数据库节点“在线/离线”时，会先读取所属服务器的 `server.status`：

- `baseNodeHealth()` 首先调用 `serverStatusOffline(nodeServerStatus(node))`；
- `mysqlRuntimeHealth()` 同样首先调用服务器状态判断；
- 服务器只要被探测为 `failed`、`offline` 或 `unavailable`，数据库节点就会直接显示“离线”，即使最新应用实例检测快照明确返回 `running`。

这混淆了两个不同的事实：

- 服务器状态表示面板对目标服务器的 SSH/主机级探测结果；
- 数据库状态表示 MySQL、Redis 或 Router 服务自身的运行检测结果。

因此，服务器探测失败、状态滞后或探测策略变化时，会错误覆盖数据库服务的实际检测结果。

## 2. 目标

数据库实例页的“在线/离线”只表达数据库服务自身的检测结果，不再表达服务器检查状态。

具体目标：

1. MySQL、Redis 和 MySQL Router 节点状态以 `app.instance` 实时检测快照为权威来源。
2. InnoDB Cluster 节点优先采用 MySQL 运行时检测字段，并继续结合集群角色/主节点检测计算集群有效性。
3. 服务器状态不再参与数据库节点在线/离线判断，但仍可用于服务器名称、地址匹配等非健康状态用途。
4. 没有可信应用检测结果时显示“未知”，不使用服务器状态或安装完成状态猜测“在线”。
5. 不改变后端采集器、数据库结构、API、SSE 协议、页面布局和现有中英文文案。

## 3. 非目标

本次不处理以下内容：

- 不修改“服务器”页面的主机在线状态定义。
- 不改变采集器如何通过 SSH 执行 MySQL/Redis 检测。
- 不把数据库端口探测改为浏览器直连或新增轮询。
- 不修改数据库安装、备份、恢复、集群启动和删除逻辑。
- 不为 Redis Sentinel 自动发现的虚拟节点伪造独立检测结果。

## 4. 推荐方案

### 4.1 状态语义分离

建立纯函数形式的数据库健康状态解析器，将数据库节点状态限定为：

- `online`：最新应用检测明确表明服务正在运行；
- `offline`：最新应用检测明确表明服务失败、停止或不可用；
- `probing`：已有明确的应用检测进行中信号时使用；
- `unknown`：没有可信的应用检测结果，或结果无法识别。

解析器不接收 `server.status`。这样从函数接口上避免以后再次误用服务器状态。

### 4.2 数据优先级

普通 MySQL、Redis 和 Router 节点读取 `metadata.lastCheck.status`：可识别的成功状态映射为 `online`，可识别的失败状态映射为 `offline`，其余情况返回 `unknown`。

其中，页面现有的 `applyRealtimeStatusToAppInstance()` 会把最新 `app.instance` 快照合并到实例状态和 `metadata.lastCheck`，因此 SSE 推送及 `/status/snapshots` 初始加载仍是主要实时数据链路。

不能仅凭“安装完成后持久化的 `app_instances.status=running`”认定在线；缺少 `lastCheck` 或实时快照检测依据时，应显示“未知”，等待后端采集结果。

### 4.3 InnoDB Cluster 节点

InnoDB Cluster 节点先解析应用检测详情中的运行时字段：

1. `lastCheck.details.runtimeStatus`；
2. `lastCheck.details.mysqlServiceStatus`；
3. `lastCheck.details.mysqlPortStatus`；
4. 兼容旧数据的 `metadata.mysqlRuntimeStatus` / `metadata.runtimeStatus`。

运行时字段明确为运行或失败时，直接得到节点运行状态；运行时字段未知时，再回退到应用检测状态，而不是服务器状态。

集群卡片的聚合逻辑继续区分：

- 所有节点运行时离线：服务不可用；
- 部分节点在线：降级；
- 所有节点运行时在线且检测到有效 Primary、各节点集群检查正常：运行中；
- 所有节点运行时在线但没有有效 Primary：服务不可用；
- 没有可信检测结果：未知。

这保留当前对“进程存活但集群无 Primary”的安全判断。

### 4.4 Redis Sentinel 虚拟节点

Redis Sentinel 页面可能依据拓扑检测结果补出没有独立 `app_instance` 的虚拟节点。此类节点没有自己的应用检测快照：

- 继续显示角色和 Endpoint；
- 当前没有独立应用检测快照的虚拟节点显示“未知”；
- 不再根据 Endpoint 匹配到的服务器状态推断在线/离线。

### 4.5 服务器状态的保留用途

`servers` 数据仍保留在数据库页面，用于：

- 显示服务器名称和主机地址；
- 根据 Endpoint 关联服务器；
- 删除确认和跳转等管理上下文。

它不再输入节点健康状态解析器，也不参与数据库卡片的 `running`、`degraded` 或 `unavailable` 聚合。

## 5. 数据流

```text
后端应用 CheckModule
  -> collector 保存 app.instance status snapshot
  -> SSE status 事件 / GET /status/snapshots
  -> realtime store
  -> applyRealtimeStatusToAppInstance()
  -> 数据库健康状态解析器
  -> 节点标签与实例卡片聚合状态
```

服务器探测链路保持独立：

```text
服务器 Probe
  -> server.status
  -> 服务器页面和服务器管理上下文
  -X-> 数据库节点在线/离线
```

## 6. 代码边界

建议增加一个小型纯函数模块，例如 `web/src/database/databaseHealth.ts`，负责：

- 识别应用检测状态是否在线、离线或未知；
- 解析普通数据库节点健康状态；
- 解析 MySQL 运行时健康状态；
- 保持状态解析与 Vue 页面、Pinia、服务器列表解耦。

`DatabaseView.vue` 继续负责：

- 构造页面节点；
- 提供节点的实例状态和检测元数据；
- 聚合节点、Router 和 Sentinel 状态；
- 渲染标签和业务操作。

此边界能用纯单元测试覆盖状态优先级，避免为了验证一个状态函数挂载整个复杂页面。

## 7. 错误与时效处理

- 后端采集失败时，采集器已经会保存 `failed` 或 `unavailable` 的应用快照；页面据此显示离线，不需要再借用服务器状态。
- 采集超时时，采集器保存 `unavailable` 快照；页面显示离线。
- SSE 暂时断开时，页面保留最后一次应用快照；连接恢复后由快照接口补齐最新版本。
- 本次不新增前端“过期阈值”。快照采集时间继续通过“最近监测”显示，避免未经产品定义就把历史正常结果自动改成离线。
- 若服务器记录已删除或无法读取，应用检测通常会产生失败快照；在失败快照到达前页面显示未知，而不是提前推断离线。

## 8. 测试设计

新增纯函数单元测试，至少覆盖以下场景：

| 应用检测结果 | 服务器状态 | 期望数据库状态 |
| --- | --- | --- |
| `running` | `failed` | 在线 |
| `failed` | `available` | 离线 |
| 无检测结果 | `available` | 未知 |
| 无检测结果 | `failed` | 未知 |
| MySQL `runtimeStatus=running` | `failed` | 在线 |
| MySQL `runtimeStatus=offline` | `available` | 离线 |
| MySQL 运行时字段未知、应用检测 `running` | 任意 | 在线 |

同时验证页面聚合行为：

- 单节点 Redis 的应用检测为 `running` 时，卡片为“运行中”，即使服务器探测失败；
- 单节点 Redis 的应用检测为 `failed` 时，卡片为“服务不可用”；
- InnoDB Cluster 三节点运行时在线但无 Primary 时，卡片仍为“服务不可用”；
- 没有应用检测结果的节点显示“未知”，不显示由服务器状态推导出的“在线/离线”。

实现时遵循测试驱动流程：先写能够复现“服务器失败覆盖应用在线”的失败测试，确认它在旧逻辑下失败，再实施最小代码修复。

## 9. 验收标准

1. 把某服务器的 `server.status` 置为失败，但其 Redis/MySQL 最新应用快照为 `running`，数据库页节点仍显示“在线”。
2. 服务器显示可用，但数据库应用快照为 `failed`，数据库页节点显示“离线”。
3. 没有应用检测快照或持久检测元数据时，数据库页显示“未知”。
4. InnoDB Cluster 仍能识别运行时离线、部分节点降级和无 Primary 的不可用状态。
5. 服务器页面行为不变。
6. `pnpm test:web` 和 `pnpm web:build` 通过。
7. 不覆盖 `DatabaseView.vue` 当前已有的备份/恢复相关未提交改动。

## 10. 风险与控制

- **短暂未知状态**：首次打开页面而采集器尚未生成应用快照时可能显示“未知”。这是比用主机状态猜测数据库状态更准确的表达，采集快照到达后会自动更新。
- **旧实例元数据不足**：旧记录可能只有安装状态。保持“未知”并等待采集，不做数据迁移。
- **复杂页面改动冲突**：当前 `DatabaseView.vue` 已有未提交的备份/恢复修改。实现仅触碰状态解析调用点，并通过新增纯函数模块降低重叠范围。
- **错误把集群进程在线当成集群可用**：继续保留 Primary 和集群节点状态校验，不降低 InnoDB Cluster 可用性判断标准。

## 11. 推荐结论

采用“数据库应用检测状态与服务器探测状态彻底解耦”的方案。服务器状态不再作为数据库节点健康判断的兜底值；应用检测明确成功或失败时分别显示在线或离线，没有可信应用检测时显示未知。该方案语义清晰、改动范围小，不需要后端协议或数据库迁移，并能通过纯函数测试稳定防止回归。

## 12. 集群聚合补充（2026-08-02）

- 三个 MySQL runtime 在线、Primary 已知且至少一个当前成员集群检查成功，但部分成员检查失败时，集群显示 `degraded`，不得显示 `unavailable`。
- 只有三个非虚拟 MySQL 节点的 runtime 均在线，且三个当前集群检查均明确为 `offline` 时，才允许执行“启动集群”。
- 三个 runtime 在线但当前集群检查仍在 `probing` 或结果不完整、未知时，分别显示 `probing` 或 `unknown`；只有所有当前检查都明确 `offline` 才显示完整不可用。
