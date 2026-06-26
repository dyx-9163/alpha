# AIFAR Deployment 重建技术架构约定

本文件基于 `aifar-deployment.zip` 打包产物反推，用于在源码丢失后重新定义技术栈、目录、模块边界和工程约束。打包产物无法 100% 还原源码结构，但足够确认这是一个 Go 后端 + Vue 前端 + 离线资源包的 Linux 面板/部署管理工具。

## 打包产物事实

从 `aifar-deployment.zip` 可确认以下事实：

- 运行入口：`start.sh`、`stop.sh`、`start.ps1`、`start.bat`。
- 后端产物：`bin/aifar-server-linux-amd64`、`bin/aifar-server-windows-amd64.exe`。
- 前端产物：`web/dist/index.html` 与 Vite 风格的 `web/dist/assets/*.js`、`*.css`。
- 默认监听：`AIFAR_ADDR=0.0.0.0:8080`。
- 静态目录：启动脚本设置 `AIFAR_STATIC_DIR=web/dist`，说明后端直接托管前端静态资源。
- 离线资源目录：启动脚本设置 `AIFAR_RESOURCE_DIR=resources`。
- 本地数据库：README 明确 SQLite 控制面数据写入 `data/aifar.db`。
- 默认账号初始化：`AIFAR_BOOTSTRAP_USERNAME`、`AIFAR_BOOTSTRAP_PASSWORD`，默认密码来自 `config/defaults.env` 的 `AIFAR_DEFAULT_PASSWORD`。
- API 前缀：前端构建产物使用 `/api/v2`。
- 终端 WebSocket：前端构建产物存在 `/api/v2/servers/{id}/terminal/ws`。
- 前端模块：Dashboard、App Store、Containers、Servers、Database、Object Storage、Terminal、Toolbox、Log Audit、Panel Settings。
- 前端库线索：构建产物包含 Vue、Pinia、Element Plus、xterm 相关 vendor chunk。
- 后端语言线索：后端二进制包含 Go build 信息。
- 后端依赖线索：二进制字符串可见 `github.com/docker/docker/client`、`golang.org/x/crypto/ssh`、`golang.org/x/crypto/bcrypt`、`github.com/golang-jwt/jwt/v4`、`modernc.org/sqlite`。
- 离线安装资源：包含 Docker 24.0.9、MySQL 8.0.36、Redis 7.2.14、MinIO 2025-10-15、MinIO Client 2025-08-13、Go 1.24.8、RPM 依赖缓存、Go module cache。

## 重建目标

重新实现一个可离线部署的 Linux 面板工具：

1. 后端使用 Go 单二进制承载 API、任务、静态资源、WebSocket 和离线部署逻辑。
2. 前端使用 Vue 3 管理控制台页面，构建后由后端托管。
3. 控制面数据默认使用 SQLite，保持单机部署简单。
4. 目标服务器操作通过 SSH、Docker API、系统命令适配器完成。
5. 应用安装优先支持离线资源包，资源统一放在 `resources/<app>/<version>/`。
6. 所有状态变更必须进入任务系统和审计日志。

## 推荐技术栈

| 层 | 技术选型 | 理由 |
| --- | --- | --- |
| 后端语言 | Go 1.24.x | 原包内置 Go 1.24.8 和 Go 构建二进制；Go 适合单文件部署、长任务、并发日志流、SSH/Docker 操作。 |
| HTTP 服务 | Go `net/http` + Chi 或 Gin | 原包无法确认具体框架；重建时优先选轻量路由。若追求少依赖选 Chi，若追求开发速度选 Gin。 |
| 数据库 | SQLite | 原 README 明确使用 `data/aifar.db`；单机面板不应强依赖外部数据库。 |
| SQLite Driver | `modernc.org/sqlite` | 二进制可见该依赖；纯 Go SQLite 更适合交叉编译与离线打包。 |
| 鉴权 | JWT + bcrypt | 二进制可见 `github.com/golang-jwt/jwt/v4` 与 `golang.org/x/crypto/bcrypt`；适合本地面板会话和密码哈希。 |
| 远程连接 | `golang.org/x/crypto/ssh` | 二进制可见 SSH 依赖；面板需要对目标服务器执行安装、日志、终端、文件操作。 |
| 容器管理 | Docker Engine API / Go Docker client | 二进制可见 Docker client；资源包内含 Docker 24.0.9 离线包。 |
| 前端框架 | Vue 3 + TypeScript + Vite | `web/dist` 是 Vite 构建产物，vendor chunk 可见 Vue/Pinia；适合后台管理台。 |
| UI 组件 | Element Plus | 构建产物包含 Element Plus vendor；适合表格、表单、弹窗、抽屉、标签页等面板 UI。 |
| 状态管理 | Pinia | 构建产物包含 Pinia；适合会话、目标服务器、任务状态和全局设置。 |
| 实时通信 | WebSocket + SSE | 已有终端 WebSocket；部署日志、任务进度、终端输出也应走流式协议。 |
| Web Terminal | xterm.js | 构建产物包含 terminal vendor；继续用于服务器终端和容器终端。 |
| 离线资源 | `resources/<app>/<version>/` | 原 README 和 zip 结构已经固定该模式；支持 Docker、MySQL、Redis、MinIO 等离线部署。 |
| 跨平台启动 | shell + PowerShell + bat | 原包同时支持 Linux 与 Windows 启动；重建发布包继续保留。 |

## 不采用的主栈

- 不把 Python/Flask 作为主后端：当前包没有 Python 运行时线索，且 Go 二进制已确认。
- 不默认引入 PostgreSQL/MySQL 作为控制面数据库：会破坏单机离线部署体验。
- 不直接拼接散落 shell：系统操作必须通过后端 adapter 封装，记录任务日志和审计。
- 不优先做 Kubernetes：当前包面向单机/多服务器部署和 Docker 离线资源，不是集群控制面。

## 目标目录

```text
.
├── AGENTS.md
├── SKILL.md
├── backend/
│   ├── cmd/
│   │   └── aifar-server/
│   ├── internal/
│   │   ├── api/
│   │   ├── auth/
│   │   ├── config/
│   │   ├── domain/
│   │   ├── adapter/
│   │   ├── worker/
│   │   ├── store/
│   │   ├── audit/
│   │   └── static/
│   └── migrations/
├── web/
│   ├── src/
│   │   ├── api/
│   │   ├── components/
│   │   ├── modules/
│   │   ├── router/
│   │   ├── stores/
│   │   └── views/
│   └── dist/
├── resources/
│   ├── docker/
│   ├── mysql/
│   ├── redis/
│   └── minio/
├── config/
│   └── defaults.env
├── scripts/
│   ├── package.ps1
│   └── package.sh
└── deploy/
    ├── start.sh
    ├── stop.sh
    ├── start.ps1
    └── start.bat
```

## 后端模块边界

- `api`：HTTP 路由、请求校验、响应格式、WebSocket/SSE。
- `auth`：登录、JWT、密码哈希、bootstrap 用户、会话状态。
- `config`：读取 `AIFAR_ADDR`、`AIFAR_STATIC_DIR`、`AIFAR_RESOURCE_DIR`、bootstrap 环境变量。
- `store`：SQLite 表结构、事务、查询、迁移。
- `domain/servers`：目标服务器、SSH 凭据、连通性检查、服务器状态。
- `domain/apps`：离线应用目录、安装参数、实例状态、卸载/升级。
- `domain/containers`：Docker 安装、容器、镜像、网络、卷、日志、终端。
- `domain/database`：MySQL/Redis 安装、账号、数据库、备份、恢复。
- `domain/storage`：MinIO 安装、bucket、access key、对象存储连接检查。
- `domain/terminal`：SSH 终端、容器终端、WebSocket 会话。
- `domain/audit`：操作日志、任务日志、登录日志、审计查询。
- `domain/settings`：面板设置、默认凭据、资源路径、系统参数。
- `worker`：长任务、日志流、取消、重试、超时、任务状态机。
- `adapter`：SSH、Docker、文件系统、RPM、systemd、tar/gzip、MySQL、Redis、MinIO、系统命令。

## 前端模块边界

- `views/LoginView`：登录页，写入 `aifar-session-token`。
- `views/DashboardView`：总览，展示服务器、应用、容器、存储健康状态。
- `views/AppsView`：应用商店，支持 Docker/MySQL/Redis/MinIO 离线安装。
- `views/ContainersView`：容器、镜像、网络、卷、日志、启动/停止/重启。
- `views/ServersView`：服务器列表、SSH 凭据、连通性、部署目录。
- `views/DatabaseView`：MySQL/Redis 实例、数据库、账号、导入导出、备份。
- `views/StorageView`：MinIO 实例、bucket、access key、对象浏览。
- `views/TerminalView`：统一终端入口。
- `views/ToolboxView`：诊断、脚本、资源校验、离线包校验。
- `views/AuditView`：操作日志、任务日志、登录日志。
- `views/SettingsView`：面板设置、资源路径、默认配置。

## 应用商店拆分约定

- 应用商店必须按“前端应用模块 + 后端安装模块”拆分，不允许只在页面里写死卡片后直接开放部署。
- 前端应用模块放在 `web/src/apps/<app>/index.ts`，由 `web/src/apps/registry/loader.ts` 通过 `import.meta.glob` 自动发现；例如 MySQL 前端模块使用 `web/src/apps/mysql/index.ts`。
- 前端应用商店的类型、catalog 合并、模块校验统一放在 `web/src/apps/registry/`，不得再新增 `web/src/app-store` 层。
- 后端应用模块放在 `backend/internal/apps/<app>/module.go`，通过 `init()` 调用 `backend/internal/apps/registry.RegisterFactory` 自注册，并由 `backend/internal/apps/autoload` blank import 编入二进制；例如 MySQL 后端模块使用 `backend/internal/apps/mysql/module.go`。
- 前端页面只展示前端自动发现模块和后端 `/api/v2/apps/catalog` 同时存在的应用。缺少前端定义或缺少后端定义时，该应用不得出现在应用商店，也不得允许部署。
- 部署按钮还必须检查离线资源和目标服务器状态。代码模块齐备但缺少 `resources/<app>/<version>/` 或 `resources/<app>-backend/<version>/` 时，应用可以显示缺失状态，但不能执行部署。
- 离线资源扫描兼容旧结构 `resources/<app>/<version>/`，也兼容拆分结构 `resources/<app>-frontend/<version>/`、`resources/<app>-backend/<version>/`，入库时使用统一 `app` 和 `part=frontend|backend`。

## 服务器工作台企业级约定

- 服务器菜单必须按“后端服务器服务层 + 前端服务器模块 + 任务系统”组织，不允许把保存、删除、探测、表单、列表过滤等逻辑堆在 `httpapi` 或 `ServersView.vue`。
- 后端服务器业务统一放在 `backend/internal/servers/`。`Service` 负责服务器默认值、校验、保存、删除、探测计划和探测执行；`httpapi` 只做请求解析、鉴权、任务创建、审计记录和响应。
- SSH 探测必须通过 `servers.Prober` 接口注入，默认实现为 `servers.SSHProber`，底层再调用 `adapter.ProbeSSH`。测试必须使用 fake prober，不直接连真实服务器。
- 服务器探测必须走 worker 任务，并记录 `task_targets` 和 `task_steps`：`load-server`、`check-credential`、`probe-ssh`、`collect-runtime`。不得只写纯文本日志。
- 服务器保存必须由 service 层做 normalize 和 validate，默认端口 `22`、认证方式 `password`、部署目录读取 `AIFAR_DEFAULT_DEPLOY_DIR`，默认 `/aifar/apps`、状态 `unknown`。
- 前端服务器模块统一放在 `web/src/servers/`，包括 `types.ts`、`api.ts`、`useServerWorkbench.ts` 和 `components/`。`web/src/views/ServersView.vue` 只做页面编排。
- 服务器页面必须展示企业运维视角：资源概览、服务器清单、访问配置、运行能力和高风险操作确认；探测结果直接展示在概览状态，不再展示任务日志面板，也不展示 Docker 专属入口。
- 服务器页面所有用户可见文本必须走 `web/src/i18n`，后端服务器任务日志和错误必须走 `backend/internal/i18n`。

## 安装器解耦约定

- 应用安装逻辑不得直接写在 `backend/internal/httpapi` handler 中。HTTP 层只做参数解析、任务创建、审计记录和调用安装器。
- 所有应用安装入口必须是“点击安装按钮 -> 打开安装配置弹窗 -> 选择版本和目标服务器 -> 提交任务”，不得在应用卡片上直接发起安装请求。
- Docker Engine + Compose 的安装弹窗必须支持多选服务器，前端提交 `serverIds`，后端在同一个任务中按服务器逐台安装并记录每台结果；其他应用默认单选 `serverId`。
- Docker 模块的前后端目录必须保持一致：后端使用 `backend/internal/apps/docker/{module.go,catalog.go,i18n.go,service.go}`，前端使用 `web/src/apps/docker/{index.ts,catalog.ts,i18n.ts}` 并复用 `web/src/components/AppInstallDialog.vue`。应用商店只通过这些模块入口接入 Docker，不在 `AppsView` 或 `httpapi` 中堆业务细节。
- Docker 国际化当前提供 `zh` 和 `en` 两套文案。前端通过 `web/src/apps/docker/i18n.ts` 管理弹窗和卡片文案；后端通过 `backend/internal/apps/docker/i18n.go` 管理 catalog 和任务日志文案；接口优先读取请求体 `language`，其次读取 `?lang=`、`X-AIFAR-Language` 和 `Accept-Language`。
- Docker Engine + Compose 安装逻辑放在 `backend/internal/installer/docker`，包括资源选择、上传清单、安装脚本生成、服务校验和测试 fake。
- SSH 命令执行与文件上传放在 `backend/internal/adapter`，通过接口注入给安装器。安装器不得直接依赖具体 SSH 会话实现，便于后续替换为 SFTP、WinRM、容器内 fake 或测试桩。
- Docker 离线资源以当前包为准：`resources/docker/<version>/aifar-docker-static-<version>-linux-x86_64.tar`，其中必须包含 `docker-<version>.tgz` 和 `docker-compose-linux-x86_64`；`rpms/` 下的 openEuler RPM 依赖在安装前同步上传并尝试本地安装。
- Docker 安装完成后必须校验 `docker version` 与 `docker compose version`，并把安装动作记录为任务日志和应用实例。

## 前端设计系统约定

- 所有 `web/src` 下的页面、布局、组件和样式修改，必须先引用并遵守 `design/ant-design-system-portable202606.md`。
- 该设计系统作为全局基础规范，优先约束 Design Token、间距、圆角、字号、控件高度、表格、表单、卡片、Tabs、Menu、Tag、Alert、Drawer 等组件参数。
- AIFAR 面板的业务视觉在该设计系统之上实现：深色左侧导航、浅色工作区、白色内容卡片、蓝色主操作、绿色成功状态、橙色告警状态。
- 页面布局必须遵循 4px/8px 网格；常用间距优先使用 8px、12px、16px、24px；控件高度优先使用 24px、32px、40px。
- 页面级内容默认使用 `colorBgLayout` 类浅灰背景，卡片和表格使用白色容器，边框使用 Ant Design `colorBorder`/`colorBorderSecondary` 语义。
- 新增页面不得绕过该文件单独发明一套视觉系统；如果截图风格与设计系统冲突，以 `design/ant-design-system-portable202606.md` 的 token/组件参数为底座，再做局部业务适配。

## 国际化约定

- 所有用户可见文案必须提供中文和英文两套，不允许在 Vue 模板、Go handler、安装器、任务日志中直接硬编码单语言文案。
- 前端全局文案统一放在 `web/src/i18n/messages.ts`，页面和通用组件通过 `web/src/i18n` 的 `useI18n()` 读取；Element Plus 语言由 `App.vue` 的 `el-config-provider` 统一切换。
- 前端应用模块可在 `web/src/apps/<app>/i18n.ts` 保留模块私有文案，但必须通过 `resolveAppLocale()` 跟随全局语言，并且安装弹窗必须继续复用 `web/src/components/AppInstallDialog.vue`。
- 前端 API 请求必须携带 `X-AIFAR-Language`，后端优先按请求体 `language`、`?lang=`、`X-AIFAR-Language`、`Accept-Language` 的顺序解析语言。
- 后端全局文案统一放在 `backend/internal/i18n`；HTTP 错误、任务公共日志、安装器公共日志和共享离线安装生命周期都必须通过 key-based 文案输出。
- 新增后端应用模块必须在 `Manifest(lang)`、`PreflightInstall`、`PlanInstall`、`ValidateInstall`、`Install` 中传递并使用 `registry.InstallRequest.Language`，不得只返回英文错误或日志。
- 状态值、审计动作、能力点和数据库枚举仍保存为稳定英文机器码，例如 `running`、`apps.docker.install`；展示层负责翻译为用户语言。

## API 约定

- API 前缀保持 `/api/v2`，兼容现有前端产物命名。
- 登录接口使用 `POST /api/v2/auth/login`。
- 终端 WebSocket 保持 `/api/v2/servers/{serverId}/terminal/ws`。
- 所有变更操作返回任务 ID，而不是同步阻塞到完成。
- 错误响应统一：

```json
{
  "code": "AIFAR_ERROR_CODE",
  "message": "human readable message",
  "details": {}
}
```

- 长任务状态统一：`pending`、`running`、`success`、`failed`、`cancelled`、`timeout`。
- 审计事件命名：`module.resource.action`，例如 `apps.mysql.install`、`containers.container.restart`。

## 数据存储约定

- 默认数据库路径：`data/aifar.db`。
- 必须使用迁移管理表结构，不允许手写散落建表逻辑。
- 敏感字段只保存加密值或哈希值：SSH 密码、私钥、数据库密码、MinIO secret key、bootstrap 密码。
- 操作日志不得写入明文凭据、token、私钥、完整连接串。
- 资源包元数据需要入库：app、version、resource path、checksum、size、createdAt。

## 离线资源约定

- 资源目录由 `AIFAR_RESOURCE_DIR` 指定，默认 `resources`。
- 应用包路径：`resources/<app>/<version>/`。
- RPM 缓存路径：`resources/<app>/<version>/rpms/`。
- 每个资源包必须有校验信息，优先使用 SHA256。
- 安装时先复制资源和 RPM 缓存到目标服务器部署目录，再执行安装。
- 不允许安装流程直接依赖公网下载；公网下载只能作为显式“补全缓存”功能。
- 现有资源版本应优先兼容：
  - Docker `24.0.9`
  - MySQL `8.0.36`
  - Redis `7.2.14`
  - MinIO `2025-10-15T17-29-55Z`
  - MinIO Client `2025-08-13T08-35-41Z`

## 安全约定

- 首次启动使用 `AIFAR_BOOTSTRAP_USERNAME` 和 `AIFAR_BOOTSTRAP_PASSWORD` 创建管理员。
- 若 `AIFAR_BOOTSTRAP_PASSWORD` 未设置，启动脚本可从 `AIFAR_DEFAULT_PASSWORD` 映射，但发布包必须提示用户修改默认密码。
- JWT 存储在前端 `localStorage` 时，后端必须控制 token 时效和撤销策略。
- SSH 凭据必须加密存储，使用时只在内存中解密。
- 所有远程命令必须走 adapter 白名单或结构化参数，不允许 API 直接传入自由 shell。
- 高风险操作必须二次确认：删除服务器、卸载应用、删除数据目录、恢复覆盖、开放端口、重置凭据。
- WebSocket 终端必须鉴权；前端当前使用 token 子协议的做法可以保留，但后端必须校验 token。

## 任务系统约定

- 安装、卸载、升级、备份、恢复、资源校验、终端连接初始化都视为任务。
- 任务必须记录：`id`、`type`、`target`、`status`、`createdBy`、`startedAt`、`finishedAt`、`logs`、`error`。
- 任务日志支持流式订阅，前端通过 SSE 或 WebSocket 展示。
- 任务取消必须尽力中止远程进程，并记录最终状态。
- 任务失败必须给出可操作错误：缺少 RPM、SSH 失败、权限不足、端口冲突、校验失败、服务启动失败。

## 重建优先级

1. 启动与配置：读取环境变量，托管 `web/dist`，初始化 SQLite。
2. 鉴权：bootstrap 管理员、登录、JWT、密码哈希。
3. 服务器管理：SSH 凭据、连接测试、服务器列表。
4. 任务系统：异步执行、日志流、审计日志。
5. 离线资源扫描：识别 `resources` 下 Docker/MySQL/Redis/MinIO 版本。
6. 应用安装：先实现 Docker，再 MySQL、Redis、MinIO。
7. 容器管理：Docker API 管理容器、镜像、日志、终端。
8. 数据库与存储：MySQL/Redis/MinIO 管理能力。
9. 前端补全：按现有页面模块重建 Vue 源码。
10. 打包发布：生成 Linux/Windows 二进制、`web/dist`、启动脚本和资源包。

## 验收标准

- `sh start.sh foreground` 能在 Linux 启动服务，并监听 `0.0.0.0:8080`。
- 访问根路径能返回前端控制台。
- `POST /api/v2/auth/login` 能使用 bootstrap 用户登录。
- SQLite 数据库自动创建在 `data/aifar.db`。
- 添加一台目标服务器后，可以完成 SSH 连通性检查。
- Docker 离线安装任务能从 `resources/docker/24.0.9/` 读取资源并产生日志。
- 前端能展示任务进度、失败原因和审计记录。
- 打包产物结构与原 zip 兼容：`bin/`、`web/dist/`、`resources/`、`config/defaults.env`、启动脚本。
## Current Enterprise Module Rules

- Frontend pages and components under `web/src` must use `design/ant-design-system-portable202606.md` as the design-system source before changing layout, color, spacing, radius, table, form, tabs, menu, tag, alert, drawer, or card styles.
- `backend/internal/apps/registry` is protocol-only. It must not import Docker, MySQL, Redis, MinIO, or any other concrete app module.
- Backend app modules self-register through `registry.RegisterFactory(...)`. Static builds include modules through `backend/internal/apps/autoload` blank imports.
- Frontend app modules self-register by exporting `web/src/apps/<app>/index.ts`; `web/src/apps/registry/loader.ts` discovers them with `import.meta.glob('../*/index.ts')`.
- App Store visibility requires all three sides to exist: frontend module, backend module with `backendReady=true`, and required offline resources. Missing any side must block deployment.
- Backend modules must implement the full install lifecycle: `Manifest`, `PreflightInstall`, `PlanInstall`, `ValidateInstall`, and `Install`.
- `PreflightInstall` reports early warnings or blocking readiness errors without changing remote state.
- `PlanInstall` returns deterministic step plans for each target. HTTP handlers store those plans as pending task steps before execution starts.
- `ValidateInstall` performs fast checks for target servers, version, resource presence, and checksum validity.
- `Install` performs the real work only through worker tasks and adapters; it must record target and step status instead of relying only on plain-text logs.
- Multi-target Docker install records one `task_targets` row per server and four `task_steps` rows per server: `load-server`, `install-engine`, `update-server`, and `record-instance`.
- MySQL, Redis, and MinIO use `backend/internal/apps/offlineapp` for the shared enterprise lifecycle until their concrete remote installers are implemented. Do not duplicate the same plan/preflight/step boilerplate inside each module.
- Shared offline modules must still keep concrete app declarations in their own package, for example `backend/internal/apps/mysql`, `backend/internal/apps/redis`, and `backend/internal/apps/minio`.
- `resources/<app>/<version>/manifest.json` may provide file checksums. The scanner should apply manifest SHA256 values, and installers must verify checksums when present.
- `web/src/components/AppInstallDialog.vue` is the shared installation dialog. Modules customize it through `installDialogProps(locale)` and schema fields; app-specific dialogs are allowed only for interactions that cannot fit the shared schema.
- The server workbench must not expose Docker-specific UI. Docker installation belongs to App Store modules, and Docker runtime management belongs to the Containers module. Server probe results are shown through server overview/status fields, not a task-log panel.

## Enterprise Apps Registry Contract

- 应用商店必须以“前端模块 registry + 后端模块 registry + 离线资源”为唯一准入来源，不允许在 `httpapi` 或 `AppsView` 中针对具体应用堆业务分支。
- `registry` 包自身不得 import 任何具体业务应用模块，例如 Docker/MySQL/Redis/MinIO；registry 只定义协议、工厂注册、模块查询和排序。
- 后端企业级应用模块统一实现 `backend/internal/apps/registry.Module`，目录示例：`backend/internal/apps/docker/{module.go,catalog.go,i18n.go,service.go}`。
- 后端模块必须提供 `Manifest`、`ValidateInstall`、`Install` 三段能力：`Manifest` 描述名称、分类、资源 app、是否需要服务器、是否支持多目标、权限能力点；`ValidateInstall` 做参数、目标服务器、资源包和版本的快速校验；`Install` 只执行模块业务，必须经由 worker task、adapter 和资源扫描结果。
- 后端模块通过 `init()` 调用 `registry.RegisterFactory(...)` 自注册；Go 静态编译需要 `backend/internal/apps/autoload` 使用 blank import 把模块编进二进制，后续新增模块只改 autoload 或用生成脚本生成 autoload。
- 前端企业级应用模块统一实现 `web/src/apps/registry/AppFrontendModule`，目录示例：`web/src/apps/docker/{index.ts,catalog.ts,i18n.ts}`。
- 通用安装弹窗放在 `web/src/components/AppInstallDialog.vue`，模块通过 `installDialog: AppInstallDialog` 和 `installDialogProps(locale)` 传入文案、单选/多选目标服务器模式等配置；除非有明显额外交互，具体应用不得重复实现自己的安装弹窗。
- 前端 registry 使用 Vite `import.meta.glob('../*/index.ts')` 自动发现 `web/src/apps/*/index.ts`，不得在 loader 中手写 import 某个具体应用。
- 前端应用商店只展示前端自动发现且后端 `/api/v2/apps/catalog` 返回 `backendReady=true` 的应用；缺少任一侧模块时不得展示为可部署应用。
- Docker 当前是第一批企业级模块：后端由 `backend/internal/apps/docker/init()` 自注册并由 autoload 编入，前端由 `web/src/apps/docker/index.ts` 默认导出模块并被 glob 自动发现。
- Docker 安装必须保持多服务器弹窗提交 `serverIds`，后端在同一任务内按目标逐台执行，并输出步骤化日志：读取目标服务器、安装 Docker Engine/Compose、更新服务器状态、记录应用实例。
- 后续 MySQL、Redis、MinIO 不得沿用静态卡片 + 通用伪安装逻辑；必须先补齐对应前后端 registry 模块，再进入应用商店。
