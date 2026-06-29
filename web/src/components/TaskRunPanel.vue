<template>
  <section class="task-run-panel">
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

    <div v-if="targetGroups.length" class="target-log-list" :class="{ 'has-toolbar': serverTargetGroups.length > 1 }">
      <div v-if="serverTargetGroups.length > 1" class="target-log-toolbar">
        <span>{{ t('table.server') }}</span>
        <el-select v-model="selectedTargetKey" size="small" class="target-select">
          <el-option v-for="group in serverTargetGroups" :key="group.key" :label="group.label" :value="group.key" />
        </el-select>
      </div>

      <section v-for="group in visibleTargetGroups" :key="group.key" class="target-log-group">
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

        <LogOutput class="target-log-output" :lines="group.logs" :empty-text="t('tasks.noLogs')" min-height="0" auto-scroll />
      </section>
    </div>

    <div v-else class="empty-detail">
      {{ t('tasks.noLogs') }}
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '../i18n'
import LogOutput from './LogOutput.vue'
import StatusTag from './StatusTag.vue'

type Task = {
  id: string
  type: string
  target: string
  status: string
  createdAt: string
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
}

type TaskStep = {
  id: number
  taskId: string
  target: string
  name: string
  title: string
  order: number
  status: string
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

const props = withDefaults(defineProps<{
  detail: TaskDetail
  servers?: ServerSummary[]
}>(), {
  servers: () => []
})

const { t } = useI18n()
const selectedTargetKey = ref('')

const serverMap = computed(() => {
  const out = new Map<string, ServerSummary>()
  for (const server of props.servers) {
    out.set(server.id, server)
  }
  return out
})

const targetGroups = computed(() => {
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

  for (const target of props.detail.targets) {
    const group = ensure(target.target)
    group.status = target.status
  }
  for (const step of props.detail.steps) {
    ensure(step.target).steps.push(step)
  }
  for (const log of props.detail.logs) {
    ensure(log.target).logs.push(log)
  }

  return Array.from(groups.values())
    .map((group) => ({
      ...group,
      steps: group.steps.slice().sort((a, b) => a.order - b.order || a.id - b.id)
    }))
    .sort((a, b) => {
      if (!a.target && b.target) {
        return 1
      }
      if (a.target && !b.target) {
        return -1
      }
      return 0
    })
})

const serverTargetGroups = computed(() => targetGroups.value.filter((group) => group.target))
const controlPlaneGroup = computed(() => targetGroups.value.find((group) => !group.target) || null)

const visibleTargetGroups = computed(() => {
  if (!targetGroups.value.length) {
    return []
  }
  const selected = serverTargetGroups.value.find((group) => group.key === selectedTargetKey.value)
    || serverTargetGroups.value[0]
  const out = selected ? [selected] : []
  if (controlPlaneGroup.value) {
    out.push(controlPlaneGroup.value)
  }
  return out
})

watch(
  serverTargetGroups,
  (groups) => {
    if (!groups.length) {
      selectedTargetKey.value = ''
      return
    }
    if (!groups.some((group) => group.key === selectedTargetKey.value)) {
      selectedTargetKey.value = groups[0].key
    }
  },
  { immediate: true }
)

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

</script>

<style scoped>
.task-run-panel {
  min-width: 0;
  height: 100%;
  min-height: 0;
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  overflow: hidden;
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
  min-height: 0;
  display: grid;
  grid-template-rows: minmax(0, 1fr);
  gap: 12px;
  overflow: hidden;
}

.target-log-list.has-toolbar {
  grid-template-rows: auto minmax(0, 1fr);
}

.target-log-toolbar {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 8px;
  min-width: 0;
}

.target-log-toolbar span {
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 700;
}

.target-select {
  width: min(320px, 48vw);
}

.target-log-group {
  flex: 1 1 0;
  min-height: 0;
  display: grid;
  grid-template-rows: auto auto minmax(0, 1fr);
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  overflow: hidden;
  background: #fff;
}

.target-log-output {
  height: 100%;
  min-height: 0 !important;
  border-radius: 0;
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

.empty-detail {
  color: #93a4bc;
  border: 1px dashed var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  padding: 40px;
  text-align: center;
  background: #fbfdff;
}

@media (max-width: 980px) {
  .task-summary {
    grid-template-columns: 1fr;
  }

  .task-summary > div + div {
    border-left: 0;
    border-top: 1px solid var(--aifar-border-soft);
  }
}
</style>
