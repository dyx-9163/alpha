import type { FrontendAppDefinition } from '../registry/types'
import { aifarCopy, aifarTopologies } from './i18n'

export function aifarFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = aifarCopy(locale)
  return {
    name: 'aifar',
    title: copy.title,
    icon: 'AF',
    category: 'devops',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: aifarTopologies(locale)
  }
}

export const aifarFrontendApp = aifarFrontendAppForLocale()
