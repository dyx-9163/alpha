import { spawn } from 'node:child_process'
import { backendDir, goCommand, withToolEnv } from './toolchain.mjs'

const child = spawn(goCommand(), ['run', './cmd/aifar-server'], {
  cwd: backendDir,
  env: withToolEnv(),
  stdio: 'inherit',
  shell: false
})

child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal)
  process.exit(code ?? 0)
})
