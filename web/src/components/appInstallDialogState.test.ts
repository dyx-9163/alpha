import { describe, expect, it } from 'vitest'
import type { AppInstallField } from '../apps/registry/contract'
import { appInstallResetKey, latestInstallVersion } from './appInstallDialogState'

function topologyField(defaultValue: string): AppInstallField {
  return {
    name: 'topology',
    label: 'Topology',
    type: 'select',
    defaultValue,
    required: true,
    options: [
      { label: 'Standalone', value: 'standalone' },
      { label: 'Cluster', value: 'cluster' }
    ]
  }
}

describe('appInstallResetKey', () => {
  it('stays stable when parent recomputes equivalent field objects while the dialog stays open', () => {
    const first = appInstallResetKey({
      visible: true,
      appName: 'Nacos',
      versions: ['2.4.3'],
      fallbackVersion: '',
      targetMode: 'single',
      fields: [topologyField('standalone')]
    })
    const second = appInstallResetKey({
      visible: true,
      appName: 'Nacos',
      versions: ['2.4.3'],
      fallbackVersion: '',
      targetMode: 'single',
      fields: [topologyField('standalone')]
    })

    expect(second).toBe(first)
  })

  it('changes when the dialog is reopened or a different field schema is shown', () => {
    const openKey = appInstallResetKey({
      visible: true,
      appName: 'Nacos',
      versions: ['2.4.3'],
      fallbackVersion: '',
      targetMode: 'single',
      fields: [topologyField('standalone')]
    })

    expect(appInstallResetKey({
      visible: false,
      appName: 'Nacos',
      versions: ['2.4.3'],
      fallbackVersion: '',
      targetMode: 'single',
      fields: [topologyField('standalone')]
    })).not.toBe(openKey)
    expect(appInstallResetKey({
      visible: true,
      appName: 'Nacos',
      versions: ['2.4.3'],
      fallbackVersion: '',
      targetMode: 'single',
      fields: [topologyField('standalone'), { name: 'nacosPassword' }]
    })).not.toBe(openKey)
  })
})

describe('latestInstallVersion', () => {
  it('uses the newest explicit version and falls back to fallbackVersion when needed', () => {
    expect(latestInstallVersion(['1.0.0', '2.0.0'], 'fallback')).toBe('2.0.0')
    expect(latestInstallVersion([], 'fallback')).toBe('fallback')
    expect(latestInstallVersion([], undefined)).toBe('')
  })
})
