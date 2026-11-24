export type MiddlewareUpgradeApp = 'mysql' | 'minio'

export type MiddlewareUpgradeInstance = {
  id: string
  app: string
  serverId: string
  version: string
  topology: string
}

const paths = {
  mysql: { sourceVersion: '8.0.36', version: '8.4.13', topology: 'innodb-cluster', count: 3 },
  minio: { sourceVersion: '2025-10-15T17-29-55Z', version: '20261112', topology: 'bucket-replication', count: 2 }
} as const

export function middlewareUpgradeTarget(app: MiddlewareUpgradeApp, instances: readonly MiddlewareUpgradeInstance[]) {
  const path = paths[app]
  if (instances.length !== path.count || new Set(instances.map(instance => instance.serverId)).size !== path.count) {
    return null
  }
  if (instances.some(instance =>
    instance.app !== app || instance.version !== path.sourceVersion || instance.topology !== path.topology || !instance.serverId || !instance.id
  )) {
    return null
  }
  return { instanceId: instances[0].id, sourceVersion: path.sourceVersion, version: path.version }
}
