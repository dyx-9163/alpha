export type ContainerResourceTab = 'images' | 'networks' | 'volumes' | 'registry' | 'settings'
export type ContainerTab = 'overview' | 'aifar-runtime' | 'images'
export type RuntimeCacheScope = 'base' | 'pods'

export function containerCacheScope(serverId: string) {
  return serverId || 'none'
}

export function summaryCacheKey(scope: string, includeDisk: boolean) {
  return `${scope}:summary:${includeDisk ? 'disk' : 'base'}`
}

export function activeCollectionKind(tab: ContainerTab, resourceTab: ContainerResourceTab) {
  if (tab === 'images') {
    return resourceTab
  }
  return ''
}

export function collectionBackedKind(kind: string) {
  return kind === 'images' || kind === 'networks' || kind === 'volumes'
}

export function collectionCacheKey(scope: string, kind: string) {
  return `${scope}:collection:${kind}`
}

export function runtimeCacheKey(scope: string, runtimeScope: RuntimeCacheScope = 'base') {
  return `${scope}:aifar-runtime:${runtimeScope}`
}

export function runtimeLogCacheKey(
  scope: string,
  instanceId: string | undefined,
  services: string[],
  pods: string[],
  tail: number,
  sinceSeconds: number
) {
  return `${scope}:aifar-runtime:logs:${instanceId || 'none'}:${services.join(',')}:${pods.join(',')}:${tail}:${sinceSeconds}`
}
