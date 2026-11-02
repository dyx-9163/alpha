# Runtime v2 升级与迁移续作

适用范围：将已有 `agent-runtime-v2` 实例迁移为 `agent-service-controller-v1`。本入口负责运行模型迁移，不负责自动更新面板或 Agent 二进制。请先部署同一发布版本的面板与 Agent，保留数据库、密钥、安装目录和 Agent 状态目录；Agent 能力检查不通过时任务失败，不会静默覆盖二进制。

## 使用入口

- 面板：容器 → AIFAR Runtime → 选择旧模型实例 →「迁移 / 继续迁移」。任务在任务中心展示目标和五个阶段。
- API：`POST /api/v2/apps/instances/{id}/runtime/migration`，需要 `apps.manage`；请求为 `{"reason":"升级运行模型","resumeTaskId":""}`。成功排队返回 HTTP 202 和 `taskId`，不会同步等待远程迁移。
- 状态：`GET /api/v2/apps/instances/{id}/runtime/migration`，返回本地控制面模型状态及该实例最近一次迁移任务；不等同于业务 Ready 检查。
- CLI：`aifar-admin migrate-runtime` 使用同一鉴权 API，不会直接打开或修改本地数据库。

先通过正常登录获得会话令牌，将其放入 `AIFAR_API_TOKEN` 环境变量。不要将令牌放在命令参数、提交文件或日志中。

```text
aifar-admin migrate-runtime --server http://127.0.0.1:8080 --instance INSTANCE_ID --reason "升级运行模型" --wait
aifar-admin migrate-runtime --server http://127.0.0.1:8080 --instance INSTANCE_ID --status
aifar-admin migrate-runtime --server http://127.0.0.1:8080 --instance INSTANCE_ID --resume-task PREVIOUS_TASK_ID --wait
```

远程面板必须使用 HTTPS；CLI 拒绝重定向，避免将鉴权信息发送到重定向目标。默认中文，可指定 `--language en`。`--timeout` 默认 10 分钟，只限制 CLI 等待；等待超时不表示服务端迁移任务失败，应先查询原任务。

发布包在 `bin/` 中提供 Linux 的 `aifar-admin-linux-amd64` 与 Windows 的 `aifar-admin-windows-amd64.exe`；以上命令中的 `aifar-admin` 请替换为对应文件名。

## 执行与恢复

迁移任务沿用现有服务层的实例维护锁、心跳和单事务提交：

1. 检查实例身份、路径、Agent features、旧配置及控制面的服务集合、副本、版本等字段。
2. Agent 尚未切换时，通过受校验的 stdin bootstrap 持久化每服务配置；已切换时跳过 bootstrap。
3. 回读实例配置与每服务 generation / spec hash，拒绝不一致或较新 generation，禁止用旧期望态覆盖。
4. 按 SHA256 校验并归档旧配置为只读备份。
5. 进入 worker 提交保护边界，在同一事务中提交实例模型与每服务控制记录；成功后入口重复调用返回 `complete`，不会重新应用 generation 1。

提交成功表示迁移记录已完成，不表示全部容器或业务依赖 Ready。迁移本身不主动重启业务容器；替换或重启承担代理的 Agent 仍可能短暂断流，不能宣称零中断升级。

失败或取消后，先查看任务失败阶段和错误中的稳定原因码，修复原因，再对**同一实例**续作。`resumeTaskId` 必须对应这个实例的已终止迁移任务，新任务以 `correlationId` 关联原任务。原任务仍在执行时拒绝续作。未指定原任务也可按实例重新提交，仍按远端实际状态恢复；界面会自动关联最近一次任务。

如果服务端进程在 Agent 切换后退出，启动时现有恢复流程会将未结束任务标记失败并回收中断任务的编排锁。用户查看状态后重新提交续作任务，重新校验持久状态，不盲目重放 bootstrap。此处采用显式续作，不在重启后自动发起远程写操作。

模型切换后，旧 CLI 全量写入被 Agent 拒绝。只能按新模型向前修复，不能仅恢复面板数据库就让旧版重新控制已迁移 Agent。

## 2026-11-02 隔离验收范围

旧版 `d1cbb673` 生成数据库并创建真实 nginx Runtime 样例；修订版 Server 经正式 API → worker → 模块 → SSH adapter → Linux Agent 完成迁移。验证包括：旧时间戳兼容、Agent 已切换后的数据库提交故障与续作、服务端进程中断与重启续作、重复调用、generation/hash 和容器 ID 保持。

验收使用专用 Docker 网络、容器、状态卷、数据库与在线准备的镜像。它不是完整 AIFAR Java/Nacos 集成、物理服务器 systemd 或离线升级验收。详细执行记录、任务结果、故障注入及流量采样保存在隔离实验目录，交付时以实际测试报告为准。
