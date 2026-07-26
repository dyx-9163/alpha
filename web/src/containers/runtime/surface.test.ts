import { describe, expect, it } from 'vitest'

import { runtimeIngressColumns, runtimeResourceTabOrder } from './surface'

describe('AIFAR Runtime surface policy', () => {
  it('places release history after every operational resource tab', () => {
    expect(runtimeResourceTabOrder).toEqual(['deployments', 'services', 'pods', 'logs', 'ingress', 'releases'])
  })

  it('keeps only operational routing columns in ingress discovery', () => {
    expect(runtimeIngressColumns).toEqual(['service', 'app', 'discoveryTarget', 'endpoint'])
  })
})
