import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

const defaultsUrl = new URL('../config/defaults.env', import.meta.url)

function parsedDefaults() {
  const entries = new Map()
  for (const rawLine of readFileSync(defaultsUrl, 'utf8').split(/\r?\n/)) {
    const line = rawLine.trim()
    if (!line || line.startsWith('#')) continue
    const equals = line.indexOf('=')
    if (equals <= 0) continue
    entries.set(line.slice(0, equals), line.slice(equals + 1))
  }
  return entries
}

test('repository defaults configure bounded local diagnostic exports', () => {
  const defaults = parsedDefaults()
  assert.equal(defaults.get('AIFAR_DIAGNOSTIC_EXPORT_DIR'), 'data/diagnostic-exports')
  assert.equal(defaults.get('AIFAR_DIAGNOSTIC_EXPORT_RETENTION_HOURS'), '24')
  assert.equal(defaults.get('AIFAR_DIAGNOSTIC_EXPORT_QUOTA_BYTES'), '5368709120')
})
