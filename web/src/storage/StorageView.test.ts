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

async function mountStorage() {
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
  return wrapper
}

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
    const wrapper = await mountStorage()

    expect(wrapper.findAll('.cleanup-policy-control')).toHaveLength(0)
    expect(wrapper.find('.cleanup-day-presets').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('统计可清理')
  })

  it.each([
    { products: ['silo', 'silo'], prefix: 'silo', product: 'Silo' },
    { products: [undefined, undefined], prefix: 'minio', product: 'MinIO' },
    { products: [undefined, 'silo'], prefix: 'object-storage', product: 'MinIO / Silo' }
  ])('shows $product for the registered storage products', async ({ products, prefix, product }) => {
    const rows = products.map((serverProduct, index) => ({
      id: `instance-${index}`,
      app: 'minio',
      version: serverProduct === 'silo' ? '20261112' : '2025-10-15T17-29-55Z',
      status: 'available',
      serverId: `server-${index}`,
      topology: 'bucket-replication',
      metadata: JSON.stringify({ serverProduct, replicationGroupId: 'group-123456' })
    }))
    apiGet.mockImplementation(async (path: string) => path === '/storage/instances' ? rows : [])
    const wrapper = await mountStorage()

    expect(wrapper.find('.storage-title-block strong').text()).toBe(`${prefix}-bucket-replication-123456`)
    expect(wrapper.find('.storage-info-grid strong').text()).toBe(product)
    expect(wrapper.find('.storage-node-list .section-label').text()).toBe(`${product} 节点`)
    expect(wrapper.findAll('.storage-node-row')).toHaveLength(2)
    wrapper.unmount()
  })
})
