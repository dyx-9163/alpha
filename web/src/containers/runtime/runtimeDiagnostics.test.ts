import { describe, expect, it } from 'vitest'
import {
  defaultRuntimeDiagnosticWindow,
  emptyRuntimeDiagnosticExportPage,
  enabledRuntimeDiagnosticServices,
  runtimeDiagnosticRequestFingerprint,
  runtimeDiagnosticSubmitDisabledReason,
  terminalDiagnosticTaskToRefresh
} from './runtimeDiagnostics'

describe('runtime diagnostic interactions', () => {
  it('defaults to the last two hours and all enabled deployments', () => {
    const now = new Date('2026-07-27T08:00:00Z')

    expect(defaultRuntimeDiagnosticWindow(now)).toEqual({
      sinceAt: new Date('2026-07-27T06:00:00Z'),
      untilAt: now
    })
    expect(enabledRuntimeDiagnosticServices([
      { instanceId: 'instance-1', serviceName: 'gateway', desiredReplicas: 1 },
      { instanceId: 'instance-1', serviceName: 'oauth', desiredReplicas: 0 }
    ])).toEqual(['gateway'])
  })

  it('blocks submit until a current estimate is allowed', () => {
    expect(runtimeDiagnosticSubmitDisabledReason({ services: ['gateway'], estimate: null, estimating: false, submitting: false })).toBe('estimate-required')
    expect(runtimeDiagnosticSubmitDisabledReason({ services: ['gateway'], estimate: { allowed: false }, estimating: false, submitting: false })).toBe('estimate-blocked')
  })

  it('requires an exact scope fingerprint for an estimate response', () => {
    const request = {
      instanceId: 'instance-1',
      sinceAt: '2026-07-27T06:00:00Z',
      untilAt: '2026-07-27T08:00:00Z',
      services: ['gateway']
    }

    expect(runtimeDiagnosticRequestFingerprint('serverId=server-1', request)).not.toBe(runtimeDiagnosticRequestFingerprint('serverId=server-2', request))
    expect(runtimeDiagnosticRequestFingerprint('serverId=server-1', request)).not.toBe(runtimeDiagnosticRequestFingerprint('serverId=server-1', { ...request, services: ['oauth'] }))
  })

  it('refreshes every tracked export task exactly once at terminal state', () => {
    const refreshed = new Set<string>()
    const tracked = new Set(['task-1', 'task-2'])

    expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'running' }], tracked, refreshed)).toEqual([])
    expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'success' }, { id: 'task-2', status: 'failed' }], tracked, refreshed)).toEqual(['task-1', 'task-2'])
    refreshed.add('task-1')
    refreshed.add('task-2')
    expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'success' }, { id: 'task-2', status: 'failed' }], tracked, refreshed)).toEqual([])
  })

  it('clears diagnostic rows immediately when the export scope changes', () => {
    const cleared = emptyRuntimeDiagnosticExportPage()

    expect(cleared).toEqual({ items: [], total: 0, page: 1, pageSize: 20 })
    expect(cleared.items).toHaveLength(0)
  })
})
