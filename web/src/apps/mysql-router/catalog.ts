import type { FrontendAppDefinition } from '../registry/types'
import { mysqlRouterCopy } from './i18n'

export function mysqlRouterFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = mysqlRouterCopy(locale)
  return {
    name: 'mysql-router',
    title: copy.title,
    icon: 'MR',
    category: 'database',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: [{ name: 'router', label: 'Router', targetMode: 'multiple', minTargets: 1, default: true }]
  }
}

export const mysqlRouterFrontendApp = mysqlRouterFrontendAppForLocale()
