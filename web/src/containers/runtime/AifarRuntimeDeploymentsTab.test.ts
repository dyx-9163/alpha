import { describe, expect, it } from 'vitest'
import { normalizeBatchOfflineDeployments } from './runtimeDeploymentSelection'
import type { AifarRuntimeDeployment } from './types'

describe('AifarRuntimeDeploymentsTab selection', () => {
  it('keeps unique online deployments and excludes rows that cannot be offlined', () => {
    const rows = [
      deployment('gateway', 1),
      deployment('oauth', 2),
      deployment('gateway', 1),
      deployment('file', 0)
    ]

    expect(normalizeBatchOfflineDeployments(
      rows,
      (row) => ({ ...row }),
      (row) => Number(row.desiredReplicas ?? 0) <= 0 ? 'offline' : ''
    )).toEqual([
      expect.objectContaining({ serviceName: 'gateway' }),
      expect.objectContaining({ serviceName: 'oauth' })
    ])
  })
})

function deployment(serviceName: string, desiredReplicas: number): AifarRuntimeDeployment {
  return {
    instanceId: 'instance-1',
    deploymentName: `alpha-${serviceName}`,
    serviceName,
    desiredReplicas,
    status: desiredReplicas > 0 ? 'ready' : 'offline'
  }
}
