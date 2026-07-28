<template>
  <el-dialog
    :model-value="modelValue"
    :title="t('database.mysqlBackup.createTitle')"
    width="min(520px, calc(100vw - 32px))"
    class="mysql-backup-dialog"
    :close-on-click-modal="false"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
    @closed="resetForm"
  >
    <el-alert type="info" :closable="false" show-icon>
      <template #title>{{ t('database.mysqlBackup.sourceSummary', { source: sourceLabel }) }}</template>
    </el-alert>
    <el-form class="backup-form" label-position="top" @submit.prevent="submit">
      <el-form-item :label="t('database.mysqlBackup.name')" required>
        <el-input
          v-model="form.name"
          maxlength="128"
          show-word-limit
          :placeholder="t('database.mysqlBackup.namePlaceholder')"
          :aria-label="t('database.mysqlBackup.name')"
        />
      </el-form-item>
      <div class="parameter-grid">
        <el-form-item :label="t('database.mysqlBackup.threads')" required>
          <el-input-number v-model="form.threads" :min="1" :max="64" :step="1" :precision="0" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('database.mysqlBackup.maxRate')" required>
          <el-input-number v-model="form.maxRateMBps" :min="0" :max="1048576" :step="1" :precision="0" controls-position="right" />
        </el-form-item>
        <el-form-item :label="t('database.mysqlBackup.keepLast')">
          <el-input-number v-model="form.keepLast" :min="1" :max="100000" :step="1" :precision="0" controls-position="right" />
        </el-form-item>
      </div>
      <p class="form-note">{{ t('database.mysqlBackup.rateHint') }}</p>
    </el-form>
    <template #footer>
      <el-button @click="emit('update:modelValue', false)">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="submitting" :disabled="!canSubmit" @click="submit">
        {{ t('database.mysqlBackup.createAction') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { useTaskProgressStore } from '../stores/taskProgress'
import { backupDefaults, startMySQLBackup, type MySQLBackupDefaults } from './mysqlBackup'

const props = defineProps<{
  modelValue: boolean
  instanceId: string
  sourceLabel: string
  defaults: MySQLBackupDefaults
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  submitted: []
}>()

const { t } = useI18n()
const taskProgress = useTaskProgressStore()
const submitting = computed(() => form.submitting)
const form = reactive({ name: '', threads: 4, maxRateMBps: 0, keepLast: undefined as number | undefined, submitting: false })
const canSubmit = computed(() => !!form.name.trim() && Number.isInteger(form.threads) && form.threads >= 1 && form.threads <= 64 && form.maxRateMBps >= 0 && !form.submitting)

watch(() => props.modelValue, (visible) => {
  if (visible) resetForm()
})

function resetForm() {
  if (form.submitting) return
  const defaults = backupDefaults(props.defaults)
  form.name = ''
  form.threads = defaults.threads
  form.maxRateMBps = defaults.maxRateMBps
  form.keepLast = defaults.keepLast
}

async function submit() {
  if (!canSubmit.value) return
  form.submitting = true
  try {
    await startMySQLBackup(props.instanceId, {
      name: form.name,
      threads: form.threads,
      maxRateMBps: form.maxRateMBps,
      ...(form.keepLast ? { keepLast: form.keepLast } : {})
    }, taskProgress, t('database.mysqlBackup.taskLabel'))
    ElMessage.success(t('database.mysqlBackup.accepted'))
    emit('submitted')
    emit('update:modelValue', false)
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.submitFailed'))
  } finally {
    form.submitting = false
  }
}
</script>

<style scoped>
.backup-form {
  margin-top: 24px;
}

.parameter-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.parameter-grid :deep(.el-input-number) {
  width: 100%;
}

.form-note {
  margin: 0;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 20px;
}

@media (max-width: 767px) {
  .parameter-grid {
    grid-template-columns: 1fr;
  }
}
</style>
