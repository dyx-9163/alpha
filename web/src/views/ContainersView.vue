<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('containers.title') }}</h1>
        <p class="page-subtitle">{{ t('containers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <ServerSelector v-model="selectedServerId" :servers="dockerServers" :placeholder="t('containers.selectDockerHost')" class="toolbar-control" />
        <el-button @click="load">{{ t('containers.checkHost') }}</el-button>
        <el-button @click="loadActive">{{ t('common.refresh') }}</el-button>
        <el-tooltip v-if="selectedDockerInstance" :content="deniedText" :disabled="canManageApps" placement="top">
          <span>
            <el-button type="danger" plain :disabled="!canManageApps" @click="openDockerUninstall">{{ t('common.uninstall') }}</el-button>
          </span>
        </el-tooltip>
      </div>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('containers.overview')" name="overview" />
      <el-tab-pane :label="t('containers.title')" name="containers" />
      <el-tab-pane :label="t('containers.aifarRuntime')" name="aifar-runtime" />
      <el-tab-pane :label="t('containers.images')" name="images" />
      <el-tab-pane :label="t('containers.network')" name="networks" />
      <el-tab-pane :label="t('containers.volumes')" name="volumes" />
      <el-tab-pane :label="t('containers.registry')" name="registry" />
      <el-tab-pane :label="t('containers.settings')" name="settings" />
    </el-tabs>

    <el-alert v-if="error" :title="errorTitle" :description="error" type="warning" :closable="false" show-icon />
    <div class="muted-strip" v-if="!summary.available">{{ t('containers.disabledHint') }}</div>

    <div class="workspace-card containers-main">
      <template v-if="tab === 'overview'">
        <MetricGrid :items="metrics" />

        <div class="sub-panel">
          <h2 class="section-title">{{ t('containers.diskUsage') }}</h2>
          <div class="disk-grid">
            <div v-for="item in normalizedDiskUsage" :key="item.type">
              <strong>{{ item.type }}</strong>
              <span>{{ item.size }}</span>
              <small>{{ t('containers.reclaimable') }} {{ item.reclaimable || '-' }}</small>
            </div>
          </div>
        </div>

        <div class="sub-panel">
          <h2 class="section-title">{{ t('containers.configSummary') }}</h2>
          <KeyValueGrid :items="configSummaryItems" />
        </div>
      </template>

      <template v-else-if="tab === 'containers'">
        <div class="table-toolbar">
          <span class="selection-summary">{{ t('containers.selectedCount', { count: selectedContainerRows.length }) }}</span>
          <div class="toolbar-actions">
            <el-switch
              v-model="showAifarRuntimeInfra"
              :active-text="t('containers.showRuntimeInfra')"
              inline-prompt
              class="runtime-infra-switch"
            />
            <el-tooltip :content="batchActionDisabledReason" :disabled="!batchActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="batchActionDisabled" @click="runContainerBatchAction('start')">{{ t('containers.batchStart') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="batchActionDisabledReason" :disabled="!batchActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="batchActionDisabled" @click="runContainerBatchAction('stop')">{{ t('containers.batchStop') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="batchActionDisabledReason" :disabled="!batchActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="batchActionDisabled" @click="runContainerBatchAction('restart')">{{ t('containers.batchRestart') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="batchRemoveDisabledReason" :disabled="!batchRemoveDisabledReason" placement="top">
              <span><el-button size="small" type="danger" plain :disabled="batchRemoveDisabled" @click="runContainerBatchAction('remove')">{{ t('containers.batchUninstall') }}</el-button></span>
            </el-tooltip>
          </div>
        </div>
        <div class="container-table-body">
          <el-table :data="containerTableRows" height="100%" row-key="id" @selection-change="onContainerSelectionChange">
            <el-table-column type="selection" width="44" :selectable="containerRowSelectable" />
            <el-table-column prop="name" :label="t('containers.name')" min-width="160" show-overflow-tooltip />
            <el-table-column prop="image" :label="t('containers.image')" min-width="190" show-overflow-tooltip />
            <el-table-column prop="state" :label="t('common.status')" width="130">
              <template #default="{ row }">
                <el-tooltip :content="containerStatusDetail(row)" :disabled="!containerStatusDetail(row)" placement="top">
                  <span>
                    <StatusTag :status="containerStatusKind(row)" :label="containerStatusLabel(row)" />
                  </span>
                </el-tooltip>
              </template>
            </el-table-column>
            <el-table-column prop="ports" :label="t('containers.ports')" min-width="180" show-overflow-tooltip />
            <el-table-column prop="networks" :label="t('containers.network')" min-width="140" show-overflow-tooltip />
            <el-table-column prop="createdAt" :label="t('containers.created')" min-width="170" show-overflow-tooltip />
            <el-table-column :label="t('common.operation')" width="430" fixed="right">
              <template #default="{ row }">
                <div class="row-actions">
                  <template v-if="!isAifarRuntimeInfraContainer(row)">
                    <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                      <span><el-button size="small" :disabled="!canManageContainers" @click="runContainerAction(row.id, 'start')">{{ t('containers.start') }}</el-button></span>
                    </el-tooltip>
                    <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                      <span><el-button size="small" :disabled="!canManageContainers" @click="runContainerAction(row.id, 'stop')">{{ t('containers.stop') }}</el-button></span>
                    </el-tooltip>
                    <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                      <span><el-button size="small" :disabled="!canManageContainers" @click="runContainerAction(row.id, 'restart')">{{ t('containers.restart') }}</el-button></span>
                    </el-tooltip>
                    <el-tooltip :content="containerRemoveDisabledReason(row)" :disabled="!containerRemoveDisabledReason(row)" placement="top">
                      <span><el-button size="small" type="danger" plain :disabled="Boolean(containerRemoveDisabledReason(row))" @click="runContainerBatchAction('remove', [row])">{{ t('containers.uninstall') }}</el-button></span>
                    </el-tooltip>
                    <el-tooltip v-if="isAifarUpdatableContainer(row)" :content="aifarUpdateDisabledReason(row)" :disabled="!aifarUpdateDisabledReason(row)" placement="top">
                      <span><el-button size="small" type="primary" plain :disabled="Boolean(aifarUpdateDisabledReason(row))" @click="openAifarUpdate(row)">{{ t('containers.updateService') }}</el-button></span>
                    </el-tooltip>
                  </template>
                  <el-tag v-else type="info" size="small">{{ t('containers.runtimeInfra') }}</el-tag>
                  <el-button size="small" @click="openLogs(row.id)">{{ t('containers.logs') }}</el-button>
                </div>
              </template>
            </el-table-column>
          </el-table>
        </div>
      </template>

      <template v-else-if="tab === 'aifar-runtime'">
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
              <el-tooltip :content="runtimeCleanupDisabledReason" :disabled="!runtimeCleanupDisabledReason" placement="top">
                <span><el-button size="small" type="warning" plain :disabled="Boolean(runtimeCleanupDisabledReason)" @click="cleanupAifarRuntimeStale">{{ t('containers.cleanupStaleRuntime') }}</el-button></span>
              </el-tooltip>
              <el-tooltip :content="runtimeAgentUninstallDisabledReason" :disabled="!runtimeAgentUninstallDisabledReason" placement="top">
                <span><el-button size="small" type="danger" plain :disabled="Boolean(runtimeAgentUninstallDisabledReason)" @click="uninstallAifarRuntimeAgent">{{ t('containers.uninstallAgent') }}</el-button></span>
              </el-tooltip>
              <el-button size="small" @click="loadAifarRuntime">{{ t('common.refresh') }}</el-button>
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
            <el-table :data="selectedRuntimeServices" height="48%" row-key="serviceName">
              <el-table-column prop="serviceName" :label="t('containers.service')" min-width="130" show-overflow-tooltip />
              <el-table-column prop="status" :label="t('common.status')" width="130">
                <template #default="{ row }">
                  <StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="aifarRuntimeStatusLabel(row.status)" />
                </template>
              </el-table-column>
              <el-table-column :label="t('containers.replicas')" width="120">
                <template #default="{ row }">{{ row.readyReplicas }} / {{ row.desiredReplicas }}</template>
              </el-table-column>
              <el-table-column prop="currentRevision" :label="t('containers.revision')" min-width="170" show-overflow-tooltip />
              <el-table-column prop="image" :label="t('containers.image')" min-width="220" show-overflow-tooltip />
              <el-table-column :label="t('containers.cpu')" width="90">
                <template #default="{ row }">{{ percentText(row.cpuPercent) }}</template>
              </el-table-column>
              <el-table-column :label="t('containers.memory')" width="100">
                <template #default="{ row }">{{ percentText(row.memoryPercent) }}</template>
              </el-table-column>
              <el-table-column :label="t('common.operation')" width="230" fixed="right">
                <template #default="{ row }">
                  <div class="row-actions">
                    <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
                      <span><el-button size="small" type="primary" plain :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="openAifarRuntimeServiceUpdate(row)">{{ t('containers.updateService') }}</el-button></span>
                    </el-tooltip>
                    <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
                      <span><el-button size="small" :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="scaleOutAifarService(row.serviceName)">{{ t('containers.scaleOut') }}</el-button></span>
                    </el-tooltip>
                  </div>
                </template>
              </el-table-column>
            </el-table>
            <el-table :data="selectedRuntimePods" height="42%" row-key="containerName">
              <el-table-column prop="containerName" :label="t('containers.name')" min-width="220" show-overflow-tooltip />
              <el-table-column prop="serviceName" :label="t('containers.service')" width="120" show-overflow-tooltip />
              <el-table-column prop="status" :label="t('common.status')" width="120">
                <template #default="{ row }">
                  <StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="aifarRuntimeStatusLabel(row.status)" />
                </template>
              </el-table-column>
              <el-table-column prop="revision" :label="t('containers.revision')" min-width="170" show-overflow-tooltip />
              <el-table-column :label="t('containers.cpu')" width="90">
                <template #default="{ row }">{{ percentText(row.cpuPercent) }}</template>
              </el-table-column>
              <el-table-column :label="t('containers.memory')" width="110">
                <template #default="{ row }">{{ row.memoryUsage || percentText(row.memoryPercent) }}</template>
              </el-table-column>
            </el-table>
          </template>
        </div>
      </template>

      <template v-else-if="tab === 'images'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="repository" :label="t('containers.repository')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="tag" :label="t('containers.tag')" width="140" show-overflow-tooltip />
          <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
          <el-table-column prop="size" :label="t('containers.size')" width="120" />
          <el-table-column prop="digest" :label="t('containers.digest')" min-width="220" show-overflow-tooltip />
          <el-table-column prop="createdAt" :label="t('containers.created')" min-width="170" show-overflow-tooltip />
          <el-table-column :label="t('common.operation')" width="110" fixed="right">
            <template #default="{ row }">
              <el-tooltip :content="deniedText" :disabled="canManageContainers" placement="top">
                <span>
                  <el-button size="small" type="danger" plain :disabled="!canManageContainers" @click="deleteImage(row)">{{ t('containers.deleteImage') }}</el-button>
                </span>
              </el-tooltip>
            </template>
          </el-table-column>
        </el-table>
      </template>

      <template v-else-if="tab === 'networks'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="name" :label="t('containers.name')" min-width="180" />
          <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
          <el-table-column prop="driver" :label="t('containers.driver')" min-width="150" />
          <el-table-column prop="scope" :label="t('containers.scope')" min-width="120" />
        </el-table>
      </template>

      <template v-else-if="tab === 'volumes'">
        <el-table :data="collection" height="100%">
          <el-table-column prop="name" :label="t('containers.name')" min-width="180" show-overflow-tooltip />
          <el-table-column prop="driver" :label="t('containers.driver')" width="140" />
          <el-table-column prop="scope" :label="t('containers.scope')" width="120" />
          <el-table-column prop="mountpoint" :label="t('containers.mountpoint')" min-width="260" show-overflow-tooltip />
          <el-table-column prop="size" :label="t('containers.size')" width="120" />
        </el-table>
      </template>

      <template v-else-if="tab === 'registry'">
        <div class="empty-state">
          <div>
            <strong>{{ t('containers.registry') }}</strong>
            <span>{{ t('containers.registryHint') }}</span>
          </div>
        </div>
      </template>

      <template v-else>
        <div class="settings-grid">
          <KeyValueGrid :items="settingsItems" />
          <p class="muted-strip">{{ t('containers.settingsHint') }}</p>
        </div>
      </template>
    </div>

    <LogDrawer v-model="logsVisible" :title="t('containers.logs')" :text="logsText" :empty-text="t('tasks.noLogs')" />
    <el-dialog v-model="aifarUpdateVisible" :title="t('apps.aifarUpdateTitle')" width="560px" destroy-on-close>
      <el-form label-width="112px" class="aifar-update-form">
        <el-form-item :label="t('containers.updateContainer')">
          <el-input :model-value="selectedAifarContainerLabel" disabled />
        </el-form-item>
        <el-form-item :label="t('apps.aifarUpdateInstance')">
          <el-input :model-value="selectedAifarInstanceLabel" disabled />
        </el-form-item>
        <el-form-item :label="t('apps.aifarUpdateMode')" required>
          <el-radio-group v-model="aifarUpdateMode">
            <el-radio-button label="single">{{ t('apps.aifarUpdateSingleMode') }}</el-radio-button>
            <el-radio-button label="bundle">{{ t('apps.aifarUpdateBundleMode') }}</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="aifarUpdateMode === 'single'" :label="t('apps.aifarUpdateService')" required>
          <el-select v-model="aifarUpdateService" filterable>
            <el-option v-for="service in aifarServiceOptions" :key="service.value" :label="service.label" :value="service.value" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('apps.aifarUpdateArtifact')" required>
          <el-upload
            :key="`${aifarUpdateMode}-${aifarUpdateService}`"
            :auto-upload="false"
            :limit="1"
            :accept="aifarArtifactAccept"
            :on-change="handleAifarArtifactChange"
            :on-remove="clearAifarArtifact"
          >
            <el-button>{{ t('apps.aifarUpdateChooseArtifact') }}</el-button>
          </el-upload>
          <div class="artifact-hint">{{ aifarArtifactHint }}</div>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="aifarUpdateVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="aifarUpdateSubmitting" @click="submitAifarUpdate">{{ t('apps.aifarUpdateSubmit') }}</el-button>
      </template>
    </el-dialog>
    <el-dialog v-model="serviceInstallVisible" :title="t('containers.installServicesDialog')" width="560px" destroy-on-close>
      <div class="service-install-dialog">
        <div>
          <div class="runtime-config-section-title">{{ t('containers.installedServices') }}</div>
          <div class="service-tag-list">
            <el-tag v-for="service in installedRuntimeServiceNamesList" :key="service" size="small">{{ service }}</el-tag>
          </div>
        </div>
        <div>
          <div class="runtime-config-section-title">{{ t('containers.installableServices') }}</div>
          <el-empty v-if="!missingRuntimeServiceOptions.length" :description="t('containers.noMissingServices')" />
          <el-checkbox-group v-else v-model="serviceInstallSelection" class="service-install-options">
            <el-checkbox v-for="service in missingRuntimeServiceOptions" :key="service.value" :label="service.value">{{ service.label }}</el-checkbox>
          </el-checkbox-group>
        </div>
      </div>
      <template #footer>
        <el-button @click="serviceInstallVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="serviceInstallSubmitting" @click="submitAifarServiceInstall">{{ t('containers.installServices') }}</el-button>
      </template>
    </el-dialog>
    <el-dialog v-model="runtimeConfigVisible" :title="t('containers.runtimeConfig')" width="780px" destroy-on-close>
      <div class="runtime-config-dialog">
        <KeyValueGrid :items="runtimeConfigMetaItems" />
        <el-form label-width="148px" class="runtime-config-form">
          <el-form-item :label="t('containers.runtimeConfigCpu')" required>
            <el-input v-model="runtimeConfigForm.appCPUs" />
          </el-form-item>
          <el-form-item :label="t('containers.runtimeConfigMemory')" required>
            <el-input v-model="runtimeConfigForm.appMemoryLimit" />
          </el-form-item>
          <el-form-item :label="t('containers.jvmInitialRam')" required>
            <el-input-number v-model="runtimeConfigForm.jvmInitialRAMPercentage" :min="1" :max="90" :step="1" controls-position="right" />
          </el-form-item>
          <el-form-item :label="t('containers.jvmMaxRam')" required>
            <el-input-number v-model="runtimeConfigForm.jvmMaxRAMPercentage" :min="1" :max="90" :step="1" controls-position="right" />
          </el-form-item>
        </el-form>
        <div class="runtime-config-section-title">{{ t('containers.runtimeConfigOverrides') }}</div>
        <el-table :data="runtimeConfigRows" max-height="300" row-key="serviceName" class="runtime-config-table">
          <el-table-column prop="serviceName" :label="t('containers.service')" width="120" />
          <el-table-column :label="t('containers.runtimeConfigCpu')" min-width="130">
            <template #default="{ row }">
              <el-input v-model="row.appCPUs" :placeholder="t('containers.inheritGlobal')" />
            </template>
          </el-table-column>
          <el-table-column :label="t('containers.runtimeConfigMemory')" min-width="140">
            <template #default="{ row }">
              <el-input v-model="row.appMemoryLimit" :placeholder="t('containers.inheritGlobal')" />
            </template>
          </el-table-column>
          <el-table-column :label="t('containers.jvmInitialRam')" min-width="130">
            <template #default="{ row }">
              <el-input v-model="row.jvmInitialRAMPercentage" :disabled="row.serviceName === 'web-vue3'" :placeholder="t('containers.inheritGlobal')" />
            </template>
          </el-table-column>
          <el-table-column :label="t('containers.jvmMaxRam')" min-width="130">
            <template #default="{ row }">
              <el-input v-model="row.jvmMaxRAMPercentage" :disabled="row.serviceName === 'web-vue3'" :placeholder="t('containers.inheritGlobal')" />
            </template>
          </el-table-column>
        </el-table>
      </div>
      <template #footer>
        <el-button @click="runtimeConfigVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="runtimeConfigSubmitting" @click="submitRuntimeConfig">{{ t('containers.applyRuntimeConfig') }}</el-button>
      </template>
    </el-dialog>
    <SecretConfirmPrompt
      v-model="deletePromptVisible"
      :title="t('apps.uninstallService')"
      :message="deletePromptMessage"
      :placeholder="t('apps.deleteServicePasswordPlaceholder')"
      :confirm-text="t('common.uninstall')"
      :cancel-text="t('common.cancel')"
      :loading="deleteSubmitting"
      @confirm="confirmDockerUninstall"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, apiPostForm, apiPut, asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import LogDrawer from '../components/LogDrawer.vue'
import MetricGrid from '../components/MetricGrid.vue'
import SecretConfirmPrompt from '../components/SecretConfirmPrompt.vue'
import ServerSelector from '../components/ServerSelector.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { getCurrentLocale, useI18n } from '../i18n'
import { permissions } from '../rbac'

type DockerSummaryResponse = {
  available?: boolean
  error?: string
  summary?: Record<string, any>
  diskUsage?: Array<Record<string, string>>
}

type AppInstance = {
  id: string
  app: string
  serverId: string
  version?: string
  status: string
  metadata?: string
}

type AifarRuntimeInstance = {
  id: string
  version?: string
  status?: string
  orchestrationModel?: string
  legacy?: boolean
  installRoot?: string
  endpoint?: string
  gatewayEndpoint?: string
  runtimeConfig?: RuntimeConfigState
}

type RuntimeConfigValues = {
  appCPUs?: string
  appMemoryLimit?: string
  jvmInitialRAMPercentage?: number
  jvmMaxRAMPercentage?: number
}

type RuntimeConfigState = {
  configVersion?: number
  updatedAt?: string
  updatedBy?: string
  global?: RuntimeConfigValues
  services?: Record<string, RuntimeConfigValues>
  appliedVersion?: number
  lastAppliedAt?: string
  lastApplyStatus?: string
  lastApplyError?: string
}

type RuntimeConfigServiceRow = {
  serviceName: string
  appCPUs: string
  appMemoryLimit: string
  jvmInitialRAMPercentage: string
  jvmMaxRAMPercentage: string
}

type AifarRuntimeAgent = {
  status?: string
  version?: string
  mode?: string
  error?: string
  features?: string[]
}

type AifarRuntimeService = {
  instanceId: string
  serviceName: string
  proxyName?: string
  desiredReplicas?: number
  readyReplicas?: number
  activeEndpoints?: number
  currentRevision?: string
  image?: string
  status?: string
  cpuPercent?: number
  memoryPercent?: number
  failureReason?: string
}

type AifarRuntimePod = {
  instanceId: string
  serviceName: string
  podId?: string
  containerName: string
  revision?: string
  image?: string
  port?: number
  status?: string
  ready?: boolean
  cpuPercent?: number
  memoryPercent?: number
  memoryUsage?: string
}

type AifarRuntimeIngress = {
  instanceId: string
  container?: string
  status?: string
  gatewayPort?: number
  webPort?: number
  gatewayRoute?: string
  webRoute?: string
  error?: string
}

type AifarRuntimeResponse = {
  serverId?: string
  runtimeStatus?: string
  agent?: AifarRuntimeAgent
  instances?: AifarRuntimeInstance[]
  services?: AifarRuntimeService[]
  pods?: AifarRuntimePod[]
  ingress?: AifarRuntimeIngress[]
  warnings?: string[]
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const AIFAR_RUNTIME_MODEL = 'agent-runtime-v2'
const selectedServerId = ref('')
const servers = ref<any[]>([])
const appInstances = ref<AppInstance[]>([])
const appSettings = ref<{ maxRequestBodyBytes?: number }>({})
const summary = ref<DockerSummaryResponse>({})
const collection = ref<any[]>([])
const selectedContainerRows = ref<any[]>([])
const error = ref('')
const tab = ref('overview')
const logsVisible = ref(false)
const logsText = ref('')
const aifarUpdateVisible = ref(false)
const aifarUpdateSubmitting = ref(false)
const aifarUpdateContainer = ref<any | null>(null)
const aifarUpdateInstanceOverride = ref<AppInstance | null>(null)
const aifarUpdateTargetLabel = ref('')
const aifarUpdateMode = ref<'single' | 'bundle'>('single')
const aifarUpdateService = ref('oauth')
const aifarArtifactFile = ref<File | null>(null)
const showAifarRuntimeInfra = ref(false)
const aifarRuntime = ref<AifarRuntimeResponse>({ runtimeStatus: 'unknown', agent: { status: 'unknown' }, instances: [], services: [], pods: [], ingress: [], warnings: [] })
const selectedRuntimeInstanceId = ref('')
const runtimeConfigVisible = ref(false)
const runtimeConfigSubmitting = ref(false)
const runtimeConfigForm = ref<Required<RuntimeConfigValues>>({
  appCPUs: '2.0',
  appMemoryLimit: '2GB',
  jvmInitialRAMPercentage: 20,
  jvmMaxRAMPercentage: 70
})
const runtimeConfigRows = ref<RuntimeConfigServiceRow[]>([])
const serviceInstallVisible = ref(false)
const serviceInstallSubmitting = ref(false)
const serviceInstallSelection = ref<string[]>([])
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const canManageContainers = computed(() => can(permissions.containersManage))
const canManageApps = computed(() => can(permissions.appsManage))

const summaryData = computed(() => summary.value.summary ?? {})
const selectedServer = computed(() => servers.value.find((server) => server.id === selectedServerId.value) ?? null)
const selectedDockerInstance = computed(() => appInstances.value.find((item) => item.app === 'docker' && item.serverId === selectedServerId.value) ?? null)
const dockerServers = computed(() => servers.value.filter((server) => String(server.dockerHost ?? '').trim() !== ''))
const targetLabel = computed(() => selectedServer.value ? serverLabel(selectedServer.value) : t('containers.selectDockerHost'))
const errorTitle = computed(() => summary.value.available === false ? t('containers.notAvailable') : t('containers.checkFailed'))
const deletePromptMessage = computed(() => selectedServer.value ? t('apps.deleteServicePasswordPrompt', { server: serverLabel(selectedServer.value) }) : '')
const metrics = computed(() => [
  { label: t('containers.title'), value: summaryData.value.containers ?? 0, note: t('containers.runningCount', { count: summaryData.value.running ?? 0 }) },
  { label: t('containers.images'), value: summaryData.value.images ?? 0, note: t('containers.localImages') },
  { label: t('containers.network'), value: summaryData.value.networks ?? 0, note: t('containers.network') },
  { label: t('containers.volumes'), value: summaryData.value.volumes ?? 0, note: t('containers.volumes') },
  { label: t('containers.registry'), value: 0, note: t('containers.controlPlaneData') }
])
const configSummaryItems = computed(() => [
  { label: t('containers.dockerHost'), value: targetLabel.value },
  { label: t('containers.endpoint'), value: summaryData.value.endpoint || '-' },
  { label: t('containers.serverVersion'), value: summaryData.value.version || '-' },
  { label: t('containers.driver'), value: summaryData.value.driver || '-' },
  { label: t('containers.rootDir'), value: summaryData.value.rootDir || '-' },
  { label: t('common.status'), status: summary.value.available ? 'available' : 'failed' }
])
const settingsItems = computed(() => [
  { label: t('containers.dockerHost'), value: targetLabel.value },
  { label: t('containers.endpoint'), value: summaryData.value.endpoint || '-' },
  { label: t('containers.rootDir'), value: summaryData.value.rootDir || '-' },
  { label: t('common.provider'), value: t('common.real') }
])
const selectedContainerIds = computed(() => selectedContainerRows.value.filter(containerRowSelectable).map((row) => String(row?.id ?? '').trim()).filter(Boolean))
const selectedRunningContainers = computed(() => selectedContainerRows.value.filter((row) => containerRowSelectable(row) && isRunningContainer(row)))
const containerTableRows = computed(() => {
  if (showAifarRuntimeInfra.value) {
    return collection.value
  }
  return collection.value.filter((row) => !isAifarRuntimeInfraContainer(row))
})
const containerStateLabelKeys: Record<string, string> = {
  created: 'containers.state.created',
  restarting: 'containers.state.restarting',
  running: 'containers.state.running',
  removing: 'containers.state.removing',
  paused: 'containers.state.paused',
  exited: 'containers.state.exited',
  dead: 'containers.state.dead'
}
const containerActionLabelKeys: Record<string, string> = {
  start: 'containers.start',
  stop: 'containers.stop',
  restart: 'containers.restart',
  remove: 'containers.uninstall'
}
const singleContainerConfirmKeys: Record<string, string> = {
  start: 'containers.confirmStartContainer',
  stop: 'containers.confirmStopContainer',
  restart: 'containers.confirmRestartContainer'
}
const batchContainerConfirmKeys: Record<string, string> = {
  start: 'containers.confirmBatchStart',
  stop: 'containers.confirmBatchStop',
  restart: 'containers.confirmBatchRestart',
  remove: 'containers.confirmUninstallSelected'
}
const batchActionDisabledReason = computed(() => {
  if (!canManageContainers.value) return deniedText.value
  if (!selectedContainerIds.value.length) return t('containers.selectContainers')
  return ''
})
const batchActionDisabled = computed(() => Boolean(batchActionDisabledReason.value))
const batchRemoveDisabledReason = computed(() => {
  if (batchActionDisabledReason.value) return batchActionDisabledReason.value
  if (selectedRunningContainers.value.length) return t('containers.stopBeforeUninstall')
  return ''
})
const batchRemoveDisabled = computed(() => Boolean(batchRemoveDisabledReason.value))
const normalizedDiskUsage = computed(() => {
  const rows = asArray<Record<string, string>>(summary.value.diskUsage)
  if (rows.length) return rows
  return [
    { type: t('containers.images'), size: '0 B', reclaimable: '-' },
    { type: t('containers.title'), size: String(summaryData.value.containers ?? 0), reclaimable: '-' },
    { type: t('containers.volumes'), size: '0 B', reclaimable: '-' },
    { type: t('containers.buildCache'), size: '0 B', reclaimable: '-' }
  ]
})
const aifarServiceOptions = [
  'oauth',
  'permission',
  'system',
  'file',
  'message',
  'im',
  'contacts',
  'meeting',
  'gateway',
  'web-vue3'
].map((name) => ({ value: name, label: name }))
const selectedAifarUpdateInstance = computed(() => aifarUpdateInstanceOverride.value ?? aifarInstanceForContainer(aifarUpdateContainer.value))
const selectedAifarContainerLabel = computed(() => aifarUpdateTargetLabel.value || containerDisplayName(aifarUpdateContainer.value) || '-')
const selectedAifarInstanceLabel = computed(() => {
  const instance = selectedAifarUpdateInstance.value
  if (!instance) {
    return '-'
  }
  const server = servers.value.find((item) => item.id === instance.serverId)
  const serverText = server ? serverLabel(server) : instance.serverId
  return `${instance.app} / ${instance.version || '-'} / ${serverText}`
})
const aifarArtifactAccept = computed(() => {
  if (aifarUpdateMode.value === 'bundle') {
    return '.zip'
  }
  return aifarUpdateService.value === 'web-vue3' ? '.zip,.tar,.tgz,.tar.gz' : '.jar'
})
const aifarArtifactHint = computed(() => {
  if (aifarUpdateMode.value === 'bundle') {
    return t('apps.aifarUpdateBundleHint')
  }
  return aifarUpdateService.value === 'web-vue3' ? t('apps.aifarUpdateFrontendHint') : t('apps.aifarUpdateJarHint')
})
const aifarRuntimeInstances = computed(() => asArray<AifarRuntimeInstance>(aifarRuntime.value.instances))
const selectedRuntimeInstance = computed(() => {
  const current = aifarRuntimeInstances.value.find((instance) => instance.id === selectedRuntimeInstanceId.value)
  return current ?? aifarRuntimeInstances.value[0] ?? null
})
const selectedRuntimeConfig = computed(() => selectedRuntimeInstance.value?.runtimeConfig ?? defaultRuntimeConfigState())
const selectedRuntimeAppInstance = computed(() => {
  const runtimeInstance = selectedRuntimeInstance.value
  if (!runtimeInstance) return null
  return appInstances.value.find((instance) => instance.id === runtimeInstance.id) ?? {
    id: runtimeInstance.id,
    app: 'aifar',
    serverId: selectedServerId.value,
    version: runtimeInstance.version,
    status: runtimeInstance.status || 'installed',
    metadata: ''
  }
})
const selectedRuntimeServices = computed(() => asArray<AifarRuntimeService>(aifarRuntime.value.services).filter((item) => item.instanceId === selectedRuntimeInstance.value?.id))
const selectedRuntimePods = computed(() => asArray<AifarRuntimePod>(aifarRuntime.value.pods).filter((item) => item.instanceId === selectedRuntimeInstance.value?.id))
const staleRuntimePodCount = computed(() => selectedRuntimePods.value.filter((item) => String(item.status || '').trim() === 'stale').length)
const selectedRuntimeIngress = computed(() => asArray<AifarRuntimeIngress>(aifarRuntime.value.ingress).find((item) => item.instanceId === selectedRuntimeInstance.value?.id) ?? null)
const aifarRuntimeWarnings = computed(() => asArray<string>(aifarRuntime.value.warnings))
const installedRuntimeServiceNames = computed(() => new Set(selectedRuntimeServices.value.map((item) => item.serviceName).filter(Boolean)))
const installedRuntimeServiceNamesList = computed(() => aifarServiceOptions.map((item) => item.value).filter((service) => installedRuntimeServiceNames.value.has(service)))
const missingRuntimeServiceOptions = computed(() => aifarServiceOptions.filter((item) => !installedRuntimeServiceNames.value.has(item.value)))
const runtimeInstanceManageDisabledReason = computed(() => {
  if (!canManageApps.value) return deniedText.value
  if (!selectedRuntimeInstance.value) return t('containers.selectAifarInstance')
  if (selectedRuntimeInstance.value.legacy) return t('containers.legacyRuntimeDisabled')
  return ''
})
const aifarRuntimeActionDisabledReason = computed(() => {
  if (runtimeInstanceManageDisabledReason.value) return runtimeInstanceManageDisabledReason.value
  if (String(aifarRuntime.value.runtimeStatus || '').trim() !== 'ready') return t('containers.runtimeDegradedDisabled')
  if (String(aifarRuntime.value.agent?.status || '').trim() !== 'running') return t('containers.agentUnavailableDisabled')
  return ''
})
const runtimeCleanupDisabledReason = computed(() => {
  if (runtimeInstanceManageDisabledReason.value) return runtimeInstanceManageDisabledReason.value
  if (!staleRuntimePodCount.value) return t('containers.noStaleRuntimePods')
  return ''
})
const runtimeAgentUninstallDisabledReason = computed(() => runtimeInstanceManageDisabledReason.value)
const serviceInstallDisabledReason = computed(() => {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  if (!missingRuntimeServiceOptions.value.length) return t('containers.noMissingServices')
  return ''
})
const runtimeSummaryItems = computed(() => {
  const instance = selectedRuntimeInstance.value
  const ingress = selectedRuntimeIngress.value
  const config = selectedRuntimeConfig.value
  return [
    { label: t('containers.aifarInstance'), value: instance ? runtimeInstanceLabel(instance) : '-' },
    { label: t('containers.installRoot'), value: instance?.installRoot || '-' },
    { label: t('containers.webRoute'), value: ingress?.webRoute || instance?.endpoint || '-' },
    { label: t('containers.gatewayRoute'), value: ingress?.gatewayRoute || instance?.gatewayEndpoint || '-' },
    { label: t('containers.runtimeConfigVersion'), value: `${config.configVersion ?? '-'} / ${config.appliedVersion ?? '-'}`, status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.ingress'), value: ingress?.container || '-', status: ingress?.status || 'unknown' },
    { label: t('containers.agent'), value: aifarRuntime.value.agent?.version || aifarRuntime.value.agent?.status || '-', status: aifarRuntime.value.agent?.status || 'unknown' }
  ]
})
const runtimeConfigMetaItems = computed(() => {
  const config = selectedRuntimeConfig.value
  return [
    { label: t('containers.runtimeConfigDesiredVersion'), value: config.configVersion ?? '-' },
    { label: t('containers.runtimeConfigAppliedVersion'), value: config.appliedVersion ?? '-' },
    { label: t('containers.lastApplyStatus'), value: runtimeApplyStatusLabel(config.lastApplyStatus), status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.lastAppliedAt'), value: config.lastAppliedAt || '-' },
    { label: t('containers.lastApplyError'), value: config.lastApplyError || '-' }
  ]
})

function targetQuery() {
  if (selectedServerId.value) {
    return `serverId=${encodeURIComponent(selectedServerId.value)}`
  }
  return ''
}

function serverLabel(server: any) {
  if (!server) {
    return ''
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

async function loadServers() {
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  appInstances.value = asArray(await apiGet<AppInstance[] | null>('/apps/instances').catch(() => []))
  appSettings.value = await apiGet<{ maxRequestBodyBytes?: number }>('/settings').catch(() => ({}))
  if (!selectedServerId.value || !dockerServers.value.some((server) => server.id === selectedServerId.value)) {
    selectedServerId.value = dockerServers.value[0]?.id ?? ''
  }
}

async function load() {
  error.value = ''
  const query = targetQuery()
  if (!query) {
    summary.value = { available: false }
    collection.value = []
    selectedContainerRows.value = []
    return
  }
  summary.value = await apiGet<DockerSummaryResponse>(`/containers/summary?${query}`).catch((err) => {
    error.value = err.message
    return { available: false, error: err.message }
  })
  if (summary.value.available === false && summary.value.error) error.value = summary.value.error
  if (tab.value !== 'overview') {
    await loadCollection()
  }
}

async function loadCollection() {
  if (tab.value === 'aifar-runtime') {
    collection.value = []
    selectedContainerRows.value = []
    await loadAifarRuntime()
    return
  }
  if (tab.value === 'overview' || tab.value === 'registry' || tab.value === 'settings') {
    collection.value = []
    selectedContainerRows.value = []
    return
  }
  const query = targetQuery()
  if (!query) {
    collection.value = []
    selectedContainerRows.value = []
    return
  }
  selectedContainerRows.value = []
  collection.value = asArray(await apiGet(`/containers?kind=${tab.value}&${query}`).catch((err) => {
    error.value = err.message
    return []
  }))
}

async function loadAifarRuntime() {
  const query = targetQuery()
  if (!query) {
    aifarRuntime.value = { runtimeStatus: 'unknown', agent: { status: 'unknown' }, instances: [], services: [], pods: [], ingress: [], warnings: [] }
    selectedRuntimeInstanceId.value = ''
    return
  }
  aifarRuntime.value = await apiGet<AifarRuntimeResponse>(`/containers/aifar/runtime?${query}`).catch((err) => {
    error.value = err.message
    return { runtimeStatus: 'degraded', agent: { status: 'missing', error: err.message }, instances: [], services: [], pods: [], ingress: [], warnings: [err.message] }
  })
  const instances = asArray<AifarRuntimeInstance>(aifarRuntime.value.instances)
  if (!instances.some((instance) => instance.id === selectedRuntimeInstanceId.value)) {
    selectedRuntimeInstanceId.value = instances.find((instance) => !instance.legacy)?.id ?? instances[0]?.id ?? ''
  }
}

async function loadActive() {
  if (tab.value === 'overview') {
    await load()
  } else if (tab.value === 'aifar-runtime') {
    await loadAifarRuntime()
  } else {
    await loadCollection()
  }
}

async function runContainerAction(id: string, action: string) {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const row = collection.value.find((item) => String(item?.id ?? '').trim() === id)
  const confirmKey = singleContainerConfirmKeys[action]
  if (confirmKey) {
    const ok = await confirmContainerAction(action, t(confirmKey, { name: containerDisplayName(row) || id }))
    if (!ok) return
  }
  try {
    await apiPost(`/containers/${encodeURIComponent(id)}/${action}?${query}`)
    ElMessage.success(t('containers.actionAccepted'))
    setTimeout(loadCollection, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.actionFailed'))
  }
}

async function runContainerBatchAction(action: string, rows = selectedContainerRows.value) {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const selectedRows = rows.filter((row) => containerRowSelectable(row) && String(row?.id ?? '').trim())
  const ids = selectedRows.map((row) => String(row.id).trim())
  if (!ids.length) {
    ElMessage.warning(t('containers.selectContainers'))
    return
  }
  if (action === 'remove') {
    if (selectedRows.some(isRunningContainer)) {
      ElMessage.warning(t('containers.stopBeforeUninstall'))
      return
    }
  }
  const confirmKey = batchContainerConfirmKeys[action]
  if (confirmKey) {
    const ok = await confirmContainerAction(action, t(confirmKey, { count: ids.length }))
    if (!ok) return
  }
  try {
    await apiPost(`/containers/actions?${query}`, { action, ids })
    ElMessage.success(t('containers.batchActionAccepted'))
    selectedContainerRows.value = []
    setTimeout(loadCollection, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.actionFailed'))
  }
}

async function deleteImage(row: any) {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const id = imageReference(row)
  if (!id) {
    ElMessage.warning(t('containers.selectImage'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmDeleteImage', { image: id }), t('containers.deleteImage'), {
      type: 'warning',
      confirmButtonText: t('containers.deleteImage'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  try {
    await apiPost(`/containers/images/remove?${query}`, { id })
    ElMessage.success(t('containers.imageRemoveAccepted'))
    setTimeout(loadCollection, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.imageRemoveFailed'))
  }
}

async function openLogs(id: string) {
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const result = await apiGet<{ logs?: string[] }>(`/containers/${encodeURIComponent(id)}/logs?tail=300&${query}`)
  logsText.value = asArray<string>(result.logs).join('\n')
  logsVisible.value = true
}

function onContainerSelectionChange(rows: any[]) {
  selectedContainerRows.value = rows.filter(containerRowSelectable)
}

function containerState(row: any) {
  return String(row?.state ?? '').trim().toLowerCase()
}

function containerStatusDetail(row: any) {
  return String(row?.status || row?.state || '').trim()
}

function containerStatusKind(row: any) {
  const state = containerState(row)
  const detail = containerStatusDetail(row).toLowerCase()
  if (state === 'running' && detail.includes('(unhealthy)')) return 'failed'
  if (state === 'running' && (detail.includes('(health: starting)') || detail.includes('(starting)'))) return 'pending'
  switch (state) {
    case 'running':
      return 'running'
    case 'exited':
      return 'stopped'
    case 'created':
    case 'restarting':
    case 'removing':
      return 'pending'
    case 'paused':
      return 'degraded'
    case 'dead':
      return 'failed'
    default:
      return 'unknown'
  }
}

function containerStatusLabel(row: any) {
  const state = containerState(row)
  const detail = containerStatusDetail(row).toLowerCase()
  if (state === 'running' && detail.includes('(unhealthy)')) return t('containers.state.unhealthy')
  if (state === 'running' && (detail.includes('(health: starting)') || detail.includes('(starting)'))) return t('containers.state.healthStarting')
  const labelKey = containerStateLabelKeys[state]
  return labelKey ? t(labelKey) : String(row?.state || '').trim() || t('common.unknown')
}

function isRunningContainer(row: any) {
  return containerState(row) === 'running'
}

function containerDisplayName(row: any) {
  return String(row?.name || row?.id || '').trim()
}

function containerLabels(row: any): Record<string, string> {
  const labels = row?.labels
  if (!labels || typeof labels !== 'object' || Array.isArray(labels)) {
    return {}
  }
  return labels as Record<string, string>
}

function isAifarRuntimeInfraContainer(row: any) {
  const component = aifarComponentFromContainer(row)
  return component === 'service-proxy' || component === 'ingress'
}

function containerRowSelectable(row: any) {
  return !isAifarRuntimeInfraContainer(row)
}

function aifarServiceFromContainer(row: any) {
  const labels = containerLabels(row)
  const labelService = String(labels['aifar.service'] || '').trim()
  if (aifarServiceOptions.some((item) => item.value === labelService)) {
    return labelService
  }
  const name = containerDisplayName(row).toLowerCase()
  const k8sMatch = aifarServiceOptions.find((item) =>
    name.startsWith(`aifar-pod-admin-${item.value}-`) || name === `aifar-svc-admin-${item.value}`
  )
  if (k8sMatch) {
    return k8sMatch.value
  }
  const match = aifarServiceOptions.find((item) => name.startsWith(`aifar-${item.value}-`))
  return match?.value || ''
}

function aifarComponentFromContainer(row: any) {
  const labels = containerLabels(row)
  const component = String(labels['aifar.component'] || '').trim()
  if (component) {
    return component
  }
  const name = containerDisplayName(row).toLowerCase()
  if (name.startsWith('aifar-pod-admin-')) {
    return 'pod'
  }
  if (name.startsWith('aifar-svc-admin-')) {
    return 'service-proxy'
  }
  if (name === 'aifar-admin-ingress') {
    return 'ingress'
  }
  return ''
}

function aifarRuntimeStatusKind(status?: string) {
  switch (String(status || '').trim()) {
    case 'ready':
    case 'running':
    case 'active':
      return 'running'
    case 'starting':
    case 'rolling':
      return 'pending'
    case 'degraded':
    case 'draining':
    case 'stale':
      return 'degraded'
    case 'missing':
    case 'unsupported':
    case 'failed':
    case 'unhealthy':
    case 'no-endpoints':
      return 'failed'
    default:
      return 'unknown'
  }
}

function aifarRuntimeStatusLabel(status?: string) {
  const key = `containers.runtimeStatus.${String(status || 'unknown').trim() || 'unknown'}`
  const value = t(key)
  return value === key ? String(status || t('common.unknown')) : value
}

function runtimeApplyStatusLabel(status?: string) {
  const key = `containers.runtimeApplyStatus.${String(status || 'unknown').trim() || 'unknown'}`
  const value = t(key)
  return value === key ? String(status || t('common.unknown')) : value
}

function percentText(value?: number) {
  const n = Number(value || 0)
  if (!Number.isFinite(n) || n <= 0) {
    return '-'
  }
  return `${n.toFixed(1)}%`
}

function defaultRuntimeConfigState(): RuntimeConfigState {
  return {
    configVersion: 1,
    appliedVersion: 1,
    lastApplyStatus: 'applied',
    global: {
      appCPUs: '2.0',
      appMemoryLimit: '2GB',
      jvmInitialRAMPercentage: 20,
      jvmMaxRAMPercentage: 70
    },
    services: {}
  }
}

function normalizedRuntimeValues(values?: RuntimeConfigValues): Required<RuntimeConfigValues> {
  return {
    appCPUs: String(values?.appCPUs || '2.0').trim(),
    appMemoryLimit: String(values?.appMemoryLimit || '2GB').trim(),
    jvmInitialRAMPercentage: Number(values?.jvmInitialRAMPercentage || 20),
    jvmMaxRAMPercentage: Number(values?.jvmMaxRAMPercentage || 70)
  }
}

function runtimeConfigNumberText(value?: number) {
  if (value === undefined || value === null || Number(value) <= 0) {
    return ''
  }
  return String(value)
}

function runtimeInstanceLabel(instance: AifarRuntimeInstance) {
  const model = instance.orchestrationModel || t('common.unknown')
  const root = instance.installRoot || instance.id
  return `${instance.version || 'aifar'} / ${model} / ${root}`
}

function openRuntimeConfigDialog() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const state = selectedRuntimeConfig.value
  const global = normalizedRuntimeValues(state.global)
  runtimeConfigForm.value = { ...global }
  const overrides = state.services || {}
  const services = selectedRuntimeServices.value.length
    ? selectedRuntimeServices.value.map((item) => item.serviceName)
    : aifarServiceOptions.map((item) => item.value)
  runtimeConfigRows.value = services.map((serviceName) => {
    const values = overrides[serviceName] || {}
    return {
      serviceName,
      appCPUs: String(values.appCPUs || '').trim(),
      appMemoryLimit: String(values.appMemoryLimit || '').trim(),
      jvmInitialRAMPercentage: serviceName === 'web-vue3' ? '' : runtimeConfigNumberText(values.jvmInitialRAMPercentage),
      jvmMaxRAMPercentage: serviceName === 'web-vue3' ? '' : runtimeConfigNumberText(values.jvmMaxRAMPercentage)
    }
  })
  runtimeConfigVisible.value = true
}

function validateRuntimeConfigValues(values: Required<RuntimeConfigValues>) {
  const cpuPattern = /^[0-9]+(\.[0-9]+)?$/
  const memoryPattern = /^[1-9][0-9]*(b|k|m|g|kb|mb|gb|kib|mib|gib)?$/i
  if (!cpuPattern.test(values.appCPUs) || Number(values.appCPUs) <= 0) return t('containers.runtimeConfigCpuInvalid')
  if (!memoryPattern.test(values.appMemoryLimit)) return t('containers.runtimeConfigMemoryInvalid')
  if (!Number.isFinite(values.jvmInitialRAMPercentage) || !Number.isFinite(values.jvmMaxRAMPercentage)) return t('containers.runtimeConfigJvmInvalid')
  if (values.jvmInitialRAMPercentage <= 0 || values.jvmMaxRAMPercentage <= 0 || values.jvmMaxRAMPercentage > 90) return t('containers.runtimeConfigJvmInvalid')
  if (values.jvmInitialRAMPercentage > values.jvmMaxRAMPercentage) return t('containers.runtimeConfigJvmOrderInvalid')
  return ''
}

function optionalRuntimeNumber(value: string) {
  const text = String(value || '').trim()
  if (!text) return undefined
  const n = Number(text)
  return Number.isFinite(n) ? n : Number.NaN
}

function buildRuntimeServiceOverrides() {
  const services: Record<string, RuntimeConfigValues> = {}
  for (const row of runtimeConfigRows.value) {
    const values: RuntimeConfigValues = {}
    if (row.appCPUs.trim()) values.appCPUs = row.appCPUs.trim()
    if (row.appMemoryLimit.trim()) values.appMemoryLimit = row.appMemoryLimit.trim()
    if (row.serviceName !== 'web-vue3') {
      const initial = optionalRuntimeNumber(row.jvmInitialRAMPercentage)
      const max = optionalRuntimeNumber(row.jvmMaxRAMPercentage)
      if (initial !== undefined) {
        if (!Number.isFinite(initial)) throw new Error(`${row.serviceName}: ${t('containers.runtimeConfigJvmInvalid')}`)
        values.jvmInitialRAMPercentage = initial
      }
      if (max !== undefined) {
        if (!Number.isFinite(max)) throw new Error(`${row.serviceName}: ${t('containers.runtimeConfigJvmInvalid')}`)
        values.jvmMaxRAMPercentage = max
      }
    }
    if (Object.keys(values).length) {
      const effective = normalizedRuntimeValues({ ...runtimeConfigForm.value, ...values })
      const err = validateRuntimeConfigValues(effective)
      if (err) {
        throw new Error(`${row.serviceName}: ${err}`)
      }
      services[row.serviceName] = values
    }
  }
  return services
}

function isAifarUpdatableContainer(row: any) {
  const labels = containerLabels(row)
  if (String(labels['aifar.app'] || '').trim() === 'aifar' && aifarComponentFromContainer(row) === 'pod' && aifarServiceFromContainer(row)) {
    return true
  }
  const name = containerDisplayName(row).toLowerCase()
  return Boolean(aifarComponentFromContainer(row) === 'pod' && aifarServiceFromContainer(row) && name.startsWith('aifar-pod-admin-'))
}

function metadataOf(instance: AppInstance): Record<string, any> {
  if (!instance.metadata) {
    return {}
  }
  try {
    const parsed = JSON.parse(instance.metadata)
    return parsed && typeof parsed === 'object' ? parsed : {}
  } catch {
    return {}
  }
}

function aifarInstanceForContainer(row: any) {
  if (!row) {
    return null
  }
  const labels = containerLabels(row)
  const installRoot = normalizeInstallRoot(String(labels['aifar.install-root'] || ''))
  const candidates = appInstances.value.filter((item) => item.app === 'aifar' && item.serverId === selectedServerId.value)
  if (installRoot) {
    const exact = candidates.filter((item) => normalizeInstallRoot(String(metadataOf(item).installRoot || '')) === installRoot)
    const matched = exact.find((item) => String(metadataOf(item).orchestrationModel || '').trim() === AIFAR_RUNTIME_MODEL) ?? exact[0]
    if (matched) {
      return matched
    }
    return null
  }
  const k8sCandidates = candidates.filter((item) => String(metadataOf(item).orchestrationModel || '').trim() === AIFAR_RUNTIME_MODEL)
  if (k8sCandidates.length === 1) {
    return k8sCandidates[0]
  }
  return candidates.length === 1 ? candidates[0] : null
}

function normalizeInstallRoot(value: string) {
  let text = String(value || '').trim()
  while (text.length > 1 && text.endsWith('/')) {
    text = text.slice(0, -1)
  }
  return text
}

function aifarUpdateDisabledReason(row: any) {
  if (!canManageApps.value) return deniedText.value
  if (!aifarServiceFromContainer(row)) return t('containers.updateServiceUnknown')
  if (!aifarInstanceForContainer(row)) return t('containers.updateServiceInstanceMissing')
  return ''
}

function openAifarUpdate(row: any) {
  const reason = aifarUpdateDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  aifarUpdateContainer.value = row
  aifarUpdateInstanceOverride.value = null
  aifarUpdateTargetLabel.value = ''
  aifarUpdateMode.value = 'single'
  aifarUpdateService.value = aifarServiceFromContainer(row) || 'oauth'
  aifarArtifactFile.value = null
  aifarUpdateVisible.value = true
}

function openAifarRuntimeServiceUpdate(row: AifarRuntimeService) {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instance = selectedRuntimeAppInstance.value
  if (!instance) {
    ElMessage.warning(t('containers.updateServiceInstanceMissing'))
    return
  }
  aifarUpdateContainer.value = null
  aifarUpdateInstanceOverride.value = instance
  aifarUpdateTargetLabel.value = row.serviceName
  aifarUpdateMode.value = 'single'
  aifarUpdateService.value = row.serviceName || 'oauth'
  aifarArtifactFile.value = null
  aifarUpdateVisible.value = true
}

function openAifarRuntimeBundleUpdate() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instance = selectedRuntimeAppInstance.value
  if (!instance) {
    ElMessage.warning(t('containers.updateServiceInstanceMissing'))
    return
  }
  aifarUpdateContainer.value = null
  aifarUpdateInstanceOverride.value = instance
  aifarUpdateTargetLabel.value = t('containers.bundleUpdate')
  aifarUpdateMode.value = 'bundle'
  aifarArtifactFile.value = null
  aifarUpdateVisible.value = true
}

function handleAifarArtifactChange(file: UploadFile) {
  aifarArtifactFile.value = file.raw ?? null
}

function clearAifarArtifact() {
  aifarArtifactFile.value = null
}

async function submitAifarUpdate() {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const instance = selectedAifarUpdateInstance.value
  if (!instance || !aifarArtifactFile.value) {
    ElMessage.warning(t('apps.aifarUpdateArtifactRequired'))
    return
  }
  const uploadLimit = Number(appSettings.value.maxRequestBodyBytes || 0)
  if (uploadLimit > 0 && aifarArtifactFile.value.size > uploadLimit) {
    ElMessage.error(t('apps.aifarUpdateArtifactTooLarge', {
      size: formatBytes(aifarArtifactFile.value.size),
      limit: formatBytes(uploadLimit)
    }))
    return
  }
  const form = new FormData()
  form.append('language', getCurrentLocale())
  if (aifarUpdateMode.value === 'bundle') {
    form.append('bundle', aifarArtifactFile.value, aifarArtifactFile.value.name)
  } else {
    form.append('service', aifarUpdateService.value)
    form.append('artifact', aifarArtifactFile.value, aifarArtifactFile.value.name)
  }
  aifarUpdateSubmitting.value = true
  try {
    const endpoint = aifarUpdateMode.value === 'bundle'
      ? `/apps/instances/${instance.id}/aifar/update-artifact-bundle`
      : `/apps/instances/${instance.id}/aifar/update-artifact`
    const result = await apiPostForm<{ taskId: string }>(endpoint, form)
    aifarUpdateVisible.value = false
    aifarArtifactFile.value = null
    aifarUpdateInstanceOverride.value = null
    aifarUpdateTargetLabel.value = ''
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
    ElMessage.success(t('apps.aifarUpdateAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.aifarUpdateFailed'))
  } finally {
    aifarUpdateSubmitting.value = false
  }
}

async function submitRuntimeConfig() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  const global = normalizedRuntimeValues(runtimeConfigForm.value)
  const validation = validateRuntimeConfigValues(global)
  if (validation) {
    ElMessage.warning(validation)
    return
  }
  let services: Record<string, RuntimeConfigValues>
  try {
    services = buildRuntimeServiceOverrides()
  } catch (err) {
    ElMessage.warning(err instanceof Error ? err.message : t('containers.runtimeConfigInvalid'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmApplyRuntimeConfig'), t('containers.runtimeConfig'), {
      type: 'warning',
      confirmButtonText: t('containers.applyRuntimeConfig'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  runtimeConfigSubmitting.value = true
  try {
    const result = await apiPut<{ taskId: string }>(`/containers/aifar/runtime/config?${query}`, {
      instanceId,
      global,
      services
    })
    runtimeConfigVisible.value = false
    ElMessage.success(t('containers.runtimeConfigApplyStarted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
    void loadAifarRuntime()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  } finally {
    runtimeConfigSubmitting.value = false
  }
}

async function reconcileAifarRuntime() {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmReconcileRuntime'), t('containers.reconcileRuntime'), {
      type: 'warning',
      confirmButtonText: t('containers.reconcileRuntime'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  try {
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/runtime/reconcile?${query}`, { instanceId })
    ElMessage.success(t('containers.runtimeActionAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function cleanupAifarRuntimeStale() {
  const reason = runtimeCleanupDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmCleanupStaleRuntime', { count: staleRuntimePodCount.value }), t('containers.cleanupStaleRuntime'), {
      type: 'warning',
      confirmButtonText: t('containers.cleanupStaleRuntime'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  try {
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/runtime/cleanup-stale?${query}`, { instanceId })
    ElMessage.success(t('containers.runtimeActionAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
    void loadAifarRuntime()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function uninstallAifarRuntimeAgent() {
  const reason = runtimeAgentUninstallDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmUninstallAgent'), t('containers.uninstallAgent'), {
      type: 'error',
      confirmButtonText: t('containers.uninstallAgent'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  try {
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/runtime/uninstall-agent?${query}`, { instanceId })
    ElMessage.success(t('containers.runtimeActionAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
    void loadAifarRuntime()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

function openServiceInstallDialog() {
  const reason = serviceInstallDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  serviceInstallSelection.value = []
  serviceInstallVisible.value = true
}

async function submitAifarServiceInstall() {
  const reason = serviceInstallDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  const services = [...serviceInstallSelection.value]
  if (!services.length) {
    ElMessage.warning(t('containers.selectServicesToInstall'))
    return
  }
  try {
    await ElMessageBox.confirm(t('containers.confirmInstallServices', { services: services.join(', ') }), t('containers.installServices'), {
      type: 'warning',
      confirmButtonText: t('containers.installServices'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  serviceInstallSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/services/install?${query}`, {
      instanceId,
      services
    })
    serviceInstallVisible.value = false
    serviceInstallSelection.value = []
    ElMessage.success(t('containers.serviceInstallAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
    void loadAifarRuntime()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.serviceInstallFailed'))
  } finally {
    serviceInstallSubmitting.value = false
  }
}

async function scaleOutAifarService(service: string) {
  const reason = aifarRuntimeActionDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  await submitAifarScaleOut(service, instanceId, () => {
    void loadAifarRuntime()
  })
}

async function submitAifarScaleOut(service: string, instanceId: string, afterSubmitted?: () => void) {
  try {
    await ElMessageBox.confirm(t('containers.confirmScaleOut', { service }), t('containers.scaleOut'), {
      type: 'warning',
      confirmButtonText: t('containers.scaleOut'),
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  try {
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/services/${encodeURIComponent(service)}/scale-out?${query}`, { instanceId })
    ElMessage.success(t('containers.runtimeActionAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
    afterSubmitted?.()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return '0 B'
  }
  const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB']
  let size = value
  let idx = 0
  while (size >= 1024 && idx < units.length - 1) {
    size /= 1024
    idx++
  }
  return `${size.toFixed(idx === 0 ? 0 : 1)} ${units[idx]}`
}

async function confirmContainerAction(action: string, message: string) {
  const labelKey = containerActionLabelKeys[action]
  const label = labelKey ? t(labelKey) : action
  try {
    await ElMessageBox.confirm(message, label, {
      type: 'warning',
      confirmButtonText: label,
      cancelButtonText: t('common.cancel')
    })
    return true
  } catch {
    return false
  }
}

function imageReference(row: any) {
  const repository = String(row?.repository ?? '').trim()
  const tag = String(row?.tag ?? '').trim()
  const id = String(row?.id ?? '').trim()
  if (repository && repository !== '<none>' && tag && tag !== '<none>') {
    return `${repository}:${tag}`
  }
  return id
}

function containerRemoveDisabledReason(row: any) {
  if (!canManageContainers.value) return deniedText.value
  if (isRunningContainer(row)) return t('containers.stopBeforeUninstall')
  return ''
}

function openDockerUninstall() {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  deletePromptVisible.value = true
}

async function confirmDockerUninstall(password: string) {
  const instance = selectedDockerInstance.value
  if (!instance) {
    return
  }
  if (!password.trim()) {
    ElMessage.warning(t('apps.deleteServicePasswordPlaceholder'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${instance.id}/delete`, {
      serverPassword: password
    })
    deletePromptVisible.value = false
    ElMessage.success(t('apps.uninstallServiceAccepted'))
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    deleteSubmitting.value = false
  }
}

watch(tab, loadActive)
watch([aifarUpdateService, aifarUpdateMode], () => {
  aifarArtifactFile.value = null
})
watch(selectedServerId, load)
onMounted(async () => {
  await loadServers()
  await load()
})
</script>

<style scoped>
.containers-main {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  padding: 10px;
}

.sub-panel {
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  overflow: hidden;
}

.disk-grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(180px, 1fr));
  gap: 8px;
  padding: 12px;
}

.disk-grid div {
  min-height: 64px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f7fbff;
  display: grid;
  place-items: center;
  color: var(--aifar-text-secondary);
}

.disk-grid strong {
  color: var(--aifar-ink);
}

.disk-grid small {
  font-size: 11px;
  color: var(--aifar-text-tertiary);
}

.row-actions {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
}

.table-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 40px;
  padding: 0 2px 8px;
}

.selection-summary {
  color: var(--aifar-text-secondary);
  font-size: 13px;
  white-space: nowrap;
}

.toolbar-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
}

.runtime-infra-switch {
  margin-right: 4px;
}

.container-table-body {
  flex: 1 1 auto;
  min-height: 0;
}

.runtime-workspace {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  flex-direction: column;
  gap: 10px;
}

.runtime-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  min-height: 40px;
}

.runtime-status {
  display: flex;
  align-items: center;
  gap: 8px;
  color: var(--aifar-text-secondary);
  font-size: 13px;
}

.runtime-instance-select {
  width: 280px;
}

.aifar-update-form :deep(.el-select),
.aifar-update-form :deep(.el-input) {
  width: 100%;
}

.artifact-hint {
  width: 100%;
  margin-top: 6px;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 18px;
}

.runtime-config-dialog {
  display: grid;
  gap: 12px;
}

.runtime-config-form {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 0 12px;
}

.runtime-config-form :deep(.el-form-item) {
  margin-bottom: 12px;
}

.runtime-config-form :deep(.el-input),
.runtime-config-form :deep(.el-input-number) {
  width: 100%;
}

.runtime-config-section-title {
  color: var(--aifar-ink);
  font-size: 14px;
  font-weight: 600;
}

.runtime-config-table :deep(.el-input) {
  width: 100%;
}

.service-install-dialog {
  display: grid;
  gap: 16px;
}

.service-tag-list,
.service-install-options {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 8px;
}

.service-install-options :deep(.el-checkbox) {
  margin-right: 0;
}

.settings-grid {
  display: grid;
  gap: 12px;
}

@media (max-width: 1100px) {
  .disk-grid {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
}

@media (max-width: 640px) {
  .disk-grid {
    grid-template-columns: 1fr;
  }

  .table-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .toolbar-actions {
    justify-content: flex-start;
  }

  .runtime-toolbar {
    align-items: flex-start;
    flex-direction: column;
  }

  .runtime-instance-select {
    width: 100%;
  }

  .runtime-config-form {
    grid-template-columns: 1fr;
  }
}
</style>
