import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({
    t: (key: string) => ({
      'common.running': '运行中'
    })[key] ?? key
  })
}))

import AifarRuntimeSummary from './AifarRuntimeSummary.vue'

describe('AifarRuntimeSummary', () => {
  it('renders summary labels, complete values, and textual statuses', async () => {
    const app = createSSRApp({
      render: () => h(AifarRuntimeSummary, {
        label: '运行时实例摘要',
        items: [
          { label: '实例', value: 'runtime-v2 / admin' },
          { label: '安装目录', value: '/aifar/apps/admin' },
          { label: '运行参数版本', value: 'v2.4.1 / v2.4.0', status: 'running' }
        ]
      })
    })
    app.component('ElTag', passThroughComponent)

    const html = await renderToString(app)

    expect(html).toContain('aria-label="运行时实例摘要"')
    expect(html).toContain('实例')
    expect(html).toContain('安装目录')
    expect(html).toContain('运行参数版本')
    expect(html).toContain('runtime-v2 / admin')
    expect(html).toContain('title="/aifar/apps/admin"')
    expect(html).toContain('v2.4.1 / v2.4.0')
    expect(html).toContain('运行中')
  })
})

const passThroughComponent = defineComponent({
  setup(_, { slots }) {
    return () => h('span', slots.default?.())
  }
})
