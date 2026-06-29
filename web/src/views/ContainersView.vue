<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('containers.title') }}</h1>
        <p class="page-subtitle">{{ t('containers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-select v-model="selectedServerId" :placeholder="t('terminal.server')" clearable class="toolbar-control">
          <el-option v-for="server in servers" :key="server.id" :label="serverLabel(server)" :value="server.id" />
        </el-select>
        <el-input v-model="dockerHost" :disabled="Boolean(selectedServerId)" :placeholder="t('containers.localDocker')" clearable class="toolbar-control" />
        <el-button @click="load">{{ t('containers.checkHost') }}</el-button>
        <el-button @click="loadActive">{{ t('common.refresh') }}</el-button>
      </div>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('containers.overview')" name="overview" />
      <el-tab-pane :label="t('containers.title')" name="containers" />
      <el-tab-pane :label="t('containers.images')" name="images" />
      <el-tab-pane :label="t('containers.network')" name="networks" />
      <el-tab-pane :label="t('containers.volumes')" name="volumes" />
      <el-tab-pane :label="t('containers.registry')" name="registry" />
      <el-tab-pane :label="t('containers.settings')" name="settings" />
    </el-tabs>

    <el-alert v-if="error" :title="errorTitle" :description="error" type="warning" :closable="false" show-icon />
    <div class="muted-strip" v-if="!summary.available">{{ t('containers.disabledHint') }}</div>

    <div class="workspace-card containers-main">
      <template v-if="tab === 'overview'">
        <div class="container-metrics">
          <div v-for="item in metrics" :key="item.label" class="metric-card">
            <div class="label">{{ item.label }}</div>
            <div class="value">{{ item.value }}</div>
            <div class="subtle-note">{{ item.note }}</div>
          </div>
        </div>

        <div class="sub-panel">
          <h2 class="section-title">{{ t('containers.diskUsage') }}</h2>
          <div class="disk-grid">
            <div v-for="item in normalizedDiskUsage" :key="item.type">
              <strong>{{ item.type }}</strong>
              <span>{{ item.size }}</span>
              <small>{{ t('containers.reclaimable') }} {{ item.reclaimable || '-' }}</small>
            </div>
          </div>
        </div>

        <div class="sub-panel">
          <h2 class="section-title">{{ t('containers.configSummary') }}</h2>
          <div class="kv-grid">
            <div class="key">{{ t('containers.dockerHost') }}</div><div>{{ targetLabel }}</div>
            <div class="key">{{ t('containers.endpoint') }}</div><div>{{ summaryData.endpoint || '-' }}</div>
            <div class="key">{{ t('containers.serverVersion') }}</div><div>{{ summaryData.version || '-' }}</div>
            <div class="key">{{ t('containers.driver') }}</div><div>{{ summaryData.driver || '-' }}</div>
            <div class="key">{{ t('containers.rootDir') }}</div><div>{{ summaryData.rootDir || '-' }}</div>
            <div class="key">{{ t('common.status') }}</div><div><StatusTag :status="summary.available ? 'available' : 'failed'" /></div>
          </div>
        </div>
      </template>

      <template v-else-if="tab === 'containers'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="name" :label="t('containers.name')" min-width="160" show-overflow-tooltip />
          <el-table-column prop="image" :label="t('containers.image')" min-width="190" show-overflow-tooltip />
          <el-table-column prop="state" :label="t('common.status')" width="120">
            <template #default="{ row }"><StatusTag :status="row.state === 'running' ? 'running' : 'stopped'" /></template>
          </el-table-column>
          <el-table-column prop="ports" :label="t('containers.ports')" min-width="180" show-overflow-tooltip />
          <el-table-column prop="networks" :label="t('containers.network')" min-width="140" show-overflow-tooltip />
          <el-table-column prop="createdAt" :label="t('containers.created')" min-width="170" show-overflow-tooltip />
          <el-table-column :label="t('common.operation')" width="280" fixed="right">
            <template #default="{ row }">
              <div class="row-actions">
                <el-button size="small" @click="runContainerAction(row.id, 'start')">{{ t('containers.start') }}</el-button>
                <el-button size="small" @click="runContainerAction(row.id, 'stop')">{{ t('containers.stop') }}</el-button>
                <el-button size="small" @click="runContainerAction(row.id, 'restart')">{{ t('containers.restart') }}</el-button>
                <el-button size="small" @click="openLogs(row.id)">{{ t('containers.logs') }}</el-button>
              </div>
            </template>
          </el-table-column>
        </el-table>
      </template>

      <template v-else-if="tab === 'images'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="repository" :label="t('containers.repository')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="tag" :label="t('containers.tag')" width="140" show-overflow-tooltip />
          <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
          <el-table-column prop="size" :label="t('containers.size')" width="120" />
          <el-table-column prop="digest" :label="t('containers.digest')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="createdAt" :label="t('containers.created')" min-width="170" show-overflow-tooltip />
        </el-table>
      </template>

      <template v-else-if="tab === 'networks'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="name" :label="t('containers.name')" min-width="180" />
          <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
          <el-table-column prop="driver" :label="t('containers.driver')" min-width="150" />
          <el-table-column prop="scope" :label="t('containers.scope')" min-width="120" />
        </el-table>
      </template>

      <template v-else-if="tab === 'volumes'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="name" :label="t('containers.name')" min-width="180" show-overflow-tooltip />
          <el-table-column prop="driver" :label="t('containers.driver')" width="140" />
          <el-table-column prop="scope" :label="t('containers.scope')" width="120" />
          <el-table-column prop="mountpoint" :label="t('containers.mountpoint')" min-width="260" show-overflow-tooltip />
          <el-table-column prop="size" :label="t('containers.size')" width="120" />
        </el-table>
      </template>

      <template v-else-if="tab === 'registry'">
        <div class="empty-state">
          <div>
            <strong>{{ t('containers.registry') }}</strong>
            <span>{{ t('containers.registryHint') }}</span>
          </div>
        </div>
      </template>

      <template v-else>
        <div class="settings-grid">
          <div class="kv-grid">
            <div class="key">{{ t('containers.dockerHost') }}</div><div>{{ targetLabel }}</div>
            <div class="key">{{ t('containers.endpoint') }}</div><div>{{ summaryData.endpoint || '-' }}</div>
            <div class="key">{{ t('containers.rootDir') }}</div><div>{{ summaryData.rootDir || '-' }}</div>
            <div class="key">{{ t('common.provider') }}</div><div>{{ t('common.real') }}</div>
          </div>
          <p class="muted-strip">{{ t('containers.settingsHint') }}</p>
        </div>
      </template>
    </div>

    <el-drawer v-model="logsVisible" :title="t('containers.logs')" size="52%">
      <pre class="terminal-box logs-box">{{ logsText || t('tasks.noLogs') }}</pre>
    </el-drawer>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { apiGet, apiPost, asArray } from '../api/client'
import StatusTag from '../components/StatusTag.vue'
import { useI18n } from '../i18n'

type DockerSummaryResponse = {
  available?: boolean
  error?: string
  summary?: Record<string, any>
  diskUsage?: Array<Record<string, string>>
}

const { t } = useI18n()
const dockerHost = ref('')
const selectedServerId = ref('')
const servers = ref<any[]>([])
const summary = ref<DockerSummaryResponse>({})
const collection = ref<any[]>([])
const error = ref('')
const tab = ref('overview')
const logsVisible = ref(false)
const logsText = ref('')

const summaryData = computed(() => summary.value.summary ?? {})
const selectedServer = computed(() => servers.value.find((server) => server.id === selectedServerId.value) ?? null)
const targetLabel = computed(() => selectedServer.value ? serverLabel(selectedServer.value) : dockerHost.value || t('containers.localDocker'))
const errorTitle = computed(() => summary.value.available === false ? t('containers.notAvailable') : t('containers.checkFailed'))
const metrics = computed(() => [
  { label: t('containers.title'), value: summaryData.value.containers ?? 0, note: t('containers.runningCount', { count: summaryData.value.running ?? 0 }) },
  { label: t('containers.images'), value: summaryData.value.images ?? 0, note: t('containers.localImages') },
  { label: t('containers.network'), value: summaryData.value.networks ?? 0, note: t('containers.network') },
  { label: t('containers.volumes'), value: summaryData.value.volumes ?? 0, note: t('containers.volumes') },
  { label: t('containers.registry'), value: 0, note: t('containers.controlPlaneData') }
])
const normalizedDiskUsage = computed(() => {
  const rows = asArray<Record<string, string>>(summary.value.diskUsage)
  if (rows.length) return rows
  return [
    { type: t('containers.images'), size: '0 B', reclaimable: '-' },
    { type: t('containers.title'), size: String(summaryData.value.containers ?? 0), reclaimable: '-' },
    { type: t('containers.volumes'), size: '0 B', reclaimable: '-' },
    { type: t('containers.buildCache'), size: '0 B', reclaimable: '-' }
  ]
})

function targetQuery() {
  if (selectedServerId.value) {
    return `serverId=${encodeURIComponent(selectedServerId.value)}`
  }
  return `dockerHost=${encodeURIComponent(dockerHost.value)}`
}

function serverLabel(server: any) {
  if (!server) {
    return ''
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

async function loadServers() {
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
}

async function load() {
  error.value = ''
  summary.value = await apiGet<DockerSummaryResponse>(`/containers/summary?${targetQuery()}`).catch((err) => {
    error.value = err.message
    return { available: false, error: err.message }
  })
  if (summary.value.available === false && summary.value.error) error.value = summary.value.error
  if (tab.value !== 'overview') {
    await loadCollection()
  }
}

async function loadCollection() {
  if (tab.value === 'overview' || tab.value === 'registry' || tab.value === 'settings') {
    collection.value = []
    return
  }
  collection.value = asArray(await apiGet(`/containers?kind=${tab.value}&${targetQuery()}`).catch((err) => {
    error.value = err.message
    return []
  }))
}

async function loadActive() {
  if (tab.value === 'overview') {
    await load()
  } else {
    await loadCollection()
  }
}

async function runContainerAction(id: string, action: string) {
  await apiPost(`/containers/${encodeURIComponent(id)}/${action}?${targetQuery()}`)
  ElMessage.success(t('containers.actionAccepted'))
  setTimeout(loadCollection, 800)
}

async function openLogs(id: string) {
  const result = await apiGet<{ logs?: string[] }>(`/containers/${encodeURIComponent(id)}/logs?tail=300&${targetQuery()}`)
  logsText.value = asArray<string>(result.logs).join('\n')
  logsVisible.value = true
}

watch(tab, loadActive)
watch(selectedServerId, load)
onMounted(async () => {
  await loadServers()
  await load()
})
</script>

<style scoped>
.containers-main {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  padding: 10px;
}

.container-metrics {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 12px;
  margin: 0;
}

.sub-panel {
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  overflow: hidden;
}

.disk-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
  padding: 12px;
}

.disk-grid div {
  min-height: 64px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f7fbff;
  display: grid;
  place-items: center;
  color: var(--aifar-text-secondary);
}

.disk-grid strong {
  color: var(--aifar-ink);
}

.disk-grid small {
  font-size: 11px;
  color: var(--aifar-text-tertiary);
}

.row-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.logs-box {
  height: calc(100vh - 120px);
  overflow: auto;
}

.settings-grid {
  display: grid;
  gap: 12px;
}

@media (max-width: 1100px) {
  .container-metrics,
  .disk-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .container-metrics,
  .disk-grid {
    grid-template-columns: 1fr;
  }
}
</style>
