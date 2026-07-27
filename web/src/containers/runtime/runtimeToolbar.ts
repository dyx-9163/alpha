export type RuntimeOverflowCommand = 'install' | 'reconcile' | 'cleanup'

export type RuntimeOverflowActions = Record<RuntimeOverflowCommand, () => void | Promise<void>>

export function dispatchRuntimeOverflowCommand(command: RuntimeOverflowCommand, actions: RuntimeOverflowActions) {
  return actions[command]()
}

export function runtimeOverflowReasonId(command: RuntimeOverflowCommand) {
  return `runtime-overflow-${command}-reason`
}
