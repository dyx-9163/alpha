<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('nacos.title') }}</h1>
        <p class="page-subtitle">{{ t('nacos.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
        <el-button type="primary" @click="router.push('/apps')">{{ t('nacos.deployFromApps') }}</el-button>
      </div>
    </div>

    <div class="aifar-panel status-line">
      <span class="subtle-note">{{ t('nacos.instanceCount', { count: instances.length }) }}</span>
      <span class="status-pill" :class="{ success: monitoringEnabled }">{{ monitoringStatusLabel }}</span>
      <span v-if="lastMonitorAt" class="subtle-note">{{ t('nacos.lastMonitoredAt') }} {{ lastMonitorAt }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('nacos.instances')" name="instances" />
      <el-tab-pane :label="t('nacos.runs')" name="runs" />
      <el-tab-pane :label="t('nacos.settings')" name="settings" />
    </el-tabs>

    <div class="workspace-card nacos-main">
      <template v-if="tab === 'instances'">
        <div class="table-toolbar">
          <div class="head-actions">
            <span class="status-pill">{{ t('common.all') }} {{ nacosGroups.length }}</span>
            <span class="status-pill">{{ t('nacos.cluster') }} {{ clusterGroupCount }}</span>
            <span class="status-pill">{{ t('nacos.standalone') }} {{ standaloneGroupCount }}</span>
            <span class="status-pill">{{ t('nacos.nodes') }} {{ nacosNodeCount }}</span>
          </div>
          <div class="monitor-actions">
            <el-switch v-model="monitoringEnabled" :disabled="!canManageApps" :active-text="t('nacos.realtimeMonitor')" @change="handleMonitoringToggle" />
            <el-button size="small" :loading="monitoringRunning" :disabled="!canManageApps" @click="runRealtimeCheck(true)">{{ t('nacos.monitorNow') }}</el-button>
            <el-input v-model="search" :placeholder="t('nacos.search')" clearable class="toolbar-control is-sm" />
          </div>
        </div>

        <div v-if="filteredGroups.length" class="nacos-card-grid">
          <article v-for="group in filteredGroups" :key="group.id" class="nacos-card">
            <div class="nacos-head">
              <div class="app-icon small">NA</div>
              <div class="nacos-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ group.topology }} / {{ t('nacos.nodes') }} {{ group.nodes.length }}</span>
              </div>
              <div class="nacos-head-actions">
                <StatusTag :status="group.status" />
                <el-tooltip :content="deniedText" :disabled="canManageApps" placement="top">
                  <span>
                    <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteGroup(group)">{{ t('nacos.uninstallGroup') }}</el-button>
                  </span>
                </el-tooltip>
              </div>
            </div>

            <div class="nacos-info-grid">
              <div>
                <span>{{ t('nacos.service') }}</span>
                <strong>nacos</strong>
              </div>
              <div>
                <span>{{ t('common.version') }}</span>
                <strong>{{ group.version || '-' }}</strong>
              </div>
              <div>
                <span>{{ t('dashboard.topology') }}</span>
                <strong>{{ group.topology || '-' }}</strong>
              </div>
              <div>
                <span>{{ t('nacos.nodes') }}</span>
                <strong>{{ group.nodes.length }}</strong>
              </div>
              <div>
                <span>{{ t('nacos.auth') }}</span>
                <strong>{{ group.authLabel }}</strong>
              </div>
              <div>
                <span>{{ t('nacos.raftPort') }}</span>
                <strong>{{ group.raftPort || '-' }}</strong>
              </div>
              <div class="nacos-info-wide">
                <span>{{ t('nacos.consoleEndpoint') }}</span>
                <strong>{{ group.endpoint || '-' }}</strong>
              </div>
              <div class="nacos-info-wide">
                <span>{{ t('nacos.externalDatabase') }}</span>
                <strong>{{ group.database || t('nacos.noDatabase') }}</strong>
              </div>
            </div>

            <div v-if="isUnavailable(group.status)" class="service-notice danger">{{ t('nacos.serviceUnavailable') }}</div>

            <div class="nacos-node-list">
              <div class="section-label">{{ t('nacos.nodes') }}</div>
              <div v-for="node in group.nodes" :key="node.instance.id" class="nacos-node-row">
                <div class="nacos-node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint }}</span>
                </div>
                <div class="nacos-node-tags">
                  <el-tag size="small" type="info" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" type="info" effect="plain">{{ node.grpcPorts }}</el-tag>
                  <StatusTag :status="node.status" />
                </div>
              </div>
            </div>
          </article>
        </div>
        <div v-else class="empty-state">
          <div>
            <strong>{{ t('nacos.noInstancesTitle') }}</strong>
            <span>{{ t('nacos.noInstancesDesc') }}</span>
          </div>
        </div>
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

    <el-dialog v-model="deletePromptVisible" :title="t('nacos.uninstallGroup')" width="520px" destroy-on-close @closed="resetDeletePrompt">
      <p v-if="deletePromptMessage" class="secret-confirm-message">{{ deletePromptMessage }}</p>
      <el-form label-position="top" class="multi-secret-form">
        <el-checkbox v-if="deleteServers.length > 1" v-model="sameDeletePassword" @change="handleSamePasswordToggle">{{ t('database.samePassword') }}</el-checkbox>
        <el-form-item v-if="sameDeletePassword && deleteServers.length > 1" :label="t('database.samePasswordLabel')">
          <el-input v-model="deleteSharedPassword" type="password" :placeholder="t('apps.deleteServicePasswordPlaceholder')" show-password @keyup.enter="confirmDeleteScope" />
        </el-form-item>
        <el-form-item v-for="server in visibleDeleteServers" v-else :key="server.id" :label="server.label">
          <el-input v-model="deletePasswords[server.id]" type="password" :placeholder="t('apps.deleteServicePasswordPlaceholder')" show-password @keyup.enter="confirmDeleteScope" />
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

type NacosNode = {
  instance: AppInstance
  metadata: InstanceMetadata
  serverLabel: string
  endpoint: string
  status: string
  roleLabel: string
  grpcPorts: string
}

type NacosGroup = {
  id: string
  title: string
  version: string
  topology: string
  status: string
  endpoint: string
  database: string
  authLabel: string
  raftPort: string
  nodes: NacosNode[]
}

type NacosState = {
  instances: AppInstance[]
  servers: any[]
  tasks: TaskRecord[]
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
const checkingInstanceIds = ref<Set<string>>(new Set())
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const pendingDeleteGroup = ref<NacosGroup | null>(null)
const deletePasswords = ref<Record<string, string>>({})
const sameDeletePassword = ref(false)
const deleteSharedPassword = ref('')
let monitorTimer: ReturnType<typeof setInterval> | undefined

const monitorIntervalMs = 30000
const canManageApps = computed(() => can(permissions.appsManage))
const nacosGroups = computed(() => buildNacosGroups(instances.value))
const filteredGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return nacosGroups.value
  return nacosGroups.value.filter((group) => nacosGroupSearchText(group).includes(q))
})
const clusterGroupCount = computed(() => nacosGroups.value.filter((group) => group.topology === 'cluster').length)
const standaloneGroupCount = computed(() => nacosGroups.value.filter((group) => group.topology !== 'cluster').length)
const nacosNodeCount = computed(() => nacosGroups.value.reduce((total, group) => total + group.nodes.length, 0))
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.nacos.')))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) return t('nacos.monitorPermissionRequired')
  if (monitoringRunning.value) return t('nacos.monitoring')
  return monitoringEnabled.value ? t('nacos.realtimeMonitorOn') : t('nacos.realtimeMonitorOff')
})
const settingsItems = computed(() => [
  { label: t('nacos.defaultPorts'), value: t('nacos.defaultPortsHint') },
  { label: t('nacos.cluster'), value: t('nacos.clusterSettings') },
  { label: t('nacos.standalone'), value: t('nacos.standaloneSettings') },
  { label: t('common.provider'), value: t('common.real') }
])
const deletePromptMessage = computed(() => {
  const group = pendingDeleteGroup.value
  return group ? t('nacos.uninstallPasswordPrompt', { name: group.title, count: deleteServers.value.length }) : ''
})
const deleteServers = computed(() => {
  const group = pendingDeleteGroup.value
  if (!group) return []
  const seen = new Set<string>()
  const out: Array<{ id: string; label: string }> = []
  for (const node of group.nodes) {
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

async function load() {
  applyNacosState(await fetchNacosState())
}

async function fetchNacosState(): Promise<NacosState> {
  const [nextInstances, nextServers, nextTasks] = await Promise.all([
    apiGet<AppInstance[] | null>('/nacos/instances').catch(() => []),
    apiGet<any[] | null>('/servers').catch(() => []),
    apiGet<TaskRecord[] | null>('/tasks').catch(() => [])
  ])
  return {
    instances: asArray<AppInstance>(nextInstances).filter((item) => item.app === 'nacos'),
    servers: asArray(nextServers),
    tasks: asArray<TaskRecord>(nextTasks)
  }
}

function applyNacosState(state: NacosState) {
  instances.value = state.instances
  servers.value = state.servers
  tasks.value = state.tasks
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
  const state = await fetchNacosState()
  applyNacosState(state)
  const rows = state.instances.filter((item) => item.app === 'nacos')
  if (!rows.length) {
    lastMonitorAt.value = new Date().toLocaleTimeString()
    return
  }
  monitoringRunning.value = true
  checkingInstanceIds.value = new Set(rows.map((item) => item.id))
  try {
    const taskIds: string[] = []
    for (const instance of rows) {
      try {
        const result = await apiPost<{ taskId: string }>(`/apps/instances/${instance.id}/check`)
        if (result.taskId) {
          taskIds.push(result.taskId)
        }
      } catch (err) {
        if (manual) {
          ElMessage.warning(err instanceof Error ? err.message : t('apps.checkServiceFailed'))
        }
      }
    }
    if (taskIds.length) {
      await waitForTasks(taskIds)
    }
    lastMonitorAt.value = new Date().toLocaleTimeString()
    applyNacosState(await fetchNacosState())
  } finally {
    monitoringRunning.value = false
    checkingInstanceIds.value = new Set()
  }
}

async function waitForTasks(taskIds: string[]) {
  const pending = new Set(taskIds)
  const deadline = Date.now() + 60000
  while (pending.size && Date.now() < deadline) {
    await delay(1000)
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

function buildNacosGroups(rows: AppInstance[]) {
  const groups = new Map<string, { items: AppInstance[]; metas: Map<string, InstanceMetadata> }>()
  for (const item of rows.filter((row) => row.app === 'nacos')) {
    const metadata = metadataOf(item)
    const topology = normalizedTopology(item, metadata)
    const key = topology === 'cluster' ? nacosClusterKey(item, metadata) : `instance:${item.id}`
    const group = groups.get(key) ?? { items: [], metas: new Map<string, InstanceMetadata>() }
    group.items.push(item)
    group.metas.set(item.id, metadata)
    groups.set(key, group)
  }
  return Array.from(groups.entries())
    .map(([id, group]) => createNacosGroup(id, group.items, group.metas))
    .sort((a, b) => a.title.localeCompare(b.title))
}

function createNacosGroup(id: string, items: AppInstance[], metas: Map<string, InstanceMetadata>): NacosGroup {
  const sortedItems = [...items].sort((a, b) => serverName(a.serverId).localeCompare(serverName(b.serverId)))
  const first = sortedItems[0]
  const firstMetadata = first ? metas.get(first.id) ?? metadataOf(first) : {}
  const topology = first ? normalizedTopology(first, firstMetadata) : 'standalone'
  const nodes = sortedItems.map((item, index) => createNacosNode(item, metas.get(item.id) ?? metadataOf(item), topology, index))
  const version = uniqueValues(sortedItems.map((item) => item.version).filter(Boolean)).join(', ')
  return {
    id,
    title: nacosGroupTitle(id, topology, first),
    version,
    topology,
    status: nacosGroupStatus(nodes),
    endpoint: uniqueValues(nodes.map((node) => node.endpoint).filter(Boolean)).join(', '),
    database: nacosDatabaseSummary(firstMetadata),
    authLabel: nacosAuthLabel(sortedItems.map((item) => metas.get(item.id) ?? metadataOf(item))),
    raftPort: stringValue(firstMetadata.raftPort) || '7848',
    nodes
  }
}

function createNacosNode(item: AppInstance, metadata: InstanceMetadata, topology: string, index: number): NacosNode {
  return {
    instance: item,
    metadata,
    serverLabel: serverName(item.serverId),
    endpoint: stringValue(metadata.endpoint) || endpointFromServer(item, metadata),
    status: displayInstanceStatus(item),
    roleLabel: topology === 'cluster' ? `${t('nacos.cluster')} ${index + 1}` : t('nacos.standalone'),
    grpcPorts: `${stringValue(metadata.grpcPort) || numberValue(metadata.port, 8848) + 1000}/${stringValue(metadata.grpcRaftPort) || numberValue(metadata.port, 8848) + 1001}`
  }
}

function nacosGroupTitle(id: string, topology: string, first?: AppInstance) {
  if (topology === 'cluster') {
    const suffix = id.startsWith('cluster:') ? id.replace('cluster:', '').slice(-6) : first?.id.slice(-6)
    return suffix ? `nacos-cluster-${suffix}` : 'nacos-cluster'
  }
  return first ? `nacos-${serverName(first.serverId)}` : 'nacos'
}

function nacosClusterKey(item: AppInstance, metadata: InstanceMetadata) {
  const clusterID = stringValue(metadata.clusterId)
  if (clusterID) {
    return `cluster:${clusterID}`
  }
  const nodes = stringList(metadata.clusterNodes).join('|')
  if (nodes) {
    return `cluster:${nodes}`
  }
  return `cluster:${item.id}`
}

function nacosGroupStatus(nodes: NacosNode[]) {
  const statuses = nodes.map((node) => node.status)
  if (!statuses.length) return 'unknown'
  if (statuses.some((status) => ['checking', 'probing', 'pending', 'deploying'].includes(status))) return 'checking'
  if (statuses.every(isHealthyStatus)) return 'running'
  if (statuses.some(isHealthyStatus)) return 'degraded'
  if (statuses.some((status) => ['unavailable', 'failed', 'error', 'missing', 'stopped'].includes(status))) return 'unavailable'
  return statuses[0] || 'unknown'
}

function isHealthyStatus(status: string) {
  return ['ok', 'success', 'installed', 'running', 'available'].includes(status)
}

function isUnavailable(status: string) {
  return status === 'unavailable'
}

function displayInstanceStatus(item: AppInstance) {
  if (checkingInstanceIds.value.has(item.id)) {
    return 'checking'
  }
  if (['running', 'failed', 'error', 'unavailable', 'stopped', 'missing'].includes(item.status)) {
    return item.status
  }
  return item.status === 'installed' ? 'checking' : item.status || 'unknown'
}

function nacosDatabaseSummary(metadata: InstanceMetadata) {
  const host = stringValue(metadata.dbHost)
  if (!host) {
    return ''
  }
  const port = stringValue(metadata.dbPort) || '3306'
  const name = stringValue(metadata.dbName)
  const user = stringValue(metadata.dbUser)
  const prefix = user ? `${user}@` : ''
  return `${prefix}${host}:${port}${name ? `/${name}` : ''}`
}

function nacosAuthLabel(metas: InstanceMetadata[]) {
  const values = metas.map((metadata) => metadata.authEnabled).filter((value) => value !== undefined && value !== null)
  if (!values.length) {
    return t('nacos.authUnknown')
  }
  return values.every(truthyValue) ? t('nacos.authEnabled') : t('nacos.authDisabled')
}

function normalizedTopology(item: AppInstance, metadata: InstanceMetadata) {
  const topology = stringValue(item.topology) || stringValue(metadata.topology) || stringValue(metadata.mode)
  return topology === 'cluster' ? 'cluster' : 'standalone'
}

function endpointFromServer(item: AppInstance, metadata: InstanceMetadata) {
  const server = servers.value.find((candidate) => candidate.id === item.serverId)
  const port = stringValue(metadata.port) || '8848'
  return server?.host ? `http://${server.host}:${port}/nacos` : '-'
}

function metadataOf(item: AppInstance) {
  try {
    return JSON.parse(item.metadata || '{}') as InstanceMetadata
  } catch {
    return {}
  }
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function stringValue(value: unknown) {
  const out = String(value ?? '').trim()
  return out === '<nil>' ? '' : out
}

function numberValue(value: unknown, fallback: number) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback
}

function stringList(value: unknown) {
  if (Array.isArray(value)) {
    return value.map((item) => stringValue(item)).filter(Boolean)
  }
  const raw = stringValue(value)
  if (!raw) return []
  return raw.split(',').map((item) => item.trim()).filter(Boolean)
}

function truthyValue(value: unknown) {
  if (typeof value === 'boolean') return value
  return ['true', '1', 'yes', 'on'].includes(stringValue(value).toLowerCase())
}

function uniqueValues(values: string[]) {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)))
}

function nacosGroupSearchText(group: NacosGroup) {
  return [
    group.title,
    group.version,
    group.topology,
    group.endpoint,
    group.database,
    group.authLabel,
    ...group.nodes.flatMap((node) => [node.serverLabel, node.endpoint, node.status, node.roleLabel])
  ].join(' ').toLowerCase()
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

function openDeleteGroup(group: NacosGroup) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  pendingDeleteGroup.value = group
  const initialPasswords: Record<string, string> = {}
  for (const node of group.nodes) {
    if (node.instance.serverId) {
      initialPasswords[node.instance.serverId] = ''
    }
  }
  deletePasswords.value = initialPasswords
  deletePromptVisible.value = true
}

function resetDeletePrompt() {
  if (!deleteSubmitting.value) {
    pendingDeleteGroup.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
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

async function confirmDeleteScope() {
  const group = pendingDeleteGroup.value
  if (!group) {
    return
  }
  const passwords = deletePasswordPayload()
  if (Object.keys(passwords).length !== deleteServers.value.length) {
    ElMessage.warning(t('nacos.deletePasswordsRequired'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>('/apps/instances/batch-delete', {
      instanceIds: group.nodes.map((node) => node.instance.id),
      serverPasswords: passwords
    })
    deletePromptVisible.value = false
    pendingDeleteGroup.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
    ElMessage.success(t('nacos.uninstallTaskAccepted', { count: group.nodes.length }))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    deleteSubmitting.value = false
  }
}

onMounted(async () => {
  await load()
  startMonitor()
  if (monitoringEnabled.value && canManageApps.value) {
    void runRealtimeCheck(false)
  }
})

onUnmounted(stopMonitor)
</script>

<style scoped>
.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
}

.nacos-main {
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

.nacos-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr));
  gap: 12px;
  padding: 12px;
  min-height: 0;
  overflow: auto;
}

.nacos-card {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 12px;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease;
}

.nacos-card:hover {
  border-color: #91caff;
  box-shadow: var(--aifar-shadow-raised);
  transform: translateY(-1px);
}

.nacos-head {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  margin-bottom: 10px;
}

.nacos-title-block {
  min-width: 0;
  flex: 1;
}

.nacos-head strong,
.nacos-head span {
  display: block;
}

.nacos-head strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nacos-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.nacos-head-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
  max-width: 240px;
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

.nacos-info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.nacos-info-grid div {
  min-height: 54px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f7fbff;
  padding: 8px;
  min-width: 0;
}

.nacos-info-grid .nacos-info-wide {
  grid-column: 1 / -1;
}

.nacos-info-grid span {
  display: block;
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.nacos-info-grid strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nacos-info-wide strong {
  overflow: visible;
  text-overflow: clip;
  white-space: normal;
  word-break: break-word;
  line-height: 1.35;
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

.nacos-node-list {
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

.nacos-node-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #fff;
}

.nacos-node-main {
  min-width: 0;
}

.nacos-node-main strong,
.nacos-node-main span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nacos-node-main span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.nacos-node-tags {
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
  .monitor-actions,
  .nacos-node-tags {
    flex-wrap: wrap;
    justify-content: flex-start;
  }

  .nacos-head {
    flex-wrap: wrap;
  }

  .nacos-head-actions {
    justify-content: flex-start;
    max-width: none;
    width: 100%;
  }

  .nacos-node-row {
    grid-template-columns: 1fr;
  }
}
</style>
