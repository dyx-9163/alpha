import { describe, expect, it } from 'vitest'

import {
  addSession,
  addToSplit,
  closeSession,
  emptyTerminalWorkspace,
  focusSession,
  nextSessionSequence,
  removeFromSplit,
  updateSessionStatus,
  type TerminalSessionMeta
} from './sessions'

function session(id: string, serverId = id): TerminalSessionMeta {
  return { id, serverId, label: id, sequence: 1, status: 'connecting' }
}

describe('terminal workspace sessions', () => {
  it('allows duplicate server sessions and assigns the next free sequence', () => {
    let state = addSession(emptyTerminalWorkspace(), session('a', 'server-1'))
    state = addSession(state, { ...session('b', 'server-1'), sequence: 2 })

    expect(state.sessions.map((item) => item.serverId)).toEqual(['server-1', 'server-1'])
    expect(nextSessionSequence(state.sessions, 'server-1')).toBe(3)
  })

  it('replaces only the focused slot when a background tab is selected', () => {
    let state = addSession(emptyTerminalWorkspace(), session('a'))
    state = addToSplit(addSession(state, session('b')), 'a').state
    state = addSession(state, session('c'))

    expect(state.visibleIds).toEqual(['b', 'c'])
    state = focusSession(state, 'a')
    expect(state.visibleIds).toEqual(['b', 'a'])
    expect(state.focusedId).toBe('a')
  })

  it('rejects a fifth visible pane without deleting its background session', () => {
    const sessions = ['a', 'b', 'c', 'd', 'e'].map((id) => session(id))
    const state = { sessions, visibleIds: ['a', 'b', 'c', 'd'], focusedId: 'd' }

    const result = addToSplit(state, 'e')

    expect(result.limitReached).toBe(true)
    expect(result.state.visibleIds).toEqual(['a', 'b', 'c', 'd'])
    expect(result.state.sessions.map((item) => item.id)).toContain('e')
  })

  it('hides a pane without deleting or disconnecting its session state', () => {
    const state = {
      sessions: [session('a'), { ...session('b'), status: 'connected' as const }],
      visibleIds: ['a', 'b'],
      focusedId: 'a'
    }

    const next = removeFromSplit(state, 'b')

    expect(next.visibleIds).toEqual(['a'])
    expect(next.sessions[1]).toMatchObject({ id: 'b', status: 'connected' })
  })

  it('closes a focused pane without promoting an unrelated background tab', () => {
    const state = {
      sessions: [session('a'), session('b'), session('c')],
      visibleIds: ['a', 'b'],
      focusedId: 'b'
    }

    expect(closeSession(state, 'b')).toEqual({
      sessions: [session('a'), session('c')],
      visibleIds: ['a'],
      focusedId: 'a'
    })
  })

  it('updates only the matching session connection state', () => {
    const state = {
      sessions: [session('a'), session('b')],
      visibleIds: ['a'],
      focusedId: 'a'
    }

    expect(updateSessionStatus(state, 'b', 'error').sessions).toEqual([
      session('a'),
      { ...session('b'), status: 'error' }
    ])
  })
})
