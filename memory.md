
# AIFAR Memory

本文件记录后续对话的精简问题与结论。每次开始先读，结束前追加。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-07-09
- 问题：用户询问当前 AIFAR Deployment 的服务升级是怎么做的。
- 结论：当前代码中通用应用 Docker/MySQL/Redis/MinIO/Nacos 仍以 install/check/delete 为主，没有完整 Update/Rollback 生命周期；已落地的“服务升级”主要是 AIFAR Runtime v2 制品滚动发布，入口在 Containers 的 AIFAR Runtime 页，支持单服务上传 jar/web 包和批量 zip 包，后端走 `/apps/instances/{id}/aifar/update-artifact` 或 `/update-artifact-bundle`，创建 worker task、上传制品和脚本、远端执行 `aifar-agent reconcile-runtime`，再写入 app_instances metadata、aifar 控制面表和 app_releases 发布记录；目前有 release 记录和保留策略，但没有面向服务升级的自动备份/正式回滚 API。
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
- 问题：用户询问各模块安装是否有标准流程，如校验、上传、安装、等待结果。
- 结论：当前安装统一抽象为 `PreflightInstall -> PlanInstall -> ValidateInstall -> worker Install`，API 创建任务并保存步骤计划；模块内步骤不完全一致，Docker 较粗，MySQL/Redis/MinIO/Nacos/AIFAR 按拓扑增加 verify/bootstrap/configure/record 等步骤。上传安装包多数在 `install-*` 步骤内部完成，AIFAR 会单独暴露 `upload-bundle`。
- 问题：用户讨论自定义部署编排模块，希望安装过程用 shell，资源统一上传，Go/Vue 主要负责触发、展示和日志收集。
- 结论：建议采用“脚本执行面 + Go 控制面 + Vue 配置/展示面”：shell 负责幂等安装、配置、等待 readiness 和诊断；Go 负责资源选择/SHA256 校验、参数和拓扑校验、SSH 上传执行、任务/步骤/审计/app_instance/凭据绑定；Vue 只基于 schema 展示配置、提交任务和查看日志。禁止通过 API 直接传自由 shell，脚本应来自受信资源包或内置模板。
- 问题：用户要求输出自定义部署编排设计文档，说明 Vue/Go 如何改造、aifar-agent 应承载哪些能力，并考虑多服务器集群模式。
- 结论：已新增 `design/custom-deployment-orchestration-design.md`。设计建议新增 `deployspec`、`apps/scripted`、前端 scripted fallback 和动态 catalog pairing；多服务器由 Go worker 负责编排，节点步骤并发执行，cluster bootstrap 在 coordinator 单点执行；aifar-agent 定位为节点本地 runtime/job/status/log/proxy 能力，不承担全局 catalog/RBAC/任务系统/集群调度。
- 问题：用户询问上述自定义部署编排改造是否会改变数据库。
- 结论：第一阶段设计不需要数据库 schema 变更，复用现有 `resources`、`tasks`、`task_steps`、`task_targets`、`audit_logs`、`app_instances` 等表；动态部署 spec 从资源包读取，集群信息写入 `app_instances.metadata`。只有后续要做强集群模型、spec 缓存、agent job 持久化或资源状态历史时，才建议新增表和迁移。
- 问题：用户询问自定义编排下各服务备份还原如何做，以及更新时如何定义版本和回滚。
- 结论：已补充 `design/custom-deployment-orchestration-design.md` 第 11 节。建议把 backup/restore/update/rollback 做成与 install/check/delete 同级的 lifecycle，全部返回 task id；spec 增加 versioning、backup、restore、update、rollback 和对应脚本声明；版本分为 resource/package/app/config schema/data schema/runtime；第一阶段备份和 release 摘要写入 `app_instances.metadata`，后续需要备份列表和发布历史再新增 `app_backups`、`app_releases`。
- 问题：用户反馈 MySQL InnoDB Cluster “启动集群”按钮失败，MySQL Shell 报 `aifar_nacos.vgroup_table` 没有 Primary Key 或等价非空唯一键。
- 结论：根因是 Group Replication/InnoDB Cluster 要求可写 InnoDB 表必须有主键或非空唯一键，当前已有业务表需手动补 DDL 后重试。已增强 MySQL 集群启动模板，在 `rebootClusterFromCompleteOutage()` 前先只读扫描 information_schema 并列出不兼容表，提示添加主键/非空唯一键；补了模板渲染测试，`pnpm test` 通过。
- 问题：用户手工执行 `cluster.status()` 后显示 132 为 ONLINE Primary，133/134 为 `(MISSING)` 且 group_replication stopped；对 133 执行 `cluster.addInstance(..., {recoveryMethod:'clone'})` 报 server_id 已被同一地址成员使用。
- 结论：当前集群不是 complete outage，133/134 已存在于 InnoDB Cluster 元数据中，不能直接 `addInstance`；若保留数据应先尝试 `cluster.rejoinInstance()`，若确认可丢弃 133/134 本地数据并用 Primary 覆盖，则先 `cluster.removeInstance('root@host:3306', {force:true})` 从元数据移除缺失成员，再 `cluster.addInstance(..., {recoveryMethod:'clone'})` 重建副本。
- 问题：用户询问应用服务卸载后，凭据中心记录是否也应删除，或删除前提示已被引用并允许强制删除。
- 结论：建议卸载应用实例时只解除凭据绑定，不默认硬删除凭据；安装自动生成且只属于该实例的凭据可在卸载成功后提示“同时删除/停用”，默认保留或停用。凭据删除应先检查现存引用，默认拦截并展示引用来源；强制删除只适合显式二次确认的高级操作，优先做“强制解绑并停用”，避免破坏仍在使用的服务。
- 问题：用户指出 MySQL/Redis/Nacos/MinIO 安装弹窗中选择已有自身凭据不合逻辑，因为服务创建后才会生成对应密钥。
- 结论：已移除 MySQL/Redis/MinIO/Nacos 安装表单中的自身凭据选择字段和文案，安装时始终手动填写自身账号/密码，后端安装成功后继续自动生成并绑定凭据；Nacos 依赖外部 MySQL 的 `dbCredentialId` 选择保留。
- 问题：用户询问 Docker/MySQL/Redis/MinIO/Nacos/AIFAR 等服务安装步骤是否相近，能否标准化。
- 结论：这些服务单机安装有共同骨架：load-server、verify-resource、prepare-storage/optional、install-runtime、configure-topology/optional、verify-runtime、record-instance；集群、Router、Sentinel、MinIO 复制、AIFAR 上传发布属于扩展阶段。建议标准化“阶段模型、步骤命名、前端展示和 task runner”，但保留各应用自己的脚本与拓扑差异，不强行合并成一个通用安装脚本。
- 问题：用户要求给出标准化安装流程的建议文件结构、每个文件职责，以及是否所有应用安装都会使用。
- 结论：建议新增 `backend/internal/installflow` 作为应用安装编排公共层，包含阶段定义、计划生成、执行 runner、资源解析、目标加载、实例记录、凭据登记、错误归类和测试；所有应用都使用阶段/计划/runner/资源/目标/记录/凭据公共能力，但应用自己的 options、service、installer、templates 保留在 `backend/internal/apps/<app>` 内；拓扑扩展阶段按需使用。
- 问题：用户询问 AIFAR jar 包和 Vue 包升级/回滚机制是否合理。
- 结论：当前升级链路可作为 forward rollout 第一版使用，已有 worker、锁、上传、校验、远端脚本、release 记录和 runtime-agent 容器失败清理；但回滚不完整。jar/vue 制品会覆盖 `app.jar`/`dist`，旧制品未按 release 持久保存，缺少 rollback API/UI/脚本，bundle 也缺少每服务 before/after revision 快照。建议补 release artifact 目录、发布快照、rollback lifecycle、失败恢复、远端/DB 双向状态一致性和 release GC。
- 问题：用户要求给出企业级 jar/Vue 包升级和回滚设计方案。
- 结论：已新增 `design/aifar-artifact-upgrade-rollback-enterprise-design.md`。方案建议把每次升级定义为不可变 release，保存原始制品、构建上下文、env/runtime-spec 快照、镜像和每服务 before/after revision；回滚作为新的 rollback release 执行；新增发布历史/详情/rollback API、远端 release 目录、脚本状态机、健康门禁、一致性对账、锁与并发、审计权限、GC 和分阶段实施计划。
- 问题：用户确认按企业级 jar/Vue 包升级回滚建议改造代码。
- 结论：已落地第一阶段闭环：AIFAR 单服务和 bundle 升级会写入 v2 release manifest、保存远端 release 制品和服务快照；新增 `app_releases` 发布历史查询 API、AIFAR artifact rollback API、受信 rollback shell 模板和前端发布历史/回滚入口。回滚会基于目标成功 release 的远端制品与 SHA256 执行，并作为新的 rollback release 记录；`pnpm test` 与 `pnpm web:build` 通过。
- 问题：用户要求继续看全工程，给出架构层面的优化方向。
- 结论：当前工程基础清晰，但随着 AIFAR Runtime、发布回滚、凭据、E2E 增加，主要架构优化点集中在：抽 `installflow` 标准安装/检查/删除流程；将 AIFAR Runtime 从 `httpapi` 和超大 `ContainersView.vue` 拆到独立 runtime 控制域；拆分 registry 通用应用生命周期和 AIFAR runtime 专属能力；引入版本化 migrations 和 typed metadata；增强 worker 为持久任务/lease/统一 task metadata；将 release/backup/credential 引用治理平台化。
