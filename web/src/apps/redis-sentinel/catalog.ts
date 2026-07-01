import type { FrontendAppDefinition } from '../registry/types'
import { redisSentinelCopy } from './i18n'

export function redisSentinelFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = redisSentinelCopy(locale)
  return {
    name: 'redis-sentinel',
    title: copy.title,
    icon: 'RS',
    category: 'database',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: [{ name: 'sentinel', label: 'Sentinel', targetMode: 'multiple', minTargets: 3, default: true }]
  }
}

export const redisSentinelFrontendApp = redisSentinelFrontendAppForLocale()
