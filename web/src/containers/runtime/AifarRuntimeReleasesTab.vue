<template>
  <div class="runtime-resource-panel">
    <div class="runtime-tab-toolbar">
      <span class="selection-summary">{{ t('containers.releaseCount', { count: aifarReleases.length }) }}</span>
      <div class="runtime-tab-actions">
        <el-button size="small" :loading="loading" @click="loadAifarReleases(true)">{{ t('common.refresh') }}</el-button>
      </div>
    </div>
    <el-table :data="aifarReleases" height="calc(100% - 44px)" row-key="releaseId">
      <el-table-column prop="releaseId" :label="t('containers.releaseId')" min-width="240" show-overflow-tooltip />
      <el-table-column :label="t('containers.releaseKind')" width="130">
        <template #default="{ row }">{{ releaseKindLabel(row.kind) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.status')" width="120">
        <template #default="{ row }">
          <StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="releaseStatusLabel(row.status)" />
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.service')" min-width="150" show-overflow-tooltip>
        <template #default="{ row }">{{ releaseServicesText(row) }}</template>
      </el-table-column>
      <el-table-column prop="activatedAt" :label="t('containers.activatedAt')" min-width="170" show-overflow-tooltip>
        <template #default="{ row }">{{ releaseActivatedAtText(row) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.operation')" width="120" fixed="right">
        <template #default="{ row }">
          <el-tooltip :content="releaseRollbackDisabledReason(row)" :disabled="!releaseRollbackDisabledReason(row)" placement="top">
            <span>
              <el-button size="small" type="warning" plain :disabled="Boolean(releaseRollbackDisabledReason(row))" @click="rollbackAifarRelease(row)">
                {{ t('containers.rollbackRelease') }}
              </el-button>
            </span>
          </el-tooltip>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import { useAifarRuntimeContext } from './context'
import { releaseActivatedAtText } from './format'

const {
  t,
  loading,
  aifarReleases,
  loadAifarReleases,
  releaseKindLabel,
  aifarRuntimeStatusKind,
  releaseStatusLabel,
  releaseServicesText,
  releaseRollbackDisabledReason,
  rollbackAifarRelease
} = useAifarRuntimeContext()
</script>
