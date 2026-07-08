<template>
  <div v-if="canViewAlerts" class="global-alert-entry">
    <el-tooltip :content="t('alerts.title')" placement="bottom">
      <el-badge :value="badgeValue" :hidden="alerts.openCount === 0" :type="badgeType" :max="99">
        <el-button class="alert-bell-button" circle @click="openDrawer">
          <el-icon><Bell /></el-icon>
        </el-button>
      </el-badge>
    </el-tooltip>
  </div>

  <el-drawer v-model="alerts.drawerVisible" size="440px" :with-header="false" append-to-body class="alerts-drawer">
    <div class="alert-drawer-shell">
      <header class="alert-drawer-head">
        <div>
          <h2>{{ t('alerts.drawerTitle') }}</h2>
          <span>{{ t('alerts.openCount', { count: alerts.openCount }) }}</span>
        </div>
        <el-tooltip :content="t('common.refresh')" placement="bottom">
          <el-button class="alert-icon-action" text :loading="alerts.loading" @click="reload">
            <el-icon><RefreshRight /></el-icon>
          </el-button>
        </el-tooltip>
      </header>

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
    </div>
  </el-drawer>
</template>

<script setup lang="ts">
import { computed, onMounted, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { Bell, Check, CircleCloseFilled, Clock, RefreshRight, Right } from '@element-plus/icons-vue'
import { useRouter } from 'vue-router'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { useAlertsStore, type AlertItem } from '../stores/alerts'
import { useSessionStore } from '../stores/session'

const router = useRouter()
const alerts = useAlertsStore()
const session = useSessionStore()
const { t } = useI18n()

const canViewAlerts = computed(() => session.hasPermission(permissions.alertsView))
const canManageAlerts = computed(() => session.hasPermission(permissions.alertsManage))
const badgeValue = computed(() => alerts.criticalCount > 0 ? alerts.criticalCount : alerts.openCount)
const badgeType = computed(() => alerts.criticalCount > 0 ? 'danger' : 'warning')

function openDrawer() {
  alerts.openDrawer()
  if (!alerts.loaded && !alerts.loading) {
    void reload()
  }
}

async function reload() {
  try {
    await alerts.load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : String(err))
  }
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
