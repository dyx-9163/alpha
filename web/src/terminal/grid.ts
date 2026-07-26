export interface TerminalGridInput {
  width: number
  height: number
  measuredCellWidth: number
  measuredCharHeight: number
  lineHeight: number
}

const horizontalReserve = 2
const verticalReserve = 4

export function calculateTerminalGrid(input: TerminalGridInput) {
  const cellWidth = input.measuredCellWidth >= 4 && input.measuredCellWidth <= 20
    ? input.measuredCellWidth
    : 8
  const safeLineHeight = Number.isFinite(input.lineHeight) && input.lineHeight > 0
    ? input.lineHeight
    : 1
  const cellHeight = input.measuredCharHeight >= 10 && input.measuredCharHeight <= 32
    ? Math.ceil(input.measuredCharHeight * safeLineHeight)
    : 20
  const availableWidth = Math.max(0, input.width - horizontalReserve)
  const availableHeight = Math.max(0, input.height - verticalReserve)

  return {
    cols: Math.max(20, Math.floor(availableWidth / cellWidth)),
    rows: Math.max(8, Math.floor(availableHeight / cellHeight))
  }
}
