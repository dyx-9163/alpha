import { describe, expect, it } from 'vitest'
import { filterTasksByScope } from './taskScope'

const tasks = [
  { id: 'restore-current', type: 'apps.mysql.restore', target: 'app-current' },
  { id: 'restore-other', type: 'apps.mysql.restore', target: 'app-other' },
  { id: 'backup-current', type: 'apps.mysql.backup', target: 'app-current' }
]

describe('task scope', () => {
  it('preserves all tasks when no scope is requested', () => {
    expect(filterTasksByScope(tasks)).toEqual(tasks)
  })

  it('combines a type prefix with an exact target match', () => {
    expect(filterTasksByScope(tasks, 'apps.mysql.restore', 'app-current')).toEqual([tasks[0]])
  })
})
