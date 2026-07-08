import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { backendDir, goCommand, rootDir, withToolEnv } from './toolchain.mjs'

const args = [
  'run',
  './cmd/aifar-smoke',
  'readonly',
  '--database',
  process.env.AIFAR_DATABASE_PATH || path.join(rootDir, 'data', 'aifar.db'),
  '--output-dir',
  process.env.AIFAR_SMOKE_OUTPUT_DIR || path.join(rootDir, 'outputs', 'test-runs')
]

const result = spawnSync(goCommand(), args, {
  cwd: backendDir,
  env: withToolEnv(),
  stdio: 'inherit'
})

process.exit(result.status ?? 1)
