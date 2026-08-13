import { describe, expect, it } from 'vitest'

import { installedGroupStatus } from './installedStatus'

describe('installedGroupStatus', () => {
  it('does not turn a successful installation into a failure on collector timeout', () => {
    expect(installedGroupStatus([
      { status: 'unavailable', metadata: '{"installState":"installed"}' }
    ])).toBe('installed')
  })

  it('does not turn a successful installation into a failure on collector failure', () => {
    expect(installedGroupStatus([
      { status: 'failed', metadata: '{"installState":"installed"}' }
    ])).toBe('installed')
  })

  it('does not treat a legacy installed row status failure as an installation failure', () => {
    expect(installedGroupStatus([
      { status: 'failed', metadata: '{"lastCheck":{"status":"failed","message":"service unavailable"}}' }
    ])).toBe('installed')
  })

  it('still reports an actual installation failure', () => {
    expect(installedGroupStatus([
      { status: 'install_failed', metadata: '{"installFailed":true}' }
    ])).toBe('failed')
  })

  it('reports explicit install state failures', () => {
    expect(installedGroupStatus([
      { status: 'failed', metadata: '{"installState":"install_failed"}' }
    ])).toBe('failed')
  })
})
