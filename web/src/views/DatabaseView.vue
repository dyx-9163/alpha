<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('database.title') }}</h1>
        <p class="page-subtitle">{{ t('database.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
        <el-button type="primary" @click="router.push('/apps')">{{ t('database.deployFromApps') }}</el-button>
      </div>
    </div>

    <div class="aifar-panel status-line">
      <span class="subtle-note">{{ t('database.instanceCount', { count: instances.length }) }}</span>
      <span class="status-pill" :class="{ success: monitoringEnabled }">{{ monitoringStatusLabel }}</span>
      <span v-if="lastMonitorAt" class="subtle-note">{{ t('database.lastMonitoredAt') }} {{ lastMonitorAt }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('database.instances')" name="instances" />
      <el-tab-pane :label="t('database.runs')" name="runs" />
      <el-tab-pane :label="t('apps.settings')" name="settings" />
    </el-tabs>

    <div class="workspace-card database-main">
      <template v-if="tab === 'instances'">
        <div class="table-toolbar">
          <div class="head-actions">
            <span class="status-pill">{{ t('common.all') }} {{ instanceGroups.length }}</span>
            <span class="status-pill">MySQL {{ mysqlGroupCount }}</span>
            <span class="status-pill">Redis {{ redisGroupCount }}</span>
            <span class="status-pill">{{ t('database.nodes') }} {{ databaseNodeCount }}</span>
            <span v-if="routerInstanceCount" class="status-pill">{{ t('database.mysqlRouter') }} {{ routerInstanceCount }}</span>
          </div>
          <div class="monitor-actions">
            <el-switch v-model="monitoringEnabled" :disabled="!canManageApps" :active-text="t('database.realtimeMonitor')" @change="handleMonitoringToggle" />
            <el-button size="small" :loading="monitoringRunning" :disabled="!canManageApps" @click="runRealtimeCheck(true)">{{ t('database.monitorNow') }}</el-button>
            <el-input v-model="search" :placeholder="t('common.search')" clearable class="toolbar-control is-sm" />
          </div>
        </div>

        <div class="db-card-grid" v-if="filteredGroups.length">
          <article v-for="group in filteredGroups" :key="group.id" class="db-card">
            <div class="db-head">
              <div class="app-icon small">{{ group.app === 'redis' ? 'RE' : 'MY' }}</div>
              <div class="db-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ groupSubtitle(group) }}</span>
              </div>
              <div class="db-head-actions">
                <StatusTag :status="group.status" />
                <el-tooltip v-if="hasMysqlClusterDelete(group)" :content="deniedText" :disabled="canManageApps" placement="top">
                  <span>
                    <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteGroup(group, 'mysql-cluster')">{{ t('database.uninstallMysqlCluster') }}</el-button>
                  </span>
                </el-tooltip>
                <el-tooltip v-if="hasRouterClusterDelete(group)" :content="deniedText" :disabled="canManageApps" placement="top">
                  <span>
                    <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteGroup(group, 'mysql-router')">{{ t('database.uninstallMysqlRouterCluster') }}</el-button>
                  </span>
                </el-tooltip>
              </div>
            </div>
            <div class="db-grid">
              <div><span>{{ t('database.engine') }}</span><strong>{{ group.app }}</strong></div>
              <div><span>{{ t('dashboard.topology') }}</span><strong>{{ group.topology || '-' }}</strong></div>
              <div><span>{{ t('common.version') }}</span><strong>{{ group.version }}</strong></div>
              <div><span>{{ t('database.nodes') }}</span><strong>{{ group.nodes.length }}</strong></div>
              <div class="db-grid-wide"><span>{{ groupEndpointLabel(group) }}</span><strong>{{ group.endpoint || '-' }}</strong></div>
              <div><span>{{ t('common.status') }}</span><StatusTag :status="group.status" /></div>
              <div v-if="group.routers.length"><span>{{ t('database.mysqlRouter') }}</span><strong>{{ group.routers.length }}</strong></div>
              <div v-if="group.routers.length" class="db-grid-wide"><span>{{ t('database.routerEndpoint') }}</span><strong>{{ routerEndpointSummary(group) }}</strong></div>
            </div>
            <div class="node-list">
              <div v-for="node in group.nodes" :key="node.instance.id" class="node-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                  <el-tooltip v-if="showNodeDeleteButton(group)" :content="deniedText" :disabled="canManageApps" placement="top">
                    <span>
                      <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteNodes([node], 'single')">{{ t('common.uninstall') }}</el-button>
                    </span>
                  </el-tooltip>
                </div>
              </div>
            </div>
            <div v-if="group.routers.length" class="router-list">
              <div class="section-label">{{ t('database.mysqlRouter') }}</div>
              <div v-for="node in group.routers" :key="node.instance.id" class="node-row router-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                </div>
              </div>
            </div>
          </article>
        </div>
        <div v-else class="empty-state"><div><strong>{{ t('database.noInstancesTitle') }}</strong><span>{{ t('database.noInstancesDesc') }}</span></div></div>
      </template>

      <template v-else-if="tab === 'runs'">
        <RunRecordTable :records="runTasks" show-details @details="openTaskDetails" />
      </template>

      <template v-else>
        <div class="settings-grid">
          <KeyValueGrid :items="settingsItems" />
        </div>
      </template>
    </div>

    <el-dialog
      v-model="deletePromptVisible"
      :title="deletePromptTitle"
      width="520px"
      destroy-on-close
      @closed="resetDeletePrompt"
    >
      <p v-if="deletePromptMessage" class="secret-confirm-message">{{ deletePromptMessage }}</p>
      <el-form label-position="top" class="multi-secret-form">
        <el-form-item v-for="server in deleteServers" :key="server.id" :label="server.label">
          <el-input
            v-model="deletePasswords[server.id]"
            type="password"
            :placeholder="t('apps.deleteServicePasswordPlaceholder')"
            show-password
            @keyup.enter="confirmDeleteScope"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button :disabled="deleteSubmitting" @click="deletePromptVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="danger" :loading="deleteSubmitting" :disabled="!deleteServers.length" @click="confirmDeleteScope">{{ t('common.uninstall') }}</el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import RunRecordTable from '../components/RunRecordTable.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

type AppInstance = {
  id: string
  app: string
  version: string
  serverId: string
  status: string
  topology: string
  metadata: string
  createdAt: string
}

type TaskRecord = {
  id: string
  type: string
  target: string
  status: string
}

type InstanceMetadata = Record<string, any>

type DatabaseNode = {
  instance: AppInstance
  metadata: InstanceMetadata
  serverLabel: string
  endpoint: string
  role: string
  roleLabel: string
}

type DatabaseGroup = {
  id: string
  app: string
  topology: string
  version: string
  title: string
  endpoint: string
  status: string
  createdAt: string
  metadata: InstanceMetadata
  nodes: DatabaseNode[]
  routers: DatabaseNode[]
}

type DeleteScopeKind = 'single' | 'mysql-cluster' | 'mysql-router'

type DeleteScope = {
  kind: DeleteScopeKind
  title: string
  message: string
  nodes: DatabaseNode[]
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<TaskRecord[]>([])
const tab = ref('instances')
const search = ref('')
const monitoringEnabled = ref(true)
const monitoringRunning = ref(false)
const lastMonitorAt = ref('')
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const pendingDeleteScope = ref<DeleteScope | null>(null)
const deletePasswords = ref<Record<string, string>>({})
let monitorTimer: ReturnType<typeof setInterval> | undefined
const monitorIntervalMs = 30000
const mysqlGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'mysql').length)
const redisGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'redis').length)
const databaseNodeCount = computed(() => instances.value.filter((item) => item.app !== 'mysql-router').length)
const routerInstanceCount = computed(() => instances.value.filter((item) => item.app === 'mysql-router').length)
const canManageApps = computed(() => can(permissions.appsManage))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) {
    return t('database.monitorPermissionRequired')
  }
  if (monitoringRunning.value) {
    return t('database.monitoring')
  }
  return monitoringEnabled.value ? t('database.realtimeMonitorOn') : t('database.realtimeMonitorOff')
})
const deletePromptMessage = computed(() => {
  return pendingDeleteScope.value?.message || ''
})
const deletePromptTitle = computed(() => {
  return pendingDeleteScope.value?.title || t('apps.uninstallService')
})
const deleteServers = computed(() => {
  const scope = pendingDeleteScope.value
  if (!scope) return []
  const seen = new Set<string>()
  const out: Array<{ id: string; label: string }> = []
  for (const node of scope.nodes) {
    const id = node.instance.serverId
    if (!id || seen.has(id)) continue
    seen.add(id)
    out.push({ id, label: serverName(id) })
  }
  return out
})
const instanceGroups = computed(() => groupDatabaseInstances(instances.value))
const filteredGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return instanceGroups.value
  return instanceGroups.value.filter((group) => groupSearchText(group).includes(q))
})
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.mysql.') || item.type?.startsWith('apps.mysql-router.') || item.type?.startsWith('apps.redis.')))
const settingsItems = computed(() => [
  { label: 'MySQL', value: t('database.mysqlSettings') },
  { label: 'Redis', value: t('database.redisSettings') },
  { label: t('common.provider'), value: t('common.real') }
])

async function load() {
  instances.value = asArray(await apiGet<AppInstance[] | null>('/database/instances').catch(() => []))
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  tasks.value = asArray<TaskRecord>(await apiGet<TaskRecord[] | null>('/tasks').catch(() => []))
}

function startMonitor() {
  stopMonitor()
  monitorTimer = setInterval(() => {
    if (monitoringEnabled.value && tab.value === 'instances') {
      void runRealtimeCheck(false)
    }
  }, monitorIntervalMs)
}

function stopMonitor() {
  if (monitorTimer) {
    clearInterval(monitorTimer)
    monitorTimer = undefined
  }
}

function handleMonitoringToggle() {
  if (monitoringEnabled.value) {
    void runRealtimeCheck(false)
  }
}

async function runRealtimeCheck(manual: boolean) {
  if (!canManageApps.value) {
    if (manual) {
      ElMessage.warning(deniedText.value)
    }
    return
  }
  if (monitoringRunning.value) {
    return
  }
  monitoringRunning.value = true
  try {
    await load()
    const taskIds: string[] = []
    for (const instance of instances.value.filter(isMonitorableInstance)) {
      try {
        const result = await apiPost<{ taskId: string }>(`/apps/instances/${instance.id}/check`)
        if (result.taskId) {
          taskIds.push(result.taskId)
        }
      } catch (err) {
        if (manual) {
          ElMessage.warning((err as Error).message)
        }
      }
    }
    if (taskIds.length) {
      await waitForTasks(taskIds)
    }
    lastMonitorAt.value = new Date().toLocaleTimeString()
    await load()
  } finally {
    monitoringRunning.value = false
  }
}

function isMonitorableInstance(instance: AppInstance) {
  return ['mysql', 'redis', 'mysql-router'].includes(instance.app)
}

async function waitForTasks(taskIds: string[]) {
  const pending = new Set(taskIds)
  const deadline = Date.now() + 90000
  while (pending.size && Date.now() < deadline) {
    await delay(2000)
    const latest = asArray<TaskRecord>(await apiGet<TaskRecord[] | null>('/tasks').catch(() => []))
    tasks.value = latest
    for (const task of latest) {
      if (pending.has(task.id) && !['pending', 'running'].includes(task.status)) {
        pending.delete(task.id)
      }
    }
  }
}

function delay(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function metadataOf(item: AppInstance) {
  try {
    return JSON.parse(item.metadata || '{}') as InstanceMetadata
  } catch {
    return {}
  }
}

function groupDatabaseInstances(items: AppInstance[]) {
  const groups = new Map<string, DatabaseGroup>()
  const routers: Array<{ item: AppInstance; metadata: InstanceMetadata; node: DatabaseNode }> = []
  for (const item of items) {
    const metadata = metadataOf(item)
    if (item.app === 'mysql-router') {
      routers.push({ item, metadata, node: databaseNode(item, metadata) })
      continue
    }
    const topology = normalizedTopology(item, metadata)
    const key = groupKey(item, metadata, topology)
    const node = databaseNode(item, metadata)
    let group = groups.get(key)
    if (!group) {
      group = {
        id: key,
        app: item.app,
        topology,
        version: item.version,
        title: groupTitle(item, metadata, topology),
        endpoint: '',
        status: item.status,
        createdAt: item.createdAt,
        metadata,
        nodes: [],
        routers: []
      }
      groups.set(key, group)
    }
    group.nodes.push(node)
    if (new Date(item.createdAt).getTime() > new Date(group.createdAt).getTime()) {
      group.createdAt = item.createdAt
    }
  }
  for (const routerNode of routers) {
    const key = routerClusterGroupKey(routerNode.item, routerNode.metadata)
    let group = groups.get(key)
    if (!group) {
      group = {
        id: key,
        app: 'mysql',
        topology: 'innodb-cluster',
        version: routerNode.item.version,
        title: routerClusterTitle(routerNode.item, routerNode.metadata),
        endpoint: '',
        status: routerNode.item.status,
        createdAt: routerNode.item.createdAt,
        metadata: routerNode.metadata,
        nodes: [],
        routers: []
      }
      groups.set(key, group)
    }
    group.routers.push(routerNode.node)
    if (new Date(routerNode.item.createdAt).getTime() > new Date(group.createdAt).getTime()) {
      group.createdAt = routerNode.item.createdAt
    }
  }
  return Array.from(groups.values())
    .map((group) => {
      const nodes = normalizeGroupNodeRoles(group)
      const normalizedGroup = {
        ...group,
        nodes: nodes.sort((a, b) => roleRank(a.role) - roleRank(b.role) || a.serverLabel.localeCompare(b.serverLabel)),
        routers: group.routers.sort((a, b) => a.serverLabel.localeCompare(b.serverLabel))
      }
      return {
        ...normalizedGroup,
        endpoint: groupEndpoint(normalizedGroup),
        status: groupStatus([...normalizedGroup.nodes, ...normalizedGroup.routers])
      }
    })
    .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime())
}

function normalizedTopology(item: AppInstance, metadata: InstanceMetadata) {
  return String(item.topology || metadata.topology || 'standalone')
}

function groupKey(item: AppInstance, metadata: InstanceMetadata, topology: string) {
  if (!isClusterTopology(topology)) {
    return item.id
  }
  const stable = stringValue(metadata.clusterId) || stringValue(metadata.clusterName) || stringValue(metadata.masterName)
  return stable ? `${item.app}:${topology}:${stable}` : `${item.app}:${topology}:${item.id}`
}

function routerClusterGroupKey(item: AppInstance, metadata: InstanceMetadata) {
  const stable = stringValue(metadata.clusterId) || stringValue(metadata.clusterName)
  return stable ? `mysql:innodb-cluster:${stable}` : `mysql:innodb-cluster:${item.id}`
}

function isClusterTopology(topology: string) {
  return ['innodb-cluster', 'sentinel', 'cluster', 'distributed'].includes(topology)
}

function groupTitle(item: AppInstance, metadata: InstanceMetadata, topology: string) {
  if (item.app === 'mysql' && topology === 'innodb-cluster') {
    return `mysql-${stringValue(metadata.clusterName) || item.id.slice(-6)}`
  }
  if (item.app === 'redis' && topology === 'sentinel') {
    return `redis-${stringValue(metadata.masterName) || item.id.slice(-6)}`
  }
  if (item.app === 'redis' && topology === 'cluster') {
    return `redis-${t('database.cluster')}`
  }
  return `${item.app}-${item.id.slice(-6)}`
}

function routerClusterTitle(item: AppInstance, metadata: InstanceMetadata) {
  return `mysql-${stringValue(metadata.clusterName) || stringValue(metadata.clusterId) || item.id.slice(-6)}`
}

function databaseNode(item: AppInstance, metadata: InstanceMetadata): DatabaseNode {
  const role = nodeRole(item, metadata)
  return {
    instance: item,
    metadata,
    serverLabel: serverName(item.serverId),
    endpoint: nodeEndpoint(item, metadata),
    role,
    roleLabel: roleLabel(role)
  }
}

function nodeRole(item: AppInstance, metadata: InstanceMetadata) {
  if (item.app === 'mysql-router') {
    return 'router'
  }
  const topology = normalizedTopology(item, metadata)
  if (topology === 'standalone') {
    return 'standalone'
  }
  if (item.app === 'redis' && topology === 'sentinel' && (metadataBool(metadata, 'sentinel') || checkedOrStaticSentinelRole(metadata))) {
    return 'sentinel'
  }
  if (item.app === 'mysql' && topology === 'innodb-cluster') {
    const currentPrimary = stringValue(metadata.currentPrimaryEndpoint)
    const endpoint = stringValue(metadata.endpoint)
    if (currentPrimary && normalizeEndpoint(endpoint) === normalizeEndpoint(currentPrimary)) {
      return 'primary'
    }
    if (currentPrimary) {
      return 'secondary'
    }
  }
  if (item.app === 'redis' && topology === 'sentinel') {
    const currentMaster = stringValue(metadata.currentMasterEndpoint)
    const endpoint = stringValue(metadata.endpoint)
    if (currentMaster && normalizeEndpoint(endpoint) === normalizeEndpoint(currentMaster)) {
      return 'master'
    }
    if (currentMaster && endpoint) {
      return 'replica'
    }
  }
  const checkedRole = checkedMetadataRole(metadata)
  if (checkedRole) {
    return checkedRole
  }
  return 'node'
}

function normalizeGroupNodeRoles(group: DatabaseGroup) {
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    const primaryEndpoint = groupCurrentPrimaryEndpoint(group)
    if (!primaryEndpoint) {
      return group.nodes
    }
    return group.nodes.map((node) => {
      const role = normalizeEndpoint(node.endpoint) === normalizeEndpoint(primaryEndpoint) ? 'primary' : 'secondary'
      return nodeWithRole(node, role)
    })
  }
  if (group.app === 'redis' && group.topology === 'sentinel') {
    const masterEndpoint = redisMasterEndpoint(group)
    return group.nodes.map((node) => {
      if (node.role === 'sentinel') {
        return node
      }
      if (masterEndpoint && normalizeEndpoint(node.endpoint) === normalizeEndpoint(masterEndpoint)) {
        return nodeWithRole(node, 'master')
      }
      if (masterEndpoint && node.endpoint && node.endpoint !== '-') {
        return nodeWithRole(node, 'replica')
      }
      return ['master', 'replica'].includes(node.role) ? nodeWithRole(node, 'node') : node
    })
  }
  return group.nodes
}

function nodeWithRole(node: DatabaseNode, role: string): DatabaseNode {
  if (node.role === role) {
    return node
  }
  return {
    ...node,
    role,
    roleLabel: roleLabel(role)
  }
}

function checkedMetadataRole(metadata: InstanceMetadata) {
  const lastCheck = metadata.lastCheck || {}
  const status = stringValue(lastCheck.status)
  if (!['ok', 'success', 'running', 'available'].includes(status)) {
    return ''
  }
  return stringValue(metadata.role) || stringValue(metadata.clusterRole)
}

function checkedOrStaticSentinelRole(metadata: InstanceMetadata) {
  return stringValue(metadata.role) === 'sentinel' || stringValue(metadata.clusterRole) === 'sentinel'
}

function roleLabel(role: string) {
  switch (role) {
    case 'primary':
      return t('database.role.primary')
    case 'secondary':
      return t('database.role.secondary')
    case 'master':
      return t('database.role.master')
    case 'replica':
      return t('database.role.replica')
    case 'sentinel':
      return t('database.role.sentinel')
    case 'router':
      return t('database.role.router')
    case 'standalone':
      return t('database.role.standalone')
    default:
      return t('database.role.node')
  }
}

function roleTagType(role: string) {
  switch (role) {
    case 'primary':
    case 'master':
      return 'primary'
    case 'secondary':
    case 'replica':
      return 'info'
    case 'sentinel':
    case 'router':
      return 'warning'
    case 'standalone':
      return 'success'
    default:
      return 'info'
  }
}

function nodeHealthLabel(node: DatabaseNode) {
  switch (nodeHealth(node)) {
    case 'online':
      return t('database.health.online')
    case 'offline':
      return t('database.health.offline')
    default:
      return t('database.health.unknown')
  }
}

function nodeHealthType(node: DatabaseNode) {
  switch (nodeHealth(node)) {
    case 'online':
      return 'success'
    case 'offline':
      return 'danger'
    default:
      return 'info'
  }
}

function nodeHealth(node: DatabaseNode) {
  if (['failed', 'error', 'missing', 'stopped'].includes(node.instance.status)) {
    return 'offline'
  }
  const lastCheckStatus = stringValue(node.metadata.lastCheck?.status)
  const status = lastCheckStatus || node.instance.status
  if (['ok', 'success', 'running', 'available'].includes(status)) {
    return 'online'
  }
  if (['failed', 'error', 'missing', 'stopped'].includes(status)) {
    return 'offline'
  }
  return 'unknown'
}

function roleRank(role: string) {
  switch (role) {
    case 'primary':
    case 'master':
      return 0
    case 'standalone':
      return 1
    case 'secondary':
    case 'replica':
      return 2
    case 'sentinel':
      return 3
    case 'router':
      return 4
    default:
      return 5
  }
}

function nodeEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const endpoint = stringValue(metadata.endpoint)
  if (endpoint) {
    return endpoint
  }
  const host = serverHost(item.serverId)
  if (!host) {
    return '-'
  }
  const topology = normalizedTopology(item, metadata)
  if (item.app === 'redis' && topology === 'sentinel' && nodeRole(item, metadata) === 'sentinel') {
    return `${host}:${numberValue(metadata.sentinelPort) || 26379}`
  }
  if (item.app === 'mysql-router') {
    return `${host}:${numberValue(metadata.basePort) || 6446}`
  }
  return `${host}:${numberValue(metadata.port) || defaultPort(item.app)}`
}

function groupEndpoint(group: DatabaseGroup) {
  if (group.app === 'redis' && group.topology === 'sentinel') {
    const currentMaster = redisMasterEndpoint(group)
    const masterName = groupMetadataValue(group, 'masterName')
    if (currentMaster) {
      return `${masterName || 'master'} -> ${currentMaster}`
    }
    return masterName || group.title
  }
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    const primaryEndpoint = groupCurrentPrimaryEndpoint(group)
    if (primaryEndpoint) {
      return primaryEndpoint
    }
  }
  const preferred = group.nodes.find((node) => ['primary', 'master'].includes(node.role)) || group.nodes[0]
  return preferred?.endpoint || '-'
}

function redisMasterEndpoint(group: DatabaseGroup) {
  return groupMetadataValue(group, 'currentMasterEndpoint')
}

function groupEndpointLabel(group: DatabaseGroup) {
  if (group.app === 'redis' && group.topology === 'sentinel') {
    return redisMasterEndpoint(group) ? t('database.currentMaster') : t('database.masterGroup')
  }
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    return groupCurrentPrimaryEndpoint(group) ? t('database.currentPrimary') : t('database.accessEndpoint')
  }
  return t('common.endpoint')
}

function groupCurrentPrimaryEndpoint(group: DatabaseGroup) {
  return groupMetadataValue(group, 'currentPrimaryEndpoint')
}

function groupMetadataValue(group: DatabaseGroup, key: string) {
  const direct = stringValue(group.metadata[key])
  if (direct) {
    return direct
  }
  for (const node of group.nodes) {
    const value = stringValue(node.metadata[key])
    if (value) {
      return value
    }
  }
  for (const node of group.routers) {
    const value = stringValue(node.metadata[key])
    if (value) {
      return value
    }
  }
  return ''
}

function groupStatus(nodes: DatabaseNode[]) {
  const healths = nodes.map((node) => nodeHealth(node))
  if (!healths.length) {
    return 'unknown'
  }
  if (healths.every((status) => status === 'online')) {
    return 'running'
  }
  if (healths.every((status) => status === 'offline')) {
    return 'failed'
  }
  if (healths.every((status) => status === 'unknown')) {
    return 'unknown'
  }
  return 'degraded'
}

function groupSubtitle(group: DatabaseGroup) {
  const parts = [`${group.topology || '-'}`, `${t('database.nodes')} ${group.nodes.length}`]
  if (group.routers.length) {
    parts.push(`${t('database.mysqlRouter')} ${group.routers.length}`)
  }
  return parts.join(' / ')
}

function routerEndpointSummary(group: DatabaseGroup) {
  const endpoints = Array.from(new Set(group.routers.map((node) => node.endpoint).filter((endpoint) => endpoint && endpoint !== '-')))
  if (!endpoints.length) {
    return '-'
  }
  return endpoints.join(', ')
}

function groupSearchText(group: DatabaseGroup) {
  return [
    group.title,
    group.app,
    group.version,
    group.topology,
    group.endpoint,
    groupCurrentPrimaryEndpoint(group),
    stringValue(group.metadata.clusterName),
    stringValue(group.metadata.masterName),
    routerEndpointSummary(group),
    ...group.nodes.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel]),
    ...group.routers.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel])
  ].join(' ').toLowerCase()
}

function defaultPort(app: string) {
  if (app === 'redis') {
    return 6379
  }
  if (app === 'mysql-router') {
    return 6446
  }
  return 3306
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function serverHost(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server?.host || ''
}

function stringValue(value: unknown) {
  const out = String(value ?? '').trim()
  return out === '<nil>' ? '' : out
}

function numberValue(value: unknown) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function metadataBool(metadata: InstanceMetadata, key: string) {
  const value = metadata[key]
  if (typeof value === 'boolean') {
    return value
  }
  return ['true', '1', 'yes'].includes(String(value ?? '').trim().toLowerCase())
}

function normalizeEndpoint(value: string) {
  return value.trim().toLowerCase().replace(/^tcp:\/\//, '').replace(/^mysql:\/\//, '')
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

function hasMysqlClusterDelete(group: DatabaseGroup) {
  return group.app === 'mysql' && group.topology === 'innodb-cluster' && group.nodes.length > 0
}

function hasRouterClusterDelete(group: DatabaseGroup) {
  return group.routers.length > 0
}

function showNodeDeleteButton(group: DatabaseGroup) {
  return !(group.app === 'mysql' && group.topology === 'innodb-cluster')
}

function openDeleteGroup(group: DatabaseGroup, kind: DeleteScopeKind) {
  const nodes = kind === 'mysql-router' ? group.routers : group.nodes
  openDeleteNodes(nodes, kind, group)
}

function openDeleteNodes(nodes: DatabaseNode[], kind: DeleteScopeKind, group?: DatabaseGroup) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const cleanNodes = nodes.filter((node) => node.instance.id)
  if (!cleanNodes.length) {
    return
  }
  pendingDeleteScope.value = {
    kind,
    title: deleteScopeTitle(kind),
    message: deleteScopeMessage(kind, cleanNodes, group),
    nodes: cleanNodes
  }
  const initialPasswords: Record<string, string> = {}
  for (const node of cleanNodes) {
    if (node.instance.serverId) {
      initialPasswords[node.instance.serverId] = ''
    }
  }
  deletePasswords.value = initialPasswords
  deletePromptVisible.value = true
}

function deleteScopeTitle(kind: DeleteScopeKind) {
  if (kind === 'mysql-cluster') {
    return t('database.uninstallMysqlCluster')
  }
  if (kind === 'mysql-router') {
    return t('database.uninstallMysqlRouterCluster')
  }
  return t('apps.uninstallService')
}

function deleteScopeMessage(kind: DeleteScopeKind, nodes: DatabaseNode[], group?: DatabaseGroup) {
  if (kind === 'single') {
    return t('apps.deleteServicePasswordPrompt', { server: nodes[0]?.serverLabel || '' })
  }
  const name = kind === 'mysql-router' ? t('database.mysqlRouter') : (group?.title || t('database.cluster'))
  return t('database.uninstallGroupPasswordPrompt', {
    name,
    count: uniqueServerCount(nodes)
  })
}

function uniqueServerCount(nodes: DatabaseNode[]) {
  return new Set(nodes.map((node) => node.instance.serverId).filter(Boolean)).size
}

function resetDeletePrompt() {
  if (!deleteSubmitting.value) {
    pendingDeleteScope.value = null
    deletePasswords.value = {}
  }
}

async function confirmDeleteScope() {
  const scope = pendingDeleteScope.value
  if (!scope) {
    return
  }
  const missing = deleteServers.value.find((server) => !String(deletePasswords.value[server.id] || '').trim())
  if (missing) {
    ElMessage.warning(t('database.deletePasswordsRequired'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>('/apps/instances/batch-delete', {
      instanceIds: scope.nodes.map((node) => node.instance.id),
      serverPasswords: deletePasswords.value
    })
    deletePromptVisible.value = false
    pendingDeleteScope.value = null
    deletePasswords.value = {}
    ElMessage.success(t('database.uninstallTaskAccepted', { count: scope.nodes.length }))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
  } catch (err) {
    ElMessage.error(deleteErrorMessage(err))
  } finally {
    deleteSubmitting.value = false
  }
}

function deleteErrorMessage(err: unknown) {
  if (!(err instanceof Error)) {
    return t('apps.deleteServiceFailed')
  }
  const details = (err as Error & { details?: Record<string, unknown> }).details
  const serverId = stringValue(details?.serverId)
  if (serverId) {
    return `${serverName(serverId)}: ${err.message}`
  }
  return err.message
}

onMounted(async () => {
  await load()
  startMonitor()
  void runRealtimeCheck(false)
})

onUnmounted(stopMonitor)
</script>

<style scoped>
.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
}

.database-main {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.monitor-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  min-width: 0;
}

.db-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr));
  gap: 12px;
  padding: 12px;
  min-height: 0;
  overflow: auto;
}

.db-card {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 12px;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease;
}

.db-card:hover {
  border-color: #91caff;
  box-shadow: var(--aifar-shadow-raised);
  transform: translateY(-1px);
}

.db-head {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  margin-bottom: 10px;
}

.db-head-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
  max-width: 220px;
}

.db-title-block {
  min-width: 0;
  flex: 1;
}

.db-head strong,
.db-head span {
  display: block;
}

.db-head strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.db-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.app-icon.small {
  width: 34px;
  height: 34px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  background: var(--aifar-primary-soft);
  color: var(--aifar-primary);
  border: 1px solid #bae0ff;
  font-weight: 850;
}

.db-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.db-grid div {
  min-height: 54px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f7fbff;
  padding: 8px;
  min-width: 0;
}

.db-grid .db-grid-wide {
  grid-column: 1 / -1;
}

.db-grid span {
  display: block;
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.db-grid strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.db-grid-wide strong {
  overflow: visible;
  text-overflow: clip;
  white-space: normal;
  word-break: break-word;
  line-height: 1.35;
}

.node-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}

.router-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--aifar-border-soft);
}

.section-label {
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 700;
}

.node-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #fff;
}

.router-row {
  background: #f8fbff;
}

.node-main {
  min-width: 0;
}

.node-main strong,
.node-main span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-main span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.node-tags {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  white-space: nowrap;
}

.settings-grid {
  padding: 12px;
}

.multi-secret-form {
  display: grid;
  gap: 8px;
}

.secret-confirm-message {
  margin: 0 0 12px;
  color: var(--aifar-text-secondary);
  font-size: 14px;
  line-height: 22px;
}

@media (max-width: 720px) {
  .monitor-actions {
    flex-wrap: wrap;
    justify-content: flex-start;
  }

  .db-head {
    flex-wrap: wrap;
  }

  .db-head-actions {
    justify-content: flex-start;
    max-width: none;
    width: 100%;
  }

  .node-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .node-tags {
    justify-content: flex-start;
  }

}
</style>
