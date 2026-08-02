import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { StatusSnapshot } from '../stores/realtime'
import type { ServerRecord } from './types'

const {
  deleteServerMock,
  getServerDefaultsMock,
  listServersMock,
  probeServerMock,
  reorderServersMock,
  saveServerMock,
  waitTaskDoneMock
} = vi.hoisted(() => ({
  deleteServerMock: vi.fn(),
  getServerDefaultsMock: vi.fn(),
  listServersMock: vi.fn(),
  probeServerMock: vi.fn(),
  reorderServersMock: vi.fn(),
  saveServerMock: vi.fn(),
  waitTaskDoneMock: vi.fn()
}))

vi.mock('./api', () => ({
  deleteServer: deleteServerMock,
  getServerDefaults: getServerDefaultsMock,
  listServers: listServersMock,
  probeServer: probeServerMock,
  reorderServers: reorderServersMock,
  saveServer: saveServerMock,
  waitTaskDone: waitTaskDoneMock
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
    waitTaskDoneMock.mockReset()
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

    expect(workbench.servers.value[0]).toMatchObject({ status: 'failed', lastError: 'timeout' })
    expect(workbench.selectedId.value).toBe('srv-1')
    expect(workbench.search.value).toBe('one')
    expect(workbench.drawer.value).toBe(true)
    expect(workbench.activeTab.value).toBe('access')
    expect(workbench.form.name).toBe('draft name')
  })

  it('keeps manual probing status until the probe task finishes and then reloads', async () => {
    const snapshots = new Map<string, StatusSnapshot>()
    const taskDone = deferred<string>()
    listServersMock.mockResolvedValue([server])
    probeServerMock.mockResolvedValueOnce({ taskId: 'tsk-probe-1' })
    waitTaskDoneMock.mockReturnValueOnce(taskDone.promise)
    const workbench = useServerWorkbench((key) => key, (id) => snapshots.get(id))
    await workbench.load()

    const probePromise = workbench.probe(workbench.servers.value[0])
    await Promise.resolve()
    snapshots.set('srv-1', snapshot({ status: 'failed', lastError: 'timeout', version: 2 }))
    workbench.applyStatusSnapshots()

    expect(workbench.probingIds.value.has('srv-1')).toBe(true)
    expect(workbench.servers.value[0]).toMatchObject({ status: 'probing', lastError: '' })

    taskDone.resolve('success')
    await probePromise

    expect(workbench.probingIds.value.has('srv-1')).toBe(false)
    expect(listServersMock).toHaveBeenCalledTimes(2)
    expect(workbench.servers.value[0]).toMatchObject({ status: 'failed', lastError: 'timeout' })
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

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason?: unknown) => void
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise
    reject = rejectPromise
  })
  return { promise, resolve, reject }
}
