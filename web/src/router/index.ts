import { createRouter, createWebHistory } from 'vue-router'
import { normalizePermissions, permissions } from '../rbac'

const LoginView = () => import('../views/LoginView.vue')
const DashboardView = () => import('../views/DashboardView.vue')
const AppsView = () => import('../views/AppsView.vue')
const ContainersView = () => import('../views/ContainersView.vue')
const ServersView = () => import('../views/ServersView.vue')
const DatabaseView = () => import('../views/DatabaseView.vue')
const NacosView = () => import('../views/NacosView.vue')
const StorageView = () => import('../views/StorageView.vue')
const CredentialsView = () => import('../views/CredentialsView.vue')
const TerminalView = () => import('../views/TerminalView.vue')
const TasksView = () => import('../views/TasksView.vue')
const ToolboxView = () => import('../views/ToolboxView.vue')
const AuditView = () => import('../views/AuditView.vue')
const SettingsView = () => import('../views/SettingsView.vue')

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
    { path: '/nacos', component: NacosView },
    { path: '/storage', component: StorageView },
    { path: '/credentials', component: CredentialsView, meta: { permission: permissions.credentialsUse } },
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
