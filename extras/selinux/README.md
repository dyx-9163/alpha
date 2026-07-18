# AIFAR 全服务 SELinux 配置工具

`configure-all-selinux.sh` 用于在 openEuler 24.03 LTS SP3 x86_64 主机上，为已安装的 AIFAR 服务持久化并校验 SELinux 端口和文件上下文规则。

## 使用方法

以 `root` 用户在 Linux 发布包根目录执行：

```bash
bash extras/selinux/configure-all-selinux.sh
```

脚本不接受参数。它会检查以下已安装组件，未安装的组件显示为 `SKIPPED`：

- Docker 与 `aifar-agent`
- AIFAR Runtime
- MySQL 与 MySQL Router
- Redis、Sentinel 与 Redis Cluster
- MinIO
- Nacos
- Keepalived
- 可选的 AIFAR HTTPS ingress

## 行为边界

- 要求 SELinux 已启用；保留当前 `Enforcing` 或 `Permissive` 模式不变。
- 缺少管理命令时，仅使用主机已配置的 DNF 仓库安装 SELinux 工具，适用于已挂载的离线 ISO 仓库。
- 只读取白名单中的 systemd 单元、配置文件和 `/aifar/apps` 安装目录。MinIO 本地数据卷还允许位于 `/data`、`/mnt` 或 `/srv` 的明确子目录。
- 使用 `semanage port`、`semanage fcontext` 和 `restorecon` 创建持久规则；不会生成 `audit2allow` 策略。
- 不修改 `/etc/selinux/config`，不执行 `setenforce`，不修改防火墙，也不启动、停止、重启或启用服务。
- 如果已安装组件的端口、路径或既有本地规则发生冲突，脚本失败并回滚本次新增或修改的 SELinux 规则。
- HTTPS ingress 正在运行时，只校验其 `:Z` 私有 MCS 挂载标签，不对 `conf.d` 和 `tls` 执行 `restorecon`。

事务记录保存在 `/var/lib/aifar-selinux/transactions/`，成功重跑应显示为 `UNCHANGED`。
