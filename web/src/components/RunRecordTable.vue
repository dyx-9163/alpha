<template>
  <DataTable :rows="tableRows" :columns="columns" :row-key="rowKey" :height="height">
    <template #status="{ value }">
      <StatusTag :status="String(value || '')" />
    </template>
    <template #time="{ value }">
      {{ formatTime(String(value || '')) }}
    </template>
    <template v-if="showDetails" #actions="{ row }">
      <el-button size="small" @click="emit('details', asTask(row))">{{ t('common.details') }}</el-button>
    </template>
  </DataTable>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import DataTable from './DataTable.vue'
import StatusTag from './StatusTag.vue'
import { useI18n } from '../i18n'

type RunRecord = {
  id: string
  type: string
  target: string
  status: string
  createdAt?: string
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
  records: RunRecord[]
  rowKey?: string
  height?: string | number
  showDetails?: boolean
  typeWidth?: string | number
}>(), {
  rowKey: 'id',
  height: '100%',
  typeWidth: 220
})

const emit = defineEmits<{
  details: [record: RunRecord]
}>()

const { t } = useI18n()
const tableRows = computed(() => props.records as unknown as Record<string, unknown>[])

const columns = computed(() => {
  const base: TableColumn[] = [
    { prop: 'type', label: t('table.type'), minWidth: props.typeWidth },
    { prop: 'target', label: t('common.target'), minWidth: 180, showOverflowTooltip: true },
    { prop: 'status', label: t('common.status'), width: 120, slot: 'status' },
    { prop: 'createdAt', label: t('common.time'), minWidth: 180, slot: 'time' }
  ]
  if (props.showDetails) {
    base.push({ label: t('common.operation'), width: 120, fixed: 'right', slot: 'actions', showOverflowTooltip: false })
  }
  return base
})

function asTask(row: Record<string, unknown>) {
  return row as unknown as RunRecord
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
