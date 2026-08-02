import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const apiGetMock = vi.hoisted(() => vi.fn())

vi.mock('../api/client', () => ({
  apiGet: apiGetMock,
  apiPost: vi.fn(),
  asArray: <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : [],
  eventStreamUrl: () => '/api/v2/events'
}))

import { type StatusSnapshot, useRealtimeStore } from './realtime'

const key = 'app.instance:instance-1'

describe('realtime status snapshots', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    apiGetMock.mockReset()
  })

  it('retains the cache and success markers when loading fails', async () => {
    const store = useRealtimeStore()
    const cached = snapshot({ status: 'running', version: 2 })
    store.statusSnapshotsByKey = { [key]: cached }
    store.statusRevision = 7
    store.snapshotsLoadedAt = 123
    apiGetMock.mockRejectedValueOnce(new Error('network unavailable'))

    await expect(store.loadStatusSnapshots()).resolves.toBe(false)

    expect(store.statusSnapshotsByKey).toEqual({ [key]: cached })
    expect(store.statusRevision).toBe(7)
    expect(store.snapshotsLoadedAt).toBe(123)
  })

  it.each([
    ['null response', null],
    ['missing items', {}],
    ['null items', { items: null }],
    ['empty items', { items: [] }]
  ])('treats a successful %s as a non-destructive load', async (_name, response) => {
    const store = useRealtimeStore()
    const cached = snapshot({ status: 'running', version: 2 })
    store.statusSnapshotsByKey = { [key]: cached }
    const cacheBeforeLoad = store.statusSnapshotsByKey
    store.statusRevision = 7
    store.snapshotsLoadedAt = 123
    apiGetMock.mockResolvedValueOnce(response)

    await expect(store.loadStatusSnapshots()).resolves.toBe(true)

    expect(store.statusSnapshotsByKey).toBe(cacheBeforeLoad)
    expect(store.statusSnapshotsByKey).toEqual({ [key]: cached })
    expect(store.statusRevision).toBe(7)
    expect(store.snapshotsLoadedAt).toBeGreaterThan(123)
  })

  it('keeps a higher version even when an older GET snapshot has a later timestamp', async () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({ status: 'healthy', version: 3, collectedAt: '2026-07-10T10:00:00Z' }))
    apiGetMock.mockResolvedValueOnce({
      items: [snapshot({ status: 'stale', version: 2, collectedAt: '2026-07-10T11:00:00Z' })]
    })

    await expect(store.loadStatusSnapshots()).resolves.toBe(true)

    expect(store.statusSnapshotsByKey[key]?.status).toBe('healthy')
    expect(store.statusSnapshotsByKey[key]?.version).toBe(3)
  })

  it('uses collectedAt and updatedAt to reject out-of-order snapshots with the same version', async () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({
      status: 'newest',
      version: 4,
      collectedAt: '2026-07-10T11:00:00Z',
      updatedAt: '2026-07-10T11:00:01Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'older-collection',
      version: 4,
      collectedAt: '2026-07-10T10:59:59Z',
      updatedAt: '2026-07-10T11:00:02Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'older-update',
      version: 4,
      collectedAt: '2026-07-10T11:00:00Z',
      updatedAt: '2026-07-10T10:59:59Z'
    }))

    expect(store.statusSnapshotsByKey[key]?.status).toBe('newest')
    expect(store.statusRevision).toBe(1)
  })

  it('accepts a newer updatedAt when version and collectedAt are equal', () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({
      status: 'before-update',
      version: 5,
      collectedAt: '2026-07-10T11:00:00Z',
      updatedAt: '2026-07-10T11:00:01Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'after-update',
      version: 5,
      collectedAt: '2026-07-10T11:00:00Z',
      updatedAt: '2026-07-10T11:00:02Z'
    }))

    expect(store.statusSnapshotsByKey[key]?.status).toBe('after-update')
    expect(store.statusRevision).toBe(2)
  })

  it('does not substitute updatedAt for a missing collectedAt', () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({
      status: 'has-collection-time',
      version: 5,
      collectedAt: '2026-07-10T11:00:00Z',
      updatedAt: '2026-07-10T11:00:01Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'missing-collection-time',
      version: 5,
      collectedAt: undefined,
      updatedAt: '2026-07-10T12:00:00Z'
    }))

    expect(store.statusSnapshotsByKey[key]?.status).toBe('has-collection-time')
    expect(store.statusRevision).toBe(1)
  })

  it('prefers a present collectedAt over a missing or invalid collectedAt', () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({
      status: 'missing-collection-time',
      version: 5,
      collectedAt: undefined,
      updatedAt: '2026-07-10T12:00:00Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'invalid-collection-time',
      version: 5,
      collectedAt: 'not-a-timestamp',
      updatedAt: '2026-07-10T13:00:00Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'has-collection-time',
      version: 5,
      collectedAt: '2026-07-10T09:00:00Z',
      updatedAt: '2026-07-10T09:00:01Z'
    }))

    expect(store.statusSnapshotsByKey[key]?.status).toBe('has-collection-time')
    expect(store.statusRevision).toBe(3)
  })

  it('does not replace a snapshot with identical freshness', () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({ status: 'accepted' }))
    const accepted = store.statusSnapshotsByKey[key]

    store.applyStatusSnapshot(snapshot({ status: 'same-freshness' }))

    expect(store.statusSnapshotsByKey[key]).toBe(accepted)
    expect(store.statusSnapshotsByKey[key]?.status).toBe('accepted')
    expect(store.statusRevision).toBe(1)
  })

  it('treats non-finite versions as zero', () => {
    const store = useRealtimeStore()
    store.applyStatusSnapshot(snapshot({
      status: 'finite-version',
      version: 1,
      collectedAt: '2026-07-10T09:00:00Z'
    }))
    store.applyStatusSnapshot(snapshot({
      status: 'non-finite-version',
      version: Number.NaN,
      collectedAt: '2026-07-10T12:00:00Z'
    }))

    expect(store.statusSnapshotsByKey[key]?.status).toBe('finite-version')
    expect(store.statusRevision).toBe(1)
  })

  it('merges successful GET and SSE snapshots without dropping unrelated cache entries', async () => {
    const store = useRealtimeStore()
    const dockerKey = 'docker.summary:server-1'
    store.statusSnapshotsByKey = {
      [dockerKey]: {
        scope: 'docker.summary',
        resourceId: 'server-1',
        status: 'available',
        version: 1,
        collectedAt: '2026-07-10T09:00:00Z'
      }
    }
    apiGetMock.mockResolvedValueOnce({
      items: [snapshot({ status: 'starting', version: 1, collectedAt: '2026-07-10T10:00:00Z' })]
    })

    await expect(store.loadStatusSnapshots()).resolves.toBe(true)
    store.applyStatusSnapshot(snapshot({ status: 'running', version: 1, collectedAt: '2026-07-10T10:00:01Z' }))

    expect(store.statusSnapshotsByKey[dockerKey]?.status).toBe('available')
    expect(store.statusSnapshotsByKey[key]?.status).toBe('running')
    expect(store.statusSnapshots).toHaveLength(2)
    expect(store.snapshotsLoadedAt).toBeGreaterThan(0)
  })

  it('increments statusRevision once when one GET accepts multiple snapshots', async () => {
    const store = useRealtimeStore()
    apiGetMock.mockResolvedValueOnce({
      items: [
        snapshot({ status: 'running' }),
        {
          scope: 'docker.summary',
          resourceId: 'server-1',
          status: 'available',
          version: 3,
          collectedAt: '2026-07-10T12:00:00Z',
          updatedAt: '2026-07-10T12:00:01Z'
        }
      ]
    })

    await expect(store.loadStatusSnapshots()).resolves.toBe(true)

    expect(store.statusSnapshots).toHaveLength(2)
    expect(store.statusRevision).toBe(1)
  })

  it('builds a snapshot from the collector status event envelope', () => {
    const store = useRealtimeStore()

    store.applyEvent({
      id: 'event-1',
      type: 'status.docker.summary.updated',
      resource: 'docker.summary',
      resourceId: 'server-9',
      serverId: 'server-9',
      status: 'available',
      version: 12,
      collectedAt: '2026-07-10T12:00:00Z',
      createdAt: '2026-07-10T12:00:02Z',
      payload: {
        scope: 'docker.summary',
        resourceId: 'server-9',
        serverId: 'server-9',
        status: 'available',
        payload: { running: 3, stopped: 1 },
        lastError: '',
        version: 12,
        collectedAt: '2026-07-10T12:00:00Z',
        updatedAt: '2026-07-10T12:00:01Z',
        changed: true
      }
    })

    expect(store.dockerSummarySnapshot('server-9')).toEqual({
      scope: 'docker.summary',
      resourceId: 'server-9',
      serverId: 'server-9',
      status: 'available',
      payload: { running: 3, stopped: 1 },
      lastError: '',
      version: 12,
      collectedAt: '2026-07-10T12:00:00Z',
      updatedAt: '2026-07-10T12:00:01Z'
    })
    expect(store.statusRevision).toBe(1)
    expect(store.revision).toBe(1)
  })

  it('indexes a server status event by server id', () => {
    const store = useRealtimeStore()

    store.applyEvent({
      type: 'status.server.updated',
      resource: 'server',
      resourceId: 'server-9',
      serverId: 'server-9',
      status: 'failed',
      version: 4,
      collectedAt: '2026-08-03T10:00:00Z',
      payload: {
        scope: 'server', resourceId: 'server-9', serverId: 'server-9',
        status: 'failed', lastError: 'connection refused', version: 4,
        collectedAt: '2026-08-03T10:00:00Z', updatedAt: '2026-08-03T10:00:01Z',
        payload: { status: 'failed' }
      }
    })

    expect(store.serverSnapshot('server-9')?.status).toBe('failed')
  })

  it('does not let an older in-flight GET overwrite a newer SSE snapshot', async () => {
    const store = useRealtimeStore()
    const older = deferred<{ items: StatusSnapshot[] }>()
    apiGetMock.mockReturnValueOnce(older.promise)

    const load = store.loadStatusSnapshots()
    store.applyEvent({
      type: 'status.app.instance.updated',
      resource: 'app.instance',
      resourceId: 'instance-1',
      version: 9,
      collectedAt: '2026-07-10T12:00:00Z',
      payload: {
        scope: 'app.instance',
        resourceId: 'instance-1',
        status: 'newer-sse',
        payload: {},
        version: 9,
        collectedAt: '2026-07-10T12:00:00Z',
        updatedAt: '2026-07-10T12:00:01Z'
      }
    })
    older.resolve({
      items: [snapshot({ status: 'older-get', version: 8, collectedAt: '2026-07-10T12:01:00Z' })]
    })

    await expect(load).resolves.toBe(true)
    expect(store.statusSnapshotsByKey[key]?.status).toBe('newer-sse')
    expect(store.statusSnapshotsByKey[key]?.version).toBe(9)
    expect(store.statusRevision).toBe(1)
  })

  it('does not regress when concurrent loads resolve newest first', async () => {
    const store = useRealtimeStore()
    const older = deferred<{ items: StatusSnapshot[] }>()
    const newer = deferred<{ items: StatusSnapshot[] }>()
    apiGetMock.mockReturnValueOnce(older.promise).mockReturnValueOnce(newer.promise)

    const firstLoad = store.loadStatusSnapshots()
    const secondLoad = store.loadStatusSnapshots()
    newer.resolve({ items: [snapshot({ status: 'newer', version: 8, collectedAt: '2026-07-10T12:00:00Z' })] })
    await expect(secondLoad).resolves.toBe(true)
    older.resolve({ items: [snapshot({ status: 'older', version: 7, collectedAt: '2026-07-10T12:01:00Z' })] })
    await expect(firstLoad).resolves.toBe(true)

    expect(store.statusSnapshotsByKey[key]?.status).toBe('newer')
    expect(store.statusSnapshotsByKey[key]?.version).toBe(8)
  })
})

function snapshot(overrides: Partial<StatusSnapshot> = {}): StatusSnapshot {
  return {
    scope: 'app.instance',
    resourceId: 'instance-1',
    status: 'unknown',
    version: 1,
    collectedAt: '2026-07-10T10:00:00Z',
    updatedAt: '2026-07-10T10:00:01Z',
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
