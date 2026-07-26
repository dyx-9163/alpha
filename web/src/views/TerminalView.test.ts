import { describe, expect, it } from 'vitest'

import source from './TerminalView.vue?raw'

describe('terminal viewport contract', () => {
  it('keeps every session mounted while only visible panes enter the grid', () => {
    expect(source).toContain('v-for="session in workspace.sessions"')
    expect(source).toContain('v-show="workspace.visibleIds.includes(session.id)"')
    expect(source).toContain(':visible="workspace.visibleIds.includes(session.id)"')
  })

  it('offers focused-session controls and split-pane actions', () => {
    expect(source).toContain('@click="disconnectFocused"')
    expect(source).toContain('@click="reconnectFocused"')
    expect(source).toContain('@click.stop="toggleSplit(session.id)"')
    expect(source).toContain('@click.stop="requestClose(session)"')
  })

  it('uses an adaptive grid with no viewport subtraction', () => {
    expect(source).toContain(':data-pane-count="workspace.visibleIds.length"')
    expect(source).toContain('grid-template-columns: repeat(2, minmax(0, 1fr));')
    expect(source).not.toContain('calc(100dvh - 176px)')
  })
})
