<template>
  <div class="runtime-workspace">
    <div class="runtime-toolbar">
      <div class="runtime-status">
        <StatusTag :status="aifarRuntimeStatusKind(aifarRuntime.runtimeStatus)" :label="aifarRuntimeStatusLabel(aifarRuntime.runtimeStatus)" />
        <span>{{ t('containers.agent') }}:</span>
        <StatusTag :status="aifarRuntimeStatusKind(aifarRuntime.agent?.status)" :label="aifarRuntimeStatusLabel(aifarRuntime.agent?.status)" />
      </div>
      <div class="toolbar-actions">
        <el-select v-model="selectedRuntimeInstanceId" size="small" class="runtime-instance-select" :placeholder="t('containers.selectAifarInstance')">
          <el-option v-for="instance in aifarRuntimeInstances" :key="instance.id" :label="runtimeInstanceLabel(instance)" :value="instance.id" />
        </el-select>
        <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
          <span><el-button size="small" type="primary" :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="openRuntimeConfigDialog">{{ t('containers.runtimeConfig') }}</el-button></span>
        </el-tooltip>
        <el-tooltip :content="serviceInstallDisabledReason" :disabled="!serviceInstallDisabledReason" placement="top">
          <span><el-button size="small" type="primary" plain :disabled="Boolean(serviceInstallDisabledReason)" @click="openServiceInstallDialog">{{ t('containers.installServices') }}</el-button></span>
        </el-tooltip>
        <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
          <span><el-button size="small" type="primary" plain :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="openAifarRuntimeBundleUpdate">{{ t('containers.bundleUpdate') }}</el-button></span>
        </el-tooltip>
        <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
          <span><el-button size="small" :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="reconcileAifarRuntime">{{ t('containers.reconcileRuntime') }}</el-button></span>
        </el-tooltip>
        <el-tooltip :content="runtimeRestartDisabledReason" :disabled="!runtimeRestartDisabledReason" placement="top">
          <span>
            <el-button size="small" type="warning" plain :loading="runtimeRestartSubmitting" :disabled="Boolean(runtimeRestartDisabledReason)" @click="restartAllAifarRuntime">
              {{ t('containers.restartAllRuntime') }}
            </el-button>
          </span>
        </el-tooltip>
        <el-tooltip :content="runtimeCleanupDisabledReason" :disabled="!runtimeCleanupDisabledReason" placement="top">
          <span><el-button size="small" type="warning" plain :disabled="Boolean(runtimeCleanupDisabledReason)" @click="cleanupAifarRuntimeStale">{{ t('containers.cleanupStaleRuntime') }}</el-button></span>
        </el-tooltip>
        <el-button size="small" :loading="loading" @click="loadAifarRuntime(true)">{{ t('common.refresh') }}</el-button>
      </div>
    </div>
    <el-alert
      v-if="aifarRuntimeWarnings.length"
      type="warning"
      :closable="false"
      show-icon
      :title="aifarRuntimeWarnings.join('；')"
    />
    <div v-if="!aifarRuntimeInstances.length" class="empty-state">
      <div>
        <strong>{{ t('containers.aifarRuntime') }}</strong>
        <span>{{ t('containers.noAifarRuntime') }}</span>
      </div>
    </div>
    <template v-else>
      <KeyValueGrid :items="runtimeSummaryItems" />
      <el-tabs v-model="runtimeResourceTab" class="runtime-resource-tabs">
        <el-tab-pane v-for="tabName in runtimeResourceTabOrder" :key="tabName" :label="t(runtimeResourceTabLabels[tabName])" :name="tabName">
          <component :is="runtimeResourceTabComponents[tabName]" />
        </el-tab-pane>
      </el-tabs>
    </template>
  </div>
</template>

<script setup lang="ts">
import type { Component } from 'vue'
import KeyValueGrid from '../../components/KeyValueGrid.vue'
import StatusTag from '../../components/StatusTag.vue'
import AifarRuntimeDeploymentsTab from './AifarRuntimeDeploymentsTab.vue'
import AifarRuntimeIngressTab from './AifarRuntimeIngressTab.vue'
import AifarRuntimeLogsTab from './AifarRuntimeLogsTab.vue'
import AifarRuntimePodsTab from './AifarRuntimePodsTab.vue'
import AifarRuntimeReleasesTab from './AifarRuntimeReleasesTab.vue'
import AifarRuntimeServicesTab from './AifarRuntimeServicesTab.vue'
import { useAifarRuntimeContext, type RuntimeResourceTab } from './context'
import { runtimeResourceTabLabels, runtimeResourceTabOrder } from './surface'
import './runtime.css'

const runtimeResourceTabComponents: Record<RuntimeResourceTab, Component> = {
  deployments: AifarRuntimeDeploymentsTab,
  services: AifarRuntimeServicesTab,
  pods: AifarRuntimePodsTab,
  logs: AifarRuntimeLogsTab,
  ingress: AifarRuntimeIngressTab,
  releases: AifarRuntimeReleasesTab
}

const {
  t,
  loading,
  aifarRuntime,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  selectedRuntimeInstanceId,
  aifarRuntimeInstances,
  runtimeInstanceLabel,
  aifarRuntimeActionDisabledReason,
  openRuntimeConfigDialog,
  serviceInstallDisabledReason,
  openServiceInstallDialog,
  openAifarRuntimeBundleUpdate,
  reconcileAifarRuntime,
  runtimeRestartDisabledReason,
  runtimeRestartSubmitting,
  restartAllAifarRuntime,
  runtimeCleanupDisabledReason,
  cleanupAifarRuntimeStale,
  loadAifarRuntime,
  aifarRuntimeWarnings,
  runtimeSummaryItems,
  runtimeResourceTab
} = useAifarRuntimeContext()
</script>
