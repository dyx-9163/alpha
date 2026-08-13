import { describe, expect, it } from 'vitest'

import source from './AppsView.vue?raw'

describe('AppsView source boundaries', () => {
  it('does not expose deployment records as an app-store tab or section', () => {
    expect(source).not.toContain("t('apps.tasks')")
    expect(source).not.toContain("name=\"tasks\"")
    expect(source).not.toContain("from '../components/AppInstanceTable.vue'")
    expect(source).not.toContain('apps.deployedServices')
  })

  it('does not overlay monitoring snapshots onto installed lifecycle records', () => {
    expect(source).not.toContain('applyRealtimeStatusToAppInstance')
    expect(source).not.toContain('appInstanceSnapshot')
  })

  it('tracks resource rescans as background tasks instead of blocking for a refreshed catalog', () => {
    expect(source).toContain('/resources/rescan')
    expect(source).toContain('taskProgress.track(result.taskId, t(')
    expect(source).not.toContain("await apiPost('/resources/rescan')\n  backendCatalog.value")
  })
})
