export type RuntimePodLoadTrigger = 'enter' | 'scope-change' | 'refresh' | 'status-event' | 'logs'

export function runtimePodLoadArgs(trigger: RuntimePodLoadTrigger): [force: boolean, includeStats: boolean] {
  switch (trigger) {
    case 'refresh':
    case 'status-event':
      return [true, true]
    case 'enter':
    case 'scope-change':
      return [false, true]
    case 'logs':
      return [false, false]
  }
}
