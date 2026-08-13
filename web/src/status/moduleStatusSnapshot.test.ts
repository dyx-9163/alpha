import { describe, expect, it } from 'vitest'
import { installedGroupStatus } from '../apps/installedStatus'
import { normalizeDashboardRuntimeStatus, normalizeDashboardServerStatus } from '../dashboard/serverStatus'
import {
  isInstallLifecycleSelectable,
  installLifecycleDisplayStatus,
  moduleRuntimeGroupStatus,
  runtimeHealthDisplayStatus
} from './semantics'
import nacosViewSource from '../views/NacosView.vue?raw'

describe('module status display snapshots', () => {
  it('keeps install lifecycle green when only runtime monitoring fails', () => {
    const runtimeFailed = { status: 'failed', metadata: { lastCheck: { status: 'failed' } } }

    expect(installLifecycleDisplayStatus(runtimeFailed)).toBe('installed')
    expect(isInstallLifecycleSelectable(runtimeFailed)).toBe(true)
    expect(installedGroupStatus([runtimeFailed])).toBe('installed')
  })

  it('keeps Nacos config dependencies based on install lifecycle, not runtime health', () => {
    expect(isInstallLifecycleSelectable({ status: 'failed', metadata: { installState: 'installed' } })).toBe(true)
    expect(isInstallLifecycleSelectable({ status: 'install_failed', metadata: { installState: 'failed' } })).toBe(false)
    expect(nacosViewSource).toContain('isInstallLifecycleSelectable')
    expect(nacosViewSource).not.toContain("item.status !== 'failed'")
  })

  it('uses one runtime failure label for dashboard, database, storage and Nacos summaries', () => {
    const monitoringFailures = ['failed', 'error', 'unavailable', 'offline', 'no-endpoints']

    for (const status of monitoringFailures) {
      expect(runtimeHealthDisplayStatus(status)).toBe('unavailable')
      expect(normalizeDashboardRuntimeStatus(status)).toBe('unavailable')
      expect(moduleRuntimeGroupStatus([status])).toBe('unavailable')
    }
  })

  it('keeps server reachability separate from application runtime health', () => {
    expect(normalizeDashboardServerStatus('running')).toBe('available')
    expect(normalizeDashboardServerStatus('failed')).toBe('unavailable')
    expect(moduleRuntimeGroupStatus(['running', 'failed'])).toBe('degraded')
    expect(moduleRuntimeGroupStatus(['checking', 'running'])).toBe('checking')
  })
})
