// @vitest-environment happy-dom

import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, inject, provide, ref, type PropType } from 'vue'
import { describe, expect, it, vi } from 'vitest'

vi.mock('../../i18n', () => ({
  useI18n: () => ({ t: (key: string) => key })
}))

import AifarRuntimeReleasesTab from './AifarRuntimeReleasesTab.vue'
import { aifarRuntimeContextKey, type AifarRuntimeContext } from './context'
import { runtimeReleaseDeleteDisabledReason } from './releaseRules'
import type { AifarRelease } from './types'

const tableRowsKey = Symbol('release-table-rows')

describe('AifarRuntimeReleasesTab', () => {
  it('marks current release services without marking a non-current release', () => {
    const wrapper = mountReleaseTab()

    const current = wrapper.get('[data-testid="release-current-services"]')
    expect(current.text()).toBe('Current')
    expect(current.attributes('title')).toContain('oauth')
    expect(wrapper.findAll('button').some((button) => button.text() === 'Roll back to this release')).toBe(true)
    expect(wrapper.findAll('[data-testid="release-current-services"]')).toHaveLength(1)
  })

  it('does not invoke rollback for an already-active release', async () => {
    const rollback = vi.fn()
    const wrapper = mountReleaseTab({ rollback })

    const rollbackButtons = wrapper.findAll('button').filter((button) => button.text() === 'Roll back to this release')
    expect(rollbackButtons).toHaveLength(2)
    expect(rollbackButtons[0].attributes('disabled')).toBeDefined()

    await rollbackButtons[0].trigger('click')
    expect(rollback).not.toHaveBeenCalled()
  })

  it('disables deletion for the current release and enables an eligible historical release', () => {
    const wrapper = mountReleaseTab()

    const deleteButtons = wrapper.findAll('button').filter((button) => button.text() === 'Delete')
    expect(deleteButtons).toHaveLength(2)
    expect(deleteButtons[0].attributes('disabled')).toBeDefined()
    expect(deleteButtons[1].attributes('disabled')).toBeUndefined()
  })
})

function mountReleaseTab({ rollback = vi.fn() }: { rollback?: ReturnType<typeof vi.fn> } = {}) {
  const Root = defineComponent({
    setup() {
      provide(aifarRuntimeContextKey, runtimeContext(rollback))
      return () => h(AifarRuntimeReleasesTab)
    }
  })

  return mount(Root, {
    global: {
      stubs: {
        ElTable: defineComponent({
          props: { data: { type: Array as PropType<AifarRelease[]>, default: () => [] } },
          setup(props, { slots }) {
            provide(tableRowsKey, computed(() => props.data))
            return () => h('div', slots.default?.())
          }
        }),
        ElTableColumn: defineComponent({
          props: { label: String },
          setup(_, { slots }) {
            const rows = inject(tableRowsKey, computed(() => [] as AifarRelease[]))
            return () => h('div', [
              slots.default ? rows.value.map((row) => h('div', { key: row.releaseId }, slots.default?.({ row }))) : null
            ])
          }
        }),
        ElButton: defineComponent({
          props: { disabled: Boolean, loading: Boolean },
          setup(props, { slots, attrs }) {
            return () => h('button', {
              disabled: props.disabled,
              onClick: (event: MouseEvent) => {
                if (!props.disabled) (attrs.onClick as ((event: MouseEvent) => void) | undefined)?.(event)
              }
            }, slots.default?.())
          }
        }),
        ElTooltip: defineComponent({
          props: { content: String },
          setup(props, { slots }) {
            return () => h('span', { title: props.content }, slots.default?.())
          }
        }),
        ElSpace: defineComponent({
          setup(_, { slots }) {
            return () => h('span', slots.default?.())
          }
        }),
        ElTag: defineComponent({
          setup(_, { slots, attrs }) {
            return () => h('span', attrs, slots.default?.())
          }
        }),
        StatusTag: defineComponent({
          props: { label: String },
          setup(props) {
            return () => h('span', props.label)
          }
        })
      }
    }
  })
}

function runtimeContext(rollback: ReturnType<typeof vi.fn>): AifarRuntimeContext {
  const releases: AifarRelease[] = [
    {
      instanceId: 'instance-1',
      releaseId: 'release-current',
      currentServices: ['oauth'],
      rollbackUnavailableReason: 'ALREADY_ACTIVE',
      deleteAvailable: false,
      deleteUnavailableReason: 'AIFAR_RELEASE_DELETE_CURRENT'
    },
    {
      instanceId: 'instance-1',
      releaseId: 'release-previous',
      deleteAvailable: true,
      deleteUnavailableReason: ''
    }
  ]
  const labels: Record<string, string> = {
    'common.refresh': 'Refresh',
    'containers.releaseCount': '2 releases',
    'containers.releaseId': 'Release ID',
    'containers.releaseKind': 'Kind',
    'common.status': 'Status',
    'containers.service': 'Service',
    'containers.activatedAt': 'Activated at',
    'common.operation': 'Operation',
    'containers.rollbackRelease': 'Roll back to this release',
    'common.delete': 'Delete',
    'containers.releaseCurrent': 'Current',
    'containers.releaseCurrentServices': 'Current services: {services}'
  }
  return {
    t: (key: string, named?: Record<string, unknown>) => labels[key]?.replace('{services}', String(named?.services ?? '')) ?? key,
    loading: computed(() => false),
    aifarReleases: ref(releases),
    loadAifarReleases: async () => {},
    releaseKindLabel: () => '',
    aifarRuntimeStatusKind: () => '',
    releaseStatusLabel: () => '',
    releaseServicesText: () => '',
    releaseCurrentServicesText: (row: AifarRelease) => row.currentServices?.join(', ') ?? '',
    releaseIsCurrent: (row: AifarRelease) => Boolean(row.currentServices?.length),
    releaseRollbackDisabledReason: (row: AifarRelease) => row.rollbackUnavailableReason === 'ALREADY_ACTIVE' ? 'already active' : '',
    rollbackAifarRelease: rollback,
    releaseDeletingId: ref(''),
    releaseDeleteDisabledReason: (row: AifarRelease) => runtimeReleaseDeleteDisabledReason(row, {
      canManage: true,
      deniedText: 'permission denied',
      t: (key: string) => labels[key] ?? key
    }),
    deleteAifarRelease: async () => {}
  } as unknown as AifarRuntimeContext
}
