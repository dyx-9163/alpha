import { getCurrentLocale, resolveLocale } from '../../i18n'

export type AppCategory = 'database' | 'devops' | 'storage'
export type AppTargetMode = 'single' | 'multiple'
export type AppLocale = 'zh' | 'en'

export interface AppTopologyDefinition {
  name: string
  label: string
  targetMode: AppTargetMode
  minTargets?: number
  default?: boolean
}

export interface FrontendAppDefinition {
  name: string
  title: string
  icon: string
  category: AppCategory
  categoryLabel: string
  sourceLabel: string
  description: string
  frontendReady: true
  topologies?: AppTopologyDefinition[]
}

export interface BackendCatalogItem {
  name: string
  title?: string
  icon?: string
  category?: AppCategory
  categoryLabel?: string
  sourceLabel?: string
  fallbackVersion?: string
  description?: string
  installName?: string
  resourceApp?: string
  requiresServer?: boolean
  backendReady?: boolean
  frontendReady?: boolean
  requiredResourceParts?: string[]
  topologies?: AppTopologyDefinition[]
  versions?: string[]
  resources?: Array<{ id: string; app: string; part?: string; version: string; path: string; size: number }>
  parts?: Record<string, boolean>
  deployable?: boolean
  missing?: string[]
}

export type AppCatalogResponse = Record<string, BackendCatalogItem> | BackendCatalogItem[]

export type AppStoreItem = FrontendAppDefinition & {
  fallbackVersion: string
  installName: string
  resourceApp: string
  requiresServer: boolean
  backendReady: boolean
  versions: string[]
  resources: NonNullable<BackendCatalogItem['resources']>
  parts: Record<string, boolean>
  deployable: boolean
  missing: string[]
  topologies: AppTopologyDefinition[]
}

export function resolveAppLocale(locale?: string): AppLocale {
  return resolveLocale(locale || getCurrentLocale())
}
