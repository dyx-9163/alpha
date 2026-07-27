export type RuntimeOverflowCommand = 'install' | 'reconcile' | 'cleanup'

export type RuntimeOverflowActions = Record<RuntimeOverflowCommand, () => void | Promise<void>>
export type RuntimeOverflowDisabledReasons = Partial<Record<RuntimeOverflowCommand, string>>

export function dispatchRuntimeOverflowCommand(
  command: RuntimeOverflowCommand,
  actions: RuntimeOverflowActions,
  disabledReasons: RuntimeOverflowDisabledReasons = {}
) {
  if (disabledReasons[command]) return
  return actions[command]()
}

export function runtimeOverflowReasonId(command: RuntimeOverflowCommand) {
  return `runtime-overflow-${command}-reason`
}

export function syncRuntimeOverflowMenuItemState(
  trigger: Pick<HTMLElement, 'closest'> | null,
  disabled: boolean,
  descriptionId: string
) {
  const menuItem = trigger?.closest<HTMLElement>('[role="menuitem"]')
  if (!menuItem) return

  menuItem.setAttribute('aria-disabled', String(disabled))
  if (disabled) {
    menuItem.setAttribute('aria-describedby', descriptionId)
  } else {
    menuItem.removeAttribute('aria-describedby')
  }
}
