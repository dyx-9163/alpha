import assert from 'node:assert/strict'
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { spawnSync } from 'node:child_process'
import path from 'node:path'
import test from 'node:test'

const shellCandidates = process.platform === 'win32'
  ? ['D:\\tools\\git\\bin\\bash.exe', 'C:\\Program Files\\Git\\bin\\bash.exe']
  : ['/bin/bash', '/usr/bin/bash']
const shell = shellCandidates.find((candidate) => existsSync(candidate))
const templatePath = path.resolve('backend/internal/apps/aifar/templates/uninstall.sh')

function toMsysPath(value) {
  return value.replace(/^([A-Za-z]):\\/, (_, drive) => `/${drive.toLowerCase()}/`).replaceAll('\\', '/')
}

function runTemplate({ removeStatus }) {
  const fixture = mkdtempSync(path.resolve('.aifar-runtime-uninstall-test-'))
  const bin = path.join(fixture, 'bin')
  const installRoot = path.join(fixture, 'install-root')
  const instanceState = path.join(fixture, 'instances', 'admin')
  const calls = path.join(fixture, 'calls.log')
  const script = path.join(fixture, 'uninstall.sh')
  const setup = `mkdir -p '${toMsysPath(bin)}' '${toMsysPath(installRoot)}' '${toMsysPath(instanceState)}'\n`
  const fakeAgent = `#!/usr/bin/env bash\nprintf '%s\\n' "$*" >>'${toMsysPath(calls)}'\n${removeStatus === 0 ? `rm -rf -- '${toMsysPath(instanceState)}'\n` : ''}exit ${removeStatus}\n`
  const fakeDocker = `#!/usr/bin/env bash\nexit 0\n`
  const source = readFileSync(templatePath, 'utf8')
    .replace('{{ quote .InstallRoot }}', `'${toMsysPath(installRoot)}'`)
    .replace('{{ quote .NetworkName }}', "'aifar-network'")
    .replaceAll('{{ "{{" }}.Names{{ "}}" }}', '{{.Names}}')
    .replace('INSTANCE_STATE=/var/lib/aifar-agent/instances/admin', `INSTANCE_STATE='${toMsysPath(instanceState)}'`)
  const harness = `${setup}cat >'${toMsysPath(path.join(bin, 'aifar-agent'))}' <<'AGENT'\n${fakeAgent}AGENT\ncat >'${toMsysPath(path.join(bin, 'docker'))}' <<'DOCKER'\n${fakeDocker}DOCKER\nchmod +x '${toMsysPath(bin)}'/*\nexport PATH='${toMsysPath(bin)}':/usr/bin:/bin\n${source}`
  writeFileSync(script, harness)
  const result = spawnSync(shell, [toMsysPath(script)], { encoding: 'utf8' })
  return { result, calls: existsSync(calls) ? readFileSync(calls, 'utf8') : '', fixture, installRoot, script }
}

test('runtime uninstall fails closed when Agent durable retirement fails', { skip: !shell }, (t) => {
  const { result, calls, fixture, installRoot, script } = runTemplate({ removeStatus: 23 })
  t.after(() => rmSync(fixture, { recursive: true, force: true }))
  assert.notEqual(calls, '', `aifar-agent was not invoked\n${result.stdout}\n${result.stderr}\nscript=${script}`)
  assert.notEqual(result.status, 0, `${result.stdout}\n${result.stderr}`)
  assert.match(calls, /^remove-instance --instance admin$/m)
  assert.equal(spawnSync(shell, ['-c', `test -d '${toMsysPath(installRoot)}'`]).status, 0)
})

test('runtime uninstall proceeds only after Agent durable retirement succeeds', { skip: !shell }, (t) => {
  const { result, calls, fixture, installRoot } = runTemplate({ removeStatus: 0 })
  t.after(() => rmSync(fixture, { recursive: true, force: true }))
  assert.equal(result.status, 0, `${result.stdout}\n${result.stderr}`)
  assert.match(calls, /^remove-instance --instance admin$/m)
  assert.notEqual(spawnSync(shell, ['-c', `test -e '${toMsysPath(installRoot)}'`]).status, 0)
})
