/**
 * @vitest-environment happy-dom
 */
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import DataMaintenancePanel from './DataMaintenancePanel.vue'
import source from './DataMaintenancePanel.vue?raw'

const apiGetMock = vi.hoisted(() => vi.fn())
const apiPostMock = vi.hoisted(() => vi.fn())
const trackTaskMock = vi.hoisted(() => vi.fn())
const messageMock = vi.hoisted(() => ({
  success: vi.fn(),
  warning: vi.fn(),
  error: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiDelete: vi.fn(),
  apiDownload: vi.fn(),
  apiGet: apiGetMock,
  apiPost: apiPostMock
}))

vi.mock('element-plus', async () => ({
  ElMessage: messageMock
}))

vi.mock('../stores/taskProgress', () => ({
  useTaskProgressStore: () => ({ track: trackTaskMock })
}))

describe('DataMaintenancePanel source contract', () => {
  beforeEach(() => {
    apiGetMock.mockReset()
    apiPostMock.mockReset()
    trackTaskMock.mockReset()
    messageMock.success.mockReset()
    messageMock.warning.mockReset()
    messageMock.error.mockReset()
  })

  it('keeps database backups but moves log cleanup out of data maintenance', () => {
    expect(source).toContain('/maintenance/database-backup/run')
    expect(source).toContain('/maintenance/database-backups')
    expect(source).not.toContain('/maintenance/retention/run')
    expect(source).not.toContain('auditRetentionDays')
    expect(source).not.toContain('taskRetentionDays')
    expect(source).not.toContain('window.setTimeout(() => void refresh(), 800)')
  })

  function mountPanel() {
    apiGetMock.mockResolvedValue({
      items: [{ name: 'aifar-control-plane-20260813.db', path: 'backup.db', size: 128, sha256: 'abc', createdAt: '2026-08-13T00:00:00Z' }]
    })

    return mount(DataMaintenancePanel, {
      props: { backupDir: 'D:/backups', canManage: true, disabledReason: 'denied' },
      global: {
        stubs: {
          KeyValueGrid: { props: ['items'], template: '<div><div v-for="item in items" :key="item.key">{{ item.label }} {{ item.value }}</div></div>' },
          DataTable: {
            props: ['rows', 'columns'],
            template: `
              <section>
                <slot name="toolbar" />
                <div v-for="row in rows" :key="row.name">
                  <span>{{ row.name }}</span>
                  <slot name="action" :row="row" />
                </div>
              </section>`
          },
          ConfirmAction: { template: '<span><slot :confirm="() => {}" /></span>' },
          'el-button': { template: `<button v-bind="$attrs" type="button" @click="$emit('click')"><slot /></button>` },
          'el-tooltip': { template: '<span><slot /></span>' }
        }
      }
    })
  }

  it('tracks database backup tasks in the global task progress', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'backup-task' })
    const wrapper = mountPanel()

    await nextTick()
    await Promise.resolve()
    await wrapper.get('[data-testid="run-database-backup"]').trigger('click')
    await nextTick()

    expect(apiPostMock).toHaveBeenCalledWith('/maintenance/database-backup/run')
    expect(trackTaskMock).toHaveBeenCalledWith('backup-task', 'Database Backups')
  })

  it('tracks database backup verification tasks in the global task progress', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'verify-task' })
    const wrapper = mountPanel()

    await nextTick()
    await Promise.resolve()
    await wrapper.get('[data-testid="verify-database-backup"]').trigger('click')
    await nextTick()

    expect(apiPostMock).toHaveBeenCalledWith('/maintenance/database-backups/aifar-control-plane-20260813.db/verify')
    expect(trackTaskMock).toHaveBeenCalledWith('verify-task', 'Database Backups')
  })
})
