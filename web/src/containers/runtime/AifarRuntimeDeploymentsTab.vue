<template>
  <div class="runtime-resource-panel">
    <el-table :data="selectedRuntimeDeployments" height="100%" row-key="serviceName">
      <el-table-column prop="deploymentName" :label="t('containers.deployment')" min-width="170" show-overflow-tooltip />
      <el-table-column prop="serviceName" :label="t('containers.service')" width="130" show-overflow-tooltip />
      <el-table-column prop="status" :label="t('common.status')" width="120">
        <template #default="{ row }">
          <StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="aifarRuntimeStatusLabel(row.status)" />
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.replicas')" width="130">
        <template #default="{ row }">{{ runtimeDeploymentReplicaText(row) }}</template>
      </el-table-column>
      <el-table-column :label="t('containers.rollout')" width="120">
        <template #default="{ row }">
          <el-tooltip :content="row.failureReason" :disabled="!row.failureReason" placement="top">
            <span><StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="aifarRuntimeStatusLabel(row.status)" /></span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="podRevision" :label="t('containers.revision')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="image" :label="t('containers.image')" min-width="240" show-overflow-tooltip />
      <el-table-column :label="t('common.operation')" width="390" fixed="right">
        <template #default="{ row }">
          <div class="row-actions">
            <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
              <span><el-button size="small" type="primary" plain :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="openAifarRuntimeServiceUpdate(runtimeServiceForDeployment(row))">{{ t('containers.updateService') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="aifarRuntimeActionDisabledReason" :disabled="!aifarRuntimeActionDisabledReason" placement="top">
              <span><el-button size="small" :disabled="Boolean(aifarRuntimeActionDisabledReason)" @click="scaleOutAifarService(row.serviceName)">{{ t('containers.scaleOut') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="aifarRuntimeScaleInDisabledReason(row)" :disabled="!aifarRuntimeScaleInDisabledReason(row)" placement="top">
              <span><el-button size="small" plain :disabled="Boolean(aifarRuntimeScaleInDisabledReason(row))" @click="scaleInAifarDeployment(row)">{{ t('containers.scaleIn') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row))" :disabled="!aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row))" placement="top">
              <span><el-button size="small" type="danger" plain :disabled="Boolean(aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row)))" @click="offlineAifarService(runtimeServiceForDeployment(row))">{{ t('containers.offlineDeployment') }}</el-button></span>
            </el-tooltip>
          </div>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import { useAifarRuntimeContext } from './context'

const {
  t,
  selectedRuntimeDeployments,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  runtimeDeploymentReplicaText,
  aifarRuntimeActionDisabledReason,
  openAifarRuntimeServiceUpdate,
  runtimeServiceForDeployment,
  scaleOutAifarService,
  aifarRuntimeScaleInDisabledReason,
  scaleInAifarDeployment,
  aifarRuntimeOfflineDisabledReason,
  offlineAifarService
} = useAifarRuntimeContext()
</script>
