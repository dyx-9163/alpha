import { mkdirSync } from 'node:fs'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import { backendDir, goCommand, rootDir, withToolEnv } from './toolchain.mjs'

const binDir = process.env.AIFAR_BIN_DIR
  ? path.resolve(rootDir, process.env.AIFAR_BIN_DIR)
  : path.join(rootDir, 'bin')
mkdirSync(binDir, { recursive: true })

const targets = [
  { goos: 'linux', goarch: 'amd64', out: 'aifar-server-linux-amd64' },
  { goos: 'windows', goarch: 'amd64', out: 'aifar-server-windows-amd64.exe' }
]

for (const target of targets) {
  const env = withToolEnv({ GOOS: target.goos, GOARCH: target.goarch })
  const out = path.join(binDir, target.out)
  console.log(`[backend build] ${target.goos}/${target.goarch} -> ${out}`)
  const result = spawnSync(goCommand(), ['build', '-buildvcs=false', '-o', out, './cmd/aifar-server'], {
    cwd: backendDir,
    env,
    stdio: 'inherit'
  })
  if (result.status !== 0) process.exit(result.status ?? 1)
}
