<template>
  <div class="runtime-resource-panel">
    <div class="runtime-tab-toolbar">
      <span class="selection-summary">{{ t('containers.selectedDeploymentCount', { count: selectedDeployments.length }) }}</span>
      <div class="runtime-tab-actions">
        <el-button
          size="small"
          type="danger"
          plain
          :disabled="selectedDeployments.length === 0"
          @click="batchOfflineDeployments"
        >{{ t('containers.batchOfflineDeployments') }}</el-button>
      </div>
    </div>
    <el-table
      ref="deploymentTable"
      :data="selectedRuntimeDeployments"
      :aria-label="t('containers.deployments')"
      height="100%"
      row-key="serviceName"
      @selection-change="handleDeploymentSelection"
    >
      <el-table-column type="selection" width="44" :selectable="deploymentSelectable" reserve-selection />
      <el-table-column prop="deploymentName" :label="t('containers.deployment')" min-width="170" show-overflow-tooltip />
      <el-table-column prop="serviceName" :label="t('containers.service')" width="130" show-overflow-tooltip />
      <el-table-column :label="t('common.status')" width="120">
        <template #default="{ row }">
          <StatusTag :status="aifarRuntimeStatusKind(runtimeDeploymentPhase(row))" :label="t(`containers.runtimePhase.${runtimeDeploymentPhase(row)}`)" />
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.replicas')" width="150">
        <template #default="{ row }">
          <span :title="t('containers.runtimeReplicaDetail', {
            desired: row.desiredReplicas ?? 0,
            current: row.currentReplicas ?? 0,
            ready: row.readyReplicas ?? 0
          })">{{ runtimeDeploymentReplicaText(row) }}</span>
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.generationObserved')" width="130">
        <template #default="{ row }">{{ runtimeGenerationText(row) }}</template>
      </el-table-column>
      <el-table-column :label="t('containers.condition')" min-width="190">
        <template #default="{ row }">
          <div v-if="runtimeConditionReason(row, t)" class="runtime-condition-cell">
            <StatusTag :status="aifarRuntimeStatusKind(runtimeDeploymentPhase(row))" :label="runtimeConditionReason(row, t)?.type || t('containers.runtimePhase.unknown')" />
            <strong>{{ runtimeConditionReason(row, t)?.reason }}</strong>
            <span v-if="runtimeConditionReason(row, t)?.message" :title="runtimeConditionReason(row, t)?.message">{{ runtimeConditionReason(row, t)?.message }}</span>
            <span v-if="runtimeConditionReason(row, t)?.advice" class="runtime-condition-advice">
              <strong>{{ runtimeConditionReason(row, t)?.advice?.group }}</strong>
              {{ runtimeConditionReason(row, t)?.advice?.suggestion }}
            </span>
          </div>
          <div v-else class="runtime-condition-empty">
            <span>{{ t('containers.conditionUnavailable') }}</span>
            <span v-if="row.failureReason" :title="row.failureReason">{{ row.failureReason }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.lastTransition')" min-width="170">
        <template #default="{ row }">{{ formatDate(runtimeConditionReason(row, t)?.lastTransitionTime || row.lastTransitionAt) }}</template>
      </el-table-column>
      <el-table-column prop="podRevision" :label="t('containers.revision')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="image" :label="t('containers.image')" min-width="240" show-overflow-tooltip />
      <el-table-column :label="t('common.operation')" width="480" fixed="right">
        <template #default="{ row }">
          <div class="row-actions">
            <el-tooltip :content="runtimeServiceActionDisabledReason(row)" :disabled="!runtimeServiceActionDisabledReason(row)" placement="top">
              <span><el-button size="small" type="primary" plain :aria-label="`${t('containers.updateService')} ${row.serviceName}`" :disabled="Boolean(runtimeServiceActionDisabledReason(row))" @click="openAifarRuntimeServiceUpdate(runtimeServiceForDeployment(row))">{{ t('containers.updateService') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="runtimeServiceActionDisabledReason(row)" :disabled="!runtimeServiceActionDisabledReason(row)" placement="top">
              <span><el-button size="small" :aria-label="`${t('containers.scaleOut')} ${row.serviceName}`" :disabled="Boolean(runtimeServiceActionDisabledReason(row))" @click="scaleOutAifarService(row.serviceName)">{{ t('containers.scaleOut') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="aifarRuntimeScaleInDisabledReason(row)" :disabled="!aifarRuntimeScaleInDisabledReason(row)" placement="top">
              <span><el-button size="small" plain :aria-label="`${t('containers.scaleIn')} ${row.serviceName}`" :disabled="Boolean(aifarRuntimeScaleInDisabledReason(row))" @click="scaleInAifarDeployment(row)">{{ t('containers.scaleIn') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row))" :disabled="!aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row))" placement="top">
              <span><el-button size="small" type="danger" plain :aria-label="`${t('containers.offlineDeployment')} ${row.serviceName}`" :disabled="Boolean(aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row)))" @click="offlineAifarService(runtimeServiceForDeployment(row))">{{ t('containers.offlineDeployment') }}</el-button></span>
            </el-tooltip>
            <el-tooltip :content="runtimeServiceActionDisabledReason(row)" :disabled="!runtimeServiceActionDisabledReason(row)" placement="top">
              <span><el-button size="small" plain :aria-label="`${t('containers.reconcileService')} ${row.serviceName}`" :disabled="Boolean(runtimeServiceActionDisabledReason(row))" @click="reconcileAifarDeployment(row)">{{ t('containers.reconcileService') }}</el-button></span>
            </el-tooltip>
          </div>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import StatusTag from '../../components/StatusTag.vue'
import { useAifarRuntimeContext } from './context'
import { formatDate, runtimeConditionReason, runtimeGenerationText } from './format'
import { normalizeBatchOfflineDeployments } from './runtimeDeploymentSelection'
import { runtimeDeploymentPhase } from './selectors'
import type { AifarRuntimeDeployment, AifarRuntimeService } from './types'

const {
  t,
  selectedRuntimeDeployments,
  aifarRuntimeStatusKind,
  runtimeDeploymentReplicaText,
  runtimeServiceActionDisabledReason,
  openAifarRuntimeServiceUpdate,
  runtimeServiceForDeployment,
  scaleOutAifarService,
  aifarRuntimeScaleInDisabledReason,
  scaleInAifarDeployment,
  reconcileAifarDeployment,
  aifarRuntimeOfflineDisabledReason,
  offlineAifarService,
  offlineAifarServices,
  selectedRuntimeInstanceId
} = useAifarRuntimeContext()

const deploymentTable = ref<{ clearSelection: () => void } | null>(null)
const selectedDeployments = ref<AifarRuntimeService[]>([])

function deploymentSelectable(row: AifarRuntimeDeployment) {
  return !aifarRuntimeOfflineDisabledReason(runtimeServiceForDeployment(row))
}

function handleDeploymentSelection(rows: AifarRuntimeDeployment[]) {
  selectedDeployments.value = normalizeBatchOfflineDeployments(
    rows,
    runtimeServiceForDeployment,
    aifarRuntimeOfflineDisabledReason
  )
}

function clearDeploymentSelection() {
  selectedDeployments.value = []
  deploymentTable.value?.clearSelection()
}

async function batchOfflineDeployments() {
  const rows = [...selectedDeployments.value]
  if (!rows.length) return
  if (await offlineAifarServices(rows)) clearDeploymentSelection()
}

watch(selectedRuntimeInstanceId, clearDeploymentSelection)
</script>
