import { describe, expect, it } from 'vitest'

import source from './TerminalView.vue?raw'

describe('terminal viewport contract', () => {
  it('uses remaining flex space instead of viewport subtraction', () => {
    expect(source).toContain('flex: 1 1 auto;')
    expect(source).toContain('min-height: 360px;')
    expect(source).not.toContain('calc(100dvh - 176px)')
  })

  it('keeps large command output scrollable', () => {
    expect(source).toContain('scrollback: 10000')
    expect(source).toMatch(/\.terminal-box :deep\(\.xterm-viewport\)[\s\S]*overflow-y: auto/)
  })
})
