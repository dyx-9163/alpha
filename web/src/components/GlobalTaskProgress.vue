<template>
  <transition name="global-task-progress">
    <div
      v-if="task"
      class="global-task-progress"
      role="button"
      tabindex="0"
      @click="openTask"
      @keydown.enter.prevent="openTask"
      @keydown.space.prevent="openTask"
    >
      <div class="task-main">
        <StatusTag :status="task.status" />
        <div class="task-copy">
          <strong>{{ taskTitle }}</strong>
          <span>{{ taskSubtitle }}</span>
        </div>
      </div>
      <el-progress class="task-progress" :percentage="task.progress" :status="progressStatus" :show-text="false" />
      <el-tooltip :content="t('taskProgress.open')" placement="bottom">
        <el-button class="task-icon-button" text @click.stop="openTask">
          <el-icon><Right /></el-icon>
        </el-button>
      </el-tooltip>
      <el-tooltip :content="t('taskProgress.dismiss')" placement="bottom">
        <el-button class="task-icon-button" text @click.stop="dismissTask">
          <el-icon><Close /></el-icon>
        </el-button>
      </el-tooltip>
    </div>
  </transition>
</template>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { Close, Right } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import StatusTag from './StatusTag.vue'
import { useI18n } from '../i18n'
import { useTaskProgressStore } from '../stores/taskProgress'

const router = useRouter()
const taskProgress = useTaskProgressStore()
const { t } = useI18n()

const task = computed(() => taskProgress.activeTask)
const taskTitle = computed(() => task.value?.label || task.value?.type || t('taskProgress.latest'))
const taskSubtitle = computed(() => {
  const current = task.value
  if (!current) {
    return ''
  }
  const target = current.target ? ` · ${current.target}` : ''
  const count = taskProgress.runningCount > 1 ? ` · ${t('taskProgress.runningCount', { count: taskProgress.runningCount })}` : ''
  return `${t('taskProgress.clickHint')}${target}${count}`
})
const progressStatus = computed(() => {
  if (task.value?.status === 'success') return 'success'
  if (task.value?.status === 'failed' || task.value?.status === 'cancelled') return 'exception'
  return undefined
})

function openTask() {
  if (task.value?.id) {
    void router.push({ path: '/tasks', query: { taskId: task.value.id } })
  }
}

function dismissTask() {
  if (task.value?.id) {
    taskProgress.dismiss(task.value.id)
  }
}

onMounted(() => {
  taskProgress.resume()
})
</script>

<style scoped>
.global-task-progress {
  position: fixed;
  top: 12px;
  right: 14px;
  z-index: 2060;
  display: grid;
  grid-template-columns: minmax(0, 1fr) 28px 28px;
  align-items: center;
  gap: 8px;
  width: min(440px, calc(100vw - var(--aifar-sidebar-width) - 28px));
  min-height: 72px;
  padding: 12px;
  border: 1px solid rgba(217, 226, 239, .92);
  border-radius: var(--aifar-radius-lg);
  background: rgba(255, 255, 255, .98);
  box-shadow: 0 14px 36px rgba(15, 35, 68, .18), 0 4px 12px rgba(15, 35, 68, .08);
  cursor: pointer;
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
.global-task-progress-leave-active {
  transition: opacity .16s ease, transform .16s ease;
}

.global-task-progress-enter-from,
.global-task-progress-leave-to {
  opacity: 0;
  transform: translateY(-6px) translateX(10px);
}

@media (max-width: 720px) {
  .global-task-progress {
    top: 10px;
    right: 10px;
    left: 10px;
    width: auto;
  }
}
</style>
