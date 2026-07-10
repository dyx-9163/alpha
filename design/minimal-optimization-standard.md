# 最小优化标准

本标准用于 AIFAR Deployment 后续“持续优化”。目标是让代码逐步变清晰，同时避免大范围重构带来的行为风险。

## 1. 优先边界

每次只优化一个明确边界：

- 页面编排
- API service
- 状态/composable
- 纯业务规则
- 格式化/选择器
- 后端 domain service
- Store/migration

一次提交不同时横跨多个不相关边界。

## 2. 行为不变

优化默认不改变用户行为、API 路径、请求参数、响应结构、权限、审计和任务语义。

如果必须改变行为，必须显式写清楚原因，并补充对应验证。

## 3. 可抽离条件

满足任一条件才抽离：

- 与 UI 无关的纯函数。
- 可被多个组件或流程复用。
- 页面内形成独立状态机。
- 函数依赖超过 5 个局部状态，且有清晰输入/输出。
- 文件已经承担两个以上职责。

不为了“看起来架构化”新建空壳抽象。

## 4. 类型线

新增模块必须有明确输入/输出类型。

禁止新增 `any` 作为领域数据类型；确实无法避免时，只允许出现在 API 边界或第三方组件边界，并在进入领域模块前转换。

## 5. 文件体量线

前端建议：

- View 只保留页面编排、路由上下文、弹窗入口和 composable 组装。
- 领域组件建议小于 350 行。
- composable/service/helper 建议小于 250 行。
- 超过后优先拆“纯函数”和“状态机”，再拆模板。

后端建议：

- handler 只做解析、鉴权、任务创建、审计和响应。
- 业务规则进 service/domain。
- Store 只负责持久化和迁移。
- 安装/远程执行只通过 installer/adapter。

## 6. 验收线

每轮至少满足：

- 前端变更跑 `pnpm web:build`。
- 后端变更跑 `pnpm test`。
- 前端逻辑测试跑 `pnpm test:web`。
- 启动脚本、配置解析或发布脚本变更跑 `pnpm test:scripts`。
- 发布收口跑 `pnpm release:verify`；完整本地门禁才跑 `pnpm test:local`。
- 不回退用户或已有改动。
- `memory.md` 记录本轮问题与结论。

这就是最小合格线；超过它可以做，但不能低于它。

## 7. 方案 0 冻结边界

当前方案 0 的优化边界是“保留现状、分域收尾”：

- 不重置工作树、不清空重做、不一次性 squash。
- 不新增数据库表或 migration。
- `/api/v2`、终端 WebSocket、SSE payload、任务状态、审计机器码保持兼容。
- Realtime 只做 last-known cache 与乱序保护；tombstone、TTL 和后端 snapshot GC 延后。
- Containers/Runtime 继续保持页面编排、缓存、权限、确认框、任务追踪、watcher 和日志连接生命周期的现有边界。
- 不引入 Vue Test Utils 或 DOM 测试栈。
- 不增加签名、SBOM、provenance、多架构或自动发布。
