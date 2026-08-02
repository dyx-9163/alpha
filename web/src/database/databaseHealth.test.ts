import { describe, expect, it } from 'vitest'
import {
  canStartMySQLCluster,
  healthFromCheckStatus,
  resolveDatabaseNodeHealth,
  resolveMySQLClusterServiceStatus,
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

  it('degrades instead of declaring an outage when one member topology check fails', () => {
    const source = {
      runtimeHealths: ['online', 'online', 'online'] as const,
      checkHealths: ['offline', 'online', 'online'] as const,
      hasPrimary: true
    }

    expect(resolveMySQLClusterServiceStatus(source)).toBe('degraded')
    expect(canStartMySQLCluster(source)).toBe(false)
  })

  it('keeps a fully verified cluster running', () => {
    expect(resolveMySQLClusterServiceStatus({
      runtimeHealths: ['online', 'online', 'online'],
      checkHealths: ['online', 'online', 'online'],
      hasPrimary: true
    })).toBe('running')
  })

  it('only allows complete-outage start when every current cluster check is offline', () => {
    expect(canStartMySQLCluster({
      runtimeHealths: ['online', 'online', 'online'],
      checkHealths: ['offline', 'offline', 'offline'],
      hasPrimary: false
    })).toBe(true)
    expect(resolveMySQLClusterServiceStatus({
      runtimeHealths: ['online', 'online', 'online'],
      checkHealths: ['offline', 'offline', 'offline'],
      hasPrimary: false
    })).toBe('unavailable')
  })

  it('does not declare an outage while cluster checks are probing', () => {
    expect(resolveMySQLClusterServiceStatus({
      runtimeHealths: ['online', 'online', 'online'],
      checkHealths: ['probing', 'probing', 'probing'],
      hasPrimary: false
    })).toBe('probing')
  })

  it('keeps the cluster unknown when current checks have no trustworthy result', () => {
    expect(resolveMySQLClusterServiceStatus({
      runtimeHealths: ['online', 'online', 'online'],
      checkHealths: ['offline', 'unknown', 'unknown'],
      hasPrimary: false
    })).toBe('unknown')
  })
})
