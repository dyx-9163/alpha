<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('containers.title') }}</h1>
        <p class="page-subtitle">{{ t('containers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <ServerSelector v-model="selectedServerId" :servers="dockerServers" :placeholder="t('containers.selectDockerHost')" class="toolbar-control" />
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
        <MetricGrid :items="metrics" />

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
          <KeyValueGrid :items="configSummaryItems" />
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
                <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                  <span><el-button size="small" :disabled="!canManageContainers" @click="runContainerAction(row.id, 'start')">{{ t('containers.start') }}</el-button></span>
                </el-tooltip>
                <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                  <span><el-button size="small" :disabled="!canManageContainers" @click="runContainerAction(row.id, 'stop')">{{ t('containers.stop') }}</el-button></span>
                </el-tooltip>
                <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                  <span><el-button size="small" :disabled="!canManageContainers" @click="runContainerAction(row.id, 'restart')">{{ t('containers.restart') }}</el-button></span>
                </el-tooltip>
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
          <KeyValueGrid :items="settingsItems" />
          <p class="muted-strip">{{ t('containers.settingsHint') }}</p>
        </div>
      </template>
    </div>

    <LogDrawer v-model="logsVisible" :title="t('containers.logs')" :text="logsText" :empty-text="t('tasks.noLogs')" />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { apiGet, apiPost, asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import LogDrawer from '../components/LogDrawer.vue'
import MetricGrid from '../components/MetricGrid.vue'
import ServerSelector from '../components/ServerSelector.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

type DockerSummaryResponse = {
  available?: boolean
  error?: string
  summary?: Record<string, any>
  diskUsage?: Array<Record<string, string>>
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const selectedServerId = ref('')
const servers = ref<any[]>([])
const summary = ref<DockerSummaryResponse>({})
const collection = ref<any[]>([])
const error = ref('')
const tab = ref('overview')
const logsVisible = ref(false)
const logsText = ref('')
const canManageContainers = computed(() => can(permissions.containersManage))

const summaryData = computed(() => summary.value.summary ?? {})
const selectedServer = computed(() => servers.value.find((server) => server.id === selectedServerId.value) ?? null)
const dockerServers = computed(() => servers.value.filter((server) => String(server.dockerHost ?? '').trim() !== ''))
const targetLabel = computed(() => selectedServer.value ? serverLabel(selectedServer.value) : t('containers.selectDockerHost'))
const errorTitle = computed(() => summary.value.available === false ? t('containers.notAvailable') : t('containers.checkFailed'))
const metrics = computed(() => [
  { label: t('containers.title'), value: summaryData.value.containers ?? 0, note: t('containers.runningCount', { count: summaryData.value.running ?? 0 }) },
  { label: t('containers.images'), value: summaryData.value.images ?? 0, note: t('containers.localImages') },
  { label: t('containers.network'), value: summaryData.value.networks ?? 0, note: t('containers.network') },
  { label: t('containers.volumes'), value: summaryData.value.volumes ?? 0, note: t('containers.volumes') },
  { label: t('containers.registry'), value: 0, note: t('containers.controlPlaneData') }
])
const configSummaryItems = computed(() => [
  { label: t('containers.dockerHost'), value: targetLabel.value },
  { label: t('containers.endpoint'), value: summaryData.value.endpoint || '-' },
  { label: t('containers.serverVersion'), value: summaryData.value.version || '-' },
  { label: t('containers.driver'), value: summaryData.value.driver || '-' },
  { label: t('containers.rootDir'), value: summaryData.value.rootDir || '-' },
  { label: t('common.status'), status: summary.value.available ? 'available' : 'failed' }
])
const settingsItems = computed(() => [
  { label: t('containers.dockerHost'), value: targetLabel.value },
  { label: t('containers.endpoint'), value: summaryData.value.endpoint || '-' },
  { label: t('containers.rootDir'), value: summaryData.value.rootDir || '-' },
  { label: t('common.provider'), value: t('common.real') }
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
  return ''
}

function serverLabel(server: any) {
  if (!server) {
    return ''
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

async function loadServers() {
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  if (!selectedServerId.value || !dockerServers.value.some((server) => server.id === selectedServerId.value)) {
    selectedServerId.value = dockerServers.value[0]?.id ?? ''
  }
}

async function load() {
  error.value = ''
  const query = targetQuery()
  if (!query) {
    summary.value = { available: false }
    collection.value = []
    return
  }
  summary.value = await apiGet<DockerSummaryResponse>(`/containers/summary?${query}`).catch((err) => {
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
  const query = targetQuery()
  if (!query) {
    collection.value = []
    return
  }
  collection.value = asArray(await apiGet(`/containers?kind=${tab.value}&${query}`).catch((err) => {
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
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  await apiPost(`/containers/${encodeURIComponent(id)}/${action}?${query}`)
  ElMessage.success(t('containers.actionAccepted'))
  setTimeout(loadCollection, 800)
}

async function openLogs(id: string) {
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const result = await apiGet<{ logs?: string[] }>(`/containers/${encodeURIComponent(id)}/logs?tail=300&${query}`)
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

.settings-grid {
  display: grid;
  gap: 12px;
}

@media (max-width: 1100px) {
  .disk-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .disk-grid {
    grid-template-columns: 1fr;
  }
}
</style>
