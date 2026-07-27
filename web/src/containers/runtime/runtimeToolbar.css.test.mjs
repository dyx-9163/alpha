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

  it('wraps log controls before the desktop workspace becomes cramped', async () => {
    const css = await readFile(new URL('./runtime.css', import.meta.url), 'utf8')

    expect(css).toMatch(/@media \(max-width: 1440px\) \{(?:(?!@media)[\s\S])*?\.runtime-tab-toolbar\s*\{[^}]*flex-wrap:\s*wrap/)
    expect(css).toMatch(/@media \(max-width: 1440px\) \{(?:(?!@media)[\s\S])*?\.runtime-log-filters\s*\{[^}]*flex-wrap:\s*wrap/)
  })

  it('provides a page scroll escape and relaxes the fixed-height chain at narrow or low viewports', async () => {
    const runtimeCss = await readFile(new URL('./runtime.css', import.meta.url), 'utf8')
    const containersView = await readFile(new URL('../../views/ContainersView.vue', import.meta.url), 'utf8')
    const escapeMedia = /@media \(max-width: 900px\), \(max-height: 600px\) \{(?:(?!@media)[\s\S])*?/

    expect(containersView).toMatch(new RegExp(`${escapeMedia.source}\\.containers-page\\.is-runtime-logs-page\\s*\\{[^}]*overflow-y:\\s*auto`))
    expect(containersView).toMatch(new RegExp(`${escapeMedia.source}\\.workspace-card\\.containers-main\\.is-runtime-logs\\s*\\{[^}]*height:\\s*auto[^}]*overflow:\\s*visible`))
    expect(runtimeCss).toMatch(new RegExp(`${escapeMedia.source}\\.workspace-card\\.containers-main\\.is-runtime-logs \\.runtime-workspace[^{]*\\{[^}]*height:\\s*auto[^}]*overflow:\\s*visible`))
    expect(runtimeCss).toMatch(new RegExp(`${escapeMedia.source}\\.runtime-log-virtual-list\\s*\\{[^}]*min-height:\\s*min\\(240px, 45vh\\)`))
  })
})
