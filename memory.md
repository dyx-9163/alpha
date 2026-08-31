# AIFAR Memory

本文件只保留**当天（YYYY-MM-DD）**的精简问题与结论。每次写入前必须执行"写入守卫"流程（见 AGENTS.md）。历史条目自动归档到 `memory/YYYY-MM-DD.md`。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-08-31
- 问题：审计 AIFAR 应用部署运维整体代码，并根据代码漏洞形成标准 SDD 与后续修复方案。
- 结论：本次 SDD 仅覆盖单服务器 AIFAR Runtime 完整生命周期，采用事务化生命周期控制器、目标状态验收失败自动恢复、默认可恢复卸载与独立高风险彻底清除。实施按已确认的九阶段顺序且一次只修复一个功能；每项完成后将修复内容、测试、提交和人工验证状态写入 SDD，通知用户并暂停，用户明确“继续”后才进入下一项。现状审计确认：成功门禁仅证明 Agent 接收期望状态，未证明目标 revision Ready/Available 或业务健康；初装缺少目标端 SHA-256 复验并提前替换运行目录；运行中任务重启后直接失败。关键后端包和前端 498 项测试通过，但未覆盖这些一致性风险。用户已确认新增 `aifar_lifecycle_operations` 持久事务日志，并以目标端 checksum、精确 generation/specHash、ObservedGeneration、目标 revision、全部 Ready/Available、服务烟测及连续稳定窗口作为成功门禁；Panel 重启按检查点观察后继续或补偿，无法证明时进入 `needs_attention`。安装、单服务升级、整包升级、扩缩容/下线/重启、回滚/对账恢复、可恢复卸载和彻底清除均采用“预检与不可变暂存—目标端校验—保存前态—切换—健康验收—提交”，失败时恢复最近已验证状态；进入提交或补偿阶段后不可取消。
- 问题：把已确认的 AIFAR Runtime 单服务器生命周期可靠闭环设计固化为可评审、可实施和可逐阶段验收的标准 SDD。
- 结论：标准 SDD 已写入 `docs/superpowers/specs/2026-08-31-aifar-runtime-single-server-lifecycle-reliability-design.md`，包含范围、现状证据、12 项风险、目标架构、专用事务表、状态机、成功门禁、重启恢复、十类数据流、API/前端/安全/测试、九阶段人工验收和修复台账；自审已消除占位符与既有 Runtime SDD 的成功语义冲突。当前仅形成设计文档，尚未修改业务代码，须待用户书面复核后再制定实施计划。
- 问题：在用户确认标准 SDD 后制定可执行实施计划，同时保持每阶段人工验收暂停边界。
- 结论：九阶段按顺序分别制定计划，当前仅形成阶段一 `docs/superpowers/plans/2026-08-31-aifar-runtime-stage-1-artifact-staging.md`。计划以 TDD 拆为 AIFAR 隔离暂存边界、目标端 SHA-256/大小校验、初装集成、回归与证据台账四个任务；bundle、agent 和安装脚本全部校验通过前禁止执行安装脚本。本阶段不修改 Agent Accepted/Ready 成功语义、生命周期新表、更新/回滚路径或 `install.sh` 活动目录切换；执行完成后必须通知用户并暂停验证。
