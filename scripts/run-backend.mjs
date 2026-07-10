import { spawn } from 'node:child_process'
import { developmentSecurityEnv, selectDevelopmentAddress } from './development-security.mjs'
import { backendDir, goCommand, withToolEnv } from './toolchain.mjs'

const baseEnv = withToolEnv()
const backendAddr = selectDevelopmentAddress(process.env, baseEnv, 'AIFAR_ADDR')
const child = spawn(goCommand(), ['run', './cmd/aifar-server'], {
  cwd: backendDir,
  env: withToolEnv({
    AIFAR_ADDR: backendAddr,
    ...developmentSecurityEnv(backendAddr, process.env)
  }),
  stdio: 'inherit',
  shell: false
})

child.on('exit', (code, signal) => {
  if (signal) process.kill(process.pid, signal)
  process.exit(code ?? 0)
})
