import { defineStore } from 'pinia'
import { apiGet } from '../api/client'

const STORAGE_KEY = 'aifar-tracked-tasks'
const TERMINAL_STATUSES = new Set(['success', 'failed', 'cancelled'])
const FLOATING_TASK_LIMIT = 4
const STORAGE_TASK_LIMIT = 16

export type TrackedTask = {
  id: string
  label: string
  type: string
  trackable: boolean
  target: string
  status: string
  progress: number
  error: string
  updatedAt: number
}

type TaskSummary = {
  id: string
  type?: string
  category?: string
  trackable?: boolean
  target?: string
  status?: string
  error?: string
  updatedAt?: string
  createdAt?: string
}

type TaskStep = {
  status?: string
}

type TaskDetail = {
  task?: TaskSummary
  steps?: TaskStep[] | null
}

const timers = new Map<string, number>()

export const useTaskProgressStore = defineStore('taskProgress', {
  state: () => ({
    items: loadStoredTasks()
  }),
  getters: {
    visibleTasks(state): TrackedTask[] {
      return orderedFloatingTasks(state.items).slice(0, FLOATING_TASK_LIMIT)
    },
    activeTask(state): TrackedTask | undefined {
      return orderedFloatingTasks(state.items)[0]
    },
    runningCount(state): number {
      return state.items.filter((item) => isFloatingTask(item) && !isTerminalStatus(item.status)).length
    }
  },
  actions: {
    track(taskId: string, label = '') {
      const id = taskId.trim()
      if (!id) {
        return
      }
      const existing = this.items.find((item) => item.id === id)
      if (existing) {
        if (label.trim()) {
          existing.label = label.trim()
        }
        existing.trackable = true
        existing.updatedAt = Date.now()
      } else {
        this.items.unshift({
          id,
          label: label.trim(),
          type: '',
          trackable: true,
          target: '',
          status: 'pending',
          progress: 8,
          error: '',
          updatedAt: Date.now()
        })
      }
      this.prune()
      this.persist()
      void this.refreshTask(id)
      this.startPolling(id)
    },
    resume() {
      for (const item of this.items) {
        if (!isTerminalStatus(item.status)) {
          void this.refreshTask(item.id)
          this.startPolling(item.id)
        }
      }
    },
    async refreshTask(taskId: string) {
      const id = taskId.trim()
      if (!id) {
        return
      }
      try {
        const detail = await apiGet<TaskDetail>(`/tasks/${encodeURIComponent(id)}`)
        this.applyTaskDetail(id, detail)
      } catch (err) {
        const item = this.items.find((entry) => entry.id === id)
        if (item) {
          item.error = err instanceof Error ? err.message : String(err)
          item.updatedAt = Date.now()
          this.persist()
        }
      }
    },
    applyTaskDetail(taskId: string, detail: TaskDetail) {
      const task = detail.task
      if (!task?.id) {
        return
      }
      let item = this.items.find((entry) => entry.id === taskId)
      if (!item) {
        item = {
          id: task.id,
          label: '',
          type: '',
          trackable: true,
          target: '',
          status: 'pending',
          progress: 8,
          error: '',
          updatedAt: Date.now()
        }
        this.items.unshift(item)
      }
      item.id = task.id
      item.type = clean(task.type)
      item.trackable = task.trackable === true
      item.target = clean(task.target)
      item.status = clean(task.status) || 'pending'
      item.error = clean(task.error)
      item.progress = taskProgress(item.status, detail.steps ?? [])
      item.updatedAt = Date.now()
      this.prune()
      this.persist()
      if (isTerminalStatus(item.status)) {
        this.stopPolling(item.id)
      }
    },
    startPolling(taskId: string) {
      const id = taskId.trim()
      if (!id || timers.has(id)) {
        return
      }
      const tick = async () => {
        await this.refreshTask(id)
        const item = this.items.find((entry) => entry.id === id)
        if (!item || isTerminalStatus(item.status)) {
          this.stopPolling(id)
          return
        }
        timers.set(id, window.setTimeout(tick, 2500))
      }
      timers.set(id, window.setTimeout(tick, 1200))
    },
    stopPolling(taskId: string) {
      const id = taskId.trim()
      const timer = timers.get(id)
      if (timer !== undefined) {
        window.clearTimeout(timer)
        timers.delete(id)
      }
    },
    dismiss(taskId: string) {
      const id = taskId.trim()
      this.stopPolling(id)
      this.items = this.items.filter((item) => item.id !== id)
      this.persist()
    },
    persist() {
      try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(this.items.slice(0, STORAGE_TASK_LIMIT)))
      } catch {
        // Ignore storage quota and private-mode errors; task polling still works in memory.
      }
    },
    prune() {
      const sorted = [...this.items].sort((a, b) => b.updatedAt - a.updatedAt)
      const keep = new Set<string>()
      for (const item of sorted.filter(isFloatingTask).slice(0, STORAGE_TASK_LIMIT)) {
        keep.add(item.id)
      }
      for (const item of sorted) {
        if (keep.size >= STORAGE_TASK_LIMIT) {
          break
        }
        keep.add(item.id)
      }
      this.items = sorted.filter((item) => keep.has(item.id))
    }
  }
})

function taskProgress(status: string, steps: TaskStep[]) {
  if (isTerminalStatus(status)) {
    return 100
  }
  if (!steps.length) {
    return status === 'running' ? 35 : 8
  }
  let score = 0
  for (const step of steps) {
    const stepStatus = clean(step.status)
    if (isTerminalStatus(stepStatus)) {
      score += 1
    } else if (stepStatus === 'running') {
      score += 0.55
    }
  }
  return Math.max(8, Math.min(95, Math.round((score / steps.length) * 100)))
}

function isTerminalStatus(status?: string) {
  return TERMINAL_STATUSES.has(clean(status))
}

function orderedFloatingTasks(items: TrackedTask[]) {
  return [...items]
    .filter(isFloatingTask)
    .sort((a, b) => {
      const aFinished = isTerminalStatus(a.status) ? 1 : 0
      const bFinished = isTerminalStatus(b.status) ? 1 : 0
      return aFinished - bFinished || b.updatedAt - a.updatedAt
    })
}

function isFloatingTask(item: Pick<TrackedTask, 'trackable'>) {
  return item.trackable === true
}

function clean(value?: string) {
  return String(value ?? '').trim()
}

function loadStoredTasks(): TrackedTask[] {
  try {
    const raw = JSON.parse(localStorage.getItem(STORAGE_KEY) ?? '[]')
    if (!Array.isArray(raw)) {
      return []
    }
    return raw
      .map((item) => ({
        id: clean(item?.id),
        label: clean(item?.label),
        type: clean(item?.type),
        trackable: item?.trackable === true,
        target: clean(item?.target),
        status: clean(item?.status) || 'pending',
        progress: Number.isFinite(Number(item?.progress)) ? Number(item.progress) : 8,
        error: clean(item?.error),
        updatedAt: Number.isFinite(Number(item?.updatedAt)) ? Number(item.updatedAt) : Date.now()
      }))
      .filter((item) => item.id)
      .slice(0, STORAGE_TASK_LIMIT)
  } catch {
    return []
  }
}
