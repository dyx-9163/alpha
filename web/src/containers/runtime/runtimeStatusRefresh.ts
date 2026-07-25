export type RuntimeStatusEvent = {
  resource?: string
  serverId?: string
}

export function createRuntimeStatusRefreshScheduler(refresh: () => Promise<void>, delayMs = 50) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  let pending = false
  let disposed = false

  async function run() {
    timer = undefined
    if (disposed || !pending || running) return
    pending = false
    running = true
    try {
      await refresh()
    } finally {
      running = false
      if (pending && !disposed) schedule()
    }
  }

  function schedule() {
    if (disposed || running || timer !== undefined) return
    timer = globalThis.setTimeout(() => void run(), delayMs)
  }

  return {
    request() {
      if (disposed) return
      pending = true
      schedule()
    },
    dispose() {
      disposed = true
      pending = false
      if (timer !== undefined) globalThis.clearTimeout(timer)
      timer = undefined
    }
  }
}

export function isRuntimeStatusEventForSelection(
  event: RuntimeStatusEvent | null | undefined,
  selectedServerId: string,
  runtimeActive: boolean
) {
  if (!runtimeActive || event?.resource !== 'aifar.runtime') return false
  return !event.serverId || event.serverId === selectedServerId
}
