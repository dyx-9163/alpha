import type { FrontendAppDefinition } from '../registry/types'
import { redisCopy, redisTopologies } from './i18n'

export function redisFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = redisCopy(locale)
  return {
    name: 'redis',
    title: copy.title,
    icon: 'RE',
    category: 'database',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: redisTopologies(locale)
  }
}

export const redisFrontendApp = redisFrontendAppForLocale()
