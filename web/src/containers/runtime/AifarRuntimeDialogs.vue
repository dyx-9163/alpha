<template>
  <el-dialog v-model="aifarUpdateVisible" :title="t('apps.aifarUpdateTitle')" width="min(560px, calc(100vw - 32px))" destroy-on-close>
    <el-form label-width="112px" class="aifar-update-form">
      <el-form-item :label="t('containers.updateTarget')">
        <el-input :model-value="selectedAifarContainerLabel" disabled />
      </el-form-item>
      <el-form-item :label="t('apps.aifarUpdateInstance')">
        <el-input :model-value="selectedAifarInstanceLabel" disabled />
      </el-form-item>
      <el-form-item :label="t('apps.aifarUpdateMode')" required>
        <el-input :model-value="aifarUpdateModeLabel" disabled />
      </el-form-item>
      <el-form-item v-if="aifarUpdateMode === 'single'" :label="t('apps.aifarUpdateService')" required>
        <el-input :model-value="aifarUpdateService" disabled />
        <div class="artifact-hint">{{ t('apps.aifarUpdateLockedServiceHint') }}</div>
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
      <el-button @click="closeAifarUpdate">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="aifarUpdateSubmitting" @click="submitAifarUpdate">{{ t('apps.aifarUpdateSubmit') }}</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="serviceInstallVisible" :title="t('containers.installServicesDialog')" width="min(560px, calc(100vw - 32px))" destroy-on-close>
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
      <el-button @click="closeServiceInstall">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="serviceInstallSubmitting" @click="submitAifarServiceInstall">{{ t('containers.installServices') }}</el-button>
    </template>
  </el-dialog>

  <el-dialog v-model="runtimeConfigVisible" :title="t('containers.runtimeConfig')" width="min(780px, calc(100vw - 32px))" destroy-on-close>
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
        <el-form-item :label="t('containers.nacosEphemeral')">
          <el-switch v-model="runtimeConfigForm.nacosEphemeral" inline-prompt active-text="true" inactive-text="false" />
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
      <el-button @click="closeRuntimeConfig">{{ t('common.cancel') }}</el-button>
      <el-button type="primary" :loading="runtimeConfigSubmitting" @click="submitRuntimeConfig">{{ t('containers.applyRuntimeConfig') }}</el-button>
    </template>
  </el-dialog>
</template>

<script setup lang="ts">
import KeyValueGrid from '../../components/KeyValueGrid.vue'
import { useAifarRuntimeDialogContext } from './context'

const {
  t,
  aifarUpdateVisible,
  selectedAifarContainerLabel,
  selectedAifarInstanceLabel,
  aifarUpdateModeLabel,
  aifarUpdateMode,
  aifarUpdateService,
  aifarArtifactAccept,
  handleAifarArtifactChange,
  clearAifarArtifact,
  aifarArtifactHint,
  aifarUpdateSubmitting,
  submitAifarUpdate,
  serviceInstallVisible,
  installedRuntimeServiceNamesList,
  missingRuntimeServiceOptions,
  serviceInstallSelection,
  serviceInstallSubmitting,
  submitAifarServiceInstall,
  runtimeConfigVisible,
  runtimeConfigMetaItems,
  runtimeConfigForm,
  runtimeConfigRows,
  runtimeConfigSubmitting,
  submitRuntimeConfig
} = useAifarRuntimeDialogContext()

function closeAifarUpdate() {
  aifarUpdateVisible.value = false
}

function closeServiceInstall() {
  serviceInstallVisible.value = false
}

function closeRuntimeConfig() {
  runtimeConfigVisible.value = false
}
</script>

<style scoped>
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

@media (max-width: 640px) {
  .runtime-config-form {
    grid-template-columns: 1fr;
  }
}
</style>
