import { installLifecycleDisplayStatus, type StatusRecord } from '../status/semantics'

export interface InstalledStatusRecord {
  status?: unknown
  metadata?: unknown
}

export function installedGroupStatus(records: InstalledStatusRecord[]) {
  return records.some((record) => installLifecycleDisplayStatus(record as StatusRecord) === 'failed') ? 'failed' : 'installed'
}
