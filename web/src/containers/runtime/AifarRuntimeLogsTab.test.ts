import { nextTick, ref } from 'vue'
import { describe, expect, it } from 'vitest'
import { useRuntimeLogWorkspaceTab } from './runtimeLogWorkspace'

describe('AIFAR Runtime focused logs workspace', () => {
  it('defaults to live logs and resets after the Runtime instance changes', async () => {
    const instanceId = ref('runtime-a')
    const targetQuery = ref('server=server-a')
    const runtimeLogWorkspaceTab = useRuntimeLogWorkspaceTab(instanceId, () => targetQuery.value)

    expect(runtimeLogWorkspaceTab.value).toBe('live')

    runtimeLogWorkspaceTab.value = 'archives'
    instanceId.value = 'runtime-b'
    await nextTick()

    expect(runtimeLogWorkspaceTab.value).toBe('live')
  })

  it('resets to live logs after the server target changes', async () => {
    const instanceId = ref('runtime-a')
    const targetQuery = ref('server=server-a')
    const runtimeLogWorkspaceTab = useRuntimeLogWorkspaceTab(instanceId, () => targetQuery.value)

    runtimeLogWorkspaceTab.value = 'archives'
    targetQuery.value = 'server=server-b'
    await nextTick()

    expect(runtimeLogWorkspaceTab.value).toBe('live')
  })
})
