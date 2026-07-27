import { ref, watch, type Ref } from 'vue'
import type { RuntimeLogWorkspaceTab } from './surface'

export function useRuntimeLogWorkspaceTab(selectedRuntimeInstanceId: Ref<string>, runtimeTargetQuery: () => string) {
  const runtimeLogWorkspaceTab = ref<RuntimeLogWorkspaceTab>('live')

  watch(
    () => [selectedRuntimeInstanceId.value, runtimeTargetQuery()],
    () => { runtimeLogWorkspaceTab.value = 'live' }
  )

  return runtimeLogWorkspaceTab
}
