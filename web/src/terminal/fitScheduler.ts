interface TerminalFitSchedulerOptions {
  isVisible(): boolean
  fit(): void
  afterRender(callback: () => void): void
  requestFrame(callback: () => void): number
  cancelFrame(id: number): void
}

export function createTerminalFitScheduler(options: TerminalFitSchedulerOptions) {
  let disposed = false
  let renderPending = false
  let frameId: number | null = null

  function schedule() {
    if (disposed || renderPending || !options.isVisible()) return
    renderPending = true
    options.afterRender(() => {
      renderPending = false
      if (disposed || !options.isVisible()) return
      options.fit()
      if (frameId !== null) options.cancelFrame(frameId)
      frameId = options.requestFrame(() => {
        frameId = null
        if (!disposed && options.isVisible()) options.fit()
      })
    })
  }

  function dispose() {
    if (disposed) return
    disposed = true
    if (frameId !== null) {
      options.cancelFrame(frameId)
      frameId = null
    }
  }

  return { schedule, dispose }
}
