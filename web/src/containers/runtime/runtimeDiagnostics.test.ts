import { describe, expect, it } from 'vitest'
import {
  defaultRuntimeDiagnosticWindow,
  enabledRuntimeDiagnosticServices,
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

  it('refreshes each tracked export task exactly once at terminal state', () => {
    const refreshed = new Set<string>()

    expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'running' }], new Set(['task-1']), refreshed)).toBe('')
    expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'success' }], new Set(['task-1']), refreshed)).toBe('task-1')
    refreshed.add('task-1')
    expect(terminalDiagnosticTaskToRefresh([{ id: 'task-1', status: 'success' }], new Set(['task-1']), refreshed)).toBe('')
  })
})
