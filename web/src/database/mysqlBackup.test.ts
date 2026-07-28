import { beforeEach, describe, expect, it, vi } from 'vitest'

const api = vi.hoisted(() => ({
  get: vi.fn(),
  post: vi.fn(),
  del: vi.fn()
}))

vi.mock('../api/client', () => ({
  apiGet: api.get,
  apiPost: api.post,
  apiDelete: api.del
}))

import {
  backupDefaults,
  backupTargetCompatibility,
  clearMySQLMaintenance,
  deduplicateMySQLBackups,
  deleteMySQLBackup,
  groupMySQLMaintenance,
  groupMySQLReconciliation,
  listMySQLBackups,
  mysqlOperationAvailability,
  parseMySQLBackupRecord,
  parseMySQLMaintenance,
  parseMySQLReconciliation,
  restoreImpactKey,
  runMySQLReconciliation,
  selectMaintenanceDisasterBackup,
  startMySQLBackup,
  startMySQLRestore,
  trackTaskResponse,
  verifyMySQLBackup,
  type MySQLBackupRecord,
  type MySQLMaintenanceResult,
  type TaskTracker
} from './mysqlBackup'

const backupId = 'backup_1234567890abcdef12345678'
const taskId = 'tsk_1234567890abcdef12345678'
const clusterId = 'cluster_1234567890abcdef12345678'
const instanceId = 'app_1234567890abcdef12345678'
const serverId = 'srv_1234567890abcdef12345678'

const tracker: TaskTracker = {
  track: vi.fn()
}

function rawBackup(overrides: Record<string, unknown> = {}) {
  return {
    id: backupId,
    app: 'mysql',
    instanceId,
    serverId,
    backupType: 'logical-full',
    status: 'success',
    path: '/repository/mysql/private.tar',
    checksum: 'a'.repeat(64),
    size: 2048,
    taskId,
    metadata: JSON.stringify({
      name: 'nightly',
      threads: 4,
      maxRateMBps: 0,
      keepLast: 5,
      phase: 'success',
      mysqlVersion: '8.0.36',
      mysqlShellVersion: '8.0.36',
      manifestVersion: 2,
      topology: 'innodb-cluster',
      clusterId,
      schemas: ['aifar', 'billing'],
      verificationResult: 'success',
      verifiedAt: '2026-07-28T01:02:03Z',
      repositoryPath: '/must/not/render',
      password: 'must-not-render'
    }),
    createdAt: '2026-07-28T00:00:00Z',
    completedAt: '2026-07-28T00:05:00Z',
    encryptedValue: 'must-not-render',
    ...overrides
  }
}

function validMaintenance(overrides: Record<string, unknown> = {}) {
  return {
    version: 1,
    state: 'required',
    reason: 'restore_incomplete',
    scope: 'standalone',
    backupId,
    taskId,
    restorePhase: 'schema_mutation_started',
    recordedAt: '2026-07-28T00:00:00Z',
    ...overrides
  }
}

function metadataWithMaintenance(marker: Record<string, unknown>) {
  return JSON.stringify({ topology: marker.scope === 'cluster' ? 'innodb-cluster' : 'standalone', mysqlMaintenance: marker })
}

describe('MySQL backup records', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('parses only controlled backup fields and metadata instead of exposing backend paths or secrets', () => {
    const parsed = parseMySQLBackupRecord(rawBackup())

    expect(parsed).toEqual({
      id: backupId,
      instanceId,
      serverId,
      backupType: 'logical-full',
      status: 'success',
      checksum: 'a'.repeat(64),
      size: 2048,
      taskId,
      metadata: {
        name: 'nightly',
        phase: 'success',
        mysqlVersion: '8.0.36',
        mysqlShellVersion: '8.0.36',
        manifestVersion: 2,
        topology: 'innodb-cluster',
        clusterId,
        schemas: ['aifar', 'billing'],
        verificationResult: 'success',
        verifiedAt: '2026-07-28T01:02:03Z'
      },
      createdAt: '2026-07-28T00:00:00Z',
      completedAt: '2026-07-28T00:05:00Z'
    })
    expect(JSON.stringify(parsed)).not.toContain('/repository')
    expect(JSON.stringify(parsed)).not.toContain('must-not-render')
  })

  it('rejects malformed records instead of inventing display-safe values', () => {
    expect(parseMySQLBackupRecord(rawBackup({ status: 'restoring' }))).toBeNull()
    expect(parseMySQLBackupRecord(rawBackup({ metadata: '{broken' }))).toBeNull()
    expect(parseMySQLBackupRecord(rawBackup({ size: -1 }))).toBeNull()
  })

  it('de-duplicates cluster list items by backup ID while preserving their order', () => {
    const first = parseMySQLBackupRecord(rawBackup()) as MySQLBackupRecord
    const duplicate = { ...first, instanceId: 'app_aaaaaaaaaaaaaaaaaaaaaaaa' }
    const second = { ...first, id: 'backup_aaaaaaaaaaaaaaaaaaaaaaaa', createdAt: '2026-07-27T00:00:00Z' }

    expect(deduplicateMySQLBackups([first, duplicate, second]).map((item) => item.id)).toEqual([
      backupId,
      'backup_aaaaaaaaaaaaaaaaaaaaaaaa'
    ])
  })

  it('uses backend defaults but normalizes invalid bounds and optional retention', () => {
    expect(backupDefaults({ threads: 8, maxRateMBps: 96, keepLast: 12 })).toEqual({
      name: '',
      threads: 8,
      maxRateMBps: 96,
      keepLast: 12
    })
    expect(backupDefaults({ threads: 0, maxRateMBps: -5, keepLast: 0 })).toEqual({
      name: '',
      threads: 4,
      maxRateMBps: 0,
      keepLast: undefined
    })
  })

  it('returns distinct impact copy keys for standalone, healthy cluster, and disaster restore', () => {
    expect(restoreImpactKey('standalone')).toBe('database.mysqlBackup.restoreImpactStandalone')
    expect(restoreImpactKey('healthy-cluster')).toBe('database.mysqlBackup.restoreImpactCluster')
    expect(restoreImpactKey('disaster-rebuild')).toBe('database.mysqlBackup.restoreImpactDisaster')
  })

  it('requires a successful logical backup owned by the exact standalone instance or cluster', () => {
    const standalone = parseMySQLBackupRecord(rawBackup({
      metadata: JSON.stringify({ topology: 'standalone', mysqlVersion: '8.0.36', manifestVersion: 2, schemas: ['aifar'] })
    })) as MySQLBackupRecord
    const cluster = parseMySQLBackupRecord(rawBackup()) as MySQLBackupRecord

    expect(backupTargetCompatibility(standalone, {
      topology: 'standalone', instanceId, serverId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: true, reasonKey: '' })
    expect(backupTargetCompatibility(standalone, {
      topology: 'standalone', instanceId: 'app_aaaaaaaaaaaaaaaaaaaaaaaa', serverId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityOwnership' })
    expect(backupTargetCompatibility(cluster, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: true, reasonKey: '' })
    const legacyClusterId = 'mysql_cluster_aaaaaaaaaaaaaaaaaaaaaaaa'
    const legacyCluster = parseMySQLBackupRecord(rawBackup({
      metadata: JSON.stringify({ topology: 'innodb-cluster', clusterId: legacyClusterId, mysqlVersion: '8.0.36', manifestVersion: 2, schemas: ['aifar'] })
    })) as MySQLBackupRecord
    expect(backupTargetCompatibility(legacyCluster, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId: legacyClusterId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: true, reasonKey: '' })
    expect(backupTargetCompatibility(cluster, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId: 'cluster_aaaaaaaaaaaaaaaaaaaaaaaa', mysqlVersion: '8.0.36'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityCluster' })
    expect(backupTargetCompatibility({ ...cluster, status: 'failed' }, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityStatus' })
    expect(backupTargetCompatibility(cluster, {
      topology: 'standalone', instanceId, serverId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityTopology' })
    expect(backupTargetCompatibility(cluster, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId, mysqlVersion: '8.0.37'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityVersion' })
    expect(backupTargetCompatibility({ ...cluster, metadata: { ...cluster.metadata, mysqlVersion: undefined } }, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityVersion' })
    expect(backupTargetCompatibility({ ...cluster, metadata: { ...cluster.metadata, topology: undefined } }, {
      topology: 'innodb-cluster', instanceId, serverId, clusterId, mysqlVersion: '8.0.36'
    })).toEqual({ compatible: false, reasonKey: 'database.mysqlBackup.compatibilityTopology' })
  })

  it('selects only the exact verified manifest-v2 backup named by the maintenance marker', () => {
    const marker = validMaintenance({ scope: 'cluster', clusterId, restorePhase: 'load_complete' })
    const maintenance: MySQLMaintenanceResult = { kind: 'required', state: marker as never }
    const marked = parseMySQLBackupRecord(rawBackup()) as MySQLBackupRecord
    const newer = { ...marked, id: 'backup_aaaaaaaaaaaaaaaaaaaaaaaa', createdAt: '2026-07-29T00:00:00Z' }
    const target = { topology: 'innodb-cluster', instanceId, serverId, clusterId, mysqlVersion: '8.0.36' }

    expect(selectMaintenanceDisasterBackup([newer, marked], maintenance, target)).toEqual({ backup: marked, reasonKey: '' })
    expect(selectMaintenanceDisasterBackup([newer], maintenance, target)).toEqual({
      backup: null, reasonKey: 'database.mysqlBackup.disasterMarkerBackupMissing'
    })
    expect(selectMaintenanceDisasterBackup([{ ...marked, metadata: { ...marked.metadata, verificationResult: undefined } }], maintenance, target)).toEqual({
      backup: null, reasonKey: 'database.mysqlBackup.disasterMarkerBackupInvalid'
    })
    expect(selectMaintenanceDisasterBackup([{ ...marked, metadata: { ...marked.metadata, manifestVersion: undefined } }], maintenance, target)).toEqual({
      backup: null, reasonKey: 'database.mysqlBackup.disasterMarkerBackupInvalid'
    })
  })
})

describe('strict MySQL maintenance state', () => {
  it('accepts an exact standalone UTC marker', () => {
    expect(parseMySQLMaintenance(metadataWithMaintenance(validMaintenance()))).toEqual({
      kind: 'required',
      state: validMaintenance()
    })
  })

  it.each([
    ['missing required field', (() => { const value: Record<string, unknown> = validMaintenance(); delete value.taskId; return value })()],
    ['unknown field', validMaintenance({ path: '/tmp/private' })],
    ['wrong backup prefix', validMaintenance({ backupId: `app_${'a'.repeat(24)}` })],
    ['wrong task prefix', validMaintenance({ taskId: `backup_${'a'.repeat(24)}` })],
    ['non-UTC timestamp', validMaintenance({ recordedAt: '2026-07-28T08:00:00+08:00' })],
    ['impossible UTC calendar date', validMaintenance({ recordedAt: '2026-02-30T00:00:00Z' })],
    ['standalone marker with cluster ID', validMaintenance({ clusterId })],
    ['cluster marker without cluster ID', validMaintenance({ scope: 'cluster' })]
  ])('fails closed for %s', (_name, marker) => {
    expect(parseMySQLMaintenance(metadataWithMaintenance(marker))).toEqual({ kind: 'invalid' })
  })

  it('fails closed when cluster members have missing or divergent markers', () => {
    const clusterMarker = validMaintenance({ scope: 'cluster', clusterId, restorePhase: 'load_complete' })
    const same = metadataWithMaintenance(clusterMarker)
    const divergent = metadataWithMaintenance({ ...clusterMarker, backupId: 'backup_aaaaaaaaaaaaaaaaaaaaaaaa' })

    expect(groupMySQLMaintenance('innodb-cluster', [same, same, same], clusterId)).toEqual({
      kind: 'required',
      state: clusterMarker
    })
    expect(groupMySQLMaintenance('innodb-cluster', [same, same, divergent], clusterId)).toEqual({ kind: 'invalid' })
    expect(groupMySQLMaintenance('innodb-cluster', [same, same, '{}'], clusterId)).toEqual({ kind: 'invalid' })
    expect(groupMySQLMaintenance('innodb-cluster', [same, same], clusterId)).toEqual({ kind: 'invalid' })
    expect(groupMySQLMaintenance('innodb-cluster', ['{}'], clusterId)).toEqual({ kind: 'invalid' })
    expect(groupMySQLMaintenance('innodb-cluster', ['{}', '{}'], clusterId)).toEqual({ kind: 'invalid' })
  })
})

describe('strict MySQL reconciliation state', () => {
  const marker = { version: 1, kind: 'local_infile', originalValue: 'OFF', recordedAt: '2026-07-28T00:00:00Z', taskId }

  it('parses the exact non-secret marker and identifies the affected instance', () => {
    expect(parseMySQLReconciliation(JSON.stringify({ mysqlReconciliation: marker }))).toEqual({ kind: 'required', state: marker })
    expect(groupMySQLReconciliation([
      { instanceId, metadata: JSON.stringify({ mysqlReconciliation: marker }) },
      { instanceId: 'app_222222222222222222222222', metadata: '{}' },
      { instanceId: 'app_333333333333333333333333', metadata: '{}' }
    ])).toEqual({ kind: 'required', instanceId, state: marker })
  })

  it.each([
    { ...marker, version: 2 },
    { ...marker, kind: 'other' },
    { ...marker, originalValue: 'MAYBE' },
    { ...marker, recordedAt: '2026-07-28T08:00:00+08:00' },
    { ...marker, secret: 'must-not-pass' }
  ])('fails closed for malformed reconciliation marker %#', (invalid) => {
    expect(parseMySQLReconciliation(JSON.stringify({ mysqlReconciliation: invalid }))).toEqual({ kind: 'invalid' })
  })

  it('fails closed for multiple affected members', () => {
    expect(groupMySQLReconciliation([
      { instanceId, metadata: JSON.stringify({ mysqlReconciliation: marker }) },
      { instanceId: 'app_222222222222222222222222', metadata: JSON.stringify({ mysqlReconciliation: marker }) }
    ])).toEqual({ kind: 'invalid' })
  })
})

describe('operation availability', () => {
  const noMaintenance: MySQLMaintenanceResult = { kind: 'none' }
  const maintenance: MySQLMaintenanceResult = { kind: 'required', state: validMaintenance() as never }

  it('allows supported online topology actions at the correct permission level', () => {
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'standalone', status: 'online', canManage: true, isOwner: false,
      nodeCount: 1, maintenance: noMaintenance
    })).toMatchObject({ backup: true, records: true, verify: true, restore: false, disaster: false, clearMaintenance: false })

    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'innodb-cluster', status: 'online', canManage: true, isOwner: true,
      nodeCount: 3, maintenance: noMaintenance
    })).toMatchObject({ backup: true, records: true, verify: true, restore: true, disaster: false, clearMaintenance: false })
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'standalone', status: 'running', canManage: true, isOwner: true,
      nodeCount: 1, maintenance: noMaintenance
    })).toMatchObject({ backup: true, restore: true })
  })

  it('blocks mutation actions for unsupported, offline, or unauthorized groups', () => {
    expect(mysqlOperationAvailability({
      app: 'redis', topology: 'standalone', status: 'online', canManage: true, isOwner: true,
      nodeCount: 1, maintenance: noMaintenance
    })).toMatchObject({ visible: false, backup: false, restore: false })
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'standalone', status: 'offline', canManage: true, isOwner: true,
      nodeCount: 1, maintenance: noMaintenance
    })).toMatchObject({ backup: false, restore: false })
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'standalone', status: 'online', canManage: false, isOwner: false,
      nodeCount: 1, maintenance: noMaintenance
    })).toMatchObject({ backup: false, records: true, verify: false, restore: false })
  })

  it('fails closed for maintenance, while exposing only owner clear and independently confirmed disaster recovery', () => {
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'standalone', status: 'online', canManage: true, isOwner: true,
      nodeCount: 1, maintenance
    })).toMatchObject({ backup: false, restore: false, disaster: false, clearMaintenance: true, lifecycleBlocked: true })
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'innodb-cluster', status: 'degraded', canManage: true, isOwner: true,
      nodeCount: 3, maintenance: { ...maintenance, state: validMaintenance({ scope: 'cluster', clusterId }) as never }
    })).toMatchObject({ backup: false, restore: false, disaster: true, clearMaintenance: true, lifecycleBlocked: true })
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'innodb-cluster', status: 'degraded', canManage: true, isOwner: false,
      nodeCount: 3, maintenance
    })).toMatchObject({ disaster: false, clearMaintenance: false })
    expect(mysqlOperationAvailability({
      app: 'mysql', topology: 'standalone', status: 'online', canManage: true, isOwner: true,
      nodeCount: 1, maintenance: { kind: 'invalid' }
    })).toMatchObject({ backup: false, restore: false, clearMaintenance: false, lifecycleBlocked: true, controlStateInvalid: true })
  })
})

describe('typed API and task tracking', () => {
  beforeEach(() => {
    vi.clearAllMocks()
  })

  it('lists and sanitizes records without returning backend-only path fields', async () => {
    api.get.mockResolvedValue({
      instanceId,
      items: [rawBackup(), rawBackup()],
      defaults: { threads: 6, maxRateMBps: 32, keepLast: 7 }
    })

    const response = await listMySQLBackups(instanceId)

    expect(api.get).toHaveBeenCalledWith(`/apps/instances/${instanceId}/backups`)
    expect(response.items).toHaveLength(1)
    expect(response.defaults).toEqual({ threads: 6, maxRateMBps: 32, keepLast: 7 })
    expect(JSON.stringify(response)).not.toContain('/repository')
  })

  it('submits the exact backup body and tracks the returned task', async () => {
    api.post.mockResolvedValue({ taskId, status: 'pending' })

    await startMySQLBackup(instanceId, { name: 'nightly', threads: 4, maxRateMBps: 64, keepLast: 8 }, tracker, 'backup')

    expect(api.post).toHaveBeenCalledWith(`/apps/instances/${instanceId}/backup`, {
      name: 'nightly', threads: 4, maxRateMBps: 64, keepLast: 8
    })
    expect(tracker.track).toHaveBeenCalledWith(taskId, 'backup')
  })

  it('maps healthy-cluster to the exact ordinary restore wire body', async () => {
    api.post.mockResolvedValue({ taskId, status: 'pending' })

    await startMySQLRestore(instanceId, {
      backupId,
      mode: 'healthy-cluster',
      maintenanceConfirmed: true,
      createPreRestoreBackup: true,
      threads: 8
    }, tracker, 'restore')

    expect(api.post).toHaveBeenCalledWith(`/apps/instances/${instanceId}/restore`, {
      backupId,
      mode: 'innodb-cluster',
      maintenanceConfirmed: true,
      createPreRestoreBackup: true,
      disasterConfirmed: false,
      threads: 8
    })
    expect(JSON.stringify(api.post.mock.calls[0])).not.toContain('healthy-cluster')
  })

  it('keeps disaster passwords in the request only and tracks no secret-bearing label', async () => {
    api.post.mockResolvedValue({ taskId, status: 'pending' })
    const instance2 = 'app_aaaaaaaaaaaaaaaaaaaaaaaa'
    const instance3 = 'app_bbbbbbbbbbbbbbbbbbbbbbbb'
    const server2 = 'srv_aaaaaaaaaaaaaaaaaaaaaaaa'
    const server3 = 'srv_bbbbbbbbbbbbbbbbbbbbbbbb'
    const targetMapping = { [instanceId]: serverId, [instance2]: server2, [instance3]: server3 }
    const passwords = { [serverId]: 'ssh-password', [server2]: 'ssh-password-2', [server3]: 'ssh-password-3' }

    await startMySQLRestore(instanceId, {
      backupId,
      mode: 'disaster-rebuild',
      maintenanceConfirmed: true,
      createPreRestoreBackup: false,
      disasterConfirmed: true,
      threads: 4,
      targetMapping,
      serverPasswords: passwords
    }, tracker, 'disaster rebuild')

    expect(api.post).toHaveBeenCalledWith(`/apps/instances/${instanceId}/restore`, {
      backupId,
      mode: 'disaster-rebuild',
      maintenanceConfirmed: true,
      createPreRestoreBackup: false,
      disasterConfirmed: true,
      threads: 4,
      targetMapping,
      serverPasswords: passwords
    })
    expect(tracker.track).toHaveBeenCalledWith(taskId, 'disaster rebuild')
    expect(JSON.stringify(vi.mocked(tracker.track).mock.calls)).not.toContain('ssh-password')
  })

  it('uses the exact verify and delete endpoints', async () => {
    api.post.mockResolvedValue({ taskId, status: 'pending' })
    api.del.mockResolvedValue({ backup: rawBackup() })

    await verifyMySQLBackup(backupId, tracker, 'verify')
    const deleted = await deleteMySQLBackup(backupId)

    expect(api.post).toHaveBeenCalledWith(`/apps/backups/${backupId}/verify`, {})
    expect(api.del).toHaveBeenCalledWith(`/apps/backups/${backupId}`)
    expect(deleted.backup).toMatchObject({ id: backupId })
    expect(JSON.stringify(deleted)).not.toContain('/repository')
    expect(tracker.track).toHaveBeenCalledWith(taskId, 'verify')
  })

  it('submits the exact owner clear body and tracks the clear task response', async () => {
    api.post.mockResolvedValue({ taskId, status: 'pending' })

    await clearMySQLMaintenance(instanceId, tracker, 'clear maintenance')

    expect(api.post).toHaveBeenCalledWith(`/apps/instances/${instanceId}/mysql/maintenance/clear`, { recoveryConfirmed: true })
    expect(tracker.track).toHaveBeenCalledWith(taskId, 'clear maintenance')
  })

  it('submits the exact owner reconciliation body for the affected instance and tracks the task', async () => {
    api.post.mockResolvedValue({ taskId, status: 'pending' })

    await runMySQLReconciliation(instanceId, tracker, 'reconcile local_infile')

    expect(api.post).toHaveBeenCalledWith(`/apps/instances/${instanceId}/mysql/reconciliation/run`, { reconciliationConfirmed: true })
    expect(tracker.track).toHaveBeenCalledWith(taskId, 'reconcile local_infile')
  })

  it('rejects malformed mutation responses instead of tracking an empty task', () => {
    expect(() => trackTaskResponse({ taskId: '' }, tracker, 'backup')).toThrow('Invalid task response')
    expect(tracker.track).not.toHaveBeenCalled()
  })
})
