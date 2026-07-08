import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { backendDir, goCommand, rootDir, withToolEnv } from './toolchain.mjs'

const args = [
  'run',
  './cmd/aifar-smoke',
  'e2e-mutating',
  '--database',
  process.env.AIFAR_DATABASE_PATH || path.join(rootDir, 'data', 'aifar.db'),
  '--output-dir',
  process.env.AIFAR_SMOKE_OUTPUT_DIR || path.join(rootDir, 'outputs', 'test-runs')
]

if (process.env.AIFAR_E2E_SERVER_IDS) {
  args.push('--server-ids', process.env.AIFAR_E2E_SERVER_IDS)
}

const result = spawnSync(goCommand(), args, {
  cwd: backendDir,
  env: withToolEnv(),
  stdio: 'inherit'
})

process.exit(result.status ?? 1)
