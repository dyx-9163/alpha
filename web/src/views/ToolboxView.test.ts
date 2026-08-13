import { describe, expect, it } from 'vitest'

import source from './ToolboxView.vue?raw'

describe('ToolboxView source boundaries', () => {
  it('tracks resource rescans as background tasks instead of blocking the page', () => {
    expect(source).toContain('/resources/rescan')
    expect(source).toContain('taskProgress.track(result.taskId, t(')
    expect(source).not.toContain("await apiPost('/resources/rescan')\n  await load()")
  })
})
