import type { FrontendAppDefinition } from '../registry/types'
import { mysqlCopy, mysqlTopologies } from './i18n'

export function mysqlFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = mysqlCopy(locale)
  return {
    name: 'mysql',
    title: copy.title,
    icon: 'MY',
    category: 'database',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: mysqlTopologies(locale)
  }
}

export const mysqlFrontendApp = mysqlFrontendAppForLocale()
