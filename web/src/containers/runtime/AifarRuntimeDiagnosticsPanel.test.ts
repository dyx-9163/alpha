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
    app.component('ElDatePicker', datePickerStub)
    app.component('ElPagination', paginationStub)
    for (const name of ['ElAlert', 'ElButton', 'ElCheckbox', 'ElCheckboxGroup', 'ElDialog', 'ElForm', 'ElFormItem', 'ElRadioButton', 'ElRadioGroup', 'ElTableColumn', 'ElTag', 'ElTooltip']) {
      app.component(name, passThroughComponent)
    }

    const html = await renderToString(app)

    expect(html).toContain('data-table-height="100%"')
  })

  it('renders one server-local calendar date picker', async () => {
    const app = createSSRApp({
      render: () => h(AifarRuntimeDiagnosticsPanel, {
        instanceId: '',
        deployments: [],
        targetQuery: ''
      })
    })
    app.component('ElTable', tableStub)
    app.component('ElDatePicker', datePickerStub)
    app.component('ElPagination', paginationStub)
    for (const name of ['ElAlert', 'ElButton', 'ElCheckbox', 'ElCheckboxGroup', 'ElDialog', 'ElForm', 'ElFormItem', 'ElRadioButton', 'ElRadioGroup', 'ElTableColumn', 'ElTag', 'ElTooltip']) {
      app.component(name, passThroughComponent)
    }

    const html = await renderToString(app)

    expect(html).toContain('data-picker-type="date"')
    expect(html).toContain('data-value-format="YYYY-MM-DD"')
  })

  it('renders server-backed archive pagination with compact page sizes', async () => {
    const app = createSSRApp({
      render: () => h(AifarRuntimeDiagnosticsPanel, {
        instanceId: '',
        deployments: [],
        targetQuery: ''
      })
    })
    app.component('ElTable', tableStub)
    app.component('ElDatePicker', datePickerStub)
    app.component('ElPagination', paginationStub)
    for (const name of ['ElAlert', 'ElButton', 'ElCheckbox', 'ElCheckboxGroup', 'ElDialog', 'ElForm', 'ElFormItem', 'ElRadioButton', 'ElRadioGroup', 'ElTableColumn', 'ElTag', 'ElTooltip']) {
      app.component(name, passThroughComponent)
    }

    const html = await renderToString(app)

    expect(html).toContain('data-pagination-layout="sizes, prev, pager, next, jumper"')
    expect(html).toContain('data-pagination-page-size="5"')
    expect(html).toContain('data-pagination-page-sizes="5,10,20"')
  })
})

const tableStub = defineComponent({
  inheritAttrs: false,
  setup(_, { attrs }) {
    return () => h('div', { 'data-table-height': attrs.height })
  }
})

const datePickerStub = defineComponent({
  inheritAttrs: false,
  setup(_, { attrs }) {
    return () => h('span', {
      'data-picker-type': attrs.type,
      'data-value-format': attrs['value-format']
    })
  }
})

const paginationStub = defineComponent({
  inheritAttrs: false,
  props: {
    currentPage: Number,
    pageSize: Number,
    pageSizes: Array,
    layout: String
  },
  emits: ['update:currentPage', 'update:pageSize', 'size-change', 'current-change'],
  setup(props) {
    return () => h('span', {
      'data-pagination-layout': props.layout,
      'data-pagination-page-size': props.pageSize,
      'data-pagination-page-sizes': Array.isArray(props.pageSizes) ? props.pageSizes.join(',') : ''
    })
  }
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})
