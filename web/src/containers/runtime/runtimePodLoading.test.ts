import { describe, expect, it } from 'vitest'

import { runtimePodLoadArgs } from './runtimePodLoading'

describe('Runtime Pod loading policy', () => {
  it.each([
    ['enter', [false, true]],
    ['scope-change', [false, true]],
    ['refresh', [true, true]],
    ['status-event', [true, true]],
    ['logs', [false, false]]
  ] as const)('uses the expected force and stats flags for %s', (trigger, expected) => {
    expect(runtimePodLoadArgs(trigger)).toEqual(expected)
  })
})
