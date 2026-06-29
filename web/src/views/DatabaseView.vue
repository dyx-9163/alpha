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
      <span class="subtle-note">{{ t('database.notChecked') }}</span>
      <span class="status-pill success">{{ t('common.connected') }}</span>
      <span class="subtle-note">{{ t('database.instanceCount', { count: instances.length }) }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('database.instances')" name="instances" />
      <el-tab-pane :label="t('database.backups')" name="backups" />
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
            <span class="status-pill">{{ t('database.nodes') }} {{ instances.length }}</span>
          </div>
          <el-input v-model="search" :placeholder="t('common.search')" clearable class="toolbar-control is-sm" />
        </div>

        <div class="db-card-grid" v-if="filteredGroups.length">
          <article v-for="group in filteredGroups" :key="group.id" class="db-card">
            <div class="db-head">
              <div class="app-icon small">{{ group.app === 'redis' ? 'RE' : 'MY' }}</div>
              <div class="db-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ groupSubtitle(group) }}</span>
              </div>
              <StatusTag :status="group.status" />
            </div>
            <div class="db-grid">
              <div><span>{{ t('database.engine') }}</span><strong>{{ group.app }}</strong></div>
              <div><span>{{ t('dashboard.topology') }}</span><strong>{{ group.topology || '-' }}</strong></div>
              <div><span>{{ t('common.version') }}</span><strong>{{ group.version }}</strong></div>
              <div><span>{{ t('database.nodes') }}</span><strong>{{ group.nodes.length }}</strong></div>
              <div><span>{{ groupEndpointLabel(group) }}</span><strong>{{ group.endpoint || '-' }}</strong></div>
              <div><span>{{ t('common.status') }}</span><StatusTag :status="group.status" /></div>
            </div>
            <div class="node-list">
              <div v-for="node in group.nodes" :key="node.instance.id" class="node-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <el-tag size="small" effect="plain">{{ node.roleLabel }}</el-tag>
                <StatusTag :status="node.instance.status" />
                <div class="node-actions">
                  <el-tooltip :content="deniedText" :disabled="canManageApps" placement="top">
                    <span><el-button size="small" :disabled="!canManageApps" @click="checkInstance(node.instance.id)">{{ t('common.check') }}</el-button></span>
                  </el-tooltip>
                  <el-tooltip :content="deniedText" :disabled="canManageDatabase" placement="top">
                    <span><el-button size="small" type="primary" plain :disabled="!canManageDatabase" @click="backupInstance(node.instance.id)">{{ t('database.backupNow') }}</el-button></span>
                  </el-tooltip>
                </div>
              </div>
            </div>
          </article>
        </div>
        <div v-else class="empty-state"><div><strong>{{ t('database.noInstancesTitle') }}</strong><span>{{ t('database.noInstancesDesc') }}</span></div></div>
      </template>

      <template v-else-if="tab === 'backups'">
        <div class="muted-strip">{{ t('database.backupHint') }}</div>
        <RunRecordTable :records="backupTasks" :type-width="180" />
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
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
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
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const tab = ref('instances')
const search = ref('')
const mysqlGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'mysql').length)
const redisGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'redis').length)
const canManageDatabase = computed(() => can(permissions.databaseManage))
const canManageApps = computed(() => can(permissions.appsManage))
const instanceGroups = computed(() => groupDatabaseInstances(instances.value))
const filteredGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return instanceGroups.value
  return instanceGroups.value.filter((group) => groupSearchText(group).includes(q))
})
const backupTasks = computed(() => tasks.value.filter((item) => item.type === 'database.backup'))
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.mysql.') || item.type?.startsWith('apps.redis.') || item.type?.startsWith('database.')))
const settingsItems = computed(() => [
  { label: 'MySQL', value: t('database.mysqlSettings') },
  { label: 'Redis', value: t('database.redisSettings') },
  { label: t('common.provider'), value: t('common.real') },
  { label: t('database.backups'), value: t('database.backupHint') }
])

async function load() {
  instances.value = asArray(await apiGet<AppInstance[] | null>('/database/instances').catch(() => []))
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  tasks.value = asArray(await apiGet<any[] | null>('/tasks').catch(() => []))
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
  for (const item of items) {
    const metadata = metadataOf(item)
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
        nodes: []
      }
      groups.set(key, group)
    }
    group.nodes.push(node)
    if (new Date(item.createdAt).getTime() > new Date(group.createdAt).getTime()) {
      group.createdAt = item.createdAt
    }
  }
  return Array.from(groups.values())
    .map((group) => ({
      ...group,
      nodes: group.nodes.sort((a, b) => roleRank(a.role) - roleRank(b.role) || a.serverLabel.localeCompare(b.serverLabel)),
      endpoint: groupEndpoint(group),
      status: groupStatus(group.nodes)
    }))
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
  const role = stringValue(metadata.role) || stringValue(metadata.clusterRole)
  if (role) {
    return role
  }
  const topology = normalizedTopology(item, metadata)
  if (topology === 'standalone') {
    return 'standalone'
  }
  if (item.app === 'mysql' && topology === 'innodb-cluster') {
    const currentPrimary = stringValue(metadata.currentPrimaryEndpoint) || stringValue(metadata.primaryEndpoint)
    const endpoint = stringValue(metadata.endpoint)
    if (currentPrimary && normalizeEndpoint(endpoint) === normalizeEndpoint(currentPrimary)) {
      return 'primary'
    }
    if (currentPrimary) {
      return 'secondary'
    }
  }
  return 'node'
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
    case 'standalone':
      return t('database.role.standalone')
    default:
      return t('database.role.node')
  }
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
    default:
      return 4
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
  return `${host}:${numberValue(metadata.port) || defaultPort(item.app)}`
}

function groupEndpoint(group: DatabaseGroup) {
  if (group.app === 'redis' && group.topology === 'sentinel') {
    const masterHost = groupMetadataValue(group, 'masterHost')
    const masterName = groupMetadataValue(group, 'masterName')
    if (masterHost) {
      return `${masterName || 'master'} -> ${masterHost}:${groupPort(group) || 6379}`
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

function groupEndpointLabel(group: DatabaseGroup) {
  if (group.app === 'redis' && group.topology === 'sentinel') {
    return groupMetadataValue(group, 'masterHost') ? t('database.currentMaster') : t('database.masterGroup')
  }
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    return groupCurrentPrimaryEndpoint(group) ? t('database.currentPrimary') : t('database.accessEndpoint')
  }
  return t('common.endpoint')
}

function groupCurrentPrimaryEndpoint(group: DatabaseGroup) {
  return groupMetadataValue(group, 'currentPrimaryEndpoint') || groupMetadataValue(group, 'primaryEndpoint')
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
  return ''
}

function groupPort(group: DatabaseGroup) {
  const direct = numberValue(group.metadata.port)
  if (direct) {
    return direct
  }
  for (const node of group.nodes) {
    const value = numberValue(node.metadata.port)
    if (value) {
      return value
    }
  }
  return 0
}

function groupStatus(nodes: DatabaseNode[]) {
  const statuses = nodes.map((node) => node.instance.status)
  if (statuses.some((status) => ['failed', 'error', 'missing'].includes(status))) {
    return statuses.find((status) => ['failed', 'error', 'missing'].includes(status)) || 'error'
  }
  if (statuses.every((status) => status === 'installed')) {
    return 'installed'
  }
  if (statuses.every((status) => status === 'running')) {
    return 'running'
  }
  return statuses[0] || 'unknown'
}

function groupSubtitle(group: DatabaseGroup) {
  return `${group.topology || '-'} / ${t('database.nodes')} ${group.nodes.length}`
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
    ...group.nodes.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel])
  ].join(' ').toLowerCase()
}

function defaultPort(app: string) {
  return app === 'redis' ? 6379 : 3306
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

function normalizeEndpoint(value: string) {
  return value.trim().toLowerCase().replace(/^tcp:\/\//, '').replace(/^mysql:\/\//, '')
}

async function backupInstance(id: string) {
  if (!canManageDatabase.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const result = await apiPost<{ taskId: string }>(`/database/instances/${id}/backup`)
  ElMessage.success(t('database.backupAccepted'))
  await load()
  void router.push({ path: '/tasks', query: { taskId: result.taskId } })
}

async function checkInstance(id: string) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const result = await apiPost<{ taskId: string }>(`/apps/instances/${id}/check`)
  ElMessage.success(t('apps.checkServiceAccepted'))
  void router.push({ path: '/tasks', query: { taskId: result.taskId } })
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

onMounted(load)
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

.node-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}

.node-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto auto auto;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #fff;
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

.node-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
}

.settings-grid {
  padding: 12px;
}

@media (max-width: 720px) {
  .node-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .node-actions {
    grid-column: 1 / -1;
    justify-content: flex-start;
    flex-wrap: wrap;
  }
}
</style>
