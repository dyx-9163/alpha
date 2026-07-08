import { createHash } from 'node:crypto'
import { createReadStream, existsSync, readdirSync, readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import { rootDir } from './toolchain.mjs'

const deploymentDir = path.join(rootDir, 'deploy', 'deployment')

function sha256(filePath) {
  return new Promise((resolve, reject) => {
    const hash = createHash('sha256')
    const stream = createReadStream(filePath)
    stream.on('data', (chunk) => hash.update(chunk))
    stream.on('error', reject)
    stream.on('end', () => resolve(hash.digest('hex')))
  })
}

function packageDirs() {
  if (!existsSync(deploymentDir)) return []
  return readdirSync(deploymentDir, { withFileTypes: true })
    .filter((entry) => entry.isDirectory())
    .map((entry) => path.join(deploymentDir, entry.name))
    .filter((dir) => existsSync(path.join(dir, 'checksums.txt')))
    .sort((a, b) => a.localeCompare(b))
}

function parseChecksumLine(line, checksumsPath) {
  const match = line.match(/^([a-fA-F0-9]{64})\s+\*?(.+)$/)
  if (!match) {
    throw new Error(`Invalid checksum line in ${checksumsPath}: ${line}`)
  }
  return { expected: match[1].toLowerCase(), relativePath: match[2].trim() }
}

async function verifyPackage(packageDir) {
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
  for (const line of lines) {
    const { expected, relativePath } = parseChecksumLine(line, checksumsPath)
    const filePath = path.join(packageDir, relativePath)
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
  console.log(`[checksums] verified ${checked} files in ${path.relative(rootDir, packageDir)}`)
}

const dirs = packageDirs()
if (dirs.length === 0) {
  console.error(`[checksums] no package directories with checksums.txt found under ${path.relative(rootDir, deploymentDir)}`)
  process.exit(1)
}

for (const dir of dirs) {
  await verifyPackage(dir)
}

console.log(`[checksums] verified ${dirs.length} release package directories`)
