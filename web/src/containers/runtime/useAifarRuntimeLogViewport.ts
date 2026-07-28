import { computed, nextTick, ref, type ComputedRef } from 'vue'
import type { RuntimeLogRow } from './types'

export type RuntimeLogViewportOptions = {
  rowHeight: number
  visibleCount: number
}

export function useAifarRuntimeLogViewport(rows: ComputedRef<RuntimeLogRow[]>, options: RuntimeLogViewportOptions) {
  const runtimeLogScrollTop = ref(0)
  const runtimeLogViewport = ref<HTMLElement | null>(null)
  const rowHeight = positiveFinite(options.rowHeight, 1)
  const visibleCount = Math.max(1, Math.floor(positiveFinite(options.visibleCount, 1)))
  const runtimeLogVirtualStart = computed(() => {
    const requestedStart = Math.max(0, Math.floor(nonNegativeFinite(runtimeLogScrollTop.value) / rowHeight) - 8)
    const lastWindowStart = Math.max(0, rows.value.length - visibleCount)
    return Math.min(requestedStart, lastWindowStart)
  })
  const runtimeLogVirtualRows = computed(() => rows.value.slice(runtimeLogVirtualStart.value, runtimeLogVirtualStart.value + visibleCount))
  const runtimeLogTopSpacer = computed(() => runtimeLogVirtualStart.value * rowHeight)
  const runtimeLogBottomSpacer = computed(() => Math.max(0, (rows.value.length - runtimeLogVirtualStart.value - runtimeLogVirtualRows.value.length) * rowHeight))

  function handleRuntimeLogScroll(event: Event) {
    runtimeLogScrollTop.value = nonNegativeFinite((event.target as HTMLElement | null)?.scrollTop)
  }

  function resetRuntimeLogViewport() {
    runtimeLogScrollTop.value = 0
    if (runtimeLogViewport.value) {
      runtimeLogViewport.value.scrollTop = 0
    }
  }

  async function scrollRuntimeLogsToBottom() {
    await nextTick()
    await nextFrame()
    const viewport = runtimeLogViewport.value
    if (viewport) {
      setRuntimeLogScrollBottom(viewport)
    }
    await nextTick()
    await nextFrame()
    if (runtimeLogViewport.value) {
      setRuntimeLogScrollBottom(runtimeLogViewport.value)
    }
  }

  function setRuntimeLogScrollBottom(viewport: HTMLElement) {
    const maxScroll = nonNegativeFinite(viewport.scrollHeight - viewport.clientHeight)
    viewport.scrollTop = maxScroll
    runtimeLogScrollTop.value = maxScroll
  }

  return {
    runtimeLogScrollTop,
    runtimeLogViewport,
    runtimeLogVirtualRows,
    runtimeLogTopSpacer,
    runtimeLogBottomSpacer,
    handleRuntimeLogScroll,
    resetRuntimeLogViewport,
    scrollRuntimeLogsToBottom
  }
}

function positiveFinite(value: number | undefined, fallback: number) {
  return Number.isFinite(value) && Number(value) > 0 ? Number(value) : fallback
}

function nonNegativeFinite(value: number | undefined) {
  return Number.isFinite(value) && Number(value) >= 0 ? Number(value) : 0
}

function nextFrame() {
  return new Promise<void>((resolve) => {
    window.requestAnimationFrame(() => resolve())
  })
}
