import { describe, expect, it } from 'vitest'
import { keepPreviousArrayOnLoadFailure, keepPreviousObjectOnLoadFailure } from './resilientLoad'

describe('resilient page loading', () => {
  it('keeps the previous array when a request fails', async () => {
    const previous = [{ id: 'srv-1' }]

    await expect(keepPreviousArrayOnLoadFailure(Promise.reject(new Error('network down')), previous)).resolves.toBe(previous)
  })

  it('accepts an empty array response as a successful empty state', async () => {
    const previous = [{ id: 'srv-1' }]

    await expect(keepPreviousArrayOnLoadFailure(Promise.resolve([]), previous)).resolves.toEqual([])
  })

  it('keeps the previous object when a request fails', async () => {
    const previous = { maxRequestBodyBytes: 1024 }

    await expect(keepPreviousObjectOnLoadFailure(Promise.reject(new Error('network down')), previous)).resolves.toBe(previous)
  })
})
