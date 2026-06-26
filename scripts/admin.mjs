import { spawnSync } from 'node:child_process'
import { backendDir, goCommand, withToolEnv } from './toolchain.mjs'

const args = process.argv.slice(2)
const result = spawnSync(goCommand(), ['run', './cmd/aifar-admin', ...args], {
  cwd: backendDir,
  env: withToolEnv(),
  stdio: 'inherit'
})

process.exit(result.status ?? 1)
