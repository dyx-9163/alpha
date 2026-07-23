# Keepalived 直接 systemd unit 设计

## 背景

当前安装器把 Keepalived unit 生成到 `/aifar/apps/keepalived/systemd/keepalived.service`，再让 `/etc/systemd/system/keepalived.service` 软链接到该文件。目标服务器重启后曾出现 unit 无法正常发现、需要人工执行 `systemctl daemon-reload` 才能启动 Keepalived 的情况。

本次改造让 systemd 直接读取 `/etc/systemd/system/keepalived.service` 普通文件，消除 unit 发现对 `/aifar` 安装树软链接的依赖。Keepalived 二进制、配置和运行数据仍保留在 `/aifar/apps/keepalived`。

## 目标

- `/etc/systemd/system/keepalived.service` 必须是普通文件，不得是软链接。
- 安装或更新时写入 unit、执行 `systemctl daemon-reload` 并启用服务。
- 服务器重启后由 systemd 直接加载 unit，不需要人工执行 `daemon-reload`。
- unit 明确等待 `/aifar/apps/keepalived` 所在挂载可用后再启动。
- 重复安装、失败回滚和卸载继续保护已有服务、配置及外部 unit。

## 非目标

- 不改变 Keepalived 二进制、配置、健康检查和运行数据的安装根。
- 不改变 VRRP、VIP、健康检查、firewalld 或服务启动成功标准。
- 不接管无法确认属于 AIFAR Keepalived 安装的第三方 unit。
- 不修改已部署服务器；真实节点迁移属于实施完成后的单独受控操作。

## Unit 生成和安装

Keepalived 构建过程可继续在 `/aifar/apps/keepalived/systemd/keepalived.service` 生成上游 unit，作为安装阶段的源文件。它不再是 systemd 的运行时 FragmentPath。

安装器应完成以下处理：

1. 验证生成的源 unit 引用了 `/aifar/apps/keepalived/sbin/keepalived`。
2. 在 unit 的 `[Unit]` 段加入 `RequiresMountsFor=/aifar/apps/keepalived`；若已有同值配置则保持单条。
3. 在安装工作目录生成最终 unit 临时文件，并校验关键段和启动路径。
4. 使用 root:root、0644 权限将最终 unit 原子安装为 `/etc/systemd/system/keepalived.service` 普通文件。
5. 明确拒绝最终路径是软链接的状态。
6. 执行 `systemctl daemon-reload`、`systemctl enable keepalived.service`，然后沿用当前 start/restart 与 active 验证流程。

`RequiresMountsFor` 只负责安装树挂载依赖，不替代 `network-online.target` 等原有 unit 依赖。

## 所有权和冲突处理

安装器以以下条件确认已有普通 unit 属于本模块：

- 文件是 `/etc/systemd/system/keepalived.service` 的普通文件；
- unit 的 `ExecStart` 引用 `/aifar/apps/keepalived/sbin/keepalived`；
- 已加载时，`systemctl show ... FragmentPath` 解析为 `/etc/systemd/system/keepalived.service`。

若最终路径为指向当前旧 unit 的软链接，视为可迁移的旧版 AIFAR 安装：事务备份后删除软链接并写入普通文件。指向其他路径的软链接、其他内容的普通 unit 或其他已加载 FragmentPath 均视为外部冲突，安装必须在覆盖前失败。

## 事务备份和回滚

安装前状态快照除现有服务 active/enabled 状态外，还必须保存 unit 的类型和内容：

- 不存在；
- 指向旧 AIFAR unit 的软链接；
- 属于本模块的普通文件。

安装后任一步失败时：

1. 停止本轮启动的服务；
2. 删除本轮写入的普通 unit；
3. 按快照恢复原普通文件或原软链接；原先不存在则保持不存在；
4. 执行 `systemctl daemon-reload`；
5. 恢复原 enabled/active 状态；
6. 若 unit 被外部并发修改，拒绝覆盖并明确报告回滚冲突。

完整安装树、SELinux 和 firewalld 的既有事务回滚保持不变。

## 卸载

卸载器在变更前验证 `/etc/systemd/system/keepalived.service` 属于本模块，并把普通 unit 内容纳入现有备份与校验和。随后按顺序：

1. 停止并禁用 `keepalived.service`；
2. 删除属于本模块的 `/etc/systemd/system/keepalived.service` 普通文件；
3. 执行 `systemctl daemon-reload`；
4. 删除 `/aifar/apps/keepalived` 安装树和本模块拥有的其他资源。

卸载失败回滚恢复 unit 普通文件及原服务状态。兼容卸载旧版 AIFAR 软链接安装，但不得删除指向外部路径的链接或外部普通 unit。

## SELinux

systemd 实际读取的 unit 位于标准目录 `/etc/systemd/system`，使用发行版对该目录的默认标签，不再需要依靠 `/aifar/apps/keepalived/systemd/keepalived.service` 的标签才能发现服务。

Keepalived 二进制、配置、健康脚本、状态和运行目录的现有 SELinux 映射保持不变。安装后对最终 unit 使用标准标签恢复流程，并验证其类型符合 systemd unit 文件预期；不得给 `/etc/systemd/system` 整体递归重标。

## 测试和验收

自动化测试至少覆盖：

- 安装器不包含 `systemctl link` 或创建 unit 软链接的 `ln -s` 路径；
- 新安装生成 `/etc/systemd/system/keepalived.service` 普通文件；
- unit 包含正确 `ExecStart` 和 `RequiresMountsFor=/aifar/apps/keepalived`；
- 旧版 AIFAR unit 软链接可迁移为普通文件；
- 外部软链接和外部普通 unit 均拒绝覆盖；
- 重复安装安全更新普通 unit；
- 安装失败分别恢复“不存在、旧软链接、旧普通文件”三种状态；
- 卸载备份并删除普通 unit，失败时恢复它；
- 卸载兼容旧 AIFAR 软链接但拒绝外部 unit；
- Bash 语法、Keepalived 专项测试、脚本测试及 `git diff --check` 通过。

真实 openEuler 验收应在单独授权后验证：

1. `test -f /etc/systemd/system/keepalived.service` 成功，`test ! -L` 成功；
2. `systemctl show keepalived.service -p FragmentPath` 返回 `/etc/systemd/system/keepalived.service`；
3. 服务为 enabled/active，VIP 与健康检查行为不变；
4. 受控重启服务器后无需人工 `daemon-reload`，Keepalived 自动启动并恢复预期 VIP 状态。
