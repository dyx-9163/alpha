import type { StatusSnapshot } from '../../stores/realtime'
import { isInstallLifecycleSelectable } from '../../status/semantics'
import type { AifarRuntimeInstance, AifarRuntimeResponse } from './types'

export type AifarRuntimeSnapshotAppInstance = {
  id: string
  app?: string
  serverId?: string
  version?: string
  status?: string
  metadata?: string
}

export function aifarRuntimeFromStatusSnapshots(
  snapshots: StatusSnapshot[],
  serverId: string,
  appInstances: AifarRuntimeSnapshotAppInstance[] = [],
  current: AifarRuntimeResponse = {}
): AifarRuntimeResponse {
  const installed = appInstances
    .filter((instance) => (
      normalizeString(instance.app).toLowerCase() === 'aifar' &&
      normalizeString(instance.serverId) === serverId &&
      normalizeString(instance.id) &&
      isInstallLifecycleSelectable(instance)
    ))
    .sort((left, right) => normalizeString(left.id).localeCompare(normalizeString(right.id)))
  const installedIds = new Set(installed.map((instance) => normalizeString(instance.id)))
  const rows = snapshots
    .filter((snapshot) => snapshot.scope === 'aifar.runtime' && snapshot.serverId === serverId && installedIds.has(snapshotInstanceId(snapshot)))
    .sort(compareRuntimeSnapshots)
  const rowsByInstanceId = new Map(rows.map((snapshot) => [snapshotInstanceId(snapshot), snapshot]))
  const instances = installed.map((instance) => runtimeInstanceFromAppInstance(instance, rowsByInstanceId.get(normalizeString(instance.id))))
  return {
    ...current,
    serverId,
    runtimeStatus: aggregateRuntimeStatus(rows.map((snapshot) => String(snapshot.status || snapshot.payload?.status || ''))),
    agent: {
      ...current.agent,
      status: aggregateAgentStatus(rows)
    },
    instances,
    warnings: rows.map((snapshot) => String(snapshot.lastError || '').trim()).filter(Boolean),
    deployments: filterByInstalledInstance(current.deployments, installedIds),
    services: filterByInstalledInstance(current.services, installedIds),
    pods: filterByInstalledInstance(current.pods, installedIds),
    ingress: filterByInstalledInstance(current.ingress, installedIds)
  }
}

function runtimeInstanceFromAppInstance(instance: AifarRuntimeSnapshotAppInstance, snapshot?: StatusSnapshot): AifarRuntimeInstance {
  const metadata = parseMetadata(instance.metadata)
  const payload = snapshot?.payload ?? {}
  return {
    id: normalizeString(instance.id),
    version: stringValue(payload.version || instance.version),
    status: stringValue(payload.appStatus || snapshot?.status || payload.status || instance.status || 'unknown'),
    orchestrationModel: stringValue(payload.orchestrationModel || metadata.orchestrationModel),
    installRoot: stringValue(payload.installRoot || metadata.installRoot),
    runtimeConfig: objectOrUndefined(payload.runtimeConfig) ?? objectOrUndefined(metadata.runtimeConfig)
  }
}

function aggregateRuntimeStatus(statuses: string[]) {
  const normalized = statuses.map((status) => status.trim().toLowerCase())
  if (normalized.some((status) => ['missing', 'failed', 'unhealthy', 'unsupported', 'no-endpoints'].includes(status))) {
    return 'failed'
  }
  if (normalized.some((status) => ['degraded', 'offline', 'stale', 'draining'].includes(status))) {
    return 'degraded'
  }
  if (normalized.some((status) => ['ready', 'running', 'available', 'active', 'success'].includes(status))) {
    return 'running'
  }
  if (normalized.some((status) => ['starting', 'rolling', 'pending', 'progressing'].includes(status))) {
    return 'progressing'
  }
  return 'unknown'
}

function aggregateAgentStatus(snapshots: StatusSnapshot[]) {
  if (!snapshots.length) {
    return 'unknown'
  }
  if (snapshots.some((snapshot) => snapshot.lastError)) {
    return 'missing'
  }
  return aggregateRuntimeStatus(snapshots.map((snapshot) => String(snapshot.status || '')))
}

function compareRuntimeSnapshots(left: StatusSnapshot, right: StatusSnapshot) {
  return String(left.resourceId).localeCompare(String(right.resourceId))
}

function stringValue(value: unknown) {
  return String(value ?? '').trim()
}

function normalizeString(value: unknown) {
  return stringValue(value)
}

function snapshotInstanceId(snapshot: StatusSnapshot) {
  return normalizeString(snapshot.payload?.instanceId || snapshot.resourceId)
}

function parseMetadata(raw?: string) {
  if (!raw) {
    return {}
  }
  try {
    const parsed = JSON.parse(raw)
    return objectRecord(parsed)
  } catch {
    return {}
  }
}

function objectRecord(value: unknown): Record<string, unknown> {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : {}
}

function objectOrUndefined(value: unknown) {
  const record = objectRecord(value)
  return Object.keys(record).length ? record : undefined
}

function filterByInstalledInstance<T extends { instanceId?: string }>(items: T[] | undefined, installedIds: Set<string>) {
  return (items ?? []).filter((item) => installedIds.has(normalizeString(item.instanceId)))
}
