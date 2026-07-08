import { spawnSync } from 'node:child_process'
import { rootDir, withToolEnv } from './toolchain.mjs'

const steps = [
  ['local', ['scripts/test-local.mjs']],
  ['server readonly', ['scripts/test-server-readonly.mjs']]
]

for (const [label, args] of steps) {
  console.log(`\n[test:all] ${label}`)
  const result = spawnSync(process.execPath, args, {
    cwd: rootDir,
    env: withToolEnv(),
    stdio: 'inherit'
  })
  if (result.status !== 0) {
    console.error(`[test:all] ${label} failed`)
    process.exit(result.status ?? 1)
  }
}

console.log('\n[test:all] PASS')
