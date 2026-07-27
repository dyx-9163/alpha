import { apiDelete, apiDownload, apiEventSourceUrl, apiGet, apiPost, apiPostForm, apiPut } from '../../api/client'
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

export function updateAifarArtifact(instanceId: string, form: FormData, mode: 'single' | 'bundle') {
  const endpoint = mode === 'bundle'
    ? `/apps/instances/${instanceId}/aifar/update-artifact-bundle`
    : `/apps/instances/${instanceId}/aifar/update-artifact`
  return apiPostForm<RuntimeTaskResponse>(endpoint, form)
}

export function applyRuntimeConfig(query: string, payload: RuntimeConfigPayload) {
  return apiPut<RuntimeTaskResponse>(`/containers/aifar/runtime/config?${query}`, payload)
}

export function reconcileRuntime(query: string, instanceId: string) {
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/runtime/reconcile?${query}`, { instanceId })
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

export function scaleOutRuntimeService(query: string, service: string, instanceId: string) {
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/services/${encodeURIComponent(service)}/scale-out?${query}`, { instanceId })
}

export function scaleInRuntimeService(query: string, service: string, instanceId: string) {
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/services/${encodeURIComponent(service)}/scale-in?${query}`, { instanceId })
}

export function offlineRuntimeService(query: string, service: string, instanceId: string) {
  return apiPost<RuntimeTaskResponse>(`/containers/aifar/services/${encodeURIComponent(service)}/offline?${query}`, { instanceId })
}
