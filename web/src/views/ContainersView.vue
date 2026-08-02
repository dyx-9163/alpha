<template>
  <section class="containers-page" :class="{ 'is-runtime-logs-page': tab === 'aifar-runtime' && runtimeResourceTab === 'logs' }">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('containers.title') }}</h1>
        <p class="page-subtitle">{{ t('containers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <ServerSelector v-model="selectedServerId" :servers="dockerServers" :placeholder="t('containers.selectDockerHost')" class="toolbar-control" />
        <el-button :loading="loading" @click="load(true)">{{ t('containers.checkHost') }}</el-button>
        <el-button :loading="loading" @click="loadActive(true)">{{ t('common.refresh') }}</el-button>
      </div>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('containers.overview')" name="overview" />
      <el-tab-pane :label="t('containers.aifarRuntime')" name="aifar-runtime" />
      <el-tab-pane :label="t('containers.images')" name="images" />
    </el-tabs>

    <el-alert v-if="error" :title="errorTitle" :description="error" type="warning" :closable="false" show-icon />
    <div class="muted-strip" v-if="!summary.available">{{ t('containers.disabledHint') }}</div>

    <div class="workspace-card containers-main" :class="{ 'is-runtime-logs': tab === 'aifar-runtime' && runtimeResourceTab === 'logs' }">
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

      <template v-else-if="tab === 'aifar-runtime'">
        <AifarRuntimeWorkspace />
      </template>
      <template v-else-if="tab === 'images'">
        <el-tabs v-model="resourceTab" class="resource-tabs">
          <el-tab-pane :label="t('containers.images')" name="images">
            <div class="resource-panel">
              <div class="table-toolbar">
                <span class="selection-summary">{{ t('containers.selectedImageCount', { count: selectedImageRows.length }) }}</span>
                <div class="toolbar-actions">
                  <el-tooltip :content="batchImageRemoveDisabledReason" :disabled="!batchImageRemoveDisabledReason" placement="top">
                    <span>
                      <el-button size="small" type="danger" plain :disabled="batchImageRemoveDisabled" @click="deleteSelectedImages">{{ t('containers.batchDeleteImages') }}</el-button>
                    </span>
                  </el-tooltip>
                </div>
              </div>
              <div class="container-table-body">
                <el-table :data="collection" height="100%" :row-key="imageRowKey" @selection-change="onImageSelectionChange">
                  <el-table-column type="selection" width="44" />
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
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.network')" name="networks">
            <div class="resource-panel">
              <el-table :data="collection" height="100%">
                <el-table-column prop="name" :label="t('containers.name')" min-width="180" />
                <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
                <el-table-column prop="driver" :label="t('containers.driver')" min-width="150" />
                <el-table-column prop="scope" :label="t('containers.scope')" min-width="120" />
              </el-table>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.volumes')" name="volumes">
            <div class="resource-panel">
              <el-table :data="collection" height="100%">
                <el-table-column prop="name" :label="t('containers.name')" min-width="180" show-overflow-tooltip />
                <el-table-column prop="driver" :label="t('containers.driver')" width="140" />
                <el-table-column prop="scope" :label="t('containers.scope')" width="120" />
                <el-table-column prop="mountpoint" :label="t('containers.mountpoint')" min-width="260" show-overflow-tooltip />
                <el-table-column prop="size" :label="t('containers.size')" width="120" />
              </el-table>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.registry')" name="registry">
            <div class="resource-panel">
              <div class="empty-state">
                <div>
                  <strong>{{ t('containers.registry') }}</strong>
                  <span>{{ t('containers.registryHint') }}</span>
                </div>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.hostConfig')" name="settings">
            <div class="resource-panel">
              <div class="settings-grid">
                <KeyValueGrid :items="settingsItems" />
                <p class="muted-strip">{{ t('containers.settingsHint') }}</p>
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
      </template>
    </div>

    <AifarRuntimeDialogs />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { apiGet } from '../api/client'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile } from 'element-plus'
import { asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import MetricGrid from '../components/MetricGrid.vue'
import ServerSelector from '../components/ServerSelector.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { useRealtimeStore } from '../stores/realtime'
import { useTaskProgressStore } from '../stores/taskProgress'
import {
  activeCollectionKind as resolveActiveCollectionKind,
  collectionBackedKind as isCollectionBackedKind,
  collectionCacheKey as buildCollectionCacheKey,
  containerCacheScope,
  runtimeCacheKey as buildRuntimeCacheKey,
  runtimeLogCacheKey as buildRuntimeLogCacheKey,
  summaryCacheKey as buildSummaryCacheKey
} from '../containers/cacheKeys'
import {
  fetchContainerPageBootstrap,
  fetchDockerCollection,
  fetchDockerSummary,
  removeDockerImages,
  type DockerSummaryResponse
} from '../containers/dockerApi'
import { imageReference, imageRowKey, uniqueValues } from '../containers/dockerImages'
import { mergeDockerSummarySnapshot } from '../containers/realtimeSummary'
import AifarRuntimeDialogs from '../containers/runtime/AifarRuntimeDialogs.vue'
import AifarRuntimeWorkspace from '../containers/runtime/AifarRuntimeWorkspace.vue'
import {
  aifarArtifactAccept as resolveAifarArtifactAccept,
  aifarArtifactHintKey,
  buildAifarArtifactForm,
  formatBytes,
  isAifarArtifactTooLarge
} from '../containers/runtime/artifacts'
import {
  applyRuntimeConfig,
  cleanupStaleRuntime,
  createRuntimeLogEventSource,
  deleteAifarRelease as deleteAifarReleaseRequest,
  fetchAifarReleases,
  fetchAifarRuntime,
  installRuntimeServices,
  offlineRuntimeServices as offlineRuntimeServicesRequest,
  offlineRuntimeService,
  reconcileRuntime,
  restartAllRuntime,
  rollbackAifarRelease as rollbackAifarReleaseRequest,
  scaleInRuntimeService,
  scaleOutRuntimeService,
  updateAifarArtifact
} from '../containers/runtime/api'
import {
  buildRuntimeServiceOverrides,
  defaultRuntimeConfigState,
  normalizedRuntimeValues,
  runtimeConfigNumberText,
  validateRuntimeConfigValues
} from '../containers/runtime/config'
import {
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel as formatAifarRuntimeStatusLabel,
  formatDate,
  percentText,
  releaseKindLabel as formatReleaseKindLabel,
  releaseServicesText,
  releaseStatusLabel as formatReleaseStatusLabel,
  runtimeApplyStatusLabel as formatRuntimeApplyStatusLabel,
  runtimeDeploymentReplicaText,
  runtimeEndpointText,
  runtimeInstanceLabel as formatRuntimeInstanceLabel,
  runtimeNacosStatus
} from '../containers/runtime/format'
import {
  runtimeReleaseDeleteDisabledReason,
  runtimeReleaseRollbackDisabledReason,
  runtimeReleaseRollbackServices
} from '../containers/runtime/releaseRules'
import { promptRuntimeReleaseRollback } from '../containers/runtime/releaseImpact'
import { useAifarRuntimeProvider } from '../containers/runtime/useAifarRuntimeProvider'
import {
  parseRuntimeLogErrorEvent,
  parseRuntimeLogLines,
  parseRuntimeLogsEvent,
  runtimeLogMaxRows,
  runtimeLogRowHeight,
  runtimeLogVisibleCount,
  type RuntimeLogParseContext
} from '../containers/runtime/logs'
import {
  createRuntimeStatusRefreshScheduler,
  isRuntimeStatusEventForSelection
} from '../containers/runtime/runtimeStatusRefresh'
import { runtimePodLoadArgs } from '../containers/runtime/runtimePodLoading'
import { mergeRuntimePodMetrics } from '../containers/runtime/runtimePodMetrics'
import { createRuntimePodMetricsScheduler } from '../containers/runtime/runtimePodMetricsScheduler'
import {
  buildAifarServiceOptions,
  buildRuntimeLogPodOptions,
  buildRuntimeServiceMap,
  filterRuntimeDeploymentsByInstance,
  filterRuntimePodsByInstance,
  filterRuntimeServicesByInstance,
  findRuntimeIngressByInstance,
  findSelectedRuntimeInstance,
  resolveRuntimeAppInstance,
  runtimeDiscoveryTarget,
  summarizeRuntimeRestartScope,
  runtimeServiceForDeployment as resolveRuntimeServiceForDeployment,
  type RuntimeAppInstance
} from '../containers/runtime/selectors'
import type { AppInstallModuleOption } from '../apps/registry/contract'
import { useAifarRuntimeLogViewport } from '../containers/runtime/useAifarRuntimeLogViewport'
import type {
  AifarRelease,
  AifarRuntimeDeployment,
  AifarRuntimeIngress,
  AifarRuntimeInstance,
  AifarRuntimeLogPod,
  AifarRuntimeLogsResponse,
  AifarRuntimePod,
  AifarRuntimeResponse,
  AifarRuntimeService,
  RuntimeConfigFormValues,
  RuntimeConfigServiceRow,
  RuntimeConfigValues,
  RuntimeEntryRoute,
  RuntimeLogRow
} from '../containers/runtime/types'

type AppInstance = RuntimeAppInstance

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const taskProgress = useTaskProgressStore()
const realtime = useRealtimeStore()
const AIFAR_RUNTIME_MODEL = 'agent-runtime-v2'
const selectedServerId = ref('')
const servers = ref<any[]>([])
const appInstances = ref<AppInstance[]>([])
const appSettings = ref<{ maxRequestBodyBytes?: number }>({})
const summary = ref<DockerSummaryResponse>({})
const collection = ref<any[]>([])
const loadingCount = ref(0)
const loading = computed(() => loadingCount.value > 0)
const pageReady = ref(false)
const summaryCache = ref<Record<string, DockerSummaryResponse>>({})
const collectionCache = ref<Record<string, any[]>>({})
const runtimeCache = ref<Record<string, AifarRuntimeResponse>>({})
const aifarReleases = ref<AifarRelease[]>([])
const aifarInstallModules = ref<AppInstallModuleOption[]>([])
const aifarReleaseCache = ref<Record<string, AifarRelease[]>>({})
const releaseDeletingId = ref('')
const selectedImageRows = ref<any[]>([])
const error = ref('')
const tab = ref<'overview' | 'aifar-runtime' | 'images'>('overview')
const resourceTab = ref<'images' | 'networks' | 'volumes' | 'registry' | 'settings'>('images')
const aifarUpdateVisible = ref(false)
const aifarUpdateSubmitting = ref(false)
const aifarUpdateInstanceOverride = ref<AppInstance | null>(null)
const aifarUpdateTargetLabel = ref('')
const aifarUpdateMode = ref<'single' | 'bundle'>('single')
const aifarUpdateService = ref('oauth')
const aifarArtifactFile = ref<File | null>(null)
const aifarRuntime = ref<AifarRuntimeResponse>({ runtimeStatus: 'unknown', agent: { status: 'unknown' }, instances: [], services: [], pods: [], ingress: [], warnings: [] })
const selectedRuntimeInstanceId = ref('')
const runtimeResourceTab = ref<'deployments' | 'releases' | 'services' | 'pods' | 'logs' | 'ingress'>('deployments')
const runtimePodServiceFilter = ref('')
const runtimePodsLoaded = ref<Record<string, boolean>>({})
const runtimePodStatsLoaded = ref<Record<string, boolean>>({})
const runtimeLogServiceFilter = ref<string[]>([])
const runtimeLogPodFilter = ref<string[]>([])
const runtimeLogLevelFilter = ref<string[]>([])
const runtimeLogKeyword = ref('')
const runtimeLogTail = ref(200)
const runtimeLogSinceSeconds = ref(0)
const runtimeLogs = ref<AifarRuntimeLogsResponse>({ pods: [], warnings: [], tail: 200 })
const runtimeLogsLoaded = ref<Record<string, boolean>>({})
const runtimeLogRows = ref<RuntimeLogRow[]>([])
const runtimeLogPendingRows = ref<RuntimeLogRow[]>([])
const runtimeLogPaused = ref(false)
const runtimeLogAutoScroll = ref(true)
const runtimeLogDroppedRows = ref(0)
const runtimeLogStreamStatus = ref<'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error'>('idle')
const runtimeLogLastDataAt = ref('')
let runtimeLogSource: EventSource | null = null
let runtimeLogStreamKey = ''
let runtimeLogSequence = 0
const runtimeLogParseContexts = new Map<string, RuntimeLogParseContext>()
const runtimeConfigVisible = ref(false)
const runtimeConfigSubmitting = ref(false)
const runtimeRestartSubmitting = ref(false)
const runtimeConfigForm = ref<RuntimeConfigFormValues>({
  appCPUs: '2.0',
  appMemoryLimit: '2GB',
  jvmInitialRAMPercentage: 20,
  jvmMaxRAMPercentage: 70,
  nacosEphemeral: true
})
const runtimeConfigRows = ref<RuntimeConfigServiceRow[]>([])
const serviceInstallVisible = ref(false)
const serviceInstallSubmitting = ref(false)
const serviceInstallSelection = ref<string[]>([])
const canManageContainers = computed(() => can(permissions.containersManage))
const canManageApps = computed(() => can(permissions.appsManage))

const summaryData = computed(() => summary.value.summary ?? {})
const selectedServer = computed(() => servers.value.find((server) => server.id === selectedServerId.value) ?? null)
const dockerServers = computed(() => servers.value.filter((server) => String(server.dockerHost ?? '').trim() !== ''))
const targetLabel = computed(() => selectedServer.value ? serverLabel(selectedServer.value) : t('containers.selectDockerHost'))
const errorTitle = computed(() => summary.value.available === false ? t('containers.notAvailable') : t('containers.checkFailed'))
const metrics = computed(() => [
  { label: t('containers.title'), value: summaryData.value.containers ?? 0, note: t('containers.runningCount', { count: summaryData.value.running ?? 0 }) },
  { label: t('containers.images'), value: summaryData.value.images ?? 0, note: t('containers.localImages') },
  { label: t('containers.network'), value: summaryData.value.networks ?? 0, note: t('containers.network') },
  { label: t('containers.volumes'), value: summaryData.value.volumes ?? 0, note: t('containers.volumes') }
])
const configSummaryItems = computed(() => [
  { label: t('containers.dockerHost'), value: targetLabel.value },
  { label: t('containers.endpoint'), value: summaryData.value.endpoint || '-' },
  { label: t('containers.serverVersion'), value: summaryData.value.version || '-' },
  { label: t('containers.driver'), value: summaryData.value.driver || '-' },
  { label: t('containers.rootDir'), value: summaryData.value.rootDir || '-' },
  { label: t('common.status'), status: summary.value.available ? 'available' : 'unavailable' }
])
const settingsItems = computed(() => [
  { label: t('containers.dockerHost'), value: targetLabel.value },
  { label: t('containers.endpoint'), value: summaryData.value.endpoint || '-' },
  { label: t('containers.rootDir'), value: summaryData.value.rootDir || '-' },
  { label: t('common.provider'), value: t('common.real') }
])
const selectedImageIds = computed(() => uniqueValues(selectedImageRows.value.map(imageReference).filter(Boolean)))
const batchImageRemoveDisabledReason = computed(() => {
  if (!canManageContainers.value) return deniedText.value
  if (!selectedImageIds.value.length) return t('containers.selectImages')
  return ''
})
const batchImageRemoveDisabled = computed(() => Boolean(batchImageRemoveDisabledReason.value))
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
const selectedAifarUpdateInstance = computed(() => aifarUpdateInstanceOverride.value)
const selectedAifarContainerLabel = computed(() => aifarUpdateTargetLabel.value || '-')
const aifarUpdateModeLabel = computed(() => aifarUpdateMode.value === 'bundle' ? t('apps.aifarUpdateAllServices') : t('apps.aifarUpdateSingleMode'))
const selectedAifarInstanceLabel = computed(() => {
  const instance = selectedAifarUpdateInstance.value
  if (!instance) {
    return '-'
  }
  const server = servers.value.find((item) => item.id === instance.serverId)
  const serverText = server ? serverLabel(server) : instance.serverId
  return `${instance.app} / ${instance.version || '-'} / ${serverText}`
})
const aifarArtifactAccept = computed(() => resolveAifarArtifactAccept(aifarUpdateMode.value, aifarUpdateService.value))
const aifarArtifactHint = computed(() => t(aifarArtifactHintKey(aifarUpdateMode.value, aifarUpdateService.value)))
const aifarRuntimeInstances = computed(() => asArray<AifarRuntimeInstance>(aifarRuntime.value.instances))
const selectedRuntimeInstance = computed(() => findSelectedRuntimeInstance(aifarRuntimeInstances.value, selectedRuntimeInstanceId.value))
const selectedRuntimeConfig = computed(() => selectedRuntimeInstance.value?.runtimeConfig ?? defaultRuntimeConfigState())
const selectedRuntimeAppInstance = computed(() => resolveRuntimeAppInstance(selectedRuntimeInstance.value, appInstances.value, selectedServerId.value))
const selectedRuntimeServices = computed(() => filterRuntimeServicesByInstance(asArray<AifarRuntimeService>(aifarRuntime.value.services), selectedRuntimeInstance.value?.id))
const selectedRuntimeDeployments = computed(() => filterRuntimeDeploymentsByInstance(asArray<AifarRuntimeDeployment>(aifarRuntime.value.deployments), selectedRuntimeInstance.value?.id))
const runtimeRestartScope = computed(() => summarizeRuntimeRestartScope(selectedRuntimeDeployments.value))
const selectedRuntimePodsRaw = computed(() => filterRuntimePodsByInstance(asArray<AifarRuntimePod>(aifarRuntime.value.pods), selectedRuntimeInstance.value?.id))
const selectedRuntimePods = computed(() => {
  const service = String(runtimePodServiceFilter.value || '').trim()
  if (!service) return selectedRuntimePodsRaw.value
  return selectedRuntimePodsRaw.value.filter((item) => item.serviceName === service)
})
const staleRuntimePodCount = computed(() => selectedRuntimePodsRaw.value.filter((item) => String(item.status || '').trim() === 'stale').length)
const selectedRuntimeIngress = computed(() => findRuntimeIngressByInstance(asArray<AifarRuntimeIngress>(aifarRuntime.value.ingress), selectedRuntimeInstance.value?.id))
const aifarRuntimeWarnings = computed(() => asArray<string>(aifarRuntime.value.warnings))
const installedRuntimeServiceNames = computed(() => new Set(selectedRuntimeServices.value.map((item) => item.serviceName).filter(Boolean)))
const runtimeServiceMap = computed(() => buildRuntimeServiceMap(selectedRuntimeServices.value))
const runtimePodsLoadedForCurrentScope = computed(() => Boolean(runtimePodsLoaded.value[runtimeCacheKey('pods')]))
const runtimePodStatsLoadedForCurrentScope = computed(() => Boolean(runtimePodStatsLoaded.value[runtimeCacheKey('pods')]))
const runtimeStatusRefresh = createRuntimeStatusRefreshScheduler(() => {
  const podsActive = runtimePodsActive()
  const [force, includeStats, background] = runtimePodLoadArgs('status-event')
  return loadAifarRuntime(force, podsActive, podsActive && includeStats, background)
})
const runtimePodMetricsScheduler = createRuntimePodMetricsScheduler(async () => {
  if (!runtimePodsActive()) return
  await ensureRuntimePodsLoaded(...runtimePodLoadArgs('metrics'))
})
const runtimeLogsLoadedForCurrentScope = computed(() => Boolean(runtimeLogsLoaded.value[runtimeLogCacheKey()]))
const runtimeLogGroups = computed(() => asArray<AifarRuntimeLogPod>(runtimeLogs.value.pods))
const runtimeLogPodOptions = computed(() => buildRuntimeLogPodOptions(selectedRuntimePodsRaw.value, runtimeLogServiceFilter.value))
const runtimeLogSelectionReady = computed(() => runtimeLogServiceFilter.value.length > 0 || runtimeLogPodFilter.value.length > 0)
const runtimeLogErrorCount = computed(() => runtimeLogRows.value.filter((row) => row.errorContext).length)
const filteredRuntimeLogRows = computed<RuntimeLogRow[]>(() => {
  const levels = new Set(runtimeLogLevelFilter.value.map((item) => item.toUpperCase()))
  const keyword = runtimeLogKeyword.value.trim().toLowerCase()
  const rows = runtimeLogRows.value.filter((row) => {
    if (levels.size && !levels.has(String(row.level || '').toUpperCase())) {
      return false
    }
    if (keyword) {
      const text = `${row.time} ${row.serviceName} ${row.pod} ${row.level} ${row.message}`.toLowerCase()
      return text.includes(keyword)
    }
    return true
  })
  return rows.sort((left, right) => {
    if (left.timestamp && right.timestamp && left.timestamp !== right.timestamp) {
      return left.timestamp - right.timestamp
    }
    if (left.timestamp && !right.timestamp) return -1
    if (!left.timestamp && right.timestamp) return 1
    return left.sequence - right.sequence
  })
})
const {
  runtimeLogViewport,
  runtimeLogVirtualRows,
  runtimeLogTopSpacer,
  runtimeLogBottomSpacer,
  handleRuntimeLogScroll,
  resetRuntimeLogViewport,
  scrollRuntimeLogsToBottom
} = useAifarRuntimeLogViewport(filteredRuntimeLogRows, {
  rowHeight: runtimeLogRowHeight,
  visibleCount: runtimeLogVisibleCount
})
const runtimeLogWarnings = computed(() => asArray<string>(runtimeLogs.value.warnings))
const selectedRuntimeReleaseCacheKey = computed(() => selectedRuntimeInstance.value?.id ? `aifar-releases:${selectedRuntimeInstance.value.id}` : '')
const runtimeLogPendingCount = computed(() => runtimeLogPendingRows.value.length)
const runtimeLogStreamStatusLabel = computed(() => t(`containers.logStream.${runtimeLogStreamStatus.value}`))
const runtimeLogStreamTagType = computed(() => {
  switch (runtimeLogStreamStatus.value) {
    case 'connected':
      return 'success'
    case 'connecting':
    case 'reconnecting':
      return 'warning'
    case 'error':
      return 'danger'
    default:
      return 'info'
  }
})
const aifarServiceOptions = computed(() => buildAifarServiceOptions(aifarInstallModules.value, Array.from(installedRuntimeServiceNames.value)))
const installedRuntimeServiceNamesList = computed(() => aifarServiceOptions.value.map((item) => item.value).filter((service) => installedRuntimeServiceNames.value.has(service)))
const missingRuntimeServiceOptions = computed(() => aifarServiceOptions.value.filter((item) => !installedRuntimeServiceNames.value.has(item.value)))
const runtimeInstanceManageDisabledReason = computed(() => {
  if (!canManageApps.value) return deniedText.value
  if (!selectedRuntimeInstance.value) return t('containers.selectAifarInstance')
  if (selectedRuntimeInstance.value.legacy) return t('containers.legacyRuntimeDisabled')
  return ''
})
const runtimeMutationBusy = computed(() => runtimeRestartSubmitting.value || runtimeConfigSubmitting.value || serviceInstallSubmitting.value || aifarUpdateSubmitting.value)
const aifarRuntimeActionDisabledReason = computed(() => {
  if (runtimeInstanceManageDisabledReason.value) return runtimeInstanceManageDisabledReason.value
  if (runtimeMutationBusy.value) return t('containers.runtimeMutationInProgress')
  if (String(aifarRuntime.value.runtimeStatus || '').trim() !== 'ready') return t('containers.runtimeDegradedDisabled')
  if (String(aifarRuntime.value.agent?.status || '').trim() !== 'running') return t('containers.agentUnavailableDisabled')
  return ''
})
const runtimeRestartDisabledReason = computed(() => {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  if (!runtimeRestartScope.value.services) return t('containers.noEnabledRuntimeServices')
  return ''
})
const runtimeCleanupDisabledReason = computed(() => {
  if (runtimeInstanceManageDisabledReason.value) return runtimeInstanceManageDisabledReason.value
  if (!staleRuntimePodCount.value) return t('containers.noStaleRuntimePods')
  return ''
})
const serviceInstallDisabledReason = computed(() => {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  if (!missingRuntimeServiceOptions.value.length) return t('containers.noMissingServices')
  return ''
})
const runtimeSummaryItems = computed(() => {
  const instance = selectedRuntimeInstance.value
  const config = selectedRuntimeConfig.value
  return [
    { label: t('containers.aifarInstance'), value: instance ? runtimeInstanceLabel(instance) : '-' },
    { label: t('containers.installRoot'), value: instance?.installRoot || '-' },
    { label: t('containers.runtimeConfigVersion'), value: `${config.configVersion ?? '-'} / ${config.appliedVersion ?? '-'}`, status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.lastApplyStatus'), value: runtimeApplyStatusLabel(config.lastApplyStatus), status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.agent'), value: aifarRuntime.value.agent?.version || aifarRuntime.value.agent?.status || '-', status: aifarRuntime.value.agent?.status || 'unknown' }
  ]
})
const runtimeEntryRoutes = computed<RuntimeEntryRoute[]>(() => {
  const ingress = selectedRuntimeIngress.value
  return [
    {
      name: t('containers.webRoute'),
      route: ingress?.webRoute || selectedRuntimeInstance.value?.endpoint || '-',
      port: `${t('containers.webPort')}: ${ingress?.webPort || '-'}`,
      status: ingress?.status || 'unknown'
    },
    {
      name: t('containers.gatewayRoute'),
      route: ingress?.gatewayRoute || selectedRuntimeInstance.value?.gatewayEndpoint || '-',
      port: `${t('containers.gatewayPort')}: ${ingress?.gatewayPort || '-'}`,
      status: ingress?.status || 'unknown'
    }
  ]
})
const runtimeConfigMetaItems = computed(() => {
  const config = selectedRuntimeConfig.value
  return [
    { label: t('containers.runtimeConfigDesiredVersion'), value: config.configVersion ?? '-' },
    { label: t('containers.runtimeConfigAppliedVersion'), value: config.appliedVersion ?? '-' },
    { label: t('containers.lastApplyStatus'), value: runtimeApplyStatusLabel(config.lastApplyStatus), status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.lastAppliedAt'), value: config.lastAppliedAt || '-' },
    { label: t('containers.lastApplyError'), value: config.lastApplyError || '-' }
  ]
})

function targetQuery() {
  if (selectedServerId.value) {
    return `serverId=${encodeURIComponent(selectedServerId.value)}`
  }
  return ''
}

function cacheScope() {
  return containerCacheScope(selectedServerId.value)
}

function summaryCacheKey(includeDisk: boolean) {
  return buildSummaryCacheKey(cacheScope(), includeDisk)
}

function activeCollectionKind() {
  return resolveActiveCollectionKind(tab.value, resourceTab.value)
}

function collectionBackedKind(kind = activeCollectionKind()) {
  return isCollectionBackedKind(kind)
}

function collectionCacheKey(kind = activeCollectionKind()) {
  return buildCollectionCacheKey(cacheScope(), kind)
}

function runtimeCacheKey(scope: 'base' | 'pods' = 'base') {
  return buildRuntimeCacheKey(cacheScope(), scope)
}

function runtimeLogCacheKey() {
  return buildRuntimeLogCacheKey(
    cacheScope(),
    selectedRuntimeInstance.value?.id,
    runtimeLogServiceFilter.value,
    runtimeLogPodFilter.value,
    runtimeLogTail.value,
    runtimeLogSinceSeconds.value
  )
}

async function withLoading<T>(fn: () => Promise<T>) {
  loadingCount.value += 1
  try {
    return await fn()
  } finally {
    loadingCount.value = Math.max(0, loadingCount.value - 1)
  }
}

function serverLabel(server: any) {
  if (!server) {
    return ''
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

async function loadServers() {
  aifarInstallModules.value = await apiGet<AppInstallModuleOption[]>('/apps/aifar/install-modules?version=runtime-v2').catch(() => [])
  const { servers: serverRows, appInstances: instanceRows, settings } = await fetchContainerPageBootstrap<AppInstance>({
    servers: servers.value,
    appInstances: appInstances.value,
    settings: appSettings.value
  })
  servers.value = asArray(serverRows)
  appInstances.value = asArray(instanceRows)
  appSettings.value = settings
  if (!selectedServerId.value || !dockerServers.value.some((server) => server.id === selectedServerId.value)) {
    selectedServerId.value = dockerServers.value[0]?.id ?? ''
  }
}

async function load(force = false) {
  return withLoading(async () => {
    error.value = ''
    const query = targetQuery()
    if (!query) {
      summary.value = { available: false }
      collection.value = []
      return
    }
    const includeDisk = tab.value === 'overview'
    if (includeDisk) {
      await loadSummary(true, force)
      return
    }
    await Promise.all([loadSummary(false, force), loadCollection(force)])
  })
}

async function loadSummary(includeDisk = false, force = false) {
  error.value = ''
  const query = targetQuery()
  if (!query) {
    summary.value = { available: false }
    return
  }
  const key = summaryCacheKey(includeDisk)
  const diskKey = summaryCacheKey(true)
  if (!force) {
    const cached = summaryCache.value[key] ?? (!includeDisk ? summaryCache.value[diskKey] : undefined)
    if (cached) {
      summary.value = cached
      return
    }
  }
  const next = await fetchDockerSummary(query, includeDisk).catch((err) => {
    error.value = err.message
    return { available: false, error: err.message }
  })
  summary.value = next
  summaryCache.value = { ...summaryCache.value, [key]: next }
  if (includeDisk) {
    summaryCache.value = { ...summaryCache.value, [summaryCacheKey(false)]: next }
  }
  if (next.available === false && next.error) error.value = next.error
}

function applyDockerSummaryEvent(event: unknown) {
  const next = mergeDockerSummarySnapshot(summary.value, event)
  if (!next) {
    return false
  }
  summary.value = next
  summaryCache.value = { ...summaryCache.value, [summaryCacheKey(false)]: next }
  if (next.available === false && next.error) {
    error.value = next.error
  }
  return true
}

async function loadCollection(force = false) {
  if (tab.value === 'aifar-runtime') {
    collection.value = []
    selectedImageRows.value = []
    await loadAifarRuntime(force)
    return
  }
  const kind = activeCollectionKind()
  if (!collectionBackedKind(kind)) {
    collection.value = []
    selectedImageRows.value = []
    return
  }
  const query = targetQuery()
  if (!query) {
    collection.value = []
    selectedImageRows.value = []
    return
  }
  selectedImageRows.value = []
  const key = collectionCacheKey(kind)
  if (!force && collectionCache.value[key]) {
    collection.value = collectionCache.value[key]
    return
  }
  const next = asArray(await fetchDockerCollection(kind, query).catch((err) => {
    error.value = err.message
    return []
  }))
  collection.value = next
  collectionCache.value = { ...collectionCache.value, [key]: next }
}

async function loadAifarRuntime(force = false, includePods = runtimeResourceTab.value === 'pods', includeStats = false, background = false) {
  const execute = async () => {
    const query = targetQuery()
    if (!query) {
      aifarRuntime.value = { runtimeStatus: 'unknown', agent: { status: 'unknown' }, instances: [], services: [], pods: [], ingress: [], warnings: [] }
      selectedRuntimeInstanceId.value = ''
      return
    }
    const scope = includePods ? 'pods' : 'base'
    const key = runtimeCacheKey(scope)
    const cacheHasRequiredStats = !includeStats || runtimePodStatsLoaded.value[key]
    if (!force && runtimeCache.value[key] && cacheHasRequiredStats) {
      aifarRuntime.value = includePods ? runtimeCache.value[key] : { ...runtimeCache.value[key], pods: asArray<AifarRuntimePod>(aifarRuntime.value.pods) }
      return
    }
    let next: AifarRuntimeResponse
    try {
      next = await fetchAifarRuntime(query, { includePods, includeStats })
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      error.value = message
      if (background) return
      next = { runtimeStatus: 'degraded', agent: { status: 'missing', error: message }, instances: [], services: [], pods: [], ingress: [], warnings: [message] }
    }
    if (key !== runtimeCacheKey(scope)) return
    const currentPods = asArray<AifarRuntimePod>(aifarRuntime.value.pods)
    const merged = includePods
      ? { ...next, pods: includeStats ? asArray<AifarRuntimePod>(next.pods) : mergeRuntimePodMetrics(currentPods, asArray<AifarRuntimePod>(next.pods)) }
      : { ...next, pods: currentPods }
    aifarRuntime.value = merged
    runtimeCache.value = { ...runtimeCache.value, [key]: merged }
    if (includePods) {
      const podsKey = runtimeCacheKey('pods')
      runtimePodsLoaded.value = { ...runtimePodsLoaded.value, [podsKey]: true }
      runtimePodStatsLoaded.value = { ...runtimePodStatsLoaded.value, [podsKey]: includeStats || Boolean(runtimePodStatsLoaded.value[podsKey]) }
      runtimeCache.value = { ...runtimeCache.value, [runtimeCacheKey('base')]: { ...merged, pods: [] } }
    }
    const instances = asArray<AifarRuntimeInstance>(aifarRuntime.value.instances)
    if (!instances.some((instance) => instance.id === selectedRuntimeInstanceId.value)) {
      selectedRuntimeInstanceId.value = instances.find((instance) => !instance.legacy)?.id ?? instances[0]?.id ?? ''
    }
  }
  return background ? execute() : withLoading(execute)
}

async function loadAifarReleases(force = false) {
  const instance = selectedRuntimeInstance.value
  if (!instance?.id) {
    aifarReleases.value = []
    return
  }
  const key = selectedRuntimeReleaseCacheKey.value
  if (!force && key && aifarReleaseCache.value[key]) {
    aifarReleases.value = aifarReleaseCache.value[key]
    return
  }
  return withLoading(async () => {
    const result = await fetchAifarReleases(instance.id)
    const items = asArray<AifarRelease>(result.items)
    aifarReleases.value = items
    if (key) {
      aifarReleaseCache.value = { ...aifarReleaseCache.value, [key]: items }
    }
  })
}

async function ensureRuntimePodsLoaded(force = false, includeStats = false, background = false) {
  if (!targetQuery()) return
  if (!force && runtimePodsLoadedForCurrentScope.value && (!includeStats || runtimePodStatsLoadedForCurrentScope.value)) return
  await loadAifarRuntime(force, true, includeStats, background)
}

function runtimePodsVisible() {
  return tab.value === 'aifar-runtime' && runtimeResourceTab.value === 'pods' && Boolean(targetQuery())
}

function runtimePodsActive() {
  return runtimePodsVisible() && Boolean(selectedRuntimeInstance.value?.id)
}

async function activateRuntimePods(trigger: 'enter' | 'scope-change') {
  runtimePodMetricsScheduler.stop()
  if (!runtimePodsVisible()) return
  await ensureRuntimePodsLoaded(...runtimePodLoadArgs(trigger))
  if (runtimePodsActive()) runtimePodMetricsScheduler.start()
}

async function refreshRuntimePodBase() {
  await ensureRuntimePodsLoaded(...runtimePodLoadArgs('refresh'))
  runtimePodMetricsScheduler.stop()
  if (runtimePodsActive()) runtimePodMetricsScheduler.start()
}

function refreshRuntimePodMetrics() {
  if (runtimePodsActive()) runtimePodMetricsScheduler.request()
}

function clearRuntimePodServiceFilter() {
  runtimePodServiceFilter.value = ''
}

function loadRuntimeLogs(force = false) {
  const key = runtimeLogCacheKey()
  if (!force && runtimeLogSource && runtimeLogStreamKey === key) {
    return
  }
  if (!runtimeLogSelectionReady.value) {
    if (force) {
      ElMessage.warning(t('containers.selectRuntimeLogScope'))
    }
    return
  }
  openRuntimeLogStream(force)
}

function openRuntimeLogStream(force = false) {
  closeRuntimeLogStream()
  const query = targetQuery()
  const instance = selectedRuntimeInstance.value
  const key = runtimeLogCacheKey()
  if (force) {
    resetRuntimeLogView()
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: false }
  }
  if (tab.value !== 'aifar-runtime' || runtimeResourceTab.value !== 'logs' || !query || !instance?.id || !runtimeLogSelectionReady.value) {
    return
  }
  const params = new URLSearchParams(query)
  params.set('instanceId', instance.id)
  params.set('tail', String(runtimeLogTail.value))
  params.set('batch', '200')
  if (runtimeLogSinceSeconds.value > 0) {
    params.set('since', String(runtimeLogSinceSeconds.value))
  } else if (runtimeLogSinceSeconds.value < 0) {
    params.set('fromEnd', '1')
  }
  if (runtimeLogServiceFilter.value.length) {
    params.set('services', runtimeLogServiceFilter.value.join(','))
  }
  if (runtimeLogPodFilter.value.length) {
    params.set('pods', runtimeLogPodFilter.value.join(','))
  }
  const source = createRuntimeLogEventSource(params)
  runtimeLogSource = source
  runtimeLogStreamKey = key
  runtimeLogStreamStatus.value = 'connecting'
  runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  source.onopen = () => {
    if (runtimeLogSource === source) {
      runtimeLogStreamStatus.value = 'connected'
    }
  }
  source.onerror = () => {
    if (runtimeLogSource === source) {
      runtimeLogStreamStatus.value = 'reconnecting'
    }
  }
  const applySnapshot = (event: Event) => {
    if (runtimeLogSource !== source) {
      return
    }
    const next = parseRuntimeLogsEvent((event as MessageEvent).data)
    if (!next) {
      return
    }
    markRuntimeLogDataReceived()
    applyRuntimeLogResponse(next, true)
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  }
  const applyBatch = (event: Event) => {
    if (runtimeLogSource !== source) {
      return
    }
    const next = parseRuntimeLogsEvent((event as MessageEvent).data)
    if (!next) {
      return
    }
    markRuntimeLogDataReceived()
    applyRuntimeLogResponse(next, false)
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  }
  source.addEventListener('runtime-logs-snapshot', applySnapshot)
  source.addEventListener('runtime-logs-batch', applyBatch)
  source.addEventListener('runtime-logs', applySnapshot)
  source.addEventListener('runtime-logs-error', (event) => {
    if (runtimeLogSource !== source) {
      return
    }
    const message = parseRuntimeLogErrorEvent((event as MessageEvent).data)
    runtimeLogStreamStatus.value = 'error'
    runtimeLogs.value = { ...runtimeLogs.value, warnings: message ? [message] : [] }
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  })
}

function closeRuntimeLogStream() {
  if (runtimeLogSource) {
    runtimeLogSource.close()
    runtimeLogSource = null
  }
  runtimeLogStreamKey = ''
  runtimeLogStreamStatus.value = 'idle'
}

function markRuntimeLogDataReceived() {
  runtimeLogStreamStatus.value = 'connected'
  runtimeLogLastDataAt.value = new Date().toLocaleTimeString()
}

function resetRuntimeLogView() {
  runtimeLogs.value = { pods: [], warnings: [], tail: runtimeLogTail.value }
  runtimeLogRows.value = []
  runtimeLogPendingRows.value = []
  runtimeLogPaused.value = false
  runtimeLogDroppedRows.value = 0
  runtimeLogSequence = 0
  runtimeLogParseContexts.clear()
  resetRuntimeLogViewport()
  runtimeLogLastDataAt.value = ''
}

function applyRuntimeLogResponse(next: AifarRuntimeLogsResponse, replace: boolean) {
  if (replace) {
    runtimeLogParseContexts.clear()
  }
  runtimeLogs.value = replace ? next : mergeRuntimeLogGroups(runtimeLogs.value, next)
  const rows = runtimeLogRowsFromResponse(next)
  appendRuntimeLogRows(rows)
}

function mergeRuntimeLogGroups(current: AifarRuntimeLogsResponse, next: AifarRuntimeLogsResponse): AifarRuntimeLogsResponse {
  const groups = new Map<string, AifarRuntimeLogPod>()
  for (const pod of asArray<AifarRuntimeLogPod>(current.pods)) {
    groups.set(pod.containerName, { ...pod, logs: [] })
  }
  for (const pod of asArray<AifarRuntimeLogPod>(next.pods)) {
    const previous = groups.get(pod.containerName)
    const previousCount = Number(previous?.lineCount ?? 0)
    const nextCount = Number(pod.lineCount ?? asArray<string>(pod.logs).length)
    groups.set(pod.containerName, {
      ...(previous ?? {}),
      ...pod,
      logs: [],
      lineCount: previous ? previousCount + nextCount : nextCount
    })
  }
  return {
    ...current,
    ...next,
    pods: Array.from(groups.values()),
    warnings: uniqueValues([...asArray<string>(current.warnings), ...asArray<string>(next.warnings)])
  }
}

function runtimeLogRowsFromResponse(next: AifarRuntimeLogsResponse) {
  const rows: RuntimeLogRow[] = []
  for (const pod of asArray<AifarRuntimeLogPod>(next.pods)) {
    const containerName = pod.containerName || pod.podId || '-'
    const parsedBatch = parseRuntimeLogLines(
      asArray<string>(pod.logs),
      runtimeLogParseContexts.get(containerName)
    )
    runtimeLogParseContexts.set(containerName, parsedBatch.context)
    for (const parsed of parsedBatch.lines) {
      const sequence = runtimeLogSequence++
      rows.push({
        id: `${containerName}:${sequence}`,
        time: parsed.time,
        timestamp: parsed.timestamp,
        sequence,
        serviceName: pod.serviceName || '-',
        pod: containerName,
        level: parsed.level,
        message: parsed.message,
        errorContext: Boolean(parsed.errorContext)
      })
    }
  }
  return rows
}

function appendRuntimeLogRows(rows: RuntimeLogRow[]) {
  if (!rows.length) {
    return
  }
  if (runtimeLogPaused.value) {
    runtimeLogPendingRows.value = boundedRuntimeLogRows([...runtimeLogPendingRows.value, ...rows])
    return
  }
  runtimeLogRows.value = boundedRuntimeLogRows([...runtimeLogRows.value, ...rows])
  if (runtimeLogAutoScroll.value) {
    void scrollRuntimeLogsToBottom()
  }
}

function boundedRuntimeLogRows(rows: RuntimeLogRow[]) {
  if (rows.length <= runtimeLogMaxRows) {
    return rows
  }
  const dropped = rows.length - runtimeLogMaxRows
  runtimeLogDroppedRows.value += dropped
  return rows.slice(dropped)
}

function toggleRuntimeLogPaused() {
  runtimeLogPaused.value = !runtimeLogPaused.value
  if (!runtimeLogPaused.value && runtimeLogPendingRows.value.length) {
    const pending = runtimeLogPendingRows.value
    runtimeLogPendingRows.value = []
    appendRuntimeLogRows(pending)
  }
}

function clearRuntimeLogView() {
  const shouldRestart = runtimeLogSelectionReady.value && (Boolean(runtimeLogSource) || runtimeLogsLoadedForCurrentScope.value)
  runtimeLogSinceSeconds.value = -1
  runtimeLogRows.value = []
  runtimeLogPendingRows.value = []
  runtimeLogs.value = {
    ...runtimeLogs.value,
    pods: runtimeLogGroups.value.map((pod) => ({ ...pod, logs: [], lineCount: 0 }))
  }
  runtimeLogDroppedRows.value = 0
  resetRuntimeLogViewport()
  runtimeLogParseContexts.clear()
  if (shouldRestart) {
    openRuntimeLogStream(true)
  }
}

function showRuntimeLogErrorsOnly() {
  runtimeLogLevelFilter.value = ['ERROR']
}

function clearRuntimeLogServiceFilter() {
  runtimeLogServiceFilter.value = []
}

async function loadActive(force = false) {
  return withLoading(async () => {
    if (tab.value === 'overview') {
      await loadSummary(true, force)
    } else if (tab.value === 'aifar-runtime') {
      if (runtimeResourceTab.value === 'logs') {
        loadRuntimeLogs(force)
      } else if (runtimeResourceTab.value === 'releases') {
        await loadAifarReleases(force)
      } else {
        await loadAifarRuntime(force)
      }
    } else {
      await loadCollection(force)
    }
  })
}

function trackTask(taskId?: string, label = '') {
  if (taskId) {
    taskProgress.track(taskId, label)
  }
}

async function deleteImage(row: any) {
  await removeImages([row], 'single')
}

async function deleteSelectedImages() {
  await removeImages(selectedImageRows.value, 'batch')
}

async function removeImages(rows: any[], mode: 'single' | 'batch') {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const ids = uniqueValues(rows.map(imageReference).filter(Boolean))
  if (!ids.length) {
    ElMessage.warning(mode === 'batch' ? t('containers.selectImages') : t('containers.selectImage'))
    return
  }
  try {
    const message = mode === 'batch'
      ? t('containers.confirmDeleteSelectedImages', { count: ids.length })
      : t('containers.confirmDeleteImage', { image: ids[0] })
    const title = mode === 'batch' ? t('containers.batchDeleteImages') : t('containers.deleteImage')
    await ElMessageBox.confirm(message, title, {
      type: 'warning',
      confirmButtonText: title,
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  try {
    const result = await removeDockerImages(query, ids, mode)
    trackTask(result.taskId, mode === 'batch' ? t('containers.batchDeleteImages') : t('containers.deleteImage'))
    ElMessage.success(mode === 'batch' ? t('containers.imageBatchRemoveAccepted') : t('containers.imageRemoveAccepted'))
    selectedImageRows.value = []
    setTimeout(() => {
      void load(true)
    }, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.imageRemoveFailed'))
  }
}

function onImageSelectionChange(rows: any[]) {
  selectedImageRows.value = rows.filter((row) => imageReference(row))
}

function aifarRuntimeStatusLabel(status?: string) {
  return formatAifarRuntimeStatusLabel(status, t)
}

function releaseKindLabel(kind?: string) {
  return formatReleaseKindLabel(kind, t)
}

function releaseStatusLabel(status?: string) {
  return formatReleaseStatusLabel(status, t)
}

function releaseCurrentServicesText(row: AifarRelease) {
  return Array.isArray(row.currentServices)
    ? row.currentServices.filter((service) => typeof service === 'string' && service.trim() !== '').join(', ')
    : ''
}

function releaseIsCurrent(row: AifarRelease) {
  return Boolean(releaseCurrentServicesText(row))
}

function runtimeApplyStatusLabel(status?: string) {
  return formatRuntimeApplyStatusLabel(status, t)
}

function runtimeInstanceLabel(instance: AifarRuntimeInstance) {
  return formatRuntimeInstanceLabel(instance, t)
}

function releaseRollbackDisabledReason(row: AifarRelease) {
  return runtimeReleaseRollbackDisabledReason(row, {
    canManage: canManageApps.value,
    deniedText: deniedText.value,
    t
  })
}

function releaseDeleteDisabledReason(row: AifarRelease) {
  return runtimeReleaseDeleteDisabledReason(row, {
    canManage: canManageApps.value,
    deniedText: deniedText.value,
    t
  })
}

async function deleteAifarRelease(row: AifarRelease) {
  const reason = releaseDeleteDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instance = selectedRuntimeInstance.value
  if (!instance?.id) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(
      t('containers.confirmDeleteRelease', { release: row.releaseId }),
      t('containers.deleteRelease'),
      {
        confirmButtonText: t('containers.deleteRelease'),
        cancelButtonText: t('common.cancel'),
        type: 'warning',
        confirmButtonClass: 'el-button--danger'
      }
    )
  } catch {
    return
  }
  releaseDeletingId.value = row.releaseId
  try {
    await deleteAifarReleaseRequest(instance.id, row.releaseId)
    aifarReleaseCache.value = {}
    await loadAifarReleases(true)
    ElMessage.success(t('containers.releaseDeleted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.releaseDeleteFailed'))
  } finally {
    if (releaseDeletingId.value === row.releaseId) {
      releaseDeletingId.value = ''
    }
  }
}

async function rollbackAifarRelease(row: AifarRelease) {
  const reason = releaseRollbackDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const services = runtimeReleaseRollbackServices(row)
  if (!services.length) {
    ElMessage.warning(t('containers.releaseRollbackUnavailable'))
    return
  }
  const instance = selectedRuntimeInstance.value
  if (!instance?.id) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  let payload: Awaited<ReturnType<typeof promptRuntimeReleaseRollback>>
  try {
    payload = await promptRuntimeReleaseRollback(
      row,
      t,
      (message, title, options) => ElMessageBox.prompt(message, title, options)
    )
  } catch {
    return
  }
  try {
    const result = await rollbackAifarReleaseRequest(instance.id, payload)
    trackTask(result.taskId, t('containers.rollbackRelease'))
    ElMessage.success(t('containers.rollbackAccepted'))
    aifarReleaseCache.value = {}
    setTimeout(() => {
      void loadAifarRuntime(true)
      void loadAifarReleases(true)
    }, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.rollbackFailed'))
  }
}

function runtimeServiceForDeployment(row: AifarRuntimeDeployment): AifarRuntimeService {
  return resolveRuntimeServiceForDeployment(row, runtimeServiceMap.value)
}

function openRuntimeConfigDialog() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const state = selectedRuntimeConfig.value
  const global = normalizedRuntimeValues(state.global)
  runtimeConfigForm.value = { ...global, nacosEphemeral: state.nacosEphemeral !== false }
  const overrides = state.services || {}
  const services = selectedRuntimeServices.value.length
    ? selectedRuntimeServices.value.map((item) => item.serviceName)
    : aifarServiceOptions.value.map((item) => item.value)
  runtimeConfigRows.value = services.map((serviceName) => {
    const values = overrides[serviceName] || {}
    return {
      serviceName,
      appCPUs: String(values.appCPUs || '').trim(),
      appMemoryLimit: String(values.appMemoryLimit || '').trim(),
      jvmInitialRAMPercentage: serviceName === 'web-vue3' ? '' : runtimeConfigNumberText(values.jvmInitialRAMPercentage),
      jvmMaxRAMPercentage: serviceName === 'web-vue3' ? '' : runtimeConfigNumberText(values.jvmMaxRAMPercentage)
    }
  })
  runtimeConfigVisible.value = true
}

function openAifarRuntimeServiceUpdate(row: AifarRuntimeService) {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instance = selectedRuntimeAppInstance.value
  if (!instance) {
    ElMessage.warning(t('containers.updateServiceInstanceMissing'))
    return
  }
  aifarUpdateInstanceOverride.value = instance
  aifarUpdateTargetLabel.value = row.serviceName
  aifarUpdateMode.value = 'single'
  aifarUpdateService.value = row.serviceName || 'oauth'
  aifarArtifactFile.value = null
  aifarUpdateVisible.value = true
}

function openAifarRuntimeBundleUpdate() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instance = selectedRuntimeAppInstance.value
  if (!instance) {
    ElMessage.warning(t('containers.updateServiceInstanceMissing'))
    return
  }
  aifarUpdateInstanceOverride.value = instance
  aifarUpdateTargetLabel.value = t('apps.aifarUpdateAllServices')
  aifarUpdateMode.value = 'bundle'
  aifarArtifactFile.value = null
  aifarUpdateVisible.value = true
}

function handleAifarArtifactChange(file: UploadFile) {
  aifarArtifactFile.value = file.raw ?? null
}

function clearAifarArtifact() {
  aifarArtifactFile.value = null
}

async function submitAifarUpdate() {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const instance = selectedAifarUpdateInstance.value
  if (!instance || !aifarArtifactFile.value) {
    ElMessage.warning(t('apps.aifarUpdateArtifactRequired'))
    return
  }
  const uploadLimit = Number(appSettings.value.maxRequestBodyBytes || 0)
  if (isAifarArtifactTooLarge(aifarArtifactFile.value, uploadLimit)) {
    ElMessage.error(t('apps.aifarUpdateArtifactTooLarge', {
      size: formatBytes(aifarArtifactFile.value.size),
      limit: formatBytes(uploadLimit)
    }))
    return
  }
  const form = buildAifarArtifactForm(aifarUpdateMode.value, aifarUpdateService.value, aifarArtifactFile.value)
  aifarUpdateSubmitting.value = true
  try {
    const result = await updateAifarArtifact(instance.id, form, aifarUpdateMode.value)
    aifarUpdateVisible.value = false
    aifarArtifactFile.value = null
    aifarUpdateInstanceOverride.value = null
    aifarUpdateTargetLabel.value = ''
    trackTask(result.taskId, t('apps.updateService'))
    ElMessage.success(t('apps.aifarUpdateAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.aifarUpdateFailed'))
  } finally {
    aifarUpdateSubmitting.value = false
  }
}

async function submitRuntimeConfig() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  const global = normalizedRuntimeValues(runtimeConfigForm.value)
  const validation = validateRuntimeConfigValues(global, t)
  if (validation) {
    ElMessage.warning(validation)
    return
  }
  let services: Record<string, RuntimeConfigValues>
  try {
    services = buildRuntimeServiceOverrides(runtimeConfigRows.value, runtimeConfigForm.value, t)
  } catch (err) {
    ElMessage.warning(err instanceof Error ? err.message : t('containers.runtimeConfigInvalid'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmApplyRuntimeConfig'), t('containers.runtimeConfig'), {
      type: 'warning',
      confirmButtonText: t('containers.applyRuntimeConfig'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  runtimeConfigSubmitting.value = true
  try {
    const result = await applyRuntimeConfig(query, {
      instanceId,
      global,
      services,
      nacosEphemeral: runtimeConfigForm.value.nacosEphemeral
    })
    runtimeConfigVisible.value = false
    ElMessage.success(t('containers.runtimeConfigApplyStarted'))
    trackTask(result.taskId, t('containers.runtimeConfig'))
    void loadAifarRuntime(true)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  } finally {
    runtimeConfigSubmitting.value = false
  }
}

async function submitRuntimeReconcile(labelKey: string, confirmKey: string) {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(t(confirmKey), t(labelKey), {
      type: 'warning',
      confirmButtonText: t(labelKey),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  try {
    const result = await reconcileRuntime(query, instanceId)
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, t(labelKey))
    void loadAifarRuntime(true)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function reconcileAifarRuntime() {
  await submitRuntimeReconcile('containers.reconcileRuntime', 'containers.confirmReconcileRuntime')
}

async function restartAllAifarRuntime() {
  const disabledReason = runtimeRestartDisabledReason.value
  if (disabledReason) {
    ElMessage.warning(disabledReason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(
      t('containers.confirmRestartAllRuntime', {
        services: runtimeRestartScope.value.services,
        replicas: runtimeRestartScope.value.replicas
      }),
      t('containers.restartAllRuntime'),
      {
        type: 'warning',
        confirmButtonText: t('containers.restartAllRuntime'),
        cancelButtonText: t('common.cancel')
      }
    )
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  runtimeRestartSubmitting.value = true
  try {
    const result = await restartAllRuntime(query, instanceId, t('containers.restartAllRuntimeReason'))
    ElMessage.success(t('containers.restartAllRuntimeAccepted'))
    trackTask(result.taskId, t('containers.restartAllRuntime'))
    runtimeCache.value = {}
    void loadAifarRuntime(true)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  } finally {
    runtimeRestartSubmitting.value = false
  }
}

function openRuntimePodLogs(row: AifarRuntimePod) {
  const service = String(row?.serviceName || '').trim()
  const pod = String(row?.containerName || '').trim()
  if (!service && !pod) {
    ElMessage.warning(t('containers.selectRuntimeLogScope'))
    return
  }
  runtimeResourceTab.value = 'logs'
  runtimeLogServiceFilter.value = service ? [service] : []
  runtimeLogPodFilter.value = pod ? [pod] : []
  runtimeLogLevelFilter.value = []
  runtimeLogKeyword.value = ''
  void nextTick().then(() => loadRuntimeLogs(true))
}

async function cleanupAifarRuntimeStale() {
  const reason = runtimeCleanupDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmCleanupStaleRuntime', { count: staleRuntimePodCount.value }), t('containers.cleanupStaleRuntime'), {
      type: 'warning',
      confirmButtonText: t('containers.cleanupStaleRuntime'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  try {
    const result = await cleanupStaleRuntime(query, instanceId)
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, t('containers.cleanupStaleRuntime'))
    void loadAifarRuntime(true)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

function openServiceInstallDialog() {
  const reason = serviceInstallDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  serviceInstallSelection.value = []
  serviceInstallVisible.value = true
}

async function submitAifarServiceInstall() {
  const reason = serviceInstallDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  const services = [...serviceInstallSelection.value]
  if (!services.length) {
    ElMessage.warning(t('containers.selectServicesToInstall'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmInstallServices', { services: services.join(', ') }), t('containers.installServices'), {
      type: 'warning',
      confirmButtonText: t('containers.installServices'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  serviceInstallSubmitting.value = true
  try {
    const result = await installRuntimeServices(query, {
      instanceId,
      services
    })
    serviceInstallVisible.value = false
    serviceInstallSelection.value = []
    ElMessage.success(t('containers.serviceInstallAccepted'))
    trackTask(result.taskId, t('containers.installServices'))
    void loadAifarRuntime(true)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.serviceInstallFailed'))
  } finally {
    serviceInstallSubmitting.value = false
  }
}

async function scaleOutAifarService(service: string) {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  await submitAifarScaleOut(service, instanceId, () => {
    void loadAifarRuntime(true)
  })
}

function aifarRuntimeScaleInDisabledReason(row: AifarRuntimeDeployment) {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  const desired = Number(row.desiredReplicas ?? 0)
  if (!Number.isFinite(desired) || desired <= 0) return t('containers.serviceAlreadyOffline')
  if (desired <= 1) return t('containers.serviceScaleInMinimum')
  return ''
}

async function scaleInAifarDeployment(row: AifarRuntimeDeployment) {
  const reason = aifarRuntimeScaleInDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  const currentReplicas = Number(row.desiredReplicas ?? 0)
  await submitAifarScaleIn(row.serviceName, instanceId, currentReplicas, currentReplicas - 1, () => {
    void loadAifarRuntime(true)
  })
}

function aifarRuntimeOfflineDisabledReason(row: AifarRuntimeService) {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  if (Number(row.desiredReplicas || 0) === 0) return t('containers.serviceAlreadyOffline')
  return ''
}

async function offlineAifarService(row: AifarRuntimeService) {
  const reason = aifarRuntimeOfflineDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  await submitAifarOffline(row.serviceName, instanceId, () => {
    void loadAifarRuntime(true)
  })
}

async function offlineAifarServices(rows: AifarRuntimeService[]) {
  const services = [...new Set(rows.map((row) => row.serviceName.trim()).filter(Boolean))]
  if (!services.length) {
    ElMessage.warning(t('containers.selectDeploymentsToOffline'))
    return false
  }
  for (const row of rows) {
    const reason = aifarRuntimeOfflineDisabledReason(row)
    if (reason) {
      ElMessage.warning(reason)
      return false
    }
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return false
  }
  try {
    await ElMessageBox.confirm(
      t('containers.confirmBatchOfflineDeployments', { count: services.length, services: services.join(', ') }),
      t('containers.batchOfflineDeployments'),
      {
        type: 'warning',
        confirmButtonText: t('containers.batchOfflineDeployments'),
        cancelButtonText: t('common.cancel')
      }
    )
  } catch {
    return false
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return false
  }
  try {
    const result = await offlineRuntimeServicesRequest(query, instanceId, services)
    ElMessage.success(t('containers.batchOfflineAccepted'))
    trackTask(result.taskId, `${t('containers.batchOfflineDeployments')} ${services.join(', ')}`)
    void loadAifarRuntime(true)
    return true
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
    return false
  }
}

async function submitAifarScaleOut(service: string, instanceId: string, afterSubmitted?: () => void) {
  try {
    await ElMessageBox.confirm(t('containers.confirmScaleOut', { service }), t('containers.scaleOut'), {
      type: 'warning',
      confirmButtonText: t('containers.scaleOut'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  try {
    const result = await scaleOutRuntimeService(query, service, instanceId)
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, `${t('containers.scaleOut')} ${service}`)
    afterSubmitted?.()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function submitAifarScaleIn(service: string, instanceId: string, currentReplicas: number, nextReplicas: number, afterSubmitted?: () => void) {
  try {
    await ElMessageBox.confirm(t('containers.confirmScaleIn', { service, current: currentReplicas, next: nextReplicas }), t('containers.scaleIn'), {
      type: 'warning',
      confirmButtonText: t('containers.scaleIn'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  try {
    const result = await scaleInRuntimeService(query, service, instanceId)
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, `${t('containers.scaleIn')} ${service}`)
    afterSubmitted?.()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function submitAifarOffline(service: string, instanceId: string, afterSubmitted?: () => void) {
  try {
    await ElMessageBox.confirm(t('containers.confirmOfflineDeployment', { service }), t('containers.offlineDeployment'), {
      type: 'warning',
      confirmButtonText: t('containers.offlineDeployment'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  try {
    const result = await offlineRuntimeService(query, service, instanceId)
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, `${t('containers.offlineDeployment')} ${service}`)
    afterSubmitted?.()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

useAifarRuntimeProvider({
  t,
  loading,
  aifarRuntime,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  selectedRuntimeInstanceId,
  runtimeTargetQuery: targetQuery,
  aifarRuntimeInstances,
  runtimeInstanceLabel,
  aifarRuntimeActionDisabledReason,
  openRuntimeConfigDialog,
  serviceInstallDisabledReason,
  openServiceInstallDialog,
  openAifarRuntimeBundleUpdate,
  reconcileAifarRuntime,
  runtimeRestartDisabledReason,
  runtimeRestartSubmitting,
  restartAllAifarRuntime,
  runtimeCleanupDisabledReason,
  cleanupAifarRuntimeStale,
  loadAifarRuntime,
  aifarRuntimeWarnings,
  runtimeSummaryItems,
  runtimeResourceTab,
  selectedRuntimeDeployments,
  runtimeDeploymentReplicaText,
  openAifarRuntimeServiceUpdate,
  runtimeServiceForDeployment,
  scaleOutAifarService,
  aifarRuntimeScaleInDisabledReason,
  scaleInAifarDeployment,
  aifarRuntimeOfflineDisabledReason,
  offlineAifarService,
  offlineAifarServices,
  aifarReleases,
  loadAifarReleases,
  releaseKindLabel,
  releaseStatusLabel,
  releaseServicesText,
  releaseCurrentServicesText,
  releaseIsCurrent,
  formatDate,
  releaseRollbackDisabledReason,
  rollbackAifarRelease,
  releaseDeletingId,
  releaseDeleteDisabledReason,
  deleteAifarRelease,
  selectedRuntimeServices,
  runtimeEndpointText,
  percentText,
  runtimePodServiceFilter,
  clearRuntimePodServiceFilter,
  installedRuntimeServiceNamesList,
  ensureRuntimePodsLoaded,
  refreshRuntimePodBase,
  refreshRuntimePodMetrics,
  runtimePodsLoadedForCurrentScope,
  selectedRuntimePods,
  openRuntimePodLogs,
  runtimeLogServiceFilter,
  clearRuntimeLogServiceFilter,
  runtimeLogPodFilter,
  runtimeLogPodOptions,
  runtimeLogLevelFilter,
  runtimeLogKeyword,
  runtimeLogTail,
  runtimeLogSelectionReady,
  loadRuntimeLogs,
  runtimeLogsLoadedForCurrentScope,
  toggleRuntimeLogPaused,
  runtimeLogPaused,
  runtimeLogRows,
  runtimeLogErrorCount,
  showRuntimeLogErrorsOnly,
  runtimeLogPendingCount,
  clearRuntimeLogView,
  runtimeLogAutoScroll,
  runtimeLogWarnings,
  runtimeLogGroups,
  runtimeLogStreamTagType,
  runtimeLogStreamStatusLabel,
  runtimeLogLastDataAt,
  filteredRuntimeLogRows,
  runtimeLogDroppedRows,
  runtimeLogViewport,
  handleRuntimeLogScroll,
  runtimeLogTopSpacer,
  runtimeLogVirtualRows,
  runtimeLogBottomSpacer,
  runtimeEntryRoutes,
  runtimeDiscoveryTarget,
  runtimeNacosStatus,
  aifarUpdateVisible,
  selectedAifarContainerLabel,
  selectedAifarInstanceLabel,
  aifarUpdateModeLabel,
  aifarUpdateMode,
  aifarUpdateService,
  aifarArtifactAccept,
  handleAifarArtifactChange,
  clearAifarArtifact,
  aifarArtifactHint,
  aifarUpdateSubmitting,
  submitAifarUpdate,
  serviceInstallVisible,
  missingRuntimeServiceOptions,
  serviceInstallSelection,
  serviceInstallSubmitting,
  submitAifarServiceInstall,
  runtimeConfigVisible,
  runtimeConfigMetaItems,
  runtimeConfigForm,
  runtimeConfigRows,
  runtimeConfigSubmitting,
  submitRuntimeConfig
})

watch(tab, async (next) => {
  if (next !== 'aifar-runtime') {
    runtimePodMetricsScheduler.stop()
    closeRuntimeLogStream()
  }
  await loadActive(false)
  if (next === 'aifar-runtime' && runtimeResourceTab.value === 'pods') {
    void activateRuntimePods('enter')
  }
})
watch(resourceTab, () => {
  if (tab.value === 'images') {
    void loadActive(false)
  }
})
watch(runtimeResourceTab, (next) => {
  if (next === 'pods') {
    void activateRuntimePods('enter')
  } else if (next === 'releases') {
    runtimePodMetricsScheduler.stop()
    closeRuntimeLogStream()
    void loadAifarReleases(false)
  } else if (next === 'logs') {
    runtimePodMetricsScheduler.stop()
    void ensureRuntimePodsLoaded(...runtimePodLoadArgs('logs'))
    if (runtimeLogSelectionReady.value) {
      void loadRuntimeLogs(false)
    }
  } else {
    runtimePodMetricsScheduler.stop()
    closeRuntimeLogStream()
  }
})
watch(selectedRuntimeInstanceId, () => {
  runtimePodMetricsScheduler.stop()
  runtimePodServiceFilter.value = ''
  runtimeLogServiceFilter.value = []
  runtimeLogPodFilter.value = []
  runtimeLogLevelFilter.value = []
  runtimeLogKeyword.value = ''
  runtimeLogSinceSeconds.value = 0
  resetRuntimeLogView()
  runtimeLogsLoaded.value = {}
  aifarReleases.value = []
  closeRuntimeLogStream()
  if (runtimeResourceTab.value === 'pods') {
    void activateRuntimePods('scope-change')
  } else if (runtimeResourceTab.value === 'logs') {
    void ensureRuntimePodsLoaded(...runtimePodLoadArgs('logs'))
  } else if (runtimeResourceTab.value === 'releases') {
    void loadAifarReleases(false)
  }
})
watch(runtimeLogServiceFilter, () => {
  const validPods = new Set(runtimeLogPodOptions.value.map((item) => item.value))
  runtimeLogPodFilter.value = runtimeLogPodFilter.value.filter((pod) => validPods.has(pod))
}, { deep: true })
watch([runtimeLogServiceFilter, runtimeLogPodFilter, runtimeLogTail], () => {
  runtimeLogSinceSeconds.value = 0
  resetRuntimeLogView()
  runtimeLogsLoaded.value = {}
  closeRuntimeLogStream()
  if (runtimeResourceTab.value === 'logs' && runtimeLogSelectionReady.value) {
    loadRuntimeLogs(true)
  }
}, { deep: true })
watch(
  () => [filteredRuntimeLogRows.value.length, runtimeLogLevelFilter.value.join(','), runtimeLogKeyword.value],
  () => {
    if (runtimeLogAutoScroll.value) {
      void scrollRuntimeLogsToBottom()
    }
  },
  { flush: 'post' }
)
watch(() => realtime.revision, () => {
  const event = realtime.lastEvent
  if (!event?.resource) {
    return
  }
  if (event.resource === 'docker.summary' && event.serverId === selectedServerId.value) {
    if (!applyDockerSummaryEvent(event)) {
      summaryCache.value = {}
    }
    return
  }
  if (isRuntimeStatusEventForSelection(event, selectedServerId.value, tab.value === 'aifar-runtime')) {
    runtimeCache.value = {}
    runtimeStatusRefresh.request()
  }
})
watch([aifarUpdateService, aifarUpdateMode], () => {
  aifarArtifactFile.value = null
})
watch(selectedServerId, async () => {
  runtimePodMetricsScheduler.stop()
  runtimeLogServiceFilter.value = []
  runtimeLogPodFilter.value = []
  runtimeLogLevelFilter.value = []
  runtimeLogKeyword.value = ''
  runtimeLogSinceSeconds.value = 0
  resetRuntimeLogView()
  runtimeLogsLoaded.value = {}
  closeRuntimeLogStream()
  if (pageReady.value) {
    await load(true)
    if (runtimeResourceTab.value === 'pods') {
      void activateRuntimePods('scope-change')
    }
  }
})
onMounted(async () => {
  await loadServers()
  pageReady.value = true
  await load()
})
onBeforeUnmount(() => {
  runtimeStatusRefresh.dispose()
  runtimePodMetricsScheduler.dispose()
  closeRuntimeLogStream()
})
</script>

<style scoped>
.containers-page.is-runtime-logs-page {
  overflow: hidden;
  padding-right: 0;
}

.containers-main {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  padding: 10px;
}

.workspace-card.containers-main.is-runtime-logs {
  flex: 1 1 0;
  height: 100%;
  min-height: 0;
  overflow: hidden;
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

.resource-tabs {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.resource-tabs :deep(.el-tabs__content) {
  flex: 1 1 auto;
  min-height: 0;
}

.resource-tabs :deep(.el-tab-pane) {
  height: 100%;
}

.resource-panel {
  height: 100%;
  min-height: 360px;
  display: flex;
  flex-direction: column;
  gap: 8px;
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

@media (max-width: 900px), (max-height: 600px) {
  .containers-page.is-runtime-logs-page {
    overflow-x: hidden;
    overflow-y: auto;
    padding-right: 2px;
  }

  .workspace-card.containers-main.is-runtime-logs {
    flex: 0 0 auto;
    height: auto;
    overflow: visible;
  }
}
</style>
