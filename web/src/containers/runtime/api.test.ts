import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const {
  apiEventSourceUrlMock,
  apiGetMock,
  apiPostFormMock,
  apiPostMock,
  apiPutMock
} = vi.hoisted(() => ({
  apiEventSourceUrlMock: vi.fn(),
  apiGetMock: vi.fn(),
  apiPostFormMock: vi.fn(),
  apiPostMock: vi.fn(),
  apiPutMock: vi.fn()
}))

vi.mock('../../api/client', () => ({
  apiEventSourceUrl: apiEventSourceUrlMock,
  apiGet: apiGetMock,
  apiPost: apiPostMock,
  apiPostForm: apiPostFormMock,
  apiPut: apiPutMock
}))

import {
  applyRuntimeConfig,
  cleanupStaleRuntime,
  createRuntimeLogEventSource,
  fetchAifarReleases,
  fetchAifarRuntime,
  installRuntimeServices,
  offlineRuntimeService,
  reconcileRuntime,
  restartAllRuntime,
  rollbackAifarRelease,
  scaleInRuntimeService,
  scaleOutRuntimeService,
  updateAifarArtifact
} from './api'

class FakeEventSource {
  static urls: string[] = []
  readonly url: string

  constructor(url: string | URL) {
    this.url = String(url)
    FakeEventSource.urls.push(this.url)
  }
}

describe('AIFAR Runtime API service', () => {
  beforeEach(() => {
    apiEventSourceUrlMock.mockReset()
    apiGetMock.mockReset()
    apiPostMock.mockReset()
    apiPostFormMock.mockReset()
    apiPutMock.mockReset()
    FakeEventSource.urls = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it.each([
    [{}, '/containers/aifar/runtime?serverId=server-1&includePods=0&includeStats=0'],
    [{ includePods: true }, '/containers/aifar/runtime?serverId=server-1&includePods=1&includeStats=0'],
    [{ includeStats: true }, '/containers/aifar/runtime?serverId=server-1&includePods=0&includeStats=1'],
    [{ includePods: true, includeStats: true }, '/containers/aifar/runtime?serverId=server-1&includePods=1&includeStats=1']
  ])('loads runtime with explicit pod and stats options %#', async (options, endpoint) => {
    const response = { runtimeStatus: 'ready', instances: [], services: [], pods: [], ingress: [], warnings: [] }
    apiGetMock.mockResolvedValueOnce(response)

    await expect(fetchAifarRuntime('serverId=server-1', options)).resolves.toBe(response)
    expect(apiGetMock).toHaveBeenCalledWith(endpoint)
  })

  it('loads releases for the selected instance', async () => {
    const response = { items: [{ instanceId: 'instance-1', releaseId: 'release-1' }] }
    apiGetMock.mockResolvedValueOnce(response)

    await expect(fetchAifarReleases('instance-1')).resolves.toBe(response)
    expect(apiGetMock).toHaveBeenCalledWith('/apps/instances/instance-1/aifar/releases')
  })

  it('builds the authenticated runtime log EventSource URL', () => {
    const params = new URLSearchParams({ serverId: 'server-1', services: 'gateway,oauth', tail: '200' })
    apiEventSourceUrlMock.mockReturnValueOnce('/api/v2/containers/aifar/runtime/logs/events?serverId=server-1&services=gateway%2Coauth&tail=200&token=redacted')

    const source = createRuntimeLogEventSource(params) as unknown as FakeEventSource

    expect(apiEventSourceUrlMock).toHaveBeenCalledWith(
      '/containers/aifar/runtime/logs/events?serverId=server-1&services=gateway%2Coauth&tail=200'
    )
    expect(source.url).toContain('/api/v2/containers/aifar/runtime/logs/events?')
    expect(FakeEventSource.urls).toEqual([source.url])
  })

  it('submits rollback payload unchanged', async () => {
    const payload = { targetReleaseId: 'release-1', services: ['gateway', 'oauth'], reason: 'health regression' }
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-rollback' })

    await expect(rollbackAifarRelease('instance-1', payload)).resolves.toEqual({ taskId: 'task-rollback' })
    expect(apiPostMock).toHaveBeenCalledWith('/apps/instances/instance-1/aifar/rollback', payload)
  })

  it.each([
    ['single', '/apps/instances/instance-1/aifar/update-artifact'],
    ['bundle', '/apps/instances/instance-1/aifar/update-artifact-bundle']
  ] as const)('uploads %s artifacts to the matching endpoint', async (mode, endpoint) => {
    const form = new FormData()
    form.append('language', 'zh')
    apiPostFormMock.mockResolvedValueOnce({ taskId: `task-${mode}` })

    await expect(updateAifarArtifact('instance-1', form, mode)).resolves.toEqual({ taskId: `task-${mode}` })
    expect(apiPostFormMock).toHaveBeenCalledWith(endpoint, form)
  })

  it('puts the complete runtime configuration payload', async () => {
    const payload = {
      instanceId: 'instance-1',
      global: {
        appCPUs: '2.0',
        appMemoryLimit: '2GB',
        jvmInitialRAMPercentage: 20,
        jvmMaxRAMPercentage: 70
      },
      services: { gateway: { appCPUs: '1.0' } },
      nacosEphemeral: true
    }
    apiPutMock.mockResolvedValueOnce({ taskId: 'task-config' })

    await expect(applyRuntimeConfig('serverId=server-1', payload)).resolves.toEqual({ taskId: 'task-config' })
    expect(apiPutMock).toHaveBeenCalledWith('/containers/aifar/runtime/config?serverId=server-1', payload)
  })

  it.each([
    ['reconcileRuntime', reconcileRuntime, '/containers/aifar/runtime/reconcile?serverId=server-1'],
    ['cleanupStaleRuntime', cleanupStaleRuntime, '/containers/aifar/runtime/cleanup-stale?serverId=server-1']
  ] as const)('%s posts the selected instance', async (_name, operation, endpoint) => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-runtime' })

    await expect(operation('serverId=server-1', 'instance-1')).resolves.toEqual({ taskId: 'task-runtime' })
    expect(apiPostMock).toHaveBeenCalledWith(endpoint, { instanceId: 'instance-1' })
  })

  it('posts restart all for only the selected runtime instance', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-restart-all' })

    await expect(restartAllRuntime('serverId=server-1', 'instance-1', ' load edited env ')).resolves.toEqual({ taskId: 'task-restart-all' })
    expect(apiPostMock).toHaveBeenCalledWith('/containers/aifar/runtime/restart-all?serverId=server-1', {
      instanceId: 'instance-1',
      reason: 'load edited env'
    })
  })

  it('installs the selected runtime services', async () => {
    const payload = { instanceId: 'instance-1', services: ['oauth', 'gateway'] }
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-install' })

    await expect(installRuntimeServices('serverId=server-1', payload)).resolves.toEqual({ taskId: 'task-install' })
    expect(apiPostMock).toHaveBeenCalledWith('/containers/aifar/services/install?serverId=server-1', payload)
  })

  it.each([
    ['scaleOutRuntimeService', scaleOutRuntimeService, 'scale-out'],
    ['scaleInRuntimeService', scaleInRuntimeService, 'scale-in'],
    ['offlineRuntimeService', offlineRuntimeService, 'offline']
  ] as const)('%s encodes the service name and posts the selected instance', async (_name, operation, action) => {
    apiPostMock.mockResolvedValueOnce({ taskId: `task-${action}` })

    await expect(operation('serverId=server-1', 'web vue/3', 'instance-1'))
      .resolves.toEqual({ taskId: `task-${action}` })
    expect(apiPostMock).toHaveBeenCalledWith(
      `/containers/aifar/services/web%20vue%2F3/${action}?serverId=server-1`,
      { instanceId: 'instance-1' }
    )
  })
})
