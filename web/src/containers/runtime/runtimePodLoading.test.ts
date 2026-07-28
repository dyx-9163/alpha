import { describe, expect, it } from 'vitest'

import { runtimePodLoadArgs } from './runtimePodLoading'

describe('Runtime Pod loading policy', () => {
  it.each([
    ['enter', [false, false, false]],
    ['scope-change', [false, false, false]],
    ['refresh', [true, false, false]],
    ['metrics', [true, true, true]],
    ['status-event', [true, false, true]],
    ['logs', [false, false, true]]
  ] as const)('uses the expected force, stats, and background flags for %s', (trigger, expected) => {
    expect(runtimePodLoadArgs(trigger)).toEqual(expected)
  })
})
