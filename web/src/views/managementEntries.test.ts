import { describe, expect, it } from 'vitest'

import { visibleManagementTabs } from './managementEntries'

describe('visible management tabs', () => {
  it('exposes only instance tabs on database, Nacos, and storage pages', () => {
    expect(visibleManagementTabs).toEqual({
      database: ['instances'],
      nacos: ['instances'],
      storage: ['instances']
    })
  })
})
