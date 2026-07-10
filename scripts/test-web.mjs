import { spawnSync } from 'node:child_process'
import path from 'node:path'

import { webDir, withToolEnv } from './toolchain.mjs'

const vitestBin = path.join(webDir, 'node_modules', 'vitest', 'vitest.mjs')
const result = spawnSync(process.execPath, [vitestBin, 'run'], {
  cwd: webDir,
  env: withToolEnv(),
  stdio: 'inherit'
})

process.exit(result.status ?? 1)
