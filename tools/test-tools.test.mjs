import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import test from 'node:test'
import { discoverToolTests } from './test-tools.mjs'

test('discovers tool tests recursively and excludes build outputs', (t) => {
  const root = mkdtempSync(path.join(tmpdir(), 'aifar-tool-tests-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  for (const relative of [
    'root.test.mjs',
    'nested/child.test.mjs',
    'nested/bin/ignored.test.mjs',
    'obj/ignored.test.mjs'
  ]) {
    const file = path.join(root, relative)
    mkdirSync(path.dirname(file), { recursive: true })
    writeFileSync(file, '')
  }

  assert.deepEqual(
    discoverToolTests(root).map((file) => path.relative(root, file).replaceAll('\\', '/')),
    ['nested/child.test.mjs', 'root.test.mjs']
  )
})
