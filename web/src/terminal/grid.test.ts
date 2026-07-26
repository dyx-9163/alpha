import { describe, expect, it } from 'vitest'

import { calculateTerminalGrid } from './grid'

describe('calculateTerminalGrid', () => {
  it('includes xterm line height when calculating visible rows', () => {
    expect(calculateTerminalGrid({
      width: 800,
      height: 400,
      measuredCellWidth: 8,
      measuredCharHeight: 15,
      lineHeight: 1.2
    })).toEqual({ cols: 99, rows: 22 })
  })

  it('uses safe defaults when xterm has not measured a character yet', () => {
    expect(calculateTerminalGrid({
      width: 800,
      height: 400,
      measuredCellWidth: 0,
      measuredCharHeight: 0,
      lineHeight: 1.2
    })).toEqual({ cols: 99, rows: 19 })
  })
})
