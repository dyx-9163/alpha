# Database Runtime Health Source Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让数据库实例页的在线/离线只反映 MySQL、Redis 和 MySQL Router 自身的应用检测结果，不再被服务器探测状态覆盖。

**Architecture:** 新增一个不接收 `server.status` 的纯 TypeScript 健康状态解析模块，并由 `DatabaseView.vue` 把应用检测状态和 MySQL 运行时状态传入。页面继续负责节点分组和集群聚合，解析模块只负责把权威应用检测信号映射为 `online`、`offline`、`probing` 或 `unknown`。

**Tech Stack:** Vue 3、TypeScript、Vitest、Pinia 实时快照、Element Plus。

## Global Constraints

- 不修改后端采集器、数据库结构、API 或 SSE 协议。
- 不使用 `server.status` 推断数据库节点健康状态。
- 没有 `metadata.lastCheck.status` 或 MySQL 运行时检测依据时返回 `unknown`，不得用安装状态猜测在线。
- InnoDB Cluster 继续保留运行时、Primary 和集群有效性判断。
- Redis Sentinel 虚拟节点没有独立应用检测结果时保持 `unknown`。
- 不修改页面布局或用户可见文案。
- 保留 `web/src/views/DatabaseView.vue` 当前未提交的备份、恢复和维护操作改动；仅编辑健康状态相关代码段。
- 测试命令使用仓库现有 Node/Vitest 工具链；完整前端门禁使用 `pnpm test:web` 和 `pnpm web:build`。

## File Structure

- Create: `web/src/database/databaseHealth.ts` — 纯健康状态归一化和 MySQL 运行时优先级解析。
- Create: `web/src/database/databaseHealth.test.ts` — 覆盖权威应用检测、无检测结果及 MySQL 运行时优先级。
- Modify: `web/src/views/DatabaseView.vue` — 删除服务器状态健康判断，改用纯解析模块；保留现有节点分组和集群聚合。
- Modify: `memory.md` — 追加本轮问题、根因、实现和验证结论，不记录长日志。

---

### Task 1: 建立应用检测健康状态解析器

**Files:**
- Create: `web/src/database/databaseHealth.test.ts`
- Create: `web/src/database/databaseHealth.ts`

**Interfaces:**
- Produces: `DatabaseHealth = 'online' | 'offline' | 'unknown' | 'probing'`
- Produces: `DatabaseHealthSource { app?: unknown; topology?: unknown; checkStatus?: unknown; runtimeStatus?: unknown }`
- Produces: `healthFromCheckStatus(status: unknown): DatabaseHealth`
- Produces: `resolveDatabaseNodeHealth(source: DatabaseHealthSource): DatabaseHealth`
- Produces: `resolveMySQLRuntimeHealth(source: DatabaseHealthSource): DatabaseHealth`

- [ ] **Step 1: 写入能够复现错误优先级的失败测试**

Create `web/src/database/databaseHealth.test.ts`:

```ts
import { describe, expect, it } from 'vitest'
import {
  healthFromCheckStatus,
  resolveDatabaseNodeHealth,
  resolveMySQLRuntimeHealth
} from './databaseHealth'

describe('database runtime health source', () => {
  it('maps authoritative application checks without a server-health input', () => {
    expect(healthFromCheckStatus('running')).toBe('online')
    expect(healthFromCheckStatus('failed')).toBe('offline')
    expect(healthFromCheckStatus('probing')).toBe('probing')
  })

  it('keeps nodes unknown when no application check exists', () => {
    expect(resolveDatabaseNodeHealth({ app: 'redis', topology: 'standalone' })).toBe('unknown')
    expect(resolveDatabaseNodeHealth({ app: 'mysql-router', topology: 'standalone', checkStatus: '' })).toBe('unknown')
  })

  it('uses the application check for ordinary MySQL and Redis nodes', () => {
    expect(resolveDatabaseNodeHealth({ app: 'redis', topology: 'standalone', checkStatus: 'running' })).toBe('online')
    expect(resolveDatabaseNodeHealth({ app: 'mysql', topology: 'standalone', checkStatus: 'unavailable' })).toBe('offline')
  })

  it('uses MySQL runtime evidence before the overall cluster check', () => {
    expect(resolveDatabaseNodeHealth({
      app: 'mysql',
      topology: 'innodb-cluster',
      runtimeStatus: 'running',
      checkStatus: 'failed'
    })).toBe('online')
    expect(resolveMySQLRuntimeHealth({ runtimeStatus: 'offline', checkStatus: 'running' })).toBe('offline')
  })

  it('falls back to the application check when MySQL runtime evidence is unknown', () => {
    expect(resolveMySQLRuntimeHealth({ runtimeStatus: '', checkStatus: 'running' })).toBe('online')
    expect(resolveMySQLRuntimeHealth({ runtimeStatus: 'unexpected', checkStatus: 'failed' })).toBe('offline')
  })
})
```

- [ ] **Step 2: 运行定向测试并确认 RED**

Run from `web/`:

```powershell
node node_modules/vitest/vitest.mjs run src/database/databaseHealth.test.ts
```

Expected: FAIL because `./databaseHealth` does not exist. This proves the test is exercising the new production boundary, not existing view internals.

- [ ] **Step 3: 实现最小纯函数模块**

Create `web/src/database/databaseHealth.ts`:

```ts
export type DatabaseHealth = 'online' | 'offline' | 'unknown' | 'probing'

export type DatabaseHealthSource = {
  app?: unknown
  topology?: unknown
  checkStatus?: unknown
  runtimeStatus?: unknown
}

const onlineStatuses = new Set(['ok', 'success', 'running', 'available'])
const offlineStatuses = new Set([
  'failed', 'error', 'missing', 'stopped', 'offline', 'unavailable',
  'unhealthy', 'down', 'no-endpoints'
])
const probingStatuses = new Set(['probing', 'checking'])

export function healthFromCheckStatus(status: unknown): DatabaseHealth {
  const normalized = normalize(status)
  if (onlineStatuses.has(normalized)) return 'online'
  if (offlineStatuses.has(normalized)) return 'offline'
  if (probingStatuses.has(normalized)) return 'probing'
  return 'unknown'
}

export function resolveDatabaseNodeHealth(source: DatabaseHealthSource): DatabaseHealth {
  if (normalize(source.app) === 'mysql' && normalize(source.topology) === 'innodb-cluster') {
    return resolveMySQLRuntimeHealth(source)
  }
  return healthFromCheckStatus(source.checkStatus)
}

export function resolveMySQLRuntimeHealth(source: DatabaseHealthSource): DatabaseHealth {
  const runtimeHealth = healthFromCheckStatus(source.runtimeStatus)
  return runtimeHealth === 'unknown' ? healthFromCheckStatus(source.checkStatus) : runtimeHealth
}

function normalize(value: unknown) {
  return String(value ?? '').trim().toLowerCase()
}
```

- [ ] **Step 4: 运行定向测试并确认 GREEN**

Run from `web/`:

```powershell
node node_modules/vitest/vitest.mjs run src/database/databaseHealth.test.ts
```

Expected: PASS, 1 test file and 5 tests.

- [ ] **Step 5: 做变异检查并提交独立解析器**

Mutation check:

- 将 `running` 从 `onlineStatuses` 移除时，普通 Redis 和 MySQL 运行时测试必须失败。
- 把 `resolveMySQLRuntimeHealth()` 改成优先 `checkStatus` 时，运行时优先级测试必须失败。
- 把无状态默认值改成 `online` 时，无应用检测结果测试必须失败。

Commit only the new files:

```powershell
git add -- web/src/database/databaseHealth.ts web/src/database/databaseHealth.test.ts
git commit -m "fix: separate database health from server probes"
```

### Task 2: 接入数据库实例页并删除服务器健康兜底

**Files:**
- Modify: `web/src/views/DatabaseView.vue:290-360`
- Modify: `web/src/views/DatabaseView.vue:940-1040`
- Modify: `web/src/views/DatabaseView.vue:1710-1745`

**Interfaces:**
- Consumes: `DatabaseHealth`, `healthFromCheckStatus()`, `resolveDatabaseNodeHealth()`, `resolveMySQLRuntimeHealth()` from `web/src/database/databaseHealth.ts`.
- Preserves: existing `nodeHealth(node)`, `baseNodeHealth(node)`, `mysqlRuntimeHealth(node)` local call sites so group aggregation and action availability do not need unrelated changes.

- [ ] **Step 1: 导入解析器并删除页面内重复状态类型**

Add beside the existing `../database/*` imports:

```ts
import {
  healthFromCheckStatus,
  resolveDatabaseNodeHealth,
  resolveMySQLRuntimeHealth,
  type DatabaseHealth
} from '../database/databaseHealth'
```

Remove the local declaration:

```ts
type DatabaseHealth = 'online' | 'offline' | 'unknown' | 'probing'
```

- [ ] **Step 2: 将节点状态改为只传递应用检测数据**

Replace the health wrappers with:

```ts
function nodeHealth(node: DatabaseNode): DatabaseHealth {
  return resolveDatabaseNodeHealth({
    app: node.instance.app,
    topology: normalizedTopology(node.instance, node.metadata),
    checkStatus: node.metadata.lastCheck?.status,
    runtimeStatus: mysqlRuntimeStatus(node)
  })
}

function baseNodeHealth(node: DatabaseNode): DatabaseHealth {
  return healthFromCheckStatus(node.metadata.lastCheck?.status)
}
```

Delete `isMysqlInnoDBNode()`, because MySQL topology selection is now contained in the pure resolver.

- [ ] **Step 3: 将 MySQL 运行时包装函数切换到纯解析器**

Replace the local `mysqlRuntimeHealth()` body with:

```ts
function mysqlRuntimeHealth(node: DatabaseNode): DatabaseHealth {
  return resolveMySQLRuntimeHealth({
    checkStatus: node.metadata.lastCheck?.status,
    runtimeStatus: mysqlRuntimeStatus(node)
  })
}
```

- [ ] **Step 4: 删除所有数据库健康对服务器状态和安装状态的依赖**

Delete these now-unused functions from `DatabaseView.vue`:

```ts
nodeServerStatus
serverStatusOnline
serverStatusOffline
statusIsOnline
statusIsOffline
```

Also remove `const instanceStatus = ...` and every fallback branch that maps `node.instance.status` to online/offline. Do not remove `servers`, `serverByEndpoint()` or server-name/address helpers because they are still required for display, Endpoint association and deletion context.

- [ ] **Step 5: 运行定向状态测试和 TypeScript/生产构建**

Run:

```powershell
Set-Location web
node node_modules/vitest/vitest.mjs run src/database/databaseHealth.test.ts
Set-Location ..
pnpm web:build
```

Expected: health tests PASS; `vue-tsc --noEmit` and Vite production build PASS with no unused symbols or type errors.

- [ ] **Step 6: 审核差异边界**

Run:

```powershell
git diff --check -- web/src/database/databaseHealth.ts web/src/database/databaseHealth.test.ts web/src/views/DatabaseView.vue
git diff -- web/src/views/DatabaseView.vue
```

Expected: no whitespace errors. The view diff for this task is limited to imports and health-source functions; pre-existing backup/restore hunks remain unchanged and are not attributed to this fix.

Do not create an integration commit that captures unrelated pre-existing `DatabaseView.vue` changes. If an exact health-only index patch cannot be staged safely, leave the integration change uncommitted and report that boundary explicitly.

### Task 3: 完整前端回归与项目记录

**Files:**
- Modify: `memory.md`

**Interfaces:**
- Consumes: completed Task 1 and Task 2 behavior.
- Produces: verified frontend result and reusable project memory entry.

- [ ] **Step 1: 运行完整前端逻辑测试**

Run from repository root:

```powershell
pnpm test:web
```

Expected: all Vitest files pass with no failed tests.

- [ ] **Step 2: 再次运行前端生产构建**

Run:

```powershell
pnpm web:build
```

Expected: `vue-tsc --noEmit` and Vite build both succeed.

- [ ] **Step 3: 验证服务器状态调用已从数据库健康路径移除**

Run:

```powershell
rg -n "nodeServerStatus|serverStatusOnline|serverStatusOffline|statusIsOnline|statusIsOffline" web/src/views/DatabaseView.vue
```

Expected: no matches. This is a cleanup audit after behavior tests, not a substitute for them.

- [ ] **Step 4: 追加精简项目记忆**

Append under `## 2026-08-02` in `memory.md`:

```markdown
- 问题：数据库实例页的在线/离线被所属服务器探测状态覆盖。
- 结论：数据库节点健康已改为只读取应用实例检测快照和 MySQL 运行时详情；无可信检测结果显示未知，服务器状态仅保留用于服务器管理上下文。定向状态测试、完整前端测试和生产构建均通过。
```

Only write the final verification claims that actually passed.

- [ ] **Step 5: 最终范围与工作区检查**

Run:

```powershell
git diff --check
git status --short
git diff -- web/src/database/databaseHealth.ts web/src/database/databaseHealth.test.ts web/src/views/DatabaseView.vue memory.md
```

Expected: no whitespace errors; report the new health module/tests, the narrow view integration, and the memory entry separately from all pre-existing dirty files.
