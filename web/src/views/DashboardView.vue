<template>
  <section class="dashboard-page">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('dashboard.title') }}</h1>
        <p class="page-subtitle">{{ t('dashboard.subtitle') }}</p>
      </div>
      <el-button @click="load">{{ t('common.refresh') }}</el-button>
    </div>

    <div class="snapshot-bar">
      <span>{{ t('dashboard.snapshot') }}</span>
      <strong>{{ now }}</strong>
    </div>

    <div class="metric-grid dashboard-kpis">
      <div class="metric-card">
        <div class="label">{{ t('nav.servers') }}</div>
        <div class="value">{{ servers.length }}</div>
        <div class="subtle-note">{{ t('common.available') }} {{ availableServers }}</div>
      </div>
      <div class="metric-card">
        <div class="label">{{ t('toolbox.tasks') }}</div>
        <div class="value">{{ tasks.length }}</div>
        <div class="subtle-note">{{ t('common.running') }} {{ runningTasks }}</div>
      </div>
      <div class="metric-card">
        <div class="label">{{ t('nav.database') }}</div>
        <div class="value">{{ databaseInstances.length }}</div>
        <div class="subtle-note">{{ t('dashboard.databaseStatus') }}</div>
      </div>
    </div>

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
            <div class="mini-progress"><span :style="{ width: metricWidth(row, 13) }" /></div>
            <div class="subtle-note">{{ row.cpu ?? '0.0%' }}</div>
          </template>
        </el-table-column>
        <el-table-column :label="t('dashboard.memory')" width="170">
          <template #default="{ row }">
            <div class="mini-progress"><span :style="{ width: metricWidth(row, 34) }" /></div>
            <div class="subtle-note">{{ row.memory ?? '-' }}</div>
          </template>
        </el-table-column>
        <el-table-column :label="t('dashboard.disk')" width="170">
          <template #default="{ row }">
            <div class="mini-progress"><span :style="{ width: metricWidth(row, 42) }" /></div>
            <div class="subtle-note">{{ row.disk ?? row.deployDir }}</div>
          </template>
        </el-table-column>
        <el-table-column label="Docker" width="150">
          <template #default="{ row }">
            <span class="status-pill">{{ row.dockerHost ? t('common.configured') : t('common.notConfigured') }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('dashboard.probeTime')" width="190">
          <template #default>{{ now }}</template>
        </el-table-column>
      </el-table>
    </div>

    <div class="dashboard-split">
      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.dockerStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ servers.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ runningTasks }}</span>
          <span class="status-pill">{{ t('common.stopped') }} {{ Math.max(servers.length - runningTasks, 0) }}</span>
        </div>
        <el-table :data="serverRows" :empty-text="t('dashboard.noDockerHosts')">
          <el-table-column prop="name" :label="t('dashboard.server')" />
          <el-table-column prop="host" :label="t('servers.host')" />
          <el-table-column :label="t('common.status')"><template #default="{ row }"><StatusTag :status="row.status" /></template></el-table-column>
          <el-table-column prop="dockerHost" :label="t('common.endpoint')" />
        </el-table>
      </div>

      <div class="workspace-card">
        <h2 class="section-title">{{ t('dashboard.databaseStatus') }}</h2>
        <div class="filter-row">
          <span class="status-pill">{{ t('common.all') }} {{ databaseInstances.length }}</span>
          <span class="status-pill success">{{ t('common.running') }} {{ databaseInstances.length }}</span>
        </div>
        <el-table :data="databaseInstances" :empty-text="t('dashboard.noDatabaseInstances')">
          <el-table-column prop="app" :label="t('dashboard.instance')" />
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
import StatusTag from '../components/StatusTag.vue'
import { useI18n } from '../i18n'

const { t } = useI18n()
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const databaseInstances = ref<any[]>([])
const now = ref('')

const serverRows = computed(() => servers.value.map((server) => ({
  ...server,
  cpu: server.cpu ?? '0.0%',
  memory: server.memory ?? '-',
  disk: server.disk ?? server.deployDir ?? '-'
})))
const availableServers = computed(() => servers.value.filter((server) => server.status === 'available').length)
const runningTasks = computed(() => tasks.value.filter((task) => task.status === 'running').length)

function metricWidth(_row: any, fallback: number) {
  return `${fallback}%`
}
async function load() {
  now.value = new Date().toLocaleString()
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  tasks.value = asArray(await apiGet<any[] | null>('/tasks').catch(() => []))
  databaseInstances.value = asArray(await apiGet<any[] | null>('/database/instances').catch(() => []))
}
onMounted(load)
</script>

<style scoped>
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
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
  margin-top: 0;
}

.dashboard-split > .workspace-card {
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.dashboard-split :deep(.el-table) {
  flex: 1 1 auto;
}

@media (max-width: 1200px) {
  .dashboard-split {
    grid-template-columns: 1fr;
  }
}
</style>
