# Keepalived 2.4.2 离线双机部署工具

本模块仅支持 openEuler 24.03 LTS SP3 x86_64，安装目录固定为 `/aifar/apps/keepalived`。安装、SELinux 和卸载脚本均为零参数命令。

## 1. 准备每个节点的配置

在两台服务器上分别把整个 `extras/keepalived` 目录复制到本地。源码包、脚本、模板、示例配置和 `SHA256SUMS` 必须保持在同一目录。然后在每个节点执行：

```bash
cp keepalived.env.example keepalived.env
```

`keepalived.env` 是纯数据文件，只允许以下七个键：

- `KEEPALIVED_LOCAL_IP`：本节点 IPv4 地址；
- `KEEPALIVED_PEER_IP`：对端节点 IPv4 地址；
- `KEEPALIVED_VIP_CIDR`：带前缀的 VIP；
- `KEEPALIVED_INTERFACE`：承载节点 IP 和 VIP 的接口；
- `KEEPALIVED_PRIORITY`：本节点 VRRP 优先级，范围 1-254；
- `KEEPALIVED_VIRTUAL_ROUTER_ID`：两节点相同的 VRID，范围 1-255；
- `KEEPALIVED_HEALTH_URL`：本节点聚合健康接口，只接受 HTTP/HTTPS，并要求 HTTP 2xx 且顶层 JSON 字段 `up` 为布尔值 `true`。

节点 192.168.74.132 的 `keepalived.env`：

```dotenv
KEEPALIVED_LOCAL_IP=192.168.74.132
KEEPALIVED_PEER_IP=192.168.74.133
KEEPALIVED_VIP_CIDR=192.168.74.130/24
KEEPALIVED_INTERFACE=ens160
KEEPALIVED_PRIORITY=150
KEEPALIVED_VIRTUAL_ROUTER_ID=130
KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

节点 192.168.74.133 的 `keepalived.env`：

```dotenv
KEEPALIVED_LOCAL_IP=192.168.74.133
KEEPALIVED_PEER_IP=192.168.74.132
KEEPALIVED_VIP_CIDR=192.168.74.130/24
KEEPALIVED_INTERFACE=ens160
KEEPALIVED_PRIORITY=100
KEEPALIVED_VIRTUAL_ROUTER_ID=130
KEEPALIVED_HEALTH_URL=http://192.168.74.133:38000/health/aggregate
```

两节点必须使用相同的 VIP/CIDR 和 VRID，并互相填写对端 IP；优先级 150 的 132 节点是正常情况下的 VIP 所有者。

## 2. 安装并自动启动

在 132、133 两个节点的模块目录中各执行一次且只执行：

```bash
bash install-keepalived-offline.sh
```

普通用户运行时脚本会通过 `sudo` 重新执行。安装器会严格校验节点配置和固定源码包，生成正式配置，安装健康检查脚本，配置精确的 VRRP 防火墙规则和 SELinux 标签，然后启用并启动 `keepalived.service`。

安装器只使用服务器当前已经配置好的离线 DNF 仓库，不添加或访问公网仓库。请先确认挂载的 openEuler ISO 仓库可被 `dnf` 直接使用。

## 3. 检查状态和 VIP

在两个节点执行：

```bash
systemctl status keepalived
journalctl -u keepalived
ip addr show dev ens160
firewall-cmd --list-rich-rules
```

两端健康接口都正常时，`192.168.74.130/24` 只应出现在优先级更高的 132 节点。firewalld 运行时，安装器仅允许配置的对端 `/32` 访问 VRRP IP protocol 112（协议 112），不会开放整个网段。

## 4. 健康故障、漂移和恢复

Keepalived 每 2 秒检查本节点的 `KEEPALIVED_HEALTH_URL`。连续 3 次失败后，本节点的 VRRP 实例进入 `FAULT`，但 `keepalived.service` 仍保持 active；132 不健康约 6 秒后，VIP 会漂移到健康的 133。

132 恢复后，连续 2 次成功探测（约 4 秒）解除 `FAULT`。配置使用默认抢占行为，因此 VIP 会自动抢占并切回优先级更高的 132，无需手工重启 Keepalived。

## 5. 手工服务操作

```bash
systemctl start keepalived
systemctl stop keepalived
systemctl restart keepalived
systemctl status keepalived
```

停止当前 VIP 所有者的服务会触发漂移；启动后仍需等待健康检查达到 `rise 2` 才能参与 VRRP 选举。

## 6. SELinux

安装器在 SELinux 启用时自动应用发行版自带 Keepalived 策略派生出的标签。它保留当前 Enforcing 或 Permissive 模式，不执行 `setenforce`、不修改 `/etc/selinux/config`，也不生成宽泛的 `audit2allow` 策略。

需要重新应用或检查标签时可独立执行：

```bash
bash configure-selinux.sh
```

独立执行会自行回滚本次失败的标签映射变更；由安装器调用时，本轮变更还会加入安装事务，后续步骤失败即可精确恢复。

## 7. 重复安装、备份和回滚

重复执行安装器前，会把完整旧安装树和原服务状态保存到 `/aifar/backups/keepalived-update-<UTC时间戳>-<PID>/`，并校验 `BACKUP.sha256`。任何后续安装步骤失败时，安装器会恢复原文件、原 systemd active/enabled 状态、本轮 SELinux 映射和精确防火墙变更。

成功或失败后备份都会保留，不会自动清理。请按运维保留策略人工归档或删除已经确认不再需要的旧备份。

## 8. 卸载

将卸载脚本保留在安装目录之外，以 root 执行：

```bash
bash uninstall-keepalived.sh
```

卸载器先把配置、`libexec` 健康脚本、systemd unit、SELinux 所有权记录和防火墙所有权记录备份到 `/aifar/backups/keepalived-<UTC时间戳>/` 并复验校验和，之后才停止服务和删除安装目录。

卸载只删除记录中由本安装创建且当前仍完全匹配的 peer `/32` 协议 112 规则，不会删除预存或不属于本安装的防火墙规则。它不会删除共享 RPM 依赖，也不会删除任何备份；无法证明归属或发现外部修改时会停止并保留恢复路径。
