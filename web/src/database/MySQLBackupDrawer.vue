<template>
  <el-drawer
    :model-value="modelValue"
    :title="t('database.mysqlBackup.recordsTitle')"
    size="min(736px, 100vw)"
    class="mysql-backup-drawer"
    destroy-on-close
    @update:model-value="emit('update:modelValue', $event)"
  >
    <div class="drawer-summary" :aria-label="t('database.mysqlBackup.drawerSummaryAria')">
      <div><span>{{ t('database.mysqlBackup.source') }}</span><strong>{{ sourceLabel }}</strong></div>
      <div><span>{{ t('common.version') }}</span><strong>{{ version || '-' }}</strong></div>
      <div><span>{{ t('dashboard.topology') }}</span><strong>{{ topologyLabel(topology) }}</strong></div>
    </div>
    <el-skeleton v-if="loading" :rows="5" animated />
    <div v-else-if="visibleRecords.length" class="backup-list">
      <article v-for="record in visibleRecords" :key="record.id" class="backup-record">
        <div class="record-head">
          <div>
            <strong>{{ record.metadata.name || record.id }}</strong>
            <span>{{ record.id }}</span>
          </div>
          <el-tag :type="statusType(record.status)" effect="plain">{{ statusLabel(record.status) }}</el-tag>
        </div>
        <dl class="record-grid">
          <div><dt>{{ t('database.mysqlBackup.source') }}</dt><dd>{{ sourceLabel }}</dd></div>
          <div><dt>{{ t('common.version') }}</dt><dd>{{ record.metadata.mysqlVersion || version || '-' }}</dd></div>
          <div><dt>{{ t('dashboard.topology') }}</dt><dd>{{ topologyLabel(record.metadata.topology || topology) }}</dd></div>
          <div><dt>{{ t('database.mysqlBackup.schemas') }}</dt><dd>{{ schemaSummary(record) }}</dd></div>
          <div><dt>{{ t('common.time') }}</dt><dd>{{ formatTime(record.completedAt || record.createdAt) }}</dd></div>
          <div><dt>{{ t('database.mysqlBackup.size') }}</dt><dd>{{ formatBytes(record.size) }}</dd></div>
          <div class="wide"><dt>SHA-256</dt><dd class="checksum">{{ record.checksum || '-' }}</dd></div>
          <div><dt>{{ t('database.mysqlBackup.verification') }}</dt><dd>{{ verificationLabel(record) }}</dd></div>
          <div><dt>{{ t('database.mysqlBackup.task') }}</dt><dd><el-button v-if="record.taskId" link type="primary" @click="emit('openTask', record.taskId)">{{ record.taskId }}</el-button><span v-else>-</span></dd></div>
        </dl>
        <div class="record-actions">
          <span v-if="!restoreCompatibility(record).compatible" class="compatibility-note" role="status">
            {{ t(restoreCompatibility(record).reasonKey) }}
          </span>
          <el-button :disabled="!canVerify || record.status !== 'success'" @click="emit('verify', record)">{{ t('database.mysqlBackup.verifyAction') }}</el-button>
          <el-tooltip :content="restoreCompatibility(record).reasonKey ? t(restoreCompatibility(record).reasonKey) : ''" :disabled="restoreCompatibility(record).compatible" placement="top">
            <span><el-button type="primary" :disabled="!canRestore || !restoreCompatibility(record).compatible" @click="emit('restore', record)">{{ t('database.mysqlBackup.restoreAction') }}</el-button></span>
          </el-tooltip>
        </div>
      </article>
    </div>
    <div v-else class="empty-state"><div><strong>{{ t('database.mysqlBackup.noRecords') }}</strong><span>{{ t('database.mysqlBackup.noRecordsHint') }}</span></div></div>
  </el-drawer>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '../i18n'
import { backupTargetCompatibility, type MySQLBackupRecord, type MySQLBackupStatus, type MySQLRestoreTarget } from './mysqlBackup'

const props = defineProps<{
  modelValue: boolean
  sourceLabel: string
  version: string
  topology: string
  target: MySQLRestoreTarget
  records: MySQLBackupRecord[]
  loading?: boolean
  canVerify: boolean
  canRestore: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  verify: [record: MySQLBackupRecord]
  restore: [record: MySQLBackupRecord]
  openTask: [taskId: string]
}>()

const { t } = useI18n()
const visibleRecords = computed(() => props.records.filter((record) => record.status !== 'deleted'))

function restoreCompatibility(record: MySQLBackupRecord) {
  return backupTargetCompatibility(record, props.target)
}

function topologyLabel(topology: string | undefined) {
  if (topology === 'standalone') return t('database.mysqlBackup.topologyStandalone')
  if (topology === 'innodb-cluster') return t('database.mysqlBackup.topologyCluster')
  return '-'
}

function statusType(status: MySQLBackupStatus) {
  if (status === 'success') return 'success'
  if (status === 'failed') return 'danger'
  if (status === 'running') return 'primary'
  return 'info'
}

function statusLabel(status: MySQLBackupStatus) {
  return t(`database.mysqlBackup.status.${status}`)
}

function verificationLabel(record: MySQLBackupRecord) {
  if (!record.metadata.verificationResult) return t('database.mysqlBackup.notVerified')
  return `${t(`database.mysqlBackup.verification.${record.metadata.verificationResult}`)}${record.metadata.verifiedAt ? ` · ${formatTime(record.metadata.verifiedAt)}` : ''}`
}

function schemaSummary(record: MySQLBackupRecord) {
  const schemas = record.metadata.schemas
  return schemas.length ? `${schemas.length} · ${schemas.join(', ')}` : t('common.noData')
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isFinite(date.getTime()) ? date.toLocaleString() : '-'
}

function formatBytes(value: number) {
  if (value < 1024) return `${value} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let size = value / 1024
  let unit = units[0]
  for (let index = 1; index < units.length && size >= 1024; index += 1) {
    size /= 1024
    unit = units[index]
  }
  return `${size.toFixed(size >= 10 ? 1 : 2)} ${unit}`
}
</script>

<style scoped>
.drawer-summary,
.record-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 16px;
}

.drawer-summary {
  padding: 16px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: 8px;
  background: #fafafa;
  margin-bottom: 24px;
}

.drawer-summary span,
.drawer-summary strong {
  display: block;
}

.drawer-summary span,
dt {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.backup-list {
  display: grid;
  gap: 16px;
}

.backup-record {
  padding: 16px;
  border: 1px solid var(--aifar-border);
  border-radius: 8px;
}

.record-head,
.record-actions {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
}

.record-head > div > * {
  display: block;
}

.record-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.record-grid {
  margin: 16px 0;
}

.record-grid div {
  min-width: 0;
}

.record-grid .wide {
  grid-column: 1 / -1;
}

dt,
dd {
  margin: 0;
}

dd {
  overflow-wrap: anywhere;
}

.checksum {
  font-family: ui-monospace, SFMono-Regular, Consolas, monospace;
  font-size: 12px;
}

.record-actions {
  justify-content: flex-end;
  flex-wrap: wrap;
}

.compatibility-note {
  margin-right: auto;
  color: var(--aifar-text-danger);
  font-size: 12px;
}

@media (max-width: 767px) {
  .drawer-summary,
  .record-grid {
    grid-template-columns: 1fr;
  }

  .record-grid .wide {
    grid-column: auto;
  }
}
</style>
