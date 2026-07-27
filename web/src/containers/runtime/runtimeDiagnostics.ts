import type { AifarRuntimeDeployment, RuntimeDiagnosticEstimate, RuntimeDiagnosticExport, RuntimeDiagnosticExportPage, RuntimeDiagnosticRequest } from './types'

export type RuntimeDiagnosticSubmitState = {
  services: string[]
  estimate: Pick<RuntimeDiagnosticEstimate, 'allowed'> | null
  estimating: boolean
  submitting: boolean
}

export function defaultRuntimeDiagnosticWindow(now = new Date()) {
  return {
    sinceAt: new Date(now.getTime() - 2 * 60 * 60 * 1000),
    untilAt: now
  }
}

export function enabledRuntimeDiagnosticServices(deployments: AifarRuntimeDeployment[]) {
  return [...new Set(deployments
    .filter((deployment) => Number(deployment.desiredReplicas ?? 0) > 0)
    .map((deployment) => deployment.serviceName)
    .filter(Boolean))]
}

export function runtimeDiagnosticSubmitDisabledReason(input: RuntimeDiagnosticSubmitState): '' | 'services-required' | 'estimate-required' | 'estimate-blocked' | 'busy' {
  if (input.estimating || input.submitting) return 'busy'
  if (!input.services.length) return 'services-required'
  if (!input.estimate) return 'estimate-required'
  return input.estimate.allowed ? '' : 'estimate-blocked'
}

export function runtimeDiagnosticLimitRows(estimate: RuntimeDiagnosticEstimate) {
  return [
    { key: 'file' as const, value: estimate.maxFileScanBytes },
    { key: 'scan' as const, value: estimate.maxTotalScanBytes },
    { key: 'filtered' as const, value: estimate.maxFilteredBytes },
    { key: 'archive' as const, value: estimate.maxArchiveBytes }
  ]
}

export function runtimeDiagnosticCapacityBlocked(estimate: RuntimeDiagnosticEstimate) {
  return !estimate.allowed && [
    'local-quota-exceeded',
    'local-disk-insufficient',
    'scan-limit-exceeded',
    'file-scan-limit-exceeded',
    'total-scan-limit-exceeded'
  ].includes(estimate.blockReason || '')
}

export function runtimeDiagnosticRequestFingerprint(query: string, request: RuntimeDiagnosticRequest) {
  return JSON.stringify({
    query,
    instanceId: request.instanceId,
    sinceAt: request.sinceAt,
    untilAt: request.untilAt,
    services: [...request.services].sort()
  })
}

export function runtimeDiagnosticExportScopeFingerprint(query: string, instanceId: string) {
  return `${query}\u0000${instanceId}`
}

export function emptyRuntimeDiagnosticExportPage(): RuntimeDiagnosticExportPage {
  return { items: [], total: 0, page: 1, pageSize: 20 }
}

export function trackRuntimeDiagnosticTask(
  tracker: { track: (taskId: string, label?: string, options?: { polling?: boolean }) => void },
  taskId: string,
  label: string
) {
  tracker.track(taskId, label, { polling: false })
}

export function terminalDiagnosticTaskToRefresh(items: Array<{ id: string; status: string }>, tracked: Set<string>, refreshed: Set<string>) {
  return items
    .filter((item) => tracked.has(item.id) && !refreshed.has(item.id) && ['success', 'failed', 'cancelled'].includes(item.status))
    .map((item) => item.id)
}

export function runtimeDiagnosticStatusKey(row: RuntimeDiagnosticExport) {
  if (row.status === 'ready') return row.warningCount > 0 ? 'containers.diagnosticsReadyWithWarnings' : 'containers.diagnosticsReady'
  if (row.status === 'pending' || row.status === 'building') return 'containers.diagnosticsBuilding'
  if (row.status === 'failed') return 'containers.diagnosticsFailed'
  if (row.status === 'cancelled') return 'containers.diagnosticsCancelled'
  if (row.status === 'expired') return 'containers.diagnosticsExpiredCleanupPending'
  if (row.status === 'deleted') return 'containers.diagnosticsDeleted'
  return 'containers.diagnosticsBuilding'
}
