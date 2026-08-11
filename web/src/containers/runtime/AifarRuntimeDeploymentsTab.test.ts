// @vitest-environment happy-dom

import { mount } from '@vue/test-utils'
import { computed, defineComponent, h, inject, provide, ref, type PropType } from 'vue'
import { describe, expect, it } from 'vitest'
import AifarRuntimeDeploymentsTab from './AifarRuntimeDeploymentsTab.vue'
import { aifarRuntimeContextKey, type AifarRuntimeContext } from './context'
import { formatDate } from './format'
import { normalizeBatchOfflineDeployments } from './runtimeDeploymentSelection'
import type { AifarRuntimeDeployment } from './types'

describe('AifarRuntimeDeploymentsTab selection', () => {
  it('keeps unique online deployments and excludes rows that cannot be offlined', () => {
    const rows = [
      deployment('gateway', 1),
      deployment('oauth', 2),
      deployment('gateway', 1),
      deployment('file', 0)
    ]

    expect(normalizeBatchOfflineDeployments(
      rows,
      (row) => ({ ...row }),
      (row) => Number(row.desiredReplicas ?? 0) <= 0 ? 'offline' : ''
    )).toEqual([
      expect.objectContaining({ serviceName: 'gateway' }),
      expect.objectContaining({ serviceName: 'oauth' })
    ])
  })

  it('renders generation, condition diagnostics, and transition time without Nacos status', () => {
    const wrapper = mountDeploymentTab(deployment('permission', 1, {
      currentReplicas: 1,
      readyReplicas: 0,
      generation: 7,
      observedGeneration: 6,
      status: 'degraded',
      conditions: [{
        type: 'Degraded',
        status: true,
        reason: 'ReadinessFailed',
        message: 'readiness probe failed',
        generation: 6,
        lastTransitionTime: '2026-08-10T08:09:10Z'
      }],
      lastTransitionAt: '2026-08-10T08:09:10Z'
    }))

    expect(wrapper.text()).toContain('7 / 6')
    expect(wrapper.text()).toContain('ReadinessFailed')
    expect(wrapper.text()).toContain('readiness probe failed')
    expect(wrapper.text()).toContain(formatDate('2026-08-10T08:09:10Z'))
    expect(wrapper.text()).not.toContain('Nacos')
  })

  it('renders status-only legacy rows as unknown instead of inventing readiness', () => {
    const wrapper = mountDeploymentTab(deployment('gateway', 1, { status: 'ready', conditions: [] }))

    expect(wrapper.text()).toContain('containers.runtimePhase.unknown')
    expect(wrapper.text()).not.toContain('>ready<')
    expect(wrapper.text()).toContain('containers.conditionUnavailable')
  })
})

const tableRowsKey = Symbol('runtime-deployment-rows')

function mountDeploymentTab(row: AifarRuntimeDeployment) {
  const Root = defineComponent({
    setup() {
      provide(aifarRuntimeContextKey, {
        t: (key: string) => key,
        selectedRuntimeDeployments: computed(() => [row]),
        selectedRuntimeInstanceId: ref('instance-1'),
        aifarRuntimeStatusKind: (status?: string) => status ?? 'unknown',
        aifarRuntimeStatusLabel: (status?: string) => status ?? 'unknown',
        runtimeDeploymentReplicaText: () => '0 / 1',
        runtimeServiceActionDisabledReason: () => '',
        openAifarRuntimeServiceUpdate: () => {},
        runtimeServiceForDeployment: (deploymentRow: AifarRuntimeDeployment) => ({
          instanceId: deploymentRow.instanceId,
          serviceName: deploymentRow.serviceName,
          desiredReplicas: deploymentRow.desiredReplicas
        }),
        scaleOutAifarService: async () => {},
        aifarRuntimeScaleInDisabledReason: () => '',
        scaleInAifarDeployment: async () => {},
        aifarRuntimeOfflineDisabledReason: () => '',
        offlineAifarService: async () => {},
        offlineAifarServices: async () => true
      } as unknown as AifarRuntimeContext)
      return () => h(AifarRuntimeDeploymentsTab)
    }
  })
  return mount(Root, {
    global: { stubs: deploymentStubs }
  })
}

const deploymentStubs = {
  ElTable: defineComponent({
    props: { data: { type: Array as PropType<AifarRuntimeDeployment[]>, default: () => [] } },
    setup(props, { slots }) {
      provide(tableRowsKey, computed(() => props.data))
      return () => h('div', slots.default?.())
    }
  }),
  ElTableColumn: defineComponent({
    props: { label: String, prop: String },
    setup(props, { slots }) {
      const rows = inject(tableRowsKey, computed(() => [] as AifarRuntimeDeployment[]))
      return () => h('div', [
        props.label,
        ...rows.value.map((row) => h('div', slots.default?.({ row }) ?? String(row[props.prop as keyof AifarRuntimeDeployment] ?? '')))
      ])
    }
  }),
  ElButton: defineComponent({ setup(_, { slots }) { return () => h('button', slots.default?.()) } }),
  ElTooltip: defineComponent({ setup(_, { slots }) { return () => h('span', slots.default?.()) } }),
  StatusTag: defineComponent({ props: { label: String }, setup(props) { return () => h('span', props.label) } })
}

function deployment(serviceName: string, desiredReplicas: number, overrides: Partial<AifarRuntimeDeployment> = {}): AifarRuntimeDeployment {
  return {
    instanceId: 'instance-1',
    deploymentName: `alpha-${serviceName}`,
    serviceName,
    desiredReplicas,
    status: desiredReplicas > 0 ? 'ready' : 'offline',
    ...overrides
  }
}
