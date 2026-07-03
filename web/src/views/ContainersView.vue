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
        <el-tooltip v-if="selectedDockerInstance" :content="deniedText" :disabled="canManageApps" placement="top">
          <span>
            <el-button type="danger" plain :disabled="!canManageApps" @click="openDockerUninstall">{{ t('common.uninstall') }}</el-button>
          </span>
        </el-tooltip>
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
        <div class="table-toolbar">
          <span class="selection-summary">{{ t('containers.selectedCount', { count: selectedContainerRows.length }) }}</span>
          <div class="toolbar-actions">
            <el-tooltip :content="batchActionDisabledReason" :disabled="!batchActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="batchActionDisabled" @click="runContainerBatchAction('start')">{{ t('containers.batchStart') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="batchActionDisabledReason" :disabled="!batchActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="batchActionDisabled" @click="runContainerBatchAction('stop')">{{ t('containers.batchStop') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="batchActionDisabledReason" :disabled="!batchActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="batchActionDisabled" @click="runContainerBatchAction('restart')">{{ t('containers.batchRestart') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="batchRemoveDisabledReason" :disabled="!batchRemoveDisabledReason" placement="top">
              <span><el-button size="small" type="danger" plain :disabled="batchRemoveDisabled" @click="runContainerBatchAction('remove')">{{ t('containers.batchUninstall') }}</el-button></span>
            </el-tooltip>
          </div>
        </div>
        <div class="container-table-body">
          <el-table :data="collection" height="100%" row-key="id" @selection-change="onContainerSelectionChange">
            <el-table-column type="selection" width="44" />
            <el-table-column prop="name" :label="t('containers.name')" min-width="160" show-overflow-tooltip />
            <el-table-column prop="image" :label="t('containers.image')" min-width="190" show-overflow-tooltip />
            <el-table-column prop="state" :label="t('common.status')" width="130">
              <template #default="{ row }">
                <el-tooltip :content="containerStatusDetail(row)" :disabled="!containerStatusDetail(row)" placement="top">
                  <span>
                    <StatusTag :status="containerStatusKind(row)" :label="containerStatusLabel(row)" />
                  </span>
                </el-tooltip>
              </template>
            </el-table-column>
            <el-table-column prop="ports" :label="t('containers.ports')" min-width="180" show-overflow-tooltip />
            <el-table-column prop="networks" :label="t('containers.network')" min-width="140" show-overflow-tooltip />
            <el-table-column prop="createdAt" :label="t('containers.created')" min-width="170" show-overflow-tooltip />
            <el-table-column :label="t('common.operation')" width="340" fixed="right">
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
                  <el-tooltip :content="containerRemoveDisabledReason(row)" :disabled="!containerRemoveDisabledReason(row)" placement="top">
                    <span><el-button size="small" type="danger" plain :disabled="Boolean(containerRemoveDisabledReason(row))" @click="runContainerBatchAction('remove', [row])">{{ t('containers.uninstall') }}</el-button></span>
                  </el-tooltip>
                  <el-button size="small" @click="openLogs(row.id)">{{ t('containers.logs') }}</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </template>

      <template v-else-if="tab === 'images'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="repository" :label="t('containers.repository')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="tag" :label="t('containers.tag')" width="140" show-overflow-tooltip />
          <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
          <el-table-column prop="size" :label="t('containers.size')" width="120" />
          <el-table-column prop="digest" :label="t('containers.digest')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="createdAt" :label="t('containers.created')" min-width="170" show-overflow-tooltip />
          <el-table-column :label="t('common.operation')" width="110" fixed="right">
            <template #default="{ row }">
              <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                <span>
                  <el-button size="small" type="danger" plain :disabled="!canManageContainers" @click="deleteImage(row)">{{ t('containers.deleteImage') }}</el-button>
                </span>
              </el-tooltip>
            </template>
          </el-table-column>
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
    <SecretConfirmPrompt
      v-model="deletePromptVisible"
      :title="t('apps.uninstallService')"
      :message="deletePromptMessage"
      :placeholder="t('apps.deleteServicePasswordPlaceholder')"
      :confirm-text="t('common.uninstall')"
      :cancel-text="t('common.cancel')"
      :loading="deleteSubmitting"
      @confirm="confirmDockerUninstall"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import LogDrawer from '../components/LogDrawer.vue'
import MetricGrid from '../components/MetricGrid.vue'
import SecretConfirmPrompt from '../components/SecretConfirmPrompt.vue'
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

type AppInstance = {
  id: string
  app: string
  serverId: string
  status: string
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const selectedServerId = ref('')
const servers = ref<any[]>([])
const appInstances = ref<AppInstance[]>([])
const summary = ref<DockerSummaryResponse>({})
const collection = ref<any[]>([])
const selectedContainerRows = ref<any[]>([])
const error = ref('')
const tab = ref('overview')
const logsVisible = ref(false)
const logsText = ref('')
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const canManageContainers = computed(() => can(permissions.containersManage))
const canManageApps = computed(() => can(permissions.appsManage))

const summaryData = computed(() => summary.value.summary ?? {})
const selectedServer = computed(() => servers.value.find((server) => server.id === selectedServerId.value) ?? null)
const selectedDockerInstance = computed(() => appInstances.value.find((item) => item.app === 'docker' && item.serverId === selectedServerId.value) ?? null)
const dockerServers = computed(() => servers.value.filter((server) => String(server.dockerHost ?? '').trim() !== ''))
const targetLabel = computed(() => selectedServer.value ? serverLabel(selectedServer.value) : t('containers.selectDockerHost'))
const errorTitle = computed(() => summary.value.available === false ? t('containers.notAvailable') : t('containers.checkFailed'))
const deletePromptMessage = computed(() => selectedServer.value ? t('apps.deleteServicePasswordPrompt', { server: serverLabel(selectedServer.value) }) : '')
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
const selectedContainerIds = computed(() => selectedContainerRows.value.map((row) => String(row?.id ?? '').trim()).filter(Boolean))
const selectedRunningContainers = computed(() => selectedContainerRows.value.filter(isRunningContainer))
const containerStateLabelKeys: Record<string, string> = {
  created: 'containers.state.created',
  restarting: 'containers.state.restarting',
  running: 'containers.state.running',
  removing: 'containers.state.removing',
  paused: 'containers.state.paused',
  exited: 'containers.state.exited',
  dead: 'containers.state.dead'
}
const containerActionLabelKeys: Record<string, string> = {
  start: 'containers.start',
  stop: 'containers.stop',
  restart: 'containers.restart',
  remove: 'containers.uninstall'
}
const singleContainerConfirmKeys: Record<string, string> = {
  start: 'containers.confirmStartContainer',
  stop: 'containers.confirmStopContainer',
  restart: 'containers.confirmRestartContainer'
}
const batchContainerConfirmKeys: Record<string, string> = {
  start: 'containers.confirmBatchStart',
  stop: 'containers.confirmBatchStop',
  restart: 'containers.confirmBatchRestart',
  remove: 'containers.confirmUninstallSelected'
}
const batchActionDisabledReason = computed(() => {
  if (!canManageContainers.value) return deniedText.value
  if (!selectedContainerIds.value.length) return t('containers.selectContainers')
  return ''
})
const batchActionDisabled = computed(() => Boolean(batchActionDisabledReason.value))
const batchRemoveDisabledReason = computed(() => {
  if (batchActionDisabledReason.value) return batchActionDisabledReason.value
  if (selectedRunningContainers.value.length) return t('containers.stopBeforeUninstall')
  return ''
})
const batchRemoveDisabled = computed(() => Boolean(batchRemoveDisabledReason.value))
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
  appInstances.value = asArray(await apiGet<AppInstance[] | null>('/apps/instances').catch(() => []))
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
    selectedContainerRows.value = []
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
    selectedContainerRows.value = []
    return
  }
  const query = targetQuery()
  if (!query) {
    collection.value = []
    selectedContainerRows.value = []
    return
  }
  selectedContainerRows.value = []
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
  const row = collection.value.find((item) => String(item?.id ?? '').trim() === id)
  const confirmKey = singleContainerConfirmKeys[action]
  if (confirmKey) {
    const ok = await confirmContainerAction(action, t(confirmKey, { name: containerDisplayName(row) || id }))
    if (!ok) return
  }
  try {
    await apiPost(`/containers/${encodeURIComponent(id)}/${action}?${query}`)
    ElMessage.success(t('containers.actionAccepted'))
    setTimeout(loadCollection, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.actionFailed'))
  }
}

async function runContainerBatchAction(action: string, rows = selectedContainerRows.value) {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const selectedRows = rows.filter((row) => String(row?.id ?? '').trim())
  const ids = selectedRows.map((row) => String(row.id).trim())
  if (!ids.length) {
    ElMessage.warning(t('containers.selectContainers'))
    return
  }
  if (action === 'remove') {
    if (selectedRows.some(isRunningContainer)) {
      ElMessage.warning(t('containers.stopBeforeUninstall'))
      return
    }
  }
  const confirmKey = batchContainerConfirmKeys[action]
  if (confirmKey) {
    const ok = await confirmContainerAction(action, t(confirmKey, { count: ids.length }))
    if (!ok) return
  }
  try {
    await apiPost(`/containers/actions?${query}`, { action, ids })
    ElMessage.success(t('containers.batchActionAccepted'))
    selectedContainerRows.value = []
    setTimeout(loadCollection, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.actionFailed'))
  }
}

async function deleteImage(row: any) {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const id = imageReference(row)
  if (!id) {
    ElMessage.warning(t('containers.selectImage'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmDeleteImage', { image: id }), t('containers.deleteImage'), {
      type: 'warning',
      confirmButtonText: t('containers.deleteImage'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  try {
    await apiPost(`/containers/images/remove?${query}`, { id })
    ElMessage.success(t('containers.imageRemoveAccepted'))
    setTimeout(loadCollection, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.imageRemoveFailed'))
  }
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

function onContainerSelectionChange(rows: any[]) {
  selectedContainerRows.value = rows
}

function containerState(row: any) {
  return String(row?.state ?? '').trim().toLowerCase()
}

function containerStatusDetail(row: any) {
  return String(row?.status || row?.state || '').trim()
}

function containerStatusKind(row: any) {
  const state = containerState(row)
  const detail = containerStatusDetail(row).toLowerCase()
  if (state === 'running' && detail.includes('(unhealthy)')) return 'failed'
  if (state === 'running' && (detail.includes('(health: starting)') || detail.includes('(starting)'))) return 'pending'
  switch (state) {
    case 'running':
      return 'running'
    case 'exited':
      return 'stopped'
    case 'created':
    case 'restarting':
    case 'removing':
      return 'pending'
    case 'paused':
      return 'degraded'
    case 'dead':
      return 'failed'
    default:
      return 'unknown'
  }
}

function containerStatusLabel(row: any) {
  const state = containerState(row)
  const detail = containerStatusDetail(row).toLowerCase()
  if (state === 'running' && detail.includes('(unhealthy)')) return t('containers.state.unhealthy')
  if (state === 'running' && (detail.includes('(health: starting)') || detail.includes('(starting)'))) return t('containers.state.healthStarting')
  const labelKey = containerStateLabelKeys[state]
  return labelKey ? t(labelKey) : String(row?.state || '').trim() || t('common.unknown')
}

function isRunningContainer(row: any) {
  return containerState(row) === 'running'
}

function containerDisplayName(row: any) {
  return String(row?.name || row?.id || '').trim()
}

async function confirmContainerAction(action: string, message: string) {
  const labelKey = containerActionLabelKeys[action]
  const label = labelKey ? t(labelKey) : action
  try {
    await ElMessageBox.confirm(message, label, {
      type: 'warning',
      confirmButtonText: label,
      cancelButtonText: t('common.cancel')
    })
    return true
  } catch {
    return false
  }
}

function imageReference(row: any) {
  const repository = String(row?.repository ?? '').trim()
  const tag = String(row?.tag ?? '').trim()
  const id = String(row?.id ?? '').trim()
  if (repository && repository !== '<none>' && tag && tag !== '<none>') {
    return `${repository}:${tag}`
  }
  return id
}

function containerRemoveDisabledReason(row: any) {
  if (!canManageContainers.value) return deniedText.value
  if (isRunningContainer(row)) return t('containers.stopBeforeUninstall')
  return ''
}

function openDockerUninstall() {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  deletePromptVisible.value = true
}

async function confirmDockerUninstall(password: string) {
  const instance = selectedDockerInstance.value
  if (!instance) {
    return
  }
  if (!password.trim()) {
    ElMessage.warning(t('apps.deleteServicePasswordPlaceholder'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${instance.id}/delete`, {
      serverPassword: password
    })
    deletePromptVisible.value = false
    ElMessage.success(t('apps.uninstallServiceAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    deleteSubmitting.value = false
  }
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

.table-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 40px;
  padding: 0 2px 8px;
}

.selection-summary {
  color: var(--aifar-text-secondary);
  font-size: 13px;
  white-space: nowrap;
}

.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.container-table-body {
  flex: 1 1 auto;
  min-height: 0;
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

  .table-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .toolbar-actions {
    justify-content: flex-start;
  }
}
</style>
