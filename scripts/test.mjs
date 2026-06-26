import { spawnSync } from 'node:child_process'
import { backendDir, goCommand, withToolEnv } from './toolchain.mjs'

const result = spawnSync(goCommand(), ['test', './...'], {
  cwd: backendDir,
  env: withToolEnv(),
  stdio: 'inherit'
})

process.exit(result.status ?? 1)
