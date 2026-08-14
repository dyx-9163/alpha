import { describe, expect, it } from 'vitest'

import {
  runtimeIngressColumns,
  runtimeLogWorkspaceTabLabels,
  runtimeLogWorkspaceTabOrder,
  runtimeResourceTabLabels,
  runtimeResourceTabOrder
} from './surface'

describe('AIFAR Runtime surface policy', () => {
  it('keeps release history out of the operational runtime tabs', () => {
    expect(runtimeResourceTabOrder).toEqual(['deployments', 'services', 'pods', 'logs', 'ingress'])
    expect(runtimeResourceTabLabels).not.toHaveProperty('releases')
  })

  it('keeps only operational routing columns in ingress discovery', () => {
    expect(runtimeIngressColumns).toEqual(['service', 'app', 'discoveryTarget', 'endpoint'])
  })

  it('keeps realtime logs and diagnostic archives as focused sub-tabs', () => {
    expect(runtimeLogWorkspaceTabOrder).toEqual(['live', 'archives'])
    expect(runtimeLogWorkspaceTabLabels).toEqual({
      live: 'containers.realtimeLogs',
      archives: 'containers.diagnosticArchives'
    })
  })
})
