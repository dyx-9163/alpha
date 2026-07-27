import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

vi.mock('../../stores/taskProgress', () => ({
  useTaskProgressStore: () => ({ items: [], track: vi.fn() })
}))

vi.mock('vue-router', () => ({
  useRouter: () => ({ push: vi.fn() })
}))

vi.mock('element-plus', () => ({
  ElMessage: { error: vi.fn(), success: vi.fn() },
  ElMessageBox: { confirm: vi.fn() }
}))

import AifarRuntimeDiagnosticsPanel from './AifarRuntimeDiagnosticsPanel.vue'

describe('AIFAR Runtime diagnostic archives', () => {
  it('gives the archive table a full-height scrollable layout', async () => {
    const app = createSSRApp({
      render: () => h(AifarRuntimeDiagnosticsPanel, {
        instanceId: '',
        deployments: [],
        targetQuery: ''
      })
    })
    app.component('ElTable', tableStub)
    for (const name of ['ElAlert', 'ElButton', 'ElCheckbox', 'ElCheckboxGroup', 'ElDatePicker', 'ElDialog', 'ElForm', 'ElFormItem', 'ElRadioButton', 'ElRadioGroup', 'ElTableColumn', 'ElTag', 'ElTooltip']) {
      app.component(name, passThroughComponent)
    }

    const html = await renderToString(app)

    expect(html).toContain('data-table-height="100%"')
  })
})

const tableStub = defineComponent({
  inheritAttrs: false,
  setup(_, { attrs }) {
    return () => h('div', { 'data-table-height': attrs.height })
  }
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})
