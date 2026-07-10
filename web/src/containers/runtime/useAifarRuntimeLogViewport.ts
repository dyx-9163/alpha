import { computed, nextTick, ref, type ComputedRef } from 'vue'
import type { RuntimeLogRow } from './types'

export type RuntimeLogViewportOptions = {
  rowHeight: number
  visibleCount: number
}

export function useAifarRuntimeLogViewport(rows: ComputedRef<RuntimeLogRow[]>, options: RuntimeLogViewportOptions) {
  const runtimeLogScrollTop = ref(0)
  const runtimeLogViewport = ref<HTMLElement | null>(null)
  const runtimeLogVirtualStart = computed(() => {
    const requestedStart = Math.max(0, Math.floor(runtimeLogScrollTop.value / options.rowHeight) - 8)
    const lastWindowStart = Math.max(0, rows.value.length - options.visibleCount)
    return Math.min(requestedStart, lastWindowStart)
  })
  const runtimeLogVirtualRows = computed(() => rows.value.slice(runtimeLogVirtualStart.value, runtimeLogVirtualStart.value + options.visibleCount))
  const runtimeLogTopSpacer = computed(() => runtimeLogVirtualStart.value * options.rowHeight)
  const runtimeLogBottomSpacer = computed(() => Math.max(0, (rows.value.length - runtimeLogVirtualStart.value - runtimeLogVirtualRows.value.length) * options.rowHeight))

  function handleRuntimeLogScroll(event: Event) {
    runtimeLogScrollTop.value = (event.target as HTMLElement | null)?.scrollTop ?? 0
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
    const maxScroll = Math.max(0, viewport.scrollHeight - viewport.clientHeight)
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
    scrollRuntimeLogsToBottom
  }
}

function nextFrame() {
  return new Promise<void>((resolve) => {
    window.requestAnimationFrame(() => resolve())
  })
}
