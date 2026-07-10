import { getCurrentLocale } from '../../i18n'

export type AifarArtifactUpdateMode = 'single' | 'bundle'

export function aifarArtifactAccept(mode: AifarArtifactUpdateMode, service: string) {
  if (mode === 'bundle') {
    return '.zip'
  }
  return service === 'web-vue3' ? '.zip,.tar,.tgz,.tar.gz' : '.jar'
}

export function aifarArtifactHintKey(mode: AifarArtifactUpdateMode, service: string) {
  if (mode === 'bundle') {
    return 'apps.aifarUpdateBundleHint'
  }
  return service === 'web-vue3' ? 'apps.aifarUpdateFrontendHint' : 'apps.aifarUpdateJarHint'
}

export function buildAifarArtifactForm(mode: AifarArtifactUpdateMode, service: string, file: File) {
  const form = new FormData()
  form.append('language', getCurrentLocale())
  if (mode === 'bundle') {
    form.append('bundle', file, file.name)
  } else {
    form.append('service', service)
    form.append('artifact', file, file.name)
  }
  return form
}

export function isAifarArtifactTooLarge(file: File, limitBytes?: number) {
  const limit = Number(limitBytes || 0)
  return limit > 0 && file.size > limit
}

export function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return '0 B'
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = value
  let idx = 0
  while (size >= 1024 && idx < units.length - 1) {
    size /= 1024
    idx++
  }
  return `${size.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`
}
