import { readFile } from 'node:fs/promises'
import { describe, expect, it } from 'vitest'

describe('AIFAR Runtime summary responsive layout', () => {
  it('keeps the five, three, and one-column summary breakpoints', async () => {
    const css = await readFile(new URL('./runtime.css', import.meta.url), 'utf8')

    expect(css).toContain('grid-template-columns: repeat(5, minmax(0, 1fr))')
    expect(css).toContain('@media (max-width: 1440px)')
    expect(css).toContain('grid-template-columns: repeat(3, minmax(0, 1fr))')
    expect(css).toContain('@media (max-width: 900px)')
    expect(css).toContain('grid-template-columns: 1fr')
    expect(css).toContain('flex-wrap: wrap')
  })
})
