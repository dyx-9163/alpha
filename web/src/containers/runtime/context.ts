import { inject, type ComputedRef, type InjectionKey, type Ref } from 'vue'
import type { UploadFile } from 'element-plus'
import type {
  AifarRelease,
  AifarRuntimeDeployment,
  AifarRuntimeInstance,
  AifarRuntimeLogPod,
  AifarRuntimePod,
  AifarRuntimeResponse,
  AifarRuntimeService,
  RuntimeConfigFormValues,
  RuntimeConfigServiceRow,
  RuntimeConfigValues,
  RuntimeEntryRoute,
  RuntimeLogRow
} from './types'

export type RuntimeResourceTab = 'deployments' | 'releases' | 'services' | 'pods' | 'logs' | 'ingress'

export type KeyValueItem = {
  key?: string
  label: string
  value?: string | number | boolean | null
  status?: string
}

export type RuntimeServiceOption = {
  label: string
  value: string
}

export type RuntimeLogPodOption = {
  label: string
  value: string
  status?: string
}

export type RuntimeStatusKind = (status?: string) => string
export type RuntimeStatusLabel = (status?: string) => string
export type RuntimeAction = () => void | Promise<void>

export type AifarRuntimeContext = {
  t: (key: string, named?: Record<string, unknown>) => string
  loading: ComputedRef<boolean>
  aifarRuntime: Ref<AifarRuntimeResponse>
  aifarRuntimeStatusKind: RuntimeStatusKind
  aifarRuntimeStatusLabel: RuntimeStatusLabel
  selectedRuntimeInstanceId: Ref<string>
  aifarRuntimeInstances: ComputedRef<AifarRuntimeInstance[]>
  runtimeInstanceLabel: (instance: AifarRuntimeInstance) => string
  aifarRuntimeActionDisabledReason: ComputedRef<string>
  openRuntimeConfigDialog: RuntimeAction
  serviceInstallDisabledReason: ComputedRef<string>
  openServiceInstallDialog: RuntimeAction
  openAifarRuntimeBundleUpdate: RuntimeAction
  reconcileAifarRuntime: RuntimeAction
  runtimeRestartDisabledReason: ComputedRef<string>
  runtimeRestartSubmitting: Ref<boolean>
  restartAllAifarRuntime: RuntimeAction
  runtimeCleanupDisabledReason: ComputedRef<string>
  cleanupAifarRuntimeStale: RuntimeAction
  loadAifarRuntime: (force?: boolean, includePods?: boolean, includeStats?: boolean) => Promise<void>
  aifarRuntimeWarnings: ComputedRef<string[]>
  runtimeSummaryItems: ComputedRef<KeyValueItem[]>
  runtimeResourceTab: Ref<RuntimeResourceTab>
  selectedRuntimeDeployments: ComputedRef<AifarRuntimeDeployment[]>
  runtimeDeploymentReplicaText: (row: AifarRuntimeDeployment) => string
  openAifarRuntimeServiceUpdate: (row: AifarRuntimeService) => void
  runtimeServiceForDeployment: (row: AifarRuntimeDeployment) => AifarRuntimeService
  scaleOutAifarService: (service: string) => Promise<void>
  aifarRuntimeScaleInDisabledReason: (row: AifarRuntimeDeployment) => string
  scaleInAifarDeployment: (row: AifarRuntimeDeployment) => Promise<void>
  aifarRuntimeOfflineDisabledReason: (row: AifarRuntimeService) => string
  offlineAifarService: (row: AifarRuntimeService) => Promise<void>
  aifarReleases: Ref<AifarRelease[]>
  loadAifarReleases: (force?: boolean) => Promise<void>
  releaseKindLabel: (kind?: string) => string
  releaseStatusLabel: (status?: string) => string
  releaseServicesText: (row: AifarRelease) => string
  formatDate: (value?: string) => string
  releaseRollbackDisabledReason: (row: AifarRelease) => string
  rollbackAifarRelease: (row: AifarRelease) => Promise<void>
  selectedRuntimeServices: ComputedRef<AifarRuntimeService[]>
  runtimeEndpointText: (row: AifarRuntimeService) => string
  percentText: (value?: number) => string
  runtimePodServiceFilter: Ref<string>
  clearRuntimePodServiceFilter: () => void
  installedRuntimeServiceNamesList: ComputedRef<string[]>
  recoverAifarRuntimePods: RuntimeAction
  ensureRuntimePodsLoaded: (force?: boolean, includeStats?: boolean) => Promise<void>
  runtimePodsLoadedForCurrentScope: ComputedRef<boolean>
  selectedRuntimePods: ComputedRef<AifarRuntimePod[]>
  openRuntimePodLogs: (row: AifarRuntimePod) => void
  runtimeLogServiceFilter: Ref<string[]>
  clearRuntimeLogServiceFilter: () => void
  runtimeLogPodFilter: Ref<string[]>
  runtimeLogPodOptions: ComputedRef<RuntimeLogPodOption[]>
  runtimeLogLevelFilter: Ref<string[]>
  runtimeLogKeyword: Ref<string>
  runtimeLogTail: Ref<number>
  runtimeLogSelectionReady: ComputedRef<boolean>
  loadRuntimeLogs: (force?: boolean) => void
  runtimeLogsLoadedForCurrentScope: ComputedRef<boolean>
  toggleRuntimeLogPaused: () => void
  runtimeLogPaused: Ref<boolean>
  runtimeLogRows: Ref<RuntimeLogRow[]>
  runtimeLogErrorCount: ComputedRef<number>
  showRuntimeLogErrorsOnly: () => void
  runtimeLogPendingCount: ComputedRef<number>
  clearRuntimeLogView: () => void
  runtimeLogAutoScroll: Ref<boolean>
  runtimeLogWarnings: ComputedRef<string[]>
  runtimeLogGroups: ComputedRef<AifarRuntimeLogPod[]>
  runtimeLogStreamTagType: ComputedRef<string>
  runtimeLogStreamStatusLabel: ComputedRef<string>
  runtimeLogLastDataAt: Ref<string>
  filteredRuntimeLogRows: ComputedRef<RuntimeLogRow[]>
  runtimeLogDroppedRows: Ref<number>
  runtimeLogViewport: Ref<HTMLElement | null>
  handleRuntimeLogScroll: (event: Event) => void
  runtimeLogTopSpacer: ComputedRef<number>
  runtimeLogVirtualRows: ComputedRef<RuntimeLogRow[]>
  runtimeLogBottomSpacer: ComputedRef<number>
  runtimeEntryRoutes: ComputedRef<RuntimeEntryRoute[]>
  runtimeDiscoveryTarget: (row: AifarRuntimeService) => string
  runtimeNacosStatus: (row: AifarRuntimeService) => string
  aifarUpdateVisible: Ref<boolean>
  selectedAifarContainerLabel: ComputedRef<string>
  selectedAifarInstanceLabel: ComputedRef<string>
  aifarUpdateModeLabel: ComputedRef<string>
  aifarUpdateMode: Ref<'single' | 'bundle'>
  aifarUpdateService: Ref<string>
  aifarArtifactAccept: ComputedRef<string>
  handleAifarArtifactChange: (file: UploadFile) => void
  clearAifarArtifact: () => void
  aifarArtifactHint: ComputedRef<string>
  aifarUpdateSubmitting: Ref<boolean>
  submitAifarUpdate: () => Promise<void>
  serviceInstallVisible: Ref<boolean>
  missingRuntimeServiceOptions: ComputedRef<RuntimeServiceOption[]>
  serviceInstallSelection: Ref<string[]>
  serviceInstallSubmitting: Ref<boolean>
  submitAifarServiceInstall: () => Promise<void>
  runtimeConfigVisible: Ref<boolean>
  runtimeConfigMetaItems: ComputedRef<KeyValueItem[]>
  runtimeConfigForm: Ref<RuntimeConfigFormValues>
  runtimeConfigRows: Ref<RuntimeConfigServiceRow[]>
  runtimeConfigSubmitting: Ref<boolean>
  submitRuntimeConfig: () => Promise<void>
}

export const aifarRuntimeContextKey: InjectionKey<AifarRuntimeContext> = Symbol('AifarRuntimeContext')
export const aifarRuntimeDialogContextKey = aifarRuntimeContextKey

export function useAifarRuntimeContext() {
  const context = inject(aifarRuntimeContextKey)
  if (!context) {
    throw new Error('AIFAR runtime context is not provided')
  }
  return context
}

export const useAifarRuntimeDialogContext = useAifarRuntimeContext
