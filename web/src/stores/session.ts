import { defineStore } from 'pinia'
import { apiPost } from '../api/client'
import { normalizePermissions, type Permission } from '../rbac'

type LoginResponse = {
  token: string
  user: {
    username: string
    role: string
    tokenVersion?: number
    permissions?: string[]
  }
}

function storedPermissions(role: string) {
  try {
    const raw = JSON.parse(localStorage.getItem('aifar-permissions') ?? '[]')
    return normalizePermissions(role, Array.isArray(raw) ? raw : [])
  } catch {
    return normalizePermissions(role)
  }
}

export const useSessionStore = defineStore('session', {
  state: () => ({
    token: localStorage.getItem('aifar-session-token') ?? '',
    username: localStorage.getItem('aifar-username') ?? '',
    role: localStorage.getItem('aifar-role') ?? '',
    tokenVersion: Number(localStorage.getItem('aifar-token-version') ?? '0'),
    permissions: storedPermissions(localStorage.getItem('aifar-role') ?? '') as Permission[]
  }),
  getters: {
    isLoggedIn: (state) => Boolean(state.token)
  },
  actions: {
    async login(username: string, password: string) {
      const result = await apiPost<LoginResponse>('/auth/login', { username, password })
      const permissions = normalizePermissions(result.user.role, result.user.permissions)
      localStorage.setItem('aifar-session-token', result.token)
      localStorage.setItem('aifar-username', result.user.username)
      localStorage.setItem('aifar-role', result.user.role)
      localStorage.setItem('aifar-token-version', String(result.user.tokenVersion ?? 0))
      localStorage.setItem('aifar-permissions', JSON.stringify(permissions))
      this.token = result.token
      this.username = result.user.username
      this.role = result.user.role
      this.tokenVersion = result.user.tokenVersion ?? 0
      this.permissions = permissions
    },
    hasPermission(permission: Permission) {
      return this.permissions.includes(permission)
    },
    logout() {
      localStorage.removeItem('aifar-session-token')
      localStorage.removeItem('aifar-username')
      localStorage.removeItem('aifar-role')
      localStorage.removeItem('aifar-token-version')
      localStorage.removeItem('aifar-permissions')
      this.token = ''
      this.username = ''
      this.role = ''
      this.tokenVersion = 0
      this.permissions = []
    }
  }
})
