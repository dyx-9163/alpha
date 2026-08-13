// @vitest-environment happy-dom

import { createPinia, setActivePinia } from 'pinia'
import { nextTick, watch } from 'vue'
import { beforeEach, describe, expect, it, vi } from 'vitest'

const apiPostMock = vi.hoisted(() => vi.fn())

vi.mock('../api/client', () => ({
  apiPost: apiPostMock
}))

import { useSessionStore } from './session'

describe('session login state', () => {
  beforeEach(() => {
    localStorage.clear()
    apiPostMock.mockReset()
    setActivePinia(createPinia())
  })

  it('notifies watchers after a successful login so realtime connects immediately', async () => {
    const session = useSessionStore()
    const changes: boolean[] = []
    const stop = watch(() => session.isLoggedIn, (loggedIn) => {
      changes.push(loggedIn)
    })
    apiPostMock.mockResolvedValueOnce({
      token: 'session-token',
      user: {
        username: 'admin',
        role: 'admin',
        tokenVersion: 2,
        permissions: []
      }
    })

    await session.login('admin', 'password')
    await nextTick()
    stop()

    expect(session.isLoggedIn).toBe(true)
    expect(changes).toEqual([true])
  })

  it('notifies watchers after logout so realtime disconnects immediately', async () => {
    localStorage.setItem('aifar-session-token', 'session-token')
    const session = useSessionStore()
    const changes: boolean[] = []
    const stop = watch(() => session.isLoggedIn, (loggedIn) => {
      changes.push(loggedIn)
    })

    session.logout()
    await nextTick()
    stop()

    expect(session.isLoggedIn).toBe(false)
    expect(changes).toEqual([false])
  })
})
