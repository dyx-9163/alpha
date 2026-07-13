# AIFAR 已选服务状态检查设计

## 问题

AIFAR 安装任务可以成功完成，实际选择的 Deployment 也全部就绪，但应用商店“已安装”页可能显示失败。当前安装任务、通用 `app.instance` 探测和 `aifar.runtime` 控制面使用不同状态来源；目录驱动模块和部分安装使这种不一致更容易出现。

## 目标语义

状态检查只覆盖实例实际选择且当前期望运行的服务：

- `desiredReplicas > 0` 的服务必须参与检查。
- 未选择的服务不参与检查。
- 已主动下线、`desiredReplicas = 0` 的服务不参与健康失败判断。
- 任一应运行服务缺失、停止或不健康时，实例不得报告为运行中。
- 全部应运行服务健康且 `aifar-agent` 正常时，实例报告为运行中。

## 方案

在 AIFAR 后端检查链路修正状态源，不在前端掩盖错误，也不在 Collector 中增加 AIFAR 特例。

`Service.Check` 从实例 metadata 的 `services` 和 `desiredReplicas` 生成期望服务集合，并将该集合传给 `Inspector.Check`。Inspector 生成远程只读检查命令时只扫描该集合，并比较期望副本与实际健康容器：

- 无期望运行副本：`offline`。
- 存在期望副本但没有可用容器：`stopped`。
- 部分缺失、停止或不健康：`degraded`。
- 期望副本全部健康且 Agent 正常：`running`。

旧实例如果缺少 `desiredReplicas`，回退到 metadata 中的 `services`，每个已选服务按 1 个期望副本检查；只有两者都缺失时才使用旧版默认服务清单。

通用 `app.instance` 快照继续由现有 Collector 发布，应用商店无需维护另一套服务清单。`aifar.runtime` 仍负责 Runtime 工作台的 Deployment、Pod、Endpoint 明细。

## 边界与错误处理

- 动态服务名来自实例 metadata，不再依赖 Go 中固定的 `serviceOrder` 扫描。
- 对服务名和副本数做规范化，忽略空名称并把负数视为 0。
- 检查命令仍只执行受信的后端模板，不接受 API 自由 shell。
- SSH、Docker 或 Agent 检查本身失败时，保持现有 error/unavailable 语义，不伪装成安装成功。
- 不修改数据库结构、安装任务结果或卸载流程。

## 测试

按 TDD 增加回归覆盖：

1. 动态服务名和部分选择只生成对应检查项。
2. 未选择服务不会影响结果。
3. `desiredReplicas = 0` 的已下线服务不会导致失败。
4. 应运行服务缺失时不能报告 `running`。
5. 多副本服务按期望副本数判断。
6. 旧 metadata 缺少副本映射时按已选服务回退。

实现后运行 AIFAR 包测试、完整后端测试和后端构建，并执行 `git diff --check`。
