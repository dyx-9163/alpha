// @vitest-environment happy-dom

import { mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import {
  promptRuntimeReleaseRollback,
  runtimeReleaseRollbackImpact,
  runtimeReleaseRollbackImpactMessage
} from './releaseImpact'
import type { AifarRelease } from './types'

const row: AifarRelease = {
  instanceId: 'instance-1',
  releaseId: 'release-target',
  rollbackServices: ['gateway', 'oauth'],
  currentServiceRevisions: {
    gateway: 'release-live-gateway'
  }
}

describe('runtime release rollback impact', () => {
  it('maps only approved rollback services to current and target revisions', () => {
    expect(runtimeReleaseRollbackImpact(row, 'Unknown revision')).toEqual([
      {
        service: 'gateway',
        currentRevision: 'release-live-gateway',
        targetRevision: 'release-target'
      },
      {
        service: 'oauth',
        currentRevision: 'Unknown revision',
        targetRevision: 'release-target'
      }
    ])
  })

  it('renders the target, affected services, revision changes, and data warning', () => {
    const labels: Record<string, string> = {
      'containers.releaseRollbackImpactTarget': 'Target: {release}',
      'containers.releaseRollbackImpactServices': 'Affected ({count}): {services}',
      'containers.releaseRollbackImpactRevision': '{service}: {current} -> {target}',
      'containers.releaseRollbackDataWarning': 'Business data is not rolled back automatically.',
      'containers.rollbackReasonInputHint': 'Enter the rollback reason below.',
      'containers.releaseUnknownRevision': 'Unknown revision'
    }
    const t = (key: string, named?: Record<string, unknown>) => Object.entries(named ?? {}).reduce(
      (text, [name, value]) => text.replace(`{${name}}`, String(value)),
      labels[key] ?? key
    )
    const wrapper = mount(defineComponent({
      setup: () => () => runtimeReleaseRollbackImpactMessage(row, t)
    }))

    expect(wrapper.text()).toContain('Target: release-target')
    expect(wrapper.text()).toContain('Affected (2): gateway, oauth')
    expect(wrapper.text()).toContain('gateway: release-live-gateway -> release-target')
    expect(wrapper.text()).toContain('oauth: Unknown revision -> release-target')
    expect(wrapper.text()).toContain('Business data is not rolled back automatically.')
    expect(wrapper.text()).toContain('Enter the rollback reason below.')
  })

  it('passes the impact preview to the prompt and returns only approved rollback services', async () => {
    const t = (key: string) => key
    const prompt = vi.fn().mockResolvedValue({ value: '  health regression  ' })

    const payload = await promptRuntimeReleaseRollback(row, t, prompt)

    expect(prompt).toHaveBeenCalledTimes(1)
    expect(prompt.mock.calls[0][0]).toMatchObject({ type: 'div' })
    expect(payload).toEqual({
      targetReleaseId: 'release-target',
      services: ['gateway', 'oauth'],
      reason: 'health regression'
    })
  })
})
