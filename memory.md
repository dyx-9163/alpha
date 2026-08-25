# AIFAR Memory

本文件只保留**当天（YYYY-MM-DD）**的精简问题与结论。每次写入前必须执行"写入守卫"流程（见 AGENTS.md）。历史条目自动归档到 `memory/YYYY-MM-DD.md`。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-08-25
- 问题：memory.md 体积过大（989 KB / ~238K tok），本地 9B 模型读取即爆上下文；希望每次写入时自动校验大小并按日期归档，主文件只展示当天内容。
- 结论：新增 memory 写入守卫机制——写入前校验文件大小/行数，超阈值或存在非当天条目时，将非当天条目按日期拆分归档到 `memory/YYYY-MM-DD.md`，主文件仅保留当天 + 头部说明；同步更新 AGENTS.md 将此流程列为强制约定。
- 问题：集群服务密码已轮换后，为什么 aifar-server 仍能正常显示/连接各服务状态。
- 结论：aifar-server 默认使用本地 SQLite，自身不依赖业务 MySQL；状态检测主要通过 SSH 到目标机执行本地探测。MySQL/Redis 检测会使用面板已绑定或默认凭据认证，MinIO/Nacos/AIFAR 应用运行状态大量依赖健康端点、端口、systemd 或 Docker health，状态正常不等同于所有业务账号链路已验证。
- 问题：容器页进入 AIFAR 运行时后 Deployments 首次为空，切到 Pods 再回来才显示。
- 结论：根因是 `ContainersView.vue` 的顶层 tab watcher 进入 `aifar-runtime` 时只套用状态快照，没有主动请求完整 runtime 数据；Pods tab 会触发 `includePods` runtime 请求并把 Deployments 一起补入缓存。修复为进入 Runtime tab 时主动加载对应 runtime 数据，并新增回归测试。
- 问题：Dashboard 顶部 KPI strip 占空间，运行状态区随数据量收缩，页面没有按统一工作区布局撑满。
- 结论：隐藏 Dashboard 顶部 KPI strip，运行状态卡改为固定全高标准布局，列表/详情区域撑满并滚动；移除 Dashboard 对 tasks/alerts KPI 数据的加载，补充对应前端回归测试。
- 问题：用户要求将当前所有未提交文件提交到本地仓库。
- 结论：按用户明确授权，将当前工作区剩余改动统一纳入本地提交；不推送远端。
- 问题：容器页 AIFAR Runtime 中 aifar-agent 已断开/缺失时仍展示缓存或响应中的 Deployments/Pods/服务/入口发现等部署运行时数据。
- 结论：新增 agent running 硬门禁；agent 非 running 时 Runtime 派生实例、部署、服务、Pods、入口发现均归零，Workspace 仅显示 agent 不可用提示，不再渲染部署相关标签页；补充回归测试。
- 问题：容器页切到“镜像”页或在镜像页切换服务器时表格为空，没有主动加载 Docker images collection。
- 结论：在进入“镜像”大页、切换镜像/网络/卷子页、以及镜像页切换服务器时主动加载当前 Docker collection；保留容器页入口使用 15 秒状态快照的策略，并补充回归测试。
