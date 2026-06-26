import { apiDelete, apiGet, apiPost, apiPut, asArray } from '../api/client'
import type { ProbeTaskResponse, ServerFormModel, ServerRecord } from './types'

export async function listServers() {
  return asArray<ServerRecord>(await apiGet<ServerRecord[] | null>('/servers'))
}

export function saveServer(payload: ServerFormModel) {
  return payload.id
    ? apiPut<ServerRecord>(`/servers/${payload.id}`, payload)
    : apiPost<ServerRecord>('/servers', payload)
}

export function deleteServer(id: string) {
  return apiDelete<{ deleted: string }>(`/servers/${id}`)
}

export function probeServer(id: string) {
  return apiPost<ProbeTaskResponse>(`/servers/${id}/probe`)
}

export async function getServerDefaults() {
  const settings = await apiGet<{ defaultDeployDir?: string }>('/settings')
  return {
    defaultDeployDir: settings.defaultDeployDir || '/aifar/apps'
  }
}

export async function waitTaskDone(taskId: string, intervalMs = 800, timeoutMs = 20000) {
  const startedAt = Date.now()
  while (Date.now() - startedAt < timeoutMs) {
    const result = await apiGet<{ task?: { status?: string } }>(`/tasks/${taskId}`)
    const status = result.task?.status
    if (status && !['pending', 'running'].includes(status)) {
      return status
    }
    await new Promise((resolve) => window.setTimeout(resolve, intervalMs))
  }
  return 'timeout'
}
