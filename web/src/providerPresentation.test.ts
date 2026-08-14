import { describe, expect, it } from 'vitest'
import appSource from './App.vue?raw'
import auditSource from './views/AuditView.vue?raw'
import containersSource from './views/ContainersView.vue?raw'
import databaseSource from './views/DatabaseView.vue?raw'
import nacosSource from './views/NacosView.vue?raw'
import settingsSource from './views/SettingsView.vue?raw'
import storageSource from './views/StorageView.vue?raw'
import { messages } from './i18n/messages'

const visibleSources = [
  appSource,
  auditSource,
  containersSource,
  databaseSource,
  nacosSource,
  settingsSource,
  storageSource
].join('\n')

const forbiddenDisplayTokens = [
  'common.providerReal',
  'common.provider',
  'common.real',
  'settings.realModeTitle',
  'settings.realModeDesc',
  'settings.providerStatus',
  'settings.providerMessage',
  'providerItems',
  'providerModeLabel'
]

describe('provider presentation cleanup', () => {
  it('does not render Provider/real mode chrome in user-facing views', () => {
    for (const token of forbiddenDisplayTokens) {
      expect(visibleSources).not.toContain(token)
    }
    expect(visibleSources).not.toContain('Provider:')
    expect(visibleSources).not.toContain('真实模式')
  })

  it('does not ship Provider/real branding copy through i18n', () => {
    for (const dictionary of Object.values(messages)) {
      const text = Object.values(dictionary).filter((value) => typeof value === 'string').join('\n')
      expect(text).not.toMatch(/Provider\s*:/i)
      expect(text).not.toContain('真实模式')
      expect(text).not.toMatch(/Provider 模式.*真实/)
      expect(text).not.toContain('私有化真实运维面板')
      expect(text).not.toContain('Private real operations panel')
    }
  })
})
