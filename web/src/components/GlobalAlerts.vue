<template>
  <div v-if="canOpenMessageCenter" class="global-alert-entry">
    <el-tooltip :content="t('messageCenter.title')" placement="bottom">
      <el-badge :value="badgeValue" :hidden="badgeValue === 0" :type="badgeType" :max="99">
        <el-button class="alert-bell-button" circle @click="openDrawer">
          <el-icon><Bell /></el-icon>
        </el-button>
      </el-badge>
    </el-tooltip>
  </div>

  <el-drawer v-model="alerts.drawerVisible" size="460px" :with-header="false" append-to-body class="alerts-drawer">
    <div class="alert-drawer-shell">
      <header class="alert-drawer-head">
        <div>
          <h2>{{ t('messageCenter.title') }}</h2>
          <span>{{ t('messageCenter.subtitle', { alertCount: alerts.openCount, taskCount: taskProgress.visibleTasks.length }) }}</span>
        </div>
        <el-tooltip :content="t('common.refresh')" placement="bottom">
          <el-button class="alert-icon-action" text :loading="refreshing" @click="reloadActiveTab">
            <el-icon><RefreshRight /></el-icon>
          </el-button>
        </el-tooltip>
      </header>

      <div class="message-center-tabs" role="tablist" :aria-label="t('messageCenter.title')">
        <button
          type="button"
          :class="{ active: activeTab === 'alerts' }"
          data-message-center-tab="alerts"
          role="tab"
          :aria-selected="activeTab === 'alerts'"
          @click="activeTab = 'alerts'"
        >
          {{ t('messageCenter.systemAlerts') }}
          <span>{{ alerts.openCount }}</span>
        </button>
        <button
          type="button"
          :class="{ active: activeTab === 'tasks' }"
          data-message-center-tab="tasks"
          role="tab"
          :aria-selected="activeTab === 'tasks'"
          @click="activeTab = 'tasks'"
        >
          {{ t('messageCenter.taskAlerts') }}
          <span>{{ taskProgress.visibleTasks.length }}</span>
        </button>
      </div>

      <template v-if="activeTab === 'alerts'">
        <div class="alert-summary-strip">
          <span class="severity-dot critical"></span>
          <strong>{{ alerts.criticalCount }}</strong>
          <span>{{ t('alerts.severity.critical') }}</span>
          <span class="severity-dot warning"></span>
          <strong>{{ alerts.warningCount }}</strong>
          <span>{{ t('alerts.severity.warning') }}</span>
        </div>

        <el-empty v-if="!alerts.openAlerts.length" :description="t('alerts.empty')" />

        <div v-else class="alert-list">
          <article
            v-for="alert in alerts.openAlerts"
            :key="alert.id"
            class="alert-item"
            :class="[`is-${alert.severity}`, { 'is-muted': isMuted(alert) }]"
            role="button"
            tabindex="0"
            @click="openAlert(alert)"
            @keydown.enter.prevent="openAlert(alert)"
            @keydown.space.prevent="openAlert(alert)"
          >
            <div class="alert-item-head">
              <el-tag :type="severityType(alert.severity)" effect="light" size="small">
                {{ severityLabel(alert.severity) }}
              </el-tag>
              <span>{{ scopeLabel(alert) }}</span>
              <el-icon class="alert-open-icon"><Right /></el-icon>
            </div>
            <strong>{{ alert.title }}</strong>
            <p v-if="alert.message">{{ alert.message }}</p>
            <div class="alert-meta">
              <span>{{ t('alerts.lastSeenAt') }} {{ formatTime(alert.lastSeenAt || alert.updatedAt) }}</span>
              <span v-if="isMuted(alert)">{{ t('alerts.mutedUntil') }} {{ formatTime(alert.mutedUntil) }}</span>
            </div>
            <div class="alert-actions" @click.stop>
              <el-tooltip :content="t('alerts.ack')" placement="top">
                <el-button class="alert-icon-action" text @click="ack(alert)">
                  <el-icon><Check /></el-icon>
                </el-button>
              </el-tooltip>
              <el-tooltip :content="t('alerts.mute')" placement="top">
                <el-button class="alert-icon-action" text @click="mute(alert)">
                  <el-icon><Clock /></el-icon>
                </el-button>
              </el-tooltip>
              <el-tooltip v-if="canManageAlerts" :content="t('alerts.resolve')" placement="top">
                <el-button class="alert-icon-action" text @click="resolve(alert)">
                  <el-icon><CircleCloseFilled /></el-icon>
                </el-button>
              </el-tooltip>
            </div>
          </article>
        </div>
      </template>

      <template v-else>
        <el-empty v-if="!taskProgress.visibleTasks.length" :description="t('messageCenter.noTasks')" />

        <div v-else class="alert-list">
          <article
            v-for="task in taskProgress.visibleTasks"
            :key="task.id"
            class="task-message-card"
            :class="`is-${task.status || 'pending'}`"
            role="button"
            tabindex="0"
            @click="openTask(task.id)"
            @keydown.enter.prevent="openTask(task.id)"
            @keydown.space.prevent="openTask(task.id)"
          >
            <div class="alert-item-head">
              <StatusTag :status="task.status" />
              <span>{{ task.target || task.type || t('taskProgress.latest') }}</span>
              <el-icon class="alert-open-icon"><Right /></el-icon>
            </div>
            <strong>{{ taskTitle(task) }}</strong>
            <p v-if="task.error">{{ task.error }}</p>
            <p v-else>{{ t('taskProgress.clickHint') }}</p>
            <el-progress class="task-message-progress" :percentage="task.progress" :status="progressStatus(task.status)" :show-text="false" />
            <div class="alert-actions" @click.stop>
              <el-tooltip :content="t('taskProgress.open')" placement="top">
                <el-button class="alert-icon-action" text @click="openTask(task.id)">
                  <el-icon><Right /></el-icon>
                </el-button>
              </el-tooltip>
              <el-tooltip :content="t('taskProgress.dismiss')" placement="top">
                <el-button class="alert-icon-action" text @click="dismissTask(task.id)">
                  <el-icon><CircleCloseFilled /></el-icon>
                </el-button>
              </el-tooltip>
            </div>
          </article>
        </div>
      </template>
    </div>
  </el-drawer>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Bell, Check, CircleCloseFilled, Clock, RefreshRight, Right } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import StatusTag from './StatusTag.vue'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { useAlertsStore, type AlertItem } from '../stores/alerts'
import { useSessionStore } from '../stores/session'
import { useTaskProgressStore, type TrackedTask } from '../stores/taskProgress'

const router = useRouter()
const alerts = useAlertsStore()
const session = useSessionStore()
const taskProgress = useTaskProgressStore()
const { t } = useI18n()
const activeTab = ref<'alerts' | 'tasks'>('alerts')

const canViewAlerts = computed(() => session.hasPermission(permissions.alertsView))
const canManageAlerts = computed(() => session.hasPermission(permissions.alertsManage))
const canOpenMessageCenter = computed(() => canViewAlerts.value || taskProgress.visibleTasks.length > 0)
const badgeValue = computed(() => alerts.openCount + taskProgress.runningCount + failedTaskCount.value)
const badgeType = computed(() => alerts.criticalCount > 0 ? 'danger' : 'warning')
const failedTaskCount = computed(() => taskProgress.visibleTasks.filter((task) => task.status === 'failed').length)
const refreshing = computed(() => activeTab.value === 'alerts' ? alerts.loading : false)

function openDrawer() {
  alerts.openDrawer()
  activeTab.value = taskProgress.runningCount > 0 || failedTaskCount.value > 0 ? 'tasks' : 'alerts'
  if (!alerts.loaded && !alerts.loading) {
    void reload()
  }
  taskProgress.resume()
}

async function reload() {
  try {
    await alerts.load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

function reloadActiveTab() {
  if (activeTab.value === 'tasks') {
    void taskProgress.refreshKnownTasks(taskProgress.visibleTasks.map((task) => task.id))
    return
  }
  void reload()
}

async function ack(alert: AlertItem) {
  try {
    await alerts.ack(alert.id)
    ElMessage.success(t('alerts.acked'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

async function mute(alert: AlertItem) {
  try {
    await alerts.mute(alert.id, 60)
    ElMessage.success(t('alerts.muted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

async function resolve(alert: AlertItem) {
  try {
    await alerts.resolve(alert.id)
    ElMessage.success(t('alerts.resolved'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
}

function openAlert(alert: AlertItem) {
  alerts.closeDrawer()
  void router.push(targetRoute(alert))
}

function openTask(taskId: string) {
  if (!taskId) {
    return
  }
  alerts.closeDrawer()
  void router.push({ path: '/tasks', query: { taskId } })
}

function dismissTask(taskId: string) {
  if (taskId) {
    taskProgress.dismiss(taskId)
  }
}

function taskTitle(task: TrackedTask) {
  return task.label || task.type || t('taskProgress.latest')
}

function progressStatus(status: string) {
  if (status === 'success') return 'success'
  if (status === 'failed' || status === 'cancelled') return 'exception'
  return undefined
}

function targetRoute(alert: AlertItem) {
  if (alert.scope === 'task') {
    return { path: '/tasks', query: { taskId: alert.resourceId || alert.id } }
  }
  if (alert.scope === 'server') {
    return { path: '/servers' }
  }
  if (alert.scope === 'docker.summary' || alert.app === 'docker') {
    return { path: '/containers' }
  }
  if (alert.scope === 'aifar.runtime' || alert.app === 'aifar') {
    return { path: '/containers', query: { mode: 'aifar' } }
  }
  if (['mysql', 'redis', 'mysql-router', 'mysqlrouter'].includes(String(alert.app ?? '').toLowerCase())) {
    return { path: '/database' }
  }
  if (alert.app === 'minio') {
    return { path: '/storage' }
  }
  if (alert.scope === 'collector') {
    return { path: '/settings' }
  }
  return { path: '/apps' }
}

function severityType(severity: string) {
  if (severity === 'critical') return 'danger'
  if (severity === 'warning') return 'warning'
  return 'info'
}

function severityLabel(severity: string) {
  return t(`alerts.severity.${severity}`)
}

function scopeLabel(alert: AlertItem) {
  return alert.app || alert.scope || t('common.unknown')
}

function isMuted(alert: AlertItem) {
  if (!alert.mutedUntil) return false
  const until = new Date(alert.mutedUntil)
  return Number.isFinite(until.getTime()) && until.getTime() > Date.now()
}

function formatTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime()) || date.getFullYear() < 2000) return '-'
  return date.toLocaleString()
}

onMounted(() => {
  taskProgress.resume()
  if (canViewAlerts.value) {
    void reload()
  }
})

watch(canViewAlerts, (allowed) => {
  if (allowed) {
    void reload()
  } else {
    alerts.clear()
  }
})

watch(() => taskProgress.runningCount, (count) => {
  if (alerts.drawerVisible && count > 0) {
    activeTab.value = 'tasks'
  }
})
</script>

<style scoped>
.global-alert-entry {
  position: fixed;
  top: 14px;
  right: 468px;
  z-index: 2061;
}

.alert-bell-button {
  width: 38px;
  height: 38px;
  border-color: rgba(185, 214, 255, .92);
  background: rgba(255, 255, 255, .98);
  box-shadow: 0 10px 28px rgba(15, 35, 68, .16);
  color: var(--aifar-primary);
}

.alert-drawer-shell {
  min-height: 100%;
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.alert-drawer-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding-bottom: 12px;
  border-bottom: 1px solid var(--aifar-border);
}

.alert-drawer-head h2 {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 18px;
  line-height: 24px;
}

.alert-drawer-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.alert-summary-strip {
  min-height: 36px;
  display: flex;
  align-items: center;
  gap: 7px;
  padding: 0 10px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-md);
  background: #f8fbff;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.message-center-tabs {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 6px;
  padding: 4px;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #f8fbff;
}

.message-center-tabs button {
  min-height: 34px;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 8px;
  border: 1px solid transparent;
  border-radius: var(--aifar-radius-md);
  background: transparent;
  color: var(--aifar-text-secondary);
  font-size: 13px;
  font-weight: 700;
  cursor: pointer;
}

.message-center-tabs button span {
  min-width: 22px;
  height: 20px;
  display: inline-grid;
  place-items: center;
  border-radius: 999px;
  background: rgba(86, 101, 124, .1);
  font-size: 12px;
}

.message-center-tabs button.active {
  border-color: #91caff;
  background: #fff;
  color: var(--aifar-primary);
  box-shadow: 0 5px 14px rgba(22, 119, 255, .12);
}

.severity-dot {
  width: 8px;
  height: 8px;
  border-radius: 50%;
}

.severity-dot.critical {
  background: #ef4444;
}

.severity-dot.warning {
  margin-left: 8px;
  background: #f59e0b;
}

.alert-list {
  display: grid;
  gap: 10px;
  overflow: auto;
  padding-right: 2px;
}

.alert-item {
  display: grid;
  gap: 8px;
  min-height: 128px;
  padding: 12px;
  border: 1px solid var(--aifar-border);
  border-left: 4px solid #8aa1bf;
  border-radius: var(--aifar-radius-md);
  background: #fff;
  cursor: pointer;
}

.task-message-card {
  display: grid;
  gap: 8px;
  min-height: 118px;
  padding: 12px;
  border: 1px solid var(--aifar-border);
  border-left: 4px solid #1677ff;
  border-radius: var(--aifar-radius-md);
  background: #fff;
  cursor: pointer;
}

.task-message-card.is-success {
  border-left-color: #22c55e;
}

.task-message-card.is-failed,
.task-message-card.is-cancelled {
  border-left-color: #ef4444;
}

.task-message-card:hover {
  border-color: #b9d6ff;
  box-shadow: 0 8px 22px rgba(20, 51, 94, .1);
}

.task-message-card strong {
  color: var(--aifar-ink);
  font-size: 14px;
  line-height: 20px;
}

.task-message-card p {
  margin: 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  line-height: 18px;
  word-break: break-word;
}

.task-message-progress {
  min-width: 0;
}

.alert-item.is-critical {
  border-left-color: #ef4444;
}

.alert-item.is-warning {
  border-left-color: #f59e0b;
}

.alert-item.is-muted {
  opacity: .68;
}

.alert-item:hover {
  border-color: #b9d6ff;
  box-shadow: 0 8px 22px rgba(20, 51, 94, .1);
}

.alert-item-head {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.alert-item strong {
  color: var(--aifar-ink);
  font-size: 14px;
  line-height: 20px;
}

.alert-item p {
  margin: 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  line-height: 18px;
  word-break: break-word;
}

.alert-open-icon {
  margin-left: auto;
}

.alert-meta {
  display: grid;
  gap: 2px;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.alert-actions {
  display: flex;
  justify-content: flex-end;
  gap: 4px;
}

.alert-icon-action {
  width: 28px;
  height: 28px;
  padding: 0;
}

@media (max-width: 720px) {
  .global-alert-entry {
    top: 88px;
    right: 16px;
  }
}
</style>
