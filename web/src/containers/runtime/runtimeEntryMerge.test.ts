import { computed, createSSRApp, defineComponent, h, provide, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import AifarRuntimePodsTab from './AifarRuntimePodsTab.vue'
import AifarRuntimeServicesTab from './AifarRuntimeServicesTab.vue'
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
        return () => h('div', [h(AifarRuntimeWorkspace), h(AifarRuntimePodsTab), h(AifarRuntimeServicesTab)])
      }
    })
    const app = createSSRApp(Root)
    for (const name of ['ElAlert', 'ElButton', 'ElDropdownItem', 'ElDropdownMenu', 'ElIcon', 'ElOption', 'ElSelect', 'ElTabs', 'ElTag', 'ElTooltip']) {
      app.component(name, passThroughComponent)
    }
    app.component('ElTable', passThroughComponent)
    app.component('ElTableColumn', tableColumnComponent)
    app.component('ElTabPane', activeTabPaneComponent)
    app.component('ElDropdown', dropdownComponent)

    const renderedText = await renderToString(app)
    expect(renderedText).toContain('运行参数')
    expect(renderedText).toContain('批量更新')
    expect(renderedText).toContain('全部重启')
    expect(renderedText).toContain('更多操作')
    expect(renderedText.match(/同步运行时/g) ?? []).toHaveLength(1)
    expect(renderedText).toContain('/aifar/apps/admin')
    expect(renderedText).not.toContain('启动/恢复 Pods')
    expect(renderedText).toContain('刷新')
    expect(renderedText).toContain('aria-label="刷新"')
    expect(renderedText).toContain('刷新指标')
    expect(renderedText).toContain('服务')
    expect(renderedText).toContain('Endpoint')
    expect(renderedText).toContain('代理')
    expect(renderedText).toContain('镜像')
    expect(renderedText).not.toContain('CPU')
    expect(renderedText).not.toContain('内存')
  })
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})

const tableColumnComponent = defineComponent({
  props: { label: String },
  setup(props, { slots }) {
    return () => h('span', [props.label, slots.default?.({ row: {} })])
  }
})

const activeTabPaneComponent = defineComponent({
  props: { name: String },
  setup(props, { slots }) {
    return () => props.name === 'pods' ? h('span', slots.default?.()) : h('span')
  }
})

const dropdownComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', [slots.default?.(), slots.dropdown?.()])
  }
})

function runtimeContext(): AifarRuntimeContext {
  const labels: Record<string, string> = {
    'common.refresh': '刷新',
    'containers.runtimeConfig': '运行参数',
    'containers.bundleUpdate': '批量更新',
    'containers.restartAllRuntime': '全部重启',
    'containers.moreRuntimeActions': '更多操作',
    'containers.installServices': '安装服务',
    'containers.reconcileRuntime': '同步运行时',
    'containers.cleanupStaleRuntime': '清理残留',
    'containers.runtimeSummary': '运行时实例摘要',
    'containers.refreshPodStats': '刷新指标',
    'containers.service': '服务',
    'containers.endpoint': 'Endpoint',
    'containers.proxy': '代理',
    'containers.image': '镜像',
    'containers.cpu': 'CPU',
    'containers.memory': '内存'
  }
  return {
    t: (key: string) => labels[key] ?? key,
    loading: computed(() => false),
    aifarRuntime: ref({ runtimeStatus: 'ready', agent: { status: 'running' } }),
    aifarRuntimeStatusKind: (status?: string) => status ?? 'unknown',
    aifarRuntimeStatusLabel: (status?: string) => status ?? 'unknown',
    selectedRuntimeInstanceId: ref('runtime-v2'),
    aifarRuntimeInstances: computed(() => [{ id: 'runtime-v2' }]),
    runtimeInstanceLabel: () => 'runtime-v2 / admin',
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
    runtimeSummaryItems: computed(() => [
      { label: '实例', value: 'runtime-v2 / admin' },
      { label: '安装目录', value: '/aifar/apps/admin' }
    ]),
    runtimeResourceTab: ref<RuntimeResourceTab>('pods'),
    runtimePodServiceFilter: ref(''),
    clearRuntimePodServiceFilter: () => {},
    installedRuntimeServiceNamesList: computed(() => []),
    ensureRuntimePodsLoaded: async () => {},
    runtimePodsLoadedForCurrentScope: computed(() => false),
    selectedRuntimePods: computed(() => []),
    selectedRuntimeServices: computed(() => []),
    runtimeEndpointText: () => '-',
    percentText: () => '-',
    openRuntimePodLogs: () => {}
  } as unknown as AifarRuntimeContext
}
