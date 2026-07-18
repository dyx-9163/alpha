# Keepalived 2.4.2 离线工具

本目录仅支持 openEuler 24.03 LTS SP3 x86_64，Keepalived 安装目录固定为 `/aifar/apps/keepalived`。

请保持本目录中的安装脚本、SELinux 脚本、卸载脚本、源码包和 `SHA256SUMS` 在一起。所有命令均为零参数命令。

## 安装

以 root 执行；普通用户运行时脚本会尝试使用 `sudo`：

```bash
bash install-keepalived-offline.sh
```

安装程序会先校验 `keepalived-2.4.2.tar.gz` 的固定大小和 SHA256，再使用服务器当前已配置的 DNF 仓库安装编译依赖。脚本不会添加公网仓库，不会复制示例配置为正式配置，也不会自动启动或启用 Keepalived。

## SELinux

安装脚本会在 SELinux 已启用时自动调用适配脚本。需要重新应用或检查标签时也可以单独执行：

```bash
bash configure-selinux.sh
```

该脚本复用发行版自带的 Keepalived 策略标签，并保持 SELinux 当前的 Enforcing 或 Permissive 模式。它不会关闭 SELinux、切换模式或根据审计日志生成宽泛授权。

## 配置和启动

安装完成后需要自行创建生产配置：

```bash
/aifar/apps/keepalived/sbin/keepalived -t \
  -f /aifar/apps/keepalived/etc/keepalived/keepalived.conf
systemctl enable --now keepalived
systemctl status keepalived
```

只有配置语法检查通过后才能启动服务。安装脚本本身不会生成 VIP、VRRP 主备或健康检查配置。

## 卸载

将本脚本保留在安装目录之外，然后以 root 执行：

```bash
bash uninstall-keepalived.sh
```

卸载程序会先把配置、健康检查脚本、systemd unit 和 SELinux 映射记录备份到 `/aifar/backups/keepalived-<UTC时间戳>/`，并复验备份校验和；只有备份通过后才停止服务和删除 `/aifar/apps/keepalived`。

卸载程序不会删除共享 RPM 依赖，不会删除备份，也不会删除无法确认归属的 VRRP 防火墙规则。发生错误时会输出已保留的备份路径，便于人工恢复。
