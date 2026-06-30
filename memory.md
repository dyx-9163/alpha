# AIFAR Memory

- 问题：用户质疑 Redis Sentinel 表单设计，询问是否应支持多主、多副本、多 Sentinel 节点，并要求按官方模型设计。
- 结论：官方 Sentinel 是可监控一个或多个 master group 的高可用机制，不是单组多主分片；本次安装模型明确为一个 master group：一个 Redis master、其余 replica、所有选中服务器运行 Sentinel。前端新增 `masterName` 监控组名称并调整提示/字段文案，后端 Sentinel 配置不再硬编码 `aifar-master`，改用 `sentinel monitor <masterName>`；`go test ./internal/apps/redis ./internal/installer/redis`、`pnpm web:build`、`pnpm test`、`pnpm backend:build`、`git diff --check` 已通过。

- 问题：用户反馈 Redis 单体安装弹窗不应展示 Sentinel/Cluster 相关参数。
- 结论：`web/src/apps/redis/i18n.ts` 已为 `sentinelPort`、`quorum` 增加 Sentinel 拓扑可见条件，为 `replicas` 增加 Cluster 拓扑可见条件；单体模式只展示 Redis 端口和密码；`pnpm web:build`、`pnpm test` 已通过。

- 问题：用户询问 MySQL `server-id` 是按什么规则生成的。
- 结论：当前规则在 `backend/internal/installer/mysql/installer.go` 中用 FNV-1a 32 位哈希计算，输入为 `server.ID|server.Host|port`，保证同一服务器同一端口稳定、不同服务器通常不同；若哈希结果为 0 则兜底为 1。

- 问题：用户反馈任务中心多服务器日志右上角服务器选择器不应展示“全部”和“控制面”，且控制面日志应固定在底部，不随服务器筛选隐藏。
- 结论：`TaskRunPanel.vue` 的目标选择器已改为只列出真实服务器；日志展示改为选中服务器日志后追加控制面日志，控制面分组始终排在最后；`pnpm web:build`、`pnpm test`、`git diff --check` 已通过。

- 问题：用户反馈 MySQL 安装成功后，单体和 InnoDB Cluster 的成功日志都显示为“单体安装成功”。
- 结论：共享 MySQL 基础安装脚本改为中性 `MySQL service` 文案，InnoDB Cluster 节点实例记录改用 `ClusterNodeInstalled`/兜底“集群节点已安装”文案，并补充测试确保集群日志不再出现 `MySQL standalone installed`；`go test ./internal/apps/mysql ./internal/installer/mysql`、`pnpm test`、`pnpm backend:build`、`git diff --check` 已通过。

- 问题：用户上传新的 MySQL InnoDB Cluster 日志，基础安装已成功，但 bootstrap 阶段 `dba.configureInstance` 需要修改 `gtid_mode`、`enforce_gtid_consistency`、`server_id` 等并尝试远程重启，最终报 `mysqld is not managed by supervisor process`。
- 结论：MySQL 安装脚本已在 `my.cnf` 中预置 InnoDB Cluster 所需的 GTID、binlog、WRITESET、relay log、log_replica_updates 配置，并由安装器按服务器 ID/主机/端口生成稳定非零 `server-id`，避免 `mysqlsh` bootstrap 阶段再要求重启；`pnpm test`、`pnpm web:build`、`pnpm backend:build`、`git diff --check` 已通过。

- 问题：用户上传 MySQL 安装日志，显示已有数据目录时服务已 `active (running)`，但旧脚本停在 `waiting for MySQL socket` 并报 `MySQL socket is not ready after installation`。
- 结论：该日志命中已修复问题，原因是运行的仍是旧后端/旧打包产物；当前源码已改为 `waiting for MySQL service readiness`，已有数据目录会用配置管理员密码探测并输出凭据不匹配提示，需要重新构建并重启后端后再安装。

本文件记录后续对话的精简问题与结论。每次开始先读，结束前追加。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-06-29
- 问题：用户反馈 MySQL 多服务器安装没有在同一任务内三台一起执行，任务中心多目标日志堆叠难查，并且已有数据目录场景仍可能在基础安装阶段报错。
- 结论：MySQL InnoDB Cluster 基础安装阶段已按面板 `deploymentConcurrency` 在单任务内并发执行，全部目标基础安装成功后才进入 bootstrap；任务中心多目标日志右上角新增服务器选择器；MySQL 安装脚本在已有数据目录时改用配置的管理员密码探测服务，并给出凭据不匹配的明确日志；`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。
- 问题：用户要求整理当前代码功能，重写 `AGENTS.md` 和 `SKILL.md`，并要求后续所有问答都先读 `memory.md`、结束前精简记录问题与结论。
- 结论：已建立 memory 工作流，并将当前项目定位为 Go/Chi/SQLite 后端 + Vue3/Element Plus/Vite 前端 + Docker/MySQL/Redis/MinIO 离线安装、任务系统和审计日志的运维面板。
- 问题：用户要求把启动端口配置也放入 `config/defaults.env`。
- 结论：已新增 `AIFAR_ADDR=0.0.0.0:8080`，并让 `scripts/toolchain.mjs` 优先读取 defaults.env 中的 `AIFAR_ADDR`。
- 问题：用户询问后端 8080 端口和本地开发 Vite 5173 端口能否合并。
- 结论：生产/打包运行可只用 8080，由 Go 后端托管 `web/dist`；本地开发不建议让 Go 和 Vite 同时占用同一端口，当前 5173 通过 Vite proxy 转发 `/api/v2` 到 8080。
- 问题：用户询问当前建议优先优化什么。
- 结论：建议优先做配置一致性、消除已暴露 UI 的 stub 能力、生产安全默认值、安装任务可观测性与测试补强。
- 问题：用户要求执行“让本地开发代理跟随 defaults.env 的 AIFAR_ADDR”优化。
- 结论：已更新 `web/vite.config.ts` 从 `config/defaults.env` 或环境变量读取 `AIFAR_ADDR`，并将 `0.0.0.0`/`::` 转为 `127.0.0.1` 作为 Vite proxy 目标；`pnpm web:build` 已通过。
- 问题：用户删除 Docker 部署服务时失败，原因是目标机还有通过 dnf 安装的系统 Docker，状态检测返回 `status=stopped`。
- 结论：已修正 Docker 删除校验：只要求 AIFAR 管理的安装目录和 systemd unit 被移除，不再要求整机 Docker 命令消失；补充了外部 Docker 仍存在时删除成功的测试，`pnpm test` 已通过。
- 问题：用户要求新增可复用拖拽公共组件，并且服务器清单拖拽后的顺序需要入库。
- 结论：新增 `web/src/components/DraggableList.vue`，服务器清单已接入拖拽；后端新增 `servers.sort_order`、`PUT /api/v2/servers/order` 和排序持久化测试，搜索状态下禁用拖拽以避免保存局部过滤顺序；`pnpm test`、`pnpm web:build` 均已通过。
- 问题：Windows 防火墙弹窗询问是否允许 `aifar-server.exe` 访问公共/专用网络，原因是本地开发启动时监听了 `0.0.0.0:8080`。
- 结论：已将本地开发默认监听改为 `127.0.0.1`：新增 `AIFAR_DEV_ADDR=127.0.0.1:8080` 和 `AIFAR_VITE_HOST=127.0.0.1`，`pnpm dev` 使用开发地址，生产 `AIFAR_ADDR=0.0.0.0:8080` 保持不变；`pnpm web:build` 已通过。
- 问题：多台服务器批量安装 Docker 时失败，远程日志显示 `tar is required`，但资源包已上传 `tar` 和 `gzip` RPM。
- 结论：原因是 Docker 安装脚本先检查 `tar` 再安装本地 RPM，导致缺 `tar` 的主机无法通过上传的 `tar-*.rpm` 自修复；已调整为先安装本地 RPM，再检查 `tar`/`gzip`，并补充脚本顺序测试；`pnpm test` 已通过。
- 问题：用户要求任务中心增加全选删除任务日志操作。
- 结论：任务列表新增“全选当前筛选”与已选数量展示；任务工具栏新增“清空所选日志”，后端新增 `DELETE /api/v2/tasks/logs` 批量清空日志接口并保留任务、目标和步骤记录；`pnpm test`、`pnpm web:build` 均已通过。
- 问题：Docker 安装需要开放远程 API、安装页校验 Docker 默认网段冲突、卸载后命令提示更友好、终端 SSH 不通、容器页去掉 Local Docker、多服务器部署日志右侧看不完整。
- 结论：Docker 安装脚本已启用 `tcp://0.0.0.0:2375` 远程 API 并支持自定义桥接 CIDR/API 端口，服务器登记为 `tcp://host:port`；安装弹窗新增 IPv4 CIDR 冲突校验；卸载脚本执行 `hash -r` 并提示既有 shell 清缓存；Vite WebSocket proxy 与终端 token 查询兜底已修复；容器页只显示已登记 DockerHost 的服务器并禁用本机默认 Docker；部署日志右侧详情改为可滚动完整查看；`pnpm test`、`pnpm web:build` 均已通过。
- 问题：用户要求全量扫描前后端，列出适合抽成公共组件/公共模块的点，并说明企业级开发运维方向原因。
- 结论：扫描确认前端优先抽页面骨架、数据表格/操作列、高危确认、目标选择器、任务/日志面板、动态安装表单、终端面板和通用 composables；后端优先抽 HTTP 响应/错误、任务生命周期执行器、安装器工具包、应用部署生命周期、参数校验、资源包校验、审计发射器、仓储拆分、远程命令策略和 WebSocket/SSE 网关。
- 问题：用户要求“按照建议顺序来”，依次推进后端公共模块、前端公共组件、`store.go`/`httpapi` 拆分。
- 结论：已新增 `taskrun`、`installerkit`、`apperror`，Docker/MySQL 安装器复用公共远程执行与脚本工具，MySQL 服务复用任务步骤 runner；前端新增 `DataTable`、`ServerSelector`、`DangerConfirm`、`TaskRunPanel` 并接入工具箱、安装弹窗、容器页和任务中心；拆出 `httpapi/response.go` 及 store 的 `audit.go`、`app_instances.go`、`storage_items.go`、`settings.go`；`pnpm test` 和 `pnpm web:build` 通过。
- 问题：用户追问是否还有需要提出为公共组件/公共模块的点。
- 结论：建议继续提炼前端 `PageHeader/PageShell`、`ActionToolbar/FilterBar`、`AppInstanceTable`、`TaskTable`、`SecretConfirmPrompt`、`LogDrawer/TerminalOutput`、`useServerMap`、`useTaskPolling/useSSE`；后端继续提炼应用服务生命周期 runner、Redis/MinIO installerkit 接入、资源包选择与 checksum 校验、删除确认生命周期、审计发射器、HTTP 模块路由拆分、任务计划落库器、敏感信息脱敏日志器和 SSH 命令策略。
- 问题：用户要求继续“按照建议来”抽公共组件和公共模块。
- 结论：已新增后端 `resourcekit` 统一资源选择、RPM 枚举和 SHA256 校验，Redis/MinIO 安装与卸载复用 `installerkit`；前端新增 `PageShell`、`AppInstanceTable`、`SecretConfirmPrompt` 并接入应用商店实例/部署记录与删除确认；`pnpm web:build` 和 `pnpm test` 已通过。
- 问题：用户询问是否还有需要提炼为公共组件的点。
- 结论：建议下一批优先抽流程型组件/模块：前端 `ConfirmAction`、`TaskTable`、`RunRecordTable`、`TabWorkspace`、`LogDrawer/TerminalOutput`、`KeyValueGrid/MetricGrid`、`ResourceHealthCard`；后端抽删除生命周期 runner、任务计划落库器、资源上传清单、审计发射器、HTTP feature router、敏感日志脱敏器和 SSH 命令策略。
- 问题：用户要求持续优化，直到达到企业级开发和运维部署工具标准。
- 结论：已创建持续目标；本轮新增前端 `ConfirmAction`、`useConfirmAction`、`RunRecordTable`，并接入审计、任务日志、数据库、对象存储和服务器删除确认；后端新增 `apps/deleteflow` 并接入 MySQL/Redis/MinIO 删除生命周期；`pnpm web:build` 和 `pnpm test` 已通过。
- 问题：持续目标推进，继续补强企业级任务可观测性和日志排障体验。
- 结论：已新增后端 `taskplan` 统一安装计划落库，并让应用安装入口复用该模块；前端新增 `LogOutput`、`LogDrawer`，容器日志抽屉和任务运行详情已复用公共日志输出；`pnpm web:build` 和 `pnpm test` 已通过。
- 问题：持续目标推进，继续收敛远程资源上传链路和运维概览展示。
- 结论：已新增后端 `installer/uploadkit` 统一上传日志、mode、失败包装和 RPM 清单展开，Docker/MySQL/Redis/MinIO 安装器已接入；前端新增 `KeyValueGrid`、`MetricGrid`，容器、Dashboard、服务器、数据库、对象存储、设置页面已复用；`pnpm web:build` 和 `pnpm test` 已通过。
- 问题：持续目标推进，补强企业级任务日志、审计日志和错误字段的敏感信息保护。
- 结论：已新增后端 `logmask` 统一脱敏和 `auditkit` 统一审计入口；任务 target/error/log、任务目标/步骤错误、服务器 lastError、审计 target/message 落库前均会脱敏；`httpapi` 审计入口已接入 `auditkit`，`pnpm test` 已通过。
- 问题：持续目标推进，补齐企业级后端权限边界，避免登录用户默认拥有所有高风险操作能力。
- 结论：已新增后端 `rbac` 权限模型，定义 owner/admin/operator/viewer/auditor 与 settings/resources/servers/terminal/tasks/audit/apps/containers/database/storage 权限点；HTTP 高风险写操作、终端入口和审计删除已接入 `requirePermission`，权限拒绝支持中英文文案；新增 HTTP 路由级权限测试，`pnpm test` 已通过。
- 问题：持续目标推进，补强 RBAC 后的会话一致性，避免改密码或角色变更后旧 token 继续生效。
- 结论：已为用户表新增 `token_version` 迁移，JWT claims 携带版本号；`ResetUserPassword` 和 `SetUserRole` 会递增版本，`requireAuth` 会校验用户仍存在、版本一致，并用数据库当前角色刷新请求上下文；登录响应返回 `tokenVersion` 和权限列表；新增旧 token 被密码重置撤销的 HTTP 测试，`pnpm test` 已通过。
- 问题：用户要求持续优化并新增“每次调整完成后推送 GitHub”的流程要求，本轮推进 RBAC 前端体验闭环。
- 结论：已新增前端 `rbac.ts` 与 `usePermissions`，session 持久化后端返回的权限列表；终端菜单/路由、应用安装/检测/删除、服务器保存/探测/删除/排序、任务清理/删除、审计删除、资源重扫、设置保存、容器启停、数据库备份/检测、对象存储写操作均按权限禁用或拦截；401 会清理本地 session；`pnpm web:build` 和 `pnpm test` 已通过。
- 问题：按用户要求尝试将本轮调整推送到 GitHub。
- 结论：已在本地创建分支 `codex/enterprise-permission-ux` 并提交 `5da12610 enterprise permission and operations hardening`；`git push` 被安全审查拦截，原因是向外部 GitHub 远端导出工作区代码需要用户在知情后再次明确批准，当前尚未推送成功。
- 问题：持续目标推进，补齐企业级安全事件审计，要求登录失败和权限拒绝都能留痕。
- 结论：已在登录失败时记录 `auth.login` 失败审计，在权限拒绝时记录 `auth.permission.denied` 审计，包含账号、权限点、HTTP 方法和路径，并继续走 `auditkit`/`logmask` 脱敏链路；新增 HTTP 测试验证 401/403 安全事件入库，`pnpm web:build` 和 `pnpm test` 已通过。
- 问题：持续目标推进，补强登录暴力尝试防护，要求安全审计之后能有实际阻断动作。
- 结论：已新增后端 `security.LoginGuard`，按账号+来源统计失败次数并临时锁定；新增 `AIFAR_AUTH_MAX_FAILURES`、`AIFAR_AUTH_LOCKOUT_SECONDS` 到配置和 `defaults.env`；登录失败达到阈值后返回 429 和 `Retry-After`，并记录 `auth.login.locked` 审计；设置接口只读暴露当前锁定策略；`pnpm test` 已通过。
- 问题：持续目标推进，补齐企业级 Web 安全基线，要求统一安全响应头和请求体大小限制。
- 结论：已新增 HTTP 安全头中间件，统一输出 `X-Content-Type-Options`、`X-Frame-Options`、`Referrer-Policy`、`Permissions-Policy`；新增 `AIFAR_MAX_REQUEST_BODY_BYTES` 配置和 `defaults.env` 默认值，请求体超限返回 413；设置接口只读暴露当前 body 限制；新增 HTTP 测试覆盖安全头、413 和设置响应，`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：用户明确后续“只把代码提交到本地仓库就行”，不再要求每次推送 GitHub。
- 结论：后续每轮调整完成后只执行本地 commit，不再尝试 `git push`，除非用户之后再次明确要求推送。
- 问题：持续目标推进，补齐企业级长期运行下的任务与审计日志保留清理能力，避免 SQLite 控制面数据无限增长。
- 结论：已新增 `AIFAR_AUDIT_RETENTION_DAYS`、`AIFAR_TASK_RETENTION_DAYS` 配置和设置页展示；新增后端 `maintenance` 服务、`POST /api/v2/maintenance/retention/run` 维护任务入口，按步骤清理过期审计日志和已结束任务；store 层支持按截止时间级联删除任务日志/步骤/目标；设置页新增“执行保留清理”按钮；`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：持续目标推进，补齐企业级控制面数据库备份能力，避免清理、升级或迁移前缺少可回滚快照。
- 结论：已新增 `AIFAR_DATABASE_BACKUP_DIR` 配置和设置页展示；store 层使用 SQLite `VACUUM INTO` 生成一致性备份并计算 SHA256；后端新增 `POST /api/v2/maintenance/database-backup/run` 任务入口，按“准备目录、备份数据库、校验文件”记录步骤和审计；设置页新增“创建数据库备份”按钮；新增 store 与 HTTP 测试验证备份文件可打开且包含控制面数据；`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：持续目标推进，补齐控制面数据库备份的可发现和可治理能力，避免只生成备份但无法查看、校验或清理。
- 结论：后端维护服务新增备份清单扫描和安全删除，只允许删除配置备份目录下符合 `aifar-control-plane-*.db` 的文件名；新增 `GET/DELETE /api/v2/maintenance/database-backups`，删除操作记录审计并对非法文件名返回 400；设置页新增数据库备份表格，展示文件名、大小、SHA256、时间并支持确认删除；新增维护服务和 HTTP 测试覆盖清单、删除和路径穿越拒绝；`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：持续目标推进，补齐控制面数据库备份的异地保存能力，避免备份只能留在面板服务器本地目录。
- 结论：后端维护服务新增单个备份文件解析复用安全文件名与目录约束；新增 `GET /api/v2/maintenance/database-backups/{name}/download`，通过授权请求下载备份并返回 SHA256/大小响应头，非法文件名返回 400、缺失文件返回 404；前端 API client 新增 `apiDownload`，设置页备份表格新增“下载”按钮；新增 HTTP 和维护服务测试覆盖下载、校验头和非法名称拒绝；`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：持续目标推进，补齐控制面数据库备份的恢复前校验能力，避免归档或恢复时才发现备份损坏或缺少关键表。
- 结论：后端维护服务新增 `VerifyDatabaseBackup`，对指定备份执行 SQLite `integrity_check` 并校验 users/servers/tasks/audit/resources/settings 等关键表；新增 `POST /api/v2/maintenance/database-backups/{name}/verify` 任务入口，按“定位备份、完整性检查、关键表检查”记录步骤和审计；设置页备份表格新增“检查”按钮；新增维护服务与 HTTP 测试覆盖成功校验任务；`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：用户反馈优化逻辑不要过于精细，不要什么都拆分成最小粒度，也不要过度依赖设计模式。
- 结论：后续优化优先保持功能之间清晰解耦、复用公共组件和公共能力，但避免为了抽象而抽象；简单功能可直接放在现有模块中，防止形成代码屎山或过度工程化。
- 问题：持续目标推进，补齐控制面自身健康检查能力，便于企业部署后接入进程守护、反向代理和监控探活。
- 结论：新增 `GET /api/v2/health/live`、`GET /api/v2/health/ready` 无鉴权探活接口，登录后 `GET /api/v2/health` 返回数据库、资源目录、静态目录和备份目录状态；store 新增轻量 `Ping()`；设置页新增“控制面健康”展示并复用现有 `KeyValueGrid`/`StatusTag`；`StatusTag` 支持 `ok/degraded`；新增 HTTP 测试覆盖 live/ready/detail；`pnpm test` 与 `pnpm web:build` 已通过。
- 问题：用户要求优化不要过度精细化，当前设置页的数据维护和控制面健康逻辑开始堆在页面里，需要按功能边界解耦但避免设计模式化。
- 结论：新增 `DataMaintenancePanel.vue` 和 `ControlPlaneHealthPanel.vue`，设置页只保留语言、并发、服务商/模块状态等页面编排；数据备份/清理和健康探活各自组件化管理，`pnpm web:build` 与 `pnpm test` 通过。
- 问题：持续目标推进到账号与角色治理，同时用户要求这个目标完成后先停止优化，因为还有业务代码未完成。
- 结论：新增 `users.manage` 权限、用户管理 API、审计记录和最后一个 owner 保护；设置页新增 `UserManagementPanel.vue` 支持账号列表、新增账号、修改角色和重置密码；`pnpm test`、`pnpm web:build` 与 `git diff --check` 通过；本轮提交后先暂停继续优化，不再主动开新优化任务。
- 问题：目标续跑上下文再次触发，但用户上一条已明确要求账号与角色治理完成后先停止优化，等待业务代码完成。
- 结论：本轮未继续做新优化，只确认工作区状态并记录暂停约定；后续除非用户明确恢复优化或提出具体任务，否则不主动推进长期优化目标。
- 问题：用户要求修复任务中心日志外层滚动、终端输出贴边、MinIO 安装磁盘选择、数据库类默认密码以及 Redis Sentinel 主从哨兵选择。
- 结论：任务中心详情改为页面内固定高度、日志框内部滚动；终端输出框铺满并保留内边距；MySQL/Redis/MinIO 安装弹窗默认密码已改为约定默认值；MinIO 安装会按目标服务器检测独立数据磁盘并回退系统盘；Redis Sentinel 模式必须选择主节点，未选主节点会在前后端校验拦截；`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。
- 问题：用户要求 MySQL 单体安装时不要展示集群信息。
- 结论：MySQL 安装弹窗的集群名称字段已增加可见条件，仅在 `innodb-cluster` 拓扑下展示；单体安装不会再展示或提交该字段；`pnpm web:build`、`pnpm test`、`git diff --check` 已通过。
- 问题：用户反馈 MySQL InnoDB Cluster 初始化失败，任务日志显示 bootstrap 阶段找不到 `mysqlsh`。
- 结论：确认 MySQL 离线 bundle 内已有 MySQL Shell 包，但安装脚本未解包；已让 MySQL 基础安装同步解出并安装 bundle 内置 MySQL Shell，集群初始化优先调用安装目录内的 `mysqlsh`，前端提示也改为内置离线安装；`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。
- 问题：用户要求任务中心任务执行按面板设置里的并发量自动并发，并补充 MySQL InnoDB Cluster 初始化时报 `root@%` 已存在导致 `clusterAdminPassword` 不允许。
- 结论：worker 任务管理器已接入 `deploymentConcurrency` 设置，任务先保持 pending，拿到并发槽后才进入 running，设置保存时会归一化到 1-20；MySQL 安装会写入 `report_host`，集群 bootstrap 遇到已存在集群管理员账号时会复用账号重试；`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。
- 问题：用户要求 Redis Sentinel 安装表单按官方模型改为显式选择 1 个 Redis master、多个 replica、多个 Sentinel 节点，而不是只从目标服务器里指定一个 master。
- 结论：已将 Redis Sentinel 前端安装弹窗改为角色化配置：Master 单选、Replica 多选、Sentinel 多选，并在隐藏默认目标选择器时自动提交三组节点并集 `serverIds`；后端按角色安装和记录实例，支持专用 Sentinel 节点只安装 Redis 二进制与 Sentinel 服务，不启动无意义的数据服务；旧 `serverIds + sentinelMasterId` 调用仍兼容；`pnpm test`、`pnpm web:build`、`pnpm backend:build`、`git diff --check` 均通过。
- 问题：用户反馈 Redis Sentinel 安装弹窗样式不对，角色标签换行错位，并询问 `Sentinel Quorum` 这一行是否可以不要。
- 结论：已调整通用安装弹窗 label 宽度与不换行样式，缩短 Redis Sentinel 角色字段标签为 Master/Replica/Sentinel 节点，并移除前端 `Sentinel Quorum` 输入行；后端继续按 Sentinel 节点数自动计算默认 quorum；`pnpm web:build` 和 `git diff --check` 已通过。
## 2026-06-29
- 问题：用户要求所有安装服务都以面板设置中的并发数为准进行并发执行。
- 结论：新增 `taskrun.RunTargets` 作为统一目标并发执行器，Docker 多服务器安装、Redis Sentinel/Cluster、MinIO distributed 和旧 `offlineapp` 多目标安装均已接入 `RunContext.Concurrency`；MySQL InnoDB Cluster 继续使用已有并发参数路径。新增/更新并发上限测试，`pnpm test` 与 `git diff --check` 已通过。
- 问题：用户截图反馈 Redis 连接报 `DENIED Redis is running in protected mode ... no password is set for the default user`。
- 结论：原因是 Sentinel 配置只设置了 `sentinel auth-pass`，没有给 Sentinel 自身客户端连接设置密码；已在 `sentinel.conf` 渲染 `requirepass $REDIS_PASSWORD`，并让 Sentinel 自检命令带 `-a` 认证；`go test ./internal/installer/redis ./internal/apps/redis`、`pnpm test`、`git diff --check` 已通过。
- 问题：用户反馈 Redis Sentinel 第 4/5 步在 `systemctl enable --now` 后失败，只看到 systemd symlink 输出和进程退出。
- 结论：Sentinel 自身认证改为 Redis 7 官方 ACL 方式，配置默认用户 ACL 与 `sentinel sentinel-pass`，保留监控 Redis master 的 `sentinel auth-pass`；启动和自检失败时会输出 `systemctl status`、`journalctl` 与 Sentinel 配置片段；`go test ./internal/installer/redis ./internal/apps/redis`、`pnpm test`、`git diff --check` 已通过。
- 问题：用户截图反馈 Navicat 连接 Redis Sentinel 报 `With sentinel, connection timeout and socket timeout cannot be 0`，并说明集群已创建好，先不要改代码。
- 结论：该错误是 Navicat Sentinel 模式的本地参数校验，尚未真正连到 Sentinel/Redis；需要在 Navicat 高级设置里把 connection timeout 和 socket timeout 改成非 0 值，再按 Sentinel 主机端口、master group 名称和认证信息测试连接。
- 问题：用户补充 Navicat 常规页参数和三台服务器 `ss -lntp` 监听截图，说明 Sentinel/Redis 配置看起来正常但 Navicat 仍报同一 timeout 校验错误。
- 结论：截图可确认三台机器 `26379` 和 `6379` 正在监听，常规页主机、端口、组名与认证方向不像根因；该报错仍应优先定位 Navicat 高级页的 connection/socket timeout 字段是否为空或为 0，或 Navicat 未保存这些高级参数。服务端可用 `redis-cli -p 26379 SENTINEL get-master-addr-by-name <master>` 与 Windows `Test-NetConnection` 做旁路验证。
- 问题：用户用 `redis-cli` 带 Sentinel 密码查询 master group，已正常返回当前 master 地址和 Redis 端口。
- 结论：Sentinel 认证、master group 名称和 Sentinel 到 Redis master 发现链路均正常；Navicat 报错可进一步判定为客户端自身 Sentinel timeout 配置校验或客户端参数未保存生效，不是 AIFAR 安装出的 Redis/Sentinel 集群不可用。
- 问题：用户反馈 Navicat Redis Sentinel 连接已恢复正常，并截图显示已能展开 Redis 数据库列表。
- 结论：Redis Sentinel 集群和客户端连接链路已验证可用；先前问题确认为 Navicat 客户端连接参数/超时设置导致，不需要修改 AIFAR 代码。
- 问题：用户反馈数据库页面将 MySQL InnoDB Cluster 和 Redis Sentinel 的节点拆成多个独立卡片展示，集群应该放在一起。
- 结论：数据库实例页已改为按集群/拓扑聚合展示：多节点拓扑优先用 metadata 中的 `clusterId` 聚合，卡片内按 master/replica/sentinel 节点行展示服务器、Endpoint、状态和操作；standalone 仍显示为单节点卡片；Redis 设置说明同步为支持 standalone、Sentinel 与 Cluster；`pnpm web:build` 和 `git diff --check` 已通过。
- 问题：用户询问数据库集群卡片中的 `Endpoint` 是否就是 master 节点。
- 结论：Redis Sentinel 卡片的 Endpoint 当前表示 master group 指向的 Redis master 数据节点；MySQL InnoDB Cluster 卡片的 Endpoint 当前只是聚合卡选择的代表/种子节点，不应严格理解为实时 primary，后续展示文案宜区分“当前 Master/Primary”和“接入端点”。
## 2026-06-29
- 问题：用户要求 Redis Sentinel 显示“当前 Master”或“Master Group”，MySQL InnoDB Cluster 需要通过检测任务查询当前 primary 后回写并展示。
- 结论：MySQL 模块已实现检测任务，检测会查询 InnoDB Cluster 当前 primary 并回写同组实例的 `currentPrimaryEndpoint` 与 primary/secondary 角色；数据库页面会按拓扑动态显示“当前 Master”“Master Group”“当前 Primary”或“接入端点”。`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。
## 2026-06-29
- 问题：用户要求在应用商店新增 MySQL Router 安装入口，并且只有已有 MySQL InnoDB Cluster 时才能选择安装。
- 结论：新增独立 `mysql-router` 后端应用模块和前端应用商店模块，复用 MySQL 离线 bundle 中的 Router 包；前端无 InnoDB Cluster 时禁用部署入口，后端安装校验必须传入已有 `clusterId` 并解析 bootstrap endpoint；Router 支持多目标并发安装、记录独立实例、检测和卸载。`go test ./internal/installer/mysql ./internal/apps/mysqlrouter`、`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。

## 2026-06-29
- 问题：用户要求 MySQL Router 在数据库页加入对应 MySQL InnoDB Cluster 卡片中展示，而不是作为独立数据库集群。
- 结论：`/database/instances` 已返回 `mysql-router` 实例；数据库页按 Router metadata 的 `clusterId`/`clusterName` 将 Router 聚合到对应 MySQL InnoDB Cluster 卡片，单独展示 Router 数量、Router 端点和检测操作，数据库节点数仍只统计 MySQL/Redis 节点。`pnpm web:build`、`pnpm test`、`git diff --check` 已通过。

## 2026-06-29
- 问题：用户反馈数据库集群卡片里的端点信息被省略号截断，希望完整展示。
- 结论：数据库页的当前 Master/Primary/接入端点和 Router 端点字段改为跨列展示并允许换行；Router 端点摘要从“首个 +N”改为完整端点列表。`pnpm web:build` 和 `git diff --check` 已通过。

## 2026-06-29
- 问题：用户要求数据库集群卡片显示哪些节点在线、哪些是 master/primary、哪些是其他节点。
- 结论：数据库页节点行新增角色标签和在线状态标签；Redis Sentinel 按当前 master endpoint 标记 master/replica/sentinel，MySQL InnoDB Cluster 优先使用检测写回的 currentPrimaryEndpoint 标记 primary/secondary，未检测时按卡片排序首个节点兜底。`pnpm web:build` 和 `git diff --check` 已通过。

## 2026-06-29
- 问题：用户要求数据库页所有状态实时监测，包括是否在线以及当前是什么节点角色。
- 结论：Redis 模块新增检测能力，检查 Redis/Sentinel 运行态并回写当前 master 与 master/replica/sentinel 角色；数据库页新增默认开启的实时监测循环，自动提交 MySQL、Redis、MySQL Router 检测任务，任务完成后刷新在线/离线和 primary/master/router 等节点状态。`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。

## 2026-06-29
- 问题：用户要求 MinIO 安装按存储方式分为直接使用本地磁盘目录和使用未挂载磁盘两种情况。
- 结论：MinIO 安装弹窗新增存储方式选择；本地目录模式只创建数据目录，不再自动探测/回退独立磁盘；未挂载磁盘模式要求填写 /dev/ 设备，后端会校验未挂载、格式化 ext4、写入 fstab 并挂载到数据根路径后再安装。`go test ./internal/apps/minio ./internal/installer/minio`、`pnpm test`、`pnpm web:build`、`git diff --check` 已通过。

## 2026-06-30
- 问题：用户要求 MinIO 未挂载磁盘模式下的磁盘设备不要手工输入，而是由接口检测出来。
- 结论：新增服务器磁盘检测能力和 GET /api/v2/servers/{id}/disks 接口，通过已保存 SSH 凭据读取 lsblk -J 并返回未挂载候选设备；安装弹窗新增 server-disk-select 字段类型，MinIO 会按已选服务器逐台展示磁盘下拉并提交 serverId 到设备路径的映射；后端 MinIO 安装继续兼容旧字符串参数，并按目标服务器二次校验设备。go test ./internal/servers ./internal/apps/minio、pnpm test、pnpm web:build、git diff --check 已通过。

## 2026-06-30
- 问题：用户担心服务器磁盘检测接口返回 ISO、光驱或其他非目标设备，要求只返回未挂载磁盘。
- 结论：服务器磁盘检测命令排除常见 loop/sr 设备，接口响应仅保留候选未挂载 disk/part，并过滤只读、可移动、ISO/UDF、已挂载和带分区整盘；补充测试覆盖非候选设备不返回。`go test ./internal/servers`、`pnpm test`、`git diff --check` 已通过，`memory.md` 仍有既有 CRLF 提示。

## 2026-06-30
- 问题：服务器磁盘检测需要避免返回 ISO、光驱、可移动盘、分区或系统盘，MinIO 未挂载磁盘模式只应让用户选择整块未挂载数据盘。
- 结论：服务器磁盘接口响应已收窄为仅返回候选整盘 `TYPE=disk`，且必须未挂载、无分区、非只读、非可移动、非 ISO/UDF；测试覆盖系统盘、未挂载分区、USB 盘和光驱不返回。`go test ./internal/servers`、`pnpm test`、`git diff --check` 已通过。

## 2026-06-30
- 问题：用户指出 MinIO 未挂载磁盘安装不应每台服务器只能单选磁盘，应按 MinIO 官方多盘挂载逻辑支持多选。
- 结论：安装弹窗的服务器磁盘选择已支持每台服务器多选，提交 `serverId -> []disk`；MinIO 后端兼容旧单盘参数并支持多盘数组，未挂载磁盘模式按 `dataRoot/diskN` 逐盘格式化挂载，MinIO volume 使用 `dataRoot/diskN/minio`，分布式配置会汇总所有节点所有 volume。`go test ./internal/installer/minio ./internal/apps/minio`、`pnpm web:build`、`pnpm test`、`git diff --check` 已通过。

## 2026-06-30
- 问题：用户要求数据库页状态必须反映数据库/Redis 服务实时探测结果，不再把服务器在线或安装状态当成数据库在线；MySQL Primary、Redis Master/Replica 也必须来自探测结果，并移除数据库页检查和备份入口。
- 结论：MySQL InnoDB Cluster 检测只把实际被检查节点标为 running，同组仅同步当前 Primary 元数据；Redis Sentinel 测试补充未检查节点状态不被顺带改为 running；数据库页不再把 installed 当在线，Primary/Master 只读 currentPrimaryEndpoint/currentMasterEndpoint，任一节点异常或未知时集群汇总降级，行内检查/备份按钮和备份页签已移除。验证通过：go test ./internal/apps/mysql ./internal/apps/redis、pnpm test、pnpm web:build、git diff --check。

## 2026-06-30
- 问题：用户要求整理 config/defaults.env 为中英双语分类配置，并评估脚本、模块配置、版本号和前后端模块结构是否过度拆分。
- 结论：defaults.env 已按控制面服务、本地开发、初始化与安全、部署运行时、API 限制、维护保留、工具链覆盖分类并补齐中英注释；MySQL/Redis/MinIO/MySQL Router/Docker 不再在前后端 catalog 写死 fallback version，展示和安装版本来自 resources/<app>/<version> 扫描；脚本外置建议采用“内置 go:embed 默认模板 + config/installers 覆盖模板”的生产结构，不建议把未接入的模块变量硬写进 env。验证通过：pnpm web:build、pnpm test、git diff --check。

## 2026-06-30
- 问题：用户确认按“方便运维、方便开发”的方向继续进行模块和配置收敛。
- 结论：新增 installerkit.RenderTemplate 统一安装脚本渲染入口，默认优先读取 config/installers/<app>/<template> 覆盖模板，缺失时回退 go:embed 内置模板；Docker/MySQL/Redis/MinIO 安装脚本均已接入该入口；defaults.env 新增 AIFAR_INSTALLER_TEMPLATE_DIR 并补充 config/installers/README.md；前端应用模块移除 fallbackVersion 字段，版本只由后端资源扫描 catalog 提供。验证通过：pnpm web:build、go test ./internal/installer/installerkit ./internal/installer/docker ./internal/installer/mysql ./internal/installer/redis ./internal/installer/minio、pnpm test、git diff --check。
## 2026-06-30
- 问题：用户要求后端模块按每个 app 一个标准结构收敛为 module.go、service.go、options.go、status.go、templates/。
- 结论：Docker、MySQL、Redis、MinIO 的 app-specific installer 实现已从 backend/internal/installer/<app> 移入对应 backend/internal/apps/<app>，backend/internal/installer 仅保留共享 installerkit/uploadkit；MySQL Router 拆出独立 bundle/installer/script/uninstall/options/status/templates，不再把 Router 安装生命周期挂在 MySQL 包上，模板覆盖路径调整为 mysql-router/install.sh 与 mysql-router/uninstall.sh。验证通过：go test ./internal/apps/docker ./internal/apps/mysql ./internal/apps/redis ./internal/apps/minio ./internal/apps/mysqlrouter、pnpm test、git diff --check（仅 memory.md 既有 CRLF 提示）。
## 2026-06-30
- 问题：用户要求去掉收敛后的多余文件和代码。
- 结论：已删除未注册且无引用的 backend/internal/apps/offlineapp 旧共享离线占位模块；移除后端应用实例 upgrade stub 路由/handler 和数据库实例 backup stub 路由/handler，只保留真实可用的控制面 SQLite 备份维护能力；同步清理后端 offline/占位 backup/占位 upgrade i18n 文案和前端数据库页旧备份文案。验证通过：pnpm test、pnpm web:build、git diff --check（仅 memory.md 既有 CRLF 提示）。

## 2026-06-30
- 问题：读取当前开发层级结构，判断还有哪些文件、模块、接口适合合并或去掉。
- 结论：后端应用安装器已基本收敛到 apps/<app> 标准结构；优先清理容器终端 echo/stub、滞后的 AGENTS.md/文档引用、前端 DangerConfirm 与细碎 registry 文件；httpapi/api.go 应按粗粒度能力拆文件但保持同包；taskplan/taskrun/resourcekit/uploadkit 等共享能力暂不建议合并。

## 2026-06-30
- 问题：用户要求按上一轮结构审视建议继续收敛代码层级，合并和去掉多余文件。
- 结论：已移除容器终端 echo/stub 路由与文案、同步更新 AGENTS.md 旧描述；前端删除 DangerConfirm 并统一使用 ConfirmAction，registry 小文件收并到 types/catalog；后端 Docker/MySQL i18n 小文件并回 i18n.go，MySQL Router bundle 转发并入 installer.go；httpapi/api.go 拆为同包粗粒度 handler 文件并保留路由初始化。验证通过：pnpm test、pnpm web:build、git diff --check（仅 memory.md 既有 CRLF 提示）。

## 2026-06-30
- 问题：用户询问当前打包结构是否合理，是否需要迁移位置或去掉内容。
- 结论：当前运行布局 bin/web/dist/resources/config/defaults.env/start 脚本方向正确，但 scripts/package.* 只构建不生成独立发行目录或压缩包，源码根和运行根混用；建议新增 dist/release staging，只打入 bin、web/dist、resources、config、运行脚本和说明，排除源码、node_modules、data、outputs、缓存与开发脚本，并统一根目录与 deploy 的启动脚本入口。

## 2026-06-30
- 问题：用户确认按打包收敛建议执行。
- 结论：新增 scripts/package-release.mjs，pnpm package 与 scripts/package.ps1/sh 会构建后生成 dist/aifar-deployment-<version>-linux-amd64 和 windows-amd64 运行包、checksums、VERSION 以及 tar.gz/zip；包内只包含 bin、web/dist、resources、config、启动脚本和 README，排除源码、node_modules、data、日志、缓存和开发脚本；package.ps1/sh 默认 Go cache 改到 .cache/go-build。验证通过：pnpm test、pnpm package、git diff --check（仅既有换行风格提示）。

## 2026-06-30
- 问题：用户指出工程里还有多个 start.bat，担心启动入口重复。
- 结论：deploy/start.bat、deploy/start.ps1、deploy/start.sh、deploy/stop.sh 只是转发到根目录脚本的重复包装，已删除；工程现在只保留根目录 start.bat、start.ps1、start.sh、stop.sh 作为唯一运行入口。验证通过：rg 启动脚本与 deploy 引用检查、git diff --check。

## 2026-06-30
- 问题：用户要求打包相关内容全部迁移到 deploy，包括 bin、dist 等中间产物，最终打出的包放到 deployment 中，但打包命令仍在工程最外层运行。
- 结论：打包入口迁移为 deploy/package.ps1、deploy/package.sh、deploy/package-build.mjs、deploy/package-release.mjs；pnpm package 从根目录执行并输出中间产物到 deploy/bin 与 deploy/dist，最终运行包输出到 deploy/deployment；根目录旧 bin、dist、web/dist 已清理，包内仍保持 bin、web/dist、resources、config、启动脚本的标准运行结构。验证通过：pnpm package、pnpm test、git diff --check（仅换行风格提示）。

## 2026-06-30
- 问题：用户要求第一张图中的 package.ps1、package.sh、package-build.mjs、package-release.mjs 还原到原来的 scripts 位置，第二张图中的 start.bat、start.ps1、start.sh、stop.sh 放入 deployment 中。
- 结论：package 相关脚本已恢复到 scripts/；运行启动模板已移动到 deploy/deployment/ 并作为打包复制源；pnpm package 仍从根目录运行，输出 deploy/bin、deploy/dist 和 deploy/deployment/aifar-deployment-*，根目录不再生成 bin、dist、web/dist 或 start/stop 脚本。验证通过：pnpm package、pnpm test、git diff --check。

## 2026-06-30
- 问题：用户询问 env.bat 和 env.ps1 的作用，若无用则删除。
- 结论：env.bat/env.ps1 只是旧的本地 D:\tools PATH/GOROOT/GOPATH/GOCACHE 便捷脚本，当前已由 scripts/toolchain.mjs、scripts/package.ps1/sh 和 AIFAR_TOOL_ROOT/AIFAR_GO_CACHE 配置覆盖；已删除这两个脚本，并将 setup-dev 提示改为使用 AIFAR_TOOL_ROOT。验证通过：rg 无残留引用、pnpm test、git diff --check。

## 2026-06-30
- 问题：用户觉得 deploy/deployment 下的 start.bat、start.ps1、start.sh、stop.sh 放置奇怪，要求移到 scripts。
- 结论：四个运行启动模板已移动到 scripts/，scripts/package-release.mjs 改为从 scripts/start.* 和 scripts/stop.sh 复制到最终发布包根目录；deploy/deployment 只保留生成出来的包目录和归档。验证通过：node scripts/package-release.mjs、pnpm test、git diff --check。
- 问题：用户要求在应用商店新增 AIFAR 服务安装，并扫描已放入 resources/aifar 的 Docker Compose 应用包后给出优化建议和部署代码。
- 结论：新增 AIFAR 后端应用模块和前端应用商店模块；catalog 支持资源版本过滤，AIFAR 只展示/安装 docker-apps，避免 docker-sql 被误选为版本；安装会整体上传 resources/aifar 包，目标目录使用标准 /aifar/apps/aifar，不带版本号，支持写入 Nacos/数据库/端口/网络配置、可选 SQL 初始化、检测和卸载。验证通过：go test ./...、pnpm web:build、git diff --check；本地提交 cd44190d。

## 2026-06-30
- 问题：用户要求所有服务安装成功后自动开放安装端口，并在 SELinux 中加入对应端口规则。
- 结论：新增 installerkit 公共服务访问策略脚本片段，Docker/MySQL/Redis/Sentinel/Cluster/MinIO/MySQL Router/AIFAR 安装成功后会通过 firewalld 开放对应 TCP 端口，并在 SELinux 启用且 semanage 可用时写入对应端口类型；缺少 firewalld 或 semanage 时只记录 warning。验证通过：go test ./...、pnpm test、git diff --check。
- 问题：用户发现 Docker 不调整 SELinux 也能启动，而其他服务更容易因为 SELinux 启动失败，询问原因。
- 结论：Docker 通常运行在 container runtime/较宽权限域，且 2375/2376 等端口常已有 docker_port_t 默认策略；容器端口发布也多由 Docker 管理 iptables/proxy。MySQL、Redis、MinIO 等自带二进制安装在 /aifar/apps 并使用非默认端口时，更容易命中 SELinux 的端口类型或文件上下文限制；端口规则只解决 name_bind，若仍失败需继续检查 /var/log/audit/audit.log 的 AVC 并补 fcontext/restorecon 或应用专用策略。
- 问题：用户询问是否有工具或依赖可以直接接入项目，自动处理 SELinux。
- 结论：没有真正“万能自动修复”的安全依赖；推荐在 installerkit 封装 SELinux Manager，优先调用系统权威工具 semanage/restorecon/setsebool/semodule/ausearch，Go 依赖可选 opencontainers/selinux 做启用状态和 context 辅助判断。端口、文件上下文和 booleans 可自动化，audit2allow/semodule 生成本地策略应作为诊断建议或需用户确认，避免过度授权。
- 问题：用户要求把 SELinux 处理提炼成单独 Go 工具模块，供各安装服务过程复用。
- 结论：新增 backend/internal/installer/selinux 模块，集中提供安装脚本模板函数和 SELinux/firewalld shell helper；支持端口放行、fcontext、restorecon、setsebool、近期 AVC 诊断入口。各 app 安装脚本渲染改为依赖 selinux.AddTemplateFuncs，installerkit 不再承载 SELinux 片段。验证通过：go test ./...、pnpm test、git diff --check。
- 问题：用户询问目标机没有 semanage 时怎么办。
- 结论：semanage 不应静默忽略；推荐安装前检测 policycoreutils-python-utils/RHEL7 的 policycoreutils-python 或通过 dnf provides 查询；离线环境应把 semanage 相关 RPM 纳入公共依赖包。没有 semanage 时端口类型不能可靠持久化，只能临时 chcon 文件上下文或提示缺工具，不能自动 setenforce 0。
- 问题：用户要求继续处理 semanage 缺失场景。
- 结论：SELinux 工具模块新增 ensure_semanage 流程：需要 semanage 时先尝试从 AIFAR_SELINUX_RPM_DIR 或 WORK_DIR/rpms 安装本地 RPM，再尝试 dnf/yum 安装 policycoreutils-python-utils 或 RHEL7 的 policycoreutils-python；仍缺失时输出明确离线依赖提示并跳过规则写入。验证通过：go test ./internal/installer/selinux、go test ./...、pnpm test。
- 问题：用户反馈 MinIO 安装成功后关闭 SELinux 仍无法登录，Console 页面提示 invalid login。
- 结论：能打开 MinIO Console 且提示 invalid login，说明网络、端口和 SELinux 基本不是当前阻塞点；应优先排查 systemd 实际加载的 MINIO_ROOT_USER/MINIO_ROOT_PASSWORD、是否改过 env 未重启、是否复用了旧数据目录或分布式节点 env 不一致。
- 问题：用户反馈 MinIO 安装第 4/5 步上传 Go module cache 时失败，错误为 EOF。
- 结论：该错误发生在 SSH 文件上传阶段，属于大文件传输链路被中断而不是 MinIO 编译脚本内部报错；uploadkit 已对 EOF、broken pipe、connection reset/timeout 等瞬时上传错误增加自动重试，并让 SSH 上传失败时携带远端 stderr，便于区分磁盘空间、权限等永久故障。
