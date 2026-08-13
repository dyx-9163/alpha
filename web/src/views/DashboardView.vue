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

    <div v-if="canViewAlerts" class="workspace-card dashboard-alerts">
      <div class="dashboard-alerts-head">
        <div>
          <h2 class="section-title">{{ t('dashboard.alertsTitle') }}</h2>
          <p>{{ t('dashboard.alertsHint') }}</p>
        </div>
        <el-button text @click="openAlertCenter">{{ t('alerts.title') }}</el-button>
      </div>
      <div class="filter-row">
        <span class="status-pill danger">{{ t('alerts.severity.critical') }} {{ alerts.criticalCount }}</span>
        <span class="status-pill warning">{{ t('alerts.severity.warning') }} {{ alerts.warningCount }}</span>
        <span class="status-pill">{{ t('common.all') }} {{ alerts.openCount }}</span>
      </div>
      <el-empty v-if="!dashboardAlerts.length" :description="t('dashboard.noAlerts')" />
      <div v-else class="dashboard-alert-list">
        <button v-for="alert in dashboardAlerts" :key="alert.id" class="dashboard-alert-row" @click="openAlertCenter">
          <el-tag :type="alertSeverityType(alert.severity)" size="small" effect="light">{{ severityLabel(alert.severity) }}</el-tag>
          <strong>{{ alert.title }}</strong>
          <span>{{ alertScope(alert) }}</span>
          <span>{{ formatTime(alert.lastSeenAt || alert.updatedAt) }}</span>
        </button>
      </div>
    </div>

    <div class="workspace-card">
      <h2 class="section-title">{{ t('dashboard.serverMetrics') }}</h2>
      <el-table :data="serverRows" :empty-text="t('dashboard.noServers')">
        <el-table-column :label="t('dashboard.server')" min-width="220">
          <template #default="{ row }">
            <strong>{{ row.name }}</strong>
            <div class="subtle-note">{{ row.host }}</div>
            <StatusTag :status="row.status" />
          </template>
        </el-table-column>
        <el-table-column label="CPU" width="160">
          <template #default="{ row }">
            <div class="mini-progress"><span :style="{ width: metricWidth(row.cpu) }" /></div>
            <div class="subtle-note">{{ row.cpuText }}</div>
          </template>
        </el-table-column>
        <el-table-column :label="t('dashboard.memory')" width="170">
          <template #default="{ row }">
            <div class="mini-progress"><span :style="{ width: metricWidth(row.memory) }" /></div>
            <div class="subtle-note">{{ row.memoryText }}</div>
          </template>
        </el-table-column>
        <el-table-column :label="t('dashboard.disk')" width="170">
          <template #default="{ row }">
            <div class="mini-progress"><span :style="{ width: metricWidth(row.disk) }" /></div>
            <div class="subtle-note">{{ row.diskText }}</div>
            <div class="subtle-note">{{ row.diskPath }}</div>
          </template>
        </el-table-column>
        <el-table-column label="Docker" width="150">
          <template #default="{ row }">
            <span class="status-pill">{{ row.dockerHost ? t('common.configured') : t('common.notConfigured') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('dashboard.probeTime')" width="190">
          <template #default="{ row }">{{ row.sampledAtText }}</template>
        </el-table-column>
      </el-table>
    </div>

    <div class="dashboard-split">
      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.dockerStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ dockerRows.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ runningDockerHosts }}</span>
          <span class="status-pill">{{ t('common.unavailable') }} {{ Math.max(dockerRows.length - runningDockerHosts, 0) }}</span>
        </div>
        <el-table :data="dockerRows" :empty-text="t('dashboard.noDockerHosts')">
          <el-table-column prop="name" :label="t('dashboard.server')" />
          <el-table-column prop="host" :label="t('servers.host')" />
          <el-table-column :label="t('common.status')"><template #default="{ row }"><StatusTag :status="row.available ? 'running' : 'unavailable'" /></template></el-table-column>
          <el-table-column prop="containers" :label="t('containers.title')" width="110" />
          <el-table-column prop="images" :label="t('containers.images')" width="110" />
          <el-table-column prop="dockerHost" :label="t('common.endpoint')" />
        </el-table>
      </div>

      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.databaseStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ liveDatabaseInstances.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ runningDatabaseInstances }}</span>
        </div>
        <el-table :data="liveDatabaseInstances" :empty-text="t('dashboard.noDatabaseInstances')">
          <el-table-column prop="app" :label="t('dashboard.instance')" />
          <el-table-column prop="version" :label="t('common.version')" />
          <el-table-column prop="topology" :label="t('dashboard.topology')" />
          <el-table-column prop="status" :label="t('common.status')"><template #default="{ row }"><StatusTag :status="row.status" /></template></el-table-column>
        </el-table>
      </div>

      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.storageStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ liveStorageInstances.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ runningStorageInstances }}</span>
        </div>
        <el-table :data="liveStorageInstances" :empty-text="t('dashboard.noStorageInstances')">
          <el-table-column prop="app" :label="t('dashboard.instance')" />
          <el-table-column :label="t('storage.server')" min-width="150"><template #default="{ row }">{{ serverName(row.serverId) }}</template></el-table-column>
          <el-table-column prop="version" :label="t('common.version')" />
          <el-table-column prop="topology" :label="t('dashboard.topology')" />
          <el-table-column prop="status" :label="t('common.status')"><template #default="{ row }"><StatusTag :status="row.status" /></template></el-table-column>
        </el-table>
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
import { useAlertsStore, type AlertItem } from '../stores/alerts'
import { applyRealtimeStatusToAppInstance, useRealtimeStore } from '../stores/realtime'
import { useSessionStore } from '../stores/session'

const { t } = useI18n()
const session = useSessionStore()
const alerts = useAlertsStore()
const realtime = useRealtimeStore()
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const databaseInstances = ref<any[]>([])
const storageInstances = ref<any[]>([])
const loading = ref(false)
const now = ref('')

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
const liveStorageInstances = computed(() => storageInstances.value.map((instance) => normalizeDashboardRuntimeInstance(applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id)))))
const availableServers = computed(() => serverRows.value.filter((server) => server.status === 'available').length)
const runningTasks = computed(() => tasks.value.filter((task) => task.status === 'running').length)
const runningDockerHosts = computed(() => dockerRows.value.filter((row) => row.available).length)
const runningDatabaseInstances = computed(() => liveDatabaseInstances.value.filter((instance) => isRunningStatus(instance.status)).length)
const runningStorageInstances = computed(() => liveStorageInstances.value.filter((instance) => isRunningStatus(instance.status)).length)
const canViewAlerts = computed(() => session.hasPermission(permissions.alertsView))
const dashboardAlerts = computed(() => alerts.openAlerts.slice(0, 5))
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

function metricWidth(value: unknown) {
  const n = Number(value)
  if (!Number.isFinite(n) || n <= 0) return '0%'
  return `${Math.min(Math.max(n, 0), 100)}%`
}

type DashboardLoadOptions = {
  hydrateSnapshots?: boolean
}

async function load(options: DashboardLoadOptions = {}) {
  loading.value = true
  now.value = new Date().toLocaleString()
  try {
    const [serverList, taskList, databaseList, storageList] = await Promise.all([
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/servers'), servers.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/tasks'), tasks.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/database/instances'), databaseInstances.value),
      keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/storage/instances'), storageInstances.value)
    ])
    servers.value = serverList
    tasks.value = taskList
    databaseInstances.value = databaseList
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

function objectRecord(value: unknown): Record<string, any> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : {}
}

function formatTime(value: unknown) {
  if (!value) return '-'
  const date = new Date(String(value))
  return Number.isNaN(date.getTime()) ? '-' : date.toLocaleString()
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function openAlertCenter() {
  alerts.openDrawer()
}

function alertSeverityType(severity: string) {
  if (severity === 'critical') return 'danger'
  if (severity === 'warning') return 'warning'
  return 'info'
}

function severityLabel(severity: string) {
  return t(`alerts.severity.${severity}`)
}

function alertScope(alert: AlertItem) {
  return alert.app || alert.scope || t('common.unknown')
}

watch(() => realtime.revision, () => {
  if (shouldRefreshDashboardForRealtimeEvent(realtime.lastEvent)) {
    realtimeRefresh.schedule()
  }
})

watch(() => realtime.connectedAt, () => {
  if (realtime.connected) {
    realtimeRefresh.schedule()
  }
})

onMounted(() => {
  void load({ hydrateSnapshots: true })
})
onBeforeUnmount(() => {
  realtimeRefresh.dispose()
})
</script>

<style scoped>
.dashboard-page {
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

.dashboard-alerts {
  display: grid;
  gap: 10px;
}

.dashboard-alerts-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.dashboard-alerts-head p {
  margin: 4px 0 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.dashboard-alert-list {
  display: grid;
  gap: 8px;
}

.dashboard-alert-row {
  display: grid;
  grid-template-columns: 78px minmax(0, 1fr) 96px 150px;
  align-items: center;
  gap: 10px;
  min-height: 42px;
  width: 100%;
  padding: 8px 10px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-md);
  background: #fff;
  color: var(--aifar-text-secondary);
  text-align: left;
  cursor: pointer;
}

.dashboard-alert-row:hover {
  border-color: #b9d6ff;
  background: #f8fbff;
}

.dashboard-alert-row strong,
.dashboard-alert-row span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.dashboard-alert-row strong {
  color: var(--aifar-ink);
  font-size: 13px;
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

.dashboard-split {
  flex: 0 0 auto;
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 360px), 1fr));
  gap: 12px;
  margin-top: 0;
  min-width: 0;
  overflow: visible;
}

.dashboard-split > .workspace-card {
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.dashboard-split :deep(.el-table) {
  flex: 0 0 auto;
}

@media (max-width: 1200px) {
  .dashboard-split {
    grid-template-columns: 1fr;
  }

  .dashboard-alert-row {
    grid-template-columns: 78px minmax(0, 1fr);
  }
}
</style>
