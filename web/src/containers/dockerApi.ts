import { apiGet, apiPost } from '../api/client'
import { keepPreviousArrayOnLoadFailure, keepPreviousObjectOnLoadFailure } from '../api/resilientLoad'

export type DockerSummaryResponse = {
  available?: boolean
  error?: string
  summary?: Record<string, any>
  diskUsage?: Array<Record<string, string>>
}

export type DockerImageRemoveMode = 'single' | 'batch'

export type ContainerPageSettings = {
  maxRequestBodyBytes?: number
}

export async function fetchContainerPageBootstrap<TAppInstance>(previous?: {
  servers?: any[]
  appInstances?: TAppInstance[]
  settings?: ContainerPageSettings
}) {
  const [servers, appInstances, settings] = await Promise.all([
    keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/servers'), previous?.servers ?? []),
    keepPreviousArrayOnLoadFailure(apiGet<TAppInstance[] | null>('/apps/instances'), previous?.appInstances ?? []),
    keepPreviousObjectOnLoadFailure(apiGet<ContainerPageSettings>('/settings'), previous?.settings ?? {})
  ])
  return { servers, appInstances, settings }
}

export function fetchDockerSummary(query: string, includeDisk: boolean) {
  return apiGet<DockerSummaryResponse>(`/containers/summary?${query}${includeDisk ? '&includeDisk=1' : ''}`)
}

export function fetchDockerCollection(kind: string, query: string) {
  return apiGet<unknown[]>(`/containers?kind=${kind}&${query}`)
}

export function removeDockerImages(query: string, ids: string[], mode: DockerImageRemoveMode) {
  return apiPost<{ taskId?: string }>(
    `/containers/images/remove?${query}`,
    mode === 'batch' ? { ids } : { id: ids[0] }
  )
}
