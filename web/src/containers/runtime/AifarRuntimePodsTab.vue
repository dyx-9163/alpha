<template>
  <div class="runtime-resource-panel">
    <div class="runtime-tab-toolbar">
      <el-select v-model="runtimePodServiceFilter" size="small" clearable class="runtime-service-filter" :placeholder="t('containers.service')" @clear="clearRuntimePodServiceFilter">
        <el-option v-for="service in installedRuntimeServiceNamesList" :key="service" :label="service" :value="service" />
      </el-select>
      <div class="runtime-tab-actions">
        <el-button size="small" :loading="loading" @click="ensureRuntimePodsLoaded(true)">{{ t('common.refresh') }}</el-button>
        <el-button size="small" plain :loading="loading" @click="ensureRuntimePodsLoaded(true, true)">{{ t('containers.refreshPodStats') }}</el-button>
      </div>
    </div>
    <div v-if="!runtimePodsLoadedForCurrentScope" class="runtime-lazy-state">
      <el-button size="small" type="primary" plain :loading="loading" @click="ensureRuntimePodsLoaded(true)">{{ t('containers.loadPods') }}</el-button>
    </div>
    <el-table v-else :data="selectedRuntimePods" height="100%" row-key="containerName">
      <el-table-column prop="containerName" :label="t('containers.name')" min-width="260" show-overflow-tooltip />
      <el-table-column prop="serviceName" :label="t('containers.service')" width="120" show-overflow-tooltip />
      <el-table-column prop="status" :label="t('common.status')" width="120">
        <template #default="{ row }">
          <StatusTag :status="aifarRuntimeStatusKind(row.status)" :label="aifarRuntimeStatusLabel(row.status)" />
        </template>
      </el-table-column>
      <el-table-column prop="revision" :label="t('containers.revision')" min-width="180" show-overflow-tooltip />
      <el-table-column prop="image" :label="t('containers.image')" min-width="220" show-overflow-tooltip />
      <el-table-column :label="t('containers.cpu')" width="90">
        <template #default="{ row }">{{ percentText(row.cpuPercent) }}</template>
      </el-table-column>
      <el-table-column :label="t('containers.memory')" width="120">
        <template #default="{ row }">{{ row.memoryUsage || percentText(row.memoryPercent) }}</template>
      </el-table-column>
      <el-table-column :label="t('common.operation')" width="90" fixed="right">
        <template #default="{ row }">
          <el-button size="small" @click="openRuntimePodLogs(row)">{{ t('containers.logs') }}</el-button>
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
  loading,
  runtimePodServiceFilter,
  clearRuntimePodServiceFilter,
  installedRuntimeServiceNamesList,
  ensureRuntimePodsLoaded,
  runtimePodsLoadedForCurrentScope,
  selectedRuntimePods,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  percentText,
  openRuntimePodLogs
} = useAifarRuntimeContext()
</script>
