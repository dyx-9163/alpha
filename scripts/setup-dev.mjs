import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync } from 'node:fs'
import path from 'node:path'
import { backendDir, goCommand, rootDir, toolsDir, withToolEnv } from './toolchain.mjs'

const env = withToolEnv()
mkdirSync(env.GOCACHE, { recursive: true })
mkdirSync(path.join(toolsDir, 'gopath'), { recursive: true })

if (!existsSync(backendDir)) {
  process.exit(0)
}

const go = goCommand()
const check = spawnSync(go, ['version'], { env, stdio: 'ignore' })
if (check.status !== 0) {
  console.warn('[aifar setup] Go not found. Install Go under D:\\tools or set AIFAR_TOOL_ROOT before backend commands.')
  process.exit(0)
}

console.log('[aifar setup] downloading Go modules...')
const result = spawnSync(go, ['mod', 'download'], { cwd: backendDir, env, stdio: 'inherit' })
if (result.status !== 0) {
  console.warn('[aifar setup] Go module download failed. You can retry with: pnpm backend:test')
  process.exit(0)
}
console.log(`[aifar setup] ready: ${rootDir}`)
