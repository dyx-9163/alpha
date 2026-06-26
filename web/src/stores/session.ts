import { defineStore } from 'pinia'
import { apiPost } from '../api/client'

export const useSessionStore = defineStore('session', {
  state: () => ({
    username: localStorage.getItem('aifar-username') ?? '',
    role: localStorage.getItem('aifar-role') ?? ''
  }),
  getters: {
    isLoggedIn: () => Boolean(localStorage.getItem('aifar-session-token'))
  },
  actions: {
    async login(username: string, password: string) {
      const result = await apiPost<{ token: string; user: { username: string; role: string } }>('/auth/login', { username, password })
      localStorage.setItem('aifar-session-token', result.token)
      localStorage.setItem('aifar-username', result.user.username)
      localStorage.setItem('aifar-role', result.user.role)
      this.username = result.user.username
      this.role = result.user.role
    },
    logout() {
      localStorage.removeItem('aifar-session-token')
      localStorage.removeItem('aifar-username')
      localStorage.removeItem('aifar-role')
      this.username = ''
      this.role = ''
    }
  }
})
