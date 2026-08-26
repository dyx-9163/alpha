# AIFAR Memory

本文件只保留**当天（YYYY-MM-DD）**的精简问题与结论。每次写入前必须执行"写入守卫"流程（见 AGENTS.md）。历史条目自动归档到 `memory/YYYY-MM-DD.md`。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-08-26
- 问题：需要扫描全仓代码，并从企业级运维方向为当前 AIFAR Deployment 生成标准 SDD 改造方案。
- 结论：采用当前仓库渐进式双 Profile 路线，保留 Go/Vue、离线交付和 Standalone 单二进制，同时分阶段建设 SSH 主机身份、持久任务租约与 fencing、标准变更审批、统一资源模型、可观测性、全栈灾备和 PostgreSQL Enterprise Profile；完整设计见 `docs/superpowers/specs/2026-08-26-aifar-enterprise-operations-transformation-design.md`。
