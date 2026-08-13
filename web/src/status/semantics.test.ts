import { describe, expect, it } from 'vitest'
import {
  installLifecycleDisplayStatus,
  runtimeHealthDisplayStatus,
  serverReachabilityDisplayStatus
} from './semantics'

describe('shared status semantics', () => {
  it('keeps install lifecycle separate from runtime health failures', () => {
    expect(installLifecycleDisplayStatus({ status: 'unavailable' })).toBe('installed')
    expect(installLifecycleDisplayStatus({ status: 'failed' })).toBe('installed')
    expect(installLifecycleDisplayStatus({ status: 'installed', metadata: { installState: 'failed' } })).toBe('failed')
    expect(installLifecycleDisplayStatus({ status: 'install_failed' })).toBe('failed')
  })

  it('normalizes runtime health failures to unavailable', () => {
    for (const status of ['failed', 'error', 'unhealthy', 'offline', 'down', 'no-endpoints', 'missing', 'stopped']) {
      expect(runtimeHealthDisplayStatus(status)).toBe('unavailable')
    }
    expect(runtimeHealthDisplayStatus('running')).toBe('running')
    expect(runtimeHealthDisplayStatus('available')).toBe('running')
    expect(runtimeHealthDisplayStatus('installed')).toBe('checking')
  })

  it('normalizes server reachability independently from runtime health', () => {
    expect(serverReachabilityDisplayStatus('failed')).toBe('unavailable')
    expect(serverReachabilityDisplayStatus('running')).toBe('available')
    expect(serverReachabilityDisplayStatus('probing')).toBe('probing')
    expect(serverReachabilityDisplayStatus('')).toBe('unknown')
  })
})
