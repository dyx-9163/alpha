import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { StatusSnapshot } from '../stores/realtime'
import type { ServerRecord } from './types'

const {
  deleteServerMock,
  getServerDefaultsMock,
  listServersMock,
  probeServerMock,
  reorderServersMock,
  saveServerMock
} = vi.hoisted(() => ({
  deleteServerMock: vi.fn(),
  getServerDefaultsMock: vi.fn(),
  listServersMock: vi.fn(),
  probeServerMock: vi.fn(),
  reorderServersMock: vi.fn(),
  saveServerMock: vi.fn()
}))

vi.mock('./api', () => ({
  deleteServer: deleteServerMock,
  getServerDefaults: getServerDefaultsMock,
  listServers: listServersMock,
  probeServer: probeServerMock,
  reorderServers: reorderServersMock,
  saveServer: saveServerMock
}))

import { useServerWorkbench } from './useServerWorkbench'

const server: ServerRecord = {
  id: 'srv-1',
  name: 'one',
  host: '10.0.0.1',
  port: 22,
  username: 'root',
  authType: 'password',
  status: 'unknown'
}

describe('useServerWorkbench realtime status', () => {
  beforeEach(() => {
    deleteServerMock.mockReset()
    getServerDefaultsMock.mockReset()
    listServersMock.mockReset()
    probeServerMock.mockReset()
    reorderServersMock.mockReset()
    saveServerMock.mockReset()
  })

  it('applies live snapshots without resetting workbench state', async () => {
    const snapshots = new Map<string, StatusSnapshot>()
    listServersMock.mockResolvedValueOnce([server])
    snapshots.set('srv-1', snapshot({ status: 'available', version: 1, collectedAt: '2026-08-03T10:00:00Z' }))
    const workbench = useServerWorkbench((key) => key, (id) => snapshots.get(id))

    await workbench.load()

    expect(workbench.servers.value[0]).toMatchObject({ status: 'available', lastError: '' })
    workbench.selectedId.value = 'srv-1'
    workbench.search.value = 'one'
    workbench.drawer.value = true
    workbench.activeTab.value = 'access'
    workbench.form.name = 'draft name'

    snapshots.set('srv-1', snapshot({
      status: 'failed',
      lastError: 'timeout',
      version: 2,
      collectedAt: '2026-08-03T10:00:15Z'
    }))
    workbench.applyStatusSnapshots()

    expect(workbench.servers.value[0]).toMatchObject({ status: 'unavailable', lastError: 'timeout' })
    expect(workbench.selectedId.value).toBe('srv-1')
    expect(workbench.search.value).toBe('one')
    expect(workbench.drawer.value).toBe(true)
    expect(workbench.activeTab.value).toBe('access')
    expect(workbench.form.name).toBe('draft name')
  })

  it('keeps manual probe local instead of tracking it globally', async () => {
    const snapshots = new Map<string, StatusSnapshot>()
    listServersMock.mockResolvedValue([server])
    probeServerMock.mockResolvedValueOnce({ taskId: 'tsk-probe-1' })
    const workbench = useServerWorkbench((key) => key, (id) => snapshots.get(id))
    await workbench.load()

    await workbench.probe(workbench.servers.value[0])
    snapshots.set('srv-1', snapshot({ status: 'failed', lastError: 'timeout', version: 2 }))
    workbench.applyStatusSnapshots()

    expect(workbench.probingIds.value.has('srv-1')).toBe(false)
    expect(listServersMock).toHaveBeenCalledTimes(2)
    expect(workbench.servers.value[0]).toMatchObject({ status: 'unavailable', lastError: 'timeout' })
  })

  it.each(['available', 'running', 'success', 'ok'])('counts %s server snapshots as available like the dashboard', async (status) => {
    const snapshots = new Map<string, StatusSnapshot>()
    listServersMock.mockResolvedValueOnce([server])
    snapshots.set('srv-1', snapshot({ status, version: 1, collectedAt: '2026-08-03T10:00:00Z' }))
    const workbench = useServerWorkbench((key) => key, (id) => snapshots.get(id))

    await workbench.load()

    expect(workbench.servers.value[0].status).toBe('available')
    expect(workbench.summary.value.available).toBe(1)
  })
})

function snapshot(overrides: Partial<StatusSnapshot> = {}): StatusSnapshot {
  return {
    scope: 'server',
    resourceId: 'srv-1',
    status: 'unknown',
    lastError: '',
    version: 1,
    collectedAt: '2026-08-03T10:00:00Z',
    ...overrides
  }
}
