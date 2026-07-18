import assert from 'node:assert/strict'
import { createHash } from 'node:crypto'
import { readFileSync, statSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const moduleDir = path.join(rootDir, 'extras', 'keepalived')
const archiveName = 'keepalived-2.4.2.tar.gz'
const archiveSha256 = '76397ad758ae871dfa713b9fc6b4ead754db7964809a3969e40c2d288bc3460b'

function read(relativePath) {
  return readFileSync(path.join(moduleDir, relativePath), 'utf8')
}

function indexOfOrFail(source, fragment) {
  const index = source.indexOf(fragment)
  assert.notEqual(index, -1, `missing ${fragment}`)
  return index
}

test('Keepalived module contains the verified installer artifacts', () => {
  for (const relativePath of [archiveName, 'SHA256SUMS', 'install-keepalived-offline.sh']) {
    assert.doesNotThrow(() => readFileSync(path.join(moduleDir, relativePath)))
  }
})

test('Keepalived source archive has the pinned size and digest', () => {
  const archivePath = path.join(moduleDir, archiveName)
  const archive = readFileSync(archivePath)
  assert.equal(statSync(archivePath).size, 6350291)
  assert.equal(createHash('sha256').update(archive).digest('hex'), archiveSha256)
  assert.equal(read('SHA256SUMS'), `${archiveSha256}  ${archiveName}\n`)
})

test('installer verifies the archive before extraction and never activates Keepalived', () => {
  const installer = read('install-keepalived-offline.sh')
  const mainStart = indexOfOrFail(installer, '\nmain() {')
  const mainBody = installer.slice(mainStart, installer.lastIndexOf('\nmain "$@"'))
  assert.match(installer, /readonly APP_ROOT="\/aifar\/apps\/keepalived"/)
  assert.ok(indexOfOrFail(installer, 'sha256sum --check') < indexOfOrFail(installer, 'tar -tzf'))
  assert.doesNotMatch(mainBody, /systemctl\s+(?:enable|start|restart)|systemctl\s+enable\s+--now/)
  assert.doesNotMatch(installer, /cp\s+.*keepalived\.conf\.sample.*keepalived\.conf/)
})

test('SELinux script preserves mode and manages persistent distribution-derived labels', () => {
  const script = read('configure-selinux.sh')
  assert.match(script, /getenforce/)
  assert.match(script, /matchpathcon/)
  assert.match(script, /semanage fcontext/)
  assert.match(script, /restorecon/)
  assert.match(script, /keepalived-selinux-fcontexts/)
  assert.doesNotMatch(
    script,
    /setenforce|SELINUX\s*=\s*(?:disabled|permissive)|\/etc\/selinux\/config|audit2allow/i
  )
})

test('installer and SELinux scripts use LF endings', () => {
  for (const relativePath of ['install-keepalived-offline.sh', 'configure-selinux.sh']) {
    assert.doesNotMatch(read(relativePath), /\r/)
  }
})
