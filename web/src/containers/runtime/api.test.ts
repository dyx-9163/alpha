import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const {
  apiEventSourceUrlMock,
  apiDeleteMock,
  apiDownloadMock,
  apiGetMock,
  apiPostFormMock,
  apiPostMock,
  apiPutMock
} = vi.hoisted(() => ({
  apiEventSourceUrlMock: vi.fn(),
  apiDeleteMock: vi.fn(),
  apiDownloadMock: vi.fn(),
  apiGetMock: vi.fn(),
  apiPostFormMock: vi.fn(),
  apiPostMock: vi.fn(),
  apiPutMock: vi.fn()
}))

vi.mock('../../api/client', () => ({
  apiEventSourceUrl: apiEventSourceUrlMock,
  apiDelete: apiDeleteMock,
  apiDownload: apiDownloadMock,
  apiGet: apiGetMock,
  apiPost: apiPostMock,
  apiPostForm: apiPostFormMock,
  apiPut: apiPutMock
}))

vi.mock('../../i18n', () => ({
  getCurrentLocale: () => 'en'
}))

import {
  applyRuntimeConfig,
  cleanupStaleRuntime,
  createRuntimeDiagnosticExport,
  createRuntimeLogEventSource,
  deleteAifarRelease,
  deleteRuntimeDiagnosticExport,
  downloadRuntimeDiagnosticExport,
  estimateRuntimeDiagnostics,
  fetchAifarReleases,
  fetchRuntimeDiagnosticExports,
  fetchAifarRuntime,
  installRuntimeServices,
  mutateRuntimeDeployment,
  reconcileRuntimeDeployment,
  restartAllRuntime,
  runtimeLockOwnerTaskId,
  rollbackAifarRelease,
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
    apiDeleteMock.mockReset()
    apiDownloadMock.mockReset()
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

  it('deletes the selected release record with encoded path segments', async () => {
    apiDeleteMock.mockResolvedValueOnce({ releaseId: 'release/old' })

    await expect(deleteAifarRelease('instance/1', 'release/old')).resolves.toEqual({ releaseId: 'release/old' })
    expect(apiDeleteMock).toHaveBeenCalledWith('/apps/instances/instance%2F1/aifar/releases/release%2Fold')
  })

  it('estimates and creates a runtime diagnostic export', async () => {
    const payload = {
      instanceId: 'instance-1',
      localDate: '2026-07-27',
      services: ['gateway', 'oauth']
    }
    apiPostMock.mockResolvedValueOnce({ allowed: true })
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-1', exportId: 'diag-1', status: 'pending' })

    await estimateRuntimeDiagnostics('serverId=server-1', payload)
    expect(apiPostMock).toHaveBeenCalledWith('/containers/aifar/runtime/diagnostics/estimate?serverId=server-1', payload)
    await createRuntimeDiagnosticExport('serverId=server-1', payload)
    expect(apiPostMock).toHaveBeenLastCalledWith('/containers/aifar/runtime/diagnostics/exports?serverId=server-1', payload)
  })

  it('lists runtime diagnostic exports with paging parameters', async () => {
    apiGetMock.mockResolvedValueOnce({ items: [], total: 0, page: 2, pageSize: 50 })

    await fetchRuntimeDiagnosticExports('serverId=server-1', 'instance / 1', 2, 50)

    expect(apiGetMock).toHaveBeenCalledWith(
      '/containers/aifar/runtime/diagnostics/exports?serverId=server-1&instanceId=instance+%2F+1&page=2&pageSize=50'
    )
  })

  it('downloads with delete-after-download disabled by default', async () => {
    apiDownloadMock.mockResolvedValueOnce({ blob: new Blob(), filename: 'diagnostics.tar.gz', sha256: 'abc' })

    await downloadRuntimeDiagnosticExport('serverId=server-1', 'diag/1')

    expect(apiDownloadMock).toHaveBeenCalledWith(
      '/containers/aifar/runtime/diagnostics/exports/diag%2F1/download?serverId=server-1&deleteAfterDownload=false'
    )
  })

  it('deletes the selected runtime diagnostic export', async () => {
    apiDeleteMock.mockResolvedValueOnce({ taskId: 'task-delete' })

    await deleteRuntimeDiagnosticExport('serverId=server-1', 'diag/1')

    expect(apiDeleteMock).toHaveBeenCalledWith(
      '/containers/aifar/runtime/diagnostics/exports/diag%2F1?serverId=server-1'
    )
  })

  it('reads the runtime diagnostic sha256 response header', async () => {
    const fetchMock = vi.fn().mockResolvedValue(new Response('diagnostic archive', {
      headers: { 'X-AIFAR-Diagnostic-SHA256': 'diagnostic-sha256' }
    }))
    vi.stubGlobal('localStorage', {
      getItem: () => null,
      setItem: () => undefined,
      removeItem: () => undefined
    })
    vi.stubGlobal('fetch', fetchMock)
    const { apiDownload } = await vi.importActual<typeof import('../../api/client')>('../../api/client')

    await expect(apiDownload('/containers/aifar/runtime/diagnostics/exports/diag-1/download')).resolves.toMatchObject({
      sha256: 'diagnostic-sha256'
    })
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
    ['single', '/apps/instances/instance-1/aifar/update-artifact', 7],
    ['bundle', '/apps/instances/instance-1/aifar/update-artifact-bundle', undefined]
  ] as const)('uploads %s artifacts to the matching endpoint', async (mode, endpoint, expectedGeneration) => {
    const form = new FormData()
    form.append('language', 'zh')
    apiPostFormMock.mockResolvedValueOnce({ taskId: `task-${mode}` })

    await expect(updateAifarArtifact('instance-1', form, mode, expectedGeneration)).resolves.toEqual({ taskId: `task-${mode}` })
    expect(form.get('expectedGeneration')).toBe(mode === 'single' ? '7' : null)
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

  it('posts cleanup for the selected instance', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-runtime' })

    await expect(cleanupStaleRuntime('serverId=server-1', 'instance-1')).resolves.toEqual({ taskId: 'task-runtime' })
    expect(apiPostMock).toHaveBeenCalledWith('/containers/aifar/runtime/cleanup-stale?serverId=server-1', { instanceId: 'instance-1' })
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

  it('puts a typed per-service mutation with the expected generation', async () => {
    apiPutMock.mockResolvedValueOnce({ taskId: 'task-scale' })
    const payload = { operation: 'scale' as const, expectedGeneration: 7, replicas: 3, reason: 'capacity' }

    await expect(mutateRuntimeDeployment('serverId=server-1', 'instance/1', 'web vue/3', payload))
      .resolves.toEqual({ taskId: 'task-scale' })
    expect(apiPutMock).toHaveBeenCalledWith(
      '/apps/instances/instance%2F1/runtime/deployments/web%20vue%2F3?serverId=server-1',
      payload
    )
  })

  it('posts a typed per-service reconcile without an operation field', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task-reconcile' })

    await expect(reconcileRuntimeDeployment('serverId=server-1', 'instance-1', 'permission', {
      expectedGeneration: 9,
      reason: 'retry now'
    })).resolves.toEqual({ taskId: 'task-reconcile' })
    expect(apiPostMock).toHaveBeenCalledWith(
      '/apps/instances/instance-1/runtime/deployments/permission/reconcile?serverId=server-1',
      { expectedGeneration: 9, reason: 'retry now' }
    )
  })

  it('extracts only a 409 service-lock owner task for UI task location', () => {
    expect(runtimeLockOwnerTaskId({ status: 409, details: { ownerTaskId: ' task-owner ' } })).toBe('task-owner')
    expect(runtimeLockOwnerTaskId({ status: 409, details: { currentGeneration: 8 } })).toBe('')
    expect(runtimeLockOwnerTaskId({ status: 400, details: { ownerTaskId: 'task-other' } })).toBe('')
  })
})
