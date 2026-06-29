import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

export const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
export const backendDir = path.join(rootDir, 'backend')
export const webDir = path.join(rootDir, 'web')
export const toolsDir = process.env.AIFAR_TOOL_ROOT || 'D:\\tools'

function loadDefaultsEnv() {
  const file = path.join(rootDir, 'config', 'defaults.env')
  if (!existsSync(file)) {
    return {}
  }
  return readFileSync(file, 'utf8').split(/\r?\n/).reduce((env, line) => {
    const trimmed = line.trim()
    if (!trimmed || trimmed.startsWith('#')) {
      return env
    }
    const index = trimmed.indexOf('=')
    if (index <= 0) {
      return env
    }
    env[trimmed.slice(0, index).trim()] = trimmed.slice(index + 1).trim()
    return env
  }, {})
}

export function withToolEnv(extra = {}) {
  const defaults = loadDefaultsEnv()
  const nodeDir = path.join(toolsDir, 'node')
  const nodeGlobalDir = path.join(toolsDir, 'node-global')
  const goDir = path.join(toolsDir, 'go')
  const gopath = path.join(toolsDir, 'gopath')
  const gocache = process.env.AIFAR_GO_CACHE || path.join(rootDir, '.cache', 'go-build')
  const pathKey = process.platform === 'win32' ? 'Path' : 'PATH'
  const currentPath = process.env[pathKey] || process.env.PATH || ''
  const prefix = [nodeDir, nodeGlobalDir, path.join(goDir, 'bin'), path.join(gopath, 'bin')].join(path.delimiter)
  const defaultPassword = process.env.AIFAR_DEFAULT_PASSWORD || defaults.AIFAR_DEFAULT_PASSWORD || 'Oversea.123'
  const devAddr = process.env.AIFAR_DEV_ADDR || defaults.AIFAR_DEV_ADDR || '127.0.0.1:8080'
  const viteHost = process.env.AIFAR_VITE_HOST || defaults.AIFAR_VITE_HOST || '127.0.0.1'
  return {
    ...process.env,
    [pathKey]: `${prefix}${path.delimiter}${currentPath}`,
    PATH: `${prefix}${path.delimiter}${currentPath}`,
    GOROOT: goDir,
    GOPATH: gopath,
    GOCACHE: gocache,
    NPM_CONFIG_PREFIX: nodeGlobalDir,
    AIFAR_ADDR: process.env.AIFAR_ADDR || defaults.AIFAR_ADDR || '0.0.0.0:8080',
    AIFAR_DEV_ADDR: devAddr,
    AIFAR_VITE_HOST: viteHost,
    AIFAR_STATIC_DIR: process.env.AIFAR_STATIC_DIR || path.join(webDir, 'dist'),
    AIFAR_RESOURCE_DIR: process.env.AIFAR_RESOURCE_DIR || path.join(rootDir, 'resources'),
    AIFAR_DATABASE_PATH: process.env.AIFAR_DATABASE_PATH || path.join(rootDir, 'data', 'aifar.db'),
    AIFAR_DEFAULT_PASSWORD: defaultPassword,
    AIFAR_BOOTSTRAP_PASSWORD: process.env.AIFAR_BOOTSTRAP_PASSWORD || defaultPassword,
    AIFAR_DEFAULT_DEPLOY_DIR: process.env.AIFAR_DEFAULT_DEPLOY_DIR || defaults.AIFAR_DEFAULT_DEPLOY_DIR || '/aifar/apps',
    ...extra
  }
}

export function command(name) {
  if (process.platform !== 'win32') return name
  return `${name}.cmd`
}

export function goCommand() {
  const exe = process.platform === 'win32' ? 'go.exe' : 'go'
  const fromTools = path.join(toolsDir, 'go', 'bin', exe)
  return existsSync(fromTools) ? fromTools : exe
}
