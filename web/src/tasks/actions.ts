import { apiPost } from '../api/client'

export type CancelTaskResponse = {
  taskId: string
  cancelled: boolean
}

export function isTaskCancellable(status?: string) {
  return ['pending', 'running'].includes(String(status ?? '').trim().toLowerCase())
}

export function cancelTask(taskId: string) {
  return apiPost<CancelTaskResponse>(`/tasks/${encodeURIComponent(taskId)}/cancel`)
}
