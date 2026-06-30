<template>
  <section class="dashboard-page">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('dashboard.title') }}</h1>
        <p class="page-subtitle">{{ t('dashboard.subtitle') }}</p>
      </div>
      <el-button :loading="loading" @click="load">{{ t('common.refresh') }}</el-button>
    </div>

    <div class="snapshot-bar">
      <span>{{ t('dashboard.snapshot') }}</span>
      <strong>{{ now }}</strong>
    </div>

    <MetricGrid :items="kpis" class="dashboard-kpis" />

    <div class="workspace-card">
      <h2 class="section-title">{{ t('dashboard.serverMetrics') }}</h2>
      <el-table :data="serverRows" :empty-text="t('dashboard.noServers')">
        <el-table-column :label="t('dashboard.server')" min-width="220">
          <template #default="{ row }">
            <strong>{{ row.name }}</strong>
            <div class="subtle-note">{{ row.host }}</div>
            <span class="status-pill success" v-if="row.status === 'available'">{{ t('common.available') }}</span>
            <span class="status-pill warning" v-else>{{ row.status || t('common.unknown') }}</span>
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
          <span class="status-pill">{{ t('common.stopped') }} {{ Math.max(dockerRows.length - runningDockerHosts, 0) }}</span>
        </div>
        <el-table :data="dockerRows" :empty-text="t('dashboard.noDockerHosts')">
          <el-table-column prop="name" :label="t('dashboard.server')" />
          <el-table-column prop="host" :label="t('servers.host')" />
          <el-table-column :label="t('common.status')"><template #default="{ row }"><StatusTag :status="row.available ? 'running' : 'failed'" /></template></el-table-column>
          <el-table-column prop="containers" :label="t('containers.title')" width="110" />
          <el-table-column prop="images" :label="t('containers.images')" width="110" />
          <el-table-column prop="dockerHost" :label="t('common.endpoint')" />
        </el-table>
      </div>

      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.databaseStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ databaseInstances.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ runningDatabaseInstances }}</span>
        </div>
        <el-table :data="databaseInstances" :empty-text="t('dashboard.noDatabaseInstances')">
          <el-table-column prop="app" :label="t('dashboard.instance')" />
          <el-table-column prop="version" :label="t('common.version')" />
          <el-table-column prop="topology" :label="t('dashboard.topology')" />
          <el-table-column prop="status" :label="t('common.status')"><template #default="{ row }"><StatusTag :status="row.status" /></template></el-table-column>
        </el-table>
      </div>

      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.storageStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ storageInstances.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ runningStorageInstances }}</span>
        </div>
        <el-table :data="storageInstances" :empty-text="t('dashboard.noStorageInstances')">
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
import { computed, onMounted, ref } from 'vue'
import { apiGet, asArray } from '../api/client'
import MetricGrid from '../components/MetricGrid.vue'
import StatusTag from '../components/StatusTag.vue'
import { useI18n } from '../i18n'

const { t } = useI18n()
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const databaseInstances = ref<any[]>([])
const storageInstances = ref<any[]>([])
const telemetryByServer = ref<Record<string, any>>({})
const dockerByServer = ref<Record<string, any>>({})
const loading = ref(false)
const now = ref('')

const serverRows = computed(() => servers.value.map((server) => ({
  ...server,
  cpu: telemetryByServer.value[server.id]?.cpu ?? 0,
  cpuText: telemetryByServer.value[server.id]?.cpuText ?? '-',
  memory: telemetryByServer.value[server.id]?.memory ?? 0,
  memoryText: telemetryByServer.value[server.id]?.memoryText ?? '-',
  disk: telemetryByServer.value[server.id]?.disk ?? 0,
  diskText: telemetryByServer.value[server.id]?.diskText ?? '-',
  diskPath: telemetryByServer.value[server.id]?.diskPath ?? server.deployDir ?? '-',
  sampledAtText: formatTime(telemetryByServer.value[server.id]?.sampledAt)
})))
const dockerRows = computed(() => servers.value
  .filter((server) => String(server.dockerHost ?? '').trim() !== '')
  .map((server) => {
    const result = dockerByServer.value[server.id] ?? {}
    const summary = result.summary ?? {}
    return {
      ...server,
      available: Boolean(result.available),
      containers: summary.containers ?? 0,
      images: summary.images ?? 0,
      dockerHost: summary.endpoint || server.dockerHost
    }
  }))
const availableServers = computed(() => servers.value.filter((server) => server.status === 'available').length)
const runningTasks = computed(() => tasks.value.filter((task) => task.status === 'running').length)
const runningDockerHosts = computed(() => dockerRows.value.filter((row) => row.available).length)
const runningDatabaseInstances = computed(() => databaseInstances.value.filter((instance) => isRunningStatus(instance.status)).length)
const runningStorageInstances = computed(() => storageInstances.value.filter((instance) => isRunningStatus(instance.status)).length)
const kpis = computed(() => [
  { label: t('nav.servers'), value: servers.value.length, note: `${t('common.available')} ${availableServers.value}` },
  { label: t('toolbox.tasks'), value: tasks.value.length, note: `${t('common.running')} ${runningTasks.value}` },
  { label: 'Docker', value: dockerRows.value.length, note: `${t('common.running')} ${runningDockerHosts.value}` },
  { label: t('nav.database'), value: databaseInstances.value.length, note: `${t('common.running')} ${runningDatabaseInstances.value}` },
  { label: t('nav.storage'), value: storageInstances.value.length, note: `${t('common.running')} ${runningStorageInstances.value}` }
])

function metricWidth(value: unknown) {
  const n = Number(value)
  if (!Number.isFinite(n) || n <= 0) return '0%'
  return `${Math.min(Math.max(n, 0), 100)}%`
}

async function load() {
  loading.value = true
  now.value = new Date().toLocaleString()
  try {
    const [serverList, taskList, databaseList, storageList] = await Promise.all([
      apiGet<any[] | null>('/servers').catch(() => []),
      apiGet<any[] | null>('/tasks').catch(() => []),
      apiGet<any[] | null>('/database/instances').catch(() => []),
      apiGet<any[] | null>('/storage/instances').catch(() => [])
    ])
    servers.value = asArray(serverList)
    tasks.value = asArray(taskList)
    databaseInstances.value = asArray(databaseList)
    storageInstances.value = asArray(storageList)
    await Promise.all([loadTelemetry(), loadDockerStatus()])
  } finally {
    loading.value = false
  }
}

async function loadTelemetry() {
  const entries = await Promise.all(servers.value.map(async (server) => {
    const telemetry = await apiGet<any>(`/servers/${encodeURIComponent(server.id)}/telemetry`).catch(() => null)
    return [server.id, telemetry] as const
  }))
  telemetryByServer.value = Object.fromEntries(entries.filter(([, telemetry]) => Boolean(telemetry)))
}

async function loadDockerStatus() {
  const dockerServers = servers.value.filter((server) => String(server.dockerHost ?? '').trim() !== '')
  const entries = await Promise.all(dockerServers.map(async (server) => {
    const result = await apiGet<any>(`/containers/summary?serverId=${encodeURIComponent(server.id)}`).catch((err) => ({
      available: false,
      error: err?.message ?? String(err)
    }))
    return [server.id, result] as const
  }))
  dockerByServer.value = Object.fromEntries(entries)
}

function isRunningStatus(status: unknown) {
  return ['running', 'available', 'success'].includes(String(status ?? '').toLowerCase())
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

onMounted(load)
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
}
</style>
