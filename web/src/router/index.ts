import { createRouter, createWebHistory } from 'vue-router'
import LoginView from '../views/LoginView.vue'
import DashboardView from '../views/DashboardView.vue'
import AppsView from '../views/AppsView.vue'
import ContainersView from '../views/ContainersView.vue'
import ServersView from '../views/ServersView.vue'
import DatabaseView from '../views/DatabaseView.vue'
import StorageView from '../views/StorageView.vue'
import TerminalView from '../views/TerminalView.vue'
import TasksView from '../views/TasksView.vue'
import ToolboxView from '../views/ToolboxView.vue'
import AuditView from '../views/AuditView.vue'
import SettingsView from '../views/SettingsView.vue'
import { normalizePermissions, permissions } from '../rbac'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', component: LoginView, meta: { public: true } },
    { path: '/', redirect: '/dashboard' },
    { path: '/dashboard', component: DashboardView },
    { path: '/apps', component: AppsView },
    { path: '/containers', component: ContainersView },
    { path: '/servers', component: ServersView },
    { path: '/database', component: DatabaseView },
    { path: '/storage', component: StorageView },
    { path: '/terminal', component: TerminalView, meta: { permission: permissions.terminalConnect } },
    { path: '/tasks', component: TasksView },
    { path: '/toolbox', component: ToolboxView },
    { path: '/audit', component: AuditView },
    { path: '/settings', component: SettingsView }
  ]
})

router.beforeEach((to) => {
  if (!to.meta.public && !localStorage.getItem('aifar-session-token')) return '/login'
  const permission = typeof to.meta.permission === 'string' ? to.meta.permission : ''
  if (permission && !(storedPermissions() as string[]).includes(permission)) return '/dashboard'
  return true
})

function storedPermissions() {
  const role = localStorage.getItem('aifar-role') ?? ''
  try {
    const raw = JSON.parse(localStorage.getItem('aifar-permissions') ?? '[]')
    return normalizePermissions(role, Array.isArray(raw) ? raw : [])
  } catch {
    return normalizePermissions(role)
  }
}

export default router
