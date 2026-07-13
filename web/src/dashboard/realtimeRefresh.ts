import type { RealtimeEvent } from '../stores/realtime'

type RefreshFn = () => Promise<void> | void

export function shouldRefreshDashboardForRealtimeEvent(event: Pick<RealtimeEvent, 'type'> | null | undefined) {
  const type = String(event?.type ?? '').trim()
  return type.startsWith('status.') || type === 'task.updated' || type === 'task.finished'
}

export function createDashboardRealtimeRefreshScheduler(refresh: RefreshFn, delayMs = 800) {
  let timer: ReturnType<typeof setTimeout> | undefined
  let running = false
  let pending = false

  const clear = () => {
    if (timer !== undefined) {
      clearTimeout(timer)
      timer = undefined
    }
  }

  const run = async () => {
    timer = undefined
    if (running) {
      pending = true
      return
    }
    running = true
    try {
      await refresh()
    } finally {
      running = false
      if (pending) {
        pending = false
        schedule()
      }
    }
  }

  const schedule = () => {
    if (running) {
      pending = true
      return
    }
    if (timer !== undefined) {
      return
    }
    timer = setTimeout(() => {
      void run()
    }, delayMs)
  }

  const dispose = () => {
    clear()
    pending = false
  }

  return { schedule, dispose }
}
