export const visibleManagementTabs = {
  database: ['instances'],
  nacos: ['instances'],
  storage: ['instances']
} as const

export const visibleManagementHeaderActions = {
  database: ['connected', 'refresh'],
  nacos: ['connected', 'refresh'],
  storage: ['connected', 'refresh']
} as const
