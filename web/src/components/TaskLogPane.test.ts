// @vitest-environment happy-dom

import { shallowMount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import TaskLogPane from './TaskLogPane.vue'
import { apiGet } from '../api/client'

vi.mock('../api/client', () => ({
  apiDelete: vi.fn(),
  apiGet: vi.fn(),
  asArray: <T>(value: unknown): T[] => Array.isArray(value) ? value as T[] : []
}))

vi.mock('../tasks/actions', () => ({
  cancelTask: vi.fn(),
  isTaskCancellable: (status?: string) => status === 'pending' || status === 'running'
}))

const apiGetMock = vi.mocked(apiGet)

class FakeEventSource {
  static urls: string[] = []

  constructor(readonly url: string) {
    FakeEventSource.urls.push(url)
  }

  addEventListener() {
    // test double
  }

  close() {
    // test double
  }
}

describe('TaskLogPane task selection concurrency', () => {
  beforeEach(() => {
    vi.useRealTimers()
    localStorage.setItem('aifar-session-token', 'session-token')
    apiGetMock.mockReset()
    FakeEventSource.urls = []
    vi.stubGlobal('EventSource', FakeEventSource)
  })

  afterEach(() => {
    vi.useRealTimers()
  })

  it('ignores a stale detail response when a newer task selection has already won', async () => {
    const first = deferred<unknown>()
    apiGetMock.mockImplementation((path: string) => {
      if (path === '/tasks') {
        return Promise.resolve([
          task('task-a', '2026-08-13T01:00:00Z'),
          task('task-b', '2026-08-13T00:59:00Z')
        ])
      }
      if (path === '/servers') {
        return Promise.resolve([])
      }
      if (path === '/tasks/task-a') {
        return first.promise
      }
      if (path === '/tasks/task-b') {
        return Promise.resolve(detail('task-b'))
      }
      return Promise.resolve(null)
    })
    const wrapper = shallowMount(TaskLogPane, {
      global: {
        stubs: {
          ConfirmAction: { template: '<div><slot :confirm=\"() => {}\" /></div>' },
          TaskListPanel: { template: `<button class="select-task-b" @click="$emit('select', 'task-b')" />` },
          TaskRunPanel: { props: ['detail'], template: '<section class=\"task-run-panel\">{{ detail.task.id }}</section>' },
          'el-button': { template: '<button><slot /></button>' },
          'el-tooltip': { template: '<span><slot /></span>' },
          'el-empty': { template: '<div />' }
        }
      }
    })

    await vi.waitFor(() => expect(apiGetMock).toHaveBeenCalledWith('/tasks/task-a'))
    await wrapper.find('.select-task-b').trigger('click')
    await vi.waitFor(() => expect(apiGetMock).toHaveBeenCalledWith('/tasks/task-b'))
    await vi.waitFor(() => expect(wrapper.text()).toContain('task-b'))
    await vi.waitFor(() => expect(FakeEventSource.urls).toEqual(['/api/v2/tasks/task-b/events?token=session-token']))
    first.resolve(detail('task-a'))
    await flushMicrotasks()

    expect(wrapper.text()).toContain('task-b')
    expect(wrapper.text()).not.toContain('task-a')
    expect(FakeEventSource.urls).toEqual(['/api/v2/tasks/task-b/events?token=session-token'])
  })

  it('ignores an in-flight periodic detail refresh for the previously selected task', async () => {
    vi.useFakeTimers()
    let staleIntervalResolve: (value: unknown) => void = () => {}
    let taskADetailCalls = 0
    apiGetMock.mockImplementation((path: string) => {
      if (path === '/tasks') {
        return Promise.resolve([
          task('task-a', '2026-08-13T01:00:00Z'),
          task('task-b', '2026-08-13T00:59:00Z')
        ])
      }
      if (path === '/servers') {
        return Promise.resolve([])
      }
      if (path === '/tasks/task-a') {
        taskADetailCalls += 1
        if (taskADetailCalls === 1) {
          return Promise.resolve(detail('task-a'))
        }
        return new Promise((resolve) => {
          staleIntervalResolve = resolve
        })
      }
      if (path === '/tasks/task-b') {
        return Promise.resolve(detail('task-b'))
      }
      return Promise.resolve(null)
    })
    const wrapper = shallowMount(TaskLogPane, {
      global: {
        stubs: {
          ConfirmAction: { template: '<div><slot :confirm=\"() => {}\" /></div>' },
          TaskListPanel: { template: `<button class="select-task-b" @click="$emit('select', 'task-b')" />` },
          TaskRunPanel: { props: ['detail'], template: '<section class=\"task-run-panel\">{{ detail.task.id }}</section>' },
          'el-button': { template: '<button><slot /></button>' },
          'el-tooltip': { template: '<span><slot /></span>' },
          'el-empty': { template: '<div />' }
        }
      }
    })

    await vi.waitFor(() => expect(wrapper.text()).toContain('task-a'))
    await vi.advanceTimersByTimeAsync(2500)
    await vi.waitFor(() => expect(taskADetailCalls).toBe(2))
    await wrapper.find('.select-task-b').trigger('click')
    await vi.waitFor(() => expect(wrapper.text()).toContain('task-b'))
    staleIntervalResolve(detail('task-a'))
    await flushMicrotasks()

    expect(wrapper.text()).toContain('task-b')
    expect(wrapper.text()).not.toContain('task-a')
  })
})

function task(id: string, createdAt: string) {
  return { id, type: 'resources.scan', target: '', status: 'running', createdAt }
}

function detail(id: string) {
  return { task: task(id, '2026-08-13T01:00:00Z'), logs: [], targets: [], steps: [] }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  const promise = new Promise<T>((resolvePromise) => {
    resolve = resolvePromise
  })
  return { promise, resolve }
}

async function flushMicrotasks() {
  for (let i = 0; i < 5; i += 1) {
    await Promise.resolve()
  }
}
