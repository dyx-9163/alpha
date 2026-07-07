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
      <span class="status-pill" :class="{ success: canManageApps }">{{ monitoringStatusLabel }}</span>
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
            <span v-if="sentinelNodeCount" class="status-pill">{{ roleLabel('sentinel') }} {{ sentinelNodeCount }}</span>
            <span v-if="routerInstanceCount" class="status-pill">{{ t('database.mysqlRouter') }} {{ routerInstanceCount }}</span>
          </div>
          <div class="monitor-actions">
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
                <el-tooltip v-if="hasMysqlClusterStart(group)" :content="deniedText" :disabled="canManageDatabase" placement="top">
                  <span>
                    <el-button
                      size="small"
                      type="primary"
                      plain
                      :loading="startingClusterId === group.id"
                      :disabled="isMysqlClusterStartDisabled(group)"
                      @click="startMysqlCluster(group)"
                    >
                      {{ t('database.startMysqlCluster') }}
                    </el-button>
                  </span>
                </el-tooltip>
                <el-tooltip v-if="hasMysqlGroupDelete(group)" :content="deniedText" :disabled="canManageApps" placement="top">
                  <span>
                    <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteGroup(group, 'mysql-group')">{{ t('database.uninstallMysql') }}</el-button>
                  </span>
                </el-tooltip>
                <el-tooltip v-if="hasRedisGroupDelete(group)" :content="deniedText" :disabled="canManageApps" placement="top">
                  <span>
                    <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteGroup(group, 'redis-group')">{{ t('database.uninstallRedisGroup') }}</el-button>
                  </span>
                </el-tooltip>
              </div>
            </div>
            <div class="db-grid">
              <div><span>{{ t('database.engine') }}</span><strong>{{ group.app }}</strong></div>
              <div><span>{{ t('dashboard.topology') }}</span><strong>{{ group.topology || '-' }}</strong></div>
              <div><span>{{ t('common.version') }}</span><strong>{{ group.version }}</strong></div>
              <div><span>{{ t('database.nodes') }}</span><strong>{{ group.nodes.length }}</strong></div>
              <div v-if="group.sentinels.length">
                <span>{{ roleLabel('sentinel') }}</span>
                <strong>{{ group.sentinels.length }}</strong>
                <StatusTag class="db-grid-tag" :status="group.sentinelStatus" />
              </div>
              <div v-if="showGroupEndpoint(group)" class="db-grid-wide"><span>{{ groupEndpointLabel(group) }}</span><strong>{{ group.endpoint || '-' }}</strong></div>
              <div><span>{{ t('common.status') }}</span><StatusTag :status="group.status" /></div>
              <div v-if="group.routers.length">
                <span>{{ t('database.mysqlRouter') }}</span>
                <strong>{{ group.routers.length }}</strong>
                <StatusTag class="db-grid-tag" :status="group.routerStatus" />
              </div>
            </div>
            <div v-if="isInstallFailedGroup(group)" class="service-notice danger">{{ t('apps.installFailedCleanupHint') }}</div>
            <div v-if="isUnavailable(group.nodeStatus)" class="service-notice danger">{{ databaseServiceUnavailableText(group) }}</div>
            <div v-if="isUnavailable(group.routerStatus)" class="service-notice danger">{{ t('database.routerServiceUnavailable') }}</div>
            <div v-if="group.nodes.length" class="node-list">
              <div v-for="node in group.nodes" :key="node.instance.id" class="node-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                  <el-tooltip v-if="showNodeDeleteButton(group, node)" :content="deniedText" :disabled="canManageApps" placement="top">
                    <span>
                      <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteNodes([node], 'single')">{{ t('common.uninstall') }}</el-button>
                    </span>
                  </el-tooltip>
                </div>
              </div>
            </div>
            <div v-if="group.sentinels.length" class="router-list">
              <div class="section-label">{{ roleLabel('sentinel') }}</div>
              <div v-for="node in group.sentinels" :key="`sentinel-${node.instance.id}`" class="node-row router-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                  <el-tooltip v-if="showSentinelDeleteButton(group, node)" :content="deniedText" :disabled="canManageApps" placement="top">
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
        <el-checkbox v-if="deleteServers.length > 1" v-model="sameDeletePassword" @change="handleSamePasswordToggle">{{ t('database.samePassword') }}</el-checkbox>
        <el-form-item v-if="sameDeletePassword && deleteServers.length > 1" :label="t('database.samePasswordLabel')">
          <el-input
            v-model="deleteSharedPassword"
            type="password"
            :placeholder="t('apps.deleteServicePasswordPlaceholder')"
            show-password
            @keyup.enter="confirmDeleteScope"
          />
        </el-form-item>
        <el-form-item v-for="server in visibleDeleteServers" v-else :key="server.id" :label="server.label">
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
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import RunRecordTable from '../components/RunRecordTable.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { useRealtimeStore } from '../stores/realtime'
import { useTaskProgressStore } from '../stores/taskProgress'

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
  virtual?: boolean
}

type DatabaseGroup = {
  id: string
  app: string
  topology: string
  version: string
  title: string
  endpoint: string
  status: string
  nodeStatus: string
  routerStatus: string
  sentinelStatus: string
  createdAt: string
  metadata: InstanceMetadata
  nodes: DatabaseNode[]
  routers: DatabaseNode[]
  sentinels: DatabaseNode[]
}

type DeleteScopeKind = 'single' | 'mysql-group' | 'redis-group'

type DeleteScope = {
  kind: DeleteScopeKind
  title: string
  message: string
  nodes: DatabaseNode[]
}

type DatabaseState = {
  instances: AppInstance[]
  servers: any[]
  tasks: TaskRecord[]
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const realtime = useRealtimeStore()
const taskProgress = useTaskProgressStore()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<TaskRecord[]>([])
const tab = ref('instances')
const search = ref('')
const monitoringRunning = ref(false)
const monitorStartedAt = ref(0)
const pendingMonitorTaskIds = ref<Set<string>>(new Set())
const lastMonitorAt = ref('')
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const pendingDeleteScope = ref<DeleteScope | null>(null)
const deletePasswords = ref<Record<string, string>>({})
const sameDeletePassword = ref(false)
const deleteSharedPassword = ref('')
const startingClusterId = ref('')
const mysqlGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'mysql').length)
const redisGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'redis').length)
const databaseNodeCount = computed(() => instanceGroups.value.reduce((total, group) => total + group.nodes.length, 0))
const sentinelNodeCount = computed(() => instanceGroups.value.reduce((total, group) => total + group.sentinels.length, 0))
const routerInstanceCount = computed(() => instances.value.filter((item) => item.app === 'mysql-router').length)
const canManageApps = computed(() => can(permissions.appsManage))
const canManageDatabase = computed(() => can(permissions.databaseManage))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) {
    return t('database.monitorPermissionRequired')
  }
  if (monitoringRunning.value) {
    return t('database.monitoring')
  }
  return t('database.backendPushReady')
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
const visibleDeleteServers = computed(() => {
  if (sameDeletePassword.value && deleteServers.value.length > 1) {
    return []
  }
  return deleteServers.value
})
const instanceGroups = computed(() => groupDatabaseInstances(instances.value))
const filteredGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return instanceGroups.value
  return instanceGroups.value.filter((group) => groupSearchText(group).includes(q))
})
const runTasks = computed(() =>
  tasks.value.filter((item) =>
    item.type?.startsWith('apps.mysql.') ||
    item.type?.startsWith('apps.mysql-router.') ||
    item.type?.startsWith('apps.redis.') ||
    item.type?.startsWith('apps.redis-sentinel.') ||
    item.type?.startsWith('database.mysql.')
  )
)
const settingsItems = computed(() => [
  { label: 'MySQL', value: t('database.mysqlSettings') },
  { label: 'Redis', value: t('database.redisSettings') },
  { label: t('common.provider'), value: t('common.real') }
])

async function load() {
  applyDatabaseState(await fetchDatabaseState())
}

async function fetchDatabaseState(): Promise<DatabaseState> {
  const [nextInstances, nextServers, nextTasks] = await Promise.all([
    apiGet<AppInstance[] | null>('/database/instances').catch(() => []),
    apiGet<any[] | null>('/servers').catch(() => []),
    apiGet<TaskRecord[] | null>('/tasks').catch(() => [])
  ])
  return {
    instances: asArray(nextInstances),
    servers: asArray(nextServers),
    tasks: asArray<TaskRecord>(nextTasks)
  }
}

function applyDatabaseState(state: DatabaseState) {
  instances.value = state.instances
  servers.value = state.servers
  tasks.value = state.tasks
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
  monitorStartedAt.value = Date.now()
  let waitForRealtime = false
  try {
    const state = await fetchDatabaseState()
    applyDatabaseState(state)
    const taskIds: string[] = []
    for (const instance of state.instances.filter(isMonitorableInstance)) {
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
      pendingMonitorTaskIds.value = new Set(taskIds)
      void settleFinishedMonitorTasks()
      waitForRealtime = true
      return
    }
    lastMonitorAt.value = new Date().toLocaleTimeString()
    applyDatabaseState(await fetchDatabaseState())
  } finally {
    if (!waitForRealtime) {
      monitoringRunning.value = false
    }
  }
}

function isMonitorableInstance(instance: AppInstance) {
  return ['mysql', 'redis', 'mysql-router'].includes(instance.app)
}

async function settleFinishedMonitorTasks() {
  if (!pendingMonitorTaskIds.value.size) {
    return
  }
  const latest = asArray<TaskRecord>(await apiGet<TaskRecord[] | null>('/tasks').catch(() => []))
  tasks.value = latest
  const pending = new Set(pendingMonitorTaskIds.value)
  for (const task of latest) {
    if (pending.has(task.id) && !['pending', 'running'].includes(task.status)) {
      pending.delete(task.id)
    }
  }
  pendingMonitorTaskIds.value = pending
  if (pending.size === 0) {
    void finishRealtimeCheck()
  }
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
        nodeStatus: item.status,
        routerStatus: 'unknown',
        sentinelStatus: 'unknown',
        createdAt: item.createdAt,
        metadata,
        nodes: [],
        routers: [],
        sentinels: []
      }
      groups.set(key, group)
    }
    if (item.app === 'redis' && topology === 'sentinel') {
      if (nodeRunsSentinel(node)) {
        group.sentinels.push(redisSentinelNode(item, metadata))
      }
      if (nodeIsRedisDataNode(node)) {
        group.nodes.push(node)
      }
    } else {
      group.nodes.push(node)
    }
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
        nodeStatus: 'unknown',
        routerStatus: routerNode.item.status,
        sentinelStatus: 'unknown',
        createdAt: routerNode.item.createdAt,
        metadata: routerNode.metadata,
        nodes: [],
        routers: [],
        sentinels: []
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
      const hydratedGroup = withRedisSentinelDiscoveredNodes(group)
      const nodes = uniqueDatabaseNodes(normalizeGroupNodeRoles(hydratedGroup))
      const routers = uniqueDatabaseNodes(hydratedGroup.routers)
      const sentinels = uniqueDatabaseNodes(hydratedGroup.sentinels)
      const normalizedGroup = {
        ...hydratedGroup,
        nodes: nodes.sort((a, b) => roleRank(a.role) - roleRank(b.role) || a.serverLabel.localeCompare(b.serverLabel)),
        routers: routers.sort((a, b) => a.serverLabel.localeCompare(b.serverLabel)),
        sentinels: sentinels.sort((a, b) => a.serverLabel.localeCompare(b.serverLabel))
      }
      const nodeStatus = normalizedGroup.app === 'mysql' && normalizedGroup.topology === 'innodb-cluster'
        ? mysqlClusterServiceStatus(normalizedGroup)
        : serviceStatus(normalizedGroup.nodes)
      const routerStatus = serviceStatus(normalizedGroup.routers)
      const sentinelStatus = serviceStatus(normalizedGroup.sentinels)
      return {
        ...normalizedGroup,
        endpoint: isUnavailable(nodeStatus) ? t('database.serviceUnavailable') : groupEndpoint(normalizedGroup),
        nodeStatus,
        routerStatus,
        sentinelStatus,
        status: groupStatus(
          nodeStatus,
          normalizedGroup.routers.length > 0 ? routerStatus : sentinelStatus,
          normalizedGroup.routers.length > 0 || normalizedGroup.sentinels.length > 0
        )
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

function redisSentinelNode(item: AppInstance, metadata: InstanceMetadata): DatabaseNode {
  const role = 'sentinel'
  const sentinelMetadata = redisSentinelNodeMetadata(metadata)
  return {
    instance: {
      ...item,
      status: stringValue(sentinelMetadata.lastCheck?.status) || item.status
    },
    metadata: sentinelMetadata,
    serverLabel: serverName(item.serverId),
    endpoint: redisSentinelEndpoint(item, metadata),
    role,
    roleLabel: roleLabel(role)
  }
}

function redisSentinelNodeMetadata(metadata: InstanceMetadata): InstanceMetadata {
  const sentinelLastCheck = metadata.sentinelLastCheck
  if (!sentinelLastCheck || typeof sentinelLastCheck !== 'object') {
    return metadata
  }
  return {
    ...metadata,
    lastCheck: sentinelLastCheck
  }
}

function withRedisSentinelDiscoveredNodes(group: DatabaseGroup): DatabaseGroup {
  if (group.app !== 'redis' || group.topology !== 'sentinel') {
    return group
  }
  const next: DatabaseGroup = {
    ...group,
    nodes: [...group.nodes],
    sentinels: [...group.sentinels]
  }
  const masterEndpoint = redisMasterEndpoint(next)
  if (masterEndpoint) {
    ensureRedisDataNode(next, masterEndpoint, 'master')
  }
  for (const endpoint of groupMetadataList(next, 'replicaEndpoints')) {
    ensureRedisDataNode(next, endpoint, 'replica')
  }
  for (const endpoint of groupMetadataList(next, 'sentinelEndpoints')) {
    ensureRedisSentinelNode(next, endpoint)
  }
  return next
}

function ensureRedisDataNode(group: DatabaseGroup, endpoint: string, role: string) {
  if (!endpoint || hasEndpoint(group.nodes, endpoint)) {
    return
  }
  group.nodes.push(virtualRedisNode(group, endpoint, role))
}

function ensureRedisSentinelNode(group: DatabaseGroup, endpoint: string) {
  if (!endpoint || hasEndpoint(group.sentinels, endpoint)) {
    return
  }
  group.sentinels.push(virtualRedisNode(group, endpoint, 'sentinel'))
}

function virtualRedisNode(group: DatabaseGroup, endpoint: string, role: string): DatabaseNode {
  const server = serverByEndpoint(endpoint)
  const metadata: InstanceMetadata = {
    topology: 'sentinel',
    role,
    endpoint,
    lastCheck: { status: 'unknown' }
  }
  if (role === 'sentinel') {
    metadata.sentinel = true
  }
  const instance: AppInstance = {
    id: `virtual-${group.id}-${role}-${normalizeEndpoint(endpoint)}`,
    app: 'redis',
    version: group.version,
    serverId: server?.id || '',
    status: 'unknown',
    topology: 'sentinel',
    metadata: '',
    createdAt: group.createdAt
  }
  return {
    instance,
    metadata,
    serverLabel: server ? `${server.name} (${server.host})` : endpointHost(endpoint) || endpoint,
    endpoint,
    role,
    roleLabel: roleLabel(role),
    virtual: true
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
    const dataRole = redisSentinelDataRole(item, metadata)
    if (dataRole) {
      return dataRole
    }
    if (metadataBool(metadata, 'sentinel') || checkedOrStaticSentinelRole(metadata)) {
      return 'sentinel'
    }
  }
  const checkedRole = checkedMetadataRole(metadata)
  if (checkedRole) {
    return checkedRole
  }
  return 'node'
}

function redisSentinelDataRole(item: AppInstance, metadata: InstanceMetadata) {
  const staticRole = stringValue(metadata.role) || stringValue(metadata.clusterRole)
  if (['master', 'replica'].includes(staticRole)) {
    return staticRole
  }
  if (staticRole === 'sentinel' || metadataBool(metadata, 'sentinel')) {
    return ''
  }
  if (item.app === 'redis' && normalizedTopology(item, metadata) === 'sentinel') {
    const currentMaster = stringValue(metadata.currentMasterEndpoint)
    const endpoint = redisDataEndpoint(item, metadata)
    if (currentMaster && normalizeEndpoint(endpoint) === normalizeEndpoint(currentMaster)) {
      return 'master'
    }
    if (currentMaster && endpoint) {
      return 'replica'
    }
  }
  return ''
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

function uniqueDatabaseNodes(nodes: DatabaseNode[]) {
  const byKey = new Map<string, DatabaseNode>()
  for (const node of nodes) {
    const key = normalizeNodeIdentity(node)
    const current = byKey.get(key)
    if (!current || shouldPreferNode(node, current)) {
      byKey.set(key, node)
    }
  }
  return Array.from(byKey.values())
}

function normalizeNodeIdentity(node: DatabaseNode) {
  const endpoint = normalizeEndpoint(node.endpoint)
  if (endpoint && endpoint !== '-') {
    return endpoint
  }
  const serverKey = node.instance.serverId || endpointHost(node.endpoint) || node.serverLabel
  return `${serverKey}:${node.role}`
}

function shouldPreferNode(candidate: DatabaseNode, current: DatabaseNode) {
  if (current.virtual !== candidate.virtual) {
    return current.virtual && !candidate.virtual
  }
  const candidateRank = roleRank(candidate.role)
  const currentRank = roleRank(current.role)
  if (candidateRank !== currentRank) {
    return candidateRank < currentRank
  }
  return nodeLastCheckedAt(candidate) > nodeLastCheckedAt(current)
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
    case 'probing':
      return t('database.health.probing')
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
    case 'probing':
      return 'warning'
    default:
      return 'info'
  }
}

function nodeHealth(node: DatabaseNode) {
  if (isMysqlInnoDBNode(node)) {
    const runtimeHealth = mysqlRuntimeHealth(node)
    if (runtimeHealth !== 'unknown') {
      return runtimeHealth
    }
  }
  return baseNodeHealth(node)
}

function baseNodeHealth(node: DatabaseNode) {
  if (serverStatusOffline(nodeServerStatus(node))) {
    return 'offline'
  }
  if (['failed', 'error', 'missing', 'stopped', 'offline', 'unavailable'].includes(node.instance.status)) {
    return 'offline'
  }
  const lastCheckStatus = stringValue(node.metadata.lastCheck?.status)
  const status = lastCheckStatus || node.instance.status
  if (node.virtual && !serverStatusOnline(nodeServerStatus(node))) {
    return 'unknown'
  }
  if (monitoringRunning.value && !statusIsOffline(status) && isNodeCheckStaleForCurrentMonitor(node)) {
    return 'probing'
  }
  if (statusIsOnline(status)) {
    return 'online'
  }
  if (statusIsOffline(status)) {
    return 'offline'
  }
  return 'unknown'
}

function isMysqlInnoDBNode(node: DatabaseNode) {
  return node.instance.app === 'mysql' && normalizedTopology(node.instance, node.metadata) === 'innodb-cluster'
}

function isNodeCheckStaleForCurrentMonitor(node: DatabaseNode) {
  if (!monitorStartedAt.value) {
    return false
  }
  const checkedAt = nodeLastCheckedAt(node)
  return checkedAt === 0 || checkedAt + 1000 < monitorStartedAt.value
}

function nodeLastCheckedAt(node: DatabaseNode) {
  const value = stringValue(node.metadata.lastCheck?.checkedAt || node.metadata.lastCheck?.details?.checkedAt)
  if (!value) {
    return 0
  }
  const time = new Date(value).getTime()
  return Number.isNaN(time) ? 0 : time
}

function nodeServerStatus(node: DatabaseNode) {
  const server = node.instance.serverId
    ? servers.value.find((item) => item.id === node.instance.serverId)
    : serverByEndpoint(node.endpoint)
  return String(server?.status || '').trim().toLowerCase()
}

function serverStatusOnline(status: string) {
  return ['available', 'running', 'success', 'ok'].includes(status)
}

function serverStatusOffline(status: string) {
  return ['failed', 'error', 'missing', 'stopped', 'offline', 'unavailable'].includes(status)
}

function statusIsOnline(status: string) {
  return ['ok', 'success', 'running', 'available'].includes(status)
}

function statusIsOffline(status: string) {
  return ['failed', 'error', 'missing', 'stopped', 'offline', 'unavailable'].includes(status)
}

function nodeRunsSentinel(node: DatabaseNode) {
  return node.instance.app === 'redis' && normalizedTopology(node.instance, node.metadata) === 'sentinel' && metadataBool(node.metadata, 'sentinel')
}

function nodeIsRedisDataNode(node: DatabaseNode) {
  if (node.role === 'sentinel') {
    return false
  }
  const endpoint = normalizeEndpoint(node.endpoint)
  return !!endpoint && endpoint !== '-'
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

function redisSentinelEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const host = serverHost(item.serverId)
  if (!host) {
    return '-'
  }
  return `${host}:${numberValue(metadata.sentinelPort) || 26379}`
}

function redisDataEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const endpoint = stringValue(metadata.endpoint)
  if (endpoint) {
    return endpoint
  }
  const host = serverHost(item.serverId)
  if (!host) {
    return ''
  }
  return `${host}:${numberValue(metadata.port) || 6379}`
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

function showGroupEndpoint(group: DatabaseGroup) {
  return !(group.app === 'redis' && group.topology === 'sentinel')
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
  for (const node of group.sentinels) {
    const value = stringValue(node.metadata[key])
    if (value) {
      return value
    }
  }
  return ''
}

function groupMetadataList(group: DatabaseGroup, key: string) {
  const out: string[] = []
  const add = (value: unknown) => {
    for (const item of stringListValue(value)) {
      if (!out.some((existing) => normalizeEndpoint(existing) === normalizeEndpoint(item))) {
        out.push(item)
      }
    }
  }
  add(group.metadata[key])
  for (const node of [...group.nodes, ...group.routers, ...group.sentinels]) {
    add(node.metadata[key])
  }
  return out
}

function hasEndpoint(nodes: DatabaseNode[], endpoint: string) {
  return nodes.some((node) => normalizeEndpoint(node.endpoint) === normalizeEndpoint(endpoint))
}

function serviceStatus(nodes: DatabaseNode[]) {
  const healths = nodes.map((node) => nodeHealth(node))
  if (!healths.length) {
    return 'unknown'
  }
  const realHealths = nodes.filter((node) => !node.virtual).map((node) => nodeHealth(node))
  if (realHealths.length && realHealths.every((status) => status === 'offline') && healths.every((status) => status !== 'online')) {
    return 'unavailable'
  }
  if (healths.every((status) => status === 'online')) {
    return 'running'
  }
  if (healths.every((status) => status === 'offline')) {
    return 'unavailable'
  }
  if (healths.some((status) => status === 'probing')) {
    return 'probing'
  }
  if (healths.every((status) => status === 'unknown')) {
    return 'unknown'
  }
  return 'degraded'
}

function mysqlClusterServiceStatus(group: DatabaseGroup) {
  const runtimeHealths = group.nodes.map((node) => mysqlRuntimeHealth(node))
  if (!runtimeHealths.length) {
    return 'unknown'
  }
  if (runtimeHealths.every((status) => status === 'offline')) {
    return 'unavailable'
  }
  if (runtimeHealths.some((status) => status === 'probing')) {
    return 'probing'
  }
  const clusterHealths = group.nodes.map((node) => baseNodeHealth(node))
  const hasPrimary = !!groupCurrentPrimaryEndpoint(group)
  if (hasPrimary && clusterHealths.every((status) => status === 'online')) {
    return 'running'
  }
  if (runtimeHealths.every((status) => status === 'online')) {
    return 'unavailable'
  }
  if (runtimeHealths.some((status) => status === 'online')) {
    return 'degraded'
  }
  return 'unknown'
}

function groupStatus(nodeStatus: string, routerStatus: string, hasRouters: boolean) {
  if (nodeStatus === 'unavailable') {
    return 'unavailable'
  }
  if (!hasRouters) {
    return nodeStatus
  }
  if (nodeStatus === 'probing' || routerStatus === 'probing') {
    return 'probing'
  }
  if (nodeStatus === 'running' && routerStatus === 'running') {
    return 'running'
  }
  if (nodeStatus === 'unknown' && routerStatus === 'unknown') {
    return 'unknown'
  }
  return 'degraded'
}

function isUnavailable(status: string) {
  return status === 'unavailable'
}

function isInstallFailedGroup(group: DatabaseGroup) {
  return [...group.nodes, ...group.routers, ...group.sentinels].some((node) => isInstallFailedInstance(node.instance, node.metadata))
}

function isInstallFailedInstance(instance: AppInstance, metadata: InstanceMetadata) {
  return instance.status === 'failed' || metadataBool(metadata, 'installFailed')
}

function databaseServiceUnavailableText(group: DatabaseGroup) {
  return group.app === 'redis' ? t('database.redisServiceUnavailable') : t('database.mysqlServiceUnavailable')
}

function groupSubtitle(group: DatabaseGroup) {
  const parts = [`${group.topology || '-'}`, `${t('database.nodes')} ${group.nodes.length}`]
  if (group.sentinels.length) {
    parts.push(`${roleLabel('sentinel')} ${group.sentinels.length}`)
  }
  if (group.routers.length) {
    parts.push(`${t('database.mysqlRouter')} ${group.routers.length}`)
  }
  return parts.join(' / ')
}

function routerEndpointSummary(group: DatabaseGroup) {
  if (isUnavailable(group.routerStatus)) {
    return t('database.serviceUnavailable')
  }
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
    ...group.routers.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel]),
    ...group.sentinels.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel])
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

function serverByEndpoint(endpoint: string) {
  const host = endpointHost(endpoint)
  return servers.value.find((item) => String(item.host || '').trim() === host)
}

function endpointHost(endpoint: string) {
  const normalized = normalizeEndpoint(endpoint)
  const idx = normalized.lastIndexOf(':')
  return idx > 0 ? normalized.slice(0, idx) : normalized
}

function stringValue(value: unknown) {
  const out = String(value ?? '').trim()
  return out === '<nil>' ? '' : out
}

function stringListValue(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.map((item) => stringValue(item)).filter(Boolean)
  }
  const text = stringValue(value)
  if (!text) {
    return []
  }
  if (text.startsWith('[')) {
    try {
      const parsed = JSON.parse(text)
      if (Array.isArray(parsed)) {
        return parsed.map((item) => stringValue(item)).filter(Boolean)
      }
    } catch {
      return []
    }
  }
  return text.split(',').map((item) => item.trim()).filter(Boolean)
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
  return value.trim().toLowerCase().replace(/^tcp:\/\//, '').replace(/^mysql:\/\//, '').replace(/^redis:\/\//, '')
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

function hasMysqlGroupDelete(group: DatabaseGroup) {
  return group.app === 'mysql' && mysqlGroupDeleteNodes(group).length > 0
}

function hasMysqlClusterStart(group: DatabaseGroup) {
  return group.app === 'mysql' && group.topology === 'innodb-cluster' && group.nodes.length > 0
}

function isMysqlClusterStartDisabled(group: DatabaseGroup) {
  return !canManageDatabase.value || !isMysqlClusterStartable(group) || (!!startingClusterId.value && startingClusterId.value !== group.id)
}

function isMysqlClusterStartable(group: DatabaseGroup) {
  return group.nodes.length >= 3 && isMysqlClusterIneffective(group) && group.nodes.every((node) => mysqlRuntimeHealth(node) === 'online')
}

function isMysqlClusterIneffective(group: DatabaseGroup) {
  return ['unavailable', 'degraded', 'failed', 'error'].includes(group.nodeStatus)
}

function mysqlRuntimeHealth(node: DatabaseNode) {
  if (serverStatusOffline(nodeServerStatus(node))) {
    return 'offline'
  }
  const runtimeStatus = mysqlRuntimeStatus(node)
  if (monitoringRunning.value && !statusIsOffline(runtimeStatus) && isNodeCheckStaleForCurrentMonitor(node)) {
    return 'probing'
  }
  if (statusIsOnline(runtimeStatus)) {
    return 'online'
  }
  if (statusIsOffline(runtimeStatus)) {
    return 'offline'
  }
  return baseNodeHealth(node)
}

function mysqlRuntimeStatus(node: DatabaseNode) {
  const details = node.metadata.lastCheck?.details || {}
  return stringValue(
    details.runtimeStatus ||
    details.mysqlServiceStatus ||
    details.mysqlPortStatus ||
    node.metadata.mysqlRuntimeStatus ||
    node.metadata.runtimeStatus
  )
}

function hasRedisGroupDelete(group: DatabaseGroup) {
  return group.app === 'redis' && redisGroupDeleteNodes(group).length > 0
}

function showNodeDeleteButton(group: DatabaseGroup, node?: DatabaseNode) {
  if (node?.virtual) {
    return false
  }
  if (group.app === 'redis' || group.app === 'mysql') {
    return false
  }
  return !(group.app === 'mysql' && group.topology === 'innodb-cluster')
}

function showSentinelDeleteButton(group: DatabaseGroup, node: DatabaseNode) {
  return showNodeDeleteButton(group, node) && !redisSentinelDataRole(node.instance, node.metadata)
}

function openDeleteGroup(group: DatabaseGroup, kind: DeleteScopeKind) {
  const nodes = groupDeleteNodes(group, kind)
  openDeleteNodes(nodes, kind, group)
}

function groupDeleteNodes(group: DatabaseGroup, kind: DeleteScopeKind) {
  if (kind === 'mysql-group') {
    return mysqlGroupDeleteNodes(group)
  }
  if (kind === 'redis-group') {
    return redisGroupDeleteNodes(group)
  }
  return group.nodes
}

function mysqlGroupDeleteNodes(group: DatabaseGroup) {
  if (group.app !== 'mysql') {
    return []
  }
  if (group.topology === 'innodb-cluster') {
    return uniqueRealNodes([...group.routers, ...group.nodes])
  }
  return uniqueRealNodes(group.nodes)
}

function redisGroupDeleteNodes(group: DatabaseGroup) {
  return uniqueRealNodes([...group.nodes, ...group.sentinels])
}

function uniqueRealNodes(nodes: DatabaseNode[]) {
  const seen = new Set<string>()
  const out: DatabaseNode[] = []
  for (const node of nodes) {
    const id = node.instance.id
    if (node.virtual || !id || !node.instance.serverId || seen.has(id)) {
      continue
    }
    seen.add(id)
    out.push(node)
  }
  return out
}

async function startMysqlCluster(group: DatabaseGroup) {
  if (!canManageDatabase.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  if (!isMysqlClusterStartable(group)) {
    return
  }
  const instanceIds = group.nodes.map((node) => node.instance.id).filter(Boolean)
  if (!instanceIds.length) {
    return
  }
  startingClusterId.value = group.id
  try {
    const result = await apiPost<{ taskId: string }>('/database/mysql/clusters/start', { instanceIds })
    ElMessage.success(t('database.startMysqlClusterAccepted'))
    taskProgress.track(result.taskId, t('database.startMysqlCluster'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('database.startMysqlClusterFailed'))
  } finally {
    startingClusterId.value = ''
  }
}

function openDeleteNodes(nodes: DatabaseNode[], kind: DeleteScopeKind, group?: DatabaseGroup) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const cleanNodes = uniqueRealNodes(nodes)
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
  if (kind === 'mysql-group') {
    return t('database.uninstallMysql')
  }
  if (kind === 'redis-group') {
    return t('database.uninstallRedisGroup')
  }
  return t('apps.uninstallService')
}

function deleteScopeMessage(kind: DeleteScopeKind, nodes: DatabaseNode[], group?: DatabaseGroup) {
  if (kind === 'single') {
    return t('apps.deleteServicePasswordPrompt', { server: nodes[0]?.serverLabel || '' })
  }
  const name = deleteScopeName(kind, group)
  return t('database.uninstallGroupPasswordPrompt', {
    name,
    count: uniqueServerCount(nodes)
  })
}

function deleteScopeName(kind: DeleteScopeKind, group?: DatabaseGroup) {
  if (kind === 'mysql-group' && group?.routers.length) {
    return `${group.title} / ${t('database.mysqlRouter')}`
  }
  return group?.title || t('database.cluster')
}

function uniqueServerCount(nodes: DatabaseNode[]) {
  return new Set(nodes.map((node) => node.instance.serverId).filter(Boolean)).size
}

function resetDeletePrompt() {
  if (!deleteSubmitting.value) {
    pendingDeleteScope.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
  }
}

async function confirmDeleteScope() {
  const scope = pendingDeleteScope.value
  if (!scope) {
    return
  }
  const passwords = deletePasswordPayload()
  if (Object.keys(passwords).length !== deleteServers.value.length) {
    ElMessage.warning(t('database.deletePasswordsRequired'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>('/apps/instances/batch-delete', {
      instanceIds: scope.nodes.map((node) => node.instance.id),
      serverPasswords: passwords
    })
    deletePromptVisible.value = false
    pendingDeleteScope.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
    ElMessage.success(t('database.uninstallTaskAccepted', { count: scope.nodes.length }))
    taskProgress.track(result.taskId, deleteScopeTitle(scope.kind))
  } catch (err) {
    ElMessage.error(deleteErrorMessage(err))
  } finally {
    deleteSubmitting.value = false
  }
}

function handleSamePasswordToggle() {
  if (sameDeletePassword.value && !deleteSharedPassword.value) {
    const firstServer = deleteServers.value[0]
    if (firstServer) {
      deleteSharedPassword.value = deletePasswords.value[firstServer.id] || ''
    }
  }
}

function deletePasswordPayload() {
  const out: Record<string, string> = {}
  if (sameDeletePassword.value && deleteServers.value.length > 1) {
    const password = deleteSharedPassword.value.trim()
    if (!password) {
      return out
    }
    for (const server of deleteServers.value) {
      out[server.id] = password
    }
    return out
  }
  for (const server of deleteServers.value) {
    const password = String(deletePasswords.value[server.id] || '').trim()
    if (!password) {
      return {}
    }
    out[server.id] = password
  }
  return out
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

async function finishRealtimeCheck() {
  lastMonitorAt.value = new Date().toLocaleTimeString()
  try {
    applyDatabaseState(await fetchDatabaseState())
  } finally {
    pendingMonitorTaskIds.value = new Set()
    monitoringRunning.value = false
  }
}

watch(() => realtime.revision, () => {
  const event = realtime.lastEvent
  if (event?.type !== 'task.finished' || !event.taskId || !pendingMonitorTaskIds.value.has(event.taskId)) {
    return
  }
  const pending = new Set(pendingMonitorTaskIds.value)
  pending.delete(event.taskId)
  pendingMonitorTaskIds.value = pending
  if (pending.size === 0) {
    void finishRealtimeCheck()
  }
})

onMounted(async () => {
  await load()
})
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
  max-width: 320px;
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

.db-grid-tag {
  margin-top: 6px;
}

.service-notice {
  margin-top: 8px;
  border-radius: var(--aifar-radius);
  padding: 8px 10px;
  font-size: 12px;
  font-weight: 700;
}

.service-notice.danger {
  color: #cf1322;
  background: #fff2f0;
  border: 1px solid #ffccc7;
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
