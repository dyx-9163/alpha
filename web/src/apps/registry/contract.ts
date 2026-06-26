import type { Component } from 'vue'
import type { FrontendAppDefinition } from './types'
import type { AppTargetMode } from './model'

export interface ServerOption {
  id: string
  name: string
  host: string
}

export interface AppInstallPayload {
  version: string
  serverId?: string
  serverIds?: string[]
  [key: string]: unknown
}

export interface AppInstallDialogCopy {
  title: string
  hint?: string
  versionLabel: string
  versionPlaceholder: string
  serversLabel: string
  serversPlaceholder: string
  noServers: string
  selectedCount(count: number): string
  cancel: string
  submit: string
}

export type AppInstallFieldType = 'text' | 'password' | 'number' | 'select' | 'switch'

export interface AppInstallFieldOption {
  label: string
  value: string | number | boolean
}

export interface AppInstallField {
  name: string
  label: string
  type: AppInstallFieldType
  placeholder?: string
  defaultValue?: unknown
  required?: boolean
  options?: AppInstallFieldOption[]
}

export interface AppInstallDialogConfig {
  targetMode?: AppTargetMode
  targetModeResolver?: (values: Record<string, unknown>) => AppTargetMode
  copy?: Partial<AppInstallDialogCopy>
  fields?: AppInstallField[]
}

export interface AppFrontendModule {
  name: string
  manifest(locale?: string): FrontendAppDefinition
  installDialog?: Component
  installDialogProps?(locale?: string): AppInstallDialogConfig
  supportsMultiTarget?: boolean
}
