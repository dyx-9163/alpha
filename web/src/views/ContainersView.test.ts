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
import containersViewSource from './ContainersView.vue?raw'

const server = {
  id: 'srv-1',
  name: 'node-1',
  host: '192.168.74.143',
  dockerHost: 'ssh://root@192.168.74.143',
  deployDir: '/aifar/apps'
}

const secondServer = {
  id: 'srv-2',
  name: 'node-2',
  host: '192.168.74.142',
  dockerHost: 'ssh://root@192.168.74.142',
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
        'el-tag': true,
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
      if (path === '/containers/summary?serverId=srv-1') return { available: true, summary: { images: 1 } }
      if (path === '/containers/summary?serverId=srv-2') return { available: true, summary: { images: 1 } }
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
      if (path === '/containers?kind=images&serverId=srv-1') {
        return [{ id: 'sha256:image-1', repository: 'aifar-gateway', tag: 'latest' }]
      }
      if (path === '/containers?kind=images&serverId=srv-2') {
        return [{ id: 'sha256:image-2', repository: 'aifar-oauth', tag: 'latest' }]
      }
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

  it('loads Docker images when entering the images tab', async () => {
    const wrapper = mountContainersView()
    await flushPromises()

    expect(requestedPaths()).not.toContain('/containers?kind=images&serverId=srv-1')

    ;(wrapper.vm as unknown as { tab: string }).tab = 'images'
    await flushPromises()

    expect(requestedPaths()).toContain('/containers?kind=images&serverId=srv-1')
    expect((wrapper.vm as unknown as { collection: unknown[] }).collection).toEqual([
      expect.objectContaining({ id: 'sha256:image-1', repository: 'aifar-gateway' })
    ])
  })

  it('classifies Docker image delete availability from container usage evidence', async () => {
    const wrapper = mountContainersView()
    await flushPromises()

    const vm = wrapper.vm as unknown as {
      imageDeleteDisabledReason: (row: unknown) => string
      imageDeleteAdvice: (row: unknown) => { type: string; label: string; hint: string }
      imageSelectable: (row: unknown) => boolean
    }
    const usedImage = {
      id: 'sha256:used',
      repository: 'aifar-gateway',
      tag: 'latest',
      usedByContainers: ['gateway-1', 'gateway-2']
    }
    const unusedImage = {
      id: 'sha256:unused',
      repository: 'aifar-oauth',
      tag: 'latest',
      usedByContainers: []
    }
    const unknownUsageImage = {
      id: 'sha256:unknown',
      repository: 'aifar-system',
      tag: 'latest'
    }
    const parentImage = {
      id: 'sha256:parent',
      repository: 'nginx',
      tag: 'stable-alpine',
      usedByContainers: [],
      usedByImages: ['aifar-web-vue3:latest']
    }

    expect(vm.imageDeleteDisabledReason(usedImage)).toBe('containers.imageDeleteBlockedInUse')
    expect(vm.imageDeleteAdvice(usedImage)).toEqual(expect.objectContaining({ type: 'danger', label: 'containers.imageDeleteBlocked' }))
    expect(vm.imageSelectable(usedImage)).toBe(false)
    expect(vm.imageDeleteDisabledReason(parentImage)).toBe('containers.imageDeleteBlockedByImages')
    expect(vm.imageSelectable(parentImage)).toBe(false)
    expect(vm.imageDeleteDisabledReason(unusedImage)).toBe('')
    expect(vm.imageDeleteAdvice(unusedImage)).toEqual(expect.objectContaining({ type: 'success', label: 'containers.imageDeleteAllowed' }))
    expect(vm.imageSelectable(unusedImage)).toBe(true)
    expect(vm.imageDeleteDisabledReason(unknownUsageImage)).toBe('')
    expect(vm.imageDeleteAdvice(unknownUsageImage)).toEqual(expect.objectContaining({ type: 'warning', label: 'containers.imageDeleteUnknown' }))
  })

  it('keeps Docker resource tables bordered and column-resizable', () => {
    const resourceTables = [
      /<el-table\s+border[\s\S]*?@selection-change="onImageSelectionChange"[\s\S]*?<\/el-table>/,
      /<el-table\s+border[\s\S]*?<el-table-column prop="scope" :label="t\('containers.scope'\)" min-width="120" :resizable="true" \/>[\s\S]*?<\/el-table>/,
      /<el-table\s+border[\s\S]*?<el-table-column prop="size" :label="t\('containers.size'\)" width="120" :resizable="true" \/>[\s\S]*?<\/el-table>/
    ]

    for (const pattern of resourceTables) {
      const match = containersViewSource.match(pattern)
      expect(match?.[0]).toContain(':resizable="true"')
    }
  })

  it('reloads Docker image rows for the newly selected server while staying on the images tab', async () => {
    apiGetMock.mockImplementation(async (path: string) => {
      if (path === '/apps/aifar/install-modules?version=runtime-v2') return []
      if (path === '/servers') return [server, secondServer]
      if (path === '/containers/summary?serverId=srv-1') return { available: true, summary: { images: 1 } }
      if (path === '/containers/summary?serverId=srv-2') return { available: true, summary: { images: 1 } }
      if (path === '/apps/instances') return []
      if (path === '/settings') return {}
      if (path === '/status/snapshots') return { items: [] }
      if (path === '/containers?kind=images&serverId=srv-1') {
        return [{ id: 'sha256:image-1', repository: 'aifar-gateway', tag: 'latest' }]
      }
      if (path === '/containers?kind=images&serverId=srv-2') {
        return [{ id: 'sha256:image-2', repository: 'aifar-oauth', tag: 'latest' }]
      }
      return null
    })

    const wrapper = mountContainersView()
    await flushPromises()

    const vm = wrapper.vm as unknown as { tab: string; selectedServerId: string; collection: unknown[] }
    vm.tab = 'images'
    await flushPromises()
    vm.selectedServerId = 'srv-2'
    await flushPromises()

    expect(requestedPaths()).toContain('/containers?kind=images&serverId=srv-2')
    expect(vm.collection).toEqual([
      expect.objectContaining({ id: 'sha256:image-2', repository: 'aifar-oauth' })
    ])
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
