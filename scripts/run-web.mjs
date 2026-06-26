import { spawn } from 'node:child_process'
import path from 'node:path'
import { webDir, withToolEnv } from './toolchain.mjs'

const viteBin = path.join(webDir, 'node_modules', 'vite', 'bin', 'vite.js')
const child = spawn(process.execPath, [viteBin, '--host', '0.0.0.0'], {
  cwd: webDir,
  env: withToolEnv(),
  stdio: 'inherit',
  shell: false
})

child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal)
  process.exit(code ?? 0)
})
