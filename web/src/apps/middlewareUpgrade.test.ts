import { describe, expect, it } from 'vitest'
import { middlewareUpgradeTarget } from './middlewareUpgrade'

describe('middleware upgrade eligibility', () => {
  const mysql = (id: string, version = '8.0.36') => ({
    id, app: 'mysql', serverId: id, version, topology: 'innodb-cluster'
  })
  const minio = (id: string) => ({
    id, app: 'minio', serverId: id, version: '2025-10-15T17-29-55Z', topology: 'bucket-replication'
  })

  it('offers the exact supported version only for the whole source group', () => {
    expect(middlewareUpgradeTarget('mysql', [mysql('a'), mysql('b'), mysql('c')])?.version).toBe('8.4.13')
    expect(middlewareUpgradeTarget('minio', [minio('a'), minio('b')])?.version).toBe('20261112')
  })

  it('withholds upgrades for incomplete, duplicate, and partially changed groups', () => {
    expect(middlewareUpgradeTarget('mysql', [mysql('a'), mysql('b')])).toBeNull()
    expect(middlewareUpgradeTarget('minio', [minio('a'), minio('a')])).toBeNull()
    expect(middlewareUpgradeTarget('mysql', [mysql('a'), mysql('b'), mysql('c', '8.4.13')])).toBeNull()
  })
})
