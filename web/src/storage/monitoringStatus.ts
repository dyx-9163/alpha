export function filterMinioInstances<T extends { app?: string }>(instances: T[]) {
  return instances.filter((instance) => String(instance.app || '').trim().toLowerCase() === 'minio')
}

export function latestSnapshotTime(snapshots: Array<{ collectedAt?: string; updatedAt?: string } | undefined>) {
  const latest = snapshots
    .map((snapshot) => {
      const collectedAt = new Date(snapshot?.collectedAt || '').getTime()
      return Number.isFinite(collectedAt) && collectedAt > 0
        ? collectedAt
        : new Date(snapshot?.updatedAt || '').getTime()
    })
    .filter((value) => Number.isFinite(value) && value > 0)
    .sort((a, b) => b - a)[0]
  return latest ? new Date(latest).toLocaleTimeString() : ''
}
