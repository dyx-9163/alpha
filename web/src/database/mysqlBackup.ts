import { apiDelete, apiGet, apiPost } from '../api/client'

export type MySQLBackupStatus = 'pending' | 'running' | 'success' | 'failed' | 'deleted'
export type MySQLRestoreMode = 'standalone' | 'healthy-cluster' | 'disaster-rebuild'

export interface MySQLMaintenanceState {
  version: 1
  state: 'required'
  reason: 'restore_incomplete'
  scope: 'standalone' | 'cluster'
  clusterId?: string
  backupId: string
  taskId: string
  restorePhase: 'schema_mutation_started' | 'load_complete'
  recordedAt: string
}

export interface MySQLReconciliationState {
  version: 1
  kind: 'local_infile'
  originalValue: 'ON' | 'OFF'
  recordedAt: string
  taskId: string
}

export interface MySQLBackupMetadata {
  manifestVersion?: 2
  name?: string
  phase?: string
  mysqlVersion?: string
  mysqlShellVersion?: string
  topology?: 'standalone' | 'innodb-cluster'
  clusterId?: string
  schemas: string[]
  verificationResult?: 'success' | 'failed'
  verifiedAt?: string
}

export interface MySQLBackupRecord {
  id: string
  instanceId: string
  serverId: string
  backupType: 'logical-full' | 'pre-restore'
  status: MySQLBackupStatus
  checksum: string
  size: number
  taskId?: string
  metadata: MySQLBackupMetadata
  createdAt: string
  completedAt?: string
}

export interface MySQLBackupDefaults {
  threads: number
  maxRateMBps: number
  keepLast?: number
}

export type MySQLBackupSchemaCategory = 'server-system' | 'cluster-metadata' | 'business'

export interface MySQLBackupSchema {
  name: string
  category: MySQLBackupSchemaCategory
  selectable: boolean
  selectedByDefault: boolean
}

export interface MySQLBackupSchemaCatalog {
  instanceId: string
  sourceInstanceId: string
  sourceServerId: string
  schemas: MySQLBackupSchema[]
}

export interface MySQLBackupParameters extends MySQLBackupDefaults {
  name: string
  schemas: string[]
}

export interface MySQLBackupListResponse {
  instanceId: string
  items: MySQLBackupRecord[]
  defaults: MySQLBackupDefaults
}

export interface TaskResponse {
  taskId: string
  status?: string
}

export interface TaskTracker {
  track(taskId: string, label?: string): void
}

export type MySQLMaintenanceResult =
  | { kind: 'none' }
  | { kind: 'required'; state: MySQLMaintenanceState }
  | { kind: 'invalid' }

export type MySQLReconciliationResult =
  | { kind: 'none' }
  | { kind: 'required'; instanceId: string; state: MySQLReconciliationState }
  | { kind: 'invalid' }

export type MySQLReconciliationMarkerResult =
  | { kind: 'none' }
  | { kind: 'required'; state: MySQLReconciliationState }
  | { kind: 'invalid' }

export type MySQLActionName = 'check' | 'start' | 'delete' | 'backup' | 'restore'

export interface MySQLOperationAvailability {
  visible: boolean
  backup: boolean
  records: boolean
  verify: boolean
  restore: boolean
  resumeRestore: boolean
  disaster: boolean
  clearMaintenance: boolean
  reconcile: boolean
  lifecycleBlocked: boolean
  controlStateInvalid: boolean
  reasonKey: string
}

export interface MySQLAvailabilityInput {
  app: string
  topology: string
  status: string
  canManage: boolean
  isOwner: boolean
  nodeCount: number
  maintenance: MySQLMaintenanceResult
  reconciliation?: MySQLReconciliationResult
}

export interface MySQLRestoreTarget {
  topology: string
  mysqlVersion: string
  instanceId: string
  serverId: string
  clusterId?: string
}

export interface OrdinaryRestoreRequest {
  backupId: string
  mode: 'standalone' | 'healthy-cluster'
  maintenanceConfirmed: true
  createPreRestoreBackup: boolean
  resumeMaintenance?: true
  threads: number
}

export interface DisasterRestoreRequest {
  backupId: string
  mode: 'disaster-rebuild'
  maintenanceConfirmed: true
  createPreRestoreBackup: false
  disasterConfirmed: true
  threads: number
  targetMapping: Record<string, string>
  serverPasswords: Record<string, string>
}

export type MySQLRestoreRequest = OrdinaryRestoreRequest | DisasterRestoreRequest

const backupStatuses = new Set<MySQLBackupStatus>(['pending', 'running', 'success', 'failed', 'deleted'])
const controlledBackupId = /^backup_[0-9a-f]{24}$/
const controlledTaskId = /^tsk_[0-9a-f]{24}$/
const controlledClusterId = /^cluster_[0-9a-f]{24}$/
const controlledBackupClusterId = /^(?:cluster|mysql_cluster)_[0-9a-f]{24}$/
const controlledInstanceId = /^app_[0-9a-f]{24}$/
const controlledServerId = /^srv_[0-9a-f]{24}$/
const checksumPattern = /^[0-9a-f]{64}$/
const utcTimestampPattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2}):(\d{2})(?:\.\d+)?Z$/

export function parseMySQLBackupRecord(value: unknown): MySQLBackupRecord | null {
  const record = objectValue(value)
  if (!record) return null
  const metadata = parseBackupMetadata(record.metadata)
  const status = stringValue(record.status) as MySQLBackupStatus
  const backupType = stringValue(record.backupType)
  const id = stringValue(record.id)
  const instanceId = stringValue(record.instanceId)
  const serverId = stringValue(record.serverId)
  const checksum = stringValue(record.checksum).toLowerCase()
  const size = numberValue(record.size)
  const createdAt = safeTimestamp(record.createdAt)
  const completedAt = optionalTimestamp(record.completedAt)
  const taskId = stringValue(record.taskId)
  if (!metadata || !controlledBackupId.test(id) || !controlledInstanceId.test(instanceId) ||
      !controlledServerId.test(serverId) || !backupStatuses.has(status) ||
      (backupType !== 'logical-full' && backupType !== 'pre-restore') ||
      !Number.isSafeInteger(size) || size < 0 || !createdAt || completedAt === null ||
      (checksum !== '' && !checksumPattern.test(checksum)) ||
      (taskId !== '' && !controlledTaskId.test(taskId))) {
    return null
  }
  return {
    id,
    instanceId,
    serverId,
    backupType,
    status,
    checksum,
    size,
    ...(taskId ? { taskId } : {}),
    metadata,
    createdAt,
    ...(completedAt ? { completedAt } : {})
  }
}

export function deduplicateMySQLBackups(items: MySQLBackupRecord[]) {
  const seen = new Set<string>()
  return items.filter((item) => {
    if (seen.has(item.id)) return false
    seen.add(item.id)
    return true
  })
}

export function isMySQLBackupVerifiable(record: MySQLBackupRecord) {
  return record.status === 'success' && record.backupType === 'logical-full'
}

export function latestVerifiableMySQLBackup(records: MySQLBackupRecord[]) {
  return records.find(isMySQLBackupVerifiable) ?? null
}

export function backupDefaults(value: unknown): Omit<MySQLBackupParameters, 'schemas'> {
  const defaults = objectValue(value) ?? {}
  const threads = positiveInteger(defaults.threads, 1, 64) ?? 4
  const maxRateMBps = nonNegativeNumber(defaults.maxRateMBps) ?? 0
  const keepLast = positiveInteger(defaults.keepLast, 1, Number.MAX_SAFE_INTEGER)
  return { name: '', threads, maxRateMBps, ...(keepLast ? { keepLast } : {}) }
}

export function restoreImpactKey(mode: MySQLRestoreMode) {
  if (mode === 'standalone') return 'database.mysqlBackup.restoreImpactStandalone'
  if (mode === 'healthy-cluster') return 'database.mysqlBackup.restoreImpactCluster'
  return 'database.mysqlBackup.restoreImpactDisaster'
}

export function backupTargetCompatibility(record: MySQLBackupRecord, target: MySQLRestoreTarget) {
  if (record.status !== 'success' || record.backupType !== 'logical-full' || !checksumPattern.test(record.checksum)) {
    return { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityStatus' }
  }
  if (record.metadata.manifestVersion !== 2) {
    return { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityManifest' }
  }
  if (!record.metadata.topology || record.metadata.topology !== target.topology) {
    return { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityTopology' }
  }
  if (!record.metadata.mysqlVersion || !target.mysqlVersion || record.metadata.mysqlVersion !== target.mysqlVersion) {
    return { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityVersion' }
  }
  if (target.topology === 'standalone') {
    if (record.instanceId !== target.instanceId || record.serverId !== target.serverId || record.metadata.clusterId) {
      return { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityOwnership' }
    }
    return { compatible: true, reasonKey: '' }
  }
  if (target.topology === 'innodb-cluster') {
    if (!target.clusterId || record.metadata.clusterId !== target.clusterId) {
      return { compatible: false, reasonKey: 'database.mysqlBackup.compatibilityCluster' }
    }
    return { compatible: true, reasonKey: '' }
  }
  return { compatible: false, reasonKey: 'database.mysqlBackup.unsupportedTopology' }
}

export function selectMaintenanceDisasterBackup(
  records: MySQLBackupRecord[],
  maintenance: MySQLMaintenanceResult,
  target: MySQLRestoreTarget
) {
  if (maintenance.kind !== 'required' || maintenance.state.scope !== 'cluster') {
    return { backup: null, reasonKey: 'database.mysqlBackup.disasterMarkerBackupInvalid' }
  }
  const backup = records.find((record) => record.id === maintenance.state.backupId) ?? null
  if (!backup) {
    return { backup: null, reasonKey: 'database.mysqlBackup.disasterMarkerBackupMissing' }
  }
  const compatibility = backupTargetCompatibility(backup, target)
  if (!compatibility.compatible || backup.metadata.verificationResult !== 'success' || backup.metadata.manifestVersion !== 2) {
    return { backup: null, reasonKey: 'database.mysqlBackup.disasterMarkerBackupInvalid' }
  }
  return { backup, reasonKey: '' }
}

export function parseMySQLMaintenance(metadataValue: unknown): MySQLMaintenanceResult {
  const metadata = jsonObject(metadataValue)
  if (!metadata) return { kind: 'invalid' }
  if (!Object.prototype.hasOwnProperty.call(metadata, 'mysqlMaintenance')) return { kind: 'none' }
  const marker = objectValue(metadata.mysqlMaintenance)
  if (!marker) return { kind: 'invalid' }
  const scope = stringValue(marker.scope)
  const allowedKeys = scope === 'cluster'
    ? ['version', 'state', 'reason', 'scope', 'clusterId', 'backupId', 'taskId', 'restorePhase', 'recordedAt']
    : ['version', 'state', 'reason', 'scope', 'backupId', 'taskId', 'restorePhase', 'recordedAt']
  if (!exactKeys(marker, allowedKeys)) return { kind: 'invalid' }
  const state: MySQLMaintenanceState = {
    version: 1,
    state: 'required',
    reason: 'restore_incomplete',
    scope: scope as 'standalone' | 'cluster',
    ...(scope === 'cluster' ? { clusterId: stringValue(marker.clusterId) } : {}),
    backupId: stringValue(marker.backupId),
    taskId: stringValue(marker.taskId),
    restorePhase: stringValue(marker.restorePhase) as MySQLMaintenanceState['restorePhase'],
    recordedAt: stringValue(marker.recordedAt)
  }
  if (marker.version !== 1 || marker.state !== 'required' || marker.reason !== 'restore_incomplete' ||
      (scope !== 'standalone' && scope !== 'cluster') ||
      !controlledBackupId.test(state.backupId) || !controlledTaskId.test(state.taskId) ||
      (state.restorePhase !== 'schema_mutation_started' && state.restorePhase !== 'load_complete') ||
      !isUTCTimestamp(state.recordedAt) ||
      (scope === 'cluster' ? !controlledClusterId.test(state.clusterId ?? '') : state.clusterId !== undefined)) {
    return { kind: 'invalid' }
  }
  return { kind: 'required', state }
}

export function groupMySQLMaintenance(topology: string, memberMetadata: unknown[], expectedClusterId = ''): MySQLMaintenanceResult {
  if (!memberMetadata.length) return { kind: 'invalid' }
  if (topology === 'innodb-cluster' && memberMetadata.length !== 3) return { kind: 'invalid' }
  if (topology !== 'innodb-cluster' && memberMetadata.length !== 1) return { kind: 'invalid' }
  const parsed = memberMetadata.map(parseMySQLMaintenance)
  if (parsed.every((item) => item.kind === 'none')) return { kind: 'none' }
  if (parsed.some((item) => item.kind !== 'required')) return { kind: 'invalid' }
  const states = parsed.map((item) => (item as { kind: 'required'; state: MySQLMaintenanceState }).state)
  const canonical = JSON.stringify(states[0])
  if (states.some((state) => JSON.stringify(state) !== canonical)) return { kind: 'invalid' }
  const state = states[0]
  if (topology === 'innodb-cluster') {
    if (memberMetadata.length !== 3 || state.scope !== 'cluster' || !expectedClusterId || state.clusterId !== expectedClusterId) return { kind: 'invalid' }
  } else if (state.scope !== 'standalone' || memberMetadata.length !== 1) {
    return { kind: 'invalid' }
  }
  return { kind: 'required', state }
}

export function parseMySQLReconciliation(metadataValue: unknown): MySQLReconciliationMarkerResult {
  const metadata = jsonObject(metadataValue)
  if (!metadata) return { kind: 'invalid' }
  if (!Object.prototype.hasOwnProperty.call(metadata, 'mysqlReconciliation')) return { kind: 'none' }
  const marker = objectValue(metadata.mysqlReconciliation)
  if (!marker || !exactKeys(marker, ['version', 'kind', 'originalValue', 'recordedAt', 'taskId'])) return { kind: 'invalid' }
  const state: MySQLReconciliationState = {
    version: 1,
    kind: 'local_infile',
    originalValue: stringValue(marker.originalValue) as 'ON' | 'OFF',
    recordedAt: stringValue(marker.recordedAt),
    taskId: stringValue(marker.taskId)
  }
  if (marker.version !== 1 || marker.kind !== 'local_infile' ||
      (state.originalValue !== 'ON' && state.originalValue !== 'OFF') ||
      !isUTCTimestamp(state.recordedAt) || !controlledTaskId.test(state.taskId)) {
    return { kind: 'invalid' }
  }
  return { kind: 'required', state }
}

export function groupMySQLReconciliation(members: Array<{ instanceId: string; metadata: unknown }>): MySQLReconciliationResult {
  if (!members.length || members.some((member) => !controlledInstanceId.test(member.instanceId))) return { kind: 'invalid' }
  const parsed = members.map((member) => ({ instanceId: member.instanceId, result: parseMySQLReconciliation(member.metadata) }))
  if (parsed.some((item) => item.result.kind === 'invalid')) return { kind: 'invalid' }
  const required = parsed.filter((item) => item.result.kind === 'required')
  if (required.length === 0) return { kind: 'none' }
  if (required.length !== 1) return { kind: 'invalid' }
  return { kind: 'required', instanceId: required[0].instanceId, state: (required[0].result as { kind: 'required'; state: MySQLReconciliationState }).state }
}

export function mysqlOperationAvailability(input: MySQLAvailabilityInput): MySQLOperationAvailability {
  const supportedTopology = ['standalone', 'innodb-cluster'].includes(input.topology)
  const topologyComplete = input.topology === 'standalone' ? input.nodeCount === 1 : input.topology === 'innodb-cluster' && input.nodeCount === 3
  const supported = input.app === 'mysql' && supportedTopology && topologyComplete
  const online = ['online', 'running', 'success', 'available'].includes(input.status)
  const reconciliation = input.reconciliation ?? { kind: 'none' as const }
  const lifecycleBlocked = input.maintenance.kind !== 'none' || reconciliation.kind !== 'none'
  const controlStateInvalid = input.maintenance.kind === 'invalid' || reconciliation.kind === 'invalid'
  const healthyAction = supported && online && input.canManage && !lifecycleBlocked
  const ownerAction = healthyAction && input.isOwner
  const maintenanceRequired = input.maintenance.kind === 'required'
  const disaster = supported && input.topology === 'innodb-cluster' && input.nodeCount === 3 &&
    input.canManage && input.isOwner && input.maintenance.kind === 'required' && input.maintenance.state.scope === 'cluster'
  const resumeRestore = supported && input.topology === 'standalone' && input.canManage && input.isOwner &&
    input.maintenance.kind === 'required' && input.maintenance.state.scope === 'standalone' &&
    ['schema_mutation_started', 'load_complete'].includes(input.maintenance.state.restorePhase) && reconciliation.kind === 'none'
  const clearMaintenance = supported && input.canManage && input.isOwner && maintenanceRequired && reconciliation.kind === 'none'
  const reconcile = supported && input.canManage && input.isOwner && reconciliation.kind === 'required'
  let reasonKey = ''
  if (input.maintenance.kind === 'invalid') reasonKey = 'database.mysqlBackup.maintenanceInvalid'
  else if (reconciliation.kind === 'invalid') reasonKey = 'database.mysqlBackup.reconciliationInvalid'
  else if (reconciliation.kind === 'required') reasonKey = 'database.mysqlBackup.reconciliationBlocked'
  else if (lifecycleBlocked) reasonKey = 'database.mysqlBackup.maintenanceBlocked'
  else if (!input.canManage) reasonKey = 'common.permissionDenied'
  else if (!online) reasonKey = 'database.mysqlBackup.offlineBlocked'
  else if (input.app === 'mysql' && supportedTopology && !topologyComplete) reasonKey = 'database.mysqlBackup.clusterIncomplete'
  else if (!supported) reasonKey = 'database.mysqlBackup.unsupportedTopology'
  else if (!input.isOwner) reasonKey = 'database.mysqlBackup.ownerRequired'
  return {
    visible: supported,
    backup: healthyAction,
    records: supported,
    verify: supported && input.canManage,
    restore: ownerAction,
    resumeRestore,
    disaster,
    clearMaintenance,
    reconcile,
    lifecycleBlocked,
    controlStateInvalid,
    reasonKey
  }
}

export function isMySQLLifecycleActionBlocked(action: MySQLActionName, maintenance: MySQLMaintenanceResult) {
  return maintenance.kind !== 'none' && ['check', 'start', 'delete', 'backup', 'restore'].includes(action)
}

export function trackTaskResponse(response: TaskResponse, tracker: TaskTracker, label: string) {
  const id = stringValue(response?.taskId)
  if (!controlledTaskId.test(id)) throw new Error('Invalid task response')
  tracker.track(id, label)
  return response
}

export async function listMySQLBackups(instanceId: string): Promise<MySQLBackupListResponse> {
  const id = requiredInstanceId(instanceId)
  const raw = await apiGet<unknown>(`/apps/instances/${encodeURIComponent(id)}/backups`)
  const response = objectValue(raw)
  if (!response) throw new Error('Invalid backup list response')
  const items = Array.isArray(response.items)
    ? response.items.map(parseMySQLBackupRecord).filter((item): item is MySQLBackupRecord => item !== null)
    : []
  const defaults = backupDefaults(response.defaults)
  return {
    instanceId: id,
    items: deduplicateMySQLBackups(items),
    defaults: { threads: defaults.threads, maxRateMBps: defaults.maxRateMBps, ...(defaults.keepLast ? { keepLast: defaults.keepLast } : {}) }
  }
}

export async function discoverMySQLBackupSchemas(instanceId: string): Promise<MySQLBackupSchemaCatalog> {
  const id = requiredInstanceId(instanceId)
  const raw = await apiGet<unknown>(`/apps/instances/${encodeURIComponent(id)}/backup-schemas`)
  const response = objectValue(raw)
  const sourceInstanceId = stringValue(response?.sourceInstanceId)
  const sourceServerId = stringValue(response?.sourceServerId)
  if (!response || stringValue(response.instanceId) !== id || !controlledInstanceId.test(sourceInstanceId) || !controlledServerId.test(sourceServerId) || !Array.isArray(response.schemas)) {
    throw new Error('Invalid backup schema response')
  }
  const seen = new Set<string>()
  const schemas = response.schemas.map((rawSchema) => {
    const schema = objectValue(rawSchema)
    const name = stringValue(schema?.name)
    const category = stringValue(schema?.category) as MySQLBackupSchemaCategory
    const selectable = schema?.selectable
    const selectedByDefault = schema?.selectedByDefault
    const business = category === 'business'
    const key = name.toLocaleLowerCase('en-US')
    if (!schema || !exactKeys(schema, ['name', 'category', 'selectable', 'selectedByDefault']) || !/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(name) ||
        !['server-system', 'cluster-metadata', 'business'].includes(category) || typeof selectable !== 'boolean' || typeof selectedByDefault !== 'boolean' ||
        selectable !== business || selectedByDefault !== business || seen.has(key)) {
      throw new Error('Invalid backup schema response')
    }
    seen.add(key)
    return { name, category, selectable, selectedByDefault }
  })
  return { instanceId: id, sourceInstanceId, sourceServerId, schemas }
}

export async function startMySQLBackup(instanceId: string, parameters: MySQLBackupParameters, tracker: TaskTracker, label: string) {
  const id = requiredInstanceId(instanceId)
  const name = parameters.name.trim()
  const threads = positiveInteger(parameters.threads, 1, 64)
  const maxRateMBps = nonNegativeNumber(parameters.maxRateMBps)
  const keepLast = positiveInteger(parameters.keepLast, 1, Number.MAX_SAFE_INTEGER)
	const schemas = Array.isArray(parameters.schemas) ? parameters.schemas.map((schema) => schema.trim()) : []
	const folded = schemas.map((schema) => schema.toLocaleLowerCase('en-US'))
	if (!name || threads === undefined || maxRateMBps === undefined || (parameters.keepLast !== undefined && keepLast === undefined) || !schemas.length ||
		schemas.some((schema) => !/^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(schema)) || new Set(folded).size !== schemas.length) {
    throw new Error('Invalid backup parameters')
  }
	const body = { name, threads, maxRateMBps, ...(keepLast ? { keepLast } : {}), schemas: [...schemas] }
  const response = await apiPost<TaskResponse>(`/apps/instances/${encodeURIComponent(id)}/backup`, body)
  return trackTaskResponse(response, tracker, label)
}

export async function verifyMySQLBackup(backupId: string, tracker: TaskTracker, label: string) {
  const id = requiredBackupId(backupId)
  const response = await apiPost<TaskResponse>(`/apps/backups/${encodeURIComponent(id)}/verify`, {})
  return trackTaskResponse(response, tracker, label)
}

export async function deleteMySQLBackup(backupId: string): Promise<{ backup: MySQLBackupRecord | null }> {
  const raw = await apiDelete<unknown>(`/apps/backups/${encodeURIComponent(requiredBackupId(backupId))}`)
  const response = objectValue(raw)
  return { backup: parseMySQLBackupRecord(response?.backup) }
}

export async function startMySQLRestore(instanceId: string, request: MySQLRestoreRequest, tracker: TaskTracker, label: string) {
  const id = requiredInstanceId(instanceId)
  const backupId = requiredBackupId(request.backupId)
  const threads = positiveInteger(request.threads, 1, 64)
  if (!threads || request.maintenanceConfirmed !== true) throw new Error('Invalid restore parameters')
  let body: Record<string, unknown>
  if (request.mode === 'disaster-rebuild') {
    if (request.createPreRestoreBackup !== false || request.disasterConfirmed !== true ||
        !exactMapping(request.targetMapping, controlledInstanceId, controlledServerId) ||
        !exactPasswords(request.serverPasswords, Object.values(request.targetMapping))) {
      throw new Error('Invalid disaster restore parameters')
    }
    body = {
      backupId,
      mode: 'disaster-rebuild',
      maintenanceConfirmed: true,
      createPreRestoreBackup: false,
      disasterConfirmed: true,
      threads,
      targetMapping: { ...request.targetMapping },
      serverPasswords: { ...request.serverPasswords }
    }
  } else {
    if (request.resumeMaintenance === true && request.mode !== 'standalone') throw new Error('Invalid restore parameters')
    body = {
      backupId,
      mode: request.mode === 'healthy-cluster' ? 'innodb-cluster' : 'standalone',
      maintenanceConfirmed: true,
      createPreRestoreBackup: request.createPreRestoreBackup,
      disasterConfirmed: false,
      ...(request.resumeMaintenance === true ? { resumeMaintenance: true } : {}),
      threads
    }
  }
  const response = await apiPost<TaskResponse>(`/apps/instances/${encodeURIComponent(id)}/restore`, body)
  return trackTaskResponse(response, tracker, label)
}

export async function clearMySQLMaintenance(instanceId: string, tracker: TaskTracker, label: string) {
  const id = requiredInstanceId(instanceId)
  const response = await apiPost<TaskResponse>(`/apps/instances/${encodeURIComponent(id)}/mysql/maintenance/clear`, { recoveryConfirmed: true })
  return trackTaskResponse(response, tracker, label)
}

export async function runMySQLReconciliation(instanceId: string, tracker: TaskTracker, label: string) {
  const id = requiredInstanceId(instanceId)
  const response = await apiPost<TaskResponse>(`/apps/instances/${encodeURIComponent(id)}/mysql/reconciliation/run`, { reconciliationConfirmed: true })
  return trackTaskResponse(response, tracker, label)
}

function parseBackupMetadata(value: unknown): MySQLBackupMetadata | null {
  const raw = jsonObject(value)
  if (!raw) return null
  const schemas = Array.isArray(raw.schemas) && raw.schemas.every((item) => typeof item === 'string' && item.trim())
    ? Array.from(new Set(raw.schemas.map((item) => item.trim())))
    : []
  const result: MySQLBackupMetadata = { schemas }
  if (raw.manifestVersion === 2) result.manifestVersion = 2
  const controlledResult = result as unknown as Record<string, unknown>
  assignControlledString(controlledResult, 'name', raw.name)
  assignControlledString(controlledResult, 'phase', raw.phase)
  assignControlledString(controlledResult, 'mysqlVersion', raw.mysqlVersion)
  assignControlledString(controlledResult, 'mysqlShellVersion', raw.mysqlShellVersion)
  const topology = stringValue(raw.topology)
  if (topology === 'standalone' || topology === 'innodb-cluster') result.topology = topology
  const clusterId = stringValue(raw.clusterId)
  if (controlledBackupClusterId.test(clusterId)) result.clusterId = clusterId
  const verificationResult = stringValue(raw.verificationResult)
  if (verificationResult === 'success' || verificationResult === 'failed') result.verificationResult = verificationResult
  const verifiedAt = safeTimestamp(raw.verifiedAt)
  if (verifiedAt) result.verifiedAt = verifiedAt
  return result
}

function assignControlledString(target: Record<string, unknown>, key: string, value: unknown) {
  const clean = stringValue(value)
  if (clean && clean.length <= 256 && !/[\x00-\x1f]/.test(clean)) target[key] = clean
}

function jsonObject(value: unknown): Record<string, unknown> | null {
  if (typeof value === 'string') {
    try {
      return objectValue(JSON.parse(value))
    } catch {
      return null
    }
  }
  return objectValue(value)
}

function objectValue(value: unknown): Record<string, unknown> | null {
  return value !== null && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : null
}

function exactKeys(value: Record<string, unknown>, allowed: string[]) {
  const actual = Object.keys(value).sort()
  return actual.length === allowed.length && actual.every((key, index) => key === [...allowed].sort()[index])
}

function stringValue(value: unknown) {
  return typeof value === 'string' ? value.trim() : ''
}

function numberValue(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) ? value : Number.NaN
}

function positiveInteger(value: unknown, minimum: number, maximum: number) {
  return typeof value === 'number' && Number.isInteger(value) && value >= minimum && value <= maximum ? value : undefined
}

function nonNegativeNumber(value: unknown) {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0 ? value : undefined
}

function safeTimestamp(value: unknown) {
  const text = stringValue(value)
  return text && isUTCTimestamp(text) ? text : ''
}

function optionalTimestamp(value: unknown): string | null | undefined {
  if (value === undefined || value === null || value === '') return undefined
  const text = safeTimestamp(value)
  return text || null
}

function isUTCTimestamp(value: string) {
  const match = utcTimestampPattern.exec(value)
  const parsed = new Date(value)
  if (!match || !Number.isFinite(parsed.getTime())) return false
  return parsed.getUTCFullYear() === Number(match[1]) && parsed.getUTCMonth() + 1 === Number(match[2]) &&
    parsed.getUTCDate() === Number(match[3]) && parsed.getUTCHours() === Number(match[4]) &&
    parsed.getUTCMinutes() === Number(match[5]) && parsed.getUTCSeconds() === Number(match[6])
}

function requiredInstanceId(value: string) {
  const id = value.trim()
  if (!controlledInstanceId.test(id)) throw new Error('Invalid MySQL instance ID')
  return id
}

function requiredBackupId(value: string) {
  const id = value.trim()
  if (!controlledBackupId.test(id)) throw new Error('Invalid MySQL backup ID')
  return id
}

function exactMapping(mapping: Record<string, string>, instancePattern: RegExp, serverPattern: RegExp) {
  const entries = Object.entries(mapping)
  return entries.length === 3 && new Set(entries.map(([, server]) => server)).size === 3 &&
    entries.every(([instance, server]) => instancePattern.test(instance) && serverPattern.test(server))
}

function exactPasswords(passwords: Record<string, string>, expectedServers: string[]) {
  const entries = Object.entries(passwords)
  const expected = new Set(expectedServers)
  return entries.length === 3 && entries.every(([server, password]) => expected.has(server) && typeof password === 'string' && password.trim() !== '')
}
