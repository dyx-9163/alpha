// @vitest-environment happy-dom

import { computed, ref } from 'vue'
import { shallowMount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import AifarRuntimeWorkspace from './AifarRuntimeWorkspace.vue'
import { aifarRuntimeContextKey, type AifarRuntimeContext } from './context'

function noop() {}

function runtimeContext(overrides: Partial<AifarRuntimeContext> = {}) {
  const context = {
    t: (key: string) => key,
    loading: computed(() => false),
    aifarRuntime: ref({
      runtimeStatus: 'degraded',
      agent: { status: 'missing', error: 'agent disconnected' },
      instances: [{ id: 'app-aifar', status: 'running', version: 'runtime-v2' }],
      deployments: [{ instanceId: 'app-aifar', serviceName: 'permission' }],
      services: [{ instanceId: 'app-aifar', serviceName: 'permission' }],
      pods: [{ instanceId: 'app-aifar', serviceName: 'permission', containerName: 'permission-1' }],
      ingress: [{ instanceId: 'app-aifar', status: 'running' }],
      warnings: ['agent disconnected']
    }),
    aifarRuntimeDataAvailable: computed(() => false),
    aifarRuntimeStatusKind: (status?: string) => status || 'unknown',
    aifarRuntimeStatusLabel: (status?: string) => status || 'unknown',
    selectedRuntimeInstanceId: ref(''),
    runtimeTargetQuery: () => 'serverId=srv-1',
    aifarRuntimeInstances: computed(() => [{ id: 'app-aifar', status: 'running', version: 'runtime-v2' }]),
    runtimeInstanceLabel: () => 'runtime-v2 / app-aifar',
    aifarRuntimeActionDisabledReason: computed(() => 'containers.agentUnavailableDisabled'),
    openRuntimeConfigDialog: noop,
    serviceInstallDisabledReason: computed(() => 'containers.agentUnavailableDisabled'),
    openServiceInstallDialog: noop,
    openAifarRuntimeBundleUpdate: noop,
    reconcileAifarRuntime: noop,
    runtimeRestartDisabledReason: computed(() => 'containers.agentUnavailableDisabled'),
    runtimeRestartSubmitting: ref(false),
    restartAllAifarRuntime: noop,
    runtimeCleanupDisabledReason: computed(() => 'containers.agentUnavailableDisabled'),
    cleanupAifarRuntimeStale: noop,
    aifarRuntimeWarnings: computed(() => ['agent disconnected']),
    runtimeSummaryItems: computed(() => []),
    runtimeResourceTab: ref('deployments'),
    selectedRuntimeDeployments: computed(() => []),
    runtimeDeploymentReplicaText: () => '',
    runtimeServiceActionDisabledReason: () => 'containers.agentUnavailableDisabled',
    openAifarRuntimeServiceUpdate: noop,
    runtimeServiceForDeployment: () => ({ instanceId: 'app-aifar', serviceName: 'permission' }),
    scaleOutAifarService: async () => {},
    aifarRuntimeScaleInDisabledReason: () => 'containers.agentUnavailableDisabled',
    scaleInAifarDeployment: async () => {},
    reconcileAifarDeployment: async () => {},
    aifarRuntimeOfflineDisabledReason: () => 'containers.agentUnavailableDisabled',
    offlineAifarService: async () => {},
    offlineAifarServices: async () => false,
    aifarReleases: ref([]),
    loadAifarReleases: async () => {},
    releaseKindLabel: () => '',
    releaseStatusLabel: () => '',
    releaseServicesText: () => '',
    releaseCurrentServicesText: () => '',
    releaseIsCurrent: () => false,
    formatDate: () => '',
    releaseRollbackDisabledReason: () => '',
    rollbackAifarRelease: async () => {},
    releaseDeletingId: ref(''),
    releaseDeleteDisabledReason: () => '',
    deleteAifarRelease: async () => {},
    selectedRuntimeServices: computed(() => []),
    runtimeEndpointText: () => '',
    percentText: () => '',
    runtimePodServiceFilter: ref(''),
    clearRuntimePodServiceFilter: noop,
    installedRuntimeServiceNamesList: computed(() => []),
    ensureRuntimePodsLoaded: async () => {},
    runtimePodsLoadedForCurrentScope: computed(() => false),
    selectedRuntimePods: computed(() => []),
    refreshRuntimePodBase: async () => {},
    refreshRuntimePodMetrics: noop,
    openRuntimePodLogs: noop,
    runtimeLogServiceFilter: ref([]),
    clearRuntimeLogServiceFilter: noop,
    runtimeLogPodFilter: ref([]),
    runtimeLogPodOptions: computed(() => []),
    runtimeLogLevelFilter: ref([]),
    runtimeLogKeyword: ref(''),
    runtimeLogTail: ref(200),
    runtimeLogSelectionReady: computed(() => false),
    loadRuntimeLogs: noop,
    runtimeLogsLoadedForCurrentScope: computed(() => false),
    toggleRuntimeLogPaused: noop,
    runtimeLogPaused: ref(false),
    runtimeLogRows: ref([]),
    runtimeLogErrorCount: computed(() => 0),
    showRuntimeLogErrorsOnly: noop,
    runtimeLogPendingCount: computed(() => 0),
    clearRuntimeLogView: noop,
    runtimeLogAutoScroll: ref(true),
    runtimeLogWarnings: computed(() => []),
    runtimeLogGroups: computed(() => []),
    runtimeLogStreamTagType: computed(() => 'unknown'),
    runtimeLogStreamStatusLabel: computed(() => 'idle'),
    runtimeLogLastDataAt: ref(''),
    filteredRuntimeLogRows: computed(() => []),
    runtimeLogDroppedRows: ref(0),
    runtimeLogViewport: ref(null),
    handleRuntimeLogScroll: noop,
    runtimeLogTopSpacer: computed(() => 0),
    runtimeLogVirtualRows: computed(() => []),
    runtimeLogBottomSpacer: computed(() => 0),
    runtimeEntryRoutes: computed(() => []),
    runtimeDiscoveryTarget: () => '',
    aifarUpdateVisible: ref(false),
    selectedAifarContainerLabel: computed(() => ''),
    selectedAifarInstanceLabel: computed(() => ''),
    aifarUpdateModeLabel: computed(() => ''),
    aifarUpdateMode: ref('single'),
    aifarUpdateService: ref('oauth'),
    aifarArtifactAccept: computed(() => ''),
    handleAifarArtifactChange: noop,
    clearAifarArtifact: noop,
    aifarArtifactHint: computed(() => ''),
    aifarUpdateSubmitting: ref(false),
    submitAifarUpdate: async () => {},
    serviceInstallVisible: ref(false),
    missingRuntimeServiceOptions: computed(() => []),
    serviceInstallSelection: ref([]),
    serviceInstallSubmitting: ref(false),
    submitAifarServiceInstall: async () => {},
    runtimeConfigVisible: ref(false),
    runtimeConfigMetaItems: computed(() => []),
    runtimeConfigForm: ref({ appCPUs: '2.0', appMemoryLimit: '2GB', jvmInitialRAMPercentage: 20, jvmMaxRAMPercentage: 70 }),
    runtimeConfigRows: ref([]),
    runtimeConfigSubmitting: ref(false),
    submitRuntimeConfig: async () => {},
    ...overrides
  } as unknown as AifarRuntimeContext
  return context
}

describe('AifarRuntimeWorkspace', () => {
  it('keeps the last runtime data visible with a stale-data warning when aifar-agent is unavailable', () => {
    const wrapper = shallowMount(AifarRuntimeWorkspace, {
      global: {
        provide: {
          [aifarRuntimeContextKey as symbol]: runtimeContext()
        },
        stubs: {
          StatusTag: true,
          AifarRuntimeOverflowAction: true,
          AifarRuntimeSummary: true,
          AifarRuntimeDeploymentsTab: true,
          AifarRuntimeServicesTab: true,
          AifarRuntimePodsTab: true,
          AifarRuntimeLogsTab: true,
          AifarRuntimeIngressTab: true,
          'el-alert': { template: '<div class="el-alert-stub">{{ title }}</div>', props: ['title'] },
          'el-button': true,
          'el-dropdown': true,
          'el-dropdown-menu': true,
          'el-option': true,
          'el-select': true,
          'el-tab-pane': true,
          'el-tabs': { template: '<div class="runtime-resource-tabs"><slot /></div>' },
          'el-tooltip': true
        }
      }
    })

    expect(wrapper.find('.runtime-resource-tabs').exists()).toBe(true)
    expect(wrapper.text()).toContain('containers.runtimeAgentUnavailableStale')
    expect(wrapper.text()).not.toContain('containers.runtimeAgentUnavailableEmpty')
  })
})
