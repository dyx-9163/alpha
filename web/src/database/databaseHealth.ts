export type DatabaseHealth = 'online' | 'offline' | 'unknown' | 'probing'

export type DatabaseHealthSource = {
  app?: unknown
  topology?: unknown
  checkStatus?: unknown
  runtimeStatus?: unknown
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

function normalize(value: unknown) {
  return String(value ?? '').trim().toLowerCase()
}
