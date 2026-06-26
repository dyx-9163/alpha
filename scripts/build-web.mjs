import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { webDir, withToolEnv } from './toolchain.mjs'

const env = withToolEnv()
const vueTscBin = path.join(webDir, 'node_modules', 'vue-tsc', 'bin', 'vue-tsc.js')
const viteBin = path.join(webDir, 'node_modules', 'vite', 'bin', 'vite.js')

for (const [name, args] of [
  ['vue-tsc', [vueTscBin, '--noEmit']],
  ['vite', [viteBin, 'build']]
]) {
  const result = spawnSync(process.execPath, args, { cwd: webDir, env, stdio: 'inherit' })
  if (result.status !== 0) {
    console.error(`[web build] ${name} failed`)
    process.exit(result.status ?? 1)
  }
}
