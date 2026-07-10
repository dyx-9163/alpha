import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { backendDir, goCommand, rootDir, withToolEnv } from './toolchain.mjs'

const binDir = process.env.AIFAR_BIN_DIR
  ? path.resolve(rootDir, process.env.AIFAR_BIN_DIR)
  : path.join(rootDir, 'bin')
mkdirSync(binDir, { recursive: true })

const targets = [
  { goos: 'linux', goarch: 'amd64', out: 'aifar-server-linux-amd64', pkg: './cmd/aifar-server' },
  { goos: 'windows', goarch: 'amd64', out: 'aifar-server-windows-amd64.exe', pkg: './cmd/aifar-server' },
  { goos: 'linux', goarch: 'amd64', out: 'aifar-agent-linux-amd64', pkg: './cmd/aifar-agent' }
]

for (const target of targets) {
  const env = withToolEnv({ GOOS: target.goos, GOARCH: target.goarch })
  const out = path.join(binDir, target.out)
  console.log(`[backend build] ${target.goos}/${target.goarch} -> ${out}`)
  const result = spawnSync(goCommand(), ['build', '-buildvcs=false', '-o', out, target.pkg], {
    cwd: backendDir,
    env,
    stdio: 'inherit'
  })
  if (result.error) {
    if (result.error.code === 'ENOENT') {
      console.error('[backend build] Go compiler was not found. Install Go in the configured tool root or make system Go available on PATH.')
    } else {
      console.error(`[backend build] unable to start Go compiler: ${result.error.message}`)
    }
    process.exit(1)
  }
  if (result.status !== 0) process.exit(result.status ?? 1)
}
