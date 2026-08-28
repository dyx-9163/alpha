# AIFAR Memory

本文件只保留**当天（YYYY-MM-DD）**的精简问题与结论。每次写入前必须执行"写入守卫"流程（见 AGENTS.md）。历史条目自动归档到 `memory/YYYY-MM-DD.md`。禁止写入密码、token、私钥、完整连接串和长日志。

## 2026-08-28
- 问题：现场批量更新 9 个 AIFAR Runtime 服务在步骤 1 报 `AIFAR_RUNTIME_DEPLOYMENT_GENERATION_CONFLICT`，随后安装缺失模块后部分服务仍为 `NoEndpoints`。
- 结论：批量任务执行时仅存在 gateway、oauth、permission、system 四个后端 Deployment，contacts、file、im、meeting、message 后续才以 generation 1 创建；`loadDeploymentForMutation` 将目标 Deployment 不存在复用为 generation-conflict 机器码和文案，因此本次“被其他任务更新”是误导提示，任务在上传/发布前失败且未造成部分批量发布。Agent 恢复后 oauth、permission、system 已观察到目标 generation 但 0/1、`NoEndpoints`，属于容器/健康端点问题，需查 Docker 状态、日志、Agent 持久 spec 与 journal，不能再归因于 Agent 断连。
- 问题：远程只读排查 192.168.74.143 上 Runtime 重启后大面积 `NoEndpoints`、服务不健康及三个服务无法创建容器。
- 结论：16:02:01 root Bash 执行 `systemctl stop firewalld` 后 Docker 的 `DOCKER-*`、FORWARD 和 NAT/MASQUERADE 规则被清空，现场仅剩 `FORWARD DROP`，容器无法访问 `.141/.142:26379` 和外部 DNS，而宿主机可达，导致 Java 服务等待 Redis Sentinel、健康端口不监听并被 Agent 反复自愈；`oauth/permission/system` 另因目标镜像标签已不存在而保持 0/1，Docker 尝试从公网拉取失败。修复应先恢复 Docker 网络规则并验证容器出站，再通过受控制品更新重建缺失镜像；不要直接归因于 Agent 或 OOM。
- 问题：AIFAR Runtime 安装或重装过程中 Agent 暂时不可用时，页面整块隐藏部署信息，用户无法观察安装进度。
- 结论：提交 `b4918440` 显式引入了 Agent 非 `running` 时清空运行时实例并隐藏工作区的逻辑；现已改为保留并展示最近一次运行时数据、明确标注数据可能暂时过期，同时继续通过既有门禁禁用所有变更操作。回归测试先在旧逻辑上失败，修复后完整前端 498 项测试及生产构建通过。
