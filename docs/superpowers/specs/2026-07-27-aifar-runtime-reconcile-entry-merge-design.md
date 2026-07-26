# AIFAR Runtime 同步入口合并设计

## 背景

当前 AIFAR Runtime 顶部的“同步运行时”和 Pods 页的“启动/恢复 Pods”都调用前端 `submitRuntimeReconcile`，提交同一个 `/containers/aifar/runtime/reconcile` API，最终执行 `aifar-agent reconcile-runtime --spec`。两者只有位置、文案和任务显示标签不同，重复入口会让用户误以为存在“完整同步”和“仅恢复 Pod”两种后端能力。

## 目标

- 只保留一个 Runtime 期望态同步入口。
- 让按钮名称覆盖修复缺失或停止 Pod、对齐期望副本、刷新入口和发现状态的实际语义。
- 不改变 Runtime reconcile 的后端行为、权限、任务、审计和 Agent 命令。

## 交互设计

- 保留 AIFAR Runtime 顶部操作栏中的“同步运行时”。
- 保留现有“同步运行时”禁用条件、确认弹窗、任务提交和任务追踪行为。
- 从 Pods 页工具栏移除“启动/恢复 Pods”。Pods 页继续保留服务筛选、“刷新”和“刷新指标”。
- 删除仅服务于重复入口的 `recoverAifarRuntimePods` 前端函数、Runtime context 字段，以及 `containers.recoverRuntimePods`、`containers.confirmRecoverRuntimePods` 中英文文案。
- 不把“启动/恢复 Pods”改成顶部按钮的别名，也不新增下拉菜单，避免同一操作继续出现两个入口。

## 代码边界

本次只修改 `web/src`：

- `AifarRuntimePodsTab.vue`：移除重复按钮和 context 解构字段。
- `ContainersView.vue`：移除重复提交函数和 context 暴露。
- `containers/runtime/context.ts`：移除重复 action 类型字段。
- `i18n/messages.ts`：移除不再使用的中英文文案。
- 前端测试：验证工作区仍保留“同步运行时”，Pods 页不再暴露恢复 action。

不修改：

- `/api/v2/containers/aifar/runtime/reconcile` 路由及请求契约。
- `aifar.reconcile` worker 任务、权限和审计。
- `runtime-reconcile.sh` 和 `aifar-agent reconcile-runtime`。
- “全部重启（读取新配置）”及单服务滚动发布能力。

## 错误处理

沿用顶部“同步运行时”现有处理：按钮根据 Runtime 操作禁用原因控制可用性，用户确认后提交任务，提交失败显示后端错误，成功后追踪任务并刷新 Runtime 状态。本次不引入新的错误分支。

## 测试与验收

- 测试先证明当前 Pods 页仍包含 `recoverAifarRuntimePods`，形成预期失败。
- 修改后验证 Pods 页不再引用该 action，工作区仍引用 `reconcileAifarRuntime`。
- 运行全部前端测试 `pnpm test:web`。
- 运行前端生产构建 `pnpm web:build`。
- 页面最终只有一个会提交 Runtime reconcile 的可见按钮，后端和 Agent 文件无变更。

