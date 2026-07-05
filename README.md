# AIFAR Deployment

Rebuilt source tree for the AIFAR Linux deployment/control panel, based on the surviving `aifar-deployment.zip` release package.

## Stack

- Backend: Go 1.24, Chi, SQLite via `modernc.org/sqlite`, JWT, bcrypt, SSH, Docker CLI adapter.
- Frontend: Vue 3, TypeScript, Vite, Element Plus, Pinia, Vue Router, xterm.js.
- Runtime package layout: `bin/`, `web/dist/`, `resources/`, `config/defaults.env`, startup scripts.

## Development

```bash
pnpm install
pnpm dev
```

Run from the repository root. `pnpm dev` starts the Go API on `127.0.0.1:8080` and the Vite frontend on `127.0.0.1:5173`.

Useful root commands:

```bash
pnpm build
pnpm package
pnpm test
pnpm backend:dev
pnpm web:dev
```

The server reads:

- `AIFAR_ADDR`, default `0.0.0.0:8080`
- `AIFAR_STATIC_DIR`, default `web/dist`
- `AIFAR_RESOURCE_DIR`, default `resources`
- `AIFAR_DATABASE_PATH`, default `data/aifar.db`
- `AIFAR_DEFAULT_DEPLOY_DIR`, default `/aifar/apps`
- `AIFAR_BOOTSTRAP_USERNAME`, default `admin`
- `AIFAR_BOOTSTRAP_PASSWORD`, default from `AIFAR_DEFAULT_PASSWORD`

## AIFAR agent runtime commands

`aifar-agent` is the local runtime-v2 controller and service data plane used by
AIFAR application deployments. The long-running command is `serve`; most other
commands are small clients that either check local prerequisites or call the
agent API on `127.0.0.1:18081`.

| Command | Purpose |
| --- | --- |
| `aifar-agent health` | Checks whether the host Docker daemon is reachable by running `docker info`. Prints `{"status":"ok"}` on success. |
| `aifar-agent serve --addr 127.0.0.1:18081` | Starts the persistent agent. It loads runtime specs from `/var/lib/aifar-agent/instances`, reconciles Docker pods, keeps the endpoint cache fresh, listens on service proxy ports, watches Docker events, runs periodic resync, and maintains Nacos proxy registration and heartbeat. |
| `aifar-agent status --addr 127.0.0.1:18081` | Reads the running agent status, including listeners, instances, deployments, services, endpoints, Nacos config, and feature flags. Requires `serve` to be running. |
| `aifar-agent reconcile-runtime --spec runtime-spec.json --addr 127.0.0.1:18081` | Submits a RuntimeSpec v2 to the running agent. The agent aligns desired state by creating, deleting, or rolling Docker pods; refreshing endpoints; starting service proxy listeners; persisting the spec; and registering Nacos proxy instances. Installs, service add-ons, scale-out, and updates should use this command. |
| `aifar-agent reconcile-ingress --spec runtime-spec.json --addr 127.0.0.1:18081` | Compatibility alias for `reconcile-runtime`, kept for older scripts. Prefer `reconcile-runtime` in new code. |
| `aifar-agent reconcile --spec runtime-spec.json --addr 127.0.0.1:18081` | Compatibility alias for `reconcile-runtime`. |
| `aifar-agent remove-instance --instance admin --addr 127.0.0.1:18081` | Removes one runtime instance from the running agent, stops unused proxy listeners, deletes the local agent state for that instance, and tries to deregister its Nacos proxy instances. This is not a full business uninstall and does not remove the install root by itself. |
| `aifar-agent register-nacos --state-dir /var/lib/aifar-agent/instances` | Replays Nacos proxy registrations for all specs in the state directory. Nacos receives stable `agentIP:servicePort` entries, not pod IPs. |
| `aifar-agent register-nacos --spec runtime-spec.json --agent-ip 192.168.x.x` | Registers Nacos proxy instances for one spec and optionally forces the registered agent IP. |
| `aifar-agent register-nacos-proxies ...` | Alias for `register-nacos`. |
| `aifar-agent deregister-nacos --state-dir /var/lib/aifar-agent/instances` | Deregisters Nacos proxy instances for specs in the state directory. This is used by stop hooks and manual cleanup. |
| `aifar-agent deregister-nacos --spec runtime-spec.json --agent-ip 192.168.x.x` | Deregisters Nacos proxy instances for one spec and optional agent IP. |
| `aifar-agent deregister-nacos-proxies ...` | Alias for `deregister-nacos`. |

## Offline resources

The large resource files are not recreated as source. Extract them from the surviving package:

```bash
sh scripts/extract-resources.sh
```

On Windows:

```powershell
.\scripts\extract-resources.ps1
```

## Packaging

Use `scripts/package.sh` or `scripts/package.ps1` after Go and pnpm are available. They install dependencies, build package-only artifacts under `deploy/bin` and `deploy/dist`, then stage clean runtime packages under `deploy/deployment`.

```bash
sh scripts/package.sh
```

On Windows:

```powershell
.\scripts\package.ps1
```

You can also run:

```bash
pnpm package
```

Release packages are platform-specific:

- `deploy/deployment/aifar-deployment-<version>-linux-amd64/` and `.tar.gz`
- `deploy/deployment/aifar-deployment-<version>-windows-amd64/` and `.zip`

Each package contains only runtime assets: `bin/`, `web/dist/`, `resources/` when present, `config/`, startup scripts, `VERSION`, and `checksums.txt`. Source code, `node_modules/`, `data/`, logs, caches, and development scripts are not included.
