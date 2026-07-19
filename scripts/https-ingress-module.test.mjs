import assert from 'node:assert/strict'
import { createPrivateKey, createPublicKey, X509Certificate } from 'node:crypto'
import { readFileSync } from 'node:fs'
import path from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

const scriptsDir = path.dirname(fileURLToPath(import.meta.url))
const rootDir = path.resolve(scriptsDir, '..')
const moduleDir = path.join(rootDir, 'extras', 'aifar-https-ingress')

const requiredFiles = [
  'config.env',
  'conf.d/aifar.conf',
  'tls/fullchain.pem',
  'tls/privkey.pem',
  'start.sh',
  'stop.sh',
  'reload.sh',
  'status.sh',
  'install-systemd.sh',
  'uninstall-systemd.sh',
  'aifar-https-ingress.service',
  'README.md'
]

function read(relativePath) {
  return readFileSync(path.join(moduleDir, relativePath), 'utf8')
}

test('HTTPS ingress module contains every copy-and-run artifact', () => {
  for (const relativePath of requiredFiles) {
    assert.doesNotThrow(() => read(relativePath), `missing ${relativePath}`)
  }
})

test('Nginx terminates TLS and routes web, API, and WebSocket traffic correctly', () => {
  const config = read('conf.d/aifar.conf')
  assert.match(config, /server_name aifar\.local;/)
  assert.match(config, /listen 443 ssl(?: http2)?;/)
  assert.match(config, /ssl_certificate\s+\/etc\/nginx\/tls\/fullchain\.pem;/)
  assert.match(config, /location \/api\/\s*\{[\s\S]*proxy_pass http:\/\/127\.0\.0\.1:38000;/)
  assert.match(config, /location \^~ \/im\/ws\s*\{[\s\S]*proxy_set_header Upgrade \$http_upgrade;/)
  assert.match(config, /location \/\s*\{[\s\S]*proxy_pass http:\/\/127\.0\.0\.1:8080;/)
  assert.match(config, /proxy_set_header X-Forwarded-Proto https;/)
})

test('start script uses host networking, read-only mounts, restart policy, and preflight checks', () => {
  const script = read('start.sh')
  assert.match(script, /docker image inspect/)
  assert.match(script, /WEB_PORT=.*8080/)
  assert.match(script, /GATEWAY_PORT=.*38000/)
  assert.match(script, /nc -z 127\.0\.0\.1 \$WEB_PORT/)
  assert.match(script, /nc -z 127\.0\.0\.1 \$GATEWAY_PORT/)
  assert.match(script, /--network host/)
  assert.match(script, /--restart unless-stopped/)
  assert.match(script, /\/etc\/nginx\/conf\.d:ro,Z/)
  assert.match(script, /\/etc\/nginx\/tls:ro,Z/)
  assert.match(script, /nginx -t/)
})

test('systemd scripts install an absolute-path oneshot service ordered after Docker', () => {
  const unit = read('aifar-https-ingress.service')
  const install = read('install-systemd.sh')
  const uninstall = read('uninstall-systemd.sh')
  assert.match(unit, /Requires=docker\.service/)
  assert.match(unit, /After=docker\.service network-online\.target/)
  assert.match(unit, /Type=oneshot/)
  assert.match(unit, /RemainAfterExit=yes/)
  assert.match(unit, /ExecStart=@@MODULE_DIR@@\/start\.sh/)
  assert.match(unit, /ExecStop=@@MODULE_DIR@@\/stop\.sh/)
  assert.match(install, /UNIT_NAME="aifar-https-ingress\.service"/)
  assert.match(install, /systemctl enable --now "\$UNIT_NAME"/)
  assert.match(uninstall, /UNIT_NAME="aifar-https-ingress\.service"/)
  assert.match(uninstall, /systemctl disable --now "\$UNIT_NAME"/)
})

test('bundled bootstrap certificate and private key match and cover aifar.local', () => {
  const certificate = new X509Certificate(read('tls/fullchain.pem'))
  const privateKey = createPrivateKey(read('tls/privkey.pem'))
  const certificatePublicKey = certificate.publicKey.export({ format: 'der', type: 'spki' })
  const privatePublicKey = createPublicKey(privateKey).export({ format: 'der', type: 'spki' })
  assert.match(certificate.subjectAltName ?? '', /DNS:aifar\.local/)
  assert.deepEqual(certificatePublicKey, privatePublicKey)
})

test('shell scripts use LF endings and documentation warns about the self-signed key', () => {
  for (const relativePath of requiredFiles.filter((file) => file.endsWith('.sh'))) {
    assert.doesNotMatch(read(relativePath), /\r/)
  }
  const readme = read('README.md')
  assert.match(readme, /self-signed/i)
  assert.match(readme, /正式证书/)
  assert.match(readme, /aifar\.local/)
})
