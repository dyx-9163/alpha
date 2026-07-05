# Single Node Kubernetes-like Runtime Resource Design

状态：Draft 0.3

目标读者：后续参与 AIFAR Runtime 通用化设计、实现、评审的人。

本文用于反复讨论和收敛一个通用单机编排工具的资源抽象。该执行面暂定名为 AIFAR Runtime。它不定义任何具体业务项目形态，也不假设固定服务名、固定端口、固定 jar/Vue/Go/Python 包结构。核心 runtime 只消费镜像，所有业务形态都应通过通用资源模型表达。

## 0. 命名约定

当前暂定命名：

- 产品/组件名：AIFAR Runtime
- 二进制名：`aifar-runtime`
- systemd 服务名：`aifar-runtime.service`
- CLI 名称：`aifarctl`
- 旧实现/兼容称呼：`aifar-agent`

设计讨论中，除非明确谈旧实现兼容层，否则统一使用 AIFAR Runtime 表示新的单机编排执行面。

## 1. 设计目标

我们要做的是一个单机 Kubernetes-like runtime：

- 单机运行，不做多节点调度。
- 声明式资源模型，用户提交期望态，AIFAR Runtime 持续对齐实际态。
- 资源语义接近 Kubernetes，但实现复杂度控制在单机范围内。
- 支持容器运行、滚动更新、服务代理、入口路由、配置、密钥、卷、任务和状态观测。
- 核心资源只接收 image；jar、静态前端包、Go 二进制、Python 包等语言/框架制品必须先在外部 Build/Import 平面转成 image 或 image archive。
- AIFAR Runtime 不认识任何业务项目、业务模块名或固定服务拓扑。

一句话：

```text
用户声明资源 -> AIFAR Runtime 保存 desired state -> controllers reconcile -> runtime driver 操作容器 -> status/events 反馈实际态
```

## 2. 非目标

第一阶段不做：

- 多节点调度。
- 跨主机 Pod 网络。
- 完整 Kubernetes API 兼容。
- CRD 泛化机制。
- Service Mesh。
- 任意 shell 执行平台。
- 完整容器镜像仓库。
- 完整 Helm/Kustomize 替代品。

这些能力可以预留扩展点，但不能拖累单机核心模型。

## 3. 核心原则

### 3.1 资源优先

核心 API 只认识通用资源：

- Node
- Pod
- Deployment
- Service
- Ingress
- Config
- Secret
- Volume
- Job
- Event

jar、Vue、Go、Python 不是核心资源，也不能直接进入 Pod/Deployment。它们只能由核心之外的 Build/Import 平面转成 image，然后由 Pod/Deployment 的 `containers[].image` 引用。

### 3.2 声明式

用户提交的是 desired state，不是一步步命令：

```yaml
kind: Deployment
spec:
  replicas: 2
```

AIFAR Runtime 负责让实际态逐步靠近这个期望态。

### 3.3 控制器模型

每类资源由对应 controller 维护：

- DeploymentController 维护 ReplicaSet/Pod 数量和发布策略。
- PodController 维护容器生命周期。
- ServiceController 维护 endpoint 与代理监听。
- IngressController 维护外部入口路由。
- JobController 维护一次性任务。

### 3.4 单机边界

所有 Pod 都运行在当前机器。没有 scheduler，或者 scheduler 只做本机 admission：

- 检查 CPU/Memory 资源是否足够。
- 检查端口是否冲突。
- 检查卷路径是否允许。
- 检查镜像是否存在或是否可拉取。

### 3.5 安全默认

默认不允许任意 shell：

- 容器 command/args 可以声明，但 host shell hook 需要显式开关和权限。
- Secret 单独存储、脱敏展示。
- 日志和事件不输出敏感字段。
- 如果产品提供构建/导入能力，必须放在核心 runtime 之外，并使用受控 builder 模板或镜像导入流程。

## 4. 与 Kubernetes 的对应关系

| Kubernetes | 单机 runtime 对应物 | 说明 |
| --- | --- | --- |
| apiserver | local API / CLI | 接收资源 apply/get/delete/describe |
| etcd | local state store | SQLite/BoltDB/JSON store，保存 desired/observed/revision |
| scheduler | local admission | 单机不调度，只做校验和资源准入 |
| kubelet | PodController + RuntimeDriver | 创建、停止、检查容器 |
| controller-manager | controllers | Deployment/Service/Ingress/Job 等控制器 |
| kube-proxy | Service proxy | 宿主机端口代理到 ready Pod endpoint |
| ingress-controller | Ingress proxy | host/path/port 路由到 Service |
| cni | runtime network | Docker/Podman bridge 或 host network |
| kube events | Events | 资源生命周期事件 |

## 5. 系统分层

```text
CLI / HTTP API
  |
Resource Store
  |
Controllers
  |-- DeploymentController
  |-- PodController
  |-- ServiceController
  |-- IngressController
  |-- JobController
  |
Runtime Plane
  |-- DockerDriver
  |-- PodmanDriver
  |-- ContainerdDriver
  |
Network Plane
  |-- ServiceProxy
  |-- IngressProxy
  |
Discovery Plane
  |-- NacosPlugin
  |-- StaticPlugin
  |-- ConsulPlugin

External Build / Import Plane
  |-- produces image or image archive
  |-- feeds containers[].image
```

Build/Import Plane 是核心之外的可选扩展。核心 runtime 的稳定规则是只消费 image，不直接消费 jar、Vue dist、Go 二进制或 Python 包。

## 5.1 进程边界

第一阶段建议采用单二进制、单常驻进程：

```text
aifar-runtime
  |-- Local API / CLI server
  |-- Resource Store
  |-- Reconcile Scheduler
  |-- Controllers
  |-- Runtime Driver
  |-- ServiceProxy / IngressProxy
  |-- Discovery Plugins
  |-- Status / Events / Logs
```

也就是说，核心能力在代码结构上拆成模块、接口和 controller，但部署形态上仍然是一个 `aifar-runtime` 进程。不要把 DeploymentController、ServiceProxy、IngressProxy、DiscoveryPlugin 等拆成多个 systemd 服务或多个独立守护进程。

原因：

- 这是单机 runtime，不需要 Kubernetes 那种多节点控制面的独立伸缩和高可用拆分。
- 单进程更容易离线安装、升级、回滚和排障。
- 本地 store、controller、proxy 在同一进程内共享状态，避免引入额外 IPC、端口、认证和一致性问题。
- Go 的 goroutine、context、channel 和接口足够支撑内部并发与模块隔离。

可以独立出去的只应是外围能力：

- External Build/Import worker：构建镜像可能耗 CPU、依赖工具链，也有更高安全隔离需求。
- 外部 nginx/网关：如果用户已有统一入口网关，可以让 Ingress 只生成配置或状态。
- 外部 registry/cache：镜像仓库和缓存不属于核心 runtime。
- ACME/cert issuer：自动签发和续期可以是后续独立 controller，但证书最终仍写入 Secret 并由 Ingress 引用。

拆分原则：

- 默认不拆进程。
- 只有出现明确的资源隔离、安全隔离、独立升级或外部系统复用需求时，才把某个能力变成外部插件/worker。
- 即使后续支持插件，核心资源模型和 reconcile 主循环仍归 AIFAR Runtime 所有。

## 5.2 调整边界

资源抽象和 reconcile 逻辑应主要收敛到 AIFAR Runtime，因为它才是单机 runtime 的真正执行面。

核心应放入 AIFAR Runtime 的能力：

- 资源 API：apply/get/delete/describe。
- Resource Store：保存 desired state、status、revision、events。
- Controllers：Deployment/Pod/Service/Ingress/Job 等控制器。
- Runtime Driver：Docker/Podman/containerd 适配。
- Network Plane：Service proxy、Ingress proxy、endpoint cache、负载均衡。
- Discovery Plugin：Nacos、static、Consul 等注册发现插件。
- CLI：本机 `aifarctl` 子命令。

面板后端不应承载核心编排逻辑。它的职责应收敛为：

- 安装或升级 AIFAR Runtime。
- 上传资源定义，或代理外部 Build/Import 产出的 image/image archive。
- 调用 AIFAR Runtime API/CLI 提交资源。
- 订阅 AIFAR Runtime status/events/logs。
- 做权限、审计、任务编排和多服务器入口。

前端不应理解底层容器细节。它的职责应收敛为：

- 展示资源对象。
- 提供 YAML/表单编辑。
- 展示状态、事件、日志、发布历史。
- 调用后端代理后的 AIFAR Runtime API。

因此，逻辑上不是“整个系统一起重写”，而是：

```text
核心编排能力：收敛到 AIFAR Runtime
现有后端：变成 AIFAR Runtime 的安装器、代理层、审计层和任务层
现有前端：变成通用资源工作台
```

第一阶段可以只实现 AIFAR Runtime + CLI 最小闭环，不要求面板同步完整改造。

## 6. 通用资源格式

所有资源使用统一 envelope：

```yaml
apiVersion: runtime.local/v1
kind: Deployment
metadata:
  namespace: default
  name: example
  labels:
    app: example
  annotations: {}
spec: {}
status: {}
```

### 6.1 Metadata

```yaml
metadata:
  namespace: default
  name: app
  labels:
    app: app
    tier: backend
  annotations:
    runtime.local/description: demo
```

规则：

- `namespace + kind + name` 唯一。
- labels 用于 selector。
- annotations 用于非核心扩展信息。
- status 由 AIFAR Runtime 写入，用户 apply 时忽略。

### 6.2 Conditions

所有有状态资源统一使用 conditions：

```yaml
status:
  phase: Running
  observedGeneration: 3
  conditions:
    - type: Available
      status: "True"
      reason: MinimumReplicasReady
      message: all desired replicas are ready
      lastTransitionTime: "2026-07-06T22:00:00Z"
```

建议 condition 类型：

- Accepted
- Progressing
- Available
- Degraded
- Ready
- Reconciling

## 7. 资源模型

### 7.1 Node

Node 是当前单机状态，只读或半只读。

```yaml
apiVersion: runtime.local/v1
kind: Node
metadata:
  name: local
status:
  os: linux
  arch: amd64
  runtime:
    type: docker
    version: 24.0.9
  capacity:
    cpu: "8"
    memory: 32Gi
  allocatable:
    cpu: "7"
    memory: 28Gi
  addresses:
    - type: InternalIP
      address: 192.168.1.10
```

Node 用于展示和 admission，不由用户频繁修改。

### 7.2 Pod

Pod 是最小运行单元。它可以直接创建，但主要由 Deployment/Job 生成。

```yaml
apiVersion: runtime.local/v1
kind: Pod
metadata:
  name: web-abc123
  namespace: default
  labels:
    app: web
spec:
  restartPolicy: Always
  networkMode: bridge
  containers:
    - name: web
      image: registry.local/web:1.0.0
      ports:
        - name: http
          containerPort: 8080
      env:
        - name: ENV
          value: prod
      envFrom:
        - configRef:
            name: web-config
        - secretRef:
            name: web-secret
      volumeMounts:
        - name: data
          mountPath: /data
      resources:
        requests:
          cpu: "0.5"
          memory: 512Mi
        limits:
          cpu: "1"
          memory: 1Gi
      probes:
        startup:
          httpGet:
            path: /health
            port: http
        readiness:
          httpGet:
            path: /ready
            port: http
        liveness:
          httpGet:
            path: /live
            port: http
  volumes:
    - name: data
      local:
        path: /var/lib/runtime/volumes/web-data
```

Pod status：

```yaml
status:
  phase: Running
  podIP: 172.20.0.10
  containerStatuses:
    - name: web
      ready: true
      restartCount: 0
      image: registry.local/web:1.0.0
      containerID: docker://abc
```

第一阶段建议限制：

- 单 Pod 支持一个主容器。
- sidecar 可以后续支持。
- initContainers 后续支持。

### 7.3 Deployment

Deployment 管理 Pod 副本、滚动更新和回滚。

```yaml
apiVersion: runtime.local/v1
kind: Deployment
metadata:
  name: web
  namespace: default
spec:
  replicas: 2
  selector:
    matchLabels:
      app: web
  template:
    metadata:
      labels:
        app: web
    spec:
      containers:
        - name: web
          image: registry.local/web:1.0.0
          ports:
            - name: http
              containerPort: 8080
          probes:
            readiness:
              httpGet:
                path: /ready
                port: http
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
    progressDeadlineSeconds: 300
    rollbackOnFailure: true
```

Deployment status：

```yaml
status:
  replicas: 2
  readyReplicas: 2
  updatedReplicas: 2
  availableReplicas: 2
  currentRevision: web-20260706-a1b2c3
  previousRevision: web-20260705-d4e5f6
```

开放问题：

- ReplicaSet 是否作为一等资源暴露？
- 第一阶段可以把 ReplicaSet 作为内部资源，只在 status 中展示 revision。
- 如果后续要做精细回滚、历史审计和扩缩容诊断，ReplicaSet 可提升为可见资源。

### 7.4 Service

Service 提供稳定访问入口，选择一组 ready Pod endpoint。

```yaml
apiVersion: runtime.local/v1
kind: Service
metadata:
  name: web
  namespace: default
spec:
  selector:
    app: web
  ports:
    - name: http
      port: 18080
      targetPort: 8080
      protocol: TCP
  type: NodePort
  sessionAffinity: None
```

单机 Service 类型建议：

| type | 含义 |
| --- | --- |
| ClusterIP | 仅 AIFAR Runtime 内部可路由，不对宿主机开放固定端口 |
| NodePort | 监听宿主机端口，代理到 ready Pods |
| Headless | 不代理，仅暴露 endpoints 给 discovery |
| ExternalName | 指向外部 DNS 或地址 |

第一阶段可只实现 `NodePort` 和 `Headless`。

Service status：

```yaml
status:
  endpoints:
    - pod: web-a
      address: 172.20.0.10:8080
      ready: true
    - pod: web-b
      address: 172.20.0.11:8080
      ready: true
```

### 7.5 Ingress

Ingress 管理外部 HTTP/HTTPS 路由。

```yaml
apiVersion: runtime.local/v1
kind: Ingress
metadata:
  name: public
  namespace: default
spec:
  listeners:
    - name: http
      port: 80
      protocol: HTTP
    - name: https
      port: 443
      protocol: HTTPS
      tls:
        secretRef: public-tls
  rules:
    - host: example.local
      paths:
        - path: /
          pathType: Prefix
          backend:
            service:
              name: web
              port: http
        - path: /api/
          pathType: Prefix
          backend:
            service:
              name: api
              port: http
  websocket:
    enabled: true
```

TLS/SSL 分层原则：

- TLS 终止默认属于 IngressController/IngressProxy，也就是 Network Plane 的外部入口层。
- 证书和私钥不放在 Deployment/Pod，也不放在 Service；它们通过 `Secret` 保存，再由 Ingress 引用。
- Service 默认仍然转发到 Pod 的普通 HTTP/TCP endpoint，避免每个业务容器都重复管理证书。
- 如果后续需要端到端 TLS，可以在 Ingress backend 或 Service port 上扩展 `backendProtocol: HTTPS` 和证书校验策略。
- TLS passthrough、mTLS、SNI 多证书、ACME 自动签发都属于后续增强能力，不影响核心资源边界。

后续可扩展：

- rewrite
- headers
- timeout
- body size
- rate limit

第一阶段建议只做：

- HTTP
- TLS 语义先定在 Ingress，具体实现可延后
- host 可选
- path prefix
- websocket upgrade 透传

### 7.6 Config

Config 存储非敏感配置。

```yaml
apiVersion: runtime.local/v1
kind: Config
metadata:
  name: web-config
  namespace: default
data:
  APP_ENV: prod
  LOG_LEVEL: info
```

Config 可以作为环境变量或文件挂载。

### 7.7 Secret

Secret 存储敏感配置。

```yaml
apiVersion: runtime.local/v1
kind: Secret
metadata:
  name: web-secret
  namespace: default
type: Opaque
data:
  DB_PASSWORD: encrypted-or-base64-value
```

TLS Secret 示例：

```yaml
apiVersion: runtime.local/v1
kind: Secret
metadata:
  name: public-tls
  namespace: default
type: kubernetes.io/tls
data:
  tls.crt: encrypted-or-base64-cert
  tls.key: encrypted-or-base64-key
```

规则：

- store 层必须加密。
- API 返回默认脱敏。
- Event 和日志不得输出明文。
- TLS 私钥只能被 IngressProxy 读取到内存用于监听，不应暴露给 Pod 或普通资源查询。

### 7.8 Volume

Volume 管理本机持久化路径。

```yaml
apiVersion: runtime.local/v1
kind: Volume
metadata:
  name: web-data
  namespace: default
spec:
  type: LocalPath
  path: /data/runtime/volumes/web-data
  accessMode: ReadWriteOnce
  reclaimPolicy: Retain
```

第一阶段建议只支持 LocalPath。

### 7.9 Job

Job 管理一次性任务。

```yaml
apiVersion: runtime.local/v1
kind: Job
metadata:
  name: migrate-db
  namespace: default
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migrate
          image: registry.local/migrate:1.0.0
          args: ["up"]
  backoffLimit: 3
  activeDeadlineSeconds: 600
```

Job status：

```yaml
status:
  phase: Succeeded
  succeeded: 1
  failed: 0
```

### 7.10 Image Build/Import（非核心）

Build/Import 不是核心 runtime 资源。它属于产品外围能力，用于把不同制品输入转成 image 或把已有 image archive 导入本机 runtime。

```yaml
apiVersion: build.runtime.local/v1
kind: ImageBuild
metadata:
  name: api-build
  namespace: default
spec:
  type: java-jar
  source:
    artifact:
      path: artifacts/api.jar
      sha256: "..."
  output:
    image: local/api:1.0.0
  template:
    baseImage: bellsoft/liberica-openjre-rocky:21
```

外部 Build/Import type 示例：

- image-tar
- dockerfile
- java-jar
- static-site
- go-binary
- python-app

原则：

- Deployment/Pod 不直接消费 jar/vue/go/python。
- Deployment/Pod 只消费 `containers[].image`。
- Build/Import 的输出必须是 image 名称或 image archive 导入结果。
- Build/Import 不进入第一阶段核心 API，也不进入 AIFAR Runtime 的核心 reconcile controller。
- AIFAR Runtime 核心可以提供 `LoadImage(archive)` 这类镜像导入能力，因为导入对象已经是标准镜像形态；但不提供 `BuildImage` 语言制品构建能力。

## 8. 资源关系

```text
External Build/Import -> Image

Deployment -> PodTemplate -> Pods
Service -> selector -> ready Pods
Ingress -> Service
Job -> PodTemplate -> Pods
Config/Secret/Volume -> Pod
Node -> observed local machine
```

对象关系建议：

- Deployment 创建 Pod 时写入 ownerReference。
- Service 通过 selector 选择 Pods。
- Ingress 通过 service name 引用 Service。
- Config/Secret/Volume 通过 name 引用。

## 9. Reconcile 流程

### 9.1 Apply

```text
1. 用户 apply yaml/json
2. API validate schema
3. admission 检查端口、路径、资源、引用
4. 写入 desired store
5. enqueue reconcile
6. controller 执行对齐
7. 写 status/events
```

### 9.2 Deployment reconcile

```text
1. 读取 Deployment desired state
2. 计算 template hash
3. 找到当前 revision Pods
4. 如果副本不足，创建 Pods
5. 如果 template 变化，执行 RollingUpdate
6. 等待 readiness
7. 删除超额旧 Pods
8. 更新 status
```

### 9.3 Service reconcile

```text
1. 读取 Service selector
2. 查询 matching Pods
3. 过滤 Ready endpoints
4. 更新 endpoint cache
5. 确保 NodePort 监听存在或停止
6. 可选同步 discovery
```

### 9.4 Ingress reconcile

```text
1. 读取 Ingress listeners/rules
2. 校验端口冲突
3. 对 HTTPS listener 加载 Secret 中的证书和私钥
4. 构建路由表
5. 启动或刷新代理监听，支持证书热更新
6. 请求进入时按 protocol/host/path 匹配 Service
7. 通过 Service endpoint 转发到 Pod
```

## 10. 网络模型

第一阶段采用单机 host proxy 模型：

```text
client -> host:ingressPort -> IngressProxy -> Service -> PodIP:containerPort
client --TLS--> host:443 -> IngressProxy(terminate TLS) -> Service -> PodIP:containerPort
service consumer -> host:servicePort -> ServiceProxy -> PodIP:containerPort
```

Service 代理策略：

- round-robin
- stable hash
- source-ip
- header-based affinity

Ingress 路由策略：

- host
- path prefix
- websocket passthrough
- TLS termination
- optional HTTPS upstream

开放问题：

- Service 的 `ClusterIP` 在单机中是否真的需要虚拟 IP？
- 是否统一用 host port + proxy 替代 ClusterIP？
- 容器内互调是走 host service port，还是走 AIFAR Runtime DNS/hosts？
- TLS passthrough 和 mTLS 是否需要进入后续阶段？

## 11. Discovery 插件

Discovery 不应写死 Nacos。

```yaml
apiVersion: runtime.local/v1
kind: Service
metadata:
  name: api
spec:
  discovery:
    enabled: true
    provider: nacos
    name: api
    group: DEFAULT_GROUP
```

插件能力：

- register
- deregister
- heartbeat
- status

第一阶段可内置：

- none
- nacos

后续可扩展：

- consul
- static file
- dns

## 12. Runtime Driver

Runtime driver 抽象：

```text
CreatePod(spec)
StartPod(id)
StopPod(id)
RemovePod(id)
InspectPod(id)
ListPods(labels)
Logs(pod, container)
Stats(pod)
PullImage(image)
LoadImage(archive)
```

说明：`LoadImage(archive)` 的输入应是 OCI/Docker image archive，而不是 jar、dist、zip、tar.gz 等语言/框架制品包。

第一阶段 driver：

- Docker CLI

后续 driver：

- Docker API
- Podman
- containerd

## 13. Store 模型

Store 至少保存：

- desired resources
- observed statuses
- revisions
- events
- secrets
- leases/locks

建议表/集合：

```text
resources(kind, namespace, name, generation, spec_json, metadata_json)
statuses(kind, namespace, name, observed_generation, status_json)
events(id, involved_kind, namespace, name, reason, message, time)
revisions(owner_kind, namespace, owner_name, revision, spec_hash, payload_json)
secrets(namespace, name, encrypted_data_json)
```

状态原则：

- spec 由用户写。
- status 由 controller 写。
- generation 每次 spec 变化递增。
- observedGeneration 表示 controller 已处理到哪一版。

## 14. CLI 草案

```bash
aifarctl apply -f app.yaml
aifarctl get pods
aifarctl get deployment web -o yaml
aifarctl describe pod web-abc
aifarctl logs pod/web-abc
aifarctl delete -f app.yaml
aifarctl rollout status deployment/web
aifarctl rollout undo deployment/web
aifarctl events
aifarctl doctor
```

AIFAR Runtime 服务：

```bash
aifar-runtime serve --addr 127.0.0.1:18081
```

## 15. API 草案

```text
POST /api/v1/apply
GET  /api/v1/resources/{kind}
GET  /api/v1/resources/{kind}/{namespace}/{name}
DELETE /api/v1/resources/{kind}/{namespace}/{name}
GET  /api/v1/events
GET  /api/v1/logs/{namespace}/{pod}/{container}
GET  /api/v1/status
```

API 可以先只给 CLI 和面板使用，不承诺 Kubernetes API 兼容。

## 16. 与现有 runtime-v2 的迁移关系

现有 `RuntimeSpec` 可作为兼容层：

```text
runtime-v2 RuntimeSpec -> v1 Deployment/Service/Ingress/Config
```

迁移建议：

1. 保持现有 AIFAR runtime-v2 不破坏。
2. 新增通用 resource engine。
3. 让旧 RuntimeSpec 在进入 AIFAR Runtime 时转换为通用资源。
4. 新功能优先落在通用资源模型。
5. 面板逐步从 AIFAR Runtime 页面迁移为通用 Runtime Resources 页面。

## 17. 第一阶段最小闭环

第一阶段目标：不用 UI，先跑通声明式单机编排核心。

必须支持：

- apply/delete/get/describe
- Deployment
- Pod
- Service NodePort
- Ingress HTTP path prefix
- Config
- Secret
- LocalPath Volume
- Events
- Docker CLI driver
- status conditions
- Pod/Deployment 容器只接受 `image`

可以暂缓：

- 外部 Build/Import API
- Job
- CronJob
- TLS 实现，语义已固定在 Ingress/Secret
- Consul/DNS discovery
- multi-container Pod
- initContainers
- ReplicaSet 作为可见资源

最小验收：

```text
1. apply 一个 Deployment + Service + Ingress
2. Deployment/Pod 只引用 image，AIFAR Runtime 拉取或加载镜像后创建容器
3. readiness 通过后 Service 有 endpoint
4. 访问 host:ingressPort/path 能转发到 Pod
5. 修改 image 后执行滚动更新
6. 失败时回滚或标记 Degraded
7. delete 后容器、代理监听、状态记录被清理
```

## 18. 待讨论问题

这些问题需要后续反复收敛：

- 资源 API 名称使用 `runtime.local/v1`，还是使用产品命名空间？
- ReplicaSet 是否一开始就暴露？
- Service 是否需要 ClusterIP 语义，还是单机只保留 NodePort/Headless？
- Pod 是否一开始支持多容器？
- 外部 Build/Import 平面是否需要由产品内置，还是只定义镜像准入规范？
- Secret 加密密钥如何生成、轮换、备份？
- 证书生命周期是否需要内置 ACME/自动续签，还是只支持用户导入证书？
- TLS passthrough/mTLS 是否进入后续阶段？
- Discovery 插件是否挂在 Service 上，还是独立 ServiceDiscovery 资源？
- 本机端口冲突时 admission 直接拒绝，还是自动分配？
- 资源文件格式是否允许多文档 YAML？
- 面板是否直接编辑 YAML，还是提供表单生成 YAML？

## 19. 设计共识草案

当前建议先形成以下共识：

- 这是单机 Kubernetes-like runtime，不是业务部署脚本。
- 核心 runtime 只消费 image，不直接消费 jar、Vue dist、Go 二进制或 Python 包。
- 核心资源模型和业务制品类型解耦，业务制品必须在核心之外转成镜像。
- Deployment/Pod/Service/Ingress 是第一批核心资源。
- Build/Import 是外围能力，不进入第一阶段核心 API。
- AIFAR Runtime 的核心职责是 reconcile desired state，不是执行任意脚本。
- 第一阶段 `aifar-runtime` 采用单二进制、单常驻进程，内部按模块/controller 拆分；不要把核心控制器、代理和 discovery 拆成多个系统服务。
- SSL/TLS 属于 Ingress/Network Plane，证书用 Secret 管理；Service 和 Pod 默认不承担证书终止。
- Nacos 只是 discovery 插件之一，不应进入核心模型。
- 旧 AIFAR RuntimeSpec 通过转换层兼容，不污染新通用资源模型。
