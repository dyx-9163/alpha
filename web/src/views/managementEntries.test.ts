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

  it('uses connected status and refresh in every management page header', () => {
    expect(visibleManagementHeaderActions).toEqual({
      database: ['connected', 'refresh'],
      nacos: ['connected', 'refresh'],
      storage: ['connected', 'refresh']
    })
  })
})
