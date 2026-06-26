import type { BackendCatalogItem, FrontendAppDefinition } from './types'

export function canPairApp(frontend: FrontendAppDefinition, backend?: BackendCatalogItem) {
  return Boolean(frontend.frontendReady && backend?.backendReady)
}
