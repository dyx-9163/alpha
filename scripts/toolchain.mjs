import { existsSync, readFileSync } from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { parseDefaultsEnv } from './runtime-security-config.mjs'

export const rootDir = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
export const backendDir = path.join(rootDir, 'backend')
export const webDir = path.join(rootDir, 'web')
export const toolsDir = Object.hasOwn(process.env, 'AIFAR_TOOL_ROOT')
  ? process.env.AIFAR_TOOL_ROOT
  : 'D:\\tools'

function loadDefaultsEnv(file = path.join(rootDir, 'config', 'defaults.env')) {
  if (!existsSync(file)) {
    return {}
  }
  return parseDefaultsEnv(readFileSync(file, 'utf8'))
}

function configuredValue(processEnv, defaults, key, fallback) {
  if (Object.hasOwn(processEnv, key)) return processEnv[key]
  if (Object.hasOwn(defaults, key)) return defaults[key]
  return fallback
}

function configuredPath(processEnv, defaults, key, fallback, baseDir) {
  if (Object.hasOwn(processEnv, key)) return processEnv[key]
  if (!Object.hasOwn(defaults, key)) return fallback
  const value = defaults[key]
  if (value === '' || path.isAbsolute(value)) return value
  return path.resolve(baseDir, value)
}

export function withToolEnv(extra = {}, options = {}) {
  const processEnv = options.processEnv ?? process.env
  const platform = options.platform ?? process.platform
  const effectiveRoot = options.rootPath ?? rootDir
  const effectiveWebDir = path.join(effectiveRoot, 'web')
  const effectiveToolsDir = options.toolRoot ?? (Object.hasOwn(processEnv, 'AIFAR_TOOL_ROOT')
    ? processEnv.AIFAR_TOOL_ROOT
    : toolsDir)
  const defaults = options.defaults ?? loadDefaultsEnv(options.defaultsFile ?? path.join(effectiveRoot, 'config', 'defaults.env'))
  const nodeDir = path.join(effectiveToolsDir, 'node')
  const nodeGlobalDir = path.join(effectiveToolsDir, 'node-global')
  const goDir = path.join(effectiveToolsDir, 'go')
  const gopath = path.join(effectiveToolsDir, 'gopath')
  const goBin = path.join(goDir, 'bin')
  const goExecutable = path.join(goBin, platform === 'win32' ? 'go.exe' : 'go')
  const bundledGo = effectiveToolsDir !== '' && existsSync(goExecutable)
  const pathKey = platform === 'win32'
    ? Object.keys(processEnv).find((key) => key.toLowerCase() === 'path') ?? 'Path'
    : 'PATH'
  const pathPresent = Object.hasOwn(processEnv, pathKey)
  const currentPath = pathPresent ? processEnv[pathKey] : ''
  const toolPaths = effectiveToolsDir === ''
    ? []
    : [nodeDir, nodeGlobalDir, goBin, path.join(gopath, 'bin')].filter(existsSync)
  const managedPath = toolPaths.join(path.delimiter)
  const effectivePath = toolPaths.length === 0 || (pathPresent && currentPath === '')
    ? currentPath
    : pathPresent
      ? `${managedPath}${path.delimiter}${currentPath}`
      : managedPath
  const gocache = Object.hasOwn(processEnv, 'GOCACHE')
    ? processEnv.GOCACHE
    : configuredPath(processEnv, defaults, 'AIFAR_GO_CACHE', path.join(effectiveRoot, '.cache', 'go-build'), effectiveRoot)
  const defaultPassword = configuredValue(processEnv, defaults, 'AIFAR_DEFAULT_PASSWORD', 'Oversea.123')
  const devAddr = configuredValue(processEnv, defaults, 'AIFAR_DEV_ADDR', '127.0.0.1:8080')
  const viteHost = configuredValue(processEnv, defaults, 'AIFAR_VITE_HOST', '127.0.0.1')
  const env = {
    ...processEnv,
    GOCACHE: gocache,
    AIFAR_ADDR: configuredValue(processEnv, defaults, 'AIFAR_ADDR', '0.0.0.0:8080'),
    AIFAR_DEV_ADDR: devAddr,
    AIFAR_VITE_HOST: viteHost,
    AIFAR_STATIC_DIR: configuredPath(processEnv, defaults, 'AIFAR_STATIC_DIR', path.join(effectiveWebDir, 'dist'), effectiveRoot),
    AIFAR_RESOURCE_DIR: configuredPath(processEnv, defaults, 'AIFAR_RESOURCE_DIR', path.join(effectiveRoot, 'resources'), effectiveRoot),
    AIFAR_DATABASE_PATH: configuredPath(processEnv, defaults, 'AIFAR_DATABASE_PATH', path.join(effectiveRoot, 'data', 'aifar.db'), effectiveRoot),
    AIFAR_DEFAULT_PASSWORD: defaultPassword,
    AIFAR_BOOTSTRAP_PASSWORD: configuredValue(processEnv, defaults, 'AIFAR_BOOTSTRAP_PASSWORD', defaultPassword),
    AIFAR_DEFAULT_DEPLOY_DIR: configuredValue(processEnv, defaults, 'AIFAR_DEFAULT_DEPLOY_DIR', '/aifar/apps'),
    AIFAR_INSTALLER_TEMPLATE_DIR: configuredPath(processEnv, defaults, 'AIFAR_INSTALLER_TEMPLATE_DIR', path.join(effectiveRoot, 'config', 'installers'), effectiveRoot),
    AIFAR_MAX_REQUEST_BODY_BYTES: configuredValue(processEnv, defaults, 'AIFAR_MAX_REQUEST_BODY_BYTES', String(4 * 1024 * 1024 * 1024)),
    ...extra
  }
  if (bundledGo) {
    if (!Object.hasOwn(processEnv, 'GOROOT')) env.GOROOT = goDir
    if (!Object.hasOwn(processEnv, 'GOPATH')) env.GOPATH = gopath
  }
  if (effectiveToolsDir !== '' && existsSync(nodeGlobalDir) && !Object.hasOwn(processEnv, 'NPM_CONFIG_PREFIX')) {
    env.NPM_CONFIG_PREFIX = nodeGlobalDir
  }
  for (const key of Object.keys(env)) {
    if (key.toLowerCase() === 'path') delete env[key]
  }
  env[pathKey] = effectivePath
  return env
}

export function command(name) {
  if (process.platform !== 'win32') return name
  return `${name}.cmd`
}

export function goCommand(options = {}) {
  const platform = options.platform ?? process.platform
  const effectiveToolsDir = options.toolRoot ?? toolsDir
  const exe = platform === 'win32' ? 'go.exe' : 'go'
  if (effectiveToolsDir === '') return exe
  const fromTools = path.join(effectiveToolsDir, 'go', 'bin', exe)
  return existsSync(fromTools) ? fromTools : exe
}
