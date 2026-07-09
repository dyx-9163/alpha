# Custom Deployment Orchestration Design

Status: Draft 0.1
Date: 2026-07-09
Audience: AIFAR Deployment backend, frontend, installer, and runtime-agent maintainers

本文定义一套“脚本执行面 + Go 控制面 + Vue 配置展示面”的自定义部署编排方案。目标是在保留当前 Apps、Tasks、Audit、Resources、Servers、Credentials 能力的前提下，让新应用尽量通过离线资源包和受信脚本接入，同时支持多服务器集群模式。

## 1. 结论

推荐边界：

- Vue 只负责应用展示、参数表单、目标选择、任务提交和日志查看。
- Go 负责可信资源发现、参数/拓扑校验、资源 SHA256 校验、任务/步骤/目标/审计、上传、远程执行、实例记录和凭据绑定。
- shell 负责目标机上的幂等安装、配置渲染、systemd/docker 操作、readiness 等待和失败诊断。
- aifar-agent 负责节点本地 runtime 能力，不应在第一阶段承担全局集群调度。多服务器集群由 Go worker 编排，agent 做节点本地 apply/status/log/proxy。

禁止边界：

- 不允许 API 或前端直接提交任意 shell 内容执行。
- 不允许脚本绕过任务系统直接做不可追踪的远程变更。
- 不允许把敏感参数写入普通日志、审计详情或明文 metadata。

## 2. 目标

1. 支持通过资源包定义新部署模块，降低新增应用的 Go/Vue 编码量。
2. 保持当前 `registry.Module` 生命周期：`PreflightInstall -> PlanInstall -> ValidateInstall -> Install`。
3. 安装过程统一可观测：task、target、step、stdout/stderr、最终实例状态。
4. 支持单机、主从、三节点、N 节点等多服务器拓扑。
5. 支持集群级阶段，例如 bootstrap、join、configure、rebalance、post-check。
6. 让 aifar-agent 承担稳定的节点执行面能力，而不是变成第二套平台控制面。

## 3. 非目标

1. 第一阶段不做完整 Kubernetes 替代。
2. 第一阶段不做任意 shell 平台。
3. 第一阶段不要求所有历史内置模块立刻迁移。
4. 第一阶段不要求 agent 替代 SSH。SSH 仍是 bootstrap 和回退通道。
5. 不在 `httpapi` 中写任何具体应用安装逻辑。

## 4. 当前代码映射

现有能力可以继续复用：

- API 入口：`backend/internal/httpapi/apps_handlers.go`
- 模块协议：`backend/internal/apps/registry/contract.go`
- 后端应用模块：`backend/internal/apps/<app>`
- 上传和远程执行：`backend/internal/installer/uploadkit`、`backend/internal/installer/installerkit`
- 任务和日志：`backend/internal/worker`
- 前端应用发现：`web/src/apps/registry/loader.ts`
- 前端通用安装弹窗：`web/src/components/AppInstallDialog.vue`
- aifar-agent：`backend/cmd/aifar-agent`、`backend/internal/runtimeagent`

需要新增的核心能力：

- `backend/internal/deployspec`：解析和校验资源包中的部署描述。
- `backend/internal/apps/scripted`：通用脚本化部署模块引擎。
- `web/src/apps/scripted`：通用脚本化前端模块和 schema 转换器。
- app catalog 对动态 scripted 应用的配对能力。

## 5. 总体架构

```text
Vue Apps page
  |
  | /api/v2/apps/catalog
  | /api/v2/apps/{app}/install
  v
Go httpapi
  |
  | static registry modules
  | + scripted modules loaded from resources
  v
registry.Module
  |
  | PlanInstall / ValidateInstall / Install
  v
worker task
  |
  | target steps, target logs, audit
  v
scripted Service
  |
  | select bundle, verify checksum, render context
  | upload bundle, run trusted scripts
  v
SSH bootstrap or aifar-agent local API
  |
  v
target servers
```

多服务器集群时，Go worker 是全局编排者：

```text
1. load all servers
2. build cluster context
3. verify resources once
4. prepare/upload on every node
5. run per-node install in parallel
6. run cluster bootstrap once on coordinator
7. run per-node health check
8. record one app_instance per node with the same clusterId
```

## 6. 资源包规范

建议目录：

```text
resources/<app>/<version>/
  aifar-deploy.json
  scripts/
    install.sh
    check.sh
    uninstall.sh
    bootstrap.sh
  files/
    app.tar.gz
    config-template/
  rpms/
    *.rpm
  manifest.json
  *.sha256sum
```

`manifest.json` 继续服务资源扫描和文件 hash。`aifar-deploy.json` 服务应用编排。

示例：

```json
{
  "apiVersion": "aifar.io/v1",
  "kind": "ScriptedDeployment",
  "name": "exampledb",
  "title": { "zh": "ExampleDB", "en": "ExampleDB" },
  "icon": "EX",
  "category": "database",
  "sourceLabel": { "zh": "离线脚本包", "en": "Offline scripted bundle" },
  "description": {
    "zh": "通过受信脚本安装 ExampleDB，支持单机和三节点集群。",
    "en": "Install ExampleDB from trusted scripts, standalone or 3-node cluster."
  },
  "resourceApp": "exampledb",
  "requiredResourceParts": ["backend"],
  "topologies": [
    { "name": "standalone", "label": { "zh": "单机", "en": "Standalone" }, "targetMode": "single", "minTargets": 1, "default": true },
    { "name": "cluster", "label": { "zh": "三节点集群", "en": "3-node cluster" }, "targetMode": "multiple", "minTargets": 3 }
  ],
  "fields": [
    { "name": "port", "label": { "zh": "服务端口", "en": "Port" }, "type": "number", "defaultValue": 3306, "required": true, "min": 1, "max": 65535 },
    { "name": "adminUser", "label": { "zh": "管理员账号", "en": "Admin user" }, "type": "text", "defaultValue": "admin", "required": true },
    { "name": "adminPassword", "label": { "zh": "管理员密码", "en": "Admin password" }, "type": "password", "required": true, "secret": true }
  ],
  "artifacts": [
    { "path": "files/app.tar.gz", "required": true },
    { "path": "rpms", "required": false, "directory": true }
  ],
  "scripts": {
    "install": { "path": "scripts/install.sh", "scope": "node" },
    "bootstrap": { "path": "scripts/bootstrap.sh", "scope": "cluster", "runOn": "coordinator", "topologies": ["cluster"] },
    "check": { "path": "scripts/check.sh", "scope": "node" },
    "delete": { "path": "scripts/uninstall.sh", "scope": "node" }
  },
  "instance": {
    "endpointTemplate": "http://{{ .Node.Host }}:{{ .Parameters.port }}",
    "status": "installed"
  }
}
```

规范要求：

- `scripts.*.path` 必须是资源包内相对路径，禁止绝对路径和 `..`。
- 脚本必须由资源扫描发现并校验 hash。
- `secret: true` 的字段只能进入受保护的 context 文件，不能进入普通日志或审计 details。
- 脚本应幂等。重复执行 install/check/delete 不应造成不可恢复状态。
- 所有脚本以非交互方式运行，不能要求用户手工输入。

## 7. 远端工作目录和上下文

每次任务为每台服务器创建独立工作目录：

```text
/aifar/apps/_work/<app>-<version>-<taskId>-<nodeIndex>/
  scripts/
  files/
  rpms/
  context/
    node.json
    cluster.json
    parameters.env
    secrets.env
```

推荐上下文：

`node.json`

```json
{
  "taskId": "task_xxx",
  "app": "exampledb",
  "version": "1.0.0",
  "topology": "cluster",
  "node": {
    "id": "srv-1",
    "name": "db-1",
    "host": "10.0.0.11",
    "role": "coordinator",
    "index": 1,
    "deployDir": "/aifar/apps",
    "workDir": "/aifar/apps/_work/exampledb-1.0.0-task_xxx-1",
    "installRoot": "/aifar/apps/exampledb"
  }
}
```

`cluster.json`

```json
{
  "clusterId": "exampledb_cluster_xxx",
  "coordinatorId": "srv-1",
  "nodes": [
    { "id": "srv-1", "host": "10.0.0.11", "role": "coordinator", "index": 1 },
    { "id": "srv-2", "host": "10.0.0.12", "role": "worker", "index": 2 },
    { "id": "srv-3", "host": "10.0.0.13", "role": "worker", "index": 3 }
  ]
}
```

`parameters.env` 只放非敏感参数。`secrets.env` 使用 `0600` 权限上传，日志中只显示字段名，不显示值。

脚本入口示例：

```sh
#!/usr/bin/env sh
set -eu

WORK_DIR="${AIFAR_WORK_DIR:?}"
INSTALL_ROOT="${AIFAR_INSTALL_ROOT:?}"

. "$WORK_DIR/context/parameters.env"
if [ -f "$WORK_DIR/context/secrets.env" ]; then
  . "$WORK_DIR/context/secrets.env"
fi

echo "install exampledb to $INSTALL_ROOT"
mkdir -p "$INSTALL_ROOT"
# unpack, install, start, wait readiness...
```

Go 执行脚本时只调用固定入口：

```text
AIFAR_WORK_DIR='<workDir>' AIFAR_INSTALL_ROOT='<installRoot>' sh '<workDir>/scripts/install.sh'
```

## 8. Go 代码设计

### 8.1 deployspec 包

新增：

```text
backend/internal/deployspec/
  spec.go
  validate.go
  loader.go
  render.go
  schema.go
```

核心类型：

```go
package deployspec

type LocalizedString map[string]string

type Spec struct {
    APIVersion            string          `json:"apiVersion"`
    Kind                  string          `json:"kind"`
    Name                  string          `json:"name"`
    Title                 LocalizedString `json:"title"`
    Icon                  string          `json:"icon"`
    Category              string          `json:"category"`
    SourceLabel           LocalizedString `json:"sourceLabel"`
    Description           LocalizedString `json:"description"`
    ResourceApp           string          `json:"resourceApp"`
    RequiredResourceParts []string        `json:"requiredResourceParts"`
    Topologies            []TopologySpec  `json:"topologies"`
    Fields                []FieldSpec     `json:"fields"`
    Artifacts             []ArtifactSpec  `json:"artifacts"`
    Scripts               ScriptSet       `json:"scripts"`
    Instance              InstanceSpec    `json:"instance"`
}

type TopologySpec struct {
    Name       string          `json:"name"`
    Label      LocalizedString `json:"label"`
    TargetMode string          `json:"targetMode"`
    MinTargets int             `json:"minTargets"`
    MaxTargets int             `json:"maxTargets,omitempty"`
    Default    bool            `json:"default,omitempty"`
}

type FieldSpec struct {
    Name         string          `json:"name"`
    Label        LocalizedString `json:"label"`
    Type         string          `json:"type"`
    DefaultValue any             `json:"defaultValue,omitempty"`
    Required     bool            `json:"required,omitempty"`
    Secret       bool            `json:"secret,omitempty"`
    Min          *float64        `json:"min,omitempty"`
    Max          *float64        `json:"max,omitempty"`
    Options      []FieldOption   `json:"options,omitempty"`
}

type ScriptSpec struct {
    Path       string   `json:"path"`
    Scope      string   `json:"scope"` // node or cluster
    RunOn      string   `json:"runOn,omitempty"` // coordinator for cluster scripts
    Topologies []string `json:"topologies,omitempty"`
    TimeoutSec int      `json:"timeoutSec,omitempty"`
}

type ScriptSet struct {
    Install   ScriptSpec `json:"install"`
    Bootstrap ScriptSpec `json:"bootstrap,omitempty"`
    Check     ScriptSpec `json:"check,omitempty"`
    Delete    ScriptSpec `json:"delete,omitempty"`
}
```

校验规则：

- `name` 只能使用稳定机器码：`[a-z0-9][a-z0-9-]{1,62}`。
- `category` 必须映射到前端已知分类，或扩展前端分类类型。
- `field.type` 只允许当前弹窗支持的类型：`text/password/number/select/switch/server-disk-select`。
- `script.path` 必须安全清理，不能跳出资源目录。
- `topology.targetMode` 只能是 `single` 或 `multiple`。
- secret 字段不能参与 `endpointTemplate` 渲染。

### 8.2 scripted 应用模块

新增：

```text
backend/internal/apps/scripted/
  module.go
  service.go
  installer.go
  bundle.go
  context.go
  steps.go
  i18n.go
```

模块实例由资源包生成：

```go
type Module struct {
    spec    deployspec.Spec
    service Service
}

func (m Module) Name() string {
    return m.spec.Name
}

func (m Module) Manifest(lang string) registry.Manifest {
    return registry.Manifest{
        Name:                  m.spec.Name,
        Title:                 m.spec.Title.Text(lang),
        Icon:                  m.spec.Icon,
        Category:              m.spec.Category,
        CategoryLabel:         categoryLabel(m.spec.Category, lang),
        SourceLabel:           m.spec.SourceLabel.Text(lang),
        Description:           m.spec.Description.Text(lang),
        InstallName:           m.spec.Name,
        ResourceApp:           resourceApp(m.spec),
        RequiresServer:        true,
        SupportsMultiTarget:   true,
        BackendReady:          true,
        RequiredResourceParts: requiredParts(m.spec),
        Topologies:            toRegistryTopologies(m.spec.Topologies, lang),
        Capabilities: []string{
            "apps." + m.spec.Name + ".install",
            "apps." + m.spec.Name + ".delete",
            "apps." + m.spec.Name + ".check",
        },
    }
}
```

`PlanInstall` 生成统一步骤：

```go
func scriptedInstallSteps(hasBootstrap bool) []stepDef {
    steps := []stepDef{
        {Name: "load-servers", Title: "加载目标服务器"},
        {Name: "verify-resource", Title: "校验离线资源"},
        {Name: "render-context", Title: "生成部署上下文"},
        {Name: "prepare-workdir", Title: "创建远端工作目录"},
        {Name: "upload-bundle", Title: "上传资源包和脚本"},
        {Name: "run-install", Title: "执行节点安装脚本"},
    }
    if hasBootstrap {
        steps = append(steps, stepDef{Name: "run-bootstrap", Title: "执行集群引导脚本"})
    }
    steps = append(steps,
        stepDef{Name: "run-check", Title: "执行安装检查"},
        stepDef{Name: "record-instance", Title: "记录部署实例"},
    )
    return steps
}
```

计划里既要有 node step，也要有 cluster step：

```go
plan = append(plan, registry.InstallStepPlan{
    Target: serverID,
    Name: "upload-bundle",
    Title: title,
    Order: order,
})

plan = append(plan, registry.InstallStepPlan{
    Target: "cluster:" + clusterID,
    Name: "run-bootstrap",
    Title: title,
    Order: order,
})
```

### 8.3 动态模块加载

当前 `registry.NewFromRegistered` 只加载静态 `init()` 注册模块。脚本化应用需要把资源包中的 `aifar-deploy.json` 转为动态 module。

推荐新增：

```go
func LoadScriptedModules(resources []store.Resource, deps registry.Dependencies, remote installerkit.Remote) ([]registry.Module, error)
```

API 侧不要只依赖启动时的 `a.apps`。建议新增 helper：

```go
func (a *API) appRegistryForRequest() (*registry.Registry, []store.Resource, error) {
    resources, err := a.store.ListResources()
    if err != nil {
        return nil, nil, err
    }
    modules := a.apps.Modules()
    scripted, err := scripted.LoadModules(resources, registry.Dependencies{
        Store: a.store,
        DefaultPassword: a.cfg.DefaultPassword,
    }, adapter.SSHRemote{})
    if err != nil {
        return nil, nil, err
    }
    r := registry.New(modules...)
    for _, module := range scripted {
        r.Register(module)
    }
    return r, resources, nil
}
```

`appsCatalog`、`installApp`、`checkAppInstance`、`deleteAppInstance` 都应通过这个 helper 查找模块。这样资源 rescan 后不需要重启后端。

### 8.4 Service 安装流程

伪代码：

```go
func (s Service) Install(ctx context.Context, req InstallRequest, resources []store.Resource, log Logger, targetLog targetLogger) error {
    spec := s.spec
    targets := req.TargetServerIDs()
    topology := selectTopology(spec, req.Topology)

    if err := validateTargets(topology, targets); err != nil {
        return err
    }
    if err := validateParameters(spec.Fields, req.Parameters); err != nil {
        return err
    }

    bundle, err := SelectBundle(spec, resources, req.Version)
    if err != nil {
        return err
    }
    if err := VerifyBundle(spec, bundle); err != nil {
        return err
    }

    servers := loadServers(targets)
    cluster := buildClusterContext(spec, topology, servers, req.Parameters)
    recorder, _ := log.(stepRecorder)

    failures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(serverID string) error {
        server := servers[serverID]
        node := cluster.Node(serverID)
        logForServer := logForTarget(log, targetLog, serverID)

        runStep(recorder, serverID, "prepare-workdir", func() error {
            return installer.PrepareWorkDir(ctx, server, node)
        })
        runStep(recorder, serverID, "upload-bundle", func() error {
            return installer.UploadBundle(ctx, server, bundle, node)
        })
        runStep(recorder, serverID, "run-install", func() error {
            return installer.RunNodeScript(ctx, server, spec.Scripts.Install, node, cluster)
        })
        return nil
    })
    if len(failures) > 0 {
        return batchError(failures)
    }

    if spec.Scripts.Bootstrap.Path != "" && topology.Name != "standalone" {
        coordinator := cluster.Coordinator()
        runClusterStep("run-bootstrap", func() error {
            return installer.RunClusterScript(ctx, coordinator.Server, spec.Scripts.Bootstrap, coordinator, cluster)
        })
    }

    checkFailures := taskrun.RunTargets(ctx, targets, req.Concurrency, func(serverID string) error {
        return installer.RunNodeScript(ctx, servers[serverID], spec.Scripts.Check, cluster.Node(serverID), cluster)
    })
    if len(checkFailures) > 0 {
        return batchError(checkFailures)
    }

    return s.recordInstances(ctx, spec, bundle, cluster)
}
```

### 8.5 Installer 实现

Installer 继续使用 `installerkit.Remote`，这样测试可以注入 fake remote。

```go
type Installer struct {
    remote installerkit.Remote
}

func (i Installer) UploadBundle(ctx context.Context, server store.Server, bundle Bundle, node NodeContext) error {
    for _, file := range bundle.Files {
        remotePath := path.Join(node.WorkDir, file.RelativePath)
        if err := uploadkit.Upload(ctx, i.remote, server, uploadkit.File{
            LocalPath: file.LocalPath,
            RemotePath: remotePath,
            Mode: file.Mode,
            LogMessage: "upload deployment file: %s",
            LogArgs: []any{file.RelativePath},
            FailureMessage: "upload deployment file failed",
        }, node.Log); err != nil {
            return err
        }
    }
    return nil
}

func (i Installer) RunScript(ctx context.Context, server store.Server, script ScriptRun) error {
    cmd := strings.Join([]string{
        "AIFAR_WORK_DIR=" + installerkit.ShellQuote(script.WorkDir),
        "AIFAR_INSTALL_ROOT=" + installerkit.ShellQuote(script.InstallRoot),
        "sh " + installerkit.ShellQuote(script.RemoteScriptPath),
    }, " ")
    _, err := installerkit.Run(ctx, i.remote, server, cmd, script.Log, "deployment script failed")
    return err
}
```

要求：

- 上传前先创建远端目录。
- 上传脚本时使用 `0755`。
- 上传 secret context 时使用 `0600`。
- 脚本 stdout/stderr 进入 task log，但 Go 需要对已知 secret 值做脱敏。
- 所有远程命令通过固定模板生成，不拼接用户传入的自由命令。

### 8.6 实例记录

当前可以不新增表，继续使用 `app_instances`。集群安装时每个节点一条 instance，共享 `clusterId`：

```json
{
  "clusterId": "exampledb_cluster_xxx",
  "nodeRole": "coordinator",
  "nodeIndex": 1,
  "topology": "cluster",
  "endpoint": "http://10.0.0.11:3306",
  "installRoot": "/aifar/apps/exampledb",
  "resourcePath": "resources/exampledb/1.0.0",
  "workDir": "/aifar/apps/_work/exampledb-1.0.0-task_xxx-1",
  "scripted": true
}
```

后续如需更强集群模型，再新增 `app_instance_groups` 或 `clusters` 表。第一阶段不建议引入新表。

## 9. Vue 代码设计

### 9.1 Catalog 类型扩展

当前前端只展示静态前端模块能配对的应用。脚本化动态应用需要后端 catalog 返回 schema，前端用通用 scripted 模块承接。

扩展 `BackendCatalogItem`：

```ts
export interface BackendInstallSchema {
  kind: 'scripted'
  fields: BackendInstallField[]
  targetMode?: AppTargetMode
  targetModeResolver?: 'topology'
}

export interface BackendInstallField {
  name: string
  label: string
  type: AppInstallFieldType
  placeholder?: string
  defaultValue?: unknown
  required?: boolean
  secret?: boolean
  multiple?: boolean
  min?: number
  max?: number
  step?: number
  options?: Array<{ label: string; value: string | number | boolean; disabled?: boolean }>
  visibleWhen?: { field: string; equals: unknown }
}

export interface BackendCatalogItem {
  // existing fields...
  frontendKind?: 'static' | 'scripted'
  installSchema?: BackendInstallSchema
}
```

扩展 `AppStoreItem`：

```ts
export type AppStoreItem = FrontendAppDefinition & {
  // existing fields...
  frontendKind?: 'static' | 'scripted'
  installSchema?: BackendInstallSchema
}
```

### 9.2 动态配对逻辑

修改 `pairedAppCatalog`：

```ts
export function pairedAppCatalog(payload: AppCatalogResponse, locale?: string): AppStoreItem[] {
  const backend = normalizeBackendCatalog(payload)
  const paired = pairStaticApps(backend, locale)
  const pairedNames = new Set(paired.map((app) => app.name))

  for (const item of Object.values(backend)) {
    if (pairedNames.has(item.name)) {
      continue
    }
    if (item.frontendKind !== 'scripted' || !item.installSchema || !item.backendReady) {
      continue
    }
    paired.push(scriptedBackendToStoreItem(item))
  }
  return paired
}
```

### 9.3 通用 scripted 前端模块

新增：

```text
web/src/apps/scripted/
  index.ts
  schema.ts
```

注意：`frontendModuleFor(app.name)` 当前只能按静态 name 找模块。需要改为：

```ts
export function frontendModuleForApp(app: AppStoreItem) {
  if (app.frontendKind === 'scripted') {
    return scriptedFrontendModule
  }
  return modules.find((module) => module.name === app.name) ?? null
}
```

`AppsView.vue` 中：

```ts
const module = frontendModuleForApp(app)
```

scripted module：

```ts
import AppInstallDialog from '../../components/AppInstallDialog.vue'
import type { AppFrontendModule, AppInstallDialogConfig } from '../registry/contract'

export const scriptedFrontendModule: AppFrontendModule = {
  name: 'scripted',
  manifest: () => ({
    name: 'scripted',
    title: 'Scripted',
    icon: 'SC',
    category: 'devops',
    categoryLabel: 'DevOps',
    sourceLabel: 'Scripted bundle',
    description: 'Generic scripted deployment module',
    frontendReady: true
  }),
  installDialog: AppInstallDialog,
  installDialogProps: (_locale, context): AppInstallDialogConfig => {
    const app = context?.currentApp
    return scriptedInstallDialogProps(app)
  }
}
```

`AppInstallDialogContext` 需要增加当前 app：

```ts
export interface AppInstallDialogContext {
  servers: ServerOption[]
  instances: AppInstanceOption[]
  credentials?: CredentialOption[]
  defaultDeployDir?: string
  currentApp?: AppStoreItem
}
```

`AppsView.vue` 构造 context：

```ts
const installDialogContext = computed<AppInstallDialogContext>(() => ({
  servers: servers.value,
  instances: instances.value,
  credentials: credentials.value,
  defaultDeployDir: appSettings.value.defaultDeployDir || '/aifar/apps',
  currentApp: moduleDialogApp.value ?? undefined
}))
```

schema 转字段：

```ts
export function scriptedInstallDialogProps(app?: AppStoreItem | null): AppInstallDialogConfig {
  const schema = app?.installSchema
  return {
    targetMode: schema?.targetMode ?? 'single',
    targetModeResolver: schema?.targetModeResolver === 'topology'
      ? targetModeResolver(app?.topologies ?? [])
      : undefined,
    fields: [
      topologySelectField('拓扑', app?.topologies ?? []),
      ...(schema?.fields ?? []).map(toInstallField)
    ]
  }
}
```

如果不想扩展 `AppInstallDialogContext`，另一种做法是在 `AppsView.vue` 直接为 scripted app 计算 dialog props。但长期看，context 携带 current app 更干净。

## 10. 多服务器集群模式

### 10.1 拓扑选择

部署 spec 用 topology 描述目标数量和选择模式：

```json
{
  "name": "cluster",
  "targetMode": "multiple",
  "minTargets": 3,
  "maxTargets": 9
}
```

如果需要角色选择，可以扩展：

```json
{
  "roles": [
    { "name": "coordinator", "count": 1 },
    { "name": "worker", "min": 2 }
  ]
}
```

第一阶段可以先自动分配：

- 第一个目标为 coordinator。
- 其余目标为 worker。
- `node.index` 从 1 开始稳定排序，按用户选择顺序。

### 10.2 编排顺序

集群安装应分为四类阶段：

1. control 阶段：Go 本地完成，如 validate、build cluster context。
2. node 阶段：每台服务器执行，如 prepare/upload/install/check。
3. cluster 阶段：只执行一次，如 bootstrap、rebalance、create admin。
4. record 阶段：Go 本地记录实例、凭据、审计。

推荐默认顺序：

```text
control: validate-request
control: verify-resource
node:    prepare-workdir
node:    upload-bundle
node:    run-install
cluster: run-bootstrap
node:    run-check
record:  record-instance
```

### 10.3 失败语义

- node install 任一失败，默认不执行 cluster bootstrap。
- cluster bootstrap 失败，所有已安装节点记录为 `install_failed` 或 task 失败实例。
- check 部分失败，task 失败，但保留失败诊断和可删除实例记录。
- delete/uninstall 对集群应支持按 `clusterId` 成组执行。
- 脚本必须允许重复运行，以便用户重试同一安装。

### 10.4 日志和步骤展示

task target 建议：

- 节点步骤 target 使用 server id。
- 集群步骤 target 使用 `cluster:<clusterId>`。

这样任务中心可以自然展示：

```text
srv-1
  prepare-workdir
  upload-bundle
  run-install
  run-check
srv-2
  prepare-workdir
  upload-bundle
  run-install
  run-check
cluster:exampledb_cluster_xxx
  run-bootstrap
```

## 11. 备份、还原、版本和回滚

备份、还原、更新、回滚必须和安装一样进入统一生命周期，不能变成页面上的同步按钮或脚本里的隐藏动作。

推荐原则：

- 所有操作都返回 task id，并写入 task、target、step、log、audit。
- Go 负责判断“能不能做、按什么顺序做、结果怎么记录”。
- shell 负责目标机上的具体备份、恢复、升级、回滚动作。
- agent 可承载节点本地 job 执行、锁、日志和状态，但不承载全局备份目录和版本决策。
- stateful 服务默认升级前强制备份；stateless 服务至少备份配置、运行 spec 和 release manifest。

### 11.1 部署描述扩展

`aifar-deploy.json` 建议扩展生命周期声明：

```json
{
  "versioning": {
    "appVersion": "1.2.0",
    "packageVersion": "1.2.0-aifar.1",
    "configSchemaVersion": "2026.07",
    "dataSchemaVersion": "5",
    "upgradeFrom": ">=1.0.0 <1.2.0",
    "downgradeTo": ">=1.1.0 <1.2.0",
    "releaseNotes": {
      "zh": "修复稳定性问题并升级数据结构。",
      "en": "Fix stability issues and upgrade data schema."
    }
  },
  "backup": {
    "supported": true,
    "modes": ["config", "data", "full"],
    "defaultMode": "full",
    "scope": "cluster",
    "runOn": "coordinator",
    "requiresQuiesce": true,
    "retention": { "keepLast": 5 }
  },
  "restore": {
    "supported": true,
    "requiresStoppedService": true,
    "allowCrossServer": false
  },
  "update": {
    "supported": true,
    "strategy": "rolling",
    "backupRequired": true,
    "order": "workers-first"
  },
  "rollback": {
    "supported": true,
    "dataRollbackSupported": false,
    "requiresBackup": true
  },
  "scripts": {
    "backup": { "path": "scripts/backup.sh", "scope": "cluster", "runOn": "coordinator" },
    "restore": { "path": "scripts/restore.sh", "scope": "cluster", "runOn": "coordinator" },
    "preUpdate": { "path": "scripts/pre-update.sh", "scope": "cluster", "runOn": "coordinator" },
    "update": { "path": "scripts/update.sh", "scope": "node" },
    "postUpdateCheck": { "path": "scripts/post-update-check.sh", "scope": "node" },
    "rollback": { "path": "scripts/rollback.sh", "scope": "node" }
  }
}
```

字段含义：

- `appVersion`：应用自身版本，展示和兼容性判断用。
- `packageVersion`：AIFAR 离线包版本，和 `resources/<app>/<version>` 对应。
- `configSchemaVersion`：配置文件结构版本。
- `dataSchemaVersion`：数据库或持久化数据结构版本。
- `upgradeFrom`：允许从哪些已安装版本升级。
- `downgradeTo`：允许包级回滚到哪些版本。
- `dataRollbackSupported`：是否支持数据结构降级。大多数数据库服务应默认为 false。

### 11.2 备份模型

备份分三类：

1. `config`：安装参数、运行 spec、systemd/docker 配置、应用配置文件。
2. `data`：数据库 dump、对象存储 metadata、业务数据目录、volume 快照。
3. `full`：`config + data + release manifest`。

备份产物建议落在目标服务器：

```text
/aifar/backups/<app>/<clusterId-or-instanceId>/<timestamp>/
  backup-manifest.json
  nodes/
    srv-1/
      config.tar.gz
      data.tar.gz
      checksums.txt
    srv-2/
      config.tar.gz
      data.tar.gz
      checksums.txt
```

`backup-manifest.json` 示例：

```json
{
  "backupId": "backup_20260709_113000",
  "app": "exampledb",
  "instanceId": "exampledb_cluster_xxx:srv-1",
  "clusterId": "exampledb_cluster_xxx",
  "topology": "cluster",
  "mode": "full",
  "sourceVersion": "1.1.0",
  "configSchemaVersion": "2026.06",
  "dataSchemaVersion": "4",
  "createdAt": "2026-07-09T11:30:00Z",
  "createdByTaskId": "task_xxx",
  "nodes": [
    { "serverId": "srv-1", "role": "coordinator", "path": "nodes/srv-1", "status": "success" }
  ],
  "files": [
    { "path": "nodes/srv-1/config.tar.gz", "sha256": "..." }
  ]
}
```

注意：

- manifest 不记录密码、token、私钥和完整连接串。
- secret 只允许在目标服务自己的安全备份机制内处理，例如数据库 dump 的加密文件。
- Go 第一阶段可以只把 `lastBackup` 摘要写入 `app_instances.metadata`。
- 后续需要备份列表、下载、保留策略和跨机恢复时，再新增 `app_backups` 表。

### 11.3 备份流程

单机备份步骤：

```text
validate-instance
prepare-backup-dir
run-backup
verify-backup
record-backup
```

集群备份步骤：

```text
validate-cluster
select-backup-coordinator
prepare-backup-dir on all nodes
run-node-config-backup on all nodes
run-cluster-data-backup on coordinator
verify-backup
record-backup
```

不同服务的脚本策略：

- MySQL：优先逻辑备份或物理备份；集群模式在 primary/coordinator 上做一致性备份，必要时锁表或使用一致性快照。
- Redis：备份 RDB/AOF 和配置；Cluster/Sentinel 额外备份拓扑信息。
- MinIO：备份配置、用户、policy、bucket 元数据；对象数据是否全量备份由脚本声明，避免默认复制巨大对象。
- Nacos：备份配置目录和外部 MySQL schema dump；cluster 节点只备份节点配置，数据以数据库为准。
- AIFAR Runtime：备份 runtime spec、env、release manifest、持久 volume 和 Nacos 注册所需配置。

### 11.4 还原流程

还原只能从 `backup-manifest.json` 开始，不能让用户随便指定一个路径执行 restore。

推荐步骤：

```text
validate-backup-manifest
validate-target-compatibility
prepare-restore-workdir
stop-service
run-restore
start-service
run-check
record-restore
```

兼容性校验：

- `backup.app` 必须等于目标 app。
- `topology` 必须匹配，除非 spec 明确允许 cross-topology restore。
- `dataSchemaVersion` 必须小于等于当前脚本支持的 restore schema。
- `allowCrossServer=false` 时，server id 或节点角色必须匹配。
- stateful restore 默认要求用户二次确认，并建议继续沿用“输入目标服务器已保存 SSH 密码确认”的删除级别保护。

### 11.5 更新版本定义

版本需要分层，不能只看一个字符串：

```text
resource version       resources/<app>/<version>
packageVersion         AIFAR 离线包版本
appVersion             应用自身版本
configSchemaVersion    配置结构版本
dataSchemaVersion      数据结构版本
runtimeVersion         依赖的 aifar-agent/runtime 能力版本
```

当前安装状态记录在：

- `app_instances.version`：当前 package/resource version。
- `app_instances.metadata.currentRelease`：当前 release 摘要。
- `app_instances.metadata.previousRelease`：上一个 release 摘要。
- `app_instances.metadata.lastBackup`：最近一次备份摘要。

第一阶段不新增 release 表。后续如果要展示完整发布历史，再新增 `app_releases`。

release metadata 示例：

```json
{
  "currentRelease": {
    "releaseId": "release_20260709_120000",
    "packageVersion": "1.2.0-aifar.1",
    "appVersion": "1.2.0",
    "configSchemaVersion": "2026.07",
    "dataSchemaVersion": "5",
    "taskId": "task_update_xxx",
    "installedAt": "2026-07-09T12:00:00Z"
  },
  "previousRelease": {
    "packageVersion": "1.1.0-aifar.2",
    "appVersion": "1.1.0",
    "dataSchemaVersion": "4"
  }
}
```

### 11.6 更新流程

更新本质是“对已安装 instance 选择一个新 resource version，然后执行 update lifecycle”。

推荐步骤：

```text
validate-update
resolve-target-version
verify-update-resource
pre-update-backup
prepare-update-workdir
upload-update-bundle
run-pre-update
run-update
run-post-update-check
promote-release
record-instance
```

多服务器集群更新策略：

- `rolling`：逐台更新，更新一台检查一台，失败则停止。
- `serial`：严格按角色顺序更新。
- `parallel`：所有节点并发，仅适合无状态或明确声明可并发的服务。
- `workers-first`：先 worker/follower，再 coordinator/primary。
- `coordinator-first`：先 coordinator，再其他节点，适合控制面服务。

stateful 默认策略：

- `backupRequired=true`。
- 如果 post-check 失败，先尝试包级 rollback。
- 如果数据 schema 已升级且 `dataRollbackSupported=false`，不能自动做数据降级，只能提示使用升级前备份进行 restore。

### 11.7 回滚模型

回滚分两种：

1. 包级回滚：切回上一个 release 包和配置，适合无状态服务或未变更 data schema 的服务。
2. 数据级还原：从升级前 backup 恢复数据，适合 stateful 服务，但通常会丢失升级后的写入。

推荐规则：

- 每次 update 前自动创建 `pre-update` 备份。
- rollback 默认只回滚到 `previousRelease`。
- 如果 `dataSchemaVersion` 发生变化且 `dataRollbackSupported=false`，rollback 只能停止在应用包层，不能自动还原数据。
- restore backup 是破坏性操作，应单独走 restore 流程，不要隐藏在 rollback 里悄悄执行。

回滚步骤：

```text
validate-rollback
prepare-rollback-workdir
upload-previous-release-if-needed
run-rollback
run-check
record-release
```

如果需要数据还原：

```text
validate-rollback
validate-pre-update-backup
stop-service
run-rollback
run-restore
start-service
run-check
record-release-and-restore
```

### 11.8 Go 接口扩展

registry 后续可以增加可选接口：

```go
type BackupModule interface {
    PlanBackup(ctx context.Context, req BackupRequest) ([]InstallStepPlan, error)
    Backup(ctx context.Context, req BackupRequest, run RunContext) error
}

type RestoreModule interface {
    PlanRestore(ctx context.Context, req RestoreRequest) ([]InstallStepPlan, error)
    Restore(ctx context.Context, req RestoreRequest, run RunContext) error
}

type UpdateModule interface {
    PlanUpdate(ctx context.Context, req UpdateRequest) ([]InstallStepPlan, error)
    ValidateUpdate(ctx context.Context, req UpdateRequest, resources []store.Resource) error
    Update(ctx context.Context, req UpdateRequest, run RunContext) error
}

type RollbackModule interface {
    PlanRollback(ctx context.Context, req RollbackRequest) ([]InstallStepPlan, error)
    Rollback(ctx context.Context, req RollbackRequest, run RunContext) error
}
```

API 路由建议：

```text
POST /api/v2/apps/instances/{id}/backup
POST /api/v2/apps/instances/{id}/restore
POST /api/v2/apps/instances/{id}/update
POST /api/v2/apps/instances/{id}/rollback
```

全部返回 task id。

### 11.9 Vue 展示

前端不需要理解每个服务如何备份，只展示能力和触发任务：

- 实例详情增加：当前版本、上次备份、上次更新、可升级版本。
- 操作按钮根据 backend capabilities 展示：备份、还原、升级、回滚。
- 备份弹窗：mode、说明、目标范围。
- 升级弹窗：目标版本、release notes、是否自动备份。
- 回滚弹窗：回滚到 previousRelease，展示是否涉及数据 schema。
- 还原弹窗：选择 backup manifest 或 backup id，明确破坏性提示。

第一阶段可以先不做备份列表页面，只从最近一次 `lastBackup` 或用户输入 manifest path 开始。但长期应做备份列表和保留策略。

## 12. aifar-agent 能力边界

### 12.1 当前已有能力

当前 agent 已有这些基础：

- `serve --addr 127.0.0.1:18081`
- `health`
- `status`
- `reconcile-runtime --spec <file>`
- `remove-instance`
- `register-nacos` / `deregister-nacos`
- Docker runtime reconcile
- endpoint cache
- Nacos proxy registration and heartbeat
- local state under `/var/lib/aifar-agent/instances`

这些能力应继续服务 AIFAR Runtime。

### 12.2 应该承载的能力

agent 应承载节点本地能力：

1. Node facts：采集 OS、arch、CPU、memory、disk、ports、systemd、docker/podman 状态。
2. Local health：统一返回 agent、runtime driver、Nacos proxy、managed instances 状态。
3. Local artifact staging：校验已上传文件 hash，整理工作目录，保证权限。
4. Local trusted script runner：执行后端下发的受信 job spec，脚本必须来自已校验资源包，覆盖 install、backup、restore、update、rollback。
5. Runtime reconcile：继续维护容器、服务代理、ingress、endpoint cache。
6. Service discovery：继续维护 Nacos 注册、注销和心跳。
7. Local locks：同一 installRoot、instanceId 或 serviceName 上避免并发冲突。
8. Logs/events：提供最近事件、脚本输出 tail、runtime 状态摘要。
9. Cleanup：按 instanceId 清理 agent state、proxy、runtime resources。

### 12.3 不应该承载的能力

agent 第一阶段不应承载：

- 应用商店 catalog。
- 用户权限和 RBAC。
- 全局任务系统。
- SQLite 控制面数据。
- 多服务器拓扑决策。
- 集群 coordinator 选举。
- 任意 shell API。

### 12.4 agent API 草案

继续监听本机回环地址，Go 通过 SSH 调用本地 CLI 或 curl 本地 API。若未来后端直接远程访问 agent，需要 mTLS 或 token。

```text
GET  /health
GET  /status
GET  /node/facts
POST /runtime/reconcile
DELETE /runtime/instances/{id}
POST /jobs
GET  /jobs/{id}
GET  /jobs/{id}/logs
POST /jobs/{id}/cancel
```

`POST /jobs` 只接受结构化 job：

```json
{
  "jobId": "task_xxx:srv-1:run-install",
  "app": "exampledb",
  "instanceId": "exampledb_cluster_xxx:srv-1",
  "workDir": "/aifar/apps/_work/exampledb-1.0.0-task_xxx-1",
  "script": "scripts/install.sh",
  "checksum": "sha256:...",
  "env": {
    "AIFAR_WORK_DIR": "/aifar/apps/_work/exampledb-1.0.0-task_xxx-1",
    "AIFAR_INSTALL_ROOT": "/aifar/apps/exampledb"
  },
  "timeoutSeconds": 900
}
```

agent 必须校验：

- `workDir` 在允许根目录内。
- `script` 是相对路径且不能跳出 workDir。
- script checksum 匹配。
- env key 在 allowlist 或 `AIFAR_` 前缀内。
- 并发锁未冲突。

### 12.5 SSH 与 agent 的关系

第一阶段：

- SSH 负责上传资源、安装或升级 agent、执行脚本。
- agent 负责 AIFAR Runtime 容器编排、Nacos proxy 和状态。

第二阶段：

- SSH 仍负责 bootstrap。
- 安装 agent 后，Go 可优先通过 agent job API 执行节点本地脚本。
- 如果 agent 不可用，按模块策略选择失败或回退 SSH。

多服务器集群中：

- Go worker 构造 cluster context。
- 每个 agent 只知道本机 node context 和完整 cluster.json。
- cluster bootstrap 由 Go 决定在 coordinator 节点执行。
- agent 不做跨节点调度，只执行本机任务和本机状态上报。

## 13. 安全设计

1. 资源信任：脚本来自资源包，资源包由扫描器登记并校验 SHA256。
2. 路径安全：所有资源路径必须在资源根目录内，所有远端路径必须在 deployDir 或 workDir 内。
3. 参数安全：字段按 schema 校验类型、范围、必填和枚举。
4. 密码安全：secret 字段只进入加密凭据或 `0600` secret context，不进入 metadata/log/audit。
5. 命令安全：Go 只执行固定模板命令，用户参数通过文件或 shell quote 传递。
6. 日志脱敏：worker 写日志前替换已知 secret 值为 `******`。
7. 审计：install/delete/check 都写 audit，target 为 server list 或 clusterId。
8. 删除确认：删除远端部署仍沿用服务器密码确认机制。

## 14. 迁移步骤

### M1: 后端 spec 和 scripted engine

- 新增 `deployspec`。
- 新增 `apps/scripted`。
- app catalog 加载动态 scripted modules。
- installApp/check/delete 通过 request-scoped registry 找模块。
- 增加 fake remote 单元测试。

### M2: 前端 scripted fallback

- 扩展 catalog types。
- `pairedAppCatalog` 支持 `frontendKind: scripted`。
- 新增 `frontendModuleForApp`。
- 新增 `web/src/apps/scripted/schema.ts`。
- 复用 `AppInstallDialog.vue`。

### M3: 多服务器集群完善

- 支持 cluster target step。
- 支持 coordinator/worker 角色。
- app_instances metadata 统一写入 `clusterId`、`nodeRole`、`nodeIndex`。
- uninstall/check 支持按 clusterId 成组执行。

### M4: 备份、更新和回滚

- scripted spec 增加 backup/restore/update/rollback 生命周期声明。
- registry 增加可选 Backup/Restore/Update/Rollback 接口。
- API 增加实例级 backup/restore/update/rollback task 入口。
- 第一阶段备份摘要写入 `app_instances.metadata.lastBackup`，发布摘要写入 `currentRelease/previousRelease`。

### M5: agent job runner

- agent 增加 `/node/facts`、`/jobs`、job logs。
- Go installer 支持 SSH runner 和 agent runner 两种后端。
- 默认仍使用 SSH，agent runner 作为可选优化。

## 15. 测试策略

后端：

- deployspec 校验测试：非法 name、非法 path、非法 field、secret 泄露防护。
- scripted bundle 选择测试：版本、resourceApp、required parts、hash。
- PlanInstall 测试：single/multiple/cluster steps。
- Service 测试：fake remote 验证上传路径、脚本命令、context 文件。
- 失败测试：节点失败不执行 bootstrap，bootstrap 失败记录失败实例。
- 生命周期测试：backup manifest 生成、update 前强制备份、rollback 不越权恢复数据。
- API 测试：catalog 动态模块、install endpoint、rescan 后可见。

前端：

- catalog pairing 测试：static app 和 scripted app 同时存在。
- schema 转 field 测试：text/password/number/select/switch。
- targetModeResolver 测试：拓扑切换后单选/多选正确。
- submit payload 测试：动态字段进入 parameters，secret 字段不会被前端缓存到不必要位置。

agent：

- job path traversal 拒绝。
- checksum mismatch 拒绝。
- lock conflict 拒绝。
- status 包含 features 和 managed instances。
- job logs tail 不输出 secret。
- backup/update/rollback job 遵守本地锁。

## 16. 推荐落地顺序

建议先做一条最小闭环：

1. 只支持 scripted standalone。
2. 使用 SSH 上传和执行脚本。
3. 前端支持 backend installSchema 动态表单。
4. 记录 app_instance。
5. 再扩展 cluster bootstrap。
6. 增加 backup/update/rollback 生命周期。
7. 最后扩展 agent job runner。

这样可以在不扰动 Docker/MySQL/Redis/MinIO/Nacos 现有实现的情况下，把新模型跑通。跑通后，再选择性把 Nacos 或一个新应用迁移到 scripted engine，作为真实样板。
