import { h, type VNode } from 'vue'
import type { RuntimeTranslate } from './format'
import { runtimeReleaseRollbackServices } from './releaseRules'
import type { AifarRelease } from './types'

export type ReleaseRollbackImpactItem = {
  service: string
  currentRevision: string
  targetRevision: string
}

export type RuntimeReleaseRollbackPrompt = (
  message: VNode,
  title: string,
  options: {
    inputType: 'textarea'
    inputPlaceholder: string
    confirmButtonText: string
    cancelButtonText: string
    type: 'warning'
    inputValidator: (value: string) => boolean | string
  }
) => Promise<{ value?: unknown }>

export function runtimeReleaseRollbackImpact(
  row: AifarRelease,
  unknownRevision: string
): ReleaseRollbackImpactItem[] {
  const targetRevision = String(row.releaseId || '').trim()
  const revisions = row.currentServiceRevisions && typeof row.currentServiceRevisions === 'object'
    ? row.currentServiceRevisions
    : {}
  return runtimeReleaseRollbackServices(row).map((service) => {
    const current = revisions[service]
    return {
      service,
      currentRevision: typeof current === 'string' && current.trim() ? current.trim() : unknownRevision,
      targetRevision
    }
  })
}

export function runtimeReleaseRollbackImpactMessage(
  row: AifarRelease,
  t: RuntimeTranslate
): VNode {
  const impacts = runtimeReleaseRollbackImpact(row, t('containers.releaseUnknownRevision'))
  const services = impacts.map((item) => item.service).join(', ')
  return h('div', { class: 'release-rollback-impact' }, [
    h('p', { class: 'release-rollback-impact-target' }, t('containers.releaseRollbackImpactTarget', {
      release: row.releaseId
    })),
    h('p', t('containers.releaseRollbackImpactServices', {
      count: impacts.length,
      services
    })),
    h('ul', { class: 'release-rollback-impact-revisions' }, impacts.map((item) => h('li', { key: item.service }, t(
      'containers.releaseRollbackImpactRevision',
      {
        service: item.service,
        current: item.currentRevision,
        target: item.targetRevision
      }
    )))),
    h('p', { class: 'release-rollback-impact-warning' }, t('containers.releaseRollbackDataWarning')),
    h('p', { class: 'release-rollback-impact-reason-hint' }, t('containers.rollbackReasonInputHint'))
  ])
}

export async function promptRuntimeReleaseRollback(
  row: AifarRelease,
  t: RuntimeTranslate,
  prompt: RuntimeReleaseRollbackPrompt
): Promise<{ targetReleaseId: string; services: string[]; reason: string }> {
  const result = await prompt(
    runtimeReleaseRollbackImpactMessage(row, t),
    t('containers.rollbackRelease'),
    {
      inputType: 'textarea',
      inputPlaceholder: t('containers.rollbackReasonPlaceholder'),
      confirmButtonText: t('containers.rollbackRelease'),
      cancelButtonText: t('common.cancel'),
      type: 'warning',
      inputValidator: (value) => Boolean(String(value || '').trim()) || t('containers.rollbackReasonRequired')
    }
  )
  return {
    targetReleaseId: row.releaseId,
    services: runtimeReleaseRollbackServices(row),
    reason: String(result.value || '').trim()
  }
}
