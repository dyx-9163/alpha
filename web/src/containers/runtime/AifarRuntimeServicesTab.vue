<template>
  <div class="runtime-resource-panel">
    <el-table :data="selectedRuntimeServices" height="100%" row-key="serviceName">
      <el-table-column prop="serviceName" :label="t('containers.service')" min-width="130" show-overflow-tooltip />
      <el-table-column prop="appName" :label="t('containers.appName')" min-width="170" show-overflow-tooltip />
      <el-table-column prop="status" :label="t('common.status')" width="120">
        <template #default="{ row }">
          <StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="aifarRuntimeStatusLabel(row.status)" />
        </template>
      </el-table-column>
      <el-table-column :label="t('containers.endpoint')" width="110">
        <template #default="{ row }">{{ runtimeEndpointText(row) }}</template>
      </el-table-column>
      <el-table-column prop="proxyName" :label="t('containers.proxy')" min-width="170" show-overflow-tooltip />
      <el-table-column prop="image" :label="t('containers.image')" min-width="240" show-overflow-tooltip />
    </el-table>
  </div>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import { useAifarRuntimeContext } from './context'

const {
  t,
  selectedRuntimeServices,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  runtimeEndpointText
} = useAifarRuntimeContext()
</script>
