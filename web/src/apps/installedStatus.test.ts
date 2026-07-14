import { describe, expect, it } from 'vitest'

import { installedGroupStatus } from './installedStatus'

describe('installedGroupStatus', () => {
  it('does not turn a successful installation into a failure on collector timeout', () => {
    expect(installedGroupStatus([
      { status: 'unavailable', metadata: '{"installState":"installed"}' }
    ])).toBe('installed')
  })

  it('still reports an actual installation failure', () => {
    expect(installedGroupStatus([
      { status: 'install_failed', metadata: '{"installFailed":true}' }
    ])).toBe('failed')
  })
})
