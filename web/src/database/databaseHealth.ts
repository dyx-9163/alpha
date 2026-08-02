export type DatabaseHealth = 'online' | 'offline' | 'unknown' | 'probing'

export type DatabaseServiceStatus = 'running' | 'degraded' | 'unavailable' | 'probing' | 'unknown'

export type DatabaseHealthSource = {
  app?: unknown
  topology?: unknown
  checkStatus?: unknown
  runtimeStatus?: unknown
}

export type MySQLClusterHealthSource = {
  runtimeHealths: readonly DatabaseHealth[]
  checkHealths: readonly DatabaseHealth[]
  hasPrimary: boolean
}

const onlineStatuses = new Set(['ok', 'success', 'running', 'available'])
const offlineStatuses = new Set([
  'failed',
  'error',
  'missing',
  'stopped',
  'offline',
  'unavailable',
  'unhealthy',
  'down',
  'no-endpoints'
])
const probingStatuses = new Set(['probing', 'checking'])

export function healthFromCheckStatus(status: unknown): DatabaseHealth {
  const normalized = normalize(status)
  if (onlineStatuses.has(normalized)) return 'online'
  if (offlineStatuses.has(normalized)) return 'offline'
  if (probingStatuses.has(normalized)) return 'probing'
  return 'unknown'
}

export function resolveDatabaseNodeHealth(source: DatabaseHealthSource): DatabaseHealth {
  if (normalize(source.app) === 'mysql' && normalize(source.topology) === 'innodb-cluster') {
    return resolveMySQLRuntimeHealth(source)
  }
  return healthFromCheckStatus(source.checkStatus)
}

export function resolveMySQLRuntimeHealth(source: DatabaseHealthSource): DatabaseHealth {
  const runtimeHealth = healthFromCheckStatus(source.runtimeStatus)
  return runtimeHealth === 'unknown' ? healthFromCheckStatus(source.checkStatus) : runtimeHealth
}

export function resolveMySQLClusterServiceStatus(source: MySQLClusterHealthSource): DatabaseServiceStatus {
  const runtimeHealths = source.runtimeHealths
  if (!runtimeHealths.length) return 'unknown'
  if (runtimeHealths.every((status) => status === 'offline')) return 'unavailable'
  if (runtimeHealths.some((status) => status === 'probing')) return 'probing'

  if (runtimeHealths.every((status) => status === 'online')) {
    const allChecksOnline = source.checkHealths.length === runtimeHealths.length &&
      source.checkHealths.every((status) => status === 'online')
    if (source.hasPrimary && allChecksOnline) return 'running'
    if (source.hasPrimary && source.checkHealths.some((status) => status === 'online')) return 'degraded'
    if (source.checkHealths.some((status) => status === 'online')) return 'unavailable'
    if (source.checkHealths.some((status) => status === 'probing')) return 'probing'
    const allChecksOffline = source.checkHealths.length === runtimeHealths.length &&
      source.checkHealths.every((status) => status === 'offline')
    return allChecksOffline ? 'unavailable' : 'unknown'
  }

  if (runtimeHealths.some((status) => status === 'online')) return 'degraded'
  return 'unknown'
}

export function canStartMySQLCluster(source: MySQLClusterHealthSource) {
  return source.runtimeHealths.length === 3 &&
    source.checkHealths.length === 3 &&
    source.runtimeHealths.every((status) => status === 'online') &&
    source.checkHealths.every((status) => status === 'offline')
}

function normalize(value: unknown) {
  return String(value ?? '').trim().toLowerCase()
}
