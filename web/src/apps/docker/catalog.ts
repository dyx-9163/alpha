import type { FrontendAppDefinition } from '../registry/types'
import { dockerCopy, dockerTopologies } from './i18n'

export function dockerFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = dockerCopy(locale)
  return {
    name: 'docker',
    title: copy.title,
    icon: 'D',
    category: 'devops',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    description: copy.description,
    frontendReady: true,
    topologies: dockerTopologies(locale)
  }
}

export const dockerFrontendApp = dockerFrontendAppForLocale()
