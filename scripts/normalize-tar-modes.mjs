import { existsSync, renameSync, rmSync } from 'node:fs'
import { createReadStream, createWriteStream } from 'node:fs'
import path from 'node:path'
import { Transform } from 'node:stream'
import { pipeline } from 'node:stream/promises'
import { createGunzip, createGzip } from 'node:zlib'

const blockSize = 512

function fieldText(block, start, length) {
  const end = block.indexOf(0, start)
  const actualEnd = end === -1 || end > start + length ? start + length : end
  return block.toString('utf8', start, actualEnd)
}

function entryPath(block) {
  const name = fieldText(block, 0, 100)
  const prefix = fieldText(block, 345, 155)
  return prefix ? `${prefix}/${name}` : name
}

function entrySize(block) {
  const field = block.subarray(124, 136)
  if ((field[0] & 0x80) !== 0) {
    throw new Error('Base-256 tar sizes are not supported by the mode normalizer')
  }
  const text = field.toString('ascii').replace(/\0.*$/, '').trim()
  if (text === '') return 0
  const size = Number.parseInt(text, 8)
  if (!Number.isSafeInteger(size) || size < 0) throw new Error(`Invalid tar entry size ${JSON.stringify(text)}`)
  return size
}

function isZeroBlock(block) {
  for (const value of block) {
    if (value !== 0) return false
  }
  return true
}

function setMode(block, mode) {
  block.fill(0, 100, 108)
  block.write(mode.toString(8).padStart(7, '0'), 100, 7, 'ascii')
  block.fill(0x20, 148, 156)
  let checksum = 0
  for (const value of block) checksum += value
  block.write(checksum.toString(8).padStart(6, '0'), 148, 6, 'ascii')
  block[154] = 0
  block[155] = 0x20
}

class TarModeTransform extends Transform {
  constructor(targets) {
    super()
    this.targets = targets
    this.matched = new Set()
    this.pending = Buffer.alloc(0)
    this.dataBlocksRemaining = 0
  }

  _transform(chunk, _encoding, callback) {
    try {
      this.pending = this.pending.length === 0 ? chunk : Buffer.concat([this.pending, chunk])
      while (this.pending.length >= blockSize) {
        const block = Buffer.from(this.pending.subarray(0, blockSize))
        this.pending = this.pending.subarray(blockSize)
        if (this.dataBlocksRemaining > 0) {
          this.dataBlocksRemaining -= 1
        } else if (!isZeroBlock(block)) {
          const currentPath = entryPath(block)
          if (this.targets.has(currentPath)) {
            setMode(block, 0o755)
            this.matched.add(currentPath)
          }
          this.dataBlocksRemaining = Math.ceil(entrySize(block) / blockSize)
        }
        this.push(block)
      }
      callback()
    } catch (error) {
      callback(error)
    }
  }

  _flush(callback) {
    if (this.pending.length !== 0) {
      callback(new Error(`Truncated tar stream: ${this.pending.length} trailing bytes`))
      return
    }
    const missing = [...this.targets].filter((target) => !this.matched.has(target))
    if (missing.length > 0) {
      callback(new Error(`Tar archive lacks executable entries: ${missing.join(', ')}`))
      return
    }
    callback()
  }
}

async function normalize(archivePath, targetPaths) {
  const archive = path.resolve(archivePath)
  if (!archive.endsWith('.tar.gz') || !existsSync(archive)) {
    throw new Error(`Expected an existing .tar.gz archive: ${archive}`)
  }
  const targets = new Set(targetPaths)
  if (targets.size === 0 || targets.size !== targetPaths.length) {
    throw new Error('Executable tar entry paths must be non-empty and unique')
  }
  for (const target of targets) {
    if (target.startsWith('/') || target.includes('..') || target.includes('\\')) {
      throw new Error(`Unsafe tar entry path: ${target}`)
    }
  }

  const temporary = `${archive}.mode-${process.pid}.tmp`
  const backup = `${archive}.mode-${process.pid}.bak`
  rmSync(temporary, { force: true })
  rmSync(backup, { force: true })
  try {
    await pipeline(
      createReadStream(archive),
      createGunzip(),
      new TarModeTransform(targets),
      createGzip(),
      createWriteStream(temporary, { flags: 'wx' })
    )
    renameSync(archive, backup)
    try {
      renameSync(temporary, archive)
    } catch (error) {
      renameSync(backup, archive)
      throw error
    }
    rmSync(backup, { force: true, maxRetries: 5, retryDelay: 100 })
  } finally {
    rmSync(temporary, { force: true, maxRetries: 5, retryDelay: 100 })
  }
}

const [, , archivePath, ...targetPaths] = process.argv
if (!archivePath) {
  console.error('usage: node normalize-tar-modes.mjs <archive.tar.gz> <entry>...')
  process.exit(2)
}

normalize(archivePath, targetPaths).catch((error) => {
  console.error(`[tar modes] ${error.message}`)
  process.exit(1)
})
