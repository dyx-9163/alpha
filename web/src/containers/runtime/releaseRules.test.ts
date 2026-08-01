import { describe, expect, it } from 'vitest'
import {
  runtimeReleaseRollbackDisabledReason,
  runtimeReleaseRollbackServices
} from './releaseRules'
import type { AifarRelease } from './types'

function release(overrides: Partial<AifarRelease> = {}): AifarRelease {
  return {
    instanceId: 'instance-1',
    releaseId: 'release-previous',
    rollbackAvailable: true,
    changedServices: ['must-not-be-submitted'],
    rollbackServices: ['gateway'],
    ...overrides
  }
}

const options = {
  canManage: true,
  deniedText: 'permission denied',
  t: (key: string) => `translated:${key}`
}

describe('runtime release rollback rules', () => {
  it.each([
    ['ROLLBACK_RECORD', 'containers.releaseRollbackAuditRecord'],
    ['ALREADY_ACTIVE', 'containers.releaseRollbackAlreadyActive'],
    ['ARTIFACT_UNAVAILABLE', 'containers.releaseRollbackUnavailable']
  ] as const)('maps %s to its disabled explanation', (rollbackUnavailableReason, messageKey) => {
    expect(runtimeReleaseRollbackDisabledReason(release({ rollbackUnavailableReason }), options))
      .toBe(`translated:${messageKey}`)
  })

  it('denies rollback before checking the release', () => {
    expect(runtimeReleaseRollbackDisabledReason(release({ releaseId: '', rollbackUnavailableReason: 'ALREADY_ACTIVE' }), {
      ...options,
      canManage: false
    })).toBe('permission denied')
  })

  it('requires a release ID before reporting the backend reason', () => {
    expect(runtimeReleaseRollbackDisabledReason(release({ releaseId: '', rollbackUnavailableReason: 'ALREADY_ACTIVE' }), options))
      .toBe('translated:containers.releaseIdRequired')
  })

  it('allows an available artifact release with rollback services', () => {
    expect(runtimeReleaseRollbackDisabledReason(release(), options)).toBe('')
  })

  it('uses only a deduplicated defensive copy of rollback services', () => {
    const row = release({
      changedServices: ['must-not-be-submitted'],
      rollbackServices: ['gateway', 'oauth', 'gateway', '']
    })

    const services = runtimeReleaseRollbackServices(row)

    expect(services).toEqual(['gateway', 'oauth'])
    expect(services).not.toBe(row.rollbackServices)
    services.push('mutated')
    expect(row.rollbackServices).toEqual(['gateway', 'oauth', 'gateway', ''])
  })
})
