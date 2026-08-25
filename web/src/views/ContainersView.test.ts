// @vitest-environment happy-dom

import { createPinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiGetMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn()
}))

vi.mock('../api/client', async () => {
  const actual = await vi.importActual<typeof import('../api/client')>('../api/client')
  return {
    ...actual,
    apiGet: apiGetMock
  }
})

vi.mock('../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('../composables/usePermissions', () => ({
  usePermissions: () => ({
    can: () => true,
    deniedText: { value: 'denied' }
  })
}))

vi.mock('element-plus', () => ({
  ElMessage: {
    error: vi.fn(),
    success: vi.fn(),
    warning: vi.fn()
  },
  ElMessageBox: {
    confirm: vi.fn(),
    prompt: vi.fn()
  }
}))

import ContainersView from './ContainersView.vue'

const server = {
  id: 'srv-1',
  name: 'node-1',
  host: '192.168.74.143',
  dockerHost: 'ssh://root@192.168.74.143',
  deployDir: '/aifar/apps'
}

function mountContainersView() {
  return shallowMount(ContainersView, {
    global: {
      plugins: [createPinia()],
      stubs: {
        ServerSelector: true,
        MetricGrid: true,
        KeyValueGrid: true,
        AifarRuntimeWorkspace: true,
        AifarRuntimeDialogs: true,
        'el-alert': true,
        'el-button': true,
        'el-dropdown': true,
        'el-dropdown-menu': true,
        'el-dropdown-item': true,
        'el-option': true,
        'el-select': true,
        'el-table': true,
        'el-table-column': true,
        'el-tab-pane': true,
        'el-tabs': true,
        'el-tooltip': true
      }
    }
  })
}

function requestedPaths() {
  return apiGetMock.mock.calls.map((call) => String(call[0]))
}

describe('ContainersView AIFAR runtime loading', () => {
  beforeEach(() => {
    apiGetMock.mockReset()
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/apps/aifar/install-modules?version=runtime-v2') return []
      if (path === '/servers') return [server]
      if (path === '/apps/instances') {
        return [{
          id: 'app-aifar',
          app: 'aifar',
          serverId: 'srv-1',
          status: 'installed',
          version: 'runtime-v2',
          metadata: JSON.stringify({ installRoot: '/aifar/apps/admin', orchestrationModel: 'agent-service-controller-v1' })
        }]
      }
      if (path === '/settings') return {}
      if (path === '/status/snapshots') return { items: [] }
      if (path === '/containers/aifar/runtime?serverId=srv-1&includePods=0&includeStats=0') {
        return {
          runtimeStatus: 'running',
          agent: { status: 'running' },
          instances: [{ id: 'app-aifar', status: 'running', version: 'runtime-v2' }],
          deployments: [{ instanceId: 'app-aifar', serviceName: 'permission', deploymentName: 'permission' }],
          services: [],
          pods: [],
          ingress: [],
          warnings: []
        }
      }
      return null
    })
  })

  it('loads base runtime data when entering the AIFAR runtime tab', async () => {
    const wrapper = mountContainersView()
    await flushPromises()

    expect(requestedPaths()).not.toContain('/containers/aifar/runtime?serverId=srv-1&includePods=0&includeStats=0')

    ;(wrapper.vm as unknown as { tab: string }).tab = 'aifar-runtime'
    await flushPromises()

    expect(requestedPaths()).toContain('/containers/aifar/runtime?serverId=srv-1&includePods=0&includeStats=0')
  })

  it('hides all deployment runtime data when aifar-agent is unavailable', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/apps/aifar/install-modules?version=runtime-v2') return []
      if (path === '/servers') return [server]
      if (path === '/apps/instances') {
        return [{
          id: 'app-aifar',
          app: 'aifar',
          serverId: 'srv-1',
          status: 'installed',
          version: 'runtime-v2',
          metadata: JSON.stringify({ installRoot: '/aifar/apps/admin', orchestrationModel: 'agent-service-controller-v1' })
        }]
      }
      if (path === '/settings') return {}
      if (path === '/status/snapshots') return { items: [] }
      if (path === '/containers/aifar/runtime?serverId=srv-1&includePods=0&includeStats=0') {
        return {
          runtimeStatus: 'degraded',
          agent: { status: 'missing', error: 'agent disconnected' },
          instances: [{ id: 'app-aifar', status: 'running', version: 'runtime-v2' }],
          deployments: [{ instanceId: 'app-aifar', serviceName: 'permission', deploymentName: 'permission' }],
          services: [{ instanceId: 'app-aifar', serviceName: 'permission', status: 'running' }],
          pods: [{ instanceId: 'app-aifar', serviceName: 'permission', containerName: 'permission-1', status: 'running' }],
          ingress: [{ instanceId: 'app-aifar', status: 'running', webRoute: 'http://example.local' }],
          warnings: ['agent disconnected']
        }
      }
      return null
    })

    const wrapper = mountContainersView()
    await flushPromises()

    ;(wrapper.vm as unknown as { tab: string }).tab = 'aifar-runtime'
    await flushPromises()

    const vm = wrapper.vm as unknown as {
      aifarRuntimeDataAvailable: boolean
      aifarRuntimeInstances: unknown[]
      selectedRuntimeInstanceId: string
      selectedRuntimeDeployments: unknown[]
      selectedRuntimeServices: unknown[]
      selectedRuntimePods: unknown[]
      runtimeEntryRoutes: Array<{ route: string; port: string }>
    }
    expect(vm.aifarRuntimeDataAvailable).toBe(false)
    expect(vm.aifarRuntimeInstances).toEqual([])
    expect(vm.selectedRuntimeInstanceId).toBe('')
    expect(vm.selectedRuntimeDeployments).toEqual([])
    expect(vm.selectedRuntimeServices).toEqual([])
    expect(vm.selectedRuntimePods).toEqual([])
    expect(vm.runtimeEntryRoutes.every((route) => route.route === '-' || route.port.endsWith('-'))).toBe(true)
  })
})
