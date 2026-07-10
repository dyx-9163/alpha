import { createHash } from 'node:crypto'
import { spawnSync } from 'node:child_process'
import { createReadStream, existsSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { rootDir } from './toolchain.mjs'
import { assertReleaseDefaultsFile } from './runtime-security-config.mjs'

const deploymentDir = path.join(rootDir, 'deploy', 'deployment')
const packageJson = JSON.parse(readFileSync(path.join(rootDir, 'package.json'), 'utf8'))
const baseName = `${packageJson.name}-${packageJson.version}`
const expectedPackages = [
  { name: `${baseName}-linux-amd64`, archive: `${baseName}-linux-amd64.tar.gz` },
  { name: `${baseName}-windows-amd64`, archive: `${baseName}-windows-amd64.zip` }
]

function sha256(filePath) {
  return new Promise((resolve, reject) => {
    const hash = createHash('sha256')
    const stream = createReadStream(filePath)
    stream.on('data', (chunk) => hash.update(chunk))
    stream.on('error', reject)
    stream.on('end', () => resolve(hash.digest('hex')))
  })
}

function deploymentEntries(predicate) {
  if (!existsSync(deploymentDir)) return []
  return readdirSync(deploymentDir, { withFileTypes: true })
    .filter(predicate)
    .map((entry) => entry.name)
    .sort((a, b) => a.localeCompare(b))
}

function assertExactSet(actual, expected, label) {
  const actualSet = new Set(actual)
  const expectedSet = new Set(expected)
  const missing = expected.filter((name) => !actualSet.has(name))
  const extra = actual.filter((name) => !expectedSet.has(name))
  if (missing.length || extra.length || actual.length !== expected.length) {
    const suffix = [
      missing.length ? `missing: ${missing.join(', ')}` : '',
      extra.length ? `extra: ${extra.join(', ')}` : ''
    ].filter(Boolean).join('; ')
    throw new Error(`[checksums] expected exactly current ${label} under ${path.relative(rootDir, deploymentDir)}${suffix ? ` (${suffix})` : ''}`)
  }
}

function parseChecksumLine(line, checksumsPath) {
  const match = line.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/)
  if (!match) {
    throw new Error(`Invalid checksum line in ${checksumsPath}: ${line}`)
  }
  return { expected: match[1].toLowerCase(), relativePath: match[2].trim() }
}

function walkFiles(root, dir = root) {
  const files = []
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name)
    if (entry.isDirectory()) {
      files.push(...walkFiles(root, fullPath))
    } else if (entry.isFile()) {
      files.push(path.relative(root, fullPath).replaceAll('\\', '/'))
    } else if (entry.isSymbolicLink()) {
      throw new Error(`Release package must not contain symlinks: ${path.relative(root, fullPath)}`)
    } else {
      throw new Error(`Release package must contain only files and directories: ${path.relative(root, fullPath)}`)
    }
  }
  return files.sort((a, b) => a.localeCompare(b))
}

function safeRelativePath(relativePath, label) {
  const normalized = relativePath.replaceAll('\\', '/')
  if (
    normalized === '' ||
    normalized.includes('\0') ||
    normalized.startsWith('/') ||
    /^[A-Za-z]:/.test(normalized) ||
    normalized.split('/').includes('..') ||
    path.isAbsolute(normalized)
  ) {
    throw new Error(`${label} escapes package directory: ${relativePath}`)
  }
  return normalized
}

function packageFilePath(packageDir, relativePath) {
  const normalizedPath = safeRelativePath(relativePath, 'Checksum target')
  const filePath = path.resolve(packageDir, normalizedPath)
  const relative = path.relative(packageDir, filePath)
  if (relative.startsWith('..') || path.isAbsolute(relative)) {
    throw new Error(`Checksum target escapes package directory: ${relativePath}`)
  }
  return filePath
}

async function verifyPackage(packageDir) {
  assertReleaseDefaultsFile(path.join(packageDir, 'config', 'defaults.env'))
  const checksumsPath = path.join(packageDir, 'checksums.txt')
  const lines = readFileSync(checksumsPath, 'utf8')
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
  if (lines.length === 0) {
    throw new Error(`No checksum entries found in ${checksumsPath}`)
  }

  let checked = 0
  let sawAifarZip = false
  const listedFiles = new Set()
  for (const line of lines) {
    const { expected, relativePath } = parseChecksumLine(line, checksumsPath)
    const normalizedPath = safeRelativePath(relativePath, 'Checksum target')
    if (listedFiles.has(normalizedPath)) {
      throw new Error(`Duplicate checksum target in ${checksumsPath}: ${normalizedPath}`)
    }
    listedFiles.add(normalizedPath)
    const filePath = packageFilePath(packageDir, relativePath)
    if (!existsSync(filePath) || !statSync(filePath).isFile()) {
      throw new Error(`Checksum target missing: ${path.relative(rootDir, filePath)}`)
    }
    const actual = await sha256(filePath)
    if (actual !== expected) {
      throw new Error(`Checksum mismatch for ${path.relative(rootDir, filePath)}: expected ${expected}, got ${actual}`)
    }
    if (relativePath.replaceAll('\\', '/') === 'resources/aifar.zip') {
      sawAifarZip = true
    }
    checked += 1
  }

  const aifarZipPath = path.join(packageDir, 'resources', 'aifar.zip')
  if (existsSync(aifarZipPath) && !sawAifarZip) {
    throw new Error(`resources/aifar.zip exists but is missing from ${checksumsPath}`)
  }
  const extraFiles = walkFiles(packageDir).filter((file) => file !== 'checksums.txt' && !listedFiles.has(file))
  if (extraFiles.length > 0) {
    throw new Error(`Files missing from ${checksumsPath}: ${extraFiles.join(', ')}`)
  }
  console.log(`[checksums] verified ${checked} files in ${path.relative(rootDir, packageDir)}`)
}

function archiveListCommand(archivePath, archiveName) {
  if (archiveName.endsWith('.tar.gz')) return ['tar', ['-tzf', archivePath]]
  if (process.platform === 'win32') return ['tar', ['-tf', archivePath]]
  return ['unzip', ['-Z1', archivePath]]
}

function archiveExtractCommand(archivePath, archiveName, targetDir) {
  if (archiveName.endsWith('.tar.gz')) return ['tar', ['-xzf', archivePath, '-C', targetDir]]
  if (process.platform === 'win32') return ['tar', ['-xf', archivePath, '-C', targetDir]]
  return ['unzip', ['-q', archivePath, '-d', targetDir]]
}

function inspectArchive(archiveName, packageName) {
  const archivePath = path.join(deploymentDir, archiveName)
  if (!existsSync(archivePath) || !statSync(archivePath).isFile()) {
    throw new Error(`[checksums] missing release archive: ${archiveName}`)
  }
  const [command, args] = archiveListCommand(archivePath, archiveName)
  const result = spawnSync(command, args, { cwd: deploymentDir, encoding: 'utf8' })
  if (result.error || result.status !== 0 || result.signal) {
    throw new Error(`[checksums] cannot inspect archive ${archiveName}: ${result.error?.message ?? result.stderr ?? `exit ${result.status ?? result.signal}`}`)
  }
  const entries = result.stdout.split(/\r?\n/).map((line) => line.trim()).filter(Boolean)
  if (entries.length === 0) throw new Error(`[checksums] archive ${archiveName} is empty`)
  const packagePrefix = `${packageName}/`
  let sawChecksums = false
  for (const entry of entries) {
    const normalized = safeRelativePath(entry, `Archive entry in ${archiveName}`)
    if (normalized !== packageName && !normalized.startsWith(packagePrefix)) {
      throw new Error(`[checksums] archive ${archiveName} contains unexpected entry: ${entry}`)
    }
    if (normalized === `${packagePrefix}checksums.txt`) sawChecksums = true
  }
  if (!sawChecksums) throw new Error(`[checksums] archive ${archiveName} lacks ${packagePrefix}checksums.txt`)
}

async function verifyArchive(archiveName, packageName) {
  inspectArchive(archiveName, packageName)
  const archivePath = path.join(deploymentDir, archiveName)
  const extractRoot = mkdtempSync(path.join(tmpdir(), 'aifar-release-verify-'))
  try {
    const [command, args] = archiveExtractCommand(archivePath, archiveName, extractRoot)
    const result = spawnSync(command, args, { cwd: deploymentDir, encoding: 'utf8' })
    if (result.error || result.status !== 0 || result.signal) {
      throw new Error(`[checksums] cannot extract archive ${archiveName}: ${result.error?.message ?? result.stderr ?? `exit ${result.status ?? result.signal}`}`)
    }
    const extractedPackageDir = path.join(extractRoot, packageName)
    if (!existsSync(extractedPackageDir) || !statSync(extractedPackageDir).isDirectory()) {
      throw new Error(`[checksums] archive ${archiveName} did not extract ${packageName}`)
    }
    await verifyPackage(extractedPackageDir)
    console.log(`[checksums] verified archive ${archiveName}`)
  } finally {
    rmSync(extractRoot, { recursive: true, force: true })
  }
}

const expectedDirectoryNames = expectedPackages.map((item) => item.name)
const actualDirectoryNames = deploymentEntries((entry) => entry.isDirectory())
assertExactSet(actualDirectoryNames, expectedDirectoryNames, 'Linux and Windows package directories')

const expectedArchiveNames = expectedPackages.map((item) => item.archive)
const actualArchiveNames = deploymentEntries((entry) => entry.isFile() && (entry.name.endsWith('.tar.gz') || entry.name.endsWith('.zip')))
assertExactSet(actualArchiveNames, expectedArchiveNames, 'Linux and Windows release archives')

const dirs = expectedDirectoryNames.map((name) => path.join(deploymentDir, name))
for (const dir of dirs) {
  await verifyPackage(dir)
}

for (const releasePackage of expectedPackages) {
  await verifyArchive(releasePackage.archive, releasePackage.name)
}

console.log(`[checksums] verified ${dirs.length} release package directories`)
