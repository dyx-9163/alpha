export function createRuntimePodMetricsScheduler(
  refresh: () => Promise<void>,
  intervalMs = 10_000
) {
  let timer: ReturnType<typeof setInterval> | undefined
  let active = false
  let running = false
  let pending = false
  let disposed = false

  async function run() {
    if (!active || disposed) return
    if (running) {
      pending = true
      return
    }
    running = true
    try {
      await refresh()
    } finally {
      running = false
      if (active && pending && !disposed) {
        pending = false
        void run()
      }
    }
  }

  function clearTimer() {
    if (timer !== undefined) globalThis.clearInterval(timer)
    timer = undefined
  }

  return {
    start() {
      if (active || disposed) return
      active = true
      timer = globalThis.setInterval(() => void run(), intervalMs)
      void run()
    },
    request() {
      void run()
    },
    stop() {
      active = false
      pending = false
      clearTimer()
    },
    dispose() {
      disposed = true
      active = false
      pending = false
      clearTimer()
    }
  }
}
