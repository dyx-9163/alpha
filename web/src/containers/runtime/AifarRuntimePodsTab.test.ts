import { computed, createSSRApp, defineComponent, h, provide, ref } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))
import AifarRuntimePodsTab from './AifarRuntimePodsTab.vue'
import { aifarRuntimeContextKey, type AifarRuntimeContext } from './context'

describe('AifarRuntimePodsTab progressive loading', () => {
  it('keeps the Pod table visible before base data is loaded', async () => {
    const Root = defineComponent({
      setup() {
        provide(aifarRuntimeContextKey, runtimeContext())
        return () => h(AifarRuntimePodsTab)
      }
    })
    const app = createSSRApp(Root)
    for (const name of ['ElButton', 'ElOption', 'ElSelect']) {
      app.component(name, passThroughComponent)
    }
    app.component('ElTable', passThroughComponent)
    app.component('ElTableColumn', tableColumnComponent)

    const rendered = await renderToString(app)

    expect(rendered).toContain('Name')
    expect(rendered).toContain('Service')
    expect(rendered).toContain('Status')
    expect(rendered).toContain('CPU')
    expect(rendered).toContain('Memory')
    expect(rendered).toContain('Refresh')
    expect(rendered).toContain('Refresh metrics')
    expect(rendered).not.toContain('Load Pods')
  })
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})

const tableColumnComponent = defineComponent({
  props: { label: String },
  setup(props) {
    return () => h('span', props.label)
  }
})

function runtimeContext(): AifarRuntimeContext {
  const labels: Record<string, string> = {
    'common.refresh': 'Refresh',
    'common.status': 'Status',
    'containers.name': 'Name',
    'containers.service': 'Service',
    'containers.revision': 'Revision',
    'containers.image': 'Image',
    'containers.cpu': 'CPU',
    'containers.memory': 'Memory',
    'containers.refreshPodStats': 'Refresh metrics',
    'containers.loadPods': 'Load Pods',
    'containers.logs': 'Logs',
    'common.operation': 'Operation'
  }
  return {
    t: (key: string) => labels[key] ?? key,
    loading: computed(() => true),
    runtimePodServiceFilter: ref(''),
    clearRuntimePodServiceFilter: () => {},
    installedRuntimeServiceNamesList: computed(() => []),
    runtimePodsLoadedForCurrentScope: computed(() => false),
    selectedRuntimePods: computed(() => []),
    aifarRuntimeStatusKind: (status?: string) => status ?? '',
    aifarRuntimeStatusLabel: (status?: string) => status ?? '',
    percentText: () => '-',
    openRuntimePodLogs: () => {},
    refreshRuntimePodBase: async () => {},
    refreshRuntimePodMetrics: () => {}
  } as unknown as AifarRuntimeContext
}
