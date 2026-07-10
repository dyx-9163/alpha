import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import {
  chmodSync,
  closeSync,
  copyFileSync,
  cpSync,
  existsSync,
  mkdirSync,
  openSync,
  readdirSync,
  readSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync
} from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

import { assertReleaseDefaultsFile } from './runtime-security-config.mjs'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptDir, '..')
const deploymentDir = path.join(rootDir, 'deploy', 'deployment')
const buildBinDir = path.join(rootDir, 'deploy', 'bin')
const buildWebDistDir = path.join(rootDir, 'deploy', 'dist')
const packageJson = JSON.parse(readFileSync(path.join(rootDir, 'package.json'), 'utf8'))
const baseName = `${packageJson.name}-${packageJson.version}`
const warnings = []
const hashBuffer = Buffer.allocUnsafe(8 * 1024 * 1024)

const commonEntries = [
  { kind: 'dir', source: 'deploy/dist', target: 'web/dist', required: true },
  { kind: 'dir', source: 'resources', target: 'resources', required: true },
  { kind: 'dir', source: 'config', target: 'config', required: true },
  { kind: 'file', source: 'README.md', target: 'README.md', required: false }
]

const targets = [
  {
    platform: 'linux',
    arch: 'amd64',
    binary: 'aifar-server-linux-amd64',
    archive: 'tar.gz',
    extraBinaries: ['aifar-agent-linux-amd64'],
    runtimeFiles: [
      { source: 'scripts/start.sh', target: 'start.sh', executable: true },
      { source: 'scripts/stop.sh', target: 'stop.sh', executable: true }
    ]
  },
  {
    platform: 'windows',
    arch: 'amd64',
    binary: 'aifar-server-windows-amd64.exe',
    archive: 'zip',
    runtimeFiles: [
      { source: 'scripts/start.ps1', target: 'start.ps1' },
      { source: 'scripts/start.bat', target: 'start.bat' }
    ]
  }
]

function ensureInside(parent, child) {
  const relative = path.relative(parent, child)
  if (relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error(`Refusing to write outside ${parent}: ${child}`)
  }
}

function removePath(targetPath) {
  ensureInside(rootDir, targetPath)
  rmSync(targetPath, { force: true, maxRetries: 5, recursive: true, retryDelay: 500 })
}

function copyEntry(entry, packageDir) {
  const sourcePath = path.join(rootDir, entry.source)
  const targetPath = path.join(packageDir, entry.target)
  if (!existsSync(sourcePath)) {
    if (entry.required) throw new Error(`Missing required package input: ${entry.source}`)
    warnings.push(`Optional package input not found, skipped: ${entry.source}`)
    return
  }

  mkdirSync(path.dirname(targetPath), { recursive: true })
  if (entry.kind === 'dir') {
    cpSync(sourcePath, targetPath, { dereference: true, force: true, recursive: true })
    return
  }
  copyFileSync(sourcePath, targetPath)
  if (entry.executable) chmodBestEffort(targetPath, 0o755)
}

function chmodBestEffort(filePath, mode) {
  try {
    chmodSync(filePath, mode)
  } catch {
    // Windows filesystems may ignore POSIX modes. The Linux archive still gets the file.
  }
}

function walkFiles(root, dir = root) {
  const files = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...walkFiles(root, fullPath))
      continue
    }
    if (entry.isFile()) files.push(path.relative(root, fullPath))
  }
  return files.sort((a, b) => a.localeCompare(b))
}

function sha256(filePath) {
  const hash = createHash('sha256')
  const fd = openSync(filePath, 'r')
  try {
    let bytesRead = 0
    do {
      bytesRead = readSync(fd, hashBuffer, 0, hashBuffer.length, null)
      if (bytesRead > 0) hash.update(hashBuffer.subarray(0, bytesRead))
    } while (bytesRead > 0)
    return hash.digest('hex')
  } finally {
    closeSync(fd)
  }
}

function writeChecksums(packageDir) {
  const lines = []
  for (const relativePath of walkFiles(packageDir)) {
    if (relativePath === 'checksums.txt') continue
    const filePath = path.join(packageDir, relativePath)
    lines.push(`${sha256(filePath)}  ${relativePath.replaceAll(path.sep, '/')}`)
  }
  writeFileSync(path.join(packageDir, 'checksums.txt'), `${lines.join('\n')}\n`, 'utf8')
}

function writeVersionFile(packageDir, target) {
  const lines = [
    `name=${packageJson.name}`,
    `version=${packageJson.version}`,
    `platform=${target.platform}`,
    `arch=${target.arch}`,
    `built_at=${new Date().toISOString()}`
  ]
  writeFileSync(path.join(packageDir, 'VERSION'), `${lines.join('\n')}\n`, 'utf8')
}

function buildPackage(target) {
  const packageName = `${baseName}-${target.platform}-${target.arch}`
  const finalPackageDir = path.join(deploymentDir, packageName)
  const stagingRoot = path.join(rootDir, 'deploy', '.stage', `${process.pid}-${Date.now().toString(36)}`)
  const packageDir = path.join(stagingRoot, packageName)
  const archivePath =
    target.archive === 'tar.gz'
      ? path.join(deploymentDir, `${packageName}.tar.gz`)
      : path.join(deploymentDir, `${packageName}.zip`)

  console.log(`[package] staging ${packageName}`)
  removePath(stagingRoot)
  removePath(archivePath)
  mkdirSync(packageDir, { recursive: true })

  for (const entry of commonEntries) copyEntry(entry, packageDir)

  assertReleaseDefaultsFile(path.join(packageDir, 'config', 'defaults.env'))

  const binarySource = path.join(buildBinDir, target.binary)
  if (!existsSync(binarySource)) {
    throw new Error(`Missing backend binary: deploy/bin/${target.binary}. Run pnpm package first.`)
  }
  const binaryTarget = path.join(packageDir, 'bin', target.binary)
  mkdirSync(path.dirname(binaryTarget), { recursive: true })
  copyFileSync(binarySource, binaryTarget)
  if (target.platform === 'linux') chmodBestEffort(binaryTarget, 0o755)

  for (const extraBinary of target.extraBinaries || []) {
    const extraSource = path.join(buildBinDir, extraBinary)
    if (!existsSync(extraSource)) {
      throw new Error(`Missing backend binary: deploy/bin/${extraBinary}. Run pnpm package first.`)
    }
    const extraTarget = path.join(packageDir, 'bin', extraBinary)
    mkdirSync(path.dirname(extraTarget), { recursive: true })
    copyFileSync(extraSource, extraTarget)
    if (target.platform === 'linux') chmodBestEffort(extraTarget, 0o755)
  }

  for (const runtimeFile of target.runtimeFiles) {
    copyEntry({ kind: 'file', required: true, ...runtimeFile }, packageDir)
  }

  writeVersionFile(packageDir, target)
  writeChecksums(packageDir)
  const archiveCreated = createArchive(target, packageName, packageDir, archivePath)
  let finalPackageReady = false
  if (archiveCreated) {
    try {
      removePath(finalPackageDir)
      cpSync(packageDir, finalPackageDir, { dereference: true, force: true, recursive: true })
      finalPackageReady = true
    } catch (error) {
      throw new Error(`Could not refresh package directory ${path.relative(rootDir, finalPackageDir)}: ${error.message}`)
    }
  }

  try {
    removePath(stagingRoot)
  } catch (error) {
    warnings.push(`Could not remove staging directory ${path.relative(rootDir, stagingRoot)}: ${error.message}`)
  }

  if (finalPackageReady) {
    console.log(`[package] ready ${path.relative(rootDir, finalPackageDir)}`)
  } else if (archiveCreated) {
    console.log(`[package] ready ${path.relative(rootDir, archivePath)}`)
  }
}

function createArchive(target, packageName, packageDir, archivePath) {
  const assertArchiveSuccess = (result, archiveLabel) => {
    if (!result.error && result.status === 0 && !result.signal) return
    const reason = result.error?.message ?? `exit ${result.status ?? result.signal}`
    try {
      if (existsSync(archivePath)) removePath(archivePath)
    } catch (cleanupError) {
      throw new Error(`Could not create ${archiveLabel} archive for ${packageName}: ${reason}; could not remove partial archive: ${cleanupError.message}`)
    }
    throw new Error(`Could not create ${archiveLabel} archive for ${packageName}: ${reason}`)
  }
  const packageParentDir = path.dirname(packageDir)
  if (target.archive === 'tar.gz') {
    const result = spawnSync('tar', ['-czf', archivePath, '-C', packageParentDir, packageName], {
      cwd: rootDir,
      stdio: 'inherit'
    })
    assertArchiveSuccess(result, 'tar')
    console.log(`[package] archive ${path.relative(rootDir, archivePath)}`)
    return true
  }

  if (process.platform === 'win32') {
    const result = spawnSync('tar', ['-a', '-cf', archivePath, '-C', packageParentDir, packageName], {
      cwd: rootDir,
      stdio: 'inherit'
    })
    assertArchiveSuccess(result, 'zip')
    console.log(`[package] archive ${path.relative(rootDir, archivePath)}`)
    return true
  }

  const result = spawnSync('zip', ['-qr', archivePath, packageName], {
    cwd: packageParentDir,
    stdio: 'inherit'
  })
  assertArchiveSuccess(result, 'zip')
  console.log(`[package] archive ${path.relative(rootDir, archivePath)}`)
  return true
}

function ensureRequiredBuildOutputs() {
  assertReleaseDefaultsFile(path.join(rootDir, 'config', 'defaults.env'))
  for (const entry of commonEntries.filter((item) => item.required)) {
    const sourcePath = path.join(rootDir, entry.source)
    if (!existsSync(sourcePath)) {
      throw new Error(`Missing required package input: ${entry.source}`)
    }
  }

  const webStat = existsSync(buildWebDistDir) ? statSync(buildWebDistDir) : undefined
  if (!webStat?.isDirectory()) {
    throw new Error('Missing deploy/dist. Run pnpm package before staging a release.')
  }
}

ensureRequiredBuildOutputs()
mkdirSync(deploymentDir, { recursive: true })
try {
  for (const target of targets) buildPackage(target)
} finally {
  const stagingRoot = path.join(rootDir, 'deploy', '.stage')
  if (existsSync(stagingRoot)) rmSync(stagingRoot, { recursive: true, force: true })
}

if (warnings.length) {
  console.warn('\n[package] warnings:')
  for (const warning of warnings) console.warn(`- ${warning}`)
}

console.log(`\n[package] release artifacts generated under ${path.relative(rootDir, deploymentDir) || deploymentDir}`)
