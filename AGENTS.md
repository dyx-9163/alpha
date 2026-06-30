# AIFAR Deployment Agent Guide

本文件是后续 agent 在 `D:\workspace\aifar-deployment` 工作时的最高优先级项目约定。它以当前源码为准，而不是旧打包产物的反推愿景。

## 必读流程

1. 每次开始处理用户问题前，先读取仓库根目录的 `memory.md`。
2. 如果问题涉及代码、架构、测试、文档或发布，再读取本文件。
3. 每次结束前，把本轮“问题”和“结论”精简追加到 `memory.md`。只记录可复用事实、决策和待办，不写密码、token、私钥、完整连接串、长日志。
4. 如果 `memory.md` 不存在，先创建，格式见本文件末尾。
5. 修改 `web/src` 下页面、布局、组件或样式前，必须先读取 `design/ant-design-system-portable202606.md`。

## 当前项目定位

AIFAR Deployment 是一个可离线部署的 Linux 运维面板：

- 后端：Go 1.24、Chi、SQLite(`modernc.org/sqlite`)、JWT、bcrypt、SSH、WebSocket、任务系统。
- 前端：Vue 3、TypeScript、Vite、Element Plus、Pinia、Vue Router、xterm.js。
- 运行形态：Go 单二进制托管 `/api/v2` 和 `web/dist` 静态资源。
- 数据库：默认 `data/aifar.db`。
- 离线资源：默认 `resources/<app>/<version>/`，当前包含 Docker、MySQL、Redis、MinIO。
- 打包结构：`bin/`、`web/dist/`、`resources/`、`config/defaults.env`、启动脚本。

## 当前代码功能快照

### 启动与脚本

- `backend/cmd/aifar-server/main.go` 加载配置，打开 SQLite，初始化 bootstrap 用户，扫描离线资源，创建 worker manager，并启动 HTTP 服务。
- `backend/cmd/aifar-admin/main.go` 支持 `inspect` 和 `reset-admin [username password]`。
- 根目录 `package.json` 提供 `pnpm dev`、`pnpm build`、`pnpm test`、`pnpm backend:dev`、`pnpm web:dev` 等命令。
- `scripts/toolchain.mjs` 优先使用 `D:\tools` 下 Node、Go、GOPATH、GOCACHE，并注入 AIFAR 默认环境变量。
- `scripts/build-web.mjs` 执行 `vue-tsc --noEmit` 与 Vite build；`scripts/build-backend.mjs` 交叉编译 Linux/Windows amd64 服务端二进制。
- `scripts/test.mjs` 当前只运行后端 `go test ./...`。

### 后端配置

`backend/internal/config` 读取：

- `AIFAR_ADDR`，默认 `0.0.0.0:8080`
- `AIFAR_STATIC_DIR`，默认 `web/dist`
- `AIFAR_RESOURCE_DIR`，默认 `resources`
- `AIFAR_DATABASE_PATH`，默认 `data/aifar.db`
- `AIFAR_DEFAULT_PASSWORD`，默认 `Oversea.123`
- `AIFAR_BOOTSTRAP_USERNAME`，默认 `admin`
- `AIFAR_BOOTSTRAP_PASSWORD`，默认使用默认密码
- `AIFAR_JWT_SECRET`
- `AIFAR_CREDENTIAL_SECRET`
- `AIFAR_DEPLOYMENT_CONCURRENCY`
- `AIFAR_DEFAULT_DEPLOY_DIR`，默认 `/aifar/apps`

### 后端 API

`backend/internal/httpapi/api.go` 使用 Chi 注册 `/api/v2`：

- Auth：`POST /auth/login`
- Settings：`GET/PUT /settings`
- Resources：`GET /resources`、`POST /resources/rescan`
- Servers：列表、保存、删除、探测、telemetry、SSH terminal WebSocket
- Tasks：列表、详情、SSE events、取消、删除、清日志
- Audit：列表和批量删除
- Apps：catalog、instances、install、check、delete/uninstall
- Containers：summary、containers/images/networks/volumes/df、start/stop/restart、logs
- Database：MySQL/Redis instances、MySQL/Redis install aliases
- Storage：MinIO instances、MinIO install alias、bucket/object/user/access-key/replica 控制面记录

所有已接入的变更操作必须返回 task id 或写审计。不要新增同步阻塞式远程变更 API。

### 鉴权与安全

- `backend/internal/auth` 使用 bcrypt 校验密码，JWT HS256 签发 24 小时 token。
- 前端 token 存在 `localStorage` 的 `aifar-session-token`。
- HTTP Bearer token 与 WebSocket 子协议 `aifar.auth.<base64url token>` 都可用于鉴权。
- `backend/internal/store/crypto.go` 使用 AES-GCM 加密保存 SSH 密码、私钥、对象存储 secret 等敏感字段。
- 删除部署服务需要用户输入目标服务器已保存的 SSH 密码确认。

### SQLite Store

`backend/internal/store` 当前用 `Store.migrate()` 做内嵌前向迁移，表包括：

- `users`
- `servers`
- `tasks`
- `task_logs`
- `task_targets`
- `task_steps`
- `audit_logs`
- `resources`
- `app_instances`
- `storage_items`
- `settings`

新增字段应继续通过集中迁移逻辑维护，并补充 store 测试。不要在 handler 或业务模块中散落建表。

### Worker 与任务

`backend/internal/worker` 支持：

- 创建并异步运行任务。
- 记录 `pending/running/success/failed/cancelled` 状态。
- 写入普通日志与 target 维度日志。
- 记录 `task_targets` 与 `task_steps`。
- SSE 订阅任务日志。
- 尽力取消正在运行的任务。

安装、卸载、检测、备份、资源扫描、服务器探测、容器变更等状态变更都必须走 worker 和审计。

### 服务器管理

- 后端业务在 `backend/internal/servers`。
- `Service.Save` 负责 normalize 和 validate，默认端口 22、认证方式 password、部署目录 `/aifar/apps`、状态 unknown。
- `Service.Probe` 通过 `servers.Prober` 注入，默认 `SSHProber` 调用 `adapter.ProbeSSH`。
- 探测任务步骤为 `load-server`、`check-credential`、`probe-ssh`、`collect-runtime`，会更新服务器状态。
- 前端服务器模块在 `web/src/servers`，`ServersView.vue` 只做页面编排。

### 资源扫描

`backend/internal/resource` 扫描 `resources`：

- 支持旧结构 `resources/<app>/<version>/`。
- 支持拆分命名 `<app>-frontend`、`<app>-backend`、`frontend/`、`backend/`。
- 读取 `manifest.json` 中的 file SHA256 和 part。
- 小于等于 512MiB 的资源文件会计算 SHA256；`.sha256sum`、`.minisig` 不计算。
- RPM 数量来自版本目录下 `rpms/*.rpm`。

当前资源目录包含：

- Docker `24.0.9`
- MySQL `8.0.36`
- Redis `7.2.14`
- MinIO `2025-10-15T17-29-55Z`
- MinIO Client `2025-08-13T08-35-41Z`
- MinIO 相关 Go `1.24.8` 与 Go module cache

### 应用 Registry

- `backend/internal/apps/registry` 是协议层，只允许定义接口、工厂注册、模块查询和排序，不得 import 具体应用。
- 具体应用通过 `init()` 调用 `registry.RegisterFactory(...)` 自注册。
- 静态编译通过 `backend/internal/apps/autoload` blank import Docker、MySQL、Redis、MinIO。
- 模块接口包括 `Manifest`、`PreflightInstall`、`PlanInstall`、`ValidateInstall`、`Install`。
- 可选接口包括 `DeleteModule` 和 `CheckModule`。
- `backend/internal/appcatalog` 将后端模块和离线资源合并成应用商店 catalog。

应用商店准入规则：

- 前端模块存在。
- 后端模块返回 `backendReady=true`。
- 必需离线资源存在。
- 三者缺一时不得执行部署。

### 当前后端应用模块

- Docker：`backend/internal/apps/docker`
  - 支持多服务器安装 Docker Engine + Compose。
  - 安装步骤：`load-server`、`install-engine`、`update-server`、`record-instance`。
  - 安装后写入服务器 `DockerHost` 与 `app_instances`。
  - 支持远程卸载、删除实例、更新服务器状态。
  - 支持实例状态检测，检查 Docker/Compose 版本、systemd unit、安装目录。
- MySQL：`backend/internal/apps/mysql`
  - 支持 standalone 和 InnoDB Cluster。
  - standalone 单目标；InnoDB Cluster 至少 3 台。
  - 支持安装、集群 bootstrap、记录实例、远程卸载和删除实例。
- Redis：`backend/internal/apps/redis`
  - 支持 standalone、Sentinel、Cluster。
  - standalone 单目标；Sentinel/Cluster 至少 3 台。
  - 支持安装、Sentinel 配置、Cluster enable/bootstrap、记录实例、远程卸载和删除实例。
- MinIO：`backend/internal/apps/minio`
  - 支持 standalone 和 distributed。
  - distributed 至少 4 台。
  - 需要 MinIO 包、mc、Go 工具链/缓存和 RPM 缓存。
  - 支持安装、分布式节点配置、记录实例、远程卸载和删除实例。

### 安装器与 Adapter

- SSH 运行命令和上传文件在 `backend/internal/adapter/ssh.go`，具体安装器通过接口依赖它。
- Docker 运行时管理在 `backend/internal/adapter/docker.go`，当前通过本机 `docker` CLI 或 SSH 到目标服务器执行 Docker CLI，不是 Docker Go client。
- 具体应用安装器在 `backend/internal/apps/<app>`，负责选择资源、校验 SHA256、上传资源/RPM、生成脚本、执行安装或卸载。
- 共享安装器工具在 `backend/internal/installer/{installerkit,uploadkit}`。
- 安装脚本模板在对应应用的 `templates/` 目录，用 `go:embed` 加载，并支持 `config/installers` 覆盖。

### 当前前端功能

- `web/src/api/client.ts` 统一 `/api/v2`、Bearer token、`X-AIFAR-Language`、错误转换和终端 WebSocket URL。
- `web/src/router/index.ts` 路由包括 login、dashboard、apps、containers、servers、database、storage、terminal、tasks、toolbox、audit、settings。
- `web/src/stores/session.ts` 管理登录态。
- `web/src/i18n` 提供 zh/en 文案与 Element Plus 语言切换入口。
- `web/src/App.vue` 提供深色侧边导航和语言切换。
- `web/src/styles.css` 定义 AIFAR token：深色侧栏、浅色工作区、白色面板、蓝色主操作、绿色成功、橙色告警。

### 前端应用商店

- `web/src/apps/registry/loader.ts` 用 `import.meta.glob('../*/index.ts')` 自动发现应用模块。
- 当前前端模块：Docker、MySQL、Redis、MinIO。
- `web/src/apps/registry/catalog.ts` 只合并前端模块和后端 catalog 中可配对应用。
- `web/src/components/AppInstallDialog.vue` 是共享安装弹窗，支持版本选择、单/多目标服务器、动态字段、拓扑影响 target mode。
- `web/src/views/AppsView.vue` 展示可配对应用、缺失状态、部署记录、实例检测与删除。

### 前端页面现状

- Dashboard：汇总服务器、Docker、数据库、存储等控制面状态。
- Servers：企业工作台，使用 `web/src/servers` 模块管理列表、表单、详情和探测。
- Apps：企业应用商店，使用前后端 registry + 资源准入。
- Containers：支持本机 Docker host 或选择服务器，经 API 查询 summary、containers、images、networks、volumes、df，支持容器 start/stop/restart 与 logs。
- Database：聚合展示 MySQL/Redis/MySQL Router app instances，支持跳转应用商店部署，并通过实时监测展示数据库/Redis 节点状态与角色。
- Storage：展示 MinIO instances，维护 bucket/object/user/access-key/replica 的控制面记录；当前不是完整 MinIO S3/mc 实操。
- Terminal：xterm.js 连接服务器 SSH WebSocket。
- Tasks：任务中心展示任务列表、target 分组、步骤状态和 SSE 日志。
- Audit：审计列表与删除。
- Settings：面板设置与语言。
- Toolbox：诊断/工具入口。

## 当前明确缺口

- Storage 的 bucket/object/user/access-key/replica 是控制面记录，不等同于真实 MinIO API 操作。
- Docker runtime 管理依赖 Docker CLI，不是 Go Docker client。
- Store 迁移当前为内嵌 `migrate()`，没有独立 migrations 目录。

## 工程约束

### 通用

- 优先沿用当前目录、接口和测试模式，不做无关重构。
- 新增状态变更必须写任务、步骤、目标和审计。
- 错误响应保持 `{ code, message, details }`。
- API 前缀保持 `/api/v2`。
- 终端 WebSocket 保持 `/api/v2/servers/{id}/terminal/ws`。
- 稳定机器码使用英文，例如 `running`、`apps.mysql.install`；展示层负责翻译。

### 后端

- HTTP handler 只做请求解析、鉴权、任务创建、审计记录和响应；业务逻辑放 service/module/installer。
- 应用安装逻辑不得写进 `httpapi`。
- 新应用必须实现 registry Module 生命周期，并通过 `init()` 自注册。
- `backend/internal/apps/registry` 不得 import 任何具体应用。
- 安装器只能通过 adapter 接口执行远程命令和上传文件。
- API 不得接收并直接执行自由 shell。
- 敏感字段必须加密或哈希保存，日志和审计必须脱敏。
- 服务器探测必须通过 `servers.Prober` 注入，测试使用 fake prober。
- 新增后端用户可见文案、任务日志、错误消息必须进入 `backend/internal/i18n` 的 zh/en。

### 前端

- 修改 `web/src` 视觉、布局、组件前先读 `design/ant-design-system-portable202606.md`。
- 用户可见文案必须走 `web/src/i18n/messages.ts` 或应用模块私有 i18n，提供 zh/en。
- API 请求必须继续携带 `X-AIFAR-Language`。
- 应用商店不得在 `AppsView.vue` 手写具体应用业务分支；应用能力来自 `web/src/apps/<app>` 模块。
- 新应用前端模块必须导出 `AppFrontendModule`，由 `import.meta.glob` 自动发现。
- 安装入口必须是“打开配置弹窗 -> 选择版本和服务器/拓扑 -> 提交任务”，不得卡片按钮直接部署。
- 服务器页面不得暴露 Docker 安装入口；Docker 安装属于 Apps，Docker runtime 属于 Containers。

### 测试与验证

- 后端变更运行 `pnpm test` 或在 `backend/` 下运行 `go test ./...`。
- 前端类型或 UI 变更运行 `pnpm web:build`，必要时运行 `pnpm build`。
- 应用安装器、资源扫描、store、server service、registry 变更必须补充或更新对应 Go 测试。
- 涉及真实 SSH/Docker 的测试必须使用 fake remote/prober，不连接真实服务器。

## memory.md 格式

```markdown
# AIFAR Memory

本文件记录后续对话的精简问题与结论。每次开始先读，结束前追加。禁止写入密码、token、私钥、完整连接串和长日志。

## YYYY-MM-DD
- 问题：...
- 结论：...
```
