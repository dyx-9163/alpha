import assert from 'node:assert/strict'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import test from 'node:test'

import { assertReleaseDefaultsFile, parseDefaultsEnv, releaseDefaultsProblems } from './runtime-security-config.mjs'

test('blank release credentials are safe to distribute', () => {
  const env = parseDefaultsEnv(`
    AIFAR_DEFAULT_PASSWORD=
    AIFAR_BOOTSTRAP_PASSWORD=
    AIFAR_JWT_SECRET=
    AIFAR_CREDENTIAL_SECRET=
    AIFAR_ALLOW_INSECURE_DEFAULTS=false
  `)
  assert.deepEqual(releaseDefaultsProblems(env), [])
})

test('repository release defaults are safe to distribute', () => {
  const defaultsPath = fileURLToPath(new URL('../config/defaults.env', import.meta.url))
  assert.doesNotThrow(() => assertReleaseDefaultsFile(defaultsPath))
})

test('release credentials reject built-in and placeholder values', () => {
  const env = parseDefaultsEnv(`
    AIFAR_DEFAULT_PASSWORD=Oversea.123
    AIFAR_JWT_SECRET=change-me-before-production
    AIFAR_CREDENTIAL_SECRET=short
    AIFAR_ALLOW_INSECURE_DEFAULTS=true
  `)
  const problems = releaseDefaultsProblems(env).join('\n')
  assert.match(problems, /AIFAR_DEFAULT_PASSWORD/)
  assert.match(problems, /AIFAR_JWT_SECRET/)
  assert.match(problems, /AIFAR_CREDENTIAL_SECRET/)
  assert.match(problems, /AIFAR_ALLOW_INSECURE_DEFAULTS/)
})

test('release override parsing matches Go boolean shorthand', () => {
  const env = parseDefaultsEnv('AIFAR_ALLOW_INSECURE_DEFAULTS=t')
  assert.match(releaseDefaultsProblems(env).join('\n'), /AIFAR_ALLOW_INSECURE_DEFAULTS/)
})

test('duplicate release configuration keys are rejected', () => {
  assert.throws(
    () => parseDefaultsEnv('AIFAR_ALLOW_INSECURE_DEFAULTS=true\nAIFAR_ALLOW_INSECURE_DEFAULTS=false'),
    /Duplicate configuration key/
  )
})

test('defaults parser rejects malformed lines and illegal keys instead of skipping them', () => {
  for (const input of [
    'BROKEN',
    'OTHER_KEY=value',
    'AIFAR_=value',
    'AIFAR_lower=value',
    'AIFAR_BAD-KEY=value',
    'AIFAR_SPACED =value'
  ]) {
    assert.throws(() => parseDefaultsEnv(input), /Malformed configuration line 1/)
  }
})

test('defaults parser accepts embedded equals and strips only matching outer quotes after trim', () => {
  const env = parseDefaultsEnv(`
      # leading-space comments are ignored
    AIFAR_EQUALS=left=middle=right
    AIFAR_DOUBLE="  keep inner spaces = yes  "
    AIFAR_SINGLE='single=value'
    AIFAR_EMPTY=
    AIFAR_UNMATCHED="not-stripped'
  `)

  assert.equal(env.AIFAR_EQUALS, 'left=middle=right')
  assert.equal(env.AIFAR_DOUBLE, '  keep inner spaces = yes  ')
  assert.equal(env.AIFAR_SINGLE, 'single=value')
  assert.equal(env.AIFAR_EMPTY, '')
  assert.equal(env.AIFAR_UNMATCHED, '"not-stripped\'')
})

test('defaults parser errors never echo secret values', () => {
  const secret = 'do-not-echo-this-secret'
  let error
  try {
    parseDefaultsEnv(`AIFAR_JWT_SECRET=${secret}\nmalformed-${secret}`)
  } catch (caught) {
    error = caught
  }
  assert.ok(error instanceof Error)
  assert.doesNotMatch(error.message, new RegExp(secret))
})

test('release file validation reports unsafe keys without echoing their values', (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), 'aifar-release-defaults-'))
  t.after(() => rmSync(directory, { recursive: true, force: true }))
  const file = path.join(directory, 'defaults.env')
  const secret = 'do-not-echo-release-secret'
  writeFileSync(file, `AIFAR_JWT_SECRET=${secret}\nAIFAR_ALLOW_INSECURE_DEFAULTS=false\n`)

  let error
  try {
    assertReleaseDefaultsFile(file)
  } catch (caught) {
    error = caught
  }
  assert.ok(error instanceof Error)
  assert.match(error.message, /AIFAR_JWT_SECRET/)
  assert.doesNotMatch(error.message, new RegExp(secret))
})

test('strong values are injected at deployment time rather than embedded', () => {
  const env = parseDefaultsEnv(`
    AIFAR_DEFAULT_PASSWORD=panel-password-2026
    AIFAR_BOOTSTRAP_PASSWORD=bootstrap-password-2026
    AIFAR_JWT_SECRET=jwt-secret-with-at-least-thirty-two-characters
    AIFAR_CREDENTIAL_SECRET=credential-secret-with-at-least-thirty-two-characters
    AIFAR_PREVIOUS_CREDENTIAL_SECRET=previous-credential-secret-for-rotation
    AIFAR_ALLOW_INSECURE_DEFAULTS=false
  `)
  const problems = releaseDefaultsProblems(env).join('\n')
  assert.match(problems, /AIFAR_DEFAULT_PASSWORD/)
  assert.match(problems, /AIFAR_BOOTSTRAP_PASSWORD/)
  assert.match(problems, /AIFAR_JWT_SECRET/)
  assert.match(problems, /AIFAR_CREDENTIAL_SECRET/)
  assert.match(problems, /AIFAR_PREVIOUS_CREDENTIAL_SECRET/)
})
