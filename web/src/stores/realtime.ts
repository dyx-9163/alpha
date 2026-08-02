import { defineStore } from 'pinia'
import { apiGet, asArray, eventStreamUrl } from '../api/client'
import { useAlertsStore } from './alerts'
import { useTaskProgressStore } from './taskProgress'

export type RealtimeEvent = {
  id?: string
  type?: string
  resource?: string
  resourceId?: string
  serverId?: string
  instanceId?: string
  taskId?: string
  status?: string
  version?: number
  collectedAt?: string
  createdAt?: string
  payload?: Record<string, unknown>
}

export type StatusSnapshot = {
  scope: string
  resourceId: string
  serverId?: string
  status?: string
  payload?: Record<string, unknown>
  lastError?: string
  version?: number
  collectedAt?: string
  updatedAt?: string
}

type AppInstanceLike = {
  id: string
  app?: string
  version?: string
  serverId?: string
  status?: string
  topology?: string
  metadata?: string
}

let source: EventSource | null = null
let reconnectTimer: number | undefined

export const useRealtimeStore = defineStore('realtime', {
  state: () => ({
    status: 'idle' as 'idle' | 'connecting' | 'connected' | 'reconnecting' | 'disconnected',
    error: '',
    lastEvent: null as RealtimeEvent | null,
    lastEventAt: 0,
    connectedAt: 0,
    reconnectAttempts: 0,
    revision: 0,
    statusRevision: 0,
    snapshotsLoadedAt: 0,
    statusRecoveryInFlight: false,
    statusRecoveryPending: false,
    statusSnapshotsByKey: {} as Record<string, StatusSnapshot>
  }),
  getters: {
    connected(state): boolean {
      return state.status === 'connected'
    },
    statusSnapshots(state): StatusSnapshot[] {
      return Object.values(state.statusSnapshotsByKey)
    },
    appInstanceSnapshot(state): (instanceId: string) => StatusSnapshot | undefined {
      return (instanceId: string) => state.statusSnapshotsByKey[snapshotKey('app.instance', instanceId)]
    },
    dockerSummarySnapshot(state): (serverId: string) => StatusSnapshot | undefined {
      return (serverId: string) => state.statusSnapshotsByKey[snapshotKey('docker.summary', serverId)]
    },
    serverSnapshot(state): (serverId: string) => StatusSnapshot | undefined {
      return (serverId: string) => state.statusSnapshotsByKey[snapshotKey('server', serverId)]
    },
    aifarRuntimeSnapshot(state): (instanceId: string) => StatusSnapshot | undefined {
      return (instanceId: string) => state.statusSnapshotsByKey[snapshotKey('aifar.runtime', instanceId)]
    }
  },
  actions: {
    connect() {
      if (!localStorage.getItem('aifar-session-token')) {
        this.disconnect()
        return
      }
      if (source && (this.status === 'connected' || this.status === 'connecting')) {
        return
      }
      clearReconnectTimer()
      this.status = this.reconnectAttempts > 0 ? 'reconnecting' : 'connecting'
      this.error = ''
      source = new EventSource(eventStreamUrl())
      source.addEventListener('open', () => {
        this.status = 'connected'
        this.connectedAt = Date.now()
        this.reconnectAttempts = 0
        this.error = ''
        void this.loadStatusSnapshots()
      })
      source.addEventListener('aifar-event', (event) => {
        this.applyEvent(parseRealtimeEvent((event as MessageEvent).data))
      })
      source.addEventListener('heartbeat', () => {
        this.lastEventAt = Date.now()
      })
      source.addEventListener('error', () => {
        this.status = 'reconnecting'
        this.error = 'event stream disconnected'
        this.scheduleReconnect()
      })
    },
    disconnect() {
      clearReconnectTimer()
      if (source) {
        source.close()
        source = null
      }
      this.status = 'disconnected'
      this.error = ''
    },
    scheduleReconnect() {
      if (reconnectTimer !== undefined) {
        return
      }
      if (source) {
        source.close()
        source = null
      }
      this.reconnectAttempts += 1
      const delay = Math.min(30000, 1200 * this.reconnectAttempts)
      reconnectTimer = window.setTimeout(() => {
        reconnectTimer = undefined
        this.connect()
      }, delay)
    },
    applyEvent(event: RealtimeEvent | null) {
      if (!event?.type) {
        return
      }
      this.lastEvent = event
      this.lastEventAt = Date.now()
      this.revision += 1
      const snapshot = snapshotFromRealtimeEvent(event)
      if (snapshot) {
        this.applyStatusSnapshot(snapshot)
      }
      if (event.type === 'realtime.gap') {
        this.recoverStatusSnapshots()
      }
      if (event.taskId && (event.type === 'task.updated' || event.type === 'task.finished')) {
        void useTaskProgressStore().refreshTask(event.taskId)
      }
      if (event.type.startsWith('alert.')) {
        useAlertsStore().applyRealtimeEvent(event)
      }
    },
    async loadStatusSnapshots() {
      let result: { items?: StatusSnapshot[] } | null
      try {
        result = await apiGet<{ items?: StatusSnapshot[] } | null>('/status/snapshots')
      } catch {
        return false
      }
      const merged = mergeStatusSnapshots(this.statusSnapshotsByKey, asArray<StatusSnapshot>(result?.items))
      if (merged.changed) {
        this.statusSnapshotsByKey = merged.snapshotsByKey
        this.statusRevision += 1
      }
      this.snapshotsLoadedAt = Date.now()
      return true
    },
    recoverStatusSnapshots() {
      if (this.statusRecoveryInFlight) {
        this.statusRecoveryPending = true
        return
      }
      this.statusRecoveryInFlight = true
      void this.loadStatusSnapshots().finally(() => {
        this.statusRecoveryInFlight = false
        if (this.statusRecoveryPending) {
          this.statusRecoveryPending = false
          this.recoverStatusSnapshots()
        }
      })
    },
    applyStatusSnapshot(snapshot: StatusSnapshot) {
      const merged = mergeStatusSnapshots(this.statusSnapshotsByKey, [snapshot])
      if (!merged.changed) {
        return
      }
      this.statusSnapshotsByKey = merged.snapshotsByKey
      this.statusRevision += 1
    }
  }
})

export function applyRealtimeStatusToAppInstance<T extends AppInstanceLike>(instance: T, snapshot?: StatusSnapshot): T {
  if (!snapshot || normalizeString(snapshot.scope) !== 'app.instance') {
    return instance
  }
  const payload = objectRecord(snapshot.payload)
  const status = normalizeString(snapshot.status || payload.status || instance.status) || instance.status || 'unknown'
  const checkedAt = stringValue(snapshot.collectedAt || payload.updatedAt || snapshot.updatedAt)
  const details = objectRecord(payload.details)
  const metadata = parseMetadata(instance.metadata)
  const lastCheck = {
    ...objectRecord(metadata.lastCheck),
    ...details,
    status,
    checkedAt,
    message: stringValue(payload.message || snapshot.lastError),
    details
  }
  const nextMetadata = {
    ...metadata,
    ...details,
    lastCheck
  }
  return {
    ...instance,
    status,
    metadata: JSON.stringify(nextMetadata)
  }
}

function parseRealtimeEvent(raw: string) {
  try {
    return JSON.parse(raw) as RealtimeEvent
  } catch {
    return null
  }
}

function clearReconnectTimer() {
  if (reconnectTimer !== undefined) {
    window.clearTimeout(reconnectTimer)
    reconnectTimer = undefined
  }
}

function snapshotFromRealtimeEvent(event: RealtimeEvent): StatusSnapshot | null {
  const payload = objectRecord(event.payload)
  const scope = normalizeString(payload.scope || event.resource)
  const type = event.type || ''
  if (!scope || !type.startsWith('status.')) {
    return null
  }
  const resourceId = normalizeString(payload.resourceId || event.resourceId || event.instanceId)
  if (!resourceId) {
    return null
  }
  const status = Object.prototype.hasOwnProperty.call(payload, 'status') ? payload.status : event.status
  const lastError = Object.prototype.hasOwnProperty.call(payload, 'lastError') ? payload.lastError : undefined
  return normalizeSnapshot({
    scope,
    resourceId,
    serverId: normalizeString(payload.serverId || event.serverId),
    status,
    payload: objectRecord(payload.payload),
    lastError,
    version: Number(payload.version || event.version || 0),
    collectedAt: stringValue(payload.collectedAt || event.collectedAt),
    updatedAt: stringValue(payload.updatedAt)
  })
}

type RuntimeStatusSnapshot = Omit<StatusSnapshot, 'status' | 'lastError'> & {
  status?: unknown
  lastError?: unknown
}

function normalizeSnapshot(snapshot: RuntimeStatusSnapshot): StatusSnapshot | null {
  if (!isOptionalString(snapshot.status) || !isOptionalString(snapshot.lastError)) {
    return null
  }
  return {
    ...snapshot,
    scope: normalizeString(snapshot.scope),
    resourceId: normalizeString(snapshot.resourceId),
    serverId: normalizeString(snapshot.serverId),
    status: normalizeString(snapshot.status),
    payload: objectRecord(snapshot.payload),
    lastError: stringValue(snapshot.lastError)
  }
}

function mergeStatusSnapshots(
  current: Record<string, StatusSnapshot>,
  incoming: StatusSnapshot[]
): { snapshotsByKey: Record<string, StatusSnapshot>; changed: boolean } {
  let next = current
  let changed = false
  for (const snapshot of incoming) {
    const normalized = normalizeSnapshot(snapshot)
    if (!normalized) {
      continue
    }
    const key = snapshotKey(normalized.scope, normalized.resourceId)
    if (!key) {
      continue
    }
    const existing = next[key]
    if (existing && compareSnapshotFreshness(normalized, existing) <= 0) {
      continue
    }
    if (!changed) {
      next = { ...current }
      changed = true
    }
    next[key] = normalized
  }
  return { snapshotsByKey: next, changed }
}

function compareSnapshotFreshness(candidate: StatusSnapshot, current: StatusSnapshot) {
  const versionComparison = numericVersion(candidate.version) - numericVersion(current.version)
  if (versionComparison !== 0) {
    return Math.sign(versionComparison)
  }
  const candidateTimes = snapshotTimes(candidate)
  const currentTimes = snapshotTimes(current)
  const collectedComparison = compareOptionalNumbers(candidateTimes.collected, currentTimes.collected)
  if (collectedComparison !== 0) {
    return collectedComparison
  }
  return compareOptionalNumbers(candidateTimes.updated, currentTimes.updated)
}

function numericVersion(value: unknown) {
  const version = Number(value)
  return Number.isFinite(version) ? version : 0
}

function snapshotTimes(snapshot: StatusSnapshot) {
  return {
    collected: timestampValue(snapshot.collectedAt),
    updated: timestampValue(snapshot.updatedAt)
  }
}

function timestampValue(value: unknown) {
  const timestamp = Date.parse(stringValue(value))
  return Number.isFinite(timestamp) ? timestamp : null
}

function compareOptionalNumbers(candidate: number | null, current: number | null) {
  if (candidate === current) {
    return 0
  }
  if (candidate === null) {
    return -1
  }
  if (current === null) {
    return 1
  }
  return Math.sign(candidate - current)
}

function snapshotKey(scope?: string, resourceId?: string) {
  const normalizedScope = normalizeString(scope)
  const normalizedResource = normalizeString(resourceId)
  return normalizedScope && normalizedResource ? `${normalizedScope}:${normalizedResource}` : ''
}

function parseMetadata(raw?: string) {
  if (!raw) {
    return {} as Record<string, unknown>
  }
  try {
    const parsed = JSON.parse(raw)
    return objectRecord(parsed)
  } catch {
    return {} as Record<string, unknown>
  }
}

function objectRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function stringValue(value: unknown) {
  return String(value ?? '').trim()
}

function isOptionalString(value: unknown) {
  return value === undefined || typeof value === 'string'
}

function normalizeString(value: unknown) {
  return stringValue(value).toLowerCase() === '<nil>' ? '' : stringValue(value)
}
