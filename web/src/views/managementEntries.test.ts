import { describe, expect, it } from 'vitest'

import { visibleManagementHeaderActions, visibleManagementTabs } from './managementEntries'

describe('visible management tabs', () => {
  it('exposes only instance tabs on database, Nacos, and storage pages', () => {
    expect(visibleManagementTabs).toEqual({
      database: ['instances'],
      nacos: ['instances'],
      storage: ['instances']
    })
  })

  it('uses connected status without manual refresh in realtime-backed page headers', () => {
    expect(visibleManagementHeaderActions).toEqual({
      database: ['connected'],
      nacos: ['connected'],
      storage: ['connected']
    })
  })
})
