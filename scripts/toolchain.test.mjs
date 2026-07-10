import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { goCommand, withToolEnv } from './toolchain.mjs'

function temporaryToolRoot(t) {
  const directory = mkdtempSync(path.join(tmpdir(), 'aifar-toolchain-'))
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  return directory
}

test('process values, including explicit empty strings, win over defaults and fallbacks', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const env = withToolEnv({}, {
    processEnv: {
      PATH: '',
      AIFAR_ADDR: '',
      AIFAR_DEFAULT_PASSWORD: '',
      AIFAR_STATIC_DIR: ''
    },
    defaults: {
      AIFAR_ADDR: 'defaults:8080',
      AIFAR_DEFAULT_PASSWORD: 'defaults-password',
      AIFAR_STATIC_DIR: 'defaults-static'
    },
    platform: 'linux',
    toolRoot
  })

  assert.equal(env.PATH, '')
  assert.equal(env.AIFAR_ADDR, '')
  assert.equal(env.AIFAR_DEFAULT_PASSWORD, '')
  assert.equal(env.AIFAR_BOOTSTRAP_PASSWORD, '')
  assert.equal(env.AIFAR_STATIC_DIR, '')
})

test('defaults values, including explicit empty strings, win over hard-coded fallbacks', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const env = withToolEnv({}, {
    processEnv: { PATH: 'system-path' },
    defaults: {
      AIFAR_ADDR: '',
      AIFAR_DEFAULT_DEPLOY_DIR: '',
      AIFAR_MAX_REQUEST_BODY_BYTES: ''
    },
    platform: 'linux',
    toolRoot
  })

  assert.equal(env.AIFAR_ADDR, '')
  assert.equal(env.AIFAR_DEFAULT_DEPLOY_DIR, '')
  assert.equal(env.AIFAR_MAX_REQUEST_BODY_BYTES, '')
})

test('relative paths from defaults resolve from the repository root while process paths remain verbatim', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const rootPath = temporaryToolRoot(t)
  const env = withToolEnv({}, {
    processEnv: {
      PATH: 'system-path',
      AIFAR_STATIC_DIR: 'process-relative-static'
    },
    defaults: {
      AIFAR_STATIC_DIR: 'web/dist',
      AIFAR_RESOURCE_DIR: 'resources',
      AIFAR_DATABASE_PATH: 'data/aifar.db',
      AIFAR_INSTALLER_TEMPLATE_DIR: 'config/installers'
    },
    platform: 'linux',
    rootPath,
    toolRoot
  })

  assert.equal(env.AIFAR_STATIC_DIR, 'process-relative-static')
  assert.equal(env.AIFAR_RESOURCE_DIR, path.resolve(rootPath, 'resources'))
  assert.equal(env.AIFAR_DATABASE_PATH, path.resolve(rootPath, 'data/aifar.db'))
  assert.equal(env.AIFAR_INSTALLER_TEMPLATE_DIR, path.resolve(rootPath, 'config/installers'))
})

test('defaults Go cache paths resolve from the repository root while process cache values stay verbatim', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const rootPath = temporaryToolRoot(t)
  const fromDefaults = withToolEnv({}, {
    processEnv: { PATH: 'system-path' },
    defaults: { AIFAR_GO_CACHE: '.cache/custom-go' },
    platform: 'linux',
    rootPath,
    toolRoot
  })
  const fromAifarProcessEnv = withToolEnv({}, {
    processEnv: { PATH: 'system-path', AIFAR_GO_CACHE: 'process-relative-cache' },
    defaults: { AIFAR_GO_CACHE: '.cache/default-go' },
    platform: 'linux',
    rootPath,
    toolRoot
  })
  const fromEmptyAifarProcessEnv = withToolEnv({}, {
    processEnv: { PATH: 'system-path', AIFAR_GO_CACHE: '' },
    defaults: { AIFAR_GO_CACHE: '.cache/default-go' },
    platform: 'linux',
    rootPath,
    toolRoot
  })
  const fromGoProcessEnv = withToolEnv({}, {
    processEnv: { PATH: 'system-path', GOCACHE: '', AIFAR_GO_CACHE: 'ignored-cache' },
    defaults: { AIFAR_GO_CACHE: '.cache/default-go' },
    platform: 'linux',
    rootPath,
    toolRoot
  })

  assert.equal(fromDefaults.GOCACHE, path.resolve(rootPath, '.cache/custom-go'))
  assert.equal(fromAifarProcessEnv.GOCACHE, 'process-relative-cache')
  assert.equal(fromEmptyAifarProcessEnv.GOCACHE, '')
  assert.equal(fromGoProcessEnv.GOCACHE, '')
})

test('Windows Path aliases collapse to one deterministic key used by child processes', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const env = withToolEnv({}, {
    processEnv: { Path: 'chosen-path', PATH: 'shadow-path' },
    defaults: {},
    platform: 'win32',
    toolRoot
  })
  const aliases = Object.entries(env).filter(([key]) => key.toLowerCase() === 'path')

  assert.deepEqual(aliases, [['Path', 'chosen-path']])
  const child = spawnSync(process.execPath, ['-e', "process.stdout.write(process.env.Path ?? process.env.PATH ?? '')"], {
    env,
    encoding: 'utf8'
  })
  assert.equal(child.status, 0, child.stderr)
  assert.equal(child.stdout, 'chosen-path')
})

test('missing tool directories do not pollute PATH or synthesize Go variables', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const env = withToolEnv({}, {
    processEnv: { PATH: 'system-path' },
    defaults: {},
    platform: 'linux',
    toolRoot
  })

  assert.equal(env.PATH, 'system-path')
  assert.equal(Object.hasOwn(env, 'GOROOT'), false)
  assert.equal(Object.hasOwn(env, 'GOPATH'), false)
  assert.equal(Object.hasOwn(env, 'NPM_CONFIG_PREFIX'), false)
  assert.equal(goCommand({ platform: 'linux', toolRoot }), 'go')
})

test('an actual bundled Go executable enables bundled Go paths', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const goBin = path.join(toolRoot, 'go', 'bin')
  const goPathBin = path.join(toolRoot, 'gopath', 'bin')
  const nodeGlobal = path.join(toolRoot, 'node-global')
  mkdirSync(goBin, { recursive: true })
  mkdirSync(goPathBin, { recursive: true })
  mkdirSync(nodeGlobal, { recursive: true })
  writeFileSync(path.join(goBin, 'go'), '')

  const env = withToolEnv({}, {
    processEnv: { PATH: 'system-path' },
    defaults: {},
    platform: 'linux',
    toolRoot
  })

  assert.equal(env.GOROOT, path.join(toolRoot, 'go'))
  assert.equal(env.GOPATH, path.join(toolRoot, 'gopath'))
  assert.equal(env.NPM_CONFIG_PREFIX, nodeGlobal)
  assert.equal(env.PATH, [nodeGlobal, goBin, goPathBin, 'system-path'].join(path.delimiter))
  assert.equal(goCommand({ platform: 'linux', toolRoot }), path.join(goBin, 'go'))
})

test('explicit empty Go and npm variables stay empty while managed PATH has no empty segment', (t) => {
  const toolRoot = temporaryToolRoot(t)
  const goBin = path.join(toolRoot, 'go', 'bin')
  const nodeGlobal = path.join(toolRoot, 'node-global')
  mkdirSync(goBin, { recursive: true })
  mkdirSync(nodeGlobal, { recursive: true })
  writeFileSync(path.join(goBin, 'go'), '')

  const env = withToolEnv({}, {
    processEnv: {
      PATH: '',
      GOROOT: '',
      GOPATH: '',
      NPM_CONFIG_PREFIX: ''
    },
    defaults: {},
    platform: 'linux',
    toolRoot
  })
  const withoutInheritedPath = withToolEnv({}, {
    processEnv: {},
    defaults: {},
    platform: 'linux',
    toolRoot
  })

  assert.equal(env.PATH, '')
  assert.equal(env.PATH.endsWith(path.delimiter), false)
  assert.equal(withoutInheritedPath.PATH, [nodeGlobal, goBin].join(path.delimiter))
  assert.equal(withoutInheritedPath.PATH.endsWith(path.delimiter), false)
  assert.equal(env.GOROOT, '')
  assert.equal(env.GOPATH, '')
  assert.equal(env.NPM_CONFIG_PREFIX, '')
})

test('backend build reports a clear error when neither bundled nor system Go can start', (t) => {
  const toolRoot = path.join(temporaryToolRoot(t), 'missing-tools')
  const binDir = temporaryToolRoot(t)
  const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
  const result = spawnSync(process.execPath, [path.join(scriptsDir, 'build-backend.mjs')], {
    cwd: path.resolve(scriptsDir, '..'),
    env: {
      ...process.env,
      AIFAR_TOOL_ROOT: toolRoot,
      AIFAR_BIN_DIR: binDir,
      PATH: '',
      Path: ''
    },
    encoding: 'utf8'
  })

  assert.notEqual(result.status, 0)
  assert.match(result.stderr, /Go compiler was not found/)
})
