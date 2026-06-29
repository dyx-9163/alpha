import { spawn } from 'node:child_process'
import path from 'node:path'
import { webDir, withToolEnv } from './toolchain.mjs'

const env = withToolEnv()
const viteBin = path.join(webDir, 'node_modules', 'vite', 'bin', 'vite.js')
const child = spawn(process.execPath, [viteBin, '--host', env.AIFAR_VITE_HOST || '127.0.0.1'], {
  cwd: webDir,
  env,
  stdio: 'inherit',
  shell: false
})

child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal)
  process.exit(code ?? 0)
})
