// @vitest-environment happy-dom

import { shallowMount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { nextTick } from 'vue'
import { createMemoryHistory, createRouter } from 'vue-router'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import App from './App.vue'
import source from './App.vue?raw'
import { useSessionStore } from './stores/session'
import { useTaskProgressStore } from './stores/taskProgress'

const apiPostMock = vi.hoisted(() => vi.fn())
const apiGetMock = vi.hoisted(() => vi.fn())

vi.mock('./api/client', () => ({
  SESSION_CLEARED_EVENT: 'aifar-session-cleared',
  apiGet: apiGetMock,
  apiPost: apiPostMock,
  asArray: <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : [],
  eventStreamUrl: () => `/api/v2/events?token=${localStorage.getItem('aifar-session-token') ?? ''}&lang=zh`
}))

class FakeEventSource {
  static urls: string[] = []
  listeners: Record<string, Array<(event?: unknown) => void>> = {}

  constructor(readonly url: string) {
    FakeEventSource.urls.push(url)
  }

  addEventListener(type: string, listener: (event?: unknown) => void) {
    this.listeners[type] = [...(this.listeners[type] ?? []), listener]
  }

  close() {
    // test double
  }
}

describe('private route keep-alive contract', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    localStorage.clear()
    FakeEventSource.urls = []
    apiPostMock.mockReset()
    apiGetMock.mockReset()
    setActivePinia(createPinia())
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  it('caches only the named terminal view inside the authenticated layout', () => {
    expect(source).toContain('<router-view v-slot="{ Component }">')
    expect(source).toContain('<keep-alive include="TerminalView">')
    expect(source).toContain('<component :is="Component" />')
    expect(source).toContain('<router-view v-if="$route.meta.public" />')
  })

  it('opens the realtime event stream immediately after login', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { template: '<div />' }, meta: { public: true } },
        { path: '/dashboard', component: { template: '<div />' } }
      ]
    })
    await router.push('/login')
    await router.isReady()
    const pinia = createPinia()
    setActivePinia(pinia)
    shallowMount(App, {
      global: {
        plugins: [router, pinia],
        stubs: {
          'el-config-provider': { template: '<div><slot /></div>' },
          'el-container': { template: '<div><slot /></div>' },
          'el-aside': { template: '<aside><slot /></aside>' },
          'el-main': { template: '<main><slot /></main>' },
          'el-menu': { template: '<nav><slot /></nav>' },
          'el-menu-item': { template: '<a><slot /></a>' },
          'el-icon': { template: '<span><slot /></span>' },
          'el-tag': { template: '<span><slot /></span>' },
          'el-button': { template: '<button><slot /></button>' },
          GlobalAlerts: true,
          GlobalRealtimeStatus: true,
          GlobalTaskProgress: true,
          RouterView: { template: '<div />' }
        }
      }
    })
    expect(FakeEventSource.urls).toEqual([])
    apiPostMock.mockResolvedValueOnce({
      token: 'session-token',
      user: {
        username: 'admin',
        role: 'admin',
        tokenVersion: 2,
        permissions: []
      }
    })

    await useSessionStore().login('admin', 'password')
    await nextTick()

    expect(FakeEventSource.urls).toEqual(['/api/v2/events?token=session-token&lang=zh'])
  })

  it('stops tracked task polling and clears persisted task progress on logout', async () => {
    localStorage.setItem('aifar-session-token', 'session-token')
    localStorage.setItem('aifar-username', 'admin')
    apiGetMock.mockImplementation(async (path: string) => {
      if (path.startsWith('/tasks/')) {
        return {
          task: { id: decodeURIComponent(path.split('/').pop() ?? ''), status: 'running', trackable: true },
          steps: []
        }
      }
      return { items: [] }
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { template: '<div />' }, meta: { public: true } },
        { path: '/dashboard', component: { template: '<div />' } }
      ]
    })
    await router.push('/dashboard')
    await router.isReady()
    const pinia = createPinia()
    setActivePinia(pinia)
    const wrapper = shallowMount(App, {
      global: {
        plugins: [router, pinia],
        stubs: {
          'el-config-provider': { template: '<div><slot /></div>' },
          'el-container': { template: '<div><slot /></div>' },
          'el-aside': { template: '<aside><slot /></aside>' },
          'el-main': { template: '<main><slot /></main>' },
          'el-menu': { template: '<nav><slot /></nav>' },
          'el-menu-item': { template: '<a><slot /></a>' },
          'el-icon': { template: '<span><slot /></span>' },
          'el-tag': { template: '<span><slot /></span>' },
          'el-button': { template: `<button class="logout-button" @click="$emit('click')"><slot /></button>` },
          GlobalAlerts: true,
          GlobalRealtimeStatus: true,
          GlobalTaskProgress: true,
          RouterView: { template: '<div />' }
        }
      }
    })
    const taskProgress = useTaskProgressStore()

    taskProgress.track('task-running', 'running task')
    await vi.runAllTicks()
    expect(apiGetMock).toHaveBeenCalledWith('/tasks/task-running')
    apiGetMock.mockClear()

    await wrapper.find('.logout-button').trigger('click')
    await nextTick()
    await vi.advanceTimersByTimeAsync(5000)

    expect(apiGetMock).not.toHaveBeenCalledWith('/tasks/task-running')
    expect(localStorage.getItem('aifar-tracked-tasks')).toBeNull()
    expect(taskProgress.items).toEqual([])
  })

  it('clears in-memory session state and tracked task polling after the API client reports a 401', async () => {
    localStorage.setItem('aifar-session-token', 'expired-token')
    localStorage.setItem('aifar-username', 'admin')
    apiGetMock.mockImplementation(async (path: string) => {
      if (path.startsWith('/tasks/')) {
        return {
          task: { id: decodeURIComponent(path.split('/').pop() ?? ''), status: 'running', trackable: true },
          steps: []
        }
      }
      return { items: [] }
    })
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [
        { path: '/login', component: { template: '<div />' }, meta: { public: true } },
        { path: '/dashboard', component: { template: '<div />' } }
      ]
    })
    await router.push('/dashboard')
    await router.isReady()
    const pinia = createPinia()
    setActivePinia(pinia)
    shallowMount(App, {
      global: {
        plugins: [router, pinia],
        stubs: {
          'el-config-provider': { template: '<div><slot /></div>' },
          'el-container': { template: '<div><slot /></div>' },
          'el-aside': { template: '<aside><slot /></aside>' },
          'el-main': { template: '<main><slot /></main>' },
          'el-menu': { template: '<nav><slot /></nav>' },
          'el-menu-item': { template: '<a><slot /></a>' },
          'el-icon': { template: '<span><slot /></span>' },
          'el-tag': { template: '<span><slot /></span>' },
          'el-button': { template: '<button><slot /></button>' },
          GlobalAlerts: true,
          GlobalRealtimeStatus: true,
          GlobalTaskProgress: true,
          RouterView: { template: '<div />' }
        }
      }
    })
    const session = useSessionStore()
    const taskProgress = useTaskProgressStore()
    taskProgress.track('task-running', 'running task')
    await vi.runAllTicks()
    apiGetMock.mockClear()

    window.dispatchEvent(new CustomEvent('aifar-session-cleared'))
    await nextTick()
    await vi.advanceTimersByTimeAsync(5000)

    expect(session.isLoggedIn).toBe(false)
    expect(taskProgress.items).toEqual([])
    expect(apiGetMock).not.toHaveBeenCalledWith('/tasks/task-running')
  })
})
