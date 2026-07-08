<template>
  <transition-group name="global-task-progress" tag="div" class="global-task-progress-stack">
    <article
      v-for="task in tasks"
      :key="task.id"
      class="global-task-progress"
      role="button"
      tabindex="0"
      @click="openTask(task.id)"
      @keydown.enter.prevent="openTask(task.id)"
      @keydown.space.prevent="openTask(task.id)"
    >
      <div class="task-main">
        <StatusTag :status="task.status" />
        <div class="task-copy">
          <strong>{{ taskTitle(task) }}</strong>
          <span>{{ taskSubtitle(task) }}</span>
        </div>
      </div>
      <el-progress class="task-progress" :percentage="task.progress" :status="progressStatus(task.status)" :show-text="false" />
      <el-tooltip :content="t('taskProgress.open')" placement="bottom">
        <el-button class="task-icon-button" text @click.stop="openTask(task.id)">
          <el-icon><Right /></el-icon>
        </el-button>
      </el-tooltip>
      <el-tooltip :content="t('taskProgress.dismiss')" placement="bottom">
        <el-button class="task-icon-button" text @click.stop="dismissTask(task.id)">
          <el-icon><Close /></el-icon>
        </el-button>
      </el-tooltip>
    </article>
  </transition-group>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { Close, Right } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import StatusTag from './StatusTag.vue'
import { useI18n } from '../i18n'
import { useTaskProgressStore, type TrackedTask } from '../stores/taskProgress'

const router = useRouter()
const taskProgress = useTaskProgressStore()
const { t } = useI18n()

const tasks = computed(() => taskProgress.visibleTasks)

function taskTitle(task: TrackedTask) {
  return task.label || task.type || t('taskProgress.latest')
}

function taskSubtitle(task: TrackedTask) {
  const target = task.target ? ` · ${task.target}` : ''
  const count = taskProgress.runningCount > 1 ? ` · ${t('taskProgress.runningCount', { count: taskProgress.runningCount })}` : ''
  return `${t('taskProgress.clickHint')}${target}${count}`
}

function progressStatus(status: string) {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'cancelled') return 'exception'
  return undefined
}

function openTask(taskId: string) {
  if (taskId) {
    void router.push({ path: '/tasks', query: { taskId } })
  }
}

function dismissTask(taskId: string) {
  if (taskId) {
    taskProgress.dismiss(taskId)
  }
}

onMounted(() => {
  taskProgress.resume()
})
</script>

<style scoped>
.global-task-progress-stack {
  position: fixed;
  top: 12px;
  right: 14px;
  z-index: 2060;
  width: min(440px, calc(100vw - var(--aifar-sidebar-width) - 28px));
  max-height: calc(100vh - 24px);
  display: grid;
  gap: 8px;
  overflow: auto;
  pointer-events: none;
  scrollbar-width: thin;
}

.global-task-progress {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 28px 28px;
  align-items: center;
  gap: 8px;
  width: 100%;
  min-height: 72px;
  padding: 12px;
  border: 1px solid rgba(217, 226, 239, .92);
  border-radius: var(--aifar-radius-lg);
  background: rgba(255, 255, 255, .98);
  box-shadow: 0 14px 36px rgba(15, 35, 68, .18), 0 4px 12px rgba(15, 35, 68, .08);
  cursor: pointer;
  pointer-events: auto;
  backdrop-filter: blur(10px);
}

.global-task-progress:hover {
  background: #fff;
  border-color: #b9d6ff;
  box-shadow: 0 18px 40px rgba(15, 35, 68, .2), 0 4px 14px rgba(22, 119, 255, .12);
}

.task-main {
  grid-column: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
}

.task-copy {
  min-width: 0;
  display: grid;
  gap: 2px;
}

.task-copy strong,
.task-copy span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-copy strong {
  color: var(--aifar-text);
  font-size: 13px;
  line-height: 16px;
  font-weight: 850;
}

.task-copy span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 16px;
}

.task-progress {
  grid-column: 1 / -1;
  grid-row: 2;
  min-width: 0;
}

.task-icon-button {
  width: 28px;
  height: 28px;
  padding: 0;
}

.global-task-progress-enter-active,
.global-task-progress-leave-active,
.global-task-progress-move {
  transition: opacity .16s ease, transform .16s ease;
}

.global-task-progress-enter-from,
.global-task-progress-leave-to {
  opacity: 0;
  transform: translateY(-6px) translateX(10px);
}

@media (max-width: 720px) {
  .global-task-progress-stack {
    top: 10px;
    right: 10px;
    left: 10px;
    width: auto;
  }
}
</style>
