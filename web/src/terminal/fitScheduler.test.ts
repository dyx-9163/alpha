import { describe, expect, it, vi } from 'vitest'

import { createTerminalFitScheduler } from './fitScheduler'

function schedulerHarness(visible = true) {
  let isVisible = visible
  const renderQueue: Array<() => void> = []
  const frameQueue = new Map<number, () => void>()
  let nextFrameId = 1
  const fit = vi.fn()
  const scheduler = createTerminalFitScheduler({
    isVisible: () => isVisible,
    fit,
    afterRender: (callback) => { renderQueue.push(callback) },
    requestFrame: (callback) => {
      const id = nextFrameId++
      frameQueue.set(id, callback)
      return id
    },
    cancelFrame: (id) => { frameQueue.delete(id) }
  })
  return {
    scheduler,
    fit,
    setVisible: (value: boolean) => { isVisible = value },
    flushRender: () => { renderQueue.shift()?.() },
    flushFrame: () => {
      const first = frameQueue.entries().next().value as [number, () => void] | undefined
      if (!first) return
      frameQueue.delete(first[0])
      first[1]()
    }
  }
}

describe('terminal fit scheduler', () => {
  it('does not measure a hidden pane', () => {
    const harness = schedulerHarness(false)

    harness.scheduler.schedule()
    harness.flushRender()
    harness.flushFrame()

    expect(harness.fit).not.toHaveBeenCalled()
  })

  it('fits after render and once more on the next animation frame', () => {
    const harness = schedulerHarness()

    harness.scheduler.schedule()
    harness.scheduler.schedule()
    harness.flushRender()
    expect(harness.fit).toHaveBeenCalledTimes(1)
    harness.flushFrame()

    expect(harness.fit).toHaveBeenCalledTimes(2)
  })

  it('does not run queued work after disposal', () => {
    const harness = schedulerHarness()

    harness.scheduler.schedule()
    harness.scheduler.dispose()
    harness.flushRender()
    harness.flushFrame()

    expect(harness.fit).not.toHaveBeenCalled()
  })

  it('rechecks visibility after the render boundary', () => {
    const harness = schedulerHarness()
    harness.scheduler.schedule()
    harness.setVisible(false)

    harness.flushRender()
    harness.flushFrame()

    expect(harness.fit).not.toHaveBeenCalled()
  })
})
