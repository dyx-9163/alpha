import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import {
  chmodSync,
  copyFileSync,
  cpSync,
  existsSync,
  mkdirSync,
  readdirSync,
  readFileSync,
  rmSync,
  statSync,
  writeFileSync
} from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const scriptDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptDir, '..')
const deploymentDir = path.join(rootDir, 'deploy', 'deployment')
const buildBinDir = path.join(rootDir, 'deploy', 'bin')
const buildWebDistDir = path.join(rootDir, 'deploy', 'dist')
const packageJson = JSON.parse(readFileSync(path.join(rootDir, 'package.json'), 'utf8'))
const baseName = `${packageJson.name}-${packageJson.version}`
const warnings = []

const commonEntries = [
  { kind: 'dir', source: 'deploy/dist', target: 'web/dist', required: true },
  { kind: 'dir', source: 'resources', target: 'resources', required: false },
  { kind: 'dir', source: 'config', target: 'config', required: true },
  { kind: 'file', source: 'README.md', target: 'README.md', required: false }
]

const targets = [
  {
    platform: 'linux',
    arch: 'amd64',
    binary: 'aifar-server-linux-amd64',
    archive: 'tar.gz',
    runtimeFiles: [
      { source: 'deploy/deployment/start.sh', target: 'start.sh', executable: true },
      { source: 'deploy/deployment/stop.sh', target: 'stop.sh', executable: true }
    ]
  },
  {
    platform: 'windows',
    arch: 'amd64',
    binary: 'aifar-server-windows-amd64.exe',
    archive: 'zip',
    runtimeFiles: [
      { source: 'deploy/deployment/start.ps1', target: 'start.ps1' },
      { source: 'deploy/deployment/start.bat', target: 'start.bat' }
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
  rmSync(targetPath, { force: true, recursive: true })
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
  hash.update(readFileSync(filePath))
  return hash.digest('hex')
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
  const packageDir = path.join(deploymentDir, packageName)
  const archivePath =
    target.archive === 'tar.gz'
      ? path.join(deploymentDir, `${packageName}.tar.gz`)
      : path.join(deploymentDir, `${packageName}.zip`)

  console.log(`[package] staging ${packageName}`)
  removePath(packageDir)
  removePath(archivePath)
  mkdirSync(packageDir, { recursive: true })

  for (const entry of commonEntries) copyEntry(entry, packageDir)

  const binarySource = path.join(buildBinDir, target.binary)
  if (!existsSync(binarySource)) {
    throw new Error(`Missing backend binary: deploy/bin/${target.binary}. Run pnpm package first.`)
  }
  const binaryTarget = path.join(packageDir, 'bin', target.binary)
  mkdirSync(path.dirname(binaryTarget), { recursive: true })
  copyFileSync(binarySource, binaryTarget)
  if (target.platform === 'linux') chmodBestEffort(binaryTarget, 0o755)

  for (const runtimeFile of target.runtimeFiles) {
    copyEntry({ kind: 'file', required: true, ...runtimeFile }, packageDir)
  }

  writeVersionFile(packageDir, target)
  writeChecksums(packageDir)
  createArchive(target, packageName, packageDir, archivePath)
  console.log(`[package] ready ${path.relative(rootDir, packageDir)}`)
}

function psQuote(value) {
  return `'${value.replaceAll("'", "''")}'`
}

function createArchive(target, packageName, packageDir, archivePath) {
  if (target.archive === 'tar.gz') {
    const result = spawnSync('tar', ['-czf', archivePath, '-C', deploymentDir, packageName], {
      cwd: rootDir,
      stdio: 'inherit'
    })
    if (result.status !== 0) {
      warnings.push(`Could not create tar archive for ${packageName}; package directory was still generated.`)
      return
    }
    console.log(`[package] archive ${path.relative(rootDir, archivePath)}`)
    return
  }

  if (process.platform === 'win32') {
    const command = [
      "$ErrorActionPreference = 'Stop'",
      `Compress-Archive -LiteralPath ${psQuote(packageDir)} -DestinationPath ${psQuote(archivePath)} -Force`
    ].join('; ')
    const result = spawnSync('powershell.exe', ['-NoProfile', '-Command', command], {
      cwd: rootDir,
      stdio: 'inherit'
    })
    if (result.status !== 0) {
      warnings.push(`Could not create zip archive for ${packageName}; package directory was still generated.`)
      return
    }
    console.log(`[package] archive ${path.relative(rootDir, archivePath)}`)
    return
  }

  const result = spawnSync('zip', ['-qr', archivePath, packageName], {
    cwd: deploymentDir,
    stdio: 'inherit'
  })
  if (result.status !== 0) {
    warnings.push(`Could not create zip archive for ${packageName}; install zip or archive the package directory manually.`)
    return
  }
  console.log(`[package] archive ${path.relative(rootDir, archivePath)}`)
}

function ensureRequiredBuildOutputs() {
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
for (const target of targets) buildPackage(target)

if (warnings.length) {
  console.warn('\n[package] warnings:')
  for (const warning of warnings) console.warn(`- ${warning}`)
}

console.log(`\n[package] release artifacts generated under ${path.relative(rootDir, deploymentDir) || deploymentDir}`)
