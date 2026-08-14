// @vitest-environment happy-dom

import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import GlobalAlerts from './GlobalAlerts.vue'
import { useAlertsStore } from '../stores/alerts'
import { useSessionStore } from '../stores/session'
import { useTaskProgressStore } from '../stores/taskProgress'

const pushMock = vi.hoisted(() => vi.fn())
const apiGetMock = vi.hoisted(() => vi.fn())

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: pushMock })
}))

vi.mock('../api/client', () => ({
  apiGet: apiGetMock,
  apiPost: vi.fn(),
  asArray: <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : []
}))

vi.mock('../i18n', () => ({
  useI18n: () => ({ t: (key: string, params?: Record<string, unknown>) => params ? `${key}:${JSON.stringify(params)}` : key })
}))

describe('GlobalAlerts message center', () => {
  beforeEach(() => {
    localStorage.clear()
    localStorage.setItem('aifar-permissions', JSON.stringify(['alerts.view', 'tasks.manage']))
    setActivePinia(createPinia())
    apiGetMock.mockReset()
    pushMock.mockReset()
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/alerts?status=open') {
        return {
          items: [{
            id: 'alert-1',
            fingerprint: 'mysql-down',
            severity: 'critical',
            scope: 'database',
            app: 'mysql',
            status: 'open',
            title: 'MYSQL instance is unavailable',
            message: 'instance status is failed',
            lastSeenAt: '2026-08-13T07:27:00Z'
          }]
        }
      }
      if (path === '/tasks/task-running') {
        return {
          task: {
            id: 'task-running',
            type: 'apps.mysql.install',
            status: 'running',
            trackable: true,
            target: 'server-1'
          },
          steps: [{ status: 'success' }, { status: 'running' }, { status: 'pending' }]
        }
      }
      return { items: [] }
    })
    useSessionStore()
  })

  it('separates system alerts from task notifications in the bell drawer', async () => {
    const taskProgress = useTaskProgressStore()
    taskProgress.track('task-running', '安装 MySQL')
    await vi.waitFor(() => {
      expect(taskProgress.items[0]?.status).toBe('running')
    })
    const alerts = useAlertsStore()
    await alerts.load()

    const wrapper = mount(GlobalAlerts, {
      global: {
        stubs: {
          teleport: true,
          transition: false,
          'el-tooltip': { template: '<span><slot /></span>' },
          'el-badge': { props: ['value'], template: '<span class="badge" :data-value="value"><slot /></span>' },
          'el-button': { props: ['loading'], template: '<button :data-loading="String(Boolean(loading))" @click="$emit(\'click\')"><slot /></button>' },
          'el-drawer': { props: ['modelValue'], template: '<aside v-if="modelValue" class="drawer"><slot /></aside>' },
          'el-icon': { template: '<i><slot /></i>' },
          'el-empty': { props: ['description'], template: '<div class="empty">{{ description }}</div>' },
          'el-tag': { template: '<span><slot /></span>' },
          'el-progress': { props: ['percentage'], template: '<div class="progress">{{ percentage }}</div>' }
        }
      }
    })

    await wrapper.find('.alert-bell-button').trigger('click')
    await vi.waitFor(() => {
      expect(wrapper.find('.message-center-tabs').exists()).toBe(true)
    })

    expect(wrapper.text()).toContain('messageCenter.systemAlerts')
    expect(wrapper.text()).toContain('messageCenter.taskAlerts')
    expect(wrapper.text()).toContain('安装 MySQL')
    expect(wrapper.text()).toContain('server-1')
    expect(wrapper.text()).not.toContain('MYSQL instance is unavailable')

    await wrapper.find('.task-message-card').trigger('click')

    expect(pushMock).toHaveBeenCalledWith({ path: '/tasks', query: { taskId: 'task-running' } })

    await wrapper.find('.alert-bell-button').trigger('click')
    await wrapper.find('[data-message-center-tab="alerts"]').trigger('click')

    expect(wrapper.text()).toContain('MYSQL instance is unavailable')
    expect(wrapper.text()).not.toContain('安装 MySQL')
  })
})
