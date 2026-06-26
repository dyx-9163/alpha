<template>
  <div class="task-log-pane">
    <div class="task-toolbar">
      <div>
        <h2>{{ t('tasks.deploymentLogs') }}</h2>
        <p>{{ t('tasks.logHint') }}</p>
      </div>
      <div class="task-actions">
        <el-button size="small" @click="loadTasks">{{ t('common.refresh') }}</el-button>
        <el-button size="small" :disabled="!selectedTaskId" @click="clearSelectedLogs">{{ t('tasks.clearLogs') }}</el-button>
        <el-button size="small" type="danger" plain :disabled="!selectedTaskId && !selectedTaskIds.length" @click="deleteSelectedTask">
          {{ selectedTaskIds.length ? t('tasks.deleteSelected', { count: selectedTaskIds.length }) : t('tasks.deleteTask') }}
        </el-button>
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

      <section class="task-detail">
        <template v-if="detail?.task">
          <div class="task-summary">
            <div>
              <span class="summary-label">{{ t('table.type') }}</span>
              <strong>{{ detail.task.type }}</strong>
            </div>
            <div>
              <span class="summary-label">{{ t('common.target') }}</span>
              <strong>{{ detail.task.target || t('tasks.controlPlane') }}</strong>
            </div>
            <div>
              <span class="summary-label">{{ t('common.status') }}</span>
              <StatusTag :status="detail.task.status" />
            </div>
          </div>

          <div v-if="targetGroups.length" class="target-log-list">
            <section v-for="group in targetGroups" :key="group.key" class="target-log-group">
              <div class="target-header">
                <div>
                  <h3>{{ group.label }}</h3>
                  <p>{{ group.target || t('tasks.controlPlane') }}</p>
                </div>
                <StatusTag v-if="group.status" :status="group.status" />
              </div>

              <div v-if="group.steps.length" class="step-row">
                <span v-for="step in group.steps" :key="`${group.key}-${step.name}`" class="step-pill" :class="step.status">
                  {{ step.order }}. {{ step.title || step.name }}
                </span>
              </div>

              <div class="terminal-box">
                <div v-if="group.logs.length" class="log-lines">
                  <div v-for="log in group.logs" :key="log.id" class="log-line" :class="log.level">
                    <span class="log-time">{{ formatTime(log.createdAt) }}</span>
                    <span class="log-level">[{{ log.level }}]</span>
                    <span class="log-message">{{ log.message }}</span>
                  </div>
                </div>
                <span v-else class="empty-log">{{ t('tasks.noLogs') }}</span>
              </div>
            </section>
          </div>

          <div v-else class="empty-detail">
            {{ t('tasks.noLogs') }}
          </div>
        </template>

        <el-empty v-else :description="t('tasks.noTaskSelected')" :image-size="96" />
      </section>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { apiDelete, apiGet, asArray } from '../api/client'
import { useI18n } from '../i18n'
import TaskListPanel from './TaskListPanel.vue'
import StatusTag from './StatusTag.vue'

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

const serverMap = computed(() => {
  const out = new Map<string, ServerSummary>()
  for (const server of servers.value) {
    out.set(server.id, server)
  }
  return out
})

const targetGroups = computed(() => {
  const current = detail.value
  if (!current) {
    return []
  }
  const groups = new Map<string, {
    key: string
    target: string
    label: string
    status: string
    logs: TaskLog[]
    steps: TaskStep[]
  }>()
  const ensure = (target?: string) => {
    const key = target || '__control__'
    if (!groups.has(key)) {
      groups.set(key, {
        key,
        target: target || '',
        label: target ? serverLabel(target) : t('tasks.controlPlane'),
        status: '',
        logs: [],
        steps: []
      })
    }
    return groups.get(key)!
  }

  for (const target of current.targets) {
    const group = ensure(target.target)
    group.status = target.status
  }
  for (const step of current.steps) {
    const group = ensure(step.target)
    group.steps.push(step)
  }
  for (const log of current.logs) {
    ensure(log.target).logs.push(log)
  }

  return Array.from(groups.values()).map((group) => ({
    ...group,
    steps: group.steps.slice().sort((a, b) => a.order - b.order || a.id - b.id)
  }))
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

async function clearSelectedLogs() {
  if (!selectedTaskId.value || !detail.value) {
    return
  }
  try {
    await ElMessageBox.confirm(t('tasks.confirmClearLogs'), t('tasks.clearLogs'), { type: 'warning' })
  } catch {
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

async function deleteSelectedTask() {
  const ids = selectedTaskIds.value.length ? selectedTaskIds.value : selectedTaskId.value ? [selectedTaskId.value] : []
  if (!ids.length) {
    return
  }
  try {
    const message = ids.length > 1 ? t('tasks.confirmDeleteTasks', { count: ids.length }) : t('tasks.confirmDeleteTask')
    await ElMessageBox.confirm(message, t('tasks.deleteTask'), { type: 'warning' })
  } catch {
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

function serverLabel(target: string) {
  const server = serverMap.value.get(target)
  if (!server) {
    return target
  }
  if (server.name && server.host) {
    return `${server.name} (${server.host})`
  }
  return server.name || server.host || target
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
}

.task-summary {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  overflow: hidden;
  margin-bottom: 12px;
  background: #fbfdff;
}

.task-summary > div {
  display: grid;
  gap: 4px;
  padding: 9px 10px;
  min-width: 0;
}

.task-summary > div + div {
  border-left: 1px solid var(--aifar-border-soft);
}

.summary-label {
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.task-summary strong {
  color: var(--aifar-ink);
  font-size: 13px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.target-log-list {
  display: grid;
  gap: 12px;
}

.target-log-group {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  overflow: hidden;
  background: #fff;
}

.target-header {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 12px;
  border-bottom: 1px solid var(--aifar-border-soft);
  background: linear-gradient(180deg, #fff, #f8fbff);
}

.target-header h3 {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 14px;
  line-height: 20px;
  font-weight: 850;
}

.target-header p {
  margin: 2px 0 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.step-row {
  display: flex;
  gap: 8px;
  flex-wrap: wrap;
  padding: 10px 12px;
  border-bottom: 1px solid var(--aifar-border-soft);
}

.step-pill {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-sm);
  padding: 2px 7px;
  color: #53657d;
  background: #fff;
  font-size: 11px;
  font-weight: 800;
}

.step-pill.running,
.step-pill.pending {
  color: #b7791f;
  border-color: #f6d794;
  background: #fffaf0;
}

.step-pill.success {
  color: #16833a;
  border-color: #a7e1b4;
  background: #f0fff4;
}

.step-pill.failed {
  color: #c0392b;
  border-color: #f5b5ad;
  background: #fff5f5;
}

.terminal-box {
  min-height: 180px;
  max-height: clamp(280px, 44vh, 520px);
  overflow: auto;
  background: var(--aifar-code-bg);
  color: #dbeafe;
  font-family: Consolas, 'SFMono-Regular', monospace;
  font-size: 12px;
  line-height: 20px;
  padding: 12px;
  white-space: pre-wrap;
}

.log-line {
  display: grid;
  grid-template-columns: minmax(132px, 168px) 56px minmax(0, 1fr);
  gap: 6px;
}

.log-line.error {
  color: #fecaca;
}

.log-line.warn {
  color: #fde68a;
}

.log-time {
  color: #93a4bc;
}

.log-level {
  color: #bfdbfe;
  font-weight: 850;
}

.log-message {
  word-break: break-word;
}

.empty-log,
.empty-detail {
  color: #93a4bc;
}

.empty-detail {
  border: 1px dashed var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  padding: 40px;
  text-align: center;
  background: #fbfdff;
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
  }

  .task-summary {
    grid-template-columns: 1fr;
  }

  .task-summary > div + div {
    border-left: 0;
    border-top: 1px solid var(--aifar-border-soft);
  }

  .log-line {
    grid-template-columns: 1fr;
  }
}
</style>
