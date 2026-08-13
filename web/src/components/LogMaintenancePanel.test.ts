/**
 * @vitest-environment happy-dom
 */
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import LogMaintenancePanel from './LogMaintenancePanel.vue'

const apiPostMock = vi.hoisted(() => vi.fn())
const apiPutMock = vi.hoisted(() => vi.fn())
const trackTaskMock = vi.hoisted(() => vi.fn())
const messageMock = vi.hoisted(() => ({
  success: vi.fn(),
  warning: vi.fn(),
  error: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiPost: apiPostMock,
  apiPut: apiPutMock
}))

vi.mock('element-plus', async () => ({
  ElMessage: messageMock
}))

vi.mock('../stores/taskProgress', () => ({
  useTaskProgressStore: () => ({ track: trackTaskMock })
}))

describe('LogMaintenancePanel', () => {
  beforeEach(() => {
    apiPostMock.mockReset()
    apiPutMock.mockReset()
    trackTaskMock.mockReset()
    messageMock.success.mockReset()
    messageMock.warning.mockReset()
    messageMock.error.mockReset()
  })

  it('runs unified SQLite log cleanup from the log maintenance tab', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-1' })
    const wrapper = mount(LogMaintenancePanel, {
      props: { logRetentionDays: 45, canManage: true, disabledReason: 'denied' },
      global: {
        stubs: {
          KeyValueGrid: { props: ['items'], template: '<div><div v-for="item in items" :key="item.key">{{ item.label }} {{ item.value }}</div></div>' },
          'el-input-number': { template: '<input />' },
          'el-button': { template: `<button type="button" @click="$emit('click')"><slot /></button>` },
          'el-tooltip': { template: '<span><slot /></span>' }
        }
      }
    })

    expect(wrapper.text()).toContain('45')
    await wrapper.get('[data-testid="run-log-cleanup"]').trigger('click')
    await nextTick()

    expect(apiPostMock).toHaveBeenCalledWith('/maintenance/retention/run')
    expect(trackTaskMock).toHaveBeenCalledWith('task-1', 'Log Maintenance')
    expect(messageMock.success).toHaveBeenCalled()
  })

  it('saves the unified log retention days before cleanup', async () => {
    apiPutMock.mockResolvedValueOnce({ logRetentionDays: '1' })
    const wrapper = mount(LogMaintenancePanel, {
      props: { logRetentionDays: 90, canManage: true, disabledReason: 'denied' },
      global: {
        stubs: {
          KeyValueGrid: { props: ['items'], template: '<div><div v-for="item in items" :key="item.key">{{ item.label }} {{ item.value }}</div></div>' },
          'el-input-number': {
            props: ['modelValue'],
            emits: ['update:modelValue'],
            template: `<button type="button" data-testid="log-retention-input" @click="$emit('update:modelValue', 1)">set 1</button>`
          },
          'el-button': { template: `<button type="button" @click="$emit('click')"><slot /></button>` },
          'el-tooltip': { template: '<span><slot /></span>' }
        }
      }
    })

    await wrapper.get('[data-testid="log-retention-input"]').trigger('click')
    await wrapper.get('[data-testid="save-log-retention"]').trigger('click')
    await nextTick()

    expect(apiPutMock).toHaveBeenCalledWith('/settings', { logRetentionDays: 1 })
    expect(messageMock.success).toHaveBeenCalled()
    expect(wrapper.text()).toContain('1')
  })
})
