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
})
