import { describe, expect, it } from 'vitest'
import {
  runtimeReleaseDeleteDisabledReason,
  runtimeReleaseRollbackDisabledReason,
  runtimeReleaseRollbackServices,
  runtimeReleaseScope
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

describe('runtime release deletion rules', () => {
  it.each([
    ['AIFAR_RELEASE_DELETE_CURRENT', 'containers.releaseDeleteCurrentUnavailable'],
    ['AIFAR_RELEASE_DELETE_ACTIVE', 'containers.releaseDeleteActiveUnavailable']
  ] as const)('maps %s to its disabled explanation', (deleteUnavailableReason, messageKey) => {
    const row = {
      ...release(),
      deleteAvailable: false,
      deleteUnavailableReason
    } as AifarRelease

    expect(runtimeReleaseDeleteDisabledReason(row, options)).toBe(`translated:${messageKey}`)
  })

  it('allows a historical release that the backend marks deletable', () => {
    const row = {
      ...release(),
      deleteAvailable: true,
      deleteUnavailableReason: ''
    } as AifarRelease

    expect(runtimeReleaseDeleteDisabledReason(row, options)).toBe('')
  })

  it('denies deletion before checking the backend reason', () => {
    const row = {
      ...release({ releaseId: '' }),
      deleteAvailable: true,
      deleteUnavailableReason: ''
    } as AifarRelease

    expect(runtimeReleaseDeleteDisabledReason(row, {
      ...options,
      canManage: false
    })).toBe('permission denied')
  })
})

describe('runtime release scope', () => {
  it('builds an ordered service union while keeping the current subset distinct', () => {
    expect(runtimeReleaseScope(release({
      changedServices: ['oauth', 'gateway'],
      currentServices: ['oauth', 'oauth', ''],
      rollbackServices: ['gateway', 'file', 'gateway']
    }))).toEqual({
      currentServices: ['oauth'],
      totalServices: ['oauth', 'gateway', 'file']
    })
  })
})
