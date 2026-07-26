import { computed, createSSRApp, defineComponent, h, provide, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import AifarRuntimePodsTab from './AifarRuntimePodsTab.vue'
import AifarRuntimeWorkspace from './AifarRuntimeWorkspace.vue'
import {
  aifarRuntimeContextKey,
  type AifarRuntimeContext,
  type RuntimeResourceTab
} from './context'

describe('AIFAR Runtime reconcile entries', () => {
  it('renders one workspace reconcile entry and no duplicate Pods recovery entry', async () => {
    const Root = defineComponent({
      setup() {
        provide(aifarRuntimeContextKey, runtimeContext())
        return () => h('div', [h(AifarRuntimeWorkspace), h(AifarRuntimePodsTab)])
      }
    })
    const app = createSSRApp(Root)
    for (const name of ['ElAlert', 'ElButton', 'ElOption', 'ElSelect', 'ElTable', 'ElTableColumn', 'ElTabPane', 'ElTabs', 'ElTag', 'ElTooltip']) {
      app.component(name, passThroughComponent)
    }

    const renderedText = await renderToString(app)
    expect(renderedText.match(/同步运行时/g) ?? []).toHaveLength(1)
    expect(renderedText).not.toContain('启动/恢复 Pods')
    expect(renderedText).toContain('刷新')
    expect(renderedText).toContain('刷新指标')
  })
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})

function runtimeContext(): AifarRuntimeContext {
  const labels: Record<string, string> = {
    'common.refresh': '刷新',
    'containers.reconcileRuntime': '同步运行时',
    'containers.refreshPodStats': '刷新指标'
  }
  return {
    t: (key: string) => labels[key] ?? key,
    loading: computed(() => false),
    aifarRuntime: ref({ runtimeStatus: 'ready', agent: { status: 'running' } }),
    aifarRuntimeStatusKind: (status?: string) => status ?? 'unknown',
    aifarRuntimeStatusLabel: (status?: string) => status ?? 'unknown',
    selectedRuntimeInstanceId: ref(''),
    aifarRuntimeInstances: computed(() => []),
    runtimeInstanceLabel: () => '',
    aifarRuntimeActionDisabledReason: computed(() => ''),
    openRuntimeConfigDialog: () => {},
    serviceInstallDisabledReason: computed(() => ''),
    openServiceInstallDialog: () => {},
    openAifarRuntimeBundleUpdate: () => {},
    reconcileAifarRuntime: () => {},
    runtimeRestartDisabledReason: computed(() => ''),
    runtimeRestartSubmitting: ref(false),
    restartAllAifarRuntime: () => {},
    runtimeCleanupDisabledReason: computed(() => ''),
    cleanupAifarRuntimeStale: () => {},
    loadAifarRuntime: async () => {},
    aifarRuntimeWarnings: computed(() => []),
    runtimeSummaryItems: computed(() => []),
    runtimeResourceTab: ref<RuntimeResourceTab>('pods'),
    runtimePodServiceFilter: ref(''),
    clearRuntimePodServiceFilter: () => {},
    installedRuntimeServiceNamesList: computed(() => []),
    ensureRuntimePodsLoaded: async () => {},
    runtimePodsLoadedForCurrentScope: computed(() => false),
    selectedRuntimePods: computed(() => []),
    percentText: () => '-',
    openRuntimePodLogs: () => {}
  } as unknown as AifarRuntimeContext
}
