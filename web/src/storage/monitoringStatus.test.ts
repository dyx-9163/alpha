import { describe, expect, it } from 'vitest'
import { filterMinioInstances, latestSnapshotTime } from './monitoringStatus'

describe('MinIO monitoring status', () => {
  it('counts only MinIO instances', () => {
    const instances = filterMinioInstances([
      { id: 'minio-1', app: 'minio' },
      { id: 'nacos-1', app: 'nacos' },
      { id: 'minio-2', app: 'MINIO' }
    ])
    expect(instances.map((item) => item.id)).toEqual(['minio-1', 'minio-2'])
  })

  it('uses the latest valid collected or updated snapshot time', () => {
    expect(latestSnapshotTime([
      { collectedAt: '2026-07-27T03:59:09Z' },
      { updatedAt: '2026-07-27T04:01:10Z' },
      { collectedAt: 'invalid' }
    ])).toBe(new Date('2026-07-27T04:01:10Z').toLocaleTimeString())
    expect(latestSnapshotTime([undefined, { collectedAt: 'invalid' }])).toBe('')
  })
})
