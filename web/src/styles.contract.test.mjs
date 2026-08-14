import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { describe, expect, it } from 'vitest'

const styles = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), 'styles.css'), 'utf8')

describe('global visual consistency', () => {
  it('uses the same soft blue selected-card language for sidebar navigation', () => {
    expect(styles).toContain('.side-menu .el-menu-item.is-active {\n  color: var(--aifar-primary);')
    expect(styles).toContain('background: #e6f4ff;')
    expect(styles).toContain('border-color: #91caff;')
    expect(styles).toContain('box-shadow: inset 3px 0 0 var(--aifar-primary), 0 1px 2px rgba(31, 58, 95, .03);')
  })

  it('keeps top metric cards aligned with runtime status cards', () => {
    expect(styles).toContain('.metric-card {\n  background: linear-gradient(180deg, #fff, #f8fbff);')
    expect(styles).toContain('border: 1px solid #d6eaff;')
    expect(styles).toContain('.metric-card .subtle-note {\n  display: inline-flex;')
    expect(styles).toContain('border-radius: 999px;')
  })

  it('uses one global card surface and list-row language across pages', () => {
    expect(styles).toContain('background: linear-gradient(180deg, rgba(255, 255, 255, .98), #f8fbff);')
    expect(styles).toContain('border: 1px solid #d6eaff;')
    expect(styles).toContain('box-shadow: var(--aifar-shadow-card);')
    expect(styles).toContain('.table-toolbar {\n  justify-content: space-between;')
    expect(styles).toContain('background: linear-gradient(180deg, #fff, #fbfdff);')
    expect(styles).toContain('.el-table {\n  border: 1px solid var(--aifar-border);')
  })

  it('centers status tags consistently across all modules', () => {
    expect(styles).toContain('.el-tag {\n  display: inline-flex;')
    expect(styles).toContain('.el-tag__content {\n  display: inline-flex;')
    expect(styles).toContain('justify-content: center;')
    expect(styles).toContain('.status-pill {\n  display: inline-flex;')
    expect(styles).toContain('line-height: 1;')
  })
})
