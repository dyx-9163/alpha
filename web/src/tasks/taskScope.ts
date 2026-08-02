type TaskScopeItem = {
  type: string
  target: string
}

export function filterTasksByScope<T extends TaskScopeItem>(tasks: readonly T[], typePrefix = '', target = ''): T[] {
  const normalizedTypePrefix = typePrefix.trim()
  const normalizedTarget = target.trim()
  return tasks.filter((task) => {
    const typeMatches = !normalizedTypePrefix || task.type.startsWith(normalizedTypePrefix)
    const targetMatches = !normalizedTarget || task.target === normalizedTarget
    return typeMatches && targetMatches
  })
}
