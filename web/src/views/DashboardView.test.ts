// @vitest-environment happy-dom

import { createPinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiGetMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiGet: apiGetMock,
  asArray: <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : []
}))

vi.mock('../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import { normalizeDashboardRuntimeStatus, normalizeDashboardServerStatus } from '../dashboard/serverStatus'
import { useRealtimeStore } from '../stores/realtime'
import DashboardView from './DashboardView.vue'

const server = {
  id: 'srv-1',
  name: 'one',
  host: '10.0.0.1',
  dockerHost: 'unix:///var/run/docker.sock',
  deployDir: '/aifar/apps',
  status: 'unknown'
}

function mountDashboard() {
  return shallowMount(DashboardView, {
    global: {
      plugins: [createPinia()],
      stubs: {
        'el-button': {
          props: ['loading'],
          emits: ['click'],
          template: '<button :data-loading="String(Boolean(loading))" @click="$emit(\'click\')"><slot /></button>'
        },
        MetricGrid: {
          props: ['items'],
          template: '<div class="metric-grid-stub">{{ JSON.stringify(items) }}</div>'
        },
        'el-table': true,
        'el-table-column': true,
        'el-empty': true,
        'el-tag': true
      }
    }
  })
}

function requestedPaths() {
  return apiGetMock.mock.calls.map((call) => String(call[0]))
}

describe('DashboardView refresh behavior', () => {
  beforeEach(() => {
    localStorage.setItem('aifar-permissions', JSON.stringify(['alerts.view']))
    apiGetMock.mockReset()
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') return { items: [] }
      if (path === '/servers/srv-1/telemetry') return { sampledAt: '2026-08-12T07:31:00Z', cpu: 12, cpuText: '12%' }
      return null
    })
  })

  it('does not probe server telemetry when the dashboard is opened', async () => {
    mountDashboard()

    await flushPromises()

    expect(requestedPaths()).toEqual(expect.arrayContaining([
      '/servers',
      '/tasks',
      '/database/instances',
      '/storage/instances'
    ]))
    expect(requestedPaths()).not.toContain('/servers/srv-1/telemetry')
  })

  it('does not render a manual refresh button for persisted dashboard snapshots', async () => {
    const wrapper = mountDashboard()
    await flushPromises()

    expect(wrapper.text()).not.toContain('common.refresh')
    expect(requestedPaths()).not.toContain('/servers/srv-1/telemetry')
  })

  it.each(['available', 'running', 'success', 'ok'])('keeps server inventory status %s aligned with the server workbench', (status) => {
    expect(normalizeDashboardServerStatus(status)).toBe('available')
  })

  it.each(['failed', 'error', 'unavailable', 'unhealthy', 'no-endpoints', 'down', 'offline', 'missing', 'stopped'])('renders runtime health status %s as service unavailable on the dashboard', (status) => {
    expect(normalizeDashboardRuntimeStatus(status)).toBe('unavailable')
  })

  it.each(['running', 'available', 'success', 'ok'])('renders runtime health status %s as running on the dashboard', (status) => {
    expect(normalizeDashboardRuntimeStatus(status)).toBe('running')
  })

  it('hydrates persisted status snapshots on entry so server KPI matches the server workbench', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') {
        return {
          items: [{
            scope: 'server',
            resourceId: 'srv-1',
            status: 'available',
            version: 7,
            collectedAt: '2026-08-12T14:11:00Z',
            updatedAt: '2026-08-12T14:11:01Z'
          }]
        }
      }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    expect(requestedPaths()).toContain('/status/snapshots')
    expect(useRealtimeStore().serverSnapshot('srv-1')?.status).toBe('available')
    await vi.waitFor(() => {
      expect(wrapper.find('.metric-grid-stub').text()).toContain('common.available 1')
    })
  })

  it('uses centralized runtime health when counting Docker hosts', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/servers') return [server]
      if (path === '/tasks') return []
      if (path === '/database/instances') return []
      if (path === '/storage/instances') return []
      if (path === '/alerts?status=open') return { items: [] }
      if (path === '/status/snapshots') {
        return {
          items: [{
            scope: 'docker.summary',
            resourceId: 'srv-1',
            status: 'error',
            payload: {
              available: true,
              summary: {
                containers: 2,
                images: 4,
                endpoint: 'unix:///var/run/docker.sock'
              }
            },
            version: 3,
            collectedAt: '2026-08-12T14:12:00Z',
            updatedAt: '2026-08-12T14:12:01Z'
          }]
        }
      }
      return null
    })

    const wrapper = mountDashboard()
    await flushPromises()

    await vi.waitFor(() => {
      expect(wrapper.find('.metric-grid-stub').text()).toContain('"label":"Docker","value":1,"note":"common.running 0"')
    })
  })
})
