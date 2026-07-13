import { afterEach, describe, expect, it, vi } from 'vitest'
import { createDashboardRealtimeRefreshScheduler, shouldRefreshDashboardForRealtimeEvent } from './realtimeRefresh'

describe('dashboard realtime refresh', () => {
  afterEach(() => {
    vi.useRealTimers()
  })

  it.each([
    ['status.server.updated'],
    ['status.docker.summary.updated'],
    ['status.app.instance.updated'],
    ['task.updated'],
    ['task.finished']
  ])('refreshes dashboard data after %s', (type) => {
    expect(shouldRefreshDashboardForRealtimeEvent({ type })).toBe(true)
  })

  it.each([
    ['alert.updated'],
    ['alert.resolved'],
    ['collector.run.started'],
    ['collector.run.finished'],
    ['realtime.connected'],
    [''],
    [undefined]
  ])('does not reload the whole dashboard after %s', (type) => {
    expect(shouldRefreshDashboardForRealtimeEvent({ type })).toBe(false)
  })

  it('coalesces a burst of events into one refresh', async () => {
    vi.useFakeTimers()
    const refresh = vi.fn().mockResolvedValue(undefined)
    const scheduler = createDashboardRealtimeRefreshScheduler(refresh, 100)

    scheduler.schedule()
    scheduler.schedule()
    scheduler.schedule()

    await vi.advanceTimersByTimeAsync(99)
    expect(refresh).not.toHaveBeenCalled()

    await vi.advanceTimersByTimeAsync(1)
    expect(refresh).toHaveBeenCalledTimes(1)
  })

  it('runs one follow-up refresh when another event arrives during an in-flight refresh', async () => {
    vi.useFakeTimers()
    let finishRefresh!: () => void
    const refresh = vi.fn(() => new Promise<void>((resolve) => {
      finishRefresh = resolve
    }))
    const scheduler = createDashboardRealtimeRefreshScheduler(refresh, 100)

    scheduler.schedule()
    await vi.advanceTimersByTimeAsync(100)
    expect(refresh).toHaveBeenCalledTimes(1)

    scheduler.schedule()
    await vi.advanceTimersByTimeAsync(100)
    expect(refresh).toHaveBeenCalledTimes(1)

    finishRefresh()
    await vi.runOnlyPendingTimersAsync()
    expect(refresh).toHaveBeenCalledTimes(2)
  })
})
