import type { AifarRuntimeDeployment, AifarRuntimeService } from './types'

export function normalizeBatchOfflineDeployments(
  rows: AifarRuntimeDeployment[],
  toService: (row: AifarRuntimeDeployment) => AifarRuntimeService,
  disabledReason: (row: AifarRuntimeService) => string
) {
  const selected = new Map<string, AifarRuntimeService>()
  for (const deployment of rows) {
    const service = toService(deployment)
    const name = service.serviceName.trim()
    if (!name || disabledReason(service) || selected.has(name)) continue
    selected.set(name, service)
  }
  return [...selected.values()]
}
