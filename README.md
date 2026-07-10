# AIFAR Deployment

AIFAR Deployment 是一个可离线部署的 Linux 运维与应用部署控制面。本仓库是基于现有 `aifar-deployment.zip` 发布包恢复并继续重构的源码树。

AIFAR Deployment is an offline-capable Linux operations and application deployment control panel. This repository is the rebuilt and actively refactored source tree based on the surviving `aifar-deployment.zip` release package.

## 技术栈 / Stack

- 后端 / Backend: Go 1.24, Chi, SQLite via `modernc.org/sqlite`, JWT, bcrypt, SSH, Docker CLI adapter.
- 前端 / Frontend: Vue 3, TypeScript, Vite, Element Plus, Pinia, Vue Router, xterm.js.
- 运行时包结构 / Runtime package layout: `bin/`, `web/dist/`, `resources/`, `config/defaults.env`, startup scripts.

## 开发 / Development

在仓库根目录运行：

Run from the repository root:

```bash
pnpm install
pnpm dev
```

`pnpm dev` 会启动 Go API `127.0.0.1:8080` 和 Vite 前端 `127.0.0.1:5173`。

`pnpm dev` starts the Go API on `127.0.0.1:8080` and the Vite frontend on `127.0.0.1:5173`.

常用根目录命令 / Useful root commands:

```bash
pnpm build
pnpm package
pnpm test
pnpm test:web
pnpm test:scripts
pnpm test:local
pnpm backend:dev
pnpm web:dev
```

`pnpm test:local` 是完整本地门禁：依次运行后端测试、前端 Vitest、脚本测试、前后端构建、一次 `pnpm package`，最后执行 `pnpm release:verify`。它会复制离线资源，可能处理数 GB 文件；日常快速验证优先使用 `pnpm test`、`pnpm test:web`、`pnpm test:scripts`、`pnpm web:build` 和 `pnpm backend:build`。

服务端读取以下环境变量：

The server reads these environment variables:

- `AIFAR_ADDR`, 默认 / default `0.0.0.0:8080`
- `AIFAR_STATIC_DIR`, 默认 / default `web/dist`
- `AIFAR_RESOURCE_DIR`, 默认 / default `resources`
- `AIFAR_DATABASE_PATH`, 默认 / default `data/aifar.db`
- `AIFAR_DEFAULT_DEPLOY_DIR`, 默认 / default `/aifar/apps`
- `AIFAR_BOOTSTRAP_USERNAME`, 默认 / default `admin`
- `AIFAR_BOOTSTRAP_PASSWORD`, 默认使用 / default from `AIFAR_DEFAULT_PASSWORD`

## 生产部署配置建议 / Production Configuration

生产包会读取 `config/defaults.env`，也可以通过系统环境变量覆盖。仓库中的密码和密钥默认留空；未显式配置强密码、JWT 密钥和凭据加密密钥时，服务会拒绝启动。上线前建议至少确认：

- `AIFAR_ADDR=0.0.0.0:8080`，如前面有 Nginx/负载均衡，也可以只监听内网地址。
- `AIFAR_DATABASE_PATH=/data/aifar/aifar.db`，不要放在临时目录；该路径需要随服务器备份。
- `AIFAR_DATABASE_BACKUP_DIR=/data/aifar/backups`，建议和数据库同盘不同目录，条件允许时同步到异地。
- `AIFAR_RESOURCE_DIR=/opt/aifar/resources`，放离线资源包；AIFAR runtime-v2、Docker、MySQL、Redis、MinIO、Nacos 等版本由目录扫描发现。
- `AIFAR_STATIC_DIR=web/dist`，生产包默认即可。
- `AIFAR_DEFAULT_DEPLOY_DIR=/aifar/apps`，目标服务器上应用安装根目录，要求磁盘空间充足。
- `AIFAR_DEPLOYMENT_CONCURRENCY=2` 起步；目标机、网络和磁盘稳定后可调到 `3` 或 `4`。
- `AIFAR_COLLECTOR_INTERVAL_SECONDS=15` 常规使用；低配服务器或目标很多时可调到 `60`。
- `AIFAR_MAX_REQUEST_BODY_BYTES=4294967296` 默认 4 GiB；如果上传 AIFAR 批量制品包可能超过 4 GiB，建议改为 `8589934592`。
- `AIFAR_AUTH_MAX_FAILURES=5`、`AIFAR_AUTH_LOCKOUT_SECONDS=300` 用于登录失败锁定。
- `AIFAR_AUDIT_RETENTION_DAYS=180`、`AIFAR_TASK_RETENTION_DAYS=90` 控制审计与已完成任务保留周期。
- `AIFAR_JWT_SECRET` 和 `AIFAR_CREDENTIAL_SECRET` 必须分别显式设置为至少 32 字符的稳定、高强度随机值，且两者不能相同。
- `AIFAR_DEFAULT_PASSWORD`、`AIFAR_BOOTSTRAP_PASSWORD` 必须设置为至少 12 字符，且不要提交真实密码到仓库。
- `AIFAR_ALLOW_INSECURE_DEFAULTS=true` 只在 `AIFAR_ADDR` 的监听主机严格为 `127.0.0.1`、`localhost` 或 `::1` 时生效；`:8080`、`0.0.0.0`、`::` 和普通主机名都会拒绝启动，且不会做 DNS 解析。生产配置和发布包必须保持 `false`。

`pnpm dev` 和 `pnpm backend:dev` 仅在上述 loopback 地址且进程环境中完全未设置 `AIFAR_ALLOW_INSECURE_DEFAULTS` 时自动启用开发放行；显式设置 `false` 会被保留。即使本地放行已启用，当前与 previous 凭据密钥相同等结构性密钥错误仍会拒绝启动。

轮换 `AIFAR_CREDENTIAL_SECRET` 时，不要直接丢弃旧值。第一次使用新密钥启动时，同时通过环境变量设置 `AIFAR_PREVIOUS_CREDENTIAL_SECRET=<旧密钥>`；服务会在单个数据库事务中重加密服务器密码、凭据版本、存储密钥和 Nacos 配置。启动日志确认轮换成功后，立即移除 previous secret。轮换失败会回滚全部密文并拒绝启动。

后续可以继续抽出的配置项：

- `AIFAR_AGENT_BINARY`：用于指定自定义 `aifar-agent` 二进制。
- `AIFAR_BIN_DIR`、`AIFAR_WEB_OUT_DIR`：构建/打包输出目录，主要给 CI 使用。
- 发布包资源 include/exclude 策略：例如是否同时保留 `resources/aifar.zip` 和展开后的 `resources/aifar/runtime-v2`。
- AIFAR Runtime 默认健康检查、端口、启动超时：当前在 `resources/aifar/runtime-v2/runtime/defaults.env`。
- AIFAR 自动扩缩容策略：当前默认内存阈值 80%、持续 5 分钟、冷却 10 分钟、最大副本 3。
- 各应用安装默认参数：Docker bridge CIDR/API 端口，MySQL/Redis/MinIO/Nacos 默认端口、数据目录、复制和 JVM 参数。

## Docker 宿主机系统参数建议 / Docker Host sysctl

这些参数面向承载 AIFAR Runtime 和业务容器的 Docker 宿主机，主要用于缓解短连接、`TIME_WAIT`、临时端口和 conntrack 压力。不要直接把参数无差别套到所有机器；先采集 `ss -s`、`/proc/sys/net/ipv4/ip_local_port_range`、`nf_conntrack_count/max`、`fs.file-nr`，再持久化。

不建议长期执行 `sysctl -w net.ipv4.tcp_tw_reuse=0`。新内核通常默认 `2`，表示仅 loopback 复用，更适合作为保守默认；`0` 可用于短时排查，不适合作为常态优化。

建议草案：

```conf
net.ipv4.tcp_timestamps = 1
net.ipv4.tcp_tw_reuse = 2
net.ipv4.tcp_max_tw_buckets = 500000
net.ipv4.ip_local_port_range = 10000 65535
net.ipv4.ip_local_reserved_ports = 2375,3306,6379,6446-6449,8080,8848,9000-9001,9848,18081,26379,38000-38033
net.ipv4.tcp_fin_timeout = 15
net.netfilter.nf_conntrack_max = 1048576
net.netfilter.nf_conntrack_tcp_timeout_time_wait = 60
fs.file-max = 2097152
net.core.somaxconn = 4096
net.ipv4.tcp_max_syn_backlog = 8192
```

持久化示例：

```bash
sudo tee /etc/sysctl.d/99-aifar-docker-runtime.conf >/dev/null <<'EOF'
net.ipv4.tcp_timestamps = 1
net.ipv4.tcp_tw_reuse = 2
net.ipv4.tcp_max_tw_buckets = 500000
net.ipv4.ip_local_port_range = 10000 65535
net.ipv4.ip_local_reserved_ports = 2375,3306,6379,6446-6449,8080,8848,9000-9001,9848,18081,26379,38000-38033
net.ipv4.tcp_fin_timeout = 15
net.netfilter.nf_conntrack_max = 1048576
net.netfilter.nf_conntrack_tcp_timeout_time_wait = 60
fs.file-max = 2097152
net.core.somaxconn = 4096
net.ipv4.tcp_max_syn_backlog = 8192
EOF
sudo sysctl --system
```

如果仍出现 `cannot assign requested address`，优先检查发起连接的一侧：容器网络命名空间里的临时端口、`TIME_WAIT` 分布、连接池复用情况，以及宿主机 Docker bridge/NAT 和 conntrack 是否接近上限。

## AIFAR Agent Runtime 命令 / AIFAR Agent Runtime Commands

`aifar-agent` 是 AIFAR runtime-v2 的本机轻量控制器和 Service 数据面。长期运行命令是 `serve`；其他命令主要用于本地检查，或作为客户端调用 `127.0.0.1:18081` 上的 agent API。

`aifar-agent` is the local runtime-v2 controller and service data plane used by AIFAR application deployments. The long-running command is `serve`; most other commands either check local prerequisites or call the agent API on `127.0.0.1:18081`.

| 命令 / Command | 用途 / Purpose |
| --- | --- |
| `aifar-agent health` | 检查宿主机 Docker daemon 是否可访问，内部执行 `docker info`；成功输出 `{"status":"ok"}`。<br>Checks whether the host Docker daemon is reachable by running `docker info`; prints `{"status":"ok"}` on success. |
| `aifar-agent serve --addr 127.0.0.1:18081` | 启动常驻 agent。它从 `/var/lib/aifar-agent/instances` 加载 runtime spec，对齐 Docker Pod，维护 endpoint cache，监听 Service 代理端口，监听 Docker events，执行周期 resync，并维护 Nacos 代理注册和心跳。<br>Starts the persistent agent. It loads runtime specs from `/var/lib/aifar-agent/instances`, reconciles Docker pods, keeps the endpoint cache fresh, listens on service proxy ports, watches Docker events, runs periodic resync, and maintains Nacos proxy registration and heartbeat. |
| `aifar-agent status --addr 127.0.0.1:18081` | 查询运行中的 agent 状态，包括 listeners、instances、deployments、services、endpoints、Nacos 配置和 feature flags；需要 `serve` 已运行。<br>Reads the running agent status, including listeners, instances, deployments, services, endpoints, Nacos config, and feature flags; requires `serve` to be running. |
| `aifar-agent reconcile-runtime --spec runtime-spec.json --addr 127.0.0.1:18081` | 提交 RuntimeSpec v2 给运行中的 agent。agent 会创建、删除或滚动 Docker Pod，刷新 endpoints，启动 Service 代理监听，持久化 spec，并注册 Nacos 代理实例。安装、补装、扩容和更新应使用该命令。<br>Submits a RuntimeSpec v2 to the running agent. The agent creates, deletes, or rolls Docker pods, refreshes endpoints, starts service proxy listeners, persists the spec, and registers Nacos proxy instances. Installs, service add-ons, scale-out, and updates should use this command. |
| `aifar-agent reconcile-ingress --spec runtime-spec.json --addr 127.0.0.1:18081` | `reconcile-runtime` 的兼容别名，用于旧脚本；新代码优先使用 `reconcile-runtime`。<br>Compatibility alias for `reconcile-runtime`, kept for older scripts. Prefer `reconcile-runtime` in new code. |
| `aifar-agent reconcile --spec runtime-spec.json --addr 127.0.0.1:18081` | `reconcile-runtime` 的兼容别名。<br>Compatibility alias for `reconcile-runtime`. |
| `aifar-agent remove-instance --instance admin --addr 127.0.0.1:18081` | 从运行中的 agent 移除一个 runtime instance，停止不再使用的代理监听，删除该实例的 agent 本地 state，并尽力摘除 Nacos 代理实例。该命令不是完整业务卸载，不会单独删除安装目录。<br>Removes one runtime instance from the running agent, stops unused proxy listeners, deletes local agent state for that instance, and tries to deregister its Nacos proxy instances. This is not a full business uninstall and does not remove the install root by itself. |
| `aifar-agent register-nacos --state-dir /var/lib/aifar-agent/instances` | 按 state 目录中的所有 spec 重放 Nacos 代理注册。Nacos 注册的是稳定的 `agentIP:servicePort`，不是 Pod IP。<br>Replays Nacos proxy registrations for all specs in the state directory. Nacos receives stable `agentIP:servicePort` entries, not pod IPs. |
| `aifar-agent register-nacos --spec runtime-spec.json --agent-ip 192.168.x.x` | 只按指定 spec 注册 Nacos 代理实例，并可强制指定注册 IP。<br>Registers Nacos proxy instances for one spec and optionally forces the registered agent IP. |
| `aifar-agent register-nacos-proxies ...` | `register-nacos` 的同义别名。<br>Alias for `register-nacos`. |
| `aifar-agent deregister-nacos --state-dir /var/lib/aifar-agent/instances` | 按 state 目录中的 spec 摘除 Nacos 代理实例，常用于停止钩子和手工清理。<br>Deregisters Nacos proxy instances for specs in the state directory. This is used by stop hooks and manual cleanup. |
| `aifar-agent deregister-nacos --spec runtime-spec.json --agent-ip 192.168.x.x` | 只按指定 spec 和可选 agent IP 摘除 Nacos 代理实例。<br>Deregisters Nacos proxy instances for one spec and optional agent IP. |
| `aifar-agent deregister-nacos-proxies ...` | `deregister-nacos` 的同义别名。<br>Alias for `deregister-nacos`. |

## 离线资源 / Offline Resources

大体积离线资源文件不会作为源码重建。请从现有发布包中提取：

The large resource files are not recreated as source. Extract them from the surviving package:

```bash
sh scripts/extract-resources.sh
```

Windows:

```powershell
.\scripts\extract-resources.ps1
```

## 打包 / Packaging

Go 和 pnpm 可用后，使用 `scripts/package.sh` 或 `scripts/package.ps1`。脚本会安装依赖，构建 package-only 产物到 `deploy/bin` 和 `deploy/dist`，再在 `deploy/deployment` 下生成干净的运行时包。

Use `scripts/package.sh` or `scripts/package.ps1` after Go and pnpm are available. They install dependencies, build package-only artifacts under `deploy/bin` and `deploy/dist`, then stage clean runtime packages under `deploy/deployment`.

```bash
sh scripts/package.sh
```

Windows:

```powershell
.\scripts\package.ps1
```

也可以直接运行：

You can also run:

```bash
pnpm package
pnpm release:verify
```

发布包按平台生成：

Release packages are platform-specific:

- `deploy/deployment/aifar-deployment-<version>-linux-amd64/` and `.tar.gz`
- `deploy/deployment/aifar-deployment-<version>-windows-amd64/` and `.zip`

每个包只包含运行时资产：`bin/`、`web/dist/`、`resources/`、`config/`、启动脚本、`VERSION` 和 `checksums.txt`。源码、`node_modules/`、`data/`、日志、缓存和开发脚本不会包含在发布包中。

Each package contains only runtime assets: `bin/`, `web/dist/`, `resources/`, `config/`, startup scripts, `VERSION`, and `checksums.txt`. Source code, `node_modules/`, `data/`, logs, caches, and development scripts are not included.

`pnpm release:verify` 只接受当前 `package.json` 版本对应的 Linux/Windows 两个平台目录和归档；缺失、旧版本、额外平台、损坏归档、归档内部 checksum 不一致、`config/defaults.env` 含弱密钥或敏感默认值都会失败。验证器会解压 `.tar.gz` 和 `.zip` 后复验内部文件全集与 `checksums.txt`。
