<template>
  <DataTable :rows="tableRows" :columns="columns" :row-key="rowKey" :height="height">
    <template #server="{ row }">
      {{ serverLabel(asInstance(row).serverId) }}
    </template>
    <template #status="{ value }">
      <StatusTag :status="String(value || '')" />
    </template>
    <template #time="{ value }">
      {{ formatTime(String(value || '')) }}
    </template>
    <template v-if="showActions" #actions="{ row }">
      <div class="instance-actions">
        <el-tooltip v-if="props.showCheck" :content="props.disabledReason" :disabled="props.canCheck || !props.disabledReason" placement="top">
          <span>
            <el-button size="small" plain :disabled="!props.canCheck" @click="emitCheck(row)">{{ t('common.check') }}</el-button>
          </span>
        </el-tooltip>
        <el-tooltip v-if="props.showUpdate && rowUpdateable(row)" :content="props.disabledReason" :disabled="props.canUpdate || !props.disabledReason" placement="top">
          <span>
            <el-button size="small" type="primary" plain :disabled="!props.canUpdate" @click="emitUpdate(row)">{{ props.updateLabel || t('common.update') }}</el-button>
          </span>
        </el-tooltip>
        <el-tooltip v-if="props.showDelete" :content="props.disabledReason" :disabled="props.canDelete || !props.disabledReason" placement="top">
          <span>
            <el-button size="small" type="danger" plain :disabled="!props.canDelete" @click="emitDelete(row)">{{ t('common.delete') }}</el-button>
          </span>
        </el-tooltip>
      </div>
    </template>
  </DataTable>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DataTable from './DataTable.vue'
import StatusTag from './StatusTag.vue'
import { useI18n } from '../i18n'

type AppInstanceTableRecord = {
  id: string
  app: string
  version: string
  serverId?: string
  status?: string
  createdAt?: string
}

type ServerOption = {
  id: string
  name?: string
  host?: string
}

type TableColumn = {
  prop?: string
  label?: string
  width?: string | number
  minWidth?: string | number
  fixed?: boolean | 'left' | 'right'
  slot?: string
  showOverflowTooltip?: boolean
}

const props = withDefaults(defineProps<{
  instances: AppInstanceTableRecord[]
  servers?: ServerOption[]
  rowKey?: string
  height?: string | number
  showActions?: boolean
  showCheck?: boolean
  showDelete?: boolean
  showUpdate?: boolean
  showTime?: boolean
  canCheck?: boolean
  canDelete?: boolean
  canUpdate?: boolean
  updateLabel?: string
  canUpdateRow?: (row: AppInstanceTableRecord) => boolean
  disabledReason?: string
}>(), {
  servers: () => [],
  rowKey: 'id',
  height: '100%',
  showCheck: true,
  showDelete: true,
  showUpdate: false,
  showTime: true,
  canCheck: true,
  canDelete: true,
  canUpdate: true,
  updateLabel: '',
  disabledReason: ''
})

const emit = defineEmits<{
  check: [row: AppInstanceTableRecord]
  delete: [row: AppInstanceTableRecord]
  update: [row: AppInstanceTableRecord]
}>()

const { t } = useI18n()

const tableRows = computed(() => props.instances as unknown as Record<string, unknown>[])

const columns = computed(() => {
  const base: TableColumn[] = [
    { prop: 'app', label: t('table.app'), minWidth: 120 },
    { prop: 'version', label: t('table.version'), minWidth: 120 },
    { prop: 'serverId', label: t('table.server'), minWidth: 180, slot: 'server' },
    { prop: 'status', label: t('common.status'), width: 120, slot: 'status' }
  ]
  if (props.showTime) {
    base.push({ prop: 'createdAt', label: t('common.time'), minWidth: 180, slot: 'time' })
  }
  if (props.showActions) {
    base.push({ label: t('common.operation'), width: props.showUpdate ? 300 : 220, fixed: 'right', slot: 'actions', showOverflowTooltip: false })
  }
  return base
})

function asInstance(row: Record<string, unknown>) {
  return row as unknown as AppInstanceTableRecord
}

function emitCheck(row: Record<string, unknown>) {
  if (!props.canCheck) {
    return
  }
  emit('check', asInstance(row))
}

function emitDelete(row: Record<string, unknown>) {
  if (!props.canDelete) {
    return
  }
  emit('delete', asInstance(row))
}

function emitUpdate(row: Record<string, unknown>) {
  if (!props.canUpdate || !rowUpdateable(row)) {
    return
  }
  emit('update', asInstance(row))
}

function rowUpdateable(row: Record<string, unknown>) {
  const instance = asInstance(row)
  return props.canUpdateRow ? props.canUpdateRow(instance) : true
}

function serverLabel(serverId?: string) {
  if (!serverId) {
    return '-'
  }
  const server = props.servers.find((item) => item.id === serverId)
  if (!server) {
    return serverId
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || serverId
}

function formatTime(value?: string) {
  if (!value) {
    return '-'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString()
}
</script>

<style scoped>
.instance-actions {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}
</style>
