import { apiGet, apiPost } from '../api/client'

export type MinioStorageInsight = {
  totalBytes: number
  usedBytes: number
  availableBytes: number
  usagePercent: number
  pathCount: number
}

export type MinioStorageDisk = {
  index: number
  path: string
  device: string
  mountPoint: string
  totalBytes: number
  usedBytes: number
  availableBytes: number
  usagePercent: number
}

export type MinioCleanupEstimate = {
  status: string
  retentionDays: number
  objectCount: number
  bytes: number
  source: string
}

export type MinioCleanupPolicy = {
  enabled: boolean
  status: string
  bucket: string
  prefix: string
  retentionDays: number
  ruleId: string
  source: string
  updatedAt: string
}

export type MinioCleanupPolicyPayload = {
  enabled: boolean
  bucket: string
  prefix?: string
  retentionDays: number
}

export type MinioInstallDiskSummary = {
  nodeCount: number
  pathCount: number
  uniform: boolean
  aggregateTotalBytes: number
  aggregateUsedBytes: number
  aggregateAvailableBytes: number
  totalBytes: number
  usedBytes: number
  availableBytes: number
  minTotalBytes: number
  maxTotalBytes: number
  minUsedBytes: number
  maxUsedBytes: number
  minAvailableBytes: number
  maxAvailableBytes: number
}

export function minioStorageInsightFromMetadata(metadata: Record<string, unknown>): MinioStorageInsight | null {
  const source = nestedDetails(metadata)
  const totalBytes = numericValue(source.minioStorageTotalBytes)
  if (totalBytes <= 0) {
    return null
  }
  return {
    totalBytes,
    usedBytes: numericValue(source.minioStorageUsedBytes),
    availableBytes: numericValue(source.minioStorageAvailableBytes),
    usagePercent: numericValue(source.minioStorageUsagePercent),
    pathCount: numericValue(source.minioStoragePathCount)
  }
}

export function minioStorageDisksFromMetadata(metadata: Record<string, unknown>): MinioStorageDisk[] {
  const source = nestedDetails(metadata)
  const disks = Array.isArray(source.minioStorageDisks) ? source.minioStorageDisks : []
  return disks
    .map((item, index) => diskFromRecord(objectRecord(item), index + 1))
    .filter((item): item is MinioStorageDisk => Boolean(item))
}

export function minioCleanupEstimateFromMetadata(metadata: Record<string, unknown>): MinioCleanupEstimate | null {
  const source = nestedDetails(metadata)
  const status = stringValue(source.cleanupEstimateStatus)
  const retentionDays = numericValue(source.cleanupEstimateRetentionDays)
  if (!status && retentionDays <= 0) {
    return null
  }
  return {
    status: status || 'unknown',
    retentionDays,
    objectCount: numericValue(source.cleanupEstimateObjectCount),
    bytes: numericValue(source.cleanupEstimateBytes),
    source: stringValue(source.cleanupEstimateSource)
  }
}

export function minioCleanupPolicyFromMetadata(metadata: Record<string, unknown>): MinioCleanupPolicy | null {
  const policy = objectRecord(metadata.cleanupPolicy)
  const bucket = stringValue(policy.bucket)
  if (!bucket) {
    return null
  }
  return {
    enabled: booleanValue(policy.enabled),
    status: stringValue(policy.status) || (booleanValue(policy.enabled) ? 'enabled' : 'disabled'),
    bucket,
    prefix: stringValue(policy.prefix),
    retentionDays: numericValue(policy.retentionDays),
    ruleId: stringValue(policy.ruleId),
    source: stringValue(policy.source),
    updatedAt: stringValue(policy.updatedAt)
  }
}

export function cleanupEstimateText(estimate: MinioCleanupEstimate | null) {
  if (!estimate) {
    return '-'
  }
  if (estimate.status && estimate.status !== 'available') {
    return estimate.status
  }
  return `${formatBytes(estimate.bytes)} / ${estimate.objectCount} objects`
}

export function summarizeMinioInstallDisks(insights: Array<MinioStorageInsight | null | undefined>): MinioInstallDiskSummary | null {
  const rows = insights.filter((item): item is MinioStorageInsight => Boolean(item && item.totalBytes > 0))
  if (!rows.length) {
    return null
  }
  const first = rows[0]
  const totals = rows.map((item) => item.totalBytes)
  const used = rows.map((item) => item.usedBytes)
  const available = rows.map((item) => item.availableBytes)
  const uniform = rows.every((item) =>
    item.totalBytes === first.totalBytes &&
    item.usedBytes === first.usedBytes &&
    item.availableBytes === first.availableBytes
  )
  return {
    nodeCount: rows.length,
    pathCount: rows.reduce((sum, item) => sum + item.pathCount, 0),
    uniform,
    aggregateTotalBytes: totals.reduce((sum, item) => sum + item, 0),
    aggregateUsedBytes: used.reduce((sum, item) => sum + item, 0),
    aggregateAvailableBytes: available.reduce((sum, item) => sum + item, 0),
    totalBytes: first.totalBytes,
    usedBytes: first.usedBytes,
    availableBytes: first.availableBytes,
    minTotalBytes: Math.min(...totals),
    maxTotalBytes: Math.max(...totals),
    minUsedBytes: Math.min(...used),
    maxUsedBytes: Math.max(...used),
    minAvailableBytes: Math.min(...available),
    maxAvailableBytes: Math.max(...available)
  }
}

export function formatMinioUsedAvailable(insight: MinioStorageInsight) {
  return `${formatBytes(insight.usedBytes)} / ${formatBytes(insight.availableBytes)}`
}

export function formatBytes(value: unknown) {
  let bytes = numericValue(value)
  if (bytes <= 0) {
    return '0 B'
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB', 'PiB']
  let index = 0
  while (bytes >= 1024 && index < units.length - 1) {
    bytes /= 1024
    index += 1
  }
  const text = bytes >= 10 || Number.isInteger(bytes) ? bytes.toFixed(0) : bytes.toFixed(1)
  return `${text} ${units[index]}`
}

export function fetchMinioCleanupEstimate(instanceId: string, retentionDays: number) {
  const days = Math.max(1, Math.min(3650, Math.floor(numericValue(retentionDays) || 30)))
  return apiGet<MinioCleanupEstimate>(`/storage/${encodeURIComponent(instanceId)}/cleanup-estimate?retentionDays=${days}`)
}

export function applyMinioCleanupPolicy(instanceId: string, payload: MinioCleanupPolicyPayload) {
  const days = Math.max(1, Math.min(3650, Math.floor(numericValue(payload.retentionDays) || 30)))
  return apiPost<{ taskId: string }>(`/storage/${encodeURIComponent(instanceId)}/cleanup-policy`, {
    enabled: Boolean(payload.enabled),
    bucket: stringValue(payload.bucket) || 'aifar',
    prefix: stringValue(payload.prefix),
    retentionDays: days
  })
}

function nestedDetails(metadata: Record<string, unknown>) {
  const lastCheck = objectRecord(metadata.lastCheck)
  const details = objectRecord(lastCheck.details)
  return {
    ...metadata,
    ...lastCheck,
    ...details
  }
}

function objectRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function diskFromRecord(item: Record<string, unknown>, fallbackIndex: number): MinioStorageDisk | null {
  const totalBytes = numericValue(item.totalBytes)
  const path = stringValue(item.path)
  if (!path || totalBytes <= 0) {
    return null
  }
  return {
    index: numericValue(item.index) || fallbackIndex,
    path,
    device: stringValue(item.device),
    mountPoint: stringValue(item.mountPoint),
    totalBytes,
    usedBytes: numericValue(item.usedBytes),
    availableBytes: numericValue(item.availableBytes),
    usagePercent: numericValue(item.usagePercent)
  }
}

function numericValue(value: unknown) {
  const n = Number(value)
  return Number.isFinite(n) && n > 0 ? n : 0
}

function stringValue(value: unknown) {
  return String(value ?? '').trim()
}

function booleanValue(value: unknown) {
  if (typeof value === 'boolean') return value
  return ['true', '1', 'yes', 'on', 'enabled'].includes(stringValue(value).toLowerCase())
}
