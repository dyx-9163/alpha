import { createSSRApp, defineComponent, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'
import AifarRuntimeOverflowAction from './AifarRuntimeOverflowAction.vue'
import {
  dispatchRuntimeOverflowCommand,
  runtimeOverflowReasonId
} from './runtimeToolbar'

describe('AIFAR Runtime overflow actions', () => {
  it('dispatches only the selected command', () => {
    const actions = {
      install: vi.fn(),
      reconcile: vi.fn(),
      cleanup: vi.fn()
    }

    dispatchRuntimeOverflowCommand('reconcile', actions)

    expect(actions.install).not.toHaveBeenCalled()
    expect(actions.reconcile).toHaveBeenCalledOnce()
    expect(actions.cleanup).not.toHaveBeenCalled()
  })

  it('keeps a disabled reason focusable and programmatically described', async () => {
    const app = createSSRApp({
      render: () => h(AifarRuntimeOverflowAction, {
        command: 'reconcile',
        label: '同步运行时',
        disabledReason: 'Agent 不可用'
      })
    })
    app.component('ElDropdownItem', passThroughComponent)
    app.component('ElTooltip', tooltipComponent)

    const html = await renderToString(app)

    expect(html).toContain('disabled')
    expect(html).toContain('data-trigger="hover focus"')
    expect(html).toContain('tabindex="0"')
    expect(html).toContain(`aria-describedby="${runtimeOverflowReasonId('reconcile')}"`)
    expect(html).toContain(`id="${runtimeOverflowReasonId('reconcile')}"`)
    expect(html).toContain('Agent 不可用')
  })
})

const passThroughComponent = defineComponent({
  setup(_, { attrs, slots }) {
    return () => h('span', attrs, slots.default?.())
  }
})

const tooltipComponent = defineComponent({
  props: { trigger: [String, Array] },
  setup(props, { attrs, slots }) {
    const trigger = Array.isArray(props.trigger) ? props.trigger.join(' ') : props.trigger ?? ''
    return () => h('span', { ...attrs, 'data-trigger': trigger }, slots.default?.())
  }
})
