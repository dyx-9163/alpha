import { frontendAppCatalog } from './loader'
import { canPairApp } from './validation'
import type { AppCatalogResponse, AppStoreItem, BackendCatalogItem } from './types'

export function normalizeBackendCatalog(payload: AppCatalogResponse): Record<string, BackendCatalogItem> {
  if (Array.isArray(payload)) {
    return Object.fromEntries(payload.map((item) => [item.name, item]))
  }
  return payload ?? {}
}

export function pairedAppCatalog(payload: AppCatalogResponse, locale?: string): AppStoreItem[] {
  const backend = normalizeBackendCatalog(payload)
  const paired: AppStoreItem[] = []
  for (const frontend of frontendAppCatalog(locale)) {
    const server = backend[frontend.name]
    if (!canPairApp(frontend, server)) {
      continue
    }
    paired.push({
      ...frontend,
      title: frontend.title || server.title || frontend.name,
      icon: frontend.icon || server.icon || frontend.name.slice(0, 2).toUpperCase(),
      category: frontend.category || server.category || 'database',
      categoryLabel: frontend.categoryLabel || server.categoryLabel || frontend.category,
      sourceLabel: frontend.sourceLabel || server.sourceLabel || 'Built-in',
      fallbackVersion: frontend.fallbackVersion || server.fallbackVersion || 'latest',
      description: frontend.description || server.description || '',
      installName: server.installName || frontend.name,
      resourceApp: server.resourceApp || frontend.name,
      requiresServer: server.requiresServer ?? true,
      backendReady: Boolean(server.backendReady),
      versions: server.versions ?? [],
      resources: server.resources ?? [],
      parts: server.parts ?? {},
      deployable: Boolean(server.deployable),
      missing: server.missing ?? [],
      topologies: frontend.topologies?.length ? frontend.topologies : server.topologies ?? []
    })
  }
  return paired
}

export type { AppCatalogResponse, AppStoreItem }
