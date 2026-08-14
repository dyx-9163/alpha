<template>
  <section class="dashboard-page">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('dashboard.title') }}</h1>
        <p class="page-subtitle">{{ t('dashboard.subtitle') }}</p>
      </div>
    </div>

    <div class="snapshot-bar">
      <span>{{ t('dashboard.snapshot') }}</span>
      <strong>{{ now }}</strong>
    </div>

    <MetricGrid :items="kpis" class="dashboard-kpis" />

    <div class="workspace-card dashboard-section-card dashboard-component-health">
      <div class="dashboard-section-head">
        <div>
          <h2 class="section-title">{{ t('dashboard.componentHealth') }}</h2>
          <p v-if="selectedComponentTab === 'servers' && hiddenComponentCount > 0">{{ t('dashboard.serverMetricsLimitHint', { count: hiddenComponentCount }) }}</p>
          <p v-else>{{ t('dashboard.componentHealthHint') }}</p>
        </div>
        <router-link v-if="selectedComponentTab !== allComponentsTabKey" class="dashboard-section-link" :to="selectedComponentLink">{{ t('dashboard.viewAllResources') }}</router-link>
      </div>
      <div class="dashboard-component-overview" data-dashboard-resource-overview>
        <span>
          <span>{{ t('dashboard.allResources') }}</span>
          <strong>{{ aggregateComponentSummary.total }}</strong>
        </span>
        <span class="success">{{ t('common.running') }} {{ aggregateComponentSummary.running }}</span>
        <span class="danger">{{ t('common.unavailable') }} {{ aggregateComponentSummary.unavailable }}</span>
      </div>
      <div class="dashboard-command-shell">
        <div class="dashboard-command-list">
          <div class="dashboard-component-tabs" role="tablist" :aria-label="t('dashboard.componentHealth')">
            <button
              v-for="summary in componentSummaries"
              :key="summary.key"
              type="button"
              :class="{ active: selectedComponentTab === summary.key }"
              :data-dashboard-component-tab="summary.key"
              data-dashboard-resource-summary-tile
              role="tab"
              :aria-selected="selectedComponentTab === summary.key"
              @click="selectComponentTab(summary.key)"
            >
              <span class="dashboard-component-tab-title">
                <span>{{ summary.label }}</span>
                <strong>{{ summary.total }}</strong>
              </span>
              <span class="dashboard-component-tab-counts">
                <span class="success">{{ t('common.running') }} {{ summary.running }}</span>
                <span class="danger">{{ t('common.unavailable') }} {{ summary.unavailable }}</span>
              </span>
            </button>
          </div>
          <div class="dashboard-health-filter" role="group" :aria-label="selectedComponentTabLabel">
            <button
              v-for="filter in componentHealthFilters"
              :key="filter.key"
              type="button"
              :class="[{ active: selectedComponentHealthFilter === filter.key }, filter.className]"
              :data-dashboard-health-filter="filter.key"
              @click="selectedComponentHealthFilter = filter.key"
            >
              {{ filter.label }} {{ filter.count }}
            </button>
          </div>
          <div v-if="filteredComponentRows.length" class="dashboard-entity-table-head" aria-hidden="true">
            <span>{{ t('dashboard.resourceName') }}</span>
            <span>{{ t('dashboard.resourceMeta') }}</span>
            <span>{{ t('common.status') }}</span>
          </div>
          <el-empty v-if="!filteredComponentRows.length" :description="selectedComponentEmptyText" />
          <div v-else class="dashboard-entity-list">
            <article
              v-for="row in filteredComponentRows"
              :key="dashboardRowKey(row)"
              class="dashboard-entity-row"
              :class="{ active: activeDashboardRow && dashboardRowKey(activeDashboardRow) === dashboardRowKey(row) }"
              @click="selectDashboardRow(row)"
            >
              <div class="dashboard-entity-main">
                <span class="dashboard-entity-type" aria-hidden="true">{{ row.category }}</span>
                <strong>{{ row.title }}</strong>
                <span>{{ row.subtitle || '-' }}</span>
              </div>
              <div class="dashboard-entity-meta">
                <span v-for="item in row.meta" :key="item" class="dashboard-text-clip">{{ item }}</span>
              </div>
              <StatusTag :status="row.status" />
            </article>
          </div>
        </div>
        <aside v-if="activeDashboardRow" class="dashboard-resource-detail">
          <div class="dashboard-detail-head">
            <span class="dashboard-entity-type">{{ activeDashboardRow.category }}</span>
            <div>
              <strong>{{ activeDashboardRow.title }}</strong>
              <span>{{ activeDashboardRow.subtitle || '-' }}</span>
            </div>
            <StatusTag :status="activeDashboardRow.status" />
          </div>
          <div class="dashboard-detail-grid">
            <span v-for="item in activeDashboardRow.meta" :key="item" class="dashboard-detail-item">{{ item }}</span>
          </div>
          <router-link v-if="selectedComponentTab !== allComponentsTabKey" class="dashboard-detail-link" :to="selectedComponentLink">{{ t('dashboard.viewAllResources') }}</router-link>
        </aside>
      </div>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiGet } from '../api/client'
import { keepPreviousArrayOnLoadFailure } from '../api/resilientLoad'
import MetricGrid from '../components/MetricGrid.vue'
import StatusTag from '../components/StatusTag.vue'
import { createDashboardRealtimeRefreshScheduler, shouldRefreshDashboardForRealtimeEvent } from '../dashboard/realtimeRefresh'
import { normalizeDashboardRuntimeStatus, normalizeDashboardServerStatus } from '../dashboard/serverStatus'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { applyRealtimeStatusToServer } from '../servers/realtimeStatus'
import { useAlertsStore } from '../stores/alerts'
import { applyRealtimeStatusToAppInstance, useRealtimeStore } from '../stores/realtime'
import { useSessionStore } from '../stores/session'
import { useRoute, useRouter } from 'vue-router'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const session = useSessionStore()
const alerts = useAlertsStore()
const realtime = useRealtimeStore()
const dashboardServerPreviewLimit = 5
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const databaseInstances = ref<any[]>([])
const nacosInstances = ref<any[]>([])
const storageInstances = ref<any[]>([])
const loading = ref(false)
const now = ref('')
const allComponentsTabKey = 'all'
const defaultComponentTabKey = 'servers'
const selectedComponentTab = ref(defaultComponentTabKey)
const selectedComponentHealthFilter = ref('all')
const selectedDashboardRowKey = ref('')

const serverRows = computed(() => servers.value.map((rawServer) => {
  const server = applyRealtimeStatusToServer(rawServer, realtime.serverSnapshot(rawServer.id))
  return {
    ...server,
    status: normalizeDashboardServerStatus(server.status),
    cpu: 0,
    cpuText: '-',
    memory: 0,
    memoryText: '-',
    disk: 0,
    diskText: '-',
    diskPath: server.deployDir ?? '-',
    sampledAtText: '-'
  }
}))
const dockerRows = computed(() => servers.value
  .filter((server) => String(server.dockerHost ?? '').trim() !== '')
  .map((server) => {
    const snapshot = realtime.dockerSummarySnapshot(server.id)
    const result = snapshot?.payload ?? {}
    const summary = objectRecord(result.summary)
    const runtimeStatus = normalizeDashboardRuntimeStatus(snapshot?.status || result.status)
    return {
      ...server,
      available: Boolean(result.available) && runtimeStatus === 'running',
      containers: summary.containers ?? 0,
      images: summary.images ?? 0,
      dockerHost: summary.endpoint || result.endpoint || server.dockerHost
    }
  }))
const liveDatabaseInstances = computed(() => databaseInstances.value.map((instance) => normalizeDashboardRuntimeInstance(applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id)))))
const liveNacosInstances = computed(() => nacosInstances.value.map((instance) => normalizeDashboardRuntimeInstance(applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id)))))
const liveStorageInstances = computed(() => storageInstances.value.map((instance) => normalizeDashboardRuntimeInstance(applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id)))))
const componentTabs = computed<DashboardComponentTab[]>(() => [
  { key: 'servers', label: t('nav.servers'), empty: t('dashboard.noServers'), rows: serverComponentRows.value },
  { key: 'docker', label: 'Docker', empty: t('dashboard.noDockerHosts'), rows: dockerComponentRows.value },
  { key: 'database', label: t('nav.database'), empty: t('dashboard.noDatabaseInstances'), rows: groupRuntimeComponentRows(liveDatabaseInstances.value, t('nav.database'), 'database') },
  { key: 'nacos', label: 'Nacos', empty: t('dashboard.noNacosInstances'), rows: groupRuntimeComponentRows(liveNacosInstances.value, 'Nacos', 'nacos') },
  { key: 'storage', label: t('nav.storage'), empty: t('dashboard.noStorageInstances'), rows: groupRuntimeComponentRows(liveStorageInstances.value, t('nav.storage'), 'storage') }
])
const aggregateComponentRows = computed(() => componentTabs.value.flatMap((tab) => tab.rows))
const aggregateComponentSummary = computed(() => {
  const running = aggregateComponentRows.value.filter((row) => isRunningStatus(row.status)).length
  return {
    total: aggregateComponentRows.value.length,
    running,
    unavailable: Math.max(aggregateComponentRows.value.length - running, 0)
  }
})
const componentSummaries = computed(() => componentTabs.value.map((tab) => {
    const running = tab.rows.filter((row) => isRunningStatus(row.status)).length
    return {
      key: tab.key,
      label: tab.label,
      total: tab.rows.length,
      running,
      unavailable: Math.max(tab.rows.length - running, 0)
    }
  }))
const selectedComponent = computed<DashboardComponentTab>(() => {
  if (selectedComponentTab.value === allComponentsTabKey) {
    return {
      key: allComponentsTabKey,
      label: t('dashboard.unavailableResources'),
      empty: t('dashboard.noUnavailableResources'),
      rows: aggregateComponentRows.value
    }
  }
  return componentTabs.value.find((tab) => tab.key === selectedComponentTab.value) ?? componentTabs.value[0]
})
const selectedComponentRows = computed(() => selectedComponent.value?.rows ?? [])
const selectedComponentTabLabel = computed(() => selectedComponent.value?.label ?? '')
const selectedComponentEmptyText = computed(() => selectedComponent.value?.empty ?? '')
const selectedComponentLink = computed(() => {
  switch (selectedComponentTab.value) {
    case 'servers':
      return '/servers'
    case 'docker':
      return '/containers'
    case 'database':
      return '/database'
    case 'nacos':
      return '/nacos'
    case 'storage':
      return '/storage'
    default:
      return '/servers'
  }
})
const filteredSelectedComponentRows = computed(() => filterComponentRows(selectedComponentRows.value))
const hiddenComponentCount = computed(() => Math.max(filteredSelectedComponentRows.value.length - filteredComponentRows.value.length, 0))
const componentHealthFilters = computed(() => {
  const rows = selectedComponentRows.value
  const running = rows.filter((row) => isRunningStatus(row.status)).length
  const unavailable = Math.max(rows.length - running, 0)
  return [
    { key: 'all', label: t('common.all'), count: rows.length, className: '' },
    { key: 'running', label: t('common.running'), count: running, className: 'success' },
    { key: 'unavailable', label: t('common.unavailable'), count: unavailable, className: 'danger' }
  ]
})
const filteredComponentRows = computed(() => {
  const rows = filteredSelectedComponentRows.value
  if (selectedComponentTab.value === allComponentsTabKey) {
    return prioritizedComponentRows(rows).slice(0, dashboardServerPreviewLimit)
  }
  if (selectedComponentTab.value === 'servers') {
    return rows.slice(0, dashboardServerPreviewLimit)
  }
  return rows
})
const activeDashboardRow = computed(() => filteredComponentRows.value.find((row) => dashboardRowKey(row) === selectedDashboardRowKey.value) ?? filteredComponentRows.value[0] ?? null)
const availableServers = computed(() => serverRows.value.filter((server) => server.status === 'available').length)
const runningTasks = computed(() => tasks.value.filter((task) => task.status === 'running').length)
const runningDockerHosts = computed(() => dockerRows.value.filter((row) => row.available).length)
const runningDatabaseInstances = computed(() => liveDatabaseInstances.value.filter((instance) => isRunningStatus(instance.status)).length)
const runningStorageInstances = computed(() => liveStorageInstances.value.filter((instance) => isRunningStatus(instance.status)).length)
const canViewAlerts = computed(() => session.hasPermission(permissions.alertsView))
const kpis = computed(() => {
  const items = [
    { label: t('nav.servers'), value: servers.value.length, note: `${t('common.available')} ${availableServers.value}` },
    { label: t('toolbox.tasks'), value: tasks.value.length, note: `${t('common.running')} ${runningTasks.value}` },
    { label: 'Docker', value: dockerRows.value.length, note: `${t('common.running')} ${runningDockerHosts.value}` },
    { label: t('nav.database'), value: liveDatabaseInstances.value.length, note: `${t('common.running')} ${runningDatabaseInstances.value}` },
    { label: t('nav.storage'), value: liveStorageInstances.value.length, note: `${t('common.running')} ${runningStorageInstances.value}` }
  ]
  if (canViewAlerts.value) {
    items.splice(1, 0, {
      label: t('alerts.title'),
      value: alerts.openCount,
      note: `${t('alerts.severity.critical')} ${alerts.criticalCount}`
    })
  }
  return items
})

function filterComponentRows(rows: DashboardComponentRow[]) {
  if (selectedComponentHealthFilter.value === 'running') {
    return rows.filter((row) => isRunningStatus(row.status))
  }
  if (selectedComponentHealthFilter.value === 'unavailable') {
    return rows.filter((row) => !isRunningStatus(row.status))
  }
  return rows
}

function dashboardRowKey(row: DashboardComponentRow) {
  return `${row.categoryKey}:${row.id}`
}

function selectDashboardRow(row: DashboardComponentRow) {
  selectedDashboardRowKey.value = dashboardRowKey(row)
}

function prioritizedComponentRows<T extends { status?: string, categoryKey?: string, title?: unknown }>(rows: T[]) {
  return [...rows].sort((left, right) => componentAttentionScore(right) - componentAttentionScore(left)
    || String(left.categoryKey ?? '').localeCompare(String(right.categoryKey ?? ''))
    || String(left.title ?? '').localeCompare(String(right.title ?? ''), undefined, { numeric: true }))
}

function componentAttentionScore(row: { status?: string }) {
  return isRunningStatus(row.status) ? 0 : 100
}

function prioritizedServerRows<T extends { status?: string, dockerHost?: unknown, name?: unknown }>(rows: T[]) {
  return [...rows].sort((left, right) => serverAttentionScore(right) - serverAttentionScore(left) || String(left.name ?? '').localeCompare(String(right.name ?? ''), undefined, { numeric: true }))
}

function serverAttentionScore(server: { status?: string, dockerHost?: unknown }) {
  let score = 0
  if (server.status === 'unavailable') score += 30
  if (server.status === 'unknown') score += 20
  if (!String(server.dockerHost ?? '').trim()) score += 8
  return score
}

type DashboardLoadOptions = {
  hydrateSnapshots?: boolean
}

async function load(options: DashboardLoadOptions = {}) {
  loading.value = true
  now.value = new Date().toLocaleString()
  try {
    const [serverList, taskList, databaseList, nacosList, storageList] = await Promise.all([
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/servers'), servers.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/tasks'), tasks.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/database/instances'), databaseInstances.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/nacos/instances'), nacosInstances.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/storage/instances'), storageInstances.value)
    ])
    servers.value = serverList
    tasks.value = taskList
    databaseInstances.value = databaseList
    nacosInstances.value = nacosList
    storageInstances.value = storageList
    const loads: Array<Promise<unknown>> = [
      options.hydrateSnapshots ? realtime.loadStatusSnapshots().catch(() => false) : Promise.resolve(),
      canViewAlerts.value ? alerts.load().catch(() => undefined) : Promise.resolve()
    ]
    await Promise.all(loads)
  } finally {
    loading.value = false
  }
}

const realtimeRefresh = createDashboardRealtimeRefreshScheduler(load)

function isRunningStatus(status: unknown) {
  return normalizeDashboardRuntimeStatus(status) === 'running'
}

function normalizeDashboardRuntimeInstance<T extends { status?: unknown }>(instance: T) {
  return {
    ...instance,
    status: normalizeDashboardRuntimeStatus(instance.status)
  }
}

function selectComponentTab(key: string) {
  selectedComponentTab.value = key
  selectedComponentHealthFilter.value = key === allComponentsTabKey ? 'unavailable' : 'all'
  selectedDashboardRowKey.value = ''
  void router.replace({ query: runtimeTabQuery(key) })
}

function runtimeTabQuery(key: string) {
  const nextQuery = { ...route.query }
  if (key === allComponentsTabKey) {
    delete nextQuery.runtime
  } else {
    nextQuery.runtime = key
  }
  return nextQuery
}

function restoreComponentTabFromRoute() {
  const runtime = typeof route.query.runtime === 'string' ? route.query.runtime : ''
  if (runtime && componentTabs.value.some((tab) => tab.key === runtime)) {
    selectedComponentTab.value = runtime
    selectedComponentHealthFilter.value = 'all'
  } else {
    selectedComponentTab.value = defaultComponentTabKey
    selectedComponentHealthFilter.value = 'all'
  }
}

type DashboardComponentRow = {
  id: string
  category: string
  categoryKey: string
  title: string
  subtitle?: string
  status: string
  meta: string[]
}

type DashboardComponentTab = {
  key: string
  label: string
  empty: string
  rows: DashboardComponentRow[]
}

const serverComponentRows = computed(() => serverRows.value.map((row) => ({
  id: row.id,
  category: t('nav.servers'),
  categoryKey: 'servers',
  title: row.name,
  subtitle: row.host,
  status: row.status,
  meta: [
    `CPU ${row.cpuText}`,
    `${t('dashboard.memory')} ${row.memoryText}`,
    `${t('dashboard.disk')} ${row.diskText}`,
    `${t('dashboard.disk')} ${row.diskPath}`,
    `Docker ${row.dockerHost ? t('common.configured') : t('common.notConfigured')}`,
    `${t('dashboard.probeTime')} ${row.sampledAtText}`
  ]
})))

const dockerComponentRows = computed(() => dockerRows.value.map((row) => ({
  id: row.id,
  category: 'Docker',
  categoryKey: 'docker',
  title: row.name,
  subtitle: row.host,
  status: row.available ? 'running' : 'unavailable',
  meta: [
    `${t('containers.title')} ${row.containers}`,
    `${t('containers.images')} ${row.images}`,
    String(row.dockerHost || '-')
  ]
})))

function objectRecord(value: unknown): Record<string, any> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : {}
}

function groupRuntimeComponentRows(rows: any[], category: string, categoryKey: string): DashboardComponentRow[] {
  const groups = new Map<string, any[]>()
  for (const row of rows) {
    const key = runtimeComponentGroupKey(row)
    const members = groups.get(key) ?? []
    members.push(row)
    groups.set(key, members)
  }
  return Array.from(groups.entries()).map(([key, members]) => runtimeComponentGroupRow(key, members, category, categoryKey))
}

function runtimeComponentGroupKey(row: any) {
  const metadata = metadataOf(row)
  const app = String(row.app || '').trim().toLowerCase() || 'unknown'
  const topology = runtimeTopology(row, metadata).toLowerCase()
  const explicitGroup = stringValue(metadata.clusterId)
    || stringValue(metadata.replicationGroupId)
    || stringValue(metadata.replicaGroupId)
    || stringValue(metadata.groupId)
  if (explicitGroup) {
    return `group:${explicitGroup}`
  }
  const clusterNodes = stringList(metadata.clusterNodes).sort().join('|')
  if (clusterNodes) {
    return `nodes:${clusterNodes}`
  }
  return `${app}:single:${row.id}`
}

function runtimeComponentGroupRow(key: string, members: any[], category: string, categoryKey: string): DashboardComponentRow {
  const sorted = [...members].sort((left, right) => serverName(left.serverId).localeCompare(serverName(right.serverId)))
  const first = sorted[0] ?? {}
  const topology = runtimeGroupTopology(sorted)
  const running = sorted.filter((member) => isRunningStatus(member.status)).length
  const unavailable = Math.max(sorted.length - running, 0)
  const serversText = uniqueValues(sorted.map((member) => serverName(member.serverId)).filter((value) => value !== '-')).join(', ') || '-'
  const clusterMeta = [
    `${t('dashboard.topology')} ${topology || '-'}`,
    `${t('dashboard.clusterNodes')} ${sorted.length}`,
    `${t('dashboard.unavailableNodes')} ${unavailable}`,
    `${t('nav.servers')} ${serversText}`
  ]
  return {
    id: key,
    category,
    categoryKey,
    title: uniqueValues(sorted.map((member) => String(member.app || '').trim()).filter(Boolean)).join(' + ') || category,
    subtitle: uniqueValues(sorted.map((member) => String(member.version || '').trim()).filter(Boolean)).join(', ') || '-',
    status: unavailable > 0 ? 'unavailable' : 'running',
    meta: isClusterRuntimeGroup(sorted, topology)
      ? clusterMeta
      : [
          `${t('storage.server')} ${serverName(first.serverId)}`,
          `${t('dashboard.topology')} ${topology || '-'}`
        ]
  }
}

function runtimeGroupTopology(members: any[]) {
  return uniqueValues(members.map((member) => runtimeTopology(member, metadataOf(member))).filter(Boolean)).join(' + ')
}

function isClusterRuntimeGroup(members: any[], topology: string) {
  return members.length > 1 || isClusterTopology(topology)
}

function isClusterTopology(topology: string) {
  return /cluster|sentinel|distributed|replication|router/.test(String(topology || '').toLowerCase())
}

function runtimeTopology(row: any, metadata: Record<string, any>) {
  return String(row.topology || metadata.topology || metadata.mode || '').trim()
}

function metadataOf(row: any): Record<string, any> {
  if (row?.metadata && typeof row.metadata === 'object' && !Array.isArray(row.metadata)) {
    return row.metadata as Record<string, any>
  }
  if (typeof row?.metadata !== 'string') {
    return {}
  }
  try {
    const parsed = JSON.parse(row.metadata)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, any> : {}
  } catch {
    return {}
  }
}

function stringValue(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function stringList(value: unknown) {
  if (Array.isArray(value)) {
    return value.map((item) => String(item || '').trim()).filter(Boolean)
  }
  if (typeof value === 'string') {
    return value.split(',').map((item) => item.trim()).filter(Boolean)
  }
  return []
}

function uniqueValues(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    if (seen.has(value)) continue
    seen.add(value)
    out.push(value)
  }
  return out
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

watch(() => realtime.revision, () => {
  if (shouldRefreshDashboardForRealtimeEvent(realtime.lastEvent)) {
    realtimeRefresh.schedule()
  }
})

watch(filteredComponentRows, (rows) => {
  if (!rows.length) {
    selectedDashboardRowKey.value = ''
    return
  }
  if (!rows.some((row) => dashboardRowKey(row) === selectedDashboardRowKey.value)) {
    selectedDashboardRowKey.value = dashboardRowKey(rows[0])
  }
}, { immediate: true })

watch(() => realtime.connectedAt, () => {
  if (realtime.connected) {
    realtimeRefresh.schedule()
  }
})

onMounted(() => {
  restoreComponentTabFromRoute()
  void load({ hydrateSnapshots: true })
})
onBeforeUnmount(() => {
  realtimeRefresh.dispose()
})
</script>

<style scoped>
.dashboard-page {
  width: 100%;
  max-width: 100%;
  overflow-x: hidden;
  overflow-y: scroll;
  padding-right: 6px;
  scrollbar-gutter: stable;
  scrollbar-width: auto;
}

.dashboard-page::-webkit-scrollbar {
  width: 10px;
}

.dashboard-page::-webkit-scrollbar-thumb {
  border: 2px solid rgba(255, 255, 255, .7);
  border-radius: 999px;
  background: rgba(86, 101, 124, .42);
}

.dashboard-page::-webkit-scrollbar-track {
  background: rgba(238, 243, 249, .68);
}

.dashboard-kpis {
  margin-bottom: 0;
}

.dashboard-page :deep(.metric-grid) {
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 180px), 1fr));
}

.dashboard-section-card {
  min-width: 0;
  overflow: hidden;
}

.dashboard-section-head {
  padding: 14px 14px 12px;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  min-width: 0;
  border-bottom: 1px solid var(--aifar-border-soft);
  background: linear-gradient(180deg, #fff, #fbfdff);
}

.dashboard-section-head p {
  margin: 4px 0 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.dashboard-section-link {
  flex: 0 0 auto;
  color: var(--aifar-primary);
  font-size: 12px;
  font-weight: 600;
  text-decoration: none;
}

.dashboard-section-link:hover {
  text-decoration: underline;
}

.snapshot-bar {
  min-height: 36px;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 0 14px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: rgba(255, 255, 255, .82);
  box-shadow: var(--aifar-shadow-card);
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.snapshot-bar strong {
  color: var(--aifar-ink);
}

.dashboard-text-clip {
  min-width: 0;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.dashboard-component-health {
  display: grid;
  align-content: start;
  gap: 0;
}

.dashboard-command-shell {
  display: grid;
  grid-template-columns: minmax(620px, 1.35fr) minmax(380px, .65fr);
  gap: 0;
  min-height: 0;
  padding: 0;
  border-top: 1px solid var(--aifar-border-soft);
}

.dashboard-command-list {
  min-width: 0;
  overflow: hidden;
  border-right: 1px solid var(--aifar-border-soft);
  background: #fff;
}

.dashboard-entity-list {
  max-height: min(430px, 45vh);
  display: grid;
  gap: 0;
  min-width: 0;
  overflow: auto;
}

.dashboard-entity-table-head {
  display: grid;
  grid-template-columns: minmax(180px, .85fr) minmax(260px, 1.45fr) auto;
  gap: 12px;
  align-items: center;
  min-height: 38px;
  padding: 0 14px;
  border-top: 1px solid var(--aifar-border-soft);
  border-bottom: 1px solid var(--aifar-border-soft);
  background: #fafcff;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 700;
}

.dashboard-entity-row {
  display: grid;
  grid-template-columns: minmax(180px, .85fr) minmax(260px, 1.45fr) auto;
  gap: 12px;
  align-items: center;
  min-width: 0;
  min-height: 64px;
  padding: 10px 14px;
  border-bottom: 1px solid var(--aifar-border-soft);
  background: rgba(255, 255, 255, .94);
  cursor: pointer;
  transition: background .16s ease, box-shadow .16s ease;
}

.dashboard-entity-row:hover {
  background: #f7fbff;
}

.dashboard-entity-row.active {
  background: linear-gradient(90deg, #f0f8ff 0%, #fff 72%);
  box-shadow: inset 3px 0 0 var(--aifar-primary);
}

.dashboard-entity-type {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: fit-content;
  min-width: 0;
  height: 22px;
  padding: 0 9px;
  border: 1px solid #d6eaff;
  border-radius: 999px;
  background: #eef6ff;
  color: var(--aifar-primary);
  font-size: 12px;
  font-weight: 700;
  white-space: nowrap;
}

.dashboard-entity-main {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr);
  gap: 2px 10px;
  align-items: center;
  min-width: 0;
}

.dashboard-entity-main > span:last-child {
  grid-column: 2;
}

.dashboard-entity-main strong,
.dashboard-entity-main span {
  min-width: 0;
  overflow-wrap: anywhere;
}

.dashboard-entity-main span,
.dashboard-entity-meta {
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.dashboard-entity-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 10px;
  min-width: 0;
}

.dashboard-entity-meta span {
  min-width: 0;
  overflow-wrap: anywhere;
}

.dashboard-resource-detail {
  display: grid;
  align-content: start;
  gap: 14px;
  min-width: 0;
  min-height: 100%;
  padding: 18px 20px 20px;
  background: linear-gradient(180deg, #fbfdff, #f7fbff);
}

.dashboard-detail-head {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto;
  align-items: center;
  gap: 10px;
  min-width: 0;
}

.dashboard-detail-head strong,
.dashboard-detail-head span {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-detail-head div > span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.dashboard-detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.dashboard-detail-item {
  min-width: 0;
  min-height: 38px;
  display: flex;
  align-items: center;
  padding: 8px 10px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: rgba(255, 255, 255, .86);
  color: var(--aifar-text-secondary);
  font-size: 12px;
  overflow-wrap: anywhere;
}

.dashboard-detail-link {
  justify-self: start;
  color: var(--aifar-primary);
  font-size: 12px;
  font-weight: 700;
  text-decoration: none;
}

.dashboard-detail-link:hover {
  text-decoration: underline;
}

.dashboard-component-tabs {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px;
  min-width: 0;
  padding: 10px 12px 6px;
}

.dashboard-component-overview {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  min-width: 0;
  padding: 10px 14px;
  border: 0;
  border-bottom: 1px solid #d6eaff;
  border-radius: 0;
  background: #f7fbff;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 600;
}

.dashboard-component-overview > span {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  white-space: nowrap;
}

.dashboard-component-overview strong {
  color: var(--aifar-primary);
  font-size: 13px;
}

.dashboard-component-overview .success {
  color: #389e0d;
}

.dashboard-component-overview .danger {
  color: #cf1322;
}

.dashboard-component-tabs button {
  min-height: 30px;
  display: inline-flex;
  align-items: center;
  gap: 8px;
  flex: 0 0 auto;
  padding: 4px 12px;
  border: 0;
  border-bottom: 2px solid transparent;
  border-radius: 0;
  background: transparent;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  text-align: left;
  box-shadow: none;
}

.dashboard-component-tabs button:hover {
  background: #f4f8ff;
  color: var(--aifar-primary);
}

.dashboard-component-tabs button.active {
  border-color: var(--aifar-primary);
  background: #f4f8ff;
  color: var(--aifar-primary);
  box-shadow: none;
}

.dashboard-component-tab-title,
.dashboard-component-tab-counts {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  gap: 8px;
  min-width: 0;
}

.dashboard-component-tab-title span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-component-tab-title strong {
  min-width: 20px;
  height: 20px;
  display: inline-grid;
  place-items: center;
  padding: 0 5px;
  border-radius: 999px;
  background: rgba(22, 119, 255, .1);
  font-size: 11px;
}

.dashboard-component-tab-counts {
  display: none;
  justify-content: flex-start;
  flex-wrap: wrap;
  gap: 6px;
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.dashboard-component-tab-counts span {
  display: inline-flex;
  align-items: center;
  height: 22px;
  padding: 0 7px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: 999px;
  background: #fff;
  white-space: nowrap;
}

.dashboard-component-tab-counts .success {
  border-color: #b7eb8f;
  background: #f6ffed;
  color: #389e0d;
}

.dashboard-component-tab-counts .danger {
  border-color: #ffccc7;
  background: #fff2f0;
  color: #cf1322;
}

.dashboard-health-filter {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  padding: 6px 12px 10px;
}

.dashboard-health-filter button {
  height: 28px;
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  padding: 3px 10px;
  border: 1px solid var(--aifar-border);
  border-radius: 999px;
  background: #fff;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
}

.dashboard-health-filter button:hover {
  border-color: #b9d6ff;
  color: var(--aifar-primary);
}

.dashboard-health-filter button.active {
  border-color: #91caff;
  background: #e6f4ff;
  color: var(--aifar-primary);
}

.dashboard-health-filter button.success {
  border-color: #b7eb8f;
  background: #f6ffed;
  color: #389e0d;
}

.dashboard-health-filter button.danger {
  border-color: #ffccc7;
  background: #fff2f0;
  color: #cf1322;
}

@media (max-width: 860px) {
  .dashboard-command-shell {
    grid-template-columns: 1fr;
  }

  .dashboard-component-tabs {
    overflow-x: auto;
    flex-wrap: nowrap;
  }

  .dashboard-entity-table-head {
    display: none;
  }

  .dashboard-entity-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .dashboard-entity-meta {
    grid-column: 1 / -1;
  }

  .dashboard-entity-row :deep(.el-tag) {
    justify-self: start;
  }
}
</style>
