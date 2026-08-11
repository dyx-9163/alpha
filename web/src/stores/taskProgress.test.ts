import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { apiGet } from '../api/client'
import { trackRuntimeDiagnosticTask } from '../containers/runtime/runtimeDiagnostics'
import { useTaskProgressStore } from './taskProgress'

vi.mock('../api/client', () => ({ apiGet: vi.fn() }))

const apiGetMock = vi.mocked(apiGet)
let storage = new Map<string, string>()

describe('task progress polling modes', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    storage = new Map()
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => storage.get(key) ?? null,
      setItem: (key: string, value: string) => storage.set(key, value),
      removeItem: (key: string) => storage.delete(key),
      clear: () => storage.clear()
    })
    vi.stubGlobal('window', globalThis)
    setActivePinia(createPinia())
    apiGetMock.mockReset()
    apiGetMock.mockImplementation(async (path: string) => ({
      task: { id: decodeURIComponent(path.split('/').pop() ?? ''), status: 'running', trackable: true },
      steps: []
    }))
  })

  it('tracks diagnostic tasks through SSE refreshes without scheduling polling', async () => {
    const store = useTaskProgressStore()

    trackRuntimeDiagnosticTask(store, 'task-1', 'diagnostic export')
    await vi.runAllTicks()

    expect(apiGetMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(apiGetMock).toHaveBeenCalledTimes(1)
  })

  it('keeps normal task tracking polling behavior', async () => {
    const store = useTaskProgressStore()

    store.track('task-1', 'normal task')
    await vi.runAllTicks()

    expect(apiGetMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1200)
    expect(apiGetMock).toHaveBeenCalledTimes(2)
    store.dismiss('task-1')
  })

  it('does not resume polling for persisted SSE-only tasks', async () => {
    const firstStore = useTaskProgressStore()
    trackRuntimeDiagnosticTask(firstStore, 'task-1', 'diagnostic export')
    await vi.runAllTicks()

    setActivePinia(createPinia())
    const resumedStore = useTaskProgressStore()
    apiGetMock.mockClear()
    resumedStore.resume()
    await vi.runAllTicks()

    expect(apiGetMock).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    expect(apiGetMock).toHaveBeenCalledTimes(1)
  })

  it('dismisses only the exact missing task and stops its polling after a 404 refresh', async () => {
    const missing = Object.assign(new Error('missing task'), { status: 404 })
    apiGetMock.mockImplementation(async (path: string) => {
      const id = decodeURIComponent(path.split('/').pop() ?? '')
      if (id === 'task-missing') throw missing
      return { task: { id, status: 'running', trackable: true }, steps: [] }
    })
    const store = useTaskProgressStore()

    store.track('task-running', 'running task')
    store.track('task-missing', 'missing task')
    await vi.runAllTicks()

    expect(store.items.map((item) => item.id)).toEqual(['task-running'])
    expect(JSON.parse(storage.get('aifar-tracked-tasks') ?? '[]').map((item: { id: string }) => item.id)).toEqual(['task-running'])
    apiGetMock.mockClear()
    await vi.advanceTimersByTimeAsync(1200)
    expect(apiGetMock).toHaveBeenCalledTimes(1)
    expect(apiGetMock).toHaveBeenCalledWith('/tasks/task-running')
    store.dismiss('task-running')
  })

  it.each([
    ['server error', Object.assign(new Error('temporary backend failure'), { status: 500 })],
    ['network error', new Error('temporary network failure')]
  ])('retains and keeps polling a tracked task after a %s', async (_name, failure) => {
    apiGetMock.mockRejectedValue(failure)
    const store = useTaskProgressStore()

    store.track('task-retry', 'retry task')
    await vi.runAllTicks()

    expect(store.items).toHaveLength(1)
    expect(store.items[0]).toMatchObject({ id: 'task-retry', error: failure.message, polling: true })
    expect(JSON.parse(storage.get('aifar-tracked-tasks') ?? '[]')).toHaveLength(1)
    apiGetMock.mockClear()
    await vi.advanceTimersByTimeAsync(1200)
    expect(apiGetMock).toHaveBeenCalledTimes(1)
    store.dismiss('task-retry')
  })

  it('refreshes only owner task IDs that are still known to the task store', async () => {
    const store = useTaskProgressStore()
    store.track('task-known', 'known task', { polling: false })
    await vi.runAllTicks()
    apiGetMock.mockClear()

    await store.refreshKnownTasks(['task-known', 'task-dismissed', 'task-known'])

    expect(apiGetMock).toHaveBeenCalledTimes(1)
    expect(apiGetMock).toHaveBeenCalledWith('/tasks/task-known')
    store.dismiss('task-known')
  })
})
