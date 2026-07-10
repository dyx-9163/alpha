import { readdirSync } from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const tests = readdirSync(scriptsDir, { withFileTypes: true })
  .filter((entry) => entry.isFile() && entry.name.endsWith('.test.mjs'))
  .map((entry) => path.join(scriptsDir, entry.name))
  .sort()

if (tests.length === 0) {
  console.error('[scripts test] no *.test.mjs files found')
  process.exit(1)
}

const result = spawnSync(process.execPath, ['--test', ...tests], {
  cwd: path.resolve(scriptsDir, '..'),
  stdio: 'inherit'
})

if (result.error) {
  console.error(`[scripts test] unable to start Node test runner: ${result.error.message}`)
  process.exit(1)
}
process.exit(result.status ?? 1)
