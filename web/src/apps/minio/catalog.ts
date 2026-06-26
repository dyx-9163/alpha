import type { FrontendAppDefinition } from '../registry/types'
import { minioCopy, minioTopologies } from './i18n'

export function minioFrontendAppForLocale(locale?: string): FrontendAppDefinition {
  const copy = minioCopy(locale)
  return {
    name: 'minio',
    title: copy.title,
    icon: 'S3',
    category: 'storage',
    categoryLabel: copy.categoryLabel,
    sourceLabel: copy.sourceLabel,
    fallbackVersion: '2025-10-15T17-29-55Z',
    description: copy.description,
    frontendReady: true,
    topologies: minioTopologies(locale)
  }
}

export const minioFrontendApp = minioFrontendAppForLocale()
