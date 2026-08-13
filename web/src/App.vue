<template>
  <el-config-provider :locale="elementLocale">
    <router-view v-if="$route.meta.public" />
    <el-container v-else class="panel-layout">
      <el-aside width="var(--aifar-sidebar-width)" class="sidebar">
        <div class="brand">
          <div class="brand-mark">A</div>
          <div>
            <strong>AIFAR Panel</strong>
            <small>{{ t('brand.subtitle') }}</small>
          </div>
        </div>
        <el-menu router :default-active="$route.path" class="side-menu">
          <el-menu-item v-for="item in navItems" :key="item.path" :index="item.path">
            <el-icon class="nav-icon"><component :is="item.icon" /></el-icon>
            <span>{{ t(item.labelKey) }}</span>
          </el-menu-item>
        </el-menu>
        <div class="sidebar-footer">
          <el-tag type="success" effect="light">{{ t('common.providerReal') }}</el-tag>
          <strong>{{ session.username || 'admin' }}</strong>
          <el-button class="logout-button" @click="logout">{{ t('auth.logout') }}</el-button>
        </div>
      </el-aside>
      <el-main class="content">
        <GlobalTaskProgress />
        <GlobalAlerts />
        <GlobalRealtimeStatus />
        <div class="content-body">
          <router-view v-slot="{ Component }">
            <keep-alive include="TerminalView">
              <component :is="Component" />
            </keep-alive>
          </router-view>
        </div>
      </el-main>
    </el-container>
  </el-config-provider>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, watch, type Component } from 'vue'
import { Box, Coin, Connection, FolderOpened, Key, List, Monitor, Odometer, Operation, Setting, Shop, Tickets } from '@element-plus/icons-vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import { useRouter } from 'vue-router'
import GlobalTaskProgress from './components/GlobalTaskProgress.vue'
import GlobalAlerts from './components/GlobalAlerts.vue'
import GlobalRealtimeStatus from './components/GlobalRealtimeStatus.vue'
import { SESSION_CLEARED_EVENT } from './api/client'
import { useAlertsStore } from './stores/alerts'
import { useSessionStore } from './stores/session'
import { useRealtimeStore } from './stores/realtime'
import { useTaskProgressStore } from './stores/taskProgress'
import { useI18n } from './i18n'
import { permissions, type Permission } from './rbac'

const router = useRouter()
const session = useSessionStore()
const realtime = useRealtimeStore()
const alerts = useAlertsStore()
const taskProgress = useTaskProgressStore()
const { locale, t } = useI18n()
const elementLocale = computed(() => locale.value === 'en' ? en : zhCn)
const navItems = computed(() => allNavItems.filter((item) => !item.permission || session.hasPermission(item.permission)))
const allNavItems: Array<{ path: string; labelKey: string; icon: Component; permission?: Permission }> = [
  { path: '/dashboard', labelKey: 'nav.dashboard', icon: Odometer },
  { path: '/apps', labelKey: 'nav.apps', icon: Shop },
  { path: '/servers', labelKey: 'nav.servers', icon: Monitor },
  { path: '/containers', labelKey: 'nav.containers', icon: Box },
  { path: '/database', labelKey: 'nav.database', icon: Coin },
  { path: '/nacos', labelKey: 'nav.nacos', icon: Connection },
  { path: '/storage', labelKey: 'nav.storage', icon: FolderOpened },
  { path: '/credentials', labelKey: 'nav.credentials', icon: Key, permission: permissions.credentialsUse },
  { path: '/terminal', labelKey: 'nav.terminal', icon: Operation, permission: permissions.terminalConnect },
  { path: '/tasks', labelKey: 'nav.tasks', icon: List },
  { path: '/audit', labelKey: 'nav.audit', icon: Tickets },
  { path: '/settings', labelKey: 'nav.settings', icon: Setting }
]

function logout() {
  clearPrivateSession()
  router.push('/login')
}

function clearPrivateSession() {
  realtime.disconnect()
  alerts.clear()
  taskProgress.clearAll()
  session.logout()
}

function handleSessionCleared() {
  clearPrivateSession()
}

onMounted(() => {
  window.addEventListener(SESSION_CLEARED_EVENT, handleSessionCleared)
  if (session.isLoggedIn) {
    realtime.connect()
  }
})

watch(() => session.isLoggedIn, (loggedIn) => {
  if (loggedIn) {
    realtime.connect()
  } else {
    realtime.disconnect()
    alerts.clear()
    taskProgress.clearAll()
  }
})

onBeforeUnmount(() => {
  window.removeEventListener(SESSION_CLEARED_EVENT, handleSessionCleared)
  realtime.disconnect()
})
</script>
