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
    case 'available':
    case 'active':
    case 'success':
      return 'running'
    case 'starting':
    case 'rolling':
    case 'pending':
    case 'progressing':
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

export function releaseActivatedAtText(row: AifarRelease) {
  if (row.status !== 'success') return '-'
  return formatDate(row.activatedAt)
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

export function runtimeGenerationText(row: AifarRuntimeDeployment) {
  return `${generationValue(row.generation, 1)} / ${generationValue(row.observedGeneration, 0)}`
}

export function runtimeConditionReason(row: AifarRuntimeDeployment, t?: RuntimeTranslate) {
  const conditions = Array.isArray(row.conditions) ? row.conditions : []
  const priority = ['Degraded', 'Progressing', 'Offline', 'Available', 'Accepted']
  const active = [...conditions]
    .filter((condition) => condition?.status === true)
    .sort((left, right) => priority.indexOf(left.type) - priority.indexOf(right.type))[0]
  if (!active) return null
  const reason = active.reason || active.type
  return {
    type: active.type,
    reason,
    message: active.message || '',
    lastTransitionTime: active.lastTransitionTime || row.lastTransitionAt || '',
    ...(t ? { advice: runtimeConditionAdvice(reason, active.type, t) } : {})
  }
}

export function runtimeConditionAdvice(reason: string | undefined, type: string | undefined, t: RuntimeTranslate) {
  const bucket = runtimeConditionAdviceBucket(reason, type)
  const groupKey = `containers.runtimeConditionGroup.${bucket}`
  const suggestionKey = `containers.runtimeConditionAdvice.${bucket}`
  return {
    group: t(groupKey),
    suggestion: t(suggestionKey),
    groupKey,
    suggestionKey
  }
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

function runtimeConditionAdviceBucket(reason?: string, type?: string) {
  const value = `${reason || ''} ${type || ''}`.toLowerCase()
  if (value.includes('agent') || value.includes('manifest') || value.includes('filesystem') || value.includes('instance') || value.includes('specrejected')) {
    return 'agent'
  }
  if (value.includes('artifact') || value.includes('image') || value.includes('revision') || value.includes('hash') || value.includes('pull')) {
    return 'artifact'
  }
  if (value.includes('offline') || value.includes('stopped') || value.includes('desiredreplicaszero')) {
    return 'offline'
  }
  if (value.includes('progress') || value.includes('accepted') || value.includes('rolling')) {
    return 'progressing'
  }
  if (value.includes('readiness') || value.includes('endpoint') || value.includes('health') || value.includes('probe') || value.includes('container') || value.includes('docker')) {
    return 'service'
  }
  return 'unknown'
}

function arrayValue<T>(value?: T[]) {
  return Array.isArray(value) ? value : []
}

function generationValue(value: number | undefined, minimum: number) {
  if (value === undefined || value === null) return '-'
  const generation = Number(value)
  return Number.isFinite(generation) && generation >= minimum ? generation : '-'
}
