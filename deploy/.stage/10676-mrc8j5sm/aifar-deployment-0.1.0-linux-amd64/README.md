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
pnpm backend:dev
pnpm web:dev
```

服务端读取以下环境变量：

The server reads these environment variables:

- `AIFAR_ADDR`, 默认 / default `0.0.0.0:8080`
- `AIFAR_STATIC_DIR`, 默认 / default `web/dist`
- `AIFAR_RESOURCE_DIR`, 默认 / default `resources`
- `AIFAR_DATABASE_PATH`, 默认 / default `data/aifar.db`
- `AIFAR_DEFAULT_DEPLOY_DIR`, 默认 / default `/aifar/apps`
- `AIFAR_BOOTSTRAP_USERNAME`, 默认 / default `admin`
- `AIFAR_BOOTSTRAP_PASSWORD`, 默认使用 / default from `AIFAR_DEFAULT_PASSWORD`

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
```

发布包按平台生成：

Release packages are platform-specific:

- `deploy/deployment/aifar-deployment-<version>-linux-amd64/` and `.tar.gz`
- `deploy/deployment/aifar-deployment-<version>-windows-amd64/` and `.zip`

每个包只包含运行时资产：`bin/`、`web/dist/`、存在时的 `resources/`、`config/`、启动脚本、`VERSION` 和 `checksums.txt`。源码、`node_modules/`、`data/`、日志、缓存和开发脚本不会包含在发布包中。

Each package contains only runtime assets: `bin/`, `web/dist/`, `resources/` when present, `config/`, startup scripts, `VERSION`, and `checksums.txt`. Source code, `node_modules/`, `data/`, logs, caches, and development scripts are not included.
