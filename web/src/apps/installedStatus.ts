export interface InstalledStatusRecord {
  status?: unknown
  metadata?: unknown
}

export function installedGroupStatus(records: InstalledStatusRecord[]) {
  return records.some((record) => {
    const status = String(record.status || '').trim().toLowerCase()
    const metadata = metadataRecord(record.metadata)
    return status === 'failed' || status === 'install_failed' || truthyValue(metadata.installFailed)
  }) ? 'failed' : 'installed'
}

function metadataRecord(value: unknown): Record<string, unknown> {
  if (value && typeof value === 'object' && !Array.isArray(value)) {
    return value as Record<string, unknown>
  }
  if (typeof value !== 'string' || !value.trim()) {
    return {}
  }
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {}
  } catch {
    return {}
  }
}

function truthyValue(value: unknown) {
  if (typeof value === 'boolean') return value
  return ['true', '1', 'yes', 'y'].includes(String(value ?? '').trim().toLowerCase())
}
