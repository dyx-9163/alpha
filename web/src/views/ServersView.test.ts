// @vitest-environment happy-dom

import { createPinia, setActivePinia } from 'pinia'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { computed } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const {
  getServerDefaultsMock,
  listServersMock,
  probeServerMock
} = vi.hoisted(() => ({
  getServerDefaultsMock: vi.fn(),
  listServersMock: vi.fn(),
  probeServerMock: vi.fn()
}))

vi.mock('../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('../composables/usePermissions', () => ({
  usePermissions: () => ({
    can: () => true,
    deniedText: computed(() => 'permission denied')
  })
}))

vi.mock('../servers/api', () => ({
  deleteServer: vi.fn(),
  getServerDefaults: getServerDefaultsMock,
  listServers: listServersMock,
  probeServer: probeServerMock,
  reorderServers: vi.fn(),
  saveServer: vi.fn()
}))

import { useRealtimeStore } from '../stores/realtime'
import { useTaskProgressStore } from '../stores/taskProgress'
import ServersView from './ServersView.vue'

const server = {
  id: 'srv-1',
  name: 'one',
  host: '10.0.0.1',
  port: 22,
  username: 'root',
  authType: 'password',
  status: 'unknown'
}

describe('ServersView', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    getServerDefaultsMock.mockReset()
    listServersMock.mockReset()
    probeServerMock.mockReset()
    getServerDefaultsMock.mockResolvedValue({ defaultDeployDir: '/aifar/apps' })
    listServersMock.mockResolvedValue([server])
    probeServerMock.mockResolvedValue({ taskId: 'tsk-probe-1' })
  })

  it('loads the server list without starting probe tasks', async () => {
    shallowMount(ServersView, {
      global: {
        plugins: [createPinia()],
        stubs: { 'el-button': true, 'el-tooltip': true }
      }
    })

    await flushPromises()

    expect(listServersMock).toHaveBeenCalled()
    expect(probeServerMock).not.toHaveBeenCalled()
  })

  it('keeps manual probe out of the global task progress drawer', async () => {
    const pinia = createPinia()
    const wrapper = shallowMount(ServersView, {
      global: {
        plugins: [pinia],
        stubs: { 'el-button': true, 'el-tooltip': true }
      }
    })
    await flushPromises()
    const progress = useTaskProgressStore(pinia)
    const trackSpy = vi.spyOn(progress, 'track')

    wrapper.findComponent({ name: 'ServerDetailPanel' }).vm.$emit('probe', server)
    await flushPromises()

    expect(probeServerMock).toHaveBeenCalledWith('srv-1')
    expect(trackSpy).not.toHaveBeenCalled()
  })

  it('applies pushed server status snapshots through the realtime revision watcher', async () => {
    const pinia = createPinia()
    const wrapper = shallowMount(ServersView, {
      global: {
        plugins: [pinia],
        stubs: { 'el-button': true, 'el-tooltip': true }
      }
    })
    await flushPromises()

    useRealtimeStore(pinia).applyStatusSnapshot({
      scope: 'server',
      resourceId: 'srv-1',
      status: 'failed',
      lastError: 'connection refused',
      version: 2,
      collectedAt: '2026-08-03T10:00:15Z'
    })
    await flushPromises()

    expect(wrapper.findComponent({ name: 'ServerDetailPanel' }).props('server')).toMatchObject({
      id: 'srv-1',
      status: 'unavailable',
      lastError: 'connection refused'
    })
  })
})
