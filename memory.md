
# AIFAR Memory

本文件记录后续对话的精简问题与结论。每次开始先读，结束前追加。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-07-09
- 问题：用户要求先做第一步，并确认是否可以连接已配置服务器做自动化只读验收。
- 结论：已用临时只读 smoke 命令读取根目录 `data/aifar.db` 并连接 3 台已配置服务器；3 台 SSH 和基础遥测均通过。Docker 运行时闭环未通过：2 台提示 `docker` 命令不存在，1 台提示 Docker daemon 未运行。当前资源 11 条，app_instances 为 0，任务历史 48 成功/11 失败，开放告警 1 条：AIFAR Runtime degraded，payload 显示 7 个 pod 中 readyPods=2。
- 问题：用户要求实现自动化测试计划，包括本地闭环、服务器只读 smoke、受控 mutating E2E 入口和 release checksum 校验。
- 结论：已新增 `store.OpenReadOnlyWithSecret` 防止只读 smoke 误建库/迁移，新增长期命令 `backend/cmd/aifar-smoke`，新增 `pnpm test:local`、`test:server:readonly`、`test:e2e:mutating`、`test:all`、`release:verify`。`pnpm test` 和 `pnpm test:local` 通过；`test:server:readonly` 按预期失败在 3 台服务器 Docker summary；`test:e2e:mutating` 未授权时按预期拒绝执行。
- 问题：用户要求将真实部署 E2E 落地，按 Docker、MySQL、Redis、MinIO、Nacos、AIFAR 顺序通过 `/api/v2` 安装、检查和清理。
- 结论：`backend/cmd/aifar-smoke e2e-mutating` 已实现真实 API runner：默认启动 in-process API，也支持 `AIFAR_E2E_BASE_URL`；必须显式设置 mutation、服务器白名单、登录凭据和应用密码；安装任务成功后要求产生新实例；默认反向清理 AIFAR/Nacos/MinIO/Redis/MySQL，Docker 默认保留，错误的 Docker 清理参数会 fail-fast。报告新增 stages、apiChecks、remoteChecks。已补单元和 fake HTTP 测试；`pnpm test`、`pnpm backend:build` 通过；未授权 `pnpm test:e2e:mutating` 按预期拒绝执行。
- 问题：用户提供 Nacos 安装失败日志，Nacos 以 cluster 模式启动且报 `No DataSource set`，并尝试连接其它节点 `9849`。
- 结论：根因是 Nacos cluster 模式不能使用 local/embedded 数据源；代码原先允许 `cluster + dbSource=local`，导致运行期才失败。已改为后端校验直接拒绝该组合，脚本层也 fail-fast 输出明确原因；standalone 安装会清理残留 `cluster.conf`，避免旧集群配置影响重装。`pnpm test`、`pnpm backend:build` 通过。
- 问题：用户要求直接连接三台服务器查看 Nacos 集群为何没安装起来。
- 结论：已新增只读 `aifar-smoke nacos-diagnose` 并 SSH 连接 3 台服务器。02:21/02:23 诊断时三台均为 cluster 模式、MySQL 配置存在、到 MySQL Router 6446 TCP 可达，但 Nacos 日志报 `No DataSource set` 且 9849 互联 refused，服务 inactive/failed。02:32 复查时三台均已 active，8848/9848/9849/7848 全部监听，readiness URL 均返回 `OK`，MySQL 查询显示 `read_only=0`、`super_read_only=0`、Nacos schema 表 17 张。结论是早期失败属于集群启动/可写 MySQL 选择/节点互联的时序问题，后续 systemd 重试或重新安装后集群已恢复。
- 问题：用户反馈 Nacos 集群实际成功启动，但安装任务在“unable to authenticate to Nacos for credential configuration”处失败。
- 结论：已调整 Nacos 安装脚本：readiness 检测独立成函数，账号配置改为最多 60 次重试；若 Nacos readiness 已 OK 但 auth/user API 仍不可认证，则记录 warning 并继续完成安装记录，避免把已成功启动的集群误判为安装失败。`pnpm test`、`pnpm backend:build` 通过。
- 问题：用户截图显示安装后“凭据中心”为空，没有自动生成任何密钥。
- 结论：根因是安装成功后只绑定“已选择的既有凭据”，手动输入的 MySQL/Redis/MinIO/Nacos 密码没有生成凭据记录。已扩展安装成功回调：若安装时使用手动密码，则自动创建并绑定 app-instance 级凭据；若选择已有凭据，则只绑定不复制。新增任务日志文案和测试覆盖四类应用生成规则。历史已安装实例无法从旧数据还原密码，需要手动补录或用新后端重新安装。`pnpm test`、`pnpm backend:build` 通过。
- 问题：用户提供 MySQL 安装失败日志，服务已 active/running，但安装脚本在等待 socket readiness 后报失败。
- 结论：MySQL 首次初始化可能超过原等待窗口，且不能只依赖 socket 检测。已增强 standalone 安装脚本：启动等待扩到最多 300 秒，支持 socket/TCP bootstrap 检测，最终密码验证最多 120 秒，失败时输出 systemd/journal/error log/端口/socket 诊断，并移除依赖未设置 `MAINPID` 的 ExecStop。`pnpm test`、`pnpm backend:build` 通过。
