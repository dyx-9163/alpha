<template>
  <aside class="task-sidebar">
    <div class="sidebar-title">
      <span>{{ t('tasks.taskList') }}</span>
      <span class="count-badge">{{ filteredTasks.length }}</span>
    </div>
    <div class="task-filters">
      <el-select v-model="categoryFilter" size="small" :placeholder="t('tasks.category')">
        <el-option v-for="option in categoryOptions" :key="option.value" :label="option.label" :value="option.value">
          <div class="filter-option">
            <span>{{ option.label }}</span>
            <span>{{ option.count }}</span>
          </div>
        </el-option>
      </el-select>
      <el-select v-model="statusFilter" size="small" :placeholder="t('common.status')">
        <el-option v-for="option in statusOptions" :key="option.value" :label="option.label" :value="option.value">
          <div class="filter-option">
            <span>{{ option.label }}</span>
            <span>{{ option.count }}</span>
          </div>
        </el-option>
      </el-select>
    </div>

    <template v-if="filteredTasks.length">
      <div class="task-list-scroll">
        <div class="task-list">
          <div
            v-for="task in pagedTasks"
            :key="task.id"
            class="task-item"
            :class="{ active: task.id === selectedId }"
            role="button"
            tabindex="0"
            @click="$emit('select', task.id)"
            @keydown.enter="$emit('select', task.id)"
          >
            <span class="task-item-head">
              <span class="task-title">
                <el-checkbox
                  :model-value="selectedSet.has(task.id)"
                  @click.stop
                  @change="toggleTask(task.id, Boolean($event))"
                />
                <span class="task-type">{{ task.type }}</span>
              </span>
              <span class="task-category">{{ taskCategoryLabel(taskCategory(task)) }}</span>
            </span>
            <span class="task-target">{{ task.target || t('tasks.controlPlane') }}</span>
            <span class="task-meta">
              <StatusTag :status="task.status" />
              <span>{{ formatTime(task.createdAt) }}</span>
            </span>
          </div>
        </div>
      </div>

      <div class="task-pagination">
        <el-select v-model="pageSize" size="small" class="page-size-select">
          <el-option :label="`8 / ${t('tasks.page')}`" :value="8" />
          <el-option :label="`12 / ${t('tasks.page')}`" :value="12" />
          <el-option :label="`20 / ${t('tasks.page')}`" :value="20" />
        </el-select>
        <el-pagination
          v-model:current-page="currentPage"
          small
          layout="prev, pager, next"
          :page-size="pageSize"
          :total="filteredTasks.length"
        />
      </div>
    </template>
    <el-empty v-else :description="t('tasks.noTasks')" :image-size="76" />
  </aside>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '../i18n'
import StatusTag from './StatusTag.vue'

type TaskListItem = {
  id: string
  type: string
  target: string
  status: string
  createdAt: string
}

const props = defineProps<{
  tasks: TaskListItem[]
  selectedId: string
  selectedIds?: string[]
  typePrefix?: string
}>()

const emit = defineEmits<{
  select: [id: string]
  selectionChange: [ids: string[]]
}>()

const { t } = useI18n()
const categoryFilter = ref('all')
const statusFilter = ref('all')
const currentPage = ref(1)
const pageSize = ref(8)
const selectedSet = computed(() => new Set(props.selectedIds ?? []))

const scopedTasks = computed(() => {
  const ordered = props.tasks.slice().sort((a, b) => taskTime(b.createdAt) - taskTime(a.createdAt))
  if (!props.typePrefix) {
    return ordered
  }
  return ordered.filter((task) => task.type.startsWith(props.typePrefix || ''))
})

const filteredTasks = computed(() => scopedTasks.value.filter((task) => {
  const categoryMatched = categoryFilter.value === 'all' || taskCategory(task) === categoryFilter.value
  const statusMatched = statusFilter.value === 'all' || task.status === statusFilter.value
  return categoryMatched && statusMatched
}))

const pagedTasks = computed(() => {
  const start = (currentPage.value - 1) * pageSize.value
  return filteredTasks.value.slice(start, start + pageSize.value)
})

const categoryOptions = computed(() => {
  const counts = countBy(scopedTasks.value, taskCategory)
  const options = [{ value: 'all', label: t('common.all'), count: scopedTasks.value.length }]
  for (const value of taskCategoryOrder) {
    const count = counts.get(value) ?? 0
    if (count > 0) {
      options.push({ value, label: taskCategoryLabel(value), count })
    }
  }
  for (const [value, count] of counts) {
    if (!taskCategoryOrder.includes(value)) {
      options.push({ value, label: taskCategoryLabel(value), count })
    }
  }
  return options
})

const statusOptions = computed(() => {
  const counts = countBy(scopedTasks.value, (task) => task.status || 'unknown')
  return [
    { value: 'all', label: t('common.all'), count: scopedTasks.value.length },
    ...taskStatusOrder
      .map((value) => ({ value, label: statusLabel(value), count: counts.get(value) ?? 0 }))
      .filter((option) => option.count > 0)
  ]
})

watch([categoryFilter, statusFilter, pageSize], () => {
  currentPage.value = 1
})

watch(filteredTasks, () => {
  const maxPage = Math.max(1, Math.ceil(filteredTasks.value.length / pageSize.value))
  if (currentPage.value > maxPage) {
    currentPage.value = maxPage
  }
})

const taskCategoryOrder = ['apps', 'servers', 'containers', 'database', 'storage', 'resources', 'terminal', 'audit', 'other']
const taskStatusOrder = ['running', 'pending', 'success', 'failed', 'cancelled', 'timeout', 'error', 'unknown']

function countBy<T>(items: T[], mapper: (item: T) => string) {
  const counts = new Map<string, number>()
  for (const item of items) {
    const key = mapper(item)
    counts.set(key, (counts.get(key) ?? 0) + 1)
  }
  return counts
}

function taskCategory(task: TaskListItem) {
  const prefix = task.type?.split('.')?.[0] || 'other'
  if (prefix === 'database' || prefix === 'databases' || prefix === 'mysql' || prefix === 'redis') {
    return 'database'
  }
  if (prefix === 'storage' || prefix === 'minio') {
    return 'storage'
  }
  if (prefix === 'resource' || prefix === 'resources' || prefix === 'toolbox') {
    return 'resources'
  }
  if (taskCategoryOrder.includes(prefix)) {
    return prefix
  }
  return prefix || 'other'
}

function taskCategoryLabel(category: string) {
  const key = `tasks.categories.${category}`
  const text = t(key)
  return text === key ? category : text
}

function toggleTask(id: string, checked: boolean) {
  const ids = new Set(props.selectedIds ?? [])
  if (checked) {
    ids.add(id)
  } else {
    ids.delete(id)
  }
  const allowed = new Set(scopedTasks.value.map((task) => task.id))
  emit('selectionChange', Array.from(ids).filter((value) => allowed.has(value)))
}

function statusLabel(status: string) {
  if (status === 'running') {
    return t('common.running')
  }
  if (status === 'pending') {
    return t('status.pending')
  }
  if (status === 'success') {
    return t('status.success')
  }
  if (status === 'failed') {
    return t('status.failed')
  }
  if (status === 'error') {
    return t('status.error')
  }
  const key = `status.${status}`
  const text = t(key)
  return text === key ? status : text
}

function taskTime(value?: string) {
  const time = value ? new Date(value).getTime() : 0
  return Number.isNaN(time) ? 0 : time
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
.task-sidebar {
  padding: 10px;
  min-width: 0;
  display: flex;
  flex-direction: column;
  height: 100%;
  min-height: 0;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: var(--aifar-surface);
  box-shadow: var(--aifar-shadow-card);
}

.sidebar-title {
  display: flex;
  justify-content: space-between;
  align-items: center;
  color: var(--aifar-ink);
  font-size: 13px;
  font-weight: 850;
  margin-bottom: 8px;
}

.count-badge {
  border: 1px solid #cfe0f5;
  background: #f7fbff;
  border-radius: var(--aifar-radius-sm);
  color: #57708d;
  min-width: 22px;
  height: 20px;
  display: inline-grid;
  place-items: center;
  font-size: 12px;
}

.task-filters {
  display: grid;
  gap: 8px;
  margin-bottom: 10px;
}

.filter-option {
  width: 100%;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.task-list-scroll {
  flex: 1 1 auto;
  min-height: 0;
  overflow-y: auto;
  padding-right: 4px;
}

.task-list-scroll::-webkit-scrollbar {
  width: 6px;
}

.task-list-scroll::-webkit-scrollbar-thumb {
  border-radius: 999px;
  background: #c9d7ea;
}

.task-list-scroll::-webkit-scrollbar-track {
  background: #f5f9ff;
}

.task-list {
  display: grid;
  gap: 10px;
}

.task-item {
  text-align: left;
  border: 1px solid var(--aifar-border);
  background: #fff;
  border-radius: var(--aifar-radius);
  padding: 10px;
  cursor: pointer;
  display: grid;
  gap: 6px;
  min-width: 0;
  transition: border-color .16s ease, box-shadow .16s ease, background .16s ease;
}

.task-item:hover {
  border-color: #91caff;
  box-shadow: 0 4px 14px rgba(22, 119, 255, .1);
}

.task-item.active {
  border-color: #8ec5ff;
  background: var(--aifar-primary-soft);
}

.task-item-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  min-width: 0;
}

.task-title {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  min-width: 0;
}

.task-title :deep(.el-checkbox) {
  height: 18px;
}

.task-type {
  color: var(--aifar-ink);
  font-size: 13px;
  font-weight: 850;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.task-category {
  flex: 0 0 auto;
  border: 1px solid #cfe0f5;
  border-radius: var(--aifar-radius-sm);
  background: #f7fbff;
  color: #57708d;
  padding: 1px 5px;
  font-size: 10px;
  line-height: 16px;
  font-weight: 850;
}

.task-target {
  color: var(--aifar-text-secondary);
  font-size: 12px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.task-meta {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  color: #8a98ad;
  font-size: 11px;
}

.task-pagination {
  display: grid;
  gap: 8px;
  padding-top: 10px;
  border-top: 1px solid var(--aifar-border-soft);
  margin-top: 10px;
}

.page-size-select {
  width: 100%;
}

.task-pagination :deep(.el-pagination) {
  justify-content: center;
  flex-wrap: wrap;
  gap: 4px;
}

@media (max-width: 980px) {
  .task-sidebar {
    height: auto;
  }

  .task-list-scroll {
    max-height: 360px;
  }
}
</style>
