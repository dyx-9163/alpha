import { beforeEach, describe, expect, it, vi } from 'vitest'

const { apiGetMock, apiPostMock } = vi.hoisted(() => ({
  apiGetMock: vi.fn(),
  apiPostMock: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiGet: apiGetMock,
  apiPost: apiPostMock
}))

import {
  fetchContainerPageBootstrap,
  fetchDockerCollection,
  fetchDockerSummary,
  removeDockerImages
} from './dockerApi'

describe('Docker API service', () => {
  beforeEach(() => {
    apiGetMock.mockReset()
    apiPostMock.mockReset()
  })

  it('loads servers, app instances, and settings together', async () => {
    const servers = [{ id: 'server-1', name: 'node-1' }]
    const instances = [{ id: 'instance-1', app: 'docker', serverId: 'server-1', status: 'installed' }]
    const settings = { maxRequestBodyBytes: 1024 }
    apiGetMock
      .mockResolvedValueOnce(servers)
      .mockResolvedValueOnce(instances)
      .mockResolvedValueOnce(settings)

    await expect(fetchContainerPageBootstrap()).resolves.toEqual({ servers, appInstances: instances, settings })
    expect(apiGetMock.mock.calls).toEqual([
      ['/servers'],
      ['/apps/instances'],
      ['/settings']
    ])
  })

  it.each([
    ['/servers', { servers: [], appInstances: [{ id: 'instance-1' }], settings: { maxRequestBodyBytes: 1024 } }],
    ['/apps/instances', { servers: [{ id: 'server-1' }], appInstances: [], settings: { maxRequestBodyBytes: 1024 } }],
    ['/settings', { servers: [{ id: 'server-1' }], appInstances: [{ id: 'instance-1' }], settings: {} }]
  ])('falls back only the failed bootstrap request %s', async (failedPath, expected) => {
    const values: Record<string, unknown> = {
      '/servers': [{ id: 'server-1' }],
      '/apps/instances': [{ id: 'instance-1' }],
      '/settings': { maxRequestBodyBytes: 1024 }
    }
    apiGetMock.mockImplementation((path: string) => {
      if (path === failedPath) return Promise.reject(new Error('unavailable'))
      return Promise.resolve(values[path])
    })

    await expect(fetchContainerPageBootstrap()).resolves.toEqual(expected)
  })

  it('keeps previous bootstrap values when requests fail', async () => {
    const previous = {
      servers: [{ id: 'previous-server' }],
      appInstances: [{ id: 'previous-instance' }],
      settings: { maxRequestBodyBytes: 2048 }
    }
    apiGetMock.mockRejectedValue(new Error('unavailable'))

    await expect(fetchContainerPageBootstrap(previous)).resolves.toEqual(previous)
  })

  it('requests a base summary without disk usage', async () => {
    apiGetMock.mockResolvedValueOnce({ available: true })

    await expect(fetchDockerSummary('serverId=server-1', false)).resolves.toEqual({ available: true })
    expect(apiGetMock).toHaveBeenCalledWith('/containers/summary?serverId=server-1')
  })

  it('requests disk usage explicitly when required', async () => {
    apiGetMock.mockResolvedValueOnce({ available: true, diskUsage: [] })

    await fetchDockerSummary('serverId=server-1', true)
    expect(apiGetMock).toHaveBeenCalledWith('/containers/summary?serverId=server-1&includeDisk=1')
  })

  it('requests the selected Docker collection', async () => {
    apiGetMock.mockResolvedValueOnce([{ id: 'network-1' }])

    await expect(fetchDockerCollection('networks', 'serverId=server-1')).resolves.toEqual([{ id: 'network-1' }])
    expect(apiGetMock).toHaveBeenCalledWith('/containers?kind=networks&serverId=server-1')
  })

  it('removes one image with the single-image body', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-1' })

    await expect(removeDockerImages('serverId=server-1', ['repo/aifar:v1'], 'single'))
      .resolves.toEqual({ taskId: 'task-1' })
    expect(apiPostMock).toHaveBeenCalledWith(
      '/containers/images/remove?serverId=server-1',
      { id: 'repo/aifar:v1' }
    )
  })

  it('removes multiple images with the batch body without reordering ids', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-2' })

    await removeDockerImages('serverId=server-1', ['image-b', 'image-a'], 'batch')
    expect(apiPostMock).toHaveBeenCalledWith(
      '/containers/images/remove?serverId=server-1',
      { ids: ['image-b', 'image-a'] }
    )
  })
})
