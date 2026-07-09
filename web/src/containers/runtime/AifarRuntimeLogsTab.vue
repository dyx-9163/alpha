<template>
  <div class="runtime-resource-panel runtime-log-panel">
    <div class="runtime-tab-toolbar">
      <div class="runtime-log-filters">
        <el-select
          v-model="runtimeLogServiceFilter"
          size="small"
          multiple
          collapse-tags
          collapse-tags-tooltip
          :max-collapse-tags="2"
          clearable
          class="runtime-service-filter"
          :placeholder="t('containers.logScopeServices')"
          @clear="clearRuntimeLogServiceFilter"
        >
          <el-option v-for="service in installedRuntimeServiceNamesList" :key="service" :label="service" :value="service" />
        </el-select>
        <el-select
          v-model="runtimeLogPodFilter"
          size="small"
          multiple
          collapse-tags
          collapse-tags-tooltip
          :max-collapse-tags="1"
          clearable
          class="runtime-pod-filter"
          :placeholder="t('containers.logScopePods')"
        >
          <el-option v-for="pod in runtimeLogPodOptions" :key="pod.value" :label="pod.label" :value="pod.value">
            <div class="runtime-log-pod-option">
              <span>{{ pod.label }}</span>
              <StatusTag :status="aifarRuntimeStatusKind(pod.status)" :label="aifarRuntimeStatusLabel(pod.status)" />
            </div>
          </el-option>
        </el-select>
        <el-select
          v-model="runtimeLogLevelFilter"
          size="small"
          multiple
          collapse-tags
          clearable
          class="runtime-level-filter"
          :placeholder="t('containers.logLevelFilter')"
        >
          <el-option v-for="level in runtimeLogLevelOptions" :key="level" :label="level" :value="level" />
        </el-select>
        <el-input v-model="runtimeLogKeyword" size="small" clearable class="runtime-log-keyword" :placeholder="t('containers.logKeyword')" />
        <el-input-number v-model="runtimeLogTail" size="small" class="runtime-log-tail" :min="20" :max="1000" :step="20" controls-position="right" />
      </div>
      <div class="runtime-tab-actions">
        <el-button size="small" type="primary" plain :loading="loading" :disabled="!runtimeLogSelectionReady" @click="loadRuntimeLogs(true)">
          {{ runtimeLogsLoadedForCurrentScope ? t('containers.restartRuntimeLogStream') : t('containers.startRuntimeLogStream') }}
        </el-button>
        <el-button size="small" plain :disabled="!runtimeLogsLoadedForCurrentScope" @click="toggleRuntimeLogPaused">
          {{ runtimeLogPaused ? t('containers.resumeLogs') : t('containers.pauseLogs') }}
        </el-button>
        <el-button size="small" plain :disabled="!runtimeLogRows.length && !runtimeLogPendingCount" @click="clearRuntimeLogView">{{ t('containers.clearLogs') }}</el-button>
        <el-switch v-model="runtimeLogAutoScroll" size="small" :active-text="t('containers.autoScroll')" inline-prompt />
      </div>
    </div>
    <el-alert
      v-if="runtimeLogWarnings.length"
      type="warning"
      :closable="false"
      show-icon
      :title="runtimeLogWarnings.join('；')"
    />
    <div v-if="!runtimeLogSelectionReady" class="runtime-lazy-state">
      <span>{{ t('containers.selectRuntimeLogScopeHint') }}</span>
    </div>
    <div v-else-if="!runtimeLogsLoadedForCurrentScope" class="runtime-lazy-state">
      <el-button size="small" type="primary" plain :loading="loading" @click="loadRuntimeLogs(true)">{{ t('containers.startRuntimeLogStream') }}</el-button>
    </div>
    <div v-else class="runtime-log-workbench">
      <div class="runtime-log-pod-strip">
        <div v-for="group in runtimeLogGroups" :key="group.containerName" class="runtime-log-pod-chip">
          <div>
            <strong>{{ group.serviceName }}</strong>
            <span>{{ group.containerName }}</span>
          </div>
          <el-tag size="small">{{ t('containers.logLines', { count: group.lineCount ?? 0 }) }}</el-tag>
        </div>
      </div>
      <div class="runtime-log-stats">
        <el-tag size="small" :type="runtimeLogStreamTagType">{{ runtimeLogStreamStatusLabel }}</el-tag>
        <span v-if="runtimeLogLastDataAt">{{ t('containers.logStreamLastEvent', { time: runtimeLogLastDataAt }) }}</span>
        <span>{{ t('containers.visibleLogRows', { count: filteredRuntimeLogRows.length }) }}</span>
        <span v-if="runtimeLogDroppedRows">{{ t('containers.droppedLogRows', { count: runtimeLogDroppedRows }) }}</span>
        <span v-if="runtimeLogPendingCount">{{ t('containers.pendingLogRows', { count: runtimeLogPendingCount }) }}</span>
      </div>
      <div ref="runtimeLogViewport" class="runtime-log-virtual-list" :class="{ 'is-empty': !filteredRuntimeLogRows.length }" @scroll="handleRuntimeLogScroll">
        <div v-if="!filteredRuntimeLogRows.length" class="runtime-log-empty-state">
          <span>{{ t('containers.noRuntimeLogs') }}</span>
        </div>
        <template v-else>
          <div :style="{ height: `${runtimeLogTopSpacer}px` }"></div>
          <div v-for="row in runtimeLogVirtualRows" :key="row.id" class="runtime-log-row">
            <span class="runtime-log-time">{{ row.time }}</span>
            <span class="runtime-log-service">{{ row.serviceName }}</span>
            <span class="runtime-log-pod">{{ row.pod }}</span>
            <span class="runtime-log-level" :class="`is-${runtimeLogLevelTag(row.level) || 'default'}`">{{ row.level || '-' }}</span>
            <span class="runtime-log-message">{{ row.message }}</span>
          </div>
          <div :style="{ height: `${runtimeLogBottomSpacer}px` }"></div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import StatusTag from '../../components/StatusTag.vue'
import { runtimeLogLevelOptions, runtimeLogLevelTag } from './logs'
import { useAifarRuntimeContext } from './context'

const {
  t,
  loading,
  runtimeLogServiceFilter,
  clearRuntimeLogServiceFilter,
  installedRuntimeServiceNamesList,
  runtimeLogPodFilter,
  runtimeLogPodOptions,
  aifarRuntimeStatusKind,
  aifarRuntimeStatusLabel,
  runtimeLogLevelFilter,
  runtimeLogKeyword,
  runtimeLogTail,
  runtimeLogSelectionReady,
  loadRuntimeLogs,
  runtimeLogsLoadedForCurrentScope,
  toggleRuntimeLogPaused,
  runtimeLogPaused,
  runtimeLogRows,
  runtimeLogPendingCount,
  clearRuntimeLogView,
  runtimeLogAutoScroll,
  runtimeLogWarnings,
  runtimeLogGroups,
  runtimeLogStreamTagType,
  runtimeLogStreamStatusLabel,
  runtimeLogLastDataAt,
  filteredRuntimeLogRows,
  runtimeLogDroppedRows,
  runtimeLogViewport,
  handleRuntimeLogScroll,
  runtimeLogTopSpacer,
  runtimeLogVirtualRows,
  runtimeLogBottomSpacer
} = useAifarRuntimeContext()
</script>
