<template>
  <div class="runtime-resource-panel">
    <div class="runtime-tab-toolbar">
      <span class="selection-summary">{{ t('containers.releaseCount', { count: aifarReleases.length }) }}</span>
      <div class="runtime-tab-actions">
        <el-button size="small" :loading="loading" @click="loadAifarReleases(true)">{{ t('common.refresh') }}</el-button>
      </div>
    </div>
    <el-table :data="aifarReleases" height="calc(100% - 44px)" row-key="releaseId">
      <el-table-column :label="t('containers.releaseId')" min-width="240" show-overflow-tooltip>
        <template #default="{ row }">{{ row.releaseId }}</template>
      </el-table-column>
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
      <el-table-column :label="t('containers.releaseCurrentScopeLabel')" min-width="180">
        <template #default="{ row }">
          <div v-if="runtimeReleaseScope(row).currentServices.length" class="release-scope-cell">
            <el-tag
              data-testid="release-current-scope"
              size="small"
              type="info"
              :title="t('containers.releaseCurrentServices', { services: releaseCurrentServicesText(row) })"
              :aria-label="t('containers.releaseCurrentServices', { services: releaseCurrentServicesText(row) })"
            >
              {{ t('containers.releaseCurrentScope', {
                current: runtimeReleaseScope(row).currentServices.length,
                total: runtimeReleaseScope(row).totalServices.length
              }) }}
            </el-tag>
            <span
              class="release-scope-services"
              data-testid="release-current-service-list"
              :title="releaseCurrentServicesText(row)"
            >
              {{ releaseCurrentServicesText(row) }}
            </span>
          </div>
          <span v-else>—</span>
        </template>
      </el-table-column>
      <el-table-column prop="activatedAt" :label="t('containers.activatedAt')" min-width="170" show-overflow-tooltip>
        <template #default="{ row }">{{ releaseActivatedAtText(row) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.operation')" width="290" fixed="right">
        <template #default="{ row }">
          <div class="release-action-cell">
            <div class="release-action-buttons">
              <el-tooltip :content="releaseRollbackDisabledReason(row)" :disabled="!releaseRollbackDisabledReason(row)" placement="top">
                <span>
                  <el-button
                    size="small"
                    type="warning"
                    plain
                    :disabled="Boolean(releaseRollbackDisabledReason(row))"
                    :aria-describedby="releaseRollbackDisabledReason(row) ? `release-rollback-reason-${row.releaseId}` : undefined"
                    @click="rollbackAifarRelease(row)"
                  >
                    {{ t('containers.rollbackRelease') }}
                  </el-button>
                </span>
              </el-tooltip>
              <el-tooltip :content="releaseDeleteDisabledReason(row)" :disabled="!releaseDeleteDisabledReason(row)" placement="top">
                <span>
                  <el-button
                    size="small"
                    type="danger"
                    plain
                    :loading="releaseDeletingId === row.releaseId"
                    :disabled="Boolean(releaseDeleteDisabledReason(row))"
                    :aria-describedby="releaseDeleteDisabledReason(row) ? `release-delete-reason-${row.releaseId}` : undefined"
                    @click="deleteAifarRelease(row)"
                  >
                    {{ t('containers.deleteRelease') }}
                  </el-button>
                </span>
              </el-tooltip>
            </div>
            <span
              v-if="releaseRollbackDisabledReason(row)"
              :id="`release-rollback-reason-${row.releaseId}`"
              class="release-action-reason"
              data-testid="release-rollback-disabled-reason"
            >
              {{ releaseRollbackDisabledReason(row) }}
            </span>
            <span
              v-if="releaseDeleteDisabledReason(row)"
              :id="`release-delete-reason-${row.releaseId}`"
              class="release-action-reason"
              data-testid="release-delete-disabled-reason"
            >
              {{ releaseDeleteDisabledReason(row) }}
            </span>
          </div>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import { useAifarRuntimeContext } from './context'
import { releaseActivatedAtText } from './format'
import { runtimeReleaseScope } from './releaseRules'

const {
  t,
  loading,
  aifarReleases,
  loadAifarReleases,
  releaseKindLabel,
  aifarRuntimeStatusKind,
  releaseStatusLabel,
  releaseServicesText,
  releaseCurrentServicesText,
  releaseRollbackDisabledReason,
  rollbackAifarRelease,
  releaseDeletingId,
  releaseDeleteDisabledReason,
  deleteAifarRelease
} = useAifarRuntimeContext()
</script>
