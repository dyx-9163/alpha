import type { FrontendAppDefinition } from '../registry/types'
import { nacosCopy, nacosTopologies } from './i18n'

export function nacosFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = nacosCopy(locale)
  return {
    name: 'nacos',
    title: copy.title,
    icon: 'NA',
    category: 'devops',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: nacosTopologies(locale)
  }
}

export const nacosFrontendApp = nacosFrontendAppForLocale()
