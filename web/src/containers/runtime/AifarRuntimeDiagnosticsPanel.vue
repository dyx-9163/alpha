<template>
  <section class="runtime-diagnostics-panel">
    <div class="runtime-diagnostics-head">
      <div>
        <h3>{{ t('containers.diagnosticsRecords') }}</h3>
        <p>{{ t('containers.diagnosticsConservativeHint') }}</p>
      </div>
      <el-button size="small" type="primary" :disabled="!instanceId" @click="openDialog">{{ t('containers.diagnosticsExport') }}</el-button>
    </div>

    <el-table :data="exportsPage.items" class="runtime-diagnostics-table" max-height="280">
      <el-table-column :label="t('common.status')" width="152">
        <template #default="{ row }">
          <el-tag size="small" :type="diagnosticStatusType(row)">{{ t(runtimeDiagnosticStatusKey(row)) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.diagnosticsTimeRange')" min-width="210">
        <template #default="{ row }">{{ formatRange(row) }}</template>
      </el-table-column>
      <el-table-column :label="t('containers.diagnosticsServices')" min-width="160" show-overflow-tooltip>
        <template #default="{ row }">{{ row.services.join(', ') || '-' }}</template>
      </el-table-column>
      <el-table-column :label="t('containers.size')" width="110">
        <template #default="{ row }">{{ formatBytes(row.archiveBytes) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.message')" min-width="130" show-overflow-tooltip>
        <template #default="{ row }">
          <el-tooltip v-if="row.warnings?.length" :content="row.warnings.join('；')" placement="top">
            <el-tag size="small" type="warning">{{ row.warningCount }}</el-tag>
          </el-tooltip>
          <span v-else>{{ row.warningCount || '-' }}</span>
        </template>
      </el-table-column>
      <el-table-column :label="t('common.time')" min-width="170">
        <template #default="{ row }">{{ formatDate(row.createdAt) }} / {{ formatDate(row.expiresAt) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.operation')" width="192" fixed="right">
        <template #default="{ row }">
          <div class="row-actions runtime-diagnostics-actions">
            <el-button v-if="row.taskId" size="small" text @click="openTask(row.taskId)">{{ t('common.details') }}</el-button>
            <el-button size="small" text type="primary" :disabled="row.status !== 'ready'" @click="download(row)">{{ t('common.download') }}</el-button>
            <el-button size="small" text type="danger" :disabled="row.status === 'deleted'" @click="remove(row)">{{ t('common.delete') }}</el-button>
          </div>
        </template>
      </el-table-column>
      <template #empty><span>{{ t('containers.diagnosticsNoRecords') }}</span></template>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="t('containers.diagnosticsExportTitle')" width="640px" class="runtime-diagnostics-dialog" @closed="resetDialog">
      <el-form label-position="top">
        <el-form-item :label="t('containers.diagnosticsTimeRange')">
          <el-radio-group v-model="mode">
            <el-radio-button value="last2h">{{ t('containers.diagnosticsLast2Hours') }}</el-radio-button>
            <el-radio-button value="custom">{{ t('containers.diagnosticsCustomRange') }}</el-radio-button>
          </el-radio-group>
          <el-date-picker
            v-if="mode === 'custom'"
            v-model="diagnosticRange"
            class="runtime-diagnostics-range"
            type="datetimerange"
            :start-placeholder="t('containers.diagnosticsTimeRange')"
            :end-placeholder="t('common.time')"
          />
        </el-form-item>
        <el-form-item :label="t('containers.diagnosticsServices')">
          <el-checkbox-group v-model="selectedServices" class="runtime-diagnostics-services">
            <el-checkbox v-for="service in availableServices" :key="service" :value="service">{{ service }}</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <div class="runtime-diagnostics-estimate-actions">
          <el-button :loading="estimating" @click="runEstimate">{{ estimating ? t('containers.diagnosticsEstimating') : t('containers.diagnosticsEstimate') }}</el-button>
        </div>
      </el-form>

      <div v-if="estimate" class="runtime-diagnostics-estimate">
        <div><span>{{ t('containers.diagnosticsFileBytes') }}</span><strong>{{ formatBytes(estimate.fileBytes) }}</strong></div>
        <div><span>{{ t('containers.diagnosticsContainerBytes') }}</span><strong>{{ formatBytes(estimate.containerBytes) }}</strong></div>
        <div><span>{{ t('containers.diagnosticsRequiredBytes') }}</span><strong>{{ formatBytes(estimate.requiredBytes) }}</strong></div>
        <div><span>{{ t('containers.diagnosticsAvailableBytes') }}</span><strong>{{ formatBytes(estimate.availableBytes) }}</strong></div>
        <el-alert
          v-if="!estimate.allowed"
          type="error"
          :closable="false"
          show-icon
          :title="estimate.requiredBytes > threeGiB ? t('containers.diagnosticsOverLimit') : t('containers.diagnosticsInsufficientSpace')"
        />
        <el-alert v-else type="info" :closable="false" show-icon :title="t('containers.diagnosticsConservativeHint')" />
      </div>

      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="submitting" :disabled="Boolean(submitDisabledReason)" @click="submit">
          {{ t('containers.diagnosticsStart') }}
        </el-button>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'
import { useI18n } from '../../i18n'
import { useTaskProgressStore } from '../../stores/taskProgress'
import { formatBytes } from './artifacts'
import {
  createRuntimeDiagnosticExport,
  deleteRuntimeDiagnosticExport,
  downloadRuntimeDiagnosticExport,
  estimateRuntimeDiagnostics,
  fetchRuntimeDiagnosticExports
} from './api'
import {
  defaultRuntimeDiagnosticWindow,
  emptyRuntimeDiagnosticExportPage,
  enabledRuntimeDiagnosticServices,
  runtimeDiagnosticExportScopeFingerprint,
  runtimeDiagnosticRequestFingerprint,
  runtimeDiagnosticStatusKey,
  runtimeDiagnosticSubmitDisabledReason,
  trackRuntimeDiagnosticTask,
  terminalDiagnosticTaskToRefresh
} from './runtimeDiagnostics'
import type { AifarRuntimeDeployment, RuntimeDiagnosticEstimate, RuntimeDiagnosticExport, RuntimeDiagnosticExportPage, RuntimeDiagnosticRequest } from './types'

const props = defineProps<{
  instanceId: string
  deployments: AifarRuntimeDeployment[]
  targetQuery: string
}>()

const { t } = useI18n()
const router = useRouter()
const taskProgress = useTaskProgressStore()
const dialogVisible = ref(false)
const mode = ref<'last2h' | 'custom'>('last2h')
const selectedServices = ref<string[]>([])
const sinceAt = ref<Date>()
const untilAt = ref<Date>()
const estimate = ref<RuntimeDiagnosticEstimate | null>(null)
const estimateFingerprint = ref('')
const exportsPage = ref<RuntimeDiagnosticExportPage>(emptyRuntimeDiagnosticExportPage())
const estimating = ref(false)
const submitting = ref(false)
const trackedTaskIds = new Set<string>()
const refreshedTaskIds = new Set<string>()
const threeGiB = 3 * 1024 * 1024 * 1024
const objectUrlCleanupDelayMs = 30 * 1000
let exportsRequestSequence = 0
let estimateRequestSequence = 0

const availableServices = computed(() => enabledRuntimeDiagnosticServices(props.deployments))
const diagnosticRange = computed({
  get: (): [Date, Date] | undefined => sinceAt.value && untilAt.value ? [sinceAt.value, untilAt.value] : undefined,
  set: (value: [Date, Date] | undefined) => {
    sinceAt.value = value?.[0]
    untilAt.value = value?.[1]
  }
})
const diagnosticRequestFingerprint = computed(() => {
  const request = payload()
  return request ? runtimeDiagnosticRequestFingerprint(props.targetQuery, request) : ''
})
const currentEstimate = computed(() => estimateFingerprint.value === diagnosticRequestFingerprint.value ? estimate.value : null)
const submitDisabledReason = computed(() => runtimeDiagnosticSubmitDisabledReason({
  services: selectedServices.value,
  estimate: currentEstimate.value,
  estimating: estimating.value,
  submitting: submitting.value
}))

watch(mode, (next) => {
  if (next === 'last2h') resetDiagnosticWindow()
  invalidateEstimate()
})
watch([selectedServices, sinceAt, untilAt], invalidateEstimate, { deep: true })
watch(() => [props.instanceId, props.targetQuery], () => {
  invalidateEstimate()
  exportsPage.value = emptyRuntimeDiagnosticExportPage()
  void loadExports()
}, { immediate: true })
watch(() => taskProgress.items.map(({ id, status }) => ({ id, status })), (items) => {
  const taskIds = terminalDiagnosticTaskToRefresh(items, trackedTaskIds, refreshedTaskIds)
  if (taskIds.length) {
    taskIds.forEach((taskId) => refreshedTaskIds.add(taskId))
    void loadExports()
  }
}, { deep: true })

function openDialog() {
  mode.value = 'last2h'
  resetDiagnosticWindow()
  selectedServices.value = availableServices.value
  invalidateEstimate()
  dialogVisible.value = true
}

function resetDialog() {
  invalidateEstimate()
}

function resetDiagnosticWindow() {
  const window = defaultRuntimeDiagnosticWindow()
  sinceAt.value = window.sinceAt
  untilAt.value = window.untilAt
}

function invalidateEstimate() {
  estimateRequestSequence++
  estimate.value = null
  estimateFingerprint.value = ''
  estimating.value = false
}

function payload(): RuntimeDiagnosticRequest | null {
  if (!props.instanceId || !sinceAt.value || !untilAt.value) return null
  return {
    instanceId: props.instanceId,
    sinceAt: sinceAt.value.toISOString(),
    untilAt: untilAt.value.toISOString(),
    services: selectedServices.value
  }
}

async function runEstimate() {
  const request = payload()
  const requestFingerprint = request ? runtimeDiagnosticRequestFingerprint(props.targetQuery, request) : ''
  if (!request || !requestFingerprint) return
  const requestSequence = ++estimateRequestSequence
  estimate.value = null
  estimateFingerprint.value = ''
  estimating.value = true
  try {
    const result = await estimateRuntimeDiagnostics(props.targetQuery, request)
    if (requestSequence === estimateRequestSequence && requestFingerprint === diagnosticRequestFingerprint.value) {
      estimate.value = result
      estimateFingerprint.value = requestFingerprint
    }
  } catch (err) {
    if (requestSequence === estimateRequestSequence) ElMessage.error(errorMessage(err))
  } finally {
    if (requestSequence === estimateRequestSequence) estimating.value = false
  }
}

async function submit() {
  const request = payload()
  if (!request || submitDisabledReason.value) return
  submitting.value = true
  try {
    const result = await createRuntimeDiagnosticExport(props.targetQuery, request)
    trackTask(result.taskId, t('containers.diagnosticsExportTask'))
    dialogVisible.value = false
    await loadExports()
  } catch (err) {
    ElMessage.error(errorMessage(err))
  } finally {
    submitting.value = false
  }
}

async function loadExports() {
  const requestSequence = ++exportsRequestSequence
  const requestScope = runtimeDiagnosticExportScopeFingerprint(props.targetQuery, props.instanceId)
  if (!props.instanceId || !props.targetQuery) {
    if (requestSequence === exportsRequestSequence) {
      exportsPage.value = emptyRuntimeDiagnosticExportPage()
    }
    return
  }
  try {
    const page = await fetchRuntimeDiagnosticExports(props.targetQuery, props.instanceId)
    if (requestSequence === exportsRequestSequence && requestScope === currentExportScope()) {
      exportsPage.value = page
    }
  } catch (err) {
    if (requestSequence === exportsRequestSequence && requestScope === currentExportScope()) ElMessage.error(errorMessage(err))
  }
}

async function download(row: RuntimeDiagnosticExport) {
  const deleteAfterDownload = await chooseDeleteAfterDownload()
  if (deleteAfterDownload === null) return
  try {
    const result = await downloadRuntimeDiagnosticExport(props.targetQuery, row.id, deleteAfterDownload)
    if (deleteAfterDownload) await loadExports()
    if (row.sha256 && result.sha256 && row.sha256.toLowerCase() !== result.sha256.toLowerCase()) {
      ElMessage.error(t('containers.diagnosticsChecksumMismatch'))
      return
    }
    const url = URL.createObjectURL(result.blob)
    const anchor = document.createElement('a')
    anchor.href = url
    anchor.download = result.filename || row.archiveName || `aifar-diagnostics-${row.id}.tar.gz`
    document.body.append(anchor)
    anchor.click()
    anchor.remove()
    window.setTimeout(() => URL.revokeObjectURL(url), objectUrlCleanupDelayMs)
    ElMessage.success(t('containers.diagnosticsDownloadStarted'))
  } catch (err) {
    ElMessage.error(errorMessage(err) || t('containers.diagnosticsDownloadFailed'))
  }
}

async function chooseDeleteAfterDownload(): Promise<boolean | null> {
  try {
    await ElMessageBox.confirm(t('containers.diagnosticsDownloadDeleteChoice'), t('common.download'), {
      type: 'warning',
      confirmButtonText: t('containers.diagnosticsDeleteAfterDownload'),
      cancelButtonText: t('common.download'),
      distinguishCancelAndClose: true
    })
    return true
  } catch (err) {
    if (err === 'cancel') return false
    return null
  }
}

async function remove(row: RuntimeDiagnosticExport) {
  try {
    await ElMessageBox.confirm(t('containers.diagnosticsDeleteConfirm'), t('common.delete'), { type: 'warning' })
    const result = await deleteRuntimeDiagnosticExport(props.targetQuery, row.id)
    trackTask(result.taskId, t('containers.diagnosticsDeleteTask'))
    await loadExports()
  } catch (err) {
    if (err !== 'cancel' && err !== 'close') ElMessage.error(errorMessage(err))
  }
}

function trackTask(taskId: string, label: string) {
  if (!taskId) return
  trackedTaskIds.add(taskId)
  trackRuntimeDiagnosticTask(taskProgress, taskId, label)
}

function currentExportScope() {
  return runtimeDiagnosticExportScopeFingerprint(props.targetQuery, props.instanceId)
}

function openTask(taskId: string) {
  void router.push({ path: '/tasks', query: { taskId } })
}

function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function formatRange(row: RuntimeDiagnosticExport) {
  return `${formatDate(row.sinceAt)} - ${formatDate(row.untilAt)}`
}

function diagnosticStatusType(row: RuntimeDiagnosticExport) {
  if (row.status === 'ready') return row.warningCount ? 'warning' : 'success'
  if (row.status === 'failed') return 'danger'
  if (row.status === 'cancelled' || row.status === 'expired') return 'warning'
  return 'info'
}

function errorMessage(err: unknown) {
  return err instanceof Error ? err.message : String(err || t('containers.diagnosticsDownloadFailed'))
}
</script>
