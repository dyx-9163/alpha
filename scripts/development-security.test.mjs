import assert from 'node:assert/strict'
import test from 'node:test'

import { developmentSecurityEnv, isLoopbackListenerAddress, selectDevelopmentAddress } from './development-security.mjs'

const overrideKey = 'AIFAR_ALLOW_INSECURE_DEFAULTS'

test('loopback listener detection accepts only exact loopback hosts', () => {
  for (const addr of ['127.0.0.1:8080', 'localhost:8080', '[::1]:8080']) {
    assert.equal(isLoopbackListenerAddress(addr), true, addr)
  }

  for (const addr of ['', '*', ':8080', '*:8080', '0.0.0.0', '0.0.0.0:8080', '::', '[::]:8080', 'app.internal:8080', '127.0.0.1:8080]', '[::1]:8080]']) {
    assert.equal(isLoopbackListenerAddress(addr), false, addr)
  }
})

test('development security override is automatic only on loopback', () => {
  assert.deepEqual(developmentSecurityEnv('127.0.0.1:8080', {}), { [overrideKey]: 'true' })
  assert.deepEqual(developmentSecurityEnv('0.0.0.0:8080', {}), {})
})

test('explicit false development security override is preserved', () => {
  const processEnv = { [overrideKey]: 'false' }
  assert.deepEqual(developmentSecurityEnv('127.0.0.1:8080', processEnv), processEnv)
})

test('development address selection preserves explicit empty environment values', () => {
  const toolEnv = { AIFAR_DEV_ADDR: '127.0.0.1:8080' }

  const devAddr = selectDevelopmentAddress({ AIFAR_DEV_ADDR: '' }, toolEnv, 'AIFAR_DEV_ADDR')
  assert.equal(devAddr, '')
  assert.deepEqual(developmentSecurityEnv(devAddr, {}), {})

  const backendAddr = selectDevelopmentAddress({ AIFAR_ADDR: '' }, toolEnv, 'AIFAR_ADDR')
  assert.equal(backendAddr, '')
  assert.deepEqual(developmentSecurityEnv(backendAddr, {}), {})
})

test('development address selection uses fallback object property presence', () => {
  assert.equal(selectDevelopmentAddress({}, { AIFAR_DEV_ADDR: '' }, 'AIFAR_ADDR'), '')
})
