import type { RuntimeTranslate } from './format'
import type { AifarRelease, RollbackUnavailableReason } from './types'

export type { RollbackUnavailableReason } from './types'

type RuntimeReleaseRollbackOptions = {
  canManage: boolean
  deniedText: string
  t: RuntimeTranslate
}

export function runtimeReleaseRollbackDisabledReason(row: AifarRelease, options: RuntimeReleaseRollbackOptions): string {
  if (!options.canManage) return options.deniedText
  if (!row.releaseId) return options.t('containers.releaseIdRequired')

  switch (row.rollbackUnavailableReason) {
    case 'ROLLBACK_RECORD':
      return options.t('containers.releaseRollbackAuditRecord')
    case 'ALREADY_ACTIVE':
      return options.t('containers.releaseRollbackAlreadyActive')
    case 'ARTIFACT_UNAVAILABLE':
      return options.t('containers.releaseRollbackUnavailable')
  }

  if (!row.rollbackAvailable || !runtimeReleaseRollbackServices(row).length) {
    return options.t('containers.releaseRollbackUnavailable')
  }
  return ''
}

export function runtimeReleaseRollbackServices(row: AifarRelease): string[] {
  if (!Array.isArray(row.rollbackServices)) return []
  return [...new Set(row.rollbackServices.filter((service) => typeof service === 'string' && service.trim() !== ''))]
}
