# Keepalived 可选健康检查设计

## 背景

当前 `extras/keepalived/install-keepalived-offline.sh` 要求 `keepalived.env` 必须提供 `KEEPALIVED_HEALTH_URL`。安装器固定生成 `vrrp_script` 和 `track_script`，安装健康检查脚本与 URL 文件，并在服务启动后执行一次健康探测。

部分部署只需要 Keepalived 根据 VRRP 节点状态和优先级管理 VIP，不需要检查本机业务接口。本变更允许通过删除或注释 `KEEPALIVED_HEALTH_URL` 关闭应用健康检查，同时保持 Keepalived 服务、VRRP、VIP、防火墙和 SELinux 管理正常工作。

## 目标

- 将 `KEEPALIVED_HEALTH_URL` 从必填字段改为可选字段。
- 未配置健康 URL 时仍生成正式 VRRP 配置、启用并启动 `keepalived.service`。
- 健康检查关闭时不安装健康脚本、不写健康 URL 文件，并且正式配置不引用这些文件。
- 保持现有健康检查模式完全兼容。
- 重复安装可安全地在启用和禁用健康检查之间切换，并保留现有事务回滚保证。

## 非目标

- 不改变 VRRP 单播、优先级、默认抢占、VIP/CIDR 或 VRID 语义。
- 不新增命令行参数或第二个安装入口。
- 不改变 firewalld 的对端 `/32`、IP protocol 112 精确规则。
- 不让安装器自动判断业务接口是否存在；健康检查模式只由 `keepalived.env` 决定。
- 不改变健康接口启用时的 HTTP、JSON、超时、fall/rise 参数。

## 配置契约

以下两种写法均表示关闭健康检查：

```dotenv
# KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

```dotenv
# 整个文件中没有 KEEPALIVED_HEALTH_URL
```

以下写法表示启用健康检查，并保持当前校验规则：

```dotenv
KEEPALIVED_HEALTH_URL=http://192.168.74.132:38000/health/aggregate
```

主动声明空值仍然是配置错误：

```dotenv
KEEPALIVED_HEALTH_URL=
```

安装器继续把配置文件作为数据解析，不执行 `source`。未知字段、重复字段、包含空白的非法行、命令替换和其他 Shell 表达式继续失败。只有 `KEEPALIVED_HEALTH_URL` 允许缺失；其余六个节点字段继续必填。

解析结果使用显式布尔状态表示健康检查是否启用，不通过“文件是否存在”推断模式。

## 配置渲染

采用单模板条件块方案。模板中的公共 VRRP 内容只保留一份，健康相关内容由安装器根据解析状态选择性渲染。

健康检查启用时，正式配置继续包含：

- `global_defs` 中的 `script_user root` 和 `enable_script_security`；
- `vrrp_script check_aifar_health`；
- `vrrp_instance` 中的 `track_script`。

健康检查禁用时，正式配置：

- 保留 `router_id`；
- 不包含 `script_user` 和 `enable_script_security`；
- 不包含 `vrrp_script check_aifar_health`；
- 不包含 `track_script`；
- 保留 `state BACKUP`、接口、VRID、优先级、单播对端、VIP 和默认抢占行为。

禁用模式下，业务服务异常不会使本节点进入 `FAULT`。只要 Keepalived、网络接口和 VRRP 通信正常，该节点仍可根据优先级成为 MASTER 并持有 VIP。

## 安装与模式切换

安装器保留零参数入口，并继续读取同目录 `keepalived.env`。

健康检查启用时：

1. 安装临时健康脚本。
2. 用临时脚本路径验证暂存配置。
3. 原子安装健康脚本、健康 URL 文件和正式配置。
4. 启动服务后执行一次健康探测；失败只告警，VRRP 实例保持 `FAULT`。

健康检查禁用时：

1. 直接验证不含健康引用的暂存配置。
2. 原子安装正式配置。
3. 不安装健康脚本和 URL 文件。
4. 删除当前安装树中由本模块管理的旧健康脚本和 URL 文件。
5. 启动服务后只验证 systemd active 和 Keepalived 配置语法，不执行健康探测。

模式切换继续使用现有完整安装根备份、服务状态快照和事务回滚。任何失败都恢复安装前的完整 `/aifar/apps/keepalived`、unit、防火墙与服务状态。因此：

- 启用切换到禁用时，成功后不留下旧健康文件；失败时恢复原启用模式。
- 禁用切换到启用时，成功后安装新健康文件；失败时恢复原禁用模式。

安装包中继续保留 `check-aggregate-health.sh`，因为同一离线包必须同时支持两种模式。禁用模式只是不会把它安装到应用目录。

## SELinux、防火墙和卸载

SELinux helper 继续管理自定义 Keepalived 安装根的发行版派生标签。禁用模式下允许 `libexec` 目录存在，但不得要求健康脚本文件存在。

firewalld 逻辑与健康模式无关，仍根据接口 zone 管理对端 `/32` 的 VRRP protocol 112 runtime/permanent 规则。

卸载器继续删除整个模块管理安装树，因此同时兼容两种模式，无需为缺失健康文件报错。

## 错误处理

- `KEEPALIVED_HEALTH_URL=`：明确报健康 URL 不能为空。
- 非空但格式、端口或主机校验失败：保持当前错误行为。
- 禁用模式生成的配置仍引用健康脚本：安装失败并回滚。
- 启用模式生成的配置没有引用临时健康脚本：安装失败并回滚。
- 模式切换中删除或安装健康文件失败：安装事务失败并恢复旧安装根。

## 测试与验收

自动化测试至少覆盖：

- 注释健康 URL 被当作缺失并成功解析；
- 完全缺失健康 URL 成功解析；
- 显式空值失败；
- 有效健康 URL 保持当前严格校验；
- 禁用模式配置不含 `script_user`、`enable_script_security`、`vrrp_script` 和 `track_script`；
- 启用模式配置保持现有健康块；
- 两种配置均通过 Keepalived 语法验证夹具；
- 禁用模式不安装或执行健康脚本，不创建健康 URL 文件；
- 启用到禁用成功移除旧健康文件；
- 禁用到启用成功安装健康文件；
- 两个方向的模式切换失败均恢复完整旧根和服务状态；
- `pnpm test:scripts`、Bash 语法检查和 `git diff --check` 通过。

真实 Linux 验收应分别验证：

1. 启用健康检查时，接口失败会进入 `FAULT` 并移除 VIP，恢复后重新参与选举。
2. 注释健康 URL 后重新安装，服务 active/enabled、配置无健康块、应用目录无健康脚本与 URL 文件，VIP 仅按 VRRP 优先级和节点状态选举。

## 兼容性说明

已有未修改的 `keepalived.env` 继续启用健康检查，行为不变。只有删除或注释 `KEEPALIVED_HEALTH_URL` 才切换到无健康检查模式。这是显式配置选择，不做自动降级。
