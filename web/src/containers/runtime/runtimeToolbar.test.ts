import { createSSRApp, h } from 'vue'
import { renderToString } from 'vue/server-renderer'
import { describe, expect, it, vi } from 'vitest'
import ElementPlus, {
  ElButton,
  ElDropdown,
  ElDropdownMenu,
  ID_INJECTION_KEY,
  ZINDEX_INJECTION_KEY
} from 'element-plus'
import AifarRuntimeOverflowAction from './AifarRuntimeOverflowAction.vue'
import * as runtimeToolbar from './runtimeToolbar'

const {
  dispatchRuntimeOverflowCommand,
  runtimeOverflowReasonId
} = runtimeToolbar

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

  it('blocks a disabled command at the dispatch boundary', () => {
    const actions = {
      install: vi.fn(),
      reconcile: vi.fn(),
      cleanup: vi.fn()
    }

    dispatchRuntimeOverflowCommand('reconcile', actions, {
      reconcile: 'Agent unavailable'
    })

    expect(actions.install).not.toHaveBeenCalled()
    expect(actions.reconcile).not.toHaveBeenCalled()
    expect(actions.cleanup).not.toHaveBeenCalled()
  })

  it('keeps a disabled action in the real Element Plus focus collection and exposes its reason', async () => {
    const reasonId = runtimeOverflowReasonId('reconcile')
    const app = createSSRApp({
      render: () => h(ElDropdown, { teleported: false, persistent: true }, {
        default: () => h(ElButton, null, () => '更多操作'),
        dropdown: () => h(ElDropdownMenu, null, () => h(AifarRuntimeOverflowAction, {
          command: 'reconcile',
          label: '同步运行时',
          disabledReason: 'Agent 不可用'
        }))
      })
    })
    app.use(ElementPlus)
    app.provide(ID_INJECTION_KEY, { prefix: 1024, current: 0 })
    app.provide(ZINDEX_INJECTION_KEY, { current: 0 })

    const html = await renderToString(app)

    expect(html).toMatch(new RegExp(`<li[^>]*aria-describedby="${reasonId}"`))
    expect(html).not.toMatch(/<li[^>]*class="[^"]*\bis-disabled\b/)
    expect(html).not.toContain('runtime-overflow-action-trigger" tabindex="0"')
    expect(html).toContain(`id="${reasonId}"`)
    expect(html).toContain('Agent 不可用')
  })

  it('applies aria-disabled semantics to the Element Plus menuitem', () => {
    const syncMenuItemState = (runtimeToolbar as Record<string, unknown>).syncRuntimeOverflowMenuItemState
    expect(syncMenuItemState).toBeTypeOf('function')
    if (typeof syncMenuItemState !== 'function') return

    const menuItem = {
      setAttribute: vi.fn(),
      removeAttribute: vi.fn()
    }
    const trigger = {
      closest: vi.fn(() => menuItem)
    }

    syncMenuItemState(trigger, true, runtimeOverflowReasonId('reconcile'))

    expect(trigger.closest).toHaveBeenCalledWith('[role="menuitem"]')
    expect(menuItem.setAttribute).toHaveBeenCalledWith('aria-disabled', 'true')
    expect(menuItem.setAttribute).toHaveBeenCalledWith('aria-describedby', runtimeOverflowReasonId('reconcile'))
  })
})
