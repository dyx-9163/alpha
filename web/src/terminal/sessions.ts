export type TerminalConnectionState = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface TerminalSessionMeta {
  id: string
  serverId: string
  label: string
  sequence: number
  status: TerminalConnectionState
}

export interface TerminalWorkspaceState {
  sessions: TerminalSessionMeta[]
  visibleIds: string[]
  focusedId: string | null
}

export const MAX_VISIBLE_TERMINALS = 4

export function emptyTerminalWorkspace(): TerminalWorkspaceState {
  return { sessions: [], visibleIds: [], focusedId: null }
}

export function nextSessionSequence(sessions: TerminalSessionMeta[], serverId: string) {
  return sessions.reduce(
    (max, item) => item.serverId === serverId ? Math.max(max, item.sequence) : max,
    0
  ) + 1
}

function replaceFocusedSlot(state: TerminalWorkspaceState, id: string) {
  if (!state.visibleIds.length) return [id]
  const focusedIndex = state.visibleIds.indexOf(state.focusedId ?? '')
  const replaceIndex = focusedIndex >= 0 ? focusedIndex : 0
  return state.visibleIds.map((visibleId, index) => index === replaceIndex ? id : visibleId)
}

export function addSession(state: TerminalWorkspaceState, item: TerminalSessionMeta): TerminalWorkspaceState {
  return {
    sessions: [...state.sessions, item],
    visibleIds: replaceFocusedSlot(state, item.id),
    focusedId: item.id
  }
}

export function focusSession(state: TerminalWorkspaceState, id: string): TerminalWorkspaceState {
  if (!state.sessions.some((item) => item.id === id)) return state
  return {
    ...state,
    visibleIds: state.visibleIds.includes(id) ? state.visibleIds : replaceFocusedSlot(state, id),
    focusedId: id
  }
}

export function addToSplit(state: TerminalWorkspaceState, id: string) {
  if (!state.sessions.some((item) => item.id === id)) {
    return { state, added: false, limitReached: false }
  }
  if (state.visibleIds.includes(id)) {
    return { state: { ...state, focusedId: id }, added: false, limitReached: false }
  }
  if (state.visibleIds.length >= MAX_VISIBLE_TERMINALS) {
    return { state, added: false, limitReached: true }
  }
  return {
    state: { ...state, visibleIds: [...state.visibleIds, id], focusedId: id },
    added: true,
    limitReached: false
  }
}

export function removeFromSplit(state: TerminalWorkspaceState, id: string): TerminalWorkspaceState {
  const visibleIds = state.visibleIds.filter((visibleId) => visibleId !== id)
  return {
    ...state,
    visibleIds,
    focusedId: state.focusedId === id ? (visibleIds[0] ?? null) : state.focusedId
  }
}

export function closeSession(state: TerminalWorkspaceState, id: string): TerminalWorkspaceState {
  const visibleIds = state.visibleIds.filter((visibleId) => visibleId !== id)
  return {
    sessions: state.sessions.filter((item) => item.id !== id),
    visibleIds,
    focusedId: state.focusedId === id ? (visibleIds[0] ?? null) : state.focusedId
  }
}

export function updateSessionStatus(
  state: TerminalWorkspaceState,
  id: string,
  status: TerminalConnectionState
): TerminalWorkspaceState {
  return {
    ...state,
    sessions: state.sessions.map((item) => item.id === id ? { ...item, status } : item)
  }
}
