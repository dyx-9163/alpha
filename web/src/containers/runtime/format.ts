import type {
  AifarRelease,
  AifarRuntimeDeployment,
  AifarRuntimeInstance,
  AifarRuntimeService
} from './types'

export type RuntimeTranslate = (key: string, named?: Record<string, unknown>) => string

export function aifarRuntimeStatusKind(status?: string) {
  switch (String(status || '').trim()) {
    case 'ready':
    case 'running':
    case 'active':
    case 'success':
      return 'running'
    case 'starting':
    case 'rolling':
    case 'pending':
      return 'pending'
    case 'degraded':
    case 'draining':
    case 'stale':
    case 'offline':
      return 'degraded'
    case 'missing':
    case 'unsupported':
    case 'failed':
    case 'unhealthy':
    case 'no-endpoints':
      return 'failed'
    default:
      return 'unknown'
  }
}

export function aifarRuntimeStatusLabel(status: string | undefined, t: RuntimeTranslate) {
  return translatedStatusLabel(`containers.runtimeStatus.${normalizeStatus(status)}`, status, t)
}

export function releaseKindLabel(kind: string | undefined, t: RuntimeTranslate) {
  return translatedStatusLabel(`containers.releaseKind.${normalizeStatus(kind)}`, kind, t)
}

export function releaseStatusLabel(status: string | undefined, t: RuntimeTranslate) {
  return translatedStatusLabel(`containers.releaseStatus.${normalizeStatus(status)}`, status, t)
}

export function runtimeApplyStatusLabel(status: string | undefined, t: RuntimeTranslate) {
  return translatedStatusLabel(`containers.runtimeApplyStatus.${normalizeStatus(status)}`, status, t)
}

export function releaseServicesText(row: AifarRelease) {
  return arrayValue(row.changedServices).join(', ') || '-'
}

export function formatDate(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return value
  return date.toLocaleString()
}

export function runtimeEndpointText(row: AifarRuntimeService) {
  const ready = Number(row.readyEndpointCount ?? row.activeEndpoints ?? row.readyReplicas ?? 0)
  const total = Number(row.endpointCount ?? row.activeEndpoints ?? ready)
  return `${Number.isFinite(ready) ? ready : 0} / ${Number.isFinite(total) ? total : 0}`
}

export function runtimeDeploymentReplicaText(row: AifarRuntimeDeployment) {
  const ready = Number(row.readyReplicas ?? row.availableReplicas ?? 0)
  const desired = Number(row.desiredReplicas ?? 0)
  const updated = Number(row.updatedReplicas ?? 0)
  const base = `${Number.isFinite(ready) ? ready : 0} / ${Number.isFinite(desired) ? desired : 0}`
  if (updated > 0 && updated !== ready) {
    return `${base} (${updated})`
  }
  return base
}

export function runtimeNacosStatus(row: AifarRuntimeService) {
  if (row.nacosReady) {
    return 'ready'
  }
  if (row.lastNacosError) {
    return 'failed'
  }
  if (row.nacosRegistered) {
    return 'running'
  }
  if (row.status === 'offline') {
    return 'offline'
  }
  return 'unknown'
}

export function percentText(value?: number) {
  const n = Number(value || 0)
  if (!Number.isFinite(n) || n <= 0) {
    return '-'
  }
  return `${n.toFixed(1)}%`
}

export function runtimeInstanceLabel(instance: AifarRuntimeInstance, t: RuntimeTranslate) {
  const model = instance.orchestrationModel || t('common.unknown')
  const root = instance.installRoot || instance.id
  return `${instance.version || 'aifar'} / ${model} / ${root}`
}

function translatedStatusLabel(key: string, rawValue: string | undefined, t: RuntimeTranslate) {
  const value = t(key)
  return value === key ? String(rawValue || t('common.unknown')) : value
}

function normalizeStatus(value?: string) {
  return String(value || 'unknown').trim() || 'unknown'
}

function arrayValue<T>(value?: T[]) {
  return Array.isArray(value) ? value : []
}
