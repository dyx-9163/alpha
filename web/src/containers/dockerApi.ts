import { apiGet, apiPost } from '../api/client'

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

export async function fetchContainerPageBootstrap<TAppInstance>() {
  const [servers, appInstances, settings] = await Promise.all([
    apiGet<any[] | null>('/servers').catch(() => []),
    apiGet<TAppInstance[] | null>('/apps/instances').catch(() => []),
    apiGet<ContainerPageSettings>('/settings').catch(() => ({}))
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
