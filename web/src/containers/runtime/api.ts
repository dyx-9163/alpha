import { apiDelete, apiDownload, apiEventSourceUrl, apiGet, apiPost, apiPostForm, apiPut, type ApiError } from '../../api/client'
import type {
  AifarReleaseListResponse,
  AifarRuntimeResponse,
  RuntimeConfigValues,
  RuntimeDiagnosticEstimate,
  RuntimeDiagnosticExportPage,
  RuntimeDiagnosticRequest
} from './types'

export type RuntimeTaskResponse = {
  taskId: string
}

export type LoadRuntimeOptions = {
  includePods?: boolean
  includeStats?: boolean
}

export type RuntimeConfigPayload = {
  instanceId: string
  global: Required<RuntimeConfigValues>
  services: Record<string, RuntimeConfigValues>
  nacosEphemeral: boolean
}

export type RuntimeRollbackPayload = {
  targetReleaseId: string
  services: string[]
  reason: string
}

export type RuntimeServiceInstallPayload = {
  instanceId: string
  services: string[]
}

export type RuntimeDeploymentMutationPayload = {
  operation: 'apply' | 'scale' | 'offline' | 'restart'
  expectedGeneration: number
  replicas?: number
  restart?: boolean
  reason?: string
}

export type RuntimeDeploymentReconcilePayload = {
  expectedGeneration: number
  reason?: string
}

export function fetchAifarRuntime(query: string, options: LoadRuntimeOptions = {}) {
  const includePods = options.includePods ? 1 : 0
  const includeStats = options.includeStats ? 1 : 0
  return apiGet<AifarRuntimeResponse>(`/containers/aifar/runtime?${query}&includePods=${includePods}&includeStats=${includeStats}`)
}

export function fetchAifarReleases(instanceId: string) {
  return apiGet<AifarReleaseListResponse>(`/apps/instances/${instanceId}/aifar/releases`)
}

export function deleteAifarRelease(instanceId: string, releaseId: string) {
  return apiDelete<{ releaseId: string }>(`/apps/instances/${encodeURIComponent(instanceId)}/aifar/releases/${encodeURIComponent(releaseId)}`)
}

export function estimateRuntimeDiagnostics(query: string, payload: RuntimeDiagnosticRequest): Promise<RuntimeDiagnosticEstimate> {
  return apiPost<RuntimeDiagnosticEstimate>(`/containers/aifar/runtime/diagnostics/estimate?${runtimeDiagnosticQuery(query)}`, payload)
}

export function createRuntimeDiagnosticExport(query: string, payload: RuntimeDiagnosticRequest): Promise<{ taskId: string; exportId: string; status: string }> {
  return apiPost<{ taskId: string; exportId: string; status: string }>(
    `/containers/aifar/runtime/diagnostics/exports?${runtimeDiagnosticQuery(query)}`,
    payload
  )
}

export function fetchRuntimeDiagnosticExports(query: string, instanceId: string, page = 1, pageSize = 20): Promise<RuntimeDiagnosticExportPage> {
  return apiGet<RuntimeDiagnosticExportPage>(`/containers/aifar/runtime/diagnostics/exports?${runtimeDiagnosticQuery(query, {
    instanceId,
    page: String(page),
    pageSize: String(pageSize)
  })}`)
}

export function downloadRuntimeDiagnosticExport(query: string, exportId: string, deleteAfterDownload = false): ReturnType<typeof apiDownload> {
  return apiDownload(`/containers/aifar/runtime/diagnostics/exports/${encodeURIComponent(exportId)}/download?${runtimeDiagnosticQuery(query, {
    deleteAfterDownload: String(deleteAfterDownload)
  })}`)
}

export function deleteRuntimeDiagnosticExport(query: string, exportId: string): Promise<RuntimeTaskResponse> {
  return apiDelete<RuntimeTaskResponse>(
    `/containers/aifar/runtime/diagnostics/exports/${encodeURIComponent(exportId)}?${runtimeDiagnosticQuery(query)}`
  )
}

function runtimeDiagnosticQuery(query: string, values: Record<string, string> = {}) {
  const params = new URLSearchParams(query)
  for (const [key, value] of Object.entries(values)) {
    params.set(key, value)
  }
  return params.toString()
}

export function createRuntimeLogEventSource(params: URLSearchParams) {
  return new EventSource(apiEventSourceUrl(`/containers/aifar/runtime/logs/events?${params.toString()}`))
}

export function rollbackAifarRelease(instanceId: string, payload: RuntimeRollbackPayload) {
  return apiPost<RuntimeTaskResponse>(`/apps/instances/${instanceId}/aifar/rollback`, payload)
}

export function updateAifarArtifact(instanceId: string, form: FormData, mode: 'single' | 'bundle', expectedGeneration?: number) {
  const endpoint = mode === 'bundle'
    ? `/apps/instances/${instanceId}/aifar/update-artifact-bundle`
    : `/apps/instances/${instanceId}/aifar/update-artifact`
  if (mode === 'single' && Number.isFinite(expectedGeneration) && Number(expectedGeneration) > 0) {
    form.set('expectedGeneration', String(expectedGeneration))
  }
  return apiPostForm<RuntimeTaskResponse>(endpoint, form)
}

export function applyRuntimeConfig(query: string, payload: RuntimeConfigPayload) {
  return apiPut<RuntimeTaskResponse>(`/containers/aifar/runtime/config?${query}`, payload)
}

export function mutateRuntimeDeployment(
  query: string,
  instanceId: string,
  service: string,
  payload: RuntimeDeploymentMutationPayload
) {
  return apiPut<RuntimeTaskResponse>(runtimeDeploymentEndpoint(query, instanceId, service), payload)
}

export function reconcileRuntimeDeployment(
  query: string,
  instanceId: string,
  service: string,
  payload: RuntimeDeploymentReconcilePayload
) {
  return apiPost<RuntimeTaskResponse>(runtimeDeploymentEndpoint(query, instanceId, service, '/reconcile'), payload)
}

export function runtimeLockOwnerTaskId(error: unknown) {
  const apiError = error as ApiError
  if (apiError?.status !== 409 || !apiError.details || typeof apiError.details !== 'object') return ''
  const ownerTaskId = (apiError.details as { ownerTaskId?: unknown }).ownerTaskId
  return typeof ownerTaskId === 'string' ? ownerTaskId.trim() : ''
}

export function restartAllRuntime(query: string, instanceId: string, reason = '') {
  const payload: { instanceId: string; reason?: string } = { instanceId }
  const normalizedReason = reason.trim()
  if (normalizedReason) {
    payload.reason = normalizedReason
  }
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/runtime/restart-all?${query}`, payload)
}

export function cleanupStaleRuntime(query: string, instanceId: string) {
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/runtime/cleanup-stale?${query}`, { instanceId })
}

export function installRuntimeServices(query: string, payload: RuntimeServiceInstallPayload) {
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/services/install?${query}`, payload)
}

function runtimeDeploymentEndpoint(query: string, instanceId: string, service: string, action = '') {
  const suffix = query ? `?${query}` : ''
  return `/apps/instances/${encodeURIComponent(instanceId)}/runtime/deployments/${encodeURIComponent(service)}${action}${suffix}`
}
