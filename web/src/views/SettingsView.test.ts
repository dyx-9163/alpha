import { describe, expect, it } from 'vitest'
import source from './SettingsView.vue?raw'

describe('SettingsView maintenance tabs', () => {
  it('keeps database backup maintenance separate from log maintenance', () => {
    expect(source).toContain("name=\"maintenance\"")
    expect(source).toContain('<DataMaintenancePanel')
    expect(source).toContain("name=\"logs\"")
    expect(source).toContain('<LogMaintenancePanel')
    expect(source).toContain(':log-retention-days="form.logRetentionDays"')
    expect(source).not.toContain('audit-retention-days')
    expect(source).not.toContain('task-retention-days')
  })
})
