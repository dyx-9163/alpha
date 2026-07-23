import { readdirSync } from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { fileURLToPath } from 'node:url'

const toolsDir = path.dirname(fileURLToPath(import.meta.url))
const ignoredDirectories = new Set(['bin', 'obj', 'node_modules'])

export function discoverToolTests(rootDirectory = toolsDir) {
  const tests = []

  function visit(directory) {
    for (const entry of readdirSync(directory, { withFileTypes: true })) {
      const entryPath = path.join(directory, entry.name)
      if (entry.isDirectory()) {
        if (!ignoredDirectories.has(entry.name)) visit(entryPath)
        continue
      }
      if (entry.isFile() && entry.name.endsWith('.test.mjs')) tests.push(entryPath)
    }
  }

  visit(path.resolve(rootDirectory))
  return tests.sort()
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const tests = discoverToolTests()
  if (tests.length === 0) {
    console.error('[tools test] no *.test.mjs files found')
    process.exit(1)
  }

  const result = spawnSync(process.execPath, ['--test', ...tests], {
    cwd: path.resolve(toolsDir, '..'),
    stdio: 'inherit'
  })
  if (result.error) {
    console.error(`[tools test] unable to start Node test runner: ${result.error.message}`)
    process.exit(1)
  }
  process.exit(result.status ?? 1)
}
