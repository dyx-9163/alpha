import { describe, expect, it } from 'vitest'
import {
	defaultRuntimeDiagnosticDate,
  emptyRuntimeDiagnosticExportPage,
  enabledRuntimeDiagnosticServices,
  runtimeDiagnosticCapacityBlocked,
  runtimeDiagnosticLimitRows,
  runtimeDiagnosticRequestFingerprint,
  runtimeDiagnosticServicePreview,
  runtimeDiagnosticSubmitDisabledReason,
  terminalDiagnosticTaskToRefresh
} from './runtimeDiagnostics'
import type { RuntimeDiagnosticEstimate } from './types'

const estimate: RuntimeDiagnosticEstimate = {
  services: [{ service: 'gateway', candidateFiles: 2, candidateScanBytes: 1024 }],
  logSource: 'host-mounted',
  candidateFiles: 2,
  candidateScanBytes: 1024,
  estimatedSecondsMin: 5,
  estimatedSecondsMax: 20,
  maxFileScanBytes: 1073741824,
  maxTotalScanBytes: 2147483648,
  maxFilteredBytes: 524288000,
  maxArchiveBytes: 268435456,
  timeoutSeconds: 900,
  serverTimezone: 'Asia/Shanghai',
  localAvailableBytes: 10_000_000_000,
  localReadyBytes: 1024,
  localReservedBytes: 0,
  localQuotaBytes: 5368709120,
  expiresAt: '2026-07-28T08:00:00Z',
  allowed: true
}

describe('runtime diagnostic interactions', () => {
	it('defaults to one local calendar date and all enabled deployments', () => {
		const now = new Date(2026, 6, 28, 8, 0, 0)

		expect(defaultRuntimeDiagnosticDate(now)).toBe('2026-07-28')
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
			localDate: '2026-07-27',
			services: ['gateway']
		}

    expect(runtimeDiagnosticRequestFingerprint('serverId=server-1', request)).not.toBe(runtimeDiagnosticRequestFingerprint('serverId=server-2', request))
		expect(runtimeDiagnosticRequestFingerprint('serverId=server-1', request)).not.toBe(runtimeDiagnosticRequestFingerprint('serverId=server-1', { ...request, services: ['oauth'] }))
		expect(runtimeDiagnosticRequestFingerprint('serverId=server-1', request)).not.toBe(runtimeDiagnosticRequestFingerprint('serverId=server-1', { ...request, localDate: '2026-07-28' }))
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

  it('shows every enforced byte limit from the estimate contract', () => {
    expect(runtimeDiagnosticLimitRows(estimate)).toEqual([
      { key: 'file', value: 1073741824 },
      { key: 'scan', value: 2147483648 },
      { key: 'filtered', value: 524288000 },
      { key: 'archive', value: 268435456 }
    ])
  })

  it('recognizes local capacity and scan limit blocks', () => {
    expect(runtimeDiagnosticCapacityBlocked({ ...estimate, allowed: false, blockReason: 'local-quota-exceeded' })).toBe(true)
    expect(runtimeDiagnosticCapacityBlocked({ ...estimate, allowed: false, blockReason: 'local-disk-insufficient' })).toBe(true)
    expect(runtimeDiagnosticCapacityBlocked({ ...estimate, allowed: false, blockReason: 'total-scan-limit-exceeded' })).toBe(true)
    expect(runtimeDiagnosticCapacityBlocked({ ...estimate, allowed: false, blockReason: 'time-range-too-large' })).toBe(false)
    expect(runtimeDiagnosticCapacityBlocked(estimate)).toBe(false)
  })

  it('builds a compact service preview without losing the full list', () => {
    expect(runtimeDiagnosticServicePreview(['contacts', 'file', 'gateway', 'im'], 3)).toEqual({
      visibleText: 'contacts, file, gateway',
      hiddenCount: 1,
      tooltip: 'contacts, file, gateway, im'
    })
    expect(runtimeDiagnosticServicePreview([], 3)).toEqual({
      visibleText: '-',
      hiddenCount: 0,
      tooltip: '-'
    })
  })
})
