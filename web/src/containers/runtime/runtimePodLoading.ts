export type RuntimePodLoadTrigger = 'enter' | 'scope-change' | 'refresh' | 'metrics' | 'status-event' | 'logs'

export function runtimePodLoadArgs(trigger: RuntimePodLoadTrigger): [force: boolean, includeStats: boolean, background: boolean] {
  switch (trigger) {
    case 'metrics':
      return [true, true, true]
    case 'refresh':
      return [true, false, false]
    case 'enter':
    case 'scope-change':
      return [false, false, false]
    case 'status-event':
      return [true, false, true]
    case 'logs':
      return [false, false, true]
  }
}
