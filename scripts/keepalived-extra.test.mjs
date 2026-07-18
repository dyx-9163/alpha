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
  for (const relativePath of [
    archiveName,
    'SHA256SUMS',
    'install-keepalived-offline.sh',
    'keepalived.env.example',
    'keepalived.conf.tpl'
  ]) {
    assert.doesNotThrow(() => readFileSync(path.join(moduleDir, relativePath)))
  }
})

test('generic node example exposes exactly the supported Keepalived keys', () => {
  const example = read('keepalived.env.example')
  const keys = [...example.matchAll(/^([A-Z][A-Z0-9_]*)=/gm)].map((match) => match[1])
  assert.deepEqual(keys, [
    'KEEPALIVED_LOCAL_IP',
    'KEEPALIVED_PEER_IP',
    'KEEPALIVED_VIP_CIDR',
    'KEEPALIVED_INTERFACE',
    'KEEPALIVED_PRIORITY',
    'KEEPALIVED_VIRTUAL_ROUTER_ID',
    'KEEPALIVED_HEALTH_URL'
  ])
})

test('production template uses BACKUP unicast health-fault and preemption defaults', () => {
  const template = read('keepalived.conf.tpl')
  for (const placeholder of [
    '@ROUTER_ID@', '@INTERFACE@', '@VIRTUAL_ROUTER_ID@', '@PRIORITY@',
    '@LOCAL_IP@', '@PEER_IP@', '@VIP_CIDR@'
  ]) assert.match(template, new RegExp(placeholder))
  assert.match(template, /state BACKUP/)
  assert.match(template, /interval 2/)
  assert.match(template, /timeout 3/)
  assert.match(template, /fall 3/)
  assert.match(template, /rise 2/)
  assert.match(template, /weight 0/)
  assert.match(template, /unicast_src_ip @LOCAL_IP@/)
  assert.doesNotMatch(template, /nopreempt/)
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

test('Keepalived module contains all release artifacts', () => {
  for (const relativePath of [
    'README.md',
    archiveName,
    'SHA256SUMS',
    'install-keepalived-offline.sh',
    'keepalived.env.example',
    'keepalived.conf.tpl',
    'configure-selinux.sh',
    'uninstall-keepalived.sh'
  ]) {
    assert.doesNotThrow(() => readFileSync(path.join(moduleDir, relativePath)))
  }
})

test('Keepalived uninstaller uses LF endings', () => {
  assert.doesNotMatch(read('uninstall-keepalived.sh'), /\r/)
})

test('uninstaller verifies a backup before service changes and exact-path deletion', () => {
  const script = read('uninstall-keepalived.sh')
  const backupVerify = indexOfOrFail(script, 'sha256sum --check BACKUP.sha256')
  const serviceStop = indexOfOrFail(script, 'systemctl stop keepalived.service')
  const installDelete = indexOfOrFail(script, 'rm -rf -- "$APP_ROOT"')
  assert.ok(backupVerify < serviceStop)
  assert.ok(serviceStop < installDelete)
  assert.match(script, /readonly APP_ROOT="\/aifar\/apps\/keepalived"/)
  assert.match(script, /readlink -f -- "\$APP_ROOT"/)
  assert.match(script, /readonly BACKUP_ROOT="\/aifar\/backups"/)
  assert.match(script, /BACKUP_DIR="\$BACKUP_ROOT\/keepalived-\$\(date -u/)
  assert.doesNotMatch(
    script,
    /dnf\s+(?:remove|erase)|yum\s+(?:remove|erase)|firewall-cmd\s+--remove|rm -rf -- \/aifar\/backups/
  )
})

test('README documents zero-argument lifecycle and retained state', () => {
  const readme = read('README.md')
  assert.match(readme, /bash install-keepalived-offline\.sh/)
  assert.match(readme, /bash configure-selinux\.sh/)
  assert.match(readme, /bash uninstall-keepalived\.sh/)
  assert.match(readme, /\/aifar\/backups\/keepalived-/)
  assert.match(readme, /不会自动启动/)
  assert.match(readme, /不会删除.*RPM/)
  assert.match(readme, /不会删除.*防火墙/)
})
