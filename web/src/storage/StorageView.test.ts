// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createPinia } from 'pinia'
import { createMemoryHistory, createRouter } from 'vue-router'
import ElementPlus from 'element-plus'
import StorageView from '../views/StorageView.vue'
import { setLocale } from '../i18n'

const { apiGet } = vi.hoisted(() => ({
  apiGet: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiGet,
  apiPost: vi.fn(),
  apiDelete: vi.fn(),
  asArray: (value: unknown) => Array.isArray(value) ? value : []
}))

vi.mock('../stores/taskProgress', () => ({
  useTaskProgressStore: () => ({ track: vi.fn() })
}))

describe('StorageView', () => {
  beforeEach(() => {
    apiGet.mockReset()
    apiGet.mockResolvedValue([])
    localStorage.clear()
    localStorage.setItem('aifar-session-token', 'test-token')
    localStorage.setItem('aifar-role', 'owner')
    setLocale('zh')
  })

  it('does not expose cleanup controls on the instances tab', async () => {
    const router = createRouter({
      history: createMemoryHistory(),
      routes: [{ path: '/', component: { template: '<div />' } }]
    })
    await router.push('/')
    await router.isReady()

    const wrapper = mount(StorageView, {
      global: {
        plugins: [createPinia(), router, ElementPlus],
        stubs: { teleport: true, StatusTag: true, RunRecordTable: true, KeyValueGrid: true }
      }
    })
    await flushPromises()

    expect(wrapper.findAll('.cleanup-policy-control')).toHaveLength(0)
    expect(wrapper.find('.cleanup-day-presets').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('统计可清理')
  })
})
