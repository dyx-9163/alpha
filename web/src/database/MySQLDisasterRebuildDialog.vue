<template>
  <el-dialog
    :model-value="modelValue"
    :title="t('database.mysqlBackup.disasterTitle')"
    width="min(736px, calc(100vw - 32px))"
    class="mysql-disaster-dialog"
    :close-on-click-modal="false"
    :close-on-press-escape="!submitting"
    destroy-on-close
    @update:model-value="handleVisibility"
    @closed="clearPasswords"
  >
    <el-alert type="error" :closable="false" show-icon :title="t('database.mysqlBackup.disasterImpact')" />
    <div v-if="backup" class="identity-grid">
      <div><span>{{ t('database.mysqlBackup.backupIdentity') }}</span><strong>{{ backup.id }}</strong></div>
      <div><span>SHA-256</span><strong class="checksum">{{ backup.checksum }}</strong></div>
    </div>
    <div class="disaster-notes">
      <p>{{ t('database.mysqlBackup.quarantineImpact') }}</p>
      <p>{{ t('database.mysqlBackup.routerImpact') }}</p>
    </div>
    <div class="node-confirmations">
      <div v-for="node in nodes" :key="node.instanceId" class="node-confirmation">
        <div>
          <strong>{{ node.serverLabel }}</strong>
          <span>{{ node.instanceLabel }} · {{ node.serverId }}</span>
        </div>
        <el-input
          v-model="serverPasswords[node.serverId]"
          type="password"
          show-password
          autocomplete="new-password"
          :placeholder="t('database.mysqlBackup.sshPassword')"
          :aria-label="t('database.mysqlBackup.sshPasswordFor', { server: node.serverLabel })"
        />
      </div>
    </div>
    <el-form class="disaster-form" label-position="top" @submit.prevent="submit">
      <el-form-item :label="t('database.mysqlBackup.restoreThreads')" required>
        <el-input-number v-model="threads" :min="1" :max="64" :step="1" :precision="0" controls-position="right" />
      </el-form-item>
      <el-checkbox v-model="maintenanceConfirmed" class="danger-confirm">{{ t('database.mysqlBackup.maintenanceConfirm') }}</el-checkbox>
      <el-checkbox v-model="disasterConfirmed" class="danger-confirm">{{ t('database.mysqlBackup.disasterConfirm') }}</el-checkbox>
    </el-form>
    <template #footer>
      <el-button :disabled="submitting" @click="handleVisibility(false)">{{ t('common.cancel') }}</el-button>
      <el-button type="danger" :loading="submitting" :disabled="!canSubmit" @click="submit">
        {{ t('database.mysqlBackup.disasterAction') }}
      </el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useI18n } from '../i18n'
import { useTaskProgressStore } from '../stores/taskProgress'
import { backupTargetCompatibility, startMySQLRestore, type MySQLBackupRecord } from './mysqlBackup'

type DisasterNode = {
  instanceId: string
  instanceLabel: string
  serverId: string
  serverLabel: string
}

const props = defineProps<{
  modelValue: boolean
  instanceId: string
  clusterId: string
  backup: MySQLBackupRecord | null
  nodes: DisasterNode[]
  defaultThreads: number
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  submitted: []
}>()

const { t } = useI18n()
const taskProgress = useTaskProgressStore()
const serverPasswords = reactive<Record<string, string>>({})
const threads = ref(4)
const maintenanceConfirmed = ref(false)
const disasterConfirmed = ref(false)
const submitting = ref(false)
const targetMapping = computed(() => Object.fromEntries(props.nodes.map((node) => [node.instanceId, node.serverId])))
const compatible = computed(() => !!props.backup && backupTargetCompatibility(props.backup, {
  topology: 'innodb-cluster', instanceId: props.instanceId, serverId: props.nodes[0]?.serverId || '', clusterId: props.clusterId
}).compatible)
const canSubmit = computed(() => compatible.value && props.nodes.length === 3 && new Set(props.nodes.map((node) => node.serverId)).size === 3 &&
  props.nodes.every((node) => !!serverPasswords[node.serverId]?.trim()) && maintenanceConfirmed.value && disasterConfirmed.value &&
  threads.value >= 1 && threads.value <= 64 && !submitting.value)

watch(() => props.modelValue, (visible) => {
  if (visible) reset()
  else clearPasswords()
})

function reset() {
  clearPasswords()
  for (const node of props.nodes) serverPasswords[node.serverId] = ''
  threads.value = props.defaultThreads >= 1 && props.defaultThreads <= 64 ? props.defaultThreads : 4
  maintenanceConfirmed.value = false
  disasterConfirmed.value = false
}

function clearPasswords() {
  for (const key of Object.keys(serverPasswords)) delete serverPasswords[key]
}

function handleVisibility(value: boolean) {
  if (!value && submitting.value) return
  if (!value) clearPasswords()
  emit('update:modelValue', value)
}

async function submit() {
  if (!props.backup || !canSubmit.value) return
  submitting.value = true
  try {
    const passwords = Object.fromEntries(props.nodes.map((node) => [node.serverId, serverPasswords[node.serverId]]))
    await startMySQLRestore(props.instanceId, {
      backupId: props.backup.id,
      mode: 'disaster-rebuild',
      maintenanceConfirmed: true,
      createPreRestoreBackup: false,
      disasterConfirmed: true,
      threads: threads.value,
      targetMapping: targetMapping.value,
      serverPasswords: passwords
    }, taskProgress, t('database.mysqlBackup.disasterTaskLabel'))
    clearPasswords()
    ElMessage.success(t('database.mysqlBackup.disasterAccepted'))
    emit('submitted')
    emit('update:modelValue', false)
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.disasterFailed'))
  } finally {
    submitting.value = false
  }
}
</script>

<style scoped>
.identity-grid,
.node-confirmation {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px;
}

.identity-grid {
  padding: 16px;
  margin: 24px 0 16px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: 8px;
  background: #fafafa;
}

.identity-grid span,
.identity-grid strong,
.node-confirmation strong,
.node-confirmation span {
  display: block;
}

.identity-grid span,
.node-confirmation span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.checksum {
  overflow-wrap: anywhere;
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}

.disaster-notes {
  color: var(--aifar-text-secondary);
  line-height: 22px;
}

.node-confirmations,
.disaster-form {
  display: grid;
  gap: 16px;
  margin-top: 24px;
}

.node-confirmation {
  align-items: center;
  padding: 16px;
  border: 1px solid var(--aifar-border);
  border-radius: 8px;
}

.danger-confirm {
  align-items: flex-start;
  white-space: normal;
}

@media (max-width: 767px) {
  .identity-grid,
  .node-confirmation {
    grid-template-columns: 1fr;
  }
}
</style>
