export function filterMinioInstances<T extends { app?: string }>(instances: T[]) {
  return instances.filter((instance) => String(instance.app || '').trim().toLowerCase() === 'minio')
}

export function latestSnapshotTime(snapshots: Array<{ collectedAt?: string; updatedAt?: string } | undefined>) {
  const latest = snapshots
    .map((snapshot) => snapshot?.collectedAt || snapshot?.updatedAt || '')
    .map((value) => new Date(value).getTime())
    .filter((value) => Number.isFinite(value) && value > 0)
    .sort((a, b) => b - a)[0]
  return latest ? new Date(latest).toLocaleTimeString() : ''
}
