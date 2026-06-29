<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('settings.title') }}</h1>
        <p class="page-subtitle">{{ t('settings.subtitle') }}</p>
      </div>
      <el-button @click="load">{{ t('common.refresh') }}</el-button>
    </div>

    <div class="workspace-card settings-card">
      <div class="settings-block">
        <label>{{ t('settings.language') }}</label>
        <el-radio-group v-model="form.language" @change="changeLanguage">
          <el-radio-button label="zh">{{ t('settings.chinese') }}</el-radio-button>
          <el-radio-button label="en">{{ t('settings.english') }}</el-radio-button>
        </el-radio-group>
        <span class="subtle-note">{{ t('settings.languageNote') }}</span>
      </div>

      <div class="settings-block">
        <label>{{ t('settings.concurrency') }}</label>
        <div class="head-actions">
          <el-input-number v-model="form.deploymentConcurrency" :min="1" :max="20" />
          <el-tooltip :content="deniedText" :disabled="canManageSettings" placement="top">
            <span><el-button type="primary" :disabled="!canManageSettings" @click="save">{{ t('common.save') }}</el-button></span>
          </el-tooltip>
        </div>
        <span class="subtle-note">{{ t('settings.concurrencyNote') }}</span>
      </div>

      <div class="settings-block">
        <label>{{ t('settings.dataMaintenance') }}</label>
        <KeyValueGrid :items="maintenanceItems" class="retention-grid" />
        <div class="retention-actions">
          <el-tooltip :content="deniedText" :disabled="canManageSettings" placement="top">
            <span>
              <el-button :loading="backupRunning" :disabled="!canManageSettings" @click="runDatabaseBackup">
                {{ t('settings.runDatabaseBackup') }}
              </el-button>
            </span>
          </el-tooltip>
          <el-tooltip :content="deniedText" :disabled="canManageSettings" placement="top">
            <span>
              <el-button type="primary" :loading="retentionRunning" :disabled="!canManageSettings" @click="runRetentionCleanup">
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
        >
          <template #toolbar>
            <el-button size="small" :disabled="!canManageSettings" @click="loadBackups">{{ t('common.refresh') }}</el-button>
          </template>
          <template #size="{ row }">
            {{ formatBytes(row.size) }}
          </template>
          <template #createdAt="{ row }">
            {{ formatDate(row.createdAt) }}
          </template>
          <template #action="{ row }">
            <div class="backup-actions">
              <el-tooltip :content="deniedText" :disabled="canManageSettings" placement="top">
                <span>
                  <el-button size="small" :disabled="!canManageSettings" @click="downloadBackup(row.name)">
                    {{ t('common.download') }}
                  </el-button>
                </span>
              </el-tooltip>
              <ConfirmAction
                :message="t('settings.confirmDeleteBackup', { name: row.name })"
                :disabled="!canManageSettings"
                type="warning"
                @confirm="deleteBackup(row.name)"
              >
                <template #default="{ confirm }">
                  <el-tooltip :content="deniedText" :disabled="canManageSettings" placement="top">
                    <span>
                      <el-button size="small" type="danger" :disabled="!canManageSettings" @click="confirm">
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

      <el-alert
        :title="t('settings.realModeTitle')"
        :description="t('settings.realModeDesc')"
        type="warning"
        :closable="false"
        show-icon
      />

      <h2 class="settings-title">{{ t('settings.providerStatus') }}</h2>
      <KeyValueGrid :items="providerItems" class="provider-grid">
        <template #value="{ item }">
          <span v-if="item.key === 'mode'" class="status-pill success">{{ item.value }}</span>
          <span v-else>{{ item.value || '-' }}</span>
        </template>
      </KeyValueGrid>

      <h2 class="settings-title">{{ t('settings.moduleStatus') }}</h2>
      <el-table :data="moduleRows">
        <el-table-column prop="module" :label="t('common.module')" />
        <el-table-column :label="t('common.status')"><template #default><span class="status-pill success">{{ t('settings.connected') }}</span></template></el-table-column>
        <el-table-column :label="t('common.provider')"><template #default>{{ t('common.real') }}</template></el-table-column>
        <el-table-column prop="message" :label="t('common.message')" />
        <el-table-column prop="time" :label="t('common.time')" width="190" />
      </el-table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiDelete, apiDownload, apiGet, apiPost, apiPut } from '../api/client'
import ConfirmAction from '../components/ConfirmAction.vue'
import DataTable from '../components/DataTable.vue'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

const { locale, setLocale, t } = useI18n()
const { can, deniedText } = usePermissions()
const form = reactive<any>({ language: locale.value, deploymentConcurrency: 2, moduleStatus: {} })
const now = ref('')
const backupRunning = ref(false)
const backupsLoading = ref(false)
const retentionRunning = ref(false)
const backups = ref<DatabaseBackup[]>([])
const platform = navigator.platform.toLowerCase().includes('win') ? 'windows' : 'linux'
const providerModeLabel = computed(() => {
  const mode = form.providerStatus || form.providerMode || 'real'
  return mode === 'real' ? t('common.real') : mode
})
const canManageSettings = computed(() => can(permissions.settingsManage))
const moduleRows = computed(() => {
  const modules = form.moduleStatus ?? {}
  return Object.keys(modules).map((module) => ({ module, message: moduleMessage(module), time: now.value }))
})
const providerItems = computed(() => [
  { key: 'mode', label: t('settings.mode'), value: providerModeLabel.value },
  { key: 'platform', label: t('settings.platform'), value: platform },
  { key: 'message', label: t('common.message'), value: t('settings.providerMessage') },
  { key: 'databasePath', label: t('settings.databasePath'), value: form.databasePath },
  { key: 'resourcePath', label: t('settings.resourcePath'), value: form.resourcePath },
  { key: 'defaultDeployDir', label: t('settings.defaultDeployDir'), value: form.defaultDeployDir },
  { key: 'confirm', label: t('settings.dangerousActionsRequire'), value: t('settings.confirmTrue') }
])
const maintenanceItems = computed(() => [
  { key: 'databaseBackupDir', label: t('settings.databaseBackupDir'), value: form.databaseBackupDir },
  { key: 'auditRetentionDays', label: t('settings.auditRetention'), value: formatRetentionDays(form.auditRetentionDays) },
  { key: 'taskRetentionDays', label: t('settings.taskRetention'), value: formatRetentionDays(form.taskRetentionDays) }
])
const backupColumns = computed(() => [
  { prop: 'name', label: t('settings.backupName'), minWidth: 240 },
  { prop: 'size', label: t('settings.backupSize'), width: 120, slot: 'size' },
  { prop: 'sha256', label: t('settings.backupChecksum'), minWidth: 240 },
  { prop: 'createdAt', label: t('common.time'), width: 190, slot: 'createdAt' },
  { label: t('common.operation'), width: 170, slot: 'action', fixed: 'right' as const }
])

type DatabaseBackup = {
  name: string
  path: string
  size: number
  sha256: string
  createdAt: string
}

function moduleMessage(module: string) {
  const key = `settings.moduleMessages.${module}`
  const text = t(key)
  return text === key ? t('settings.moduleAvailable', { module }) : text
}

function changeLanguage(value: string | number | boolean | undefined) {
  if (typeof value === 'string') {
    setLocale(value)
  }
}

async function load() {
  Object.assign(form, await apiGet('/settings'))
  form.language = locale.value
  form.deploymentConcurrency = Number(form.deploymentConcurrency)
  now.value = new Date().toISOString()
  await loadBackups()
}
async function save() {
  if (!canManageSettings.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  setLocale(form.language)
  Object.assign(form, await apiPut('/settings', { language: form.language, deploymentConcurrency: form.deploymentConcurrency }))
  form.language = locale.value
  form.deploymentConcurrency = Number(form.deploymentConcurrency)
  now.value = new Date().toISOString()
}

async function runRetentionCleanup() {
  if (!canManageSettings.value) {
    ElMessage.warning(deniedText.value)
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
  if (!canManageSettings.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  backupRunning.value = true
  try {
    await apiPost('/maintenance/database-backup/run')
    ElMessage.success(t('settings.databaseBackupAccepted'))
    window.setTimeout(() => void loadBackups(), 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.databaseBackupFailed'))
  } finally {
    backupRunning.value = false
  }
}

async function loadBackups() {
  if (!canManageSettings.value) {
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

async function deleteBackup(name: unknown) {
  if (!canManageSettings.value || typeof name !== 'string') {
    ElMessage.warning(deniedText.value)
    return
  }
  try {
    await apiDelete('/maintenance/database-backups', { names: [name] })
    ElMessage.success(t('settings.backupDeleted'))
    await loadBackups()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.backupDeleteFailed'))
  }
}

async function downloadBackup(name: unknown) {
  if (!canManageSettings.value || typeof name !== 'string') {
    ElMessage.warning(deniedText.value)
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
onMounted(load)
</script>

<style scoped>
.settings-card {
  padding: 12px;
  display: grid;
  gap: 12px;
}

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

.settings-title {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 16px;
  font-weight: 850;
}

.provider-grid {
  grid-template-columns: minmax(150px, 180px) minmax(0, 1fr);
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
  .provider-grid,
  .retention-grid {
    grid-template-columns: 120px minmax(0, 1fr);
  }
}
</style>
