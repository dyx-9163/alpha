import { describe, expect, it, vi } from 'vitest'

const apiGetMock = vi.hoisted(() => vi.fn())
const apiPostMock = vi.hoisted(() => vi.fn())

vi.mock('../api/client', () => ({
  apiGet: apiGetMock,
  apiPost: apiPostMock
}))

import {
  applyMinioCleanupPolicy,
  cleanupEstimateText,
  fetchMinioCleanupEstimate,
  formatBytes,
  formatMinioUsedAvailable,
  minioCleanupEstimateFromMetadata,
  minioStorageDisksFromMetadata,
  minioStorageInsightFromMetadata,
  summarizeMinioInstallDisks
} from './minioInsights'

describe('MinIO storage insights', () => {
  it('extracts capacity from nested last check details', () => {
    const insight = minioStorageInsightFromMetadata({
      lastCheck: {
        details: {
          minioStorageTotalBytes: 1024 ** 3,
          minioStorageUsedBytes: 512 * 1024 ** 2,
          minioStorageAvailableBytes: 512 * 1024 ** 2,
          minioStorageUsagePercent: 50,
          minioStoragePathCount: 2
        }
      }
    })

    expect(insight).toEqual({
      totalBytes: 1024 ** 3,
      usedBytes: 512 * 1024 ** 2,
      availableBytes: 512 * 1024 ** 2,
      usagePercent: 50,
      pathCount: 2
    })
  })

  it('extracts installed MinIO disk details from nested last check details', () => {
    const disks = minioStorageDisksFromMetadata({
      lastCheck: {
        details: {
          minioStorageDisks: [
            {
              index: 1,
              path: '/aifar/apps/minio/data/disk1',
              device: '/dev/nvme0n2',
              mountPoint: '/aifar/apps/minio/data/disk1',
              totalBytes: 1000,
              usedBytes: 400,
              availableBytes: 600,
              usagePercent: 40
            },
            {
              index: 2,
              path: '/aifar/apps/minio/data/disk2',
              device: '/dev/nvme0n3',
              mountPoint: '/aifar/apps/minio/data/disk2',
              totalBytes: 2000,
              usedBytes: 800,
              availableBytes: 1200,
              usagePercent: 40
            }
          ]
        }
      }
    })

    expect(disks).toEqual([
      {
        index: 1,
        path: '/aifar/apps/minio/data/disk1',
        device: '/dev/nvme0n2',
        mountPoint: '/aifar/apps/minio/data/disk1',
        totalBytes: 1000,
        usedBytes: 400,
        availableBytes: 600,
        usagePercent: 40
      },
      {
        index: 2,
        path: '/aifar/apps/minio/data/disk2',
        device: '/dev/nvme0n3',
        mountPoint: '/aifar/apps/minio/data/disk2',
        totalBytes: 2000,
        usedBytes: 800,
        availableBytes: 1200,
        usagePercent: 40
      }
    ])
  })

  it('extracts cleanup estimate from realtime-flattened metadata', () => {
    const estimate = minioCleanupEstimateFromMetadata({
      cleanupEstimateStatus: 'available',
      cleanupEstimateRetentionDays: 14,
      cleanupEstimateObjectCount: 3,
      cleanupEstimateBytes: 2048,
      cleanupEstimateSource: 'mc'
    })

    expect(estimate).toEqual({
      status: 'available',
      retentionDays: 14,
      objectCount: 3,
      bytes: 2048,
      source: 'mc'
    })
    expect(cleanupEstimateText(estimate)).toBe('2 KiB / 3 objects')
  })

  it('formats byte values with binary units', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1536)).toBe('1.5 KiB')
    expect(formatBytes(5 * 1024 ** 3)).toBe('5 GiB')
  })

  it('formats MinIO usage as used and available, not used and total', () => {
    expect(formatMinioUsedAvailable({
      totalBytes: 20 * 1024 ** 3,
      usedBytes: 831 * 1024 ** 2,
      availableBytes: 18 * 1024 ** 3,
      usagePercent: 4,
      pathCount: 2
    })).toBe('831 MiB / 18 GiB')
  })

  it('summarizes installed MinIO disks per node without aggregating replicas', () => {
    const node = {
      totalBytes: 20 * 1024 ** 3,
      usedBytes: 831 * 1024 ** 2,
      availableBytes: 18 * 1024 ** 3,
      usagePercent: 4,
      pathCount: 2
    }

    expect(summarizeMinioInstallDisks([node, node])).toEqual({
      nodeCount: 2,
      pathCount: 4,
      uniform: true,
      aggregateTotalBytes: node.totalBytes * 2,
      aggregateUsedBytes: node.usedBytes * 2,
      aggregateAvailableBytes: node.availableBytes * 2,
      totalBytes: node.totalBytes,
      usedBytes: node.usedBytes,
      availableBytes: node.availableBytes,
      minTotalBytes: node.totalBytes,
      maxTotalBytes: node.totalBytes,
      minUsedBytes: node.usedBytes,
      maxUsedBytes: node.usedBytes,
      minAvailableBytes: node.availableBytes,
      maxAvailableBytes: node.availableBytes
    })
  })

  it('requests cleanup estimate for a selected retention window', async () => {
    apiGetMock.mockResolvedValueOnce({ status: 'available', retentionDays: 30, objectCount: 2, bytes: 4096, source: 'mc' })

    await expect(fetchMinioCleanupEstimate('instance-1', 30)).resolves.toEqual({
      status: 'available',
      retentionDays: 30,
      objectCount: 2,
      bytes: 4096,
      source: 'mc'
    })
    expect(apiGetMock).toHaveBeenCalledWith('/storage/instance-1/cleanup-estimate?retentionDays=30')
  })

  it('submits MinIO cleanup policy with normalized retention days', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-1' })

    await expect(applyMinioCleanupPolicy('instance-1', {
      enabled: true,
      bucket: 'aifar',
      prefix: 'logs/',
      retentionDays: 60.8
    })).resolves.toEqual({ taskId: 'task-1' })

    expect(apiPostMock).toHaveBeenCalledWith('/storage/instance-1/cleanup-policy', {
      enabled: true,
      bucket: 'aifar',
      prefix: 'logs/',
      retentionDays: 60
    })
  })
})
