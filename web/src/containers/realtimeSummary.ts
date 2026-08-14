import type { DockerSummaryResponse } from './dockerApi'
import type { StatusSnapshot } from '../stores/realtime'

export function mergeDockerSummarySnapshot(current: DockerSummaryResponse, event: unknown) {
  const envelope = realtimeSnapshotEnvelope(event)
  const payload = objectPayload(envelope?.payload)
  if (!payload) {
    return null
  }
  return {
    ...current,
    available: payload.available === true,
    error: typeof envelope?.lastError === 'string' ? envelope.lastError : current.error,
    summary: objectPayload(payload.summary) ?? current.summary,
    diskUsage: current.diskUsage
  }
}

export function dockerSummaryFromStatusSnapshot(snapshot: StatusSnapshot | undefined, current: DockerSummaryResponse = {}) {
  if (!snapshot || snapshot.scope !== 'docker.summary') {
    return null
  }
  const payload = objectPayload(snapshot.payload)
  if (!payload) {
    return null
  }
  return {
    ...current,
    available: payload.available === true,
    error: typeof snapshot.lastError === 'string' ? snapshot.lastError : current.error,
    summary: objectPayload(payload.summary) ?? current.summary,
    diskUsage: current.diskUsage
  }
}

function realtimeSnapshotEnvelope(event: unknown) {
  return objectPayload((event as { payload?: unknown } | null)?.payload)
}

function objectPayload(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : null
}
