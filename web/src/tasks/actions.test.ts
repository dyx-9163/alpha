import { beforeEach, describe, expect, it, vi } from 'vitest'

const apiPostMock = vi.hoisted(() => vi.fn())

vi.mock('../api/client', () => ({
  apiPost: apiPostMock
}))

import { cancelTask, isTaskCancellable } from './actions'

describe('task actions', () => {
  beforeEach(() => {
    apiPostMock.mockReset()
  })

  it.each([
    ['pending', true],
    [' running ', true],
    ['RUNNING', true],
    ['success', false],
    ['failed', false],
    ['cancelled', false],
    ['', false],
    [undefined, false]
  ])('reports whether status %s can be cancelled', (status, expected) => {
    expect(isTaskCancellable(status)).toBe(expected)
  })

  it('cancels only the supplied task id through the task cancel endpoint', async () => {
    apiPostMock.mockResolvedValueOnce({ taskId: 'task/one', cancelled: true })

    const result = await cancelTask('task/one')

    expect(apiPostMock).toHaveBeenCalledTimes(1)
    expect(apiPostMock).toHaveBeenCalledWith('/tasks/task%2Fone/cancel')
    expect(result).toEqual({ taskId: 'task/one', cancelled: true })
  })
})
