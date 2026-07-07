import type { Component } from 'vue'
import type { AppTargetMode, FrontendAppDefinition } from './types'

export interface ServerOption {
  id: string
  name: string
  host: string
  status?: string
  dockerHost?: string
}

export interface AppInstanceOption {
  id: string
  app: string
  version: string
  serverId?: string
  status?: string
  topology?: string
  metadata?: string
  createdAt?: string
}

export interface CredentialOption {
  id: string
  name: string
  kind: string
  username?: string
  endpoint?: string
  status?: string
  purpose?: string
}

export interface AppInstallDialogContext {
  servers: ServerOption[]
  instances: AppInstanceOption[]
  credentials?: CredentialOption[]
  defaultDeployDir?: string
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

export type AppInstallFieldType = 'text' | 'password' | 'number' | 'select' | 'switch' | 'server-disk-select'

export interface AppInstallFieldOption {
  label: string
  value: string | number | boolean
  disabled?: boolean
}

export type AppInstallFieldValue =
  | string
  | number
  | boolean
  | Array<string | number | boolean>
  | Record<string, string | string[]>
  | undefined
export type AppInstallFieldValues = Record<string, AppInstallFieldValue>

export interface AppInstallField {
  name: string
  label: string
  type: AppInstallFieldType
  placeholder?: string
  defaultValue?: unknown
  required?: boolean
  options?: AppInstallFieldOption[]
  optionsResolver?: AppInstallFieldOptionsResolver
  visibleWhen?: AppInstallFieldVisibility
  multiple?: boolean
  min?: number
  max?: number
  step?: number
  validate?: AppInstallFieldValidator
}

export interface AppInstallValidationContext {
  servers: ServerOption[]
  selectedServers: ServerOption[]
  targetMode: AppTargetMode
}

export type AppInstallFieldValidator = (
  value: unknown,
  values: AppInstallFieldValues,
  context: AppInstallValidationContext
) => string | undefined | null

export type AppInstallFieldOptionsResolver = (
  values: AppInstallFieldValues,
  context: AppInstallValidationContext
) => AppInstallFieldOption[]

export type AppInstallFieldVisibility = (
  values: AppInstallFieldValues,
  context: AppInstallValidationContext
) => boolean

export interface AppInstallDialogConfig {
  targetMode?: AppTargetMode
  targetServerFilter?: (server: ServerOption, context: AppInstallDialogContext) => boolean
  targetModeResolver?: (values: AppInstallFieldValues) => AppTargetMode
  hideTargetSelector?: boolean
  hideTargetSelectorResolver?: (values: AppInstallFieldValues) => boolean
  targetCountResolver?: (values: AppInstallFieldValues, context: AppInstallValidationContext) => number
  targetIdsResolver?: (values: AppInstallFieldValues, context: AppInstallValidationContext) => string[]
  targetValidationResolver?: (values: AppInstallFieldValues, context: AppInstallValidationContext) => string | undefined | null
  copy?: Partial<AppInstallDialogCopy>
  fields?: AppInstallField[]
}

export interface AppFrontendModule {
  name: string
  manifest(locale?: string): FrontendAppDefinition
  installDialog?: Component
  installDialogProps?(locale?: string, context?: AppInstallDialogContext): AppInstallDialogConfig
  deployDisabledReason?(locale: string | undefined, context: AppInstallDialogContext): string
  supportsMultiTarget?: boolean
}
