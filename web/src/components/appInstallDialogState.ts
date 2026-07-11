import type { AppInstallField } from '../apps/registry/contract'
import type { AppTargetMode } from '../apps/registry/types'

const RESET_KEY_SEPARATOR = '\u001f'

export interface AppInstallResetKeyInput {
  visible: boolean
  appName?: string
  versions?: string[]
  fallbackVersion?: string
  targetMode: AppTargetMode
  fields: Pick<AppInstallField, 'name'>[]
}

export function appInstallResetKey(input: AppInstallResetKeyInput) {
  return [
    input.visible ? 'open' : 'closed',
    input.appName ?? '',
    (input.versions ?? []).join('|'),
    input.fallbackVersion ?? '',
    input.targetMode,
    input.fields.map((field) => field.name).join('|')
  ].join(RESET_KEY_SEPARATOR)
}

export function latestInstallVersion(versions: string[], fallbackVersion?: string) {
  return versions.at(-1) ?? fallbackVersion ?? ''
}
