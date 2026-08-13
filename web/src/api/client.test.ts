// @vitest-environment happy-dom

import { beforeEach, describe, expect, it, vi } from 'vitest'
import { apiGet } from './client'

describe('api client authentication boundary', () => {
  beforeEach(() => {
    localStorage.clear()
    vi.restoreAllMocks()
  })

  it('emits a session cleared event when an API request receives 401', async () => {
    localStorage.setItem('aifar-session-token', 'expired-token')
    const events: Event[] = []
    window.addEventListener('aifar-session-cleared', (event) => events.push(event), { once: true })
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ message: 'session invalid' }), {
      status: 401,
      headers: { 'Content-Type': 'application/json' }
    })))

    await expect(apiGet('/settings')).rejects.toMatchObject({ status: 401 })

    expect(localStorage.getItem('aifar-session-token')).toBeNull()
    expect(events).toHaveLength(1)
  })
})
