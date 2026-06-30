import { spawnSync } from 'node:child_process'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptDir, '..')

const env = {
  ...process.env,
  AIFAR_BIN_DIR: path.join('deploy', 'bin'),
  AIFAR_WEB_OUT_DIR: path.join('deploy', 'dist')
}

function run(label, command, args) {
  console.log(`[package] ${label}`)
  const result = spawnSync(command, args, {
    cwd: rootDir,
    env,
    stdio: 'inherit'
  })
  if (result.status !== 0) process.exit(result.status ?? 1)
}

run('build web -> deploy/dist', process.execPath, ['scripts/build-web.mjs'])
run('build backend -> deploy/bin', process.execPath, ['scripts/build-backend.mjs'])
run('stage release -> deploy/deployment', process.execPath, ['scripts/package-release.mjs'])
