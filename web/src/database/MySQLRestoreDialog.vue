<template>
  <el-dialog
    :model-value="modelValue"
    :title="t('database.mysqlBackup.restoreTitle')"
    width="min(736px, calc(100vw - 32px))"
    class="mysql-restore-dialog"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
    @closed="reset"
  >
    <div v-if="backup" class="restore-summary">
      <div><span>{{ t('database.mysqlBackup.backupIdentity') }}</span><strong>{{ backup.id }}</strong></div>
      <div><span>SHA-256</span><strong class="checksum">{{ backup.checksum }}</strong></div>
      <div><span>{{ t('database.mysqlBackup.target') }}</span><strong>{{ target.label }}</strong></div>
      <div><span>{{ t('dashboard.topology') }}</span><strong>{{ target.topology }}</strong></div>
      <div><span>{{ t('database.mysqlBackup.schemas') }}</span><strong>{{ backup.metadata.schemas.length }}</strong></div>
      <div><span>{{ t('database.mysqlBackup.compatibility') }}</span><el-tag :type="compatibility.compatible ? 'success' : 'danger'" effect="plain">{{ compatibility.compatible ? t('database.mysqlBackup.compatible') : t(compatibility.reasonKey) }}</el-tag></div>
    </div>
    <el-alert type="warning" :closable="false" show-icon :title="t(restoreImpactKey(mode))" />
    <el-form class="restore-form" label-position="top" @submit.prevent="submit">
      <el-form-item :label="t('database.mysqlBackup.restoreThreads')" required>
        <el-input-number v-model="threads" :min="1" :max="64" :step="1" :precision="0" controls-position="right" />
      </el-form-item>
      <el-checkbox v-model="createPreRestoreBackup">{{ t('database.mysqlBackup.preRestoreBackup') }}</el-checkbox>
      <el-checkbox v-model="maintenanceConfirmed" class="danger-confirm">{{ t('database.mysqlBackup.maintenanceConfirm') }}</el-checkbox>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">{{ t('common.cancel') }}</el-button>
      <el-button type="danger" :loading="submitting" :disabled="!canSubmit" @click="submit">
        {{ t('database.mysqlBackup.restoreAction') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { useTaskProgressStore } from '../stores/taskProgress'
import {
  backupTargetCompatibility,
  restoreImpactKey,
  startMySQLRestore,
  type MySQLBackupRecord,
  type MySQLRestoreMode,
  type MySQLRestoreTarget
} from './mysqlBackup'

type RestoreTarget = MySQLRestoreTarget & { label: string }

const props = defineProps<{
  modelValue: boolean
  backup: MySQLBackupRecord | null
  target: RestoreTarget
  defaultThreads: number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  submitted: []
}>()

const { t } = useI18n()
const taskProgress = useTaskProgressStore()
const threads = ref(4)
const maintenanceConfirmed = ref(false)
const createPreRestoreBackup = ref(true)
const submitting = ref(false)
const mode = computed<MySQLRestoreMode>(() => props.target.topology === 'innodb-cluster' ? 'healthy-cluster' : 'standalone')
const compatibility = computed(() => props.backup
  ? backupTargetCompatibility(props.backup, props.target)
  : { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityMissing' })
const canSubmit = computed(() => !!props.backup && compatibility.value.compatible && maintenanceConfirmed.value && createPreRestoreBackup.value && threads.value >= 1 && threads.value <= 64 && !submitting.value)

watch(() => props.modelValue, (visible) => {
  if (visible) reset()
})

function reset() {
  if (submitting.value) return
  threads.value = props.defaultThreads >= 1 && props.defaultThreads <= 64 ? props.defaultThreads : 4
  maintenanceConfirmed.value = false
  createPreRestoreBackup.value = true
}

async function submit() {
  if (!props.backup || !canSubmit.value) return
  submitting.value = true
  try {
    await startMySQLRestore(props.target.instanceId, {
      backupId: props.backup.id,
      mode: mode.value as 'standalone' | 'healthy-cluster',
      maintenanceConfirmed: true,
      createPreRestoreBackup: createPreRestoreBackup.value,
      threads: threads.value
    }, taskProgress, t('database.mysqlBackup.restoreTaskLabel'))
    ElMessage.success(t('database.mysqlBackup.restoreAccepted'))
    emit('submitted')
    emit('update:modelValue', false)
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.restoreFailed'))
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.restore-summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
  padding: 16px;
  margin-bottom: 24px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: 8px;
  background: #fafafa;
}

.restore-summary span,
.restore-summary strong {
  display: block;
}

.restore-summary span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.checksum {
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}

.restore-form {
  display: grid;
  gap: 16px;
  margin-top: 24px;
}

.danger-confirm {
  align-items: flex-start;
  white-space: normal;
}

@media (max-width: 767px) {
  .restore-summary {
    grid-template-columns: 1fr;
  }
}
</style>
