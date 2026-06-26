import { spawn } from 'node:child_process'
import { rootDir, withToolEnv } from './toolchain.mjs'

const env = withToolEnv()
const children = []

function start(name, cmd, args, cwd, shell = false) {
  const child = spawn(cmd, args, { cwd, env, shell })
  children.push(child)
  child.stdout?.on('data', (data) => process.stdout.write(`[${name}] ${data}`))
  child.stderr?.on('data', (data) => process.stderr.write(`[${name}] ${data}`))
  child.on('exit', (code, signal) => {
    if (shuttingDown) return
    console.log(`[${name}] exited ${signal || code}`)
    shutdown(code || 1)
  })
}

let shuttingDown = false
function shutdown(code = 0) {
  shuttingDown = true
  for (const child of children) {
    if (!child.killed) child.kill(process.platform === 'win32' ? 'SIGTERM' : 'SIGTERM')
  }
  setTimeout(() => process.exit(code), 300)
}

process.on('SIGINT', () => shutdown(0))
process.on('SIGTERM', () => shutdown(0))

console.log('AIFAR dev starting...')
console.log('Backend: http://127.0.0.1:8080')
console.log('Frontend: http://127.0.0.1:5173')

start('api', process.execPath, ['scripts/run-backend.mjs'], rootDir)
start('web', process.execPath, ['scripts/run-web.mjs'], rootDir)
