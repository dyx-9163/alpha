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

Use `scripts/package.sh` or `scripts/package.ps1` after Go and pnpm are available. They install dependencies, build the frontend and backend, then stage clean runtime packages under `dist/`.

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

- `dist/aifar-deployment-<version>-linux-amd64/` and `.tar.gz`
- `dist/aifar-deployment-<version>-windows-amd64/` and `.zip`

Each package contains only runtime assets: `bin/`, `web/dist/`, `resources/` when present, `config/`, startup scripts, `VERSION`, and `checksums.txt`. Source code, `node_modules/`, `data/`, logs, caches, and development scripts are not included.
