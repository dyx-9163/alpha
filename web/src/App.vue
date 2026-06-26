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
        <div class="content-body">
          <router-view />
        </div>
      </el-main>
    </el-container>
  </el-config-provider>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { Box, Coin, FolderOpened, List, Monitor, Odometer, Operation, Setting, Shop, Tickets } from '@element-plus/icons-vue'
import zhCn from 'element-plus/es/locale/lang/zh-cn'
import en from 'element-plus/es/locale/lang/en'
import { useRouter } from 'vue-router'
import { useSessionStore } from './stores/session'
import { useI18n } from './i18n'

const router = useRouter()
const session = useSessionStore()
const { locale, t } = useI18n()
const elementLocale = computed(() => locale.value === 'en' ? en : zhCn)
const navItems = [
  { path: '/dashboard', labelKey: 'nav.dashboard', icon: Odometer },
  { path: '/apps', labelKey: 'nav.apps', icon: Shop },
  { path: '/servers', labelKey: 'nav.servers', icon: Monitor },
  { path: '/containers', labelKey: 'nav.containers', icon: Box },
  { path: '/database', labelKey: 'nav.database', icon: Coin },
  { path: '/storage', labelKey: 'nav.storage', icon: FolderOpened },
  { path: '/terminal', labelKey: 'nav.terminal', icon: Operation },
  { path: '/tasks', labelKey: 'nav.tasks', icon: List },
  { path: '/audit', labelKey: 'nav.audit', icon: Tickets },
  { path: '/settings', labelKey: 'nav.settings', icon: Setting }
]

function logout() {
  session.logout()
  router.push('/login')
}
</script>
