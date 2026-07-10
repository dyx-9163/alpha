import { spawnSync } from 'node:child_process'
import { rootDir, withToolEnv } from './toolchain.mjs'

const steps = [
  ['backend tests', process.execPath, ['scripts/test.mjs']],
  ['web tests', process.execPath, ['scripts/test-web.mjs']],
  ['script tests', process.execPath, ['scripts/test-scripts.mjs']],
  ['web build', process.execPath, ['scripts/build-web.mjs']],
  ['backend build', process.execPath, ['scripts/build-backend.mjs']],
  ['package', process.execPath, ['scripts/package-build.mjs']],
  ['verify release checksums', process.execPath, ['scripts/verify-release-checksums.mjs']]
]

for (const [label, command, args] of steps) {
  console.log(`\n[test:local] ${label}`)
  const result = spawnSync(command, args, {
    cwd: rootDir,
    env: withToolEnv(),
    stdio: 'inherit'
  })
  if (result.status !== 0) {
    console.error(`[test:local] ${label} failed`)
    process.exit(result.status ?? 1)
  }
}

console.log('\n[test:local] PASS')
