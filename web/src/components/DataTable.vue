<template>
  <div class="data-table">
    <div v-if="$slots.toolbar || title" class="data-table-toolbar">
      <strong v-if="title">{{ title }}</strong>
      <slot name="toolbar" />
    </div>
    <el-table
      v-loading="loading"
      :data="rows"
      :height="height"
      :row-key="rowKey"
      :fit="fit"
      @selection-change="emit('selectionChange', $event)"
    >
      <el-table-column
        v-for="column in columns"
        :key="columnKey(column)"
        :type="column.type"
        :prop="column.prop"
        :label="column.label"
        :width="column.width"
        :min-width="column.minWidth"
        :fixed="column.fixed"
        :align="column.align"
        :show-overflow-tooltip="column.showOverflowTooltip ?? true"
      >
        <template v-if="column.slot || column.formatter || column.prop" #default="{ row, $index }">
          <slot
            v-if="column.slot && $slots[column.slot]"
            :name="column.slot"
            :row="row"
            :index="$index"
            :value="columnValue(row, column.prop)"
          />
          <span v-else>{{ formattedValue(row, column, $index) }}</span>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
type TableColumn = {
  prop?: string
  label?: string
  width?: string | number
  minWidth?: string | number
  fixed?: boolean | 'left' | 'right'
  align?: 'left' | 'center' | 'right'
  type?: 'selection' | 'index' | 'expand'
  slot?: string
  showOverflowTooltip?: boolean
  formatter?: (row: Record<string, unknown>, value: unknown, index: number) => string | number
}

const props = withDefaults(defineProps<{
  rows: Record<string, unknown>[]
  columns: TableColumn[]
  title?: string
  rowKey?: string
  height?: string | number
  loading?: boolean
  fit?: boolean
}>(), {
  rowKey: 'id',
  height: '100%',
  fit: true
})

const emit = defineEmits<{
  selectionChange: [rows: Record<string, unknown>[]]
}>()

function columnKey(column: TableColumn) {
  return column.type || column.prop || column.label || column.slot || 'column'
}

function columnValue(row: Record<string, unknown>, prop?: string) {
  if (!prop) {
    return ''
  }
  return prop.split('.').reduce<unknown>((value, key) => {
    if (value && typeof value === 'object') {
      return (value as Record<string, unknown>)[key]
    }
    return undefined
  }, row)
}

function formattedValue(row: Record<string, unknown>, column: TableColumn, index: number) {
  const value = columnValue(row, column.prop)
  if (column.formatter) {
    return column.formatter(row, value, index)
  }
  if (value === undefined || value === null || value === '') {
    return '-'
  }
  return String(value)
}
</script>

<style scoped>
.data-table {
  min-width: 0;
  min-height: 0;
  height: 100%;
  display: flex;
  flex-direction: column;
}

.data-table-toolbar {
  min-height: 44px;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--aifar-border-soft);
  background: linear-gradient(180deg, #fff, #fbfdff);
}

.data-table-toolbar strong {
  color: var(--aifar-ink);
  font-size: 15px;
  line-height: 22px;
  font-weight: 850;
}

.data-table :deep(.el-table) {
  flex: 1 1 auto;
}
</style>
