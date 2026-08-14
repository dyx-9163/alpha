import { describe, expect, it } from 'vitest'
import type { StatusSnapshot } from '../../stores/realtime'
import { aifarRuntimeFromStatusSnapshots } from './snapshotProjection'

describe('aifarRuntimeFromStatusSnapshots', () => {
  it('only exposes installed AIFAR instances for the selected server', () => {
    const response = aifarRuntimeFromStatusSnapshots([
      runtimeSnapshot('app-current', 'srv-1', 'failed', { version: 'runtime-v2' }, 'agent missing'),
      runtimeSnapshot('app-stale', 'srv-1', 'failed', { version: 'runtime-v2' }),
      runtimeSnapshot('app-other-server', 'srv-2', 'running', { version: 'runtime-v2' })
    ], 'srv-1', [
      { id: 'app-current', app: 'aifar', serverId: 'srv-1', status: 'installed', version: 'runtime-v2' },
      { id: 'app-runtime-failed', app: 'aifar', serverId: 'srv-1', status: 'failed', version: 'runtime-v2', metadata: '{"installState":"installed"}' },
      { id: 'app-install-failed', app: 'aifar', serverId: 'srv-1', status: 'install_failed', version: 'runtime-v2', metadata: '{"installFailed":true}' },
      { id: 'app-not-aifar', app: 'mysql', serverId: 'srv-1', status: 'installed', version: '8.0.36' },
      { id: 'app-other-server', app: 'aifar', serverId: 'srv-2', status: 'installed', version: 'runtime-v2' }
    ], {})

    expect(response.instances?.map((instance) => instance.id)).toEqual(['app-current', 'app-runtime-failed'])
    expect(response.runtimeStatus).toBe('failed')
    expect(response.agent?.status).toBe('missing')
    expect(response.warnings).toEqual(['agent missing'])
  })

  it('keeps an installed AIFAR instance visible before its first runtime snapshot arrives', () => {
    const response = aifarRuntimeFromStatusSnapshots([], 'srv-1', [
      {
        id: 'app-current',
        app: 'aifar',
        serverId: 'srv-1',
        status: 'installed',
        version: 'runtime-v2',
        metadata: JSON.stringify({ orchestrationModel: 'agent-service-controller-v1', installRoot: '/aifar/apps/admin' })
      }
    ], {
      instances: [{ id: 'app-stale' }],
      deployments: [{ instanceId: 'app-stale', serviceName: 'oauth' }]
    })

    expect(response.instances).toEqual([{
      id: 'app-current',
      version: 'runtime-v2',
      status: 'installed',
      orchestrationModel: 'agent-service-controller-v1',
      installRoot: '/aifar/apps/admin',
      runtimeConfig: undefined
    }])
    expect(response.deployments).toEqual([])
  })
})

function runtimeSnapshot(
  resourceId: string,
  serverId: string,
  status: string,
  payload: Record<string, unknown> = {},
  lastError = ''
): StatusSnapshot {
  return {
    scope: 'aifar.runtime',
    resourceId,
    serverId,
    status,
    payload: { instanceId: resourceId, ...payload },
    lastError
  }
}
