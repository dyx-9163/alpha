<template>
  <div class="task-log-pane">
    <div class="task-toolbar">
      <div>
        <h2>{{ t('tasks.deploymentLogs') }}</h2>
        <p>{{ t('tasks.logHint') }}</p>
      </div>
      <div class="task-actions">
        <el-button size="small" @click="loadTasks">{{ t('common.refresh') }}</el-button>
        <ConfirmAction
          :message="t('tasks.confirmClearLogs')"
          :title="t('tasks.clearLogs')"
          :confirm-text="t('tasks.clearLogs')"
          :cancel-text="t('common.cancel')"
          :disabled="!selectedTaskId"
          @confirm="clearCurrentTaskLogs"
        >
          <template #default="{ confirm }">
            <el-button size="small" :disabled="!selectedTaskId" @click="confirm">{{ t('tasks.clearLogs') }}</el-button>
          </template>
        </ConfirmAction>
        <ConfirmAction
          :message="t('tasks.confirmClearSelectedLogs', { count: selectedTaskIds.length })"
          :title="t('tasks.clearLogs')"
          :confirm-text="t('tasks.clearLogs')"
          :cancel-text="t('common.cancel')"
          :disabled="!selectedTaskIds.length"
          @confirm="clearSelectedTaskLogs"
        >
          <template #default="{ confirm }">
            <el-button size="small" :disabled="!selectedTaskIds.length" @click="confirm">
              {{ t('tasks.clearSelectedLogs', { count: selectedTaskIds.length }) }}
            </el-button>
          </template>
        </ConfirmAction>
        <DangerConfirm
          :title="deleteConfirmText"
          :confirm-text="t('common.delete')"
          :cancel-text="t('common.cancel')"
          :disabled="!selectedTaskId && !selectedTaskIds.length"
          @confirm="deleteSelectedTask"
        >
          <el-button size="small" type="danger" plain :disabled="!selectedTaskId && !selectedTaskIds.length">
            {{ selectedTaskIds.length ? t('tasks.deleteSelected', { count: selectedTaskIds.length }) : t('tasks.deleteTask') }}
          </el-button>
        </DangerConfirm>
      </div>
    </div>

    <div class="task-layout">
      <TaskListPanel
        :tasks="tasks"
        :selected-id="selectedTaskId"
        :selected-ids="selectedTaskIds"
        :type-prefix="props.typePrefix"
        @select="selectTask"
        @selection-change="selectedTaskIds = $event"
      />

      <TaskRunPanel v-if="detail?.task" :detail="detail" :servers="servers" class="task-detail" />
      <section v-else class="task-detail task-detail-empty">
        <el-empty :description="t('tasks.noTaskSelected')" :image-size="96" />
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { apiDelete, apiGet, asArray } from '../api/client'
import { useI18n } from '../i18n'
import ConfirmAction from './ConfirmAction.vue'
import DangerConfirm from './DangerConfirm.vue'
import TaskListPanel from './TaskListPanel.vue'
import TaskRunPanel from './TaskRunPanel.vue'

type Task = {
  id: string
  type: string
  target: string
  status: string
  error?: string
  createdAt: string
  startedAt?: string
  finishedAt?: string
}

type TaskLog = {
  id: number
  taskId: string
  target?: string
  level: string
  message: string
  createdAt: string
}

type TaskTarget = {
  id: number
  taskId: string
  target: string
  status: string
  error?: string
}

type TaskStep = {
  id: number
  taskId: string
  target: string
  name: string
  title: string
  order: number
  status: string
  error?: string
}

type TaskDetail = {
  task: Task
  logs: TaskLog[]
  targets: TaskTarget[]
  steps: TaskStep[]
}

type ServerSummary = {
  id: string
  name?: string
  host?: string
}

const props = defineProps<{ taskId?: string; typePrefix?: string }>()
const { t } = useI18n()

const tasks = ref<Task[]>([])
const servers = ref<ServerSummary[]>([])
const detail = ref<TaskDetail | null>(null)
const selectedTaskId = ref('')
const selectedTaskIds = ref<string[]>([])
let source: EventSource | null = null
let refreshTimer: number | null = null

const selectableTasks = computed(() => {
  const ordered = tasks.value.slice().sort((a, b) => taskTime(b.createdAt) - taskTime(a.createdAt))
  if (!props.typePrefix) {
    return ordered
  }
  return ordered.filter((task) => task.type.startsWith(props.typePrefix || ''))
})

const deleteConfirmText = computed(() => {
  return selectedTaskIds.value.length > 1 ? t('tasks.confirmDeleteTasks', { count: selectedTaskIds.value.length }) : t('tasks.confirmDeleteTask')
})

watch(
  () => props.taskId,
  (taskId) => {
    if (taskId) {
      void selectTask(taskId)
    }
  },
  { immediate: true }
)

onMounted(async () => {
  await Promise.all([loadTasks(), loadServers()])
  if (!selectedTaskId.value && selectableTasks.value.length) {
    await selectTask(selectableTasks.value[0].id)
  }
})

onBeforeUnmount(() => closeSource())

async function loadTasks() {
  tasks.value = asArray(await apiGet<Task[] | null>('/tasks').catch(() => []))
  if (!selectedTaskId.value && selectableTasks.value.length) {
    await selectTask(selectableTasks.value[0].id)
  }
}

async function loadServers() {
  servers.value = asArray(await apiGet<ServerSummary[] | null>('/servers').catch(() => []))
}

async function selectTask(taskId: string) {
  if (!taskId) {
    return
  }
  selectedTaskId.value = taskId
  closeSource()
  await loadTaskDetail(taskId)
  openSource(taskId)
  startDetailRefresh(taskId)
}

async function loadTaskDetail(taskId: string) {
  detail.value = await apiGet<TaskDetail>(`/tasks/${taskId}`).catch(() => null)
}

function openSource(taskId: string) {
  const token = encodeURIComponent(localStorage.getItem('aifar-session-token') ?? '')
  source = new EventSource(`/api/v2/tasks/${taskId}/events?token=${token}`)
  source.addEventListener('task-event', (event) => {
    const payload = JSON.parse((event as MessageEvent).data) as TaskLog
    mergeLog(payload)
  })
}

function closeSource() {
  source?.close()
  source = null
  stopDetailRefresh()
}

function mergeLog(log: TaskLog) {
  if (!detail.value || detail.value.task.id !== log.taskId) {
    return
  }
  if (detail.value.logs.some((item) => item.id === log.id)) {
    return
  }
  detail.value.logs.push(log)
}

async function clearCurrentTaskLogs() {
  if (!selectedTaskId.value || !detail.value) {
    return
  }
  try {
    await apiDelete(`/tasks/${selectedTaskId.value}/logs`)
    detail.value.logs = []
    ElMessage.success(t('tasks.logsCleared'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

async function clearSelectedTaskLogs() {
  const ids = selectedTaskIds.value.slice()
  if (!ids.length) {
    return
  }
  try {
    await apiDelete('/tasks/logs', { ids })
    if (ids.includes(selectedTaskId.value) && detail.value) {
      detail.value.logs = []
    }
    selectedTaskIds.value = []
    await loadTasks()
    ElMessage.success(t('tasks.logsClearedForTasks', { count: ids.length }))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

async function deleteSelectedTask() {
  const ids = selectedTaskIds.value.length ? selectedTaskIds.value : selectedTaskId.value ? [selectedTaskId.value] : []
  if (!ids.length) {
    return
  }
  try {
    if (ids.length === 1) {
      await apiDelete(`/tasks/${ids[0]}`)
    } else {
      await apiDelete('/tasks', { ids })
    }
    if (ids.includes(selectedTaskId.value)) {
      closeSource()
      detail.value = null
      selectedTaskId.value = ''
    }
    selectedTaskIds.value = []
    await loadTasks()
    ElMessage.success(t('tasks.tasksDeleted', { count: ids.length }))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

function startDetailRefresh(taskId: string) {
  stopDetailRefresh()
  refreshTimer = window.setInterval(async () => {
    if (selectedTaskId.value !== taskId) {
      stopDetailRefresh()
      return
    }
    await Promise.all([loadTaskDetail(taskId), loadTasks()])
    const status = detail.value?.task.status
    if (status && !['pending', 'running'].includes(status)) {
      stopDetailRefresh()
    }
  }, 2500)
}

function stopDetailRefresh() {
  if (refreshTimer !== null) {
    window.clearInterval(refreshTimer)
    refreshTimer = null
  }
}

function taskTime(value?: string) {
  const time = value ? new Date(value).getTime() : 0
  return Number.isNaN(time) ? 0 : time
}
</script>

<style scoped>
.task-log-pane {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  gap: 12px;
  min-width: 0;
  min-height: 0;
}

.task-toolbar {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  box-shadow: var(--aifar-shadow-card);
}

.task-toolbar h2 {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 15px;
  line-height: 22px;
  font-weight: 850;
}

.task-toolbar p {
  margin: 3px 0 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.task-actions {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  justify-content: flex-end;
}

.task-layout {
  display: grid;
  grid-template-columns: clamp(220px, 20vw, 300px) minmax(0, 1fr);
  gap: 12px;
  min-height: 0;
  min-width: 0;
  height: calc(100vh - 210px);
}

.task-detail {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: var(--aifar-surface);
  box-shadow: var(--aifar-shadow-card);
}

.task-detail {
  padding: 10px;
  min-width: 0;
  min-height: 0;
  overflow: auto;
}

@media (max-width: 980px) {
  .task-toolbar {
    display: grid;
  }

  .task-actions {
    justify-content: flex-start;
  }

  .task-layout {
    grid-template-columns: 1fr;
    height: auto;
  }

}
</style>
