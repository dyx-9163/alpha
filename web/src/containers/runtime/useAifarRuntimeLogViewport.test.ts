import { computed, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { useAifarRuntimeLogViewport } from './useAifarRuntimeLogViewport'
import type { RuntimeLogRow } from './types'

describe('AIFAR Runtime log viewport', () => {
  beforeEach(() => {
    vi.stubGlobal('window', {
      requestAnimationFrame(callback: FrameRequestCallback) {
        callback(0)
        return 1
      }
    })
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('computes the overscanned start, visible rows, and spacers', () => {
    const source = ref(rows(20))
    const viewport = useAifarRuntimeLogViewport(computed(() => source.value), {
      rowHeight: 10,
      visibleCount: 3
    })

    viewport.handleRuntimeLogScroll({ target: { scrollTop: 150 } } as unknown as Event)

    expect(viewport.runtimeLogVirtualRows.value.map((row) => row.sequence)).toEqual([7, 8, 9])
    expect(viewport.runtimeLogTopSpacer.value).toBe(70)
    expect(viewport.runtimeLogBottomSpacer.value).toBe(100)
  })

  it('clamps a deep scroll position after filtering shortens the row set', () => {
    const source = ref(rows(20))
    const viewport = useAifarRuntimeLogViewport(computed(() => source.value), {
      rowHeight: 10,
      visibleCount: 3
    })
    viewport.handleRuntimeLogScroll({ target: { scrollTop: 150 } } as unknown as Event)

    source.value = rows(5)

    expect(viewport.runtimeLogVirtualRows.value.map((row) => row.sequence)).toEqual([2, 3, 4])
    expect(viewport.runtimeLogTopSpacer.value).toBe(20)
    expect(viewport.runtimeLogBottomSpacer.value).toBe(0)
  })

  it('resets the tracked scroll offset when the event target is absent', () => {
    const viewport = useAifarRuntimeLogViewport(computed(() => rows(3)), {
      rowHeight: 10,
      visibleCount: 3
    })
    viewport.handleRuntimeLogScroll({ target: { scrollTop: 40 } } as unknown as Event)
    expect(viewport.runtimeLogScrollTop.value).toBe(40)
    expect(viewport.runtimeLogTopSpacer.value).toBe(0)

    viewport.handleRuntimeLogScroll({ target: null } as unknown as Event)

    expect(viewport.runtimeLogScrollTop.value).toBe(0)
    expect(viewport.runtimeLogTopSpacer.value).toBe(0)
  })

  it('scrolls to the current bottom on two animation frames', async () => {
    let frames = 0
    vi.stubGlobal('window', {
      requestAnimationFrame(callback: FrameRequestCallback) {
        frames += 1
        callback(frames)
        return frames
      }
    })
    let scrollTop = 0
    const element = {
      get scrollHeight() {
        return frames === 1 ? 500 : 700
      },
      clientHeight: 100,
      get scrollTop() {
        return scrollTop
      },
      set scrollTop(value: number) {
        scrollTop = value
      }
    } as unknown as HTMLElement
    const viewport = useAifarRuntimeLogViewport(computed(() => rows(30)), {
      rowHeight: 10,
      visibleCount: 3
    })
    viewport.runtimeLogViewport.value = element

    await viewport.scrollRuntimeLogsToBottom()

    expect(frames).toBe(2)
    expect(scrollTop).toBe(600)
    expect(viewport.runtimeLogBottomSpacer.value).toBe(0)
  })
})

function rows(count: number): RuntimeLogRow[] {
  return Array.from({ length: count }, (_, sequence) => ({
    id: `row-${sequence}`,
    time: `time-${sequence}`,
    timestamp: sequence,
    sequence,
    serviceName: 'gateway',
    pod: 'gateway-1',
    level: 'INFO',
    message: `message-${sequence}`,
    errorContext: false
  }))
}
