import { afterEach, describe, expect, it, vi } from 'vitest'

import { createRuntimeStatusRefreshScheduler, isRuntimeStatusEventForSelection } from './runtimeStatusRefresh'

describe('runtime status refresh scheduler', () => {
  afterEach(() => vi.useRealTimers())

  it('coalesces multiple status events into one refresh', async () => {
    vi.useFakeTimers()
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimeStatusRefreshScheduler(refresh, 25)

    scheduler.request()
    scheduler.request()
    await vi.advanceTimersByTimeAsync(25)

    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('runs one follow-up refresh when an event arrives in flight', async () => {
    vi.useFakeTimers()
    let finish!: () => void
    const refresh = vi.fn()
      .mockImplementationOnce(() => new Promise<void>((resolve) => { finish = resolve }))
      .mockResolvedValue(undefined)
    const scheduler = createRuntimeStatusRefreshScheduler(refresh, 1)

    scheduler.request()
    await vi.advanceTimersByTimeAsync(1)
    scheduler.request()
    finish()
    await Promise.resolve()
    await vi.advanceTimersByTimeAsync(1)

    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('cancels pending work when disposed', async () => {
    vi.useFakeTimers()
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimeStatusRefreshScheduler(refresh, 10)

    scheduler.request()
    scheduler.dispose()
    await vi.runAllTimersAsync()

    expect(refresh).not.toHaveBeenCalled()
  })

  it('accepts only runtime events for the active selected server', () => {
    expect(isRuntimeStatusEventForSelection({ resource: 'aifar.runtime', serverId: 'server-1' }, 'server-1', true)).toBe(true)
    expect(isRuntimeStatusEventForSelection({ resource: 'aifar.runtime', serverId: 'server-2' }, 'server-1', true)).toBe(false)
    expect(isRuntimeStatusEventForSelection({ resource: 'docker.summary', serverId: 'server-1' }, 'server-1', true)).toBe(false)
    expect(isRuntimeStatusEventForSelection({ resource: 'aifar.runtime', serverId: 'server-1' }, 'server-1', false)).toBe(false)
  })
})
