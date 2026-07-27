// @ts-expect-error Vitest supplies the Node runtime module; the web build omits Node typings.
import { readFileSync } from 'node:fs'
import { describe, expect, it } from 'vitest'

const source = readFileSync(new URL('./AifarRuntimeLogsTab.vue', import.meta.url), 'utf8')
const diagnosticsSource = readFileSync(new URL('./AifarRuntimeDiagnosticsPanel.vue', import.meta.url), 'utf8')

describe('AIFAR Runtime focused logs workspace', () => {
  it('separates realtime logs and diagnostic archives', () => {
    expect(source).toContain('v-model="runtimeLogWorkspaceTab"')
    expect(source).toContain('runtimeLogWorkspaceTabOrder')
    expect(source).toContain(':name="tabName"')
    expect(source).toContain("tabName === 'archives'")
    expect(source).toContain('<AifarRuntimeDiagnosticsPanel')
  })

  it('uses a compact six-column diagnostic table', () => {
    expect(diagnosticsSource.match(/<el-table-column/g) ?? []).toHaveLength(6)
    expect(diagnosticsSource).toContain('runtimeDiagnosticServicePreview')
    expect(diagnosticsSource).not.toContain('max-height="280"')
  })
})
