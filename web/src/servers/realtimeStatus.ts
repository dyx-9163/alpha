import type { StatusSnapshot } from '../stores/realtime'
import type { ServerRecord } from './types'

export function applyRealtimeStatusToServer(server: ServerRecord, snapshot?: StatusSnapshot): ServerRecord {
  if (!snapshot || snapshot.scope !== 'server' || snapshot.resourceId !== server.id) return server
  const observedAt = Date.parse(snapshot.collectedAt ?? '')
  const configuredAt = Date.parse(server.updatedAt ?? '')
  if (Number.isFinite(observedAt) && Number.isFinite(configuredAt) && observedAt < configuredAt) return server

  const status = String(snapshot.status ?? '').trim()
  if (!status) return server
  const lastError = String(snapshot.lastError ?? '').trim()
  if (server.status === status && String(server.lastError ?? '') === lastError) return server
  return { ...server, status, lastError }
}
