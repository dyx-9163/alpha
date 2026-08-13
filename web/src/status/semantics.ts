export interface StatusRecord {
  status?: unknown
  metadata?: unknown
}

export function installLifecycleDisplayStatus(record: StatusRecord) {
  const status = normalizeStatus(record.status)
  const metadata = metadataRecord(record.metadata)
  const installState = normalizeStatus(metadata.installState)
  if (
    status === 'install_failed' ||
    installState === 'failed' ||
    installState === 'install_failed' ||
    truthyValue(metadata.installFailed)
  ) {
    return 'failed'
  }
  return 'installed'
}

export function isInstallLifecycleSelectable(record: StatusRecord) {
  return installLifecycleDisplayStatus(record) !== 'failed'
}

export function runtimeHealthDisplayStatus(status: unknown) {
  const normalized = normalizeStatus(status)
  if (isUnavailableStatus(normalized) || ['missing', 'stopped'].includes(normalized)) return 'unavailable'
  if (['probing', 'checking', 'pending', 'installed'].includes(normalized)) return 'checking'
  if (['available', 'running', 'success', 'ok'].includes(normalized)) return 'running'
  return normalized || 'unknown'
}

export function moduleRuntimeGroupStatus(statuses: readonly unknown[]) {
  const normalized = statuses.map((status) => runtimeHealthDisplayStatus(status))
  if (!normalized.length) return 'unknown'
  if (normalized.some((status) => status === 'checking')) return 'checking'
  if (normalized.every((status) => status === 'running')) return 'running'
  if (normalized.some((status) => status === 'running')) return 'degraded'
  if (normalized.some((status) => status === 'unavailable')) return 'unavailable'
  if (normalized.every((status) => status === 'unknown')) return 'unknown'
  return normalized[0] || 'unknown'
}

export function serverReachabilityDisplayStatus(status: unknown) {
  const normalized = normalizeStatus(status)
  if (isUnavailableStatus(normalized)) return 'unavailable'
  if (['probing', 'checking'].includes(normalized)) return normalized
  if (['available', 'running', 'success', 'ok'].includes(normalized)) return 'available'
  return normalized || 'unknown'
}

function normalizeStatus(status: unknown) {
  return String(status ?? '').trim().toLowerCase()
}

function isUnavailableStatus(status: string) {
  return ['failed', 'error', 'unavailable', 'unhealthy', 'no-endpoints', 'down', 'offline'].includes(status)
}

function metadataRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  if (typeof value !== 'string' || !value.trim()) {
    return {}
  }
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

function truthyValue(value: unknown) {
  if (typeof value === 'boolean') return value
  return ['true', '1', 'yes', 'y'].includes(String(value ?? '').trim().toLowerCase())
}
