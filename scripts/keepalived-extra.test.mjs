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
    'check-aggregate-health.sh',
    'keepalived.env.example',
    'keepalived.conf.tpl'
  ]) {
    assert.doesNotThrow(() => readFileSync(path.join(moduleDir, relativePath)))
  }
})

test('generic node example exposes required keys and comments the optional health URL', () => {
  const example = read('keepalived.env.example')
  const keys = [...example.matchAll(/^([A-Z][A-Z0-9_]*)=/gm)].map((match) => match[1])
  assert.deepEqual(keys, [
    'KEEPALIVED_LOCAL_IP',
    'KEEPALIVED_PEER_IP',
    'KEEPALIVED_VIP_CIDR',
    'KEEPALIVED_INTERFACE',
    'KEEPALIVED_PRIORITY',
    'KEEPALIVED_VIRTUAL_ROUTER_ID'
  ])
  assert.match(example, /^# KEEPALIVED_HEALTH_URL=http:\/\//m)
})

test('production template uses BACKUP unicast health-fault and preemption defaults', () => {
  const template = read('keepalived.conf.tpl')
  const installer = read('install-keepalived-offline.sh')
  const healthContract = `${template}\n${installer}`
  for (const placeholder of [
    '@ROUTER_ID@', '@INTERFACE@', '@VIRTUAL_ROUTER_ID@', '@PRIORITY@',
    '@LOCAL_IP@', '@PEER_IP@', '@VIP_CIDR@', '@SCRIPT_SECURITY@',
    '@HEALTH_SCRIPT@', '@TRACK_SCRIPT@'
  ]) assert.match(template, new RegExp(placeholder))
  assert.match(template, /state BACKUP/)
  assert.match(healthContract, /interval 2/)
  assert.match(healthContract, /timeout 3/)
  assert.match(healthContract, /fall 3/)
  assert.match(healthContract, /rise 2/)
  assert.match(healthContract, /weight 0/)
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

test('installer verifies source and requires managed configuration before build', () => {
  const installer = read('install-keepalived-offline.sh')
  const mainStart = indexOfOrFail(installer, '\nmain() {')
  const mainBody = installer.slice(mainStart, installer.indexOf('\nif [[ "${BASH_SOURCE[0]}" == "$0" ]]'))
  assert.ok(indexOfOrFail(mainBody, 'parse_node_config "$NODE_CONFIG"') < indexOfOrFail(mainBody, 'install_build_dependencies'))
  assert.ok(indexOfOrFail(mainBody, 'validate_node_config') < indexOfOrFail(mainBody, 'install_build_dependencies'))
  assert.ok(indexOfOrFail(mainBody, 'create_install_backup') < indexOfOrFail(mainBody, 'build_and_install_keepalived'))
  assert.ok(indexOfOrFail(mainBody, 'install_managed_configuration') < indexOfOrFail(mainBody, 'activate_keepalived'))
  const transactionOrder = [
    'render_keepalived_config',
    'validate_selinux_record_file "$SELINUX_RECORD"',
    'capture_service_state',
    'create_install_backup',
    'install_build_dependencies',
    'build_and_install_keepalived',
    'register_systemd_unit',
    'install_managed_configuration',
    'configure_selinux_if_enabled',
    'activate_keepalived',
    'verify_installation',
    'TRANSACTION_ACTIVE=0'
  ].map((fragment) => indexOfOrFail(mainBody, fragment))
  assert.deepEqual(transactionOrder, [...transactionOrder].sort((left, right) => left - right))
  assert.match(installer, /systemctl enable keepalived\.service/)
  assert.match(installer, /systemctl is-active --quiet keepalived\.service/)
  assert.match(installer, /cp -a -- "\$APP_ROOT" "\$BACKUP_DIR\/installed-root"/)
  assert.match(installer, /sha256sum --check BACKUP\.sha256/)
  assert.match(installer, /mv -f -- "\$config_tmp" "\$FORMAL_CONFIG"/)
  assert.match(installer, /readlink -f -- "\$APP_ROOT"/)
  assert.match(installer, /mountpoint -q "\$APP_ROOT"/)
  assert.match(installer, /rm -rf -- "\$APP_ROOT"/)
  assert.match(installer, /ln -s -- "\$UNIT_LINK_TARGET" "\$UNIT_LINK"/)
  assert.match(installer, /mountpoint -q "\$WORK_DIR"/)
  assert.match(installer, /die "keepalived\.service 启动失败"/)
  assert.match(installer, /log "WARNING: 健康检查当前不可用；服务保持 active，VRRP 实例将保持 FAULT"/)
  assert.doesNotMatch(installer, /cp\s+.*keepalived\.conf\.sample.*keepalived\.conf/)
})

test('SELinux script preserves mode and manages persistent distribution-derived labels', () => {
  const script = read('configure-selinux.sh')
  assert.match(script, /getenforce/)
  assert.match(script, /matchpathcon/)
  assert.match(script, /semanage fcontext/)
  assert.match(script, /restorecon/)
  assert.match(script, /keepalived-selinux-fcontexts/)
  assert.match(script, /\/aifar\/apps\/keepalived\/libexec\(\/\.\*\)\?/)
  assert.match(script, /KEEPALIVED_SELINUX_TRANSACTION_FILE/)
  assert.match(read('install-keepalived-offline.sh'), /rollback_selinux_journal/)
  assert.ok(
    indexOfOrFail(script, 'validate_selinux_record_file "$RECORD_FILE"') <
      indexOfOrFail(script, "apply_new_mapping '/aifar/apps/keepalived/sbin/keepalived'")
  )
  for (const scriptName of ['configure-selinux.sh', 'uninstall-keepalived.sh']) {
    const source = read(scriptName)
    assert.match(source, /PATTERN="\$1" awk '\$1 == ENVIRON\["PATTERN"\] \{ print \$NF; exit \}'/)
    assert.doesNotMatch(source, /awk -v pattern="\$1"/)
  }
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
    'check-aggregate-health.sh',
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
  const transactionStart = indexOfOrFail(script, 'execute_uninstall_transaction()')
  const transactionEnd = indexOfOrFail(script.slice(transactionStart), '\n}') + transactionStart
  const transactionBody = script.slice(transactionStart, transactionEnd)
  const preflight = indexOfOrFail(transactionBody, 'preflight_uninstall_state')
  const backup = indexOfOrFail(transactionBody, 'create_and_verify_backup')
  const transactionActive = indexOfOrFail(transactionBody, 'TRANSACTION_ACTIVE=1')
  const mutations = indexOfOrFail(transactionBody, 'perform_uninstall_mutations')
  assert.ok(preflight < backup)
  assert.ok(backup < transactionActive)
  assert.ok(transactionActive < mutations)
  const mutationStart = indexOfOrFail(script, 'perform_uninstall_mutations()')
  const mutationEnd = indexOfOrFail(script.slice(mutationStart), '\n}') + mutationStart
  const mutationBody = script.slice(mutationStart, mutationEnd)
  assert.ok(indexOfOrFail(mutationBody, 'systemctl stop keepalived.service') < indexOfOrFail(mutationBody, 'safe_remove_uninstall_root'))
  assert.match(script, /preflight_selinux_state\(\)[\s\S]*validate_selinux_record_file "\$SELINUX_RECORD"/)
  assert.match(script, /rollback_uninstall_transaction\(\)/)
  assert.match(script, /readonly APP_ROOT="\/aifar\/apps\/keepalived"/)
  assert.match(script, /readlink -f -- "\$APP_ROOT"/)
  assert.match(script, /readonly BACKUP_ROOT="\/aifar\/backups"/)
  assert.match(script, /BACKUP_DIR="\$BACKUP_ROOT\/keepalived-\$\(date -u/)
  assert.match(script, /firewall-cmd --zone="\$zone" --remove-rich-rule="\$rule"/)
  assert.match(script, /firewall-cmd --permanent --zone="\$zone" --remove-rich-rule="\$rule"/)
  assert.doesNotMatch(script, /\[\[ -s "\$FIREWALL_RECORD" \]\]/)
  assert.match(script, /\[\[ ! -e "\$FIREWALL_RECORD" && ! -L "\$FIREWALL_RECORD" \]\]/)
  assert.equal((script.match(/\[\[ -f "\$FIREWALL_RECORD" && ! -L "\$FIREWALL_RECORD" \]\]/g) ?? []).length, 2)
  assert.doesNotMatch(
    script,
    /dnf\s+(?:remove|erase)|yum\s+(?:remove|erase)|firewall-cmd\s+--remove-(?:service|port|protocol)|firewall-cmd\s+--remove-rich-rule(?:\s|$)|rm -rf -- \/aifar\/backups/
  )
})

test('README documents zero-argument lifecycle and retained state', () => {
  const readme = read('README.md')
  for (const key of [
    'KEEPALIVED_LOCAL_IP',
    'KEEPALIVED_PEER_IP',
    'KEEPALIVED_VIP_CIDR',
    'KEEPALIVED_INTERFACE',
    'KEEPALIVED_PRIORITY',
    'KEEPALIVED_VIRTUAL_ROUTER_ID',
    'KEEPALIVED_HEALTH_URL'
  ]) assert.match(readme, new RegExp(key))
  assert.match(readme, /192\.168\.74\.132/)
  assert.match(readme, /192\.168\.74\.133/)
  assert.match(readme, /192\.168\.74\.130\/24/)
  assert.match(readme, /bash install-keepalived-offline\.sh/)
  assert.match(readme, /bash configure-selinux\.sh/)
  assert.match(readme, /bash uninstall-keepalived\.sh/)
  assert.match(readme, /systemctl status keepalived/)
  assert.match(readme, /systemctl restart keepalived/)
  assert.match(readme, /systemctl stop keepalived/)
  assert.match(readme, /systemctl start keepalived/)
  assert.match(readme, /ip addr show dev ens160/)
  assert.match(readme, /FAULT/)
  assert.match(readme, /自动.*(?:抢占|切回).*132/)
  assert.match(readme, /protocol 112|协议 112/)
  assert.match(readme, /\/aifar\/backups\/keepalived-/)
  assert.match(readme, /不会删除.*RPM/)
  assert.match(readme, /不会删除.*(?:预存|非本安装|不属于).*防火墙/)
})
