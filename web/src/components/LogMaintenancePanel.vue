<template>
  <div class="settings-block">
    <label>{{ t('settings.logMaintenance') }}</label>
    <div class="retention-editor">
      <span class="retention-label">{{ t('settings.logRetention') }}</span>
      <el-input-number
        v-model="draftRetentionDays"
        :min="1"
        :max="3650"
        :step="1"
        data-testid="log-retention-input"
      />
      <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
        <span>
          <el-button
            :loading="saveRunning"
            :disabled="!canManage"
            data-testid="save-log-retention"
            @click="saveLogRetention"
          >
            {{ t('settings.saveLogRetention') }}
          </el-button>
        </span>
      </el-tooltip>
    </div>
    <KeyValueGrid :items="logItems" class="retention-grid" />
    <div class="retention-actions">
      <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
        <span>
          <el-button
            type="primary"
            :loading="cleanupRunning"
            :disabled="!canManage"
            data-testid="run-log-cleanup"
            @click="runLogCleanup"
          >
            {{ t('settings.runLogCleanup') }}
          </el-button>
        </span>
      </el-tooltip>
    </div>
    <span class="subtle-note">{{ t('settings.logMaintenanceNote') }}</span>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { apiPost, apiPut } from '../api/client'
import { useI18n } from '../i18n'
import { useTaskProgressStore } from '../stores/taskProgress'
import KeyValueGrid from './KeyValueGrid.vue'

const props = defineProps<{
  logRetentionDays?: number | string
  canManage: boolean
  disabledReason: string
}>()

const { t } = useI18n()
const taskProgress = useTaskProgressStore()
const cleanupRunning = ref(false)
const saveRunning = ref(false)
const draftRetentionDays = ref(normalizeRetentionDays(props.logRetentionDays))

watch(
  () => props.logRetentionDays,
  (value) => {
    draftRetentionDays.value = normalizeRetentionDays(value)
  }
)

const logItems = computed(() => [
  { key: 'logRetentionDays', label: t('settings.logRetention'), value: formatRetentionDays(draftRetentionDays.value) },
  { key: 'logCleanupScope', label: t('settings.logCleanupScope'), value: t('settings.logCleanupScopeValue') },
  { key: 'logCleanupKeeps', label: t('settings.logCleanupKeeps'), value: t('settings.logCleanupKeepsValue') }
])

async function runLogCleanup() {
  if (!props.canManage) {
    ElMessage.warning(props.disabledReason)
    return
  }
  cleanupRunning.value = true
  try {
    const result = await apiPost<{ taskId?: string }>('/maintenance/retention/run')
    if (result.taskId) {
      taskProgress.track(result.taskId, t('settings.logMaintenance'))
    }
    ElMessage.success(t('settings.logCleanupAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.logCleanupFailed'))
  } finally {
    cleanupRunning.value = false
  }
}

async function saveLogRetention() {
  if (!props.canManage) {
    ElMessage.warning(props.disabledReason)
    return
  }
  const days = normalizeRetentionDays(draftRetentionDays.value)
  saveRunning.value = true
  try {
    await apiPut('/settings', { logRetentionDays: days })
    ElMessage.success(t('settings.logRetentionSaved'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.logRetentionSaveFailed'))
  } finally {
    saveRunning.value = false
  }
}

function formatRetentionDays(value: unknown) {
  return t('settings.days', { count: normalizeRetentionDays(value) })
}

function normalizeRetentionDays(value: unknown) {
  const count = Math.floor(Number(value))
  if (!Number.isFinite(count) || count < 1) {
    return 1
  }
  return Math.min(count, 3650)
}
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

.retention-editor {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  margin-bottom: 12px;
}

.retention-label {
  color: var(--aifar-muted);
  font-size: 13px;
  font-weight: 700;
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

@media (max-width: 720px) {
  .retention-grid {
    grid-template-columns: 120px minmax(0, 1fr);
  }
}
</style>
