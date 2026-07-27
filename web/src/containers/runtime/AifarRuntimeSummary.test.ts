import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import AifarRuntimeSummary from './AifarRuntimeSummary.vue'

describe('AifarRuntimeSummary', () => {
  it('renders every summary label and full value for tooltip access', async () => {
    const app = createSSRApp({
      render: () => h(AifarRuntimeSummary, {
        label: '运行时实例摘要',
        items: [
          { label: '实例', value: 'runtime-v2 / admin' },
          { label: '安装目录', value: '/aifar/apps/admin' },
          { label: 'Agent', value: '运行中', status: 'running' }
        ]
      })
    })
    app.component('ElTag', passThroughComponent)

    const html = await renderToString(app)

    expect(html).toContain('runtime-v2 / admin')
    expect(html).toContain('title="/aifar/apps/admin"')
    expect(html).toContain('运行中')
  })
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})
