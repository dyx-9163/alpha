<template>
  <div class="settings-block">
    <label>{{ t('settings.dataMaintenance') }}</label>
    <KeyValueGrid :items="maintenanceItems" class="retention-grid" />
    <div class="retention-actions">
      <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
        <span>
          <el-button :loading="backupRunning" :disabled="!canManage" @click="runDatabaseBackup">
            {{ t('settings.runDatabaseBackup') }}
          </el-button>
        </span>
      </el-tooltip>
      <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
        <span>
          <el-button type="primary" :loading="retentionRunning" :disabled="!canManage" @click="runRetentionCleanup">
            {{ t('settings.runRetentionCleanup') }}
          </el-button>
        </span>
      </el-tooltip>
    </div>
    <span class="subtle-note">{{ t('settings.retentionNote') }}</span>
    <DataTable
      class="backup-table"
      :rows="backups as unknown as Record<string, unknown>[]"
      :columns="backupColumns"
      :title="t('settings.databaseBackups')"
      row-key="name"
      :height="260"
      :loading="backupsLoading"
      :fit="false"
      :table-width="1170"
    >
      <template #toolbar>
        <el-button size="small" :disabled="!canManage" @click="refresh">{{ t('common.refresh') }}</el-button>
      </template>
      <template #size="{ row }">
        {{ formatBytes(row.size) }}
      </template>
      <template #createdAt="{ row }">
        {{ formatDate(row.createdAt) }}
      </template>
      <template #action="{ row }">
        <div class="backup-actions">
          <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
            <span>
              <el-button size="small" :disabled="!canManage" @click="verifyBackup(row.name)">
                {{ t('common.check') }}
              </el-button>
            </span>
          </el-tooltip>
          <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
            <span>
              <el-button size="small" :disabled="!canManage" @click="downloadBackup(row.name)">
                {{ t('common.download') }}
              </el-button>
            </span>
          </el-tooltip>
          <ConfirmAction
            :message="t('settings.confirmDeleteBackup', { name: row.name })"
            :disabled="!canManage"
            type="warning"
            @confirm="deleteBackup(row.name)"
          >
            <template #default="{ confirm }">
              <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
                <span>
                  <el-button size="small" type="danger" :disabled="!canManage" @click="confirm">
                    {{ t('common.delete') }}
                  </el-button>
                </span>
              </el-tooltip>
            </template>
          </ConfirmAction>
        </div>
      </template>
    </DataTable>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiDelete, apiDownload, apiGet, apiPost } from '../api/client'
import { useI18n } from '../i18n'
import ConfirmAction from './ConfirmAction.vue'
import DataTable from './DataTable.vue'
import KeyValueGrid from './KeyValueGrid.vue'

const props = defineProps<{
  backupDir?: string
  auditRetentionDays?: number | string
  taskRetentionDays?: number | string
  canManage: boolean
  disabledReason: string
}>()

type DatabaseBackup = {
  name: string
  path: string
  size: number
  sha256: string
  createdAt: string
}

const { t } = useI18n()
const backupRunning = ref(false)
const backupsLoading = ref(false)
const retentionRunning = ref(false)
const backups = ref<DatabaseBackup[]>([])

const maintenanceItems = computed(() => [
  { key: 'databaseBackupDir', label: t('settings.databaseBackupDir'), value: props.backupDir },
  { key: 'auditRetentionDays', label: t('settings.auditRetention'), value: formatRetentionDays(props.auditRetentionDays) },
  { key: 'taskRetentionDays', label: t('settings.taskRetention'), value: formatRetentionDays(props.taskRetentionDays) }
])
const backupColumns = computed(() => [
  { prop: 'name', label: t('settings.backupName'), width: 280 },
  { prop: 'size', label: t('settings.backupSize'), width: 110, slot: 'size' },
  { prop: 'sha256', label: t('settings.backupChecksum'), width: 360 },
  { prop: 'createdAt', label: t('common.time'), width: 190, slot: 'createdAt' },
  { label: t('common.operation'), width: 230, slot: 'action' }
])

async function refresh() {
  if (!props.canManage) {
    backups.value = []
    return
  }
  backupsLoading.value = true
  try {
    const res = await apiGet<{ items?: DatabaseBackup[] }>('/maintenance/database-backups')
    backups.value = Array.isArray(res.items) ? res.items : []
  } catch {
    backups.value = []
  } finally {
    backupsLoading.value = false
  }
}

async function runRetentionCleanup() {
  if (!props.canManage) {
    ElMessage.warning(props.disabledReason)
    return
  }
  retentionRunning.value = true
  try {
    await apiPost('/maintenance/retention/run')
    ElMessage.success(t('settings.retentionCleanupAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.retentionCleanupFailed'))
  } finally {
    retentionRunning.value = false
  }
}

async function runDatabaseBackup() {
  if (!props.canManage) {
    ElMessage.warning(props.disabledReason)
    return
  }
  backupRunning.value = true
  try {
    await apiPost('/maintenance/database-backup/run')
    ElMessage.success(t('settings.databaseBackupAccepted'))
    window.setTimeout(() => void refresh(), 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.databaseBackupFailed'))
  } finally {
    backupRunning.value = false
  }
}

async function deleteBackup(name: unknown) {
  if (!props.canManage || typeof name !== 'string') {
    ElMessage.warning(props.disabledReason)
    return
  }
  try {
    await apiDelete('/maintenance/database-backups', { names: [name] })
    ElMessage.success(t('settings.backupDeleted'))
    await refresh()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.backupDeleteFailed'))
  }
}

async function downloadBackup(name: unknown) {
  if (!props.canManage || typeof name !== 'string') {
    ElMessage.warning(props.disabledReason)
    return
  }
  try {
    const file = await apiDownload(`/maintenance/database-backups/${encodeURIComponent(name)}/download`)
    const url = URL.createObjectURL(file.blob)
    const link = document.createElement('a')
    link.href = url
    link.download = file.filename || name
    document.body.appendChild(link)
    link.click()
    link.remove()
    URL.revokeObjectURL(url)
    ElMessage.success(t('settings.backupDownloadStarted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.backupDownloadFailed'))
  }
}

async function verifyBackup(name: unknown) {
  if (!props.canManage || typeof name !== 'string') {
    ElMessage.warning(props.disabledReason)
    return
  }
  try {
    await apiPost(`/maintenance/database-backups/${encodeURIComponent(name)}/verify`)
    ElMessage.success(t('settings.backupVerifyAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.backupVerifyFailed'))
  }
}

function formatRetentionDays(value: unknown) {
  const count = Number(value)
  if (!Number.isFinite(count) || count < 1) {
    return '-'
  }
  return t('settings.days', { count })
}

function formatBytes(value: unknown) {
  const bytes = Number(value)
  if (!Number.isFinite(bytes) || bytes < 0) {
    return '-'
  }
  if (bytes < 1024) {
    return `${bytes} B`
  }
  if (bytes < 1024 * 1024) {
    return `${(bytes / 1024).toFixed(1)} KiB`
  }
  return `${(bytes / 1024 / 1024).toFixed(1)} MiB`
}

function formatDate(value: unknown) {
  if (!value) {
    return '-'
  }
  const date = new Date(String(value))
  if (Number.isNaN(date.getTime())) {
    return String(value)
  }
  return date.toLocaleString()
}

onMounted(refresh)
defineExpose({ refresh })
</script>

<style scoped>
.settings-block {
  margin-bottom: 0;
  padding: 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: #fbfdff;
}

.settings-block label {
  display: block;
  margin-bottom: 8px;
  color: var(--aifar-ink);
  font-weight: 800;
}

.settings-block .subtle-note {
  display: block;
  margin-top: 8px;
}

.retention-grid {
  grid-template-columns: minmax(150px, 180px) minmax(0, 1fr);
}

.retention-actions {
  margin-top: 12px;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.backup-table {
  height: 300px;
  margin-top: 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-md);
  overflow: hidden;
}

.backup-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

@media (max-width: 720px) {
  .retention-grid {
    grid-template-columns: 120px minmax(0, 1fr);
  }
}
</style>
