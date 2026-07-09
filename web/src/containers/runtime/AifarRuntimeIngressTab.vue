<template>
  <div class="runtime-resource-panel">
    <div class="runtime-entry-grid">
      <div v-for="route in runtimeEntryRoutes" :key="route.name" class="runtime-entry-card">
        <div class="runtime-entry-card-head">
          <strong>{{ route.name }}</strong>
          <StatusTag :status="aifarRuntimeStatusKind(route.status)" :label="aifarRuntimeStatusLabel(route.status)" />
        </div>
        <span>{{ route.route }}</span>
        <small>{{ route.port }}</small>
      </div>
    </div>
    <el-table :data="selectedRuntimeServices" height="calc(100% - 104px)" row-key="serviceName">
      <el-table-column prop="serviceName" :label="t('containers.service')" min-width="130" />
      <el-table-column prop="appName" :label="t('containers.appName')" min-width="170" show-overflow-tooltip />
      <el-table-column :label="t('containers.discoveryTarget')" min-width="180" show-overflow-tooltip>
        <template #default="{ row }">{{ runtimeDiscoveryTarget(row) }}</template>
      </el-table-column>
      <el-table-column :label="t('containers.endpoint')" width="110">
        <template #default="{ row }">{{ runtimeEndpointText(row) }}</template>
      </el-table-column>
      <el-table-column label="Nacos" width="120">
        <template #default="{ row }">
          <el-tooltip :content="row.lastNacosError" :disabled="!row.lastNacosError" placement="top">
            <span><StatusTag :status="aifarRuntimeStatusKind(runtimeNacosStatus(row))" :label="aifarRuntimeStatusLabel(runtimeNacosStatus(row))" /></span>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column prop="lastNacosError" :label="t('containers.lastApplyError')" min-width="260" show-overflow-tooltip />
    </el-table>
  </div>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import { useAifarRuntimeContext } from './context'

const {
  t,
  runtimeEntryRoutes,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  selectedRuntimeServices,
  runtimeDiscoveryTarget,
  runtimeEndpointText,
  runtimeNacosStatus
} = useAifarRuntimeContext()
</script>
