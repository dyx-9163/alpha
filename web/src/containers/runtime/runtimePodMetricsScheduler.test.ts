import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createRuntimePodMetricsScheduler } from './runtimePodMetricsScheduler'

describe('Runtime Pod metrics scheduler', () => {
  beforeEach(() => vi.useFakeTimers())
  afterEach(() => vi.useRealTimers())

  it('refreshes immediately and every interval while active', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimePodMetricsScheduler(refresh, 10_000)

    scheduler.start()
    expect(refresh).toHaveBeenCalledTimes(1)

    await vi.advanceTimersByTimeAsync(20_000)
    expect(refresh).toHaveBeenCalledTimes(3)
  })

  it('coalesces overlapping intervals into one follow-up refresh', async () => {
    let resolveRefresh: (() => void) | undefined
    const refresh = vi.fn(() => new Promise<void>((resolve) => {
      resolveRefresh = resolve
    }))
    const scheduler = createRuntimePodMetricsScheduler(refresh, 10_000)

    scheduler.start()
    await vi.advanceTimersByTimeAsync(30_000)
    expect(refresh).toHaveBeenCalledTimes(1)

    resolveRefresh?.()
    await Promise.resolve()
    await Promise.resolve()
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('stops cleanly and starts a fresh immediate refresh when reactivated', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimePodMetricsScheduler(refresh, 10_000)

    scheduler.start()
    scheduler.stop()
    await vi.advanceTimersByTimeAsync(20_000)
    expect(refresh).toHaveBeenCalledTimes(1)

    scheduler.start()
    expect(refresh).toHaveBeenCalledTimes(2)
  })

  it('cannot restart after disposal', async () => {
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createRuntimePodMetricsScheduler(refresh, 10_000)

    scheduler.start()
    scheduler.dispose()
    scheduler.start()
    scheduler.request()
    await vi.advanceTimersByTimeAsync(20_000)

    expect(refresh).toHaveBeenCalledTimes(1)
  })
})
