import { describe, expect, it } from 'vitest'
import { applyRealtimeStatusToServer } from './realtimeStatus'
import type { StatusSnapshot } from '../stores/realtime'
import type { ServerRecord } from './types'

const server: ServerRecord = {
  id: 'server-9',
  name: 'Primary',
  host: '10.0.0.9',
  port: 22,
  username: 'root',
  authType: 'password',
  status: 'available',
  lastError: '',
  updatedAt: '2026-08-03T10:00:00Z'
}

describe('applyRealtimeStatusToServer', () => {
  it('applies a newer matching snapshot without copying payload fields', () => {
    const result = applyRealtimeStatusToServer(server, {
      scope: 'server',
      resourceId: 'server-9',
      status: 'failed',
      lastError: 'connection refused',
      collectedAt: '2026-08-03T10:00:01Z',
      payload: { host: 'wrong-host', deployDir: '/wrong/path' }
    })

    expect(result).toEqual({
      ...server,
      status: 'failed',
      lastError: 'connection refused'
    })
  })

  it.each([
    ['wrong scope', { scope: 'docker.summary', resourceId: 'server-9', status: 'failed' }],
    ['wrong resource id', { scope: 'server', resourceId: 'server-8', status: 'failed' }]
  ])('keeps the canonical server for a snapshot with %s', (_reason, snapshot) => {
    expect(applyRealtimeStatusToServer(server, snapshot)).toBe(server)
  })

  it('keeps the canonical server when the snapshot predates its update', () => {
    const result = applyRealtimeStatusToServer(server, {
      scope: 'server',
      resourceId: 'server-9',
      status: 'failed',
      lastError: 'connection refused',
      collectedAt: '2026-08-03T09:59:59Z'
    })

    expect(result).toBe(server)
  })

  it('keeps the canonical server when the snapshot has no usable status', () => {
    const result = applyRealtimeStatusToServer(server, {
      scope: 'server',
      resourceId: 'server-9',
      status: '   ',
      collectedAt: '2026-08-03T10:00:01Z'
    })

    expect(result).toBe(server)
  })

  it.each([
    ['status', { status: { state: 'failed' }, lastError: '' }],
    ['lastError', { status: 'failed', lastError: { message: 'connection refused' } }]
  ])('keeps the canonical server when snapshot %s is not a string', (_field, malformed) => {
    const result = applyRealtimeStatusToServer(server, {
      scope: 'server',
      resourceId: 'server-9',
      collectedAt: '2026-08-03T10:00:01Z',
      ...malformed
    } as unknown as StatusSnapshot)

    expect(result).toBe(server)
  })
})
