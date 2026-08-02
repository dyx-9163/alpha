import { describe, expect, it } from 'vitest'
import {
  healthFromCheckStatus,
  resolveDatabaseNodeHealth,
  resolveMySQLRuntimeHealth
} from './databaseHealth'

describe('database runtime health source', () => {
  it('maps authoritative application checks without a server-health input', () => {
    expect(healthFromCheckStatus('running')).toBe('online')
    expect(healthFromCheckStatus('failed')).toBe('offline')
    expect(healthFromCheckStatus('probing')).toBe('probing')
  })

  it('keeps nodes unknown when no application check exists', () => {
    expect(resolveDatabaseNodeHealth({ app: 'redis', topology: 'standalone' })).toBe('unknown')
    expect(resolveDatabaseNodeHealth({ app: 'mysql-router', topology: 'standalone', checkStatus: '' })).toBe('unknown')
  })

  it('uses the application check for ordinary MySQL and Redis nodes', () => {
    expect(resolveDatabaseNodeHealth({ app: 'redis', topology: 'standalone', checkStatus: 'running' })).toBe('online')
    expect(resolveDatabaseNodeHealth({ app: 'mysql', topology: 'standalone', checkStatus: 'unavailable' })).toBe('offline')
  })

  it('uses MySQL runtime evidence before the overall cluster check', () => {
    expect(resolveDatabaseNodeHealth({
      app: 'mysql',
      topology: 'innodb-cluster',
      runtimeStatus: 'running',
      checkStatus: 'failed'
    })).toBe('online')
    expect(resolveMySQLRuntimeHealth({ runtimeStatus: 'offline', checkStatus: 'running' })).toBe('offline')
  })

  it('falls back to the application check when MySQL runtime evidence is unknown', () => {
    expect(resolveMySQLRuntimeHealth({ runtimeStatus: '', checkStatus: 'running' })).toBe('online')
    expect(resolveMySQLRuntimeHealth({ runtimeStatus: 'unexpected', checkStatus: 'failed' })).toBe('offline')
  })
})
