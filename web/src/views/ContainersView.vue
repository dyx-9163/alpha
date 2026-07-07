<template>
  <section class="containers-page" :class="{ 'is-runtime-logs-page': tab === 'aifar-runtime' && runtimeResourceTab === 'logs' }">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('containers.title') }}</h1>
        <p class="page-subtitle">{{ t('containers.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <ServerSelector v-model="selectedServerId" :servers="dockerServers" :placeholder="t('containers.selectDockerHost')" class="toolbar-control" />
        <el-button :loading="loading" @click="load(true)">{{ t('containers.checkHost') }}</el-button>
        <el-button :loading="loading" @click="loadActive(true)">{{ t('common.refresh') }}</el-button>
        <el-tooltip v-if="selectedDockerInstance" :content="deniedText" :disabled="canManageApps" placement="top">
          <span>
            <el-button type="danger" plain :disabled="!canManageApps" @click="openDockerUninstall">{{ t('common.uninstall') }}</el-button>
          </span>
        </el-tooltip>
      </div>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('containers.overview')" name="overview" />
      <el-tab-pane :label="t('containers.aifarRuntime')" name="aifar-runtime" />
      <el-tab-pane :label="t('containers.title')" name="containers" />
      <el-tab-pane :label="t('containers.images')" name="images" />
    </el-tabs>

    <el-alert v-if="error" :title="errorTitle" :description="error" type="warning" :closable="false" show-icon />
    <div class="muted-strip" v-if="!summary.available">{{ t('containers.disabledHint') }}</div>

    <div class="workspace-card containers-main" :class="{ 'is-runtime-logs': tab === 'aifar-runtime' && runtimeResourceTab === 'logs' }" v-loading="loading">
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
              <el-tooltip :content="aifarAppUninstallDisabledReason" :disabled="!aifarAppUninstallDisabledReason" placement="top">
                <span><el-button size="small" type="danger" plain :disabled="Boolean(aifarAppUninstallDisabledReason)" @click="openAifarAppUninstall">{{ t('containers.uninstallAifarApp') }}</el-button></span>
              </el-tooltip>
              <el-button size="small" :loading="loading" @click="loadAifarRuntime(true)">{{ t('common.refresh') }}</el-button>
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
            <el-tabs v-model="runtimeResourceTab" class="runtime-resource-tabs">
              <el-tab-pane :label="t('containers.deployments')" name="deployments">
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
              </el-tab-pane>
              <el-tab-pane :label="t('containers.services')" name="services">
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
                    <el-table-column :label="t('containers.cpu')" width="90">
                      <template #default="{ row }">{{ percentText(row.cpuPercent) }}</template>
                    </el-table-column>
                    <el-table-column :label="t('containers.memory')" width="100">
                      <template #default="{ row }">{{ percentText(row.memoryPercent) }}</template>
                    </el-table-column>
                  </el-table>
                </div>
              </el-tab-pane>
              <el-tab-pane :label="t('containers.pods')" name="pods">
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
                  </el-table>
                </div>
              </el-tab-pane>
              <el-tab-pane :label="t('containers.logs')" name="logs">
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
              </el-tab-pane>
              <el-tab-pane :label="t('containers.ingressAndNacos')" name="ingress">
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
              </el-tab-pane>
            </el-tabs>
          </template>
        </div>
      </template>

      <template v-else-if="tab === 'images'">
        <el-tabs v-model="resourceTab" class="resource-tabs">
          <el-tab-pane :label="t('containers.images')" name="images">
            <div class="resource-panel">
              <div class="table-toolbar">
                <span class="selection-summary">{{ t('containers.selectedImageCount', { count: selectedImageRows.length }) }}</span>
                <div class="toolbar-actions">
                  <el-tooltip :content="batchImageRemoveDisabledReason" :disabled="!batchImageRemoveDisabledReason" placement="top">
                    <span>
                      <el-button size="small" type="danger" plain :disabled="batchImageRemoveDisabled" @click="deleteSelectedImages">{{ t('containers.batchDeleteImages') }}</el-button>
                    </span>
                  </el-tooltip>
                </div>
              </div>
              <div class="container-table-body">
                <el-table :data="collection" height="100%" :row-key="imageRowKey" @selection-change="onImageSelectionChange">
                  <el-table-column type="selection" width="44" />
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
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.network')" name="networks">
            <div class="resource-panel">
              <el-table :data="collection" height="100%">
                <el-table-column prop="name" :label="t('containers.name')" min-width="180" />
                <el-table-column prop="id" label="ID" min-width="150" show-overflow-tooltip />
                <el-table-column prop="driver" :label="t('containers.driver')" min-width="150" />
                <el-table-column prop="scope" :label="t('containers.scope')" min-width="120" />
              </el-table>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.volumes')" name="volumes">
            <div class="resource-panel">
              <el-table :data="collection" height="100%">
                <el-table-column prop="name" :label="t('containers.name')" min-width="180" show-overflow-tooltip />
                <el-table-column prop="driver" :label="t('containers.driver')" width="140" />
                <el-table-column prop="scope" :label="t('containers.scope')" width="120" />
                <el-table-column prop="mountpoint" :label="t('containers.mountpoint')" min-width="260" show-overflow-tooltip />
                <el-table-column prop="size" :label="t('containers.size')" width="120" />
              </el-table>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.registry')" name="registry">
            <div class="resource-panel">
              <div class="empty-state">
                <div>
                  <strong>{{ t('containers.registry') }}</strong>
                  <span>{{ t('containers.registryHint') }}</span>
                </div>
              </div>
            </div>
          </el-tab-pane>
          <el-tab-pane :label="t('containers.hostConfig')" name="settings">
            <div class="resource-panel">
              <div class="settings-grid">
                <KeyValueGrid :items="settingsItems" />
                <p class="muted-strip">{{ t('containers.settingsHint') }}</p>
              </div>
            </div>
          </el-tab-pane>
        </el-tabs>
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
        <el-button @click="runtimeConfigVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="runtimeConfigSubmitting" @click="submitRuntimeConfig">{{ t('containers.applyRuntimeConfig') }}</el-button>
      </template>
    </el-dialog>
    <SecretConfirmPrompt
      v-model="deletePromptVisible"
      :title="deletePromptTitle"
      :message="deletePromptMessage"
      :placeholder="t('apps.deleteServicePasswordPlaceholder')"
      :confirm-text="t('common.uninstall')"
      :cancel-text="t('common.cancel')"
      :loading="deleteSubmitting"
      @confirm="confirmAppUninstall"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { UploadFile } from 'element-plus'
import { apiEventSourceUrl, apiGet, apiPost, apiPostForm, apiPut, asArray } from '../api/client'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import LogDrawer from '../components/LogDrawer.vue'
import MetricGrid from '../components/MetricGrid.vue'
import SecretConfirmPrompt from '../components/SecretConfirmPrompt.vue'
import ServerSelector from '../components/ServerSelector.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { getCurrentLocale, useI18n } from '../i18n'
import { permissions } from '../rbac'
import { useRealtimeStore } from '../stores/realtime'
import { useTaskProgressStore } from '../stores/taskProgress'

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
  nacosEphemeral?: boolean
  appliedVersion?: number
  lastAppliedAt?: string
  lastApplyStatus?: string
  lastApplyError?: string
}

type RuntimeConfigFormValues = Required<RuntimeConfigValues> & {
  nacosEphemeral: boolean
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
  listeners?: number[]
  features?: string[]
}

type AifarRuntimeService = {
  instanceId: string
  serviceName: string
  appName?: string
  proxyName?: string
  desiredReplicas?: number
  readyReplicas?: number
  activeEndpoints?: number
  endpointCount?: number
  readyEndpointCount?: number
  image?: string
  status?: string
  rolloutStatus?: string
  nacosRegistered?: boolean
  nacosReady?: boolean
  lastNacosError?: string
  lastError?: string
  cpuPercent?: number
  memoryPercent?: number
  failureReason?: string
}

type AifarRuntimeDeployment = {
  instanceId: string
  deploymentName?: string
  serviceName: string
  appName?: string
  desiredReplicas?: number
  currentReplicas?: number
  readyReplicas?: number
  updatedReplicas?: number
  availableReplicas?: number
  podRevision?: string
  updatingPodRevision?: string
  image?: string
  status?: string
  updatedAt?: string
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

type AifarRuntimeLogPod = {
  instanceId: string
  serviceName: string
  podId?: string
  containerName: string
  revision?: string
  status?: string
  ready?: boolean
  logs?: string[]
  lineCount?: number
  collectionError?: string
}

type AifarRuntimeLogsResponse = {
  serverId?: string
  instanceId?: string
  service?: string
  services?: string[]
  podsFilter?: string[]
  tail?: number
  batchSize?: number
  mode?: string
  pods?: AifarRuntimeLogPod[]
  warnings?: string[]
}

type RuntimeLogRow = {
  id: string
  time: string
  timestamp: number
  sequence: number
  serviceName: string
  pod: string
  level: string
  message: string
}

type RuntimeEntryRoute = {
  name: string
  route: string
  port: string
  status: string
}

type AifarRuntimeResponse = {
  serverId?: string
  runtimeStatus?: string
  agent?: AifarRuntimeAgent
  instances?: AifarRuntimeInstance[]
  deployments?: AifarRuntimeDeployment[]
  services?: AifarRuntimeService[]
  pods?: AifarRuntimePod[]
  ingress?: AifarRuntimeIngress[]
  warnings?: string[]
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const taskProgress = useTaskProgressStore()
const realtime = useRealtimeStore()
const AIFAR_RUNTIME_MODEL = 'agent-runtime-v2'
const selectedServerId = ref('')
const servers = ref<any[]>([])
const appInstances = ref<AppInstance[]>([])
const appSettings = ref<{ maxRequestBodyBytes?: number }>({})
const summary = ref<DockerSummaryResponse>({})
const collection = ref<any[]>([])
const loadingCount = ref(0)
const loading = computed(() => loadingCount.value > 0)
const pageReady = ref(false)
const summaryCache = ref<Record<string, DockerSummaryResponse>>({})
const collectionCache = ref<Record<string, any[]>>({})
const runtimeCache = ref<Record<string, AifarRuntimeResponse>>({})
const selectedContainerRows = ref<any[]>([])
const selectedImageRows = ref<any[]>([])
const error = ref('')
const tab = ref<'overview' | 'aifar-runtime' | 'containers' | 'images'>('overview')
const resourceTab = ref<'images' | 'networks' | 'volumes' | 'registry' | 'settings'>('images')
const logsVisible = ref(false)
const logsText = ref('')
let containerLogSource: EventSource | null = null
const containerLogMaxLines = 3000
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
const runtimeResourceTab = ref<'deployments' | 'services' | 'pods' | 'logs' | 'ingress'>('deployments')
const runtimePodServiceFilter = ref('')
const runtimePodsLoaded = ref<Record<string, boolean>>({})
const runtimePodStatsLoaded = ref<Record<string, boolean>>({})
const runtimeLogServiceFilter = ref<string[]>([])
const runtimeLogPodFilter = ref<string[]>([])
const runtimeLogLevelFilter = ref<string[]>([])
const runtimeLogKeyword = ref('')
const runtimeLogTail = ref(200)
const runtimeLogSinceSeconds = ref(0)
const runtimeLogs = ref<AifarRuntimeLogsResponse>({ pods: [], warnings: [], tail: 200 })
const runtimeLogsLoaded = ref<Record<string, boolean>>({})
const runtimeLogRows = ref<RuntimeLogRow[]>([])
const runtimeLogPendingRows = ref<RuntimeLogRow[]>([])
const runtimeLogPaused = ref(false)
const runtimeLogAutoScroll = ref(true)
const runtimeLogDroppedRows = ref(0)
const runtimeLogScrollTop = ref(0)
const runtimeLogViewport = ref<HTMLElement | null>(null)
const runtimeLogStreamStatus = ref<'idle' | 'connecting' | 'connected' | 'reconnecting' | 'error'>('idle')
const runtimeLogLastDataAt = ref('')
let runtimeLogSource: EventSource | null = null
let runtimeLogStreamKey = ''
let runtimeLogSequence = 0
const runtimeLogMaxRows = 3000
const runtimeLogRowHeight = 32
const runtimeLogVisibleCount = 100
const runtimeLogLevelOptions = ['ERROR', 'WARN', 'INFO', 'DEBUG', 'TRACE']
const runtimeConfigVisible = ref(false)
const runtimeConfigSubmitting = ref(false)
const runtimeConfigForm = ref<RuntimeConfigFormValues>({
  appCPUs: '2.0',
  appMemoryLimit: '2GB',
  jvmInitialRAMPercentage: 20,
  jvmMaxRAMPercentage: 70,
  nacosEphemeral: true
})
const runtimeConfigRows = ref<RuntimeConfigServiceRow[]>([])
const serviceInstallVisible = ref(false)
const serviceInstallSubmitting = ref(false)
const serviceInstallSelection = ref<string[]>([])
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const deletePromptInstance = ref<AppInstance | null>(null)
const canManageContainers = computed(() => can(permissions.containersManage))
const canManageApps = computed(() => can(permissions.appsManage))

const summaryData = computed(() => summary.value.summary ?? {})
const selectedServer = computed(() => servers.value.find((server) => server.id === selectedServerId.value) ?? null)
const selectedDockerInstance = computed(() => appInstances.value.find((item) => item.app === 'docker' && item.serverId === selectedServerId.value) ?? null)
const dockerServers = computed(() => servers.value.filter((server) => String(server.dockerHost ?? '').trim() !== ''))
const targetLabel = computed(() => selectedServer.value ? serverLabel(selectedServer.value) : t('containers.selectDockerHost'))
const errorTitle = computed(() => summary.value.available === false ? t('containers.notAvailable') : t('containers.checkFailed'))
const deletePromptTitle = computed(() => deletePromptInstance.value?.app === 'aifar' ? t('containers.uninstallAifarApp') : t('apps.uninstallService'))
const deletePromptMessage = computed(() => {
  const instance = deletePromptInstance.value
  const server = servers.value.find((item) => item.id === instance?.serverId) ?? selectedServer.value
  return server ? t('apps.deleteServicePasswordPrompt', { server: serverLabel(server) }) : ''
})
const metrics = computed(() => [
  { label: t('containers.title'), value: summaryData.value.containers ?? 0, note: t('containers.runningCount', { count: summaryData.value.running ?? 0 }) },
  { label: t('containers.images'), value: summaryData.value.images ?? 0, note: t('containers.localImages') },
  { label: t('containers.network'), value: summaryData.value.networks ?? 0, note: t('containers.network') },
  { label: t('containers.volumes'), value: summaryData.value.volumes ?? 0, note: t('containers.volumes') }
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
const selectedImageIds = computed(() => uniqueValues(selectedImageRows.value.map(imageReference).filter(Boolean)))
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
const batchImageRemoveDisabledReason = computed(() => {
  if (!canManageContainers.value) return deniedText.value
  if (!selectedImageIds.value.length) return t('containers.selectImages')
  return ''
})
const batchImageRemoveDisabled = computed(() => Boolean(batchImageRemoveDisabledReason.value))
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
const selectedRuntimeDeployments = computed(() => asArray<AifarRuntimeDeployment>(aifarRuntime.value.deployments).filter((item) => item.instanceId === selectedRuntimeInstance.value?.id))
const selectedRuntimePodsRaw = computed(() => asArray<AifarRuntimePod>(aifarRuntime.value.pods).filter((item) => item.instanceId === selectedRuntimeInstance.value?.id))
const selectedRuntimePods = computed(() => {
  const service = String(runtimePodServiceFilter.value || '').trim()
  if (!service) return selectedRuntimePodsRaw.value
  return selectedRuntimePodsRaw.value.filter((item) => item.serviceName === service)
})
const staleRuntimePodCount = computed(() => selectedRuntimePodsRaw.value.filter((item) => String(item.status || '').trim() === 'stale').length)
const selectedRuntimeIngress = computed(() => asArray<AifarRuntimeIngress>(aifarRuntime.value.ingress).find((item) => item.instanceId === selectedRuntimeInstance.value?.id) ?? null)
const aifarRuntimeWarnings = computed(() => asArray<string>(aifarRuntime.value.warnings))
const installedRuntimeServiceNames = computed(() => new Set(selectedRuntimeServices.value.map((item) => item.serviceName).filter(Boolean)))
const runtimeServiceMap = computed(() => {
  const out = new Map<string, AifarRuntimeService>()
  for (const service of selectedRuntimeServices.value) {
    if (service.serviceName) out.set(service.serviceName, service)
  }
  return out
})
const runtimePodsLoadedForCurrentScope = computed(() => Boolean(runtimePodsLoaded.value[runtimeCacheKey('pods')]))
const runtimePodStatsLoadedForCurrentScope = computed(() => Boolean(runtimePodStatsLoaded.value[runtimeCacheKey('pods')]))
const runtimeLogsLoadedForCurrentScope = computed(() => Boolean(runtimeLogsLoaded.value[runtimeLogCacheKey()]))
const runtimeLogGroups = computed(() => asArray<AifarRuntimeLogPod>(runtimeLogs.value.pods))
const runtimeLogPodOptions = computed(() => {
  const services = new Set(runtimeLogServiceFilter.value)
  return selectedRuntimePodsRaw.value
    .filter((pod) => !services.size || services.has(pod.serviceName))
    .map((pod) => ({
      value: pod.containerName,
      label: `${pod.serviceName} / ${pod.containerName}`,
      serviceName: pod.serviceName,
      status: pod.status || 'unknown'
    }))
})
const runtimeLogSelectionReady = computed(() => runtimeLogServiceFilter.value.length > 0 || runtimeLogPodFilter.value.length > 0)
const filteredRuntimeLogRows = computed<RuntimeLogRow[]>(() => {
  const levels = new Set(runtimeLogLevelFilter.value.map((item) => item.toUpperCase()))
  const keyword = runtimeLogKeyword.value.trim().toLowerCase()
  const rows = runtimeLogRows.value.filter((row) => {
    if (levels.size && !levels.has(String(row.level || '').toUpperCase())) {
      return false
    }
    if (keyword) {
      const text = `${row.time} ${row.serviceName} ${row.pod} ${row.level} ${row.message}`.toLowerCase()
      return text.includes(keyword)
    }
    return true
  })
  return rows.sort((left, right) => {
    if (left.timestamp && right.timestamp && left.timestamp !== right.timestamp) {
      return left.timestamp - right.timestamp
    }
    if (left.timestamp && !right.timestamp) return -1
    if (!left.timestamp && right.timestamp) return 1
    return left.sequence - right.sequence
  })
})
const runtimeLogVirtualStart = computed(() => Math.max(0, Math.floor(runtimeLogScrollTop.value / runtimeLogRowHeight) - 8))
const runtimeLogVirtualRows = computed(() => filteredRuntimeLogRows.value.slice(runtimeLogVirtualStart.value, runtimeLogVirtualStart.value + runtimeLogVisibleCount))
const runtimeLogTopSpacer = computed(() => runtimeLogVirtualStart.value * runtimeLogRowHeight)
const runtimeLogBottomSpacer = computed(() => Math.max(0, (filteredRuntimeLogRows.value.length - runtimeLogVirtualStart.value - runtimeLogVirtualRows.value.length) * runtimeLogRowHeight))
const runtimeLogWarnings = computed(() => asArray<string>(runtimeLogs.value.warnings))
const runtimeLogPendingCount = computed(() => runtimeLogPendingRows.value.length)
const runtimeLogStreamStatusLabel = computed(() => t(`containers.logStream.${runtimeLogStreamStatus.value}`))
const runtimeLogStreamTagType = computed(() => {
  switch (runtimeLogStreamStatus.value) {
    case 'connected':
      return 'success'
    case 'connecting':
    case 'reconnecting':
      return 'warning'
    case 'error':
      return 'danger'
    default:
      return 'info'
  }
})
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
const aifarAppUninstallDisabledReason = computed(() => {
  if (!canManageApps.value) return deniedText.value
  if (!selectedRuntimeAppInstance.value) return t('containers.selectAifarInstance')
  return ''
})
const serviceInstallDisabledReason = computed(() => {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  if (!missingRuntimeServiceOptions.value.length) return t('containers.noMissingServices')
  return ''
})
const runtimeSummaryItems = computed(() => {
  const instance = selectedRuntimeInstance.value
  const config = selectedRuntimeConfig.value
  return [
    { label: t('containers.aifarInstance'), value: instance ? runtimeInstanceLabel(instance) : '-' },
    { label: t('containers.installRoot'), value: instance?.installRoot || '-' },
    { label: t('containers.runtimeConfigVersion'), value: `${config.configVersion ?? '-'} / ${config.appliedVersion ?? '-'}`, status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.lastApplyStatus'), value: runtimeApplyStatusLabel(config.lastApplyStatus), status: config.lastApplyStatus || 'unknown' },
    { label: t('containers.agent'), value: aifarRuntime.value.agent?.version || aifarRuntime.value.agent?.status || '-', status: aifarRuntime.value.agent?.status || 'unknown' }
  ]
})
const runtimeEntryRoutes = computed<RuntimeEntryRoute[]>(() => {
  const ingress = selectedRuntimeIngress.value
  return [
    {
      name: t('containers.webRoute'),
      route: ingress?.webRoute || selectedRuntimeInstance.value?.endpoint || '-',
      port: `${t('containers.webPort')}: ${ingress?.webPort || '-'}`,
      status: ingress?.status || 'unknown'
    },
    {
      name: t('containers.gatewayRoute'),
      route: ingress?.gatewayRoute || selectedRuntimeInstance.value?.gatewayEndpoint || '-',
      port: `${t('containers.gatewayPort')}: ${ingress?.gatewayPort || '-'}`,
      status: ingress?.status || 'unknown'
    }
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

function cacheScope() {
  return selectedServerId.value || 'none'
}

function summaryCacheKey(includeDisk: boolean) {
  return `${cacheScope()}:summary:${includeDisk ? 'disk' : 'base'}`
}

function activeCollectionKind() {
  if (tab.value === 'images') {
    return resourceTab.value
  }
  if (tab.value === 'containers') {
    return 'containers'
  }
  return ''
}

function collectionBackedKind(kind = activeCollectionKind()) {
  return kind === 'containers' || kind === 'images' || kind === 'networks' || kind === 'volumes'
}

function collectionCacheKey(kind = activeCollectionKind()) {
  return `${cacheScope()}:collection:${kind}`
}

function runtimeCacheKey(scope: 'base' | 'pods' = 'base') {
  return `${cacheScope()}:aifar-runtime:${scope}`
}

function runtimeLogCacheKey() {
  return `${cacheScope()}:aifar-runtime:logs:${selectedRuntimeInstance.value?.id || 'none'}:${runtimeLogServiceFilter.value.join(',')}:${runtimeLogPodFilter.value.join(',')}:${runtimeLogTail.value}:${runtimeLogSinceSeconds.value}`
}

async function withLoading<T>(fn: () => Promise<T>) {
  loadingCount.value += 1
  try {
    return await fn()
  } finally {
    loadingCount.value = Math.max(0, loadingCount.value - 1)
  }
}

function serverLabel(server: any) {
  if (!server) {
    return ''
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || server.id
}

async function loadServers() {
  const [serverRows, instanceRows, settings] = await Promise.all([
    apiGet<any[] | null>('/servers').catch(() => []),
    apiGet<AppInstance[] | null>('/apps/instances').catch(() => []),
    apiGet<{ maxRequestBodyBytes?: number }>('/settings').catch(() => ({}))
  ])
  servers.value = asArray(serverRows)
  appInstances.value = asArray(instanceRows)
  appSettings.value = settings
  if (!selectedServerId.value || !dockerServers.value.some((server) => server.id === selectedServerId.value)) {
    selectedServerId.value = dockerServers.value[0]?.id ?? ''
  }
}

async function load(force = false) {
  return withLoading(async () => {
    error.value = ''
    const query = targetQuery()
    if (!query) {
      summary.value = { available: false }
      collection.value = []
      selectedContainerRows.value = []
      return
    }
    const includeDisk = tab.value === 'overview'
    if (includeDisk) {
      await loadSummary(true, force)
      return
    }
    await Promise.all([loadSummary(false, force), loadCollection(force)])
  })
}

async function loadSummary(includeDisk = false, force = false) {
  error.value = ''
  const query = targetQuery()
  if (!query) {
    summary.value = { available: false }
    return
  }
  const key = summaryCacheKey(includeDisk)
  const diskKey = summaryCacheKey(true)
  if (!force) {
    const cached = summaryCache.value[key] ?? (!includeDisk ? summaryCache.value[diskKey] : undefined)
    if (cached) {
      summary.value = cached
      return
    }
  }
  const endpoint = `/containers/summary?${query}${includeDisk ? '&includeDisk=1' : ''}`
  const next = await apiGet<DockerSummaryResponse>(endpoint).catch((err) => {
    error.value = err.message
    return { available: false, error: err.message }
  })
  summary.value = next
  summaryCache.value = { ...summaryCache.value, [key]: next }
  if (includeDisk) {
    summaryCache.value = { ...summaryCache.value, [summaryCacheKey(false)]: next }
  }
  if (next.available === false && next.error) error.value = next.error
}

function applyDockerSummaryEvent(event: unknown) {
  const envelope = realtimeSnapshotEnvelope(event)
  const payload = objectPayload(envelope?.payload)
  if (!payload) {
    return false
  }
  const next: DockerSummaryResponse = {
    ...summary.value,
    available: payload.available === true,
    error: typeof envelope?.lastError === 'string' ? envelope.lastError : summary.value.error,
    summary: objectPayload(payload.summary) ?? summary.value.summary,
    diskUsage: summary.value.diskUsage
  }
  summary.value = next
  summaryCache.value = { ...summaryCache.value, [summaryCacheKey(false)]: next }
  if (next.available === false && next.error) {
    error.value = next.error
  }
  return true
}

function realtimeSnapshotEnvelope(event: unknown) {
  return objectPayload((event as { payload?: unknown } | null)?.payload)
}

function objectPayload(value: unknown) {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, any> : null
}

async function loadCollection(force = false) {
  if (tab.value === 'aifar-runtime') {
    collection.value = []
    selectedContainerRows.value = []
    selectedImageRows.value = []
    await loadAifarRuntime(force)
    return
  }
  const kind = activeCollectionKind()
  if (!collectionBackedKind(kind)) {
    collection.value = []
    selectedContainerRows.value = []
    selectedImageRows.value = []
    return
  }
  const query = targetQuery()
  if (!query) {
    collection.value = []
    selectedContainerRows.value = []
    selectedImageRows.value = []
    return
  }
  selectedContainerRows.value = []
  selectedImageRows.value = []
  const key = collectionCacheKey(kind)
  if (!force && collectionCache.value[key]) {
    collection.value = collectionCache.value[key]
    return
  }
  const next = asArray(await apiGet(`/containers?kind=${kind}&${query}`).catch((err) => {
    error.value = err.message
    return []
  }))
  collection.value = next
  collectionCache.value = { ...collectionCache.value, [key]: next }
}

async function loadAifarRuntime(force = false, includePods = runtimeResourceTab.value === 'pods', includeStats = false) {
  return withLoading(async () => {
    const query = targetQuery()
    if (!query) {
      aifarRuntime.value = { runtimeStatus: 'unknown', agent: { status: 'unknown' }, instances: [], services: [], pods: [], ingress: [], warnings: [] }
      selectedRuntimeInstanceId.value = ''
      return
    }
    const scope = includePods ? 'pods' : 'base'
    const key = runtimeCacheKey(scope)
    const cacheHasRequiredStats = !includeStats || runtimePodStatsLoaded.value[key]
    if (!force && runtimeCache.value[key] && cacheHasRequiredStats) {
      aifarRuntime.value = includePods ? runtimeCache.value[key] : { ...runtimeCache.value[key], pods: asArray<AifarRuntimePod>(aifarRuntime.value.pods) }
      return
    }
    const next = await apiGet<AifarRuntimeResponse>(`/containers/aifar/runtime?${query}&includePods=${includePods ? 1 : 0}&includeStats=${includeStats ? 1 : 0}`).catch((err) => {
      error.value = err.message
      return { runtimeStatus: 'degraded', agent: { status: 'missing', error: err.message }, instances: [], services: [], pods: [], ingress: [], warnings: [err.message] }
    })
    const currentPods = asArray<AifarRuntimePod>(aifarRuntime.value.pods)
    const merged = includePods ? next : { ...next, pods: currentPods }
    aifarRuntime.value = merged
    runtimeCache.value = { ...runtimeCache.value, [key]: merged }
    if (includePods) {
      const podsKey = runtimeCacheKey('pods')
      runtimePodsLoaded.value = { ...runtimePodsLoaded.value, [podsKey]: true }
      runtimePodStatsLoaded.value = { ...runtimePodStatsLoaded.value, [podsKey]: includeStats }
      runtimeCache.value = { ...runtimeCache.value, [runtimeCacheKey('base')]: { ...merged, pods: [] } }
    }
    const instances = asArray<AifarRuntimeInstance>(aifarRuntime.value.instances)
    if (!instances.some((instance) => instance.id === selectedRuntimeInstanceId.value)) {
      selectedRuntimeInstanceId.value = instances.find((instance) => !instance.legacy)?.id ?? instances[0]?.id ?? ''
    }
  })
}

async function ensureRuntimePodsLoaded(force = false, includeStats = false) {
  if (!targetQuery()) return
  if (!force && runtimePodsLoadedForCurrentScope.value && (!includeStats || runtimePodStatsLoadedForCurrentScope.value)) return
  await loadAifarRuntime(force, true, includeStats)
}

function clearRuntimePodServiceFilter() {
  runtimePodServiceFilter.value = ''
}

function loadRuntimeLogs(force = false) {
  const key = runtimeLogCacheKey()
  if (!force && runtimeLogSource && runtimeLogStreamKey === key) {
    return
  }
  if (!runtimeLogSelectionReady.value) {
    if (force) {
      ElMessage.warning(t('containers.selectRuntimeLogScope'))
    }
    return
  }
  openRuntimeLogStream(force)
}

function openRuntimeLogStream(force = false) {
  closeRuntimeLogStream()
  const query = targetQuery()
  const instance = selectedRuntimeInstance.value
  const key = runtimeLogCacheKey()
  if (force) {
    resetRuntimeLogView()
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: false }
  }
  if (tab.value !== 'aifar-runtime' || runtimeResourceTab.value !== 'logs' || !query || !instance?.id || !runtimeLogSelectionReady.value) {
    return
  }
  const params = new URLSearchParams(query)
  params.set('instanceId', instance.id)
  params.set('tail', String(runtimeLogTail.value))
  params.set('batch', '200')
  if (runtimeLogSinceSeconds.value > 0) {
    params.set('since', String(runtimeLogSinceSeconds.value))
  } else if (runtimeLogSinceSeconds.value < 0) {
    params.set('fromEnd', '1')
  }
  if (runtimeLogServiceFilter.value.length) {
    params.set('services', runtimeLogServiceFilter.value.join(','))
  }
  if (runtimeLogPodFilter.value.length) {
    params.set('pods', runtimeLogPodFilter.value.join(','))
  }
  const source = new EventSource(apiEventSourceUrl(`/containers/aifar/runtime/logs/events?${params.toString()}`))
  runtimeLogSource = source
  runtimeLogStreamKey = key
  runtimeLogStreamStatus.value = 'connecting'
  runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  source.onopen = () => {
    if (runtimeLogSource === source) {
      runtimeLogStreamStatus.value = 'connected'
    }
  }
  source.onerror = () => {
    if (runtimeLogSource === source) {
      runtimeLogStreamStatus.value = 'reconnecting'
    }
  }
  const applySnapshot = (event: Event) => {
    if (runtimeLogSource !== source) {
      return
    }
    const next = parseRuntimeLogsEvent((event as MessageEvent).data)
    if (!next) {
      return
    }
    markRuntimeLogDataReceived()
    applyRuntimeLogResponse(next, true)
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  }
  const applyBatch = (event: Event) => {
    if (runtimeLogSource !== source) {
      return
    }
    const next = parseRuntimeLogsEvent((event as MessageEvent).data)
    if (!next) {
      return
    }
    markRuntimeLogDataReceived()
    applyRuntimeLogResponse(next, false)
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  }
  source.addEventListener('runtime-logs-snapshot', applySnapshot)
  source.addEventListener('runtime-logs-batch', applyBatch)
  source.addEventListener('runtime-logs', applySnapshot)
  source.addEventListener('runtime-logs-error', (event) => {
    if (runtimeLogSource !== source) {
      return
    }
    const message = parseRuntimeLogErrorEvent((event as MessageEvent).data)
    runtimeLogStreamStatus.value = 'error'
    runtimeLogs.value = { ...runtimeLogs.value, warnings: message ? [message] : [] }
    runtimeLogsLoaded.value = { ...runtimeLogsLoaded.value, [key]: true }
  })
}

function closeRuntimeLogStream() {
  if (runtimeLogSource) {
    runtimeLogSource.close()
    runtimeLogSource = null
  }
  runtimeLogStreamKey = ''
  runtimeLogStreamStatus.value = 'idle'
}

function markRuntimeLogDataReceived() {
  runtimeLogStreamStatus.value = 'connected'
  runtimeLogLastDataAt.value = new Date().toLocaleTimeString()
}

function parseRuntimeLogsEvent(raw: string) {
  try {
    return JSON.parse(raw) as AifarRuntimeLogsResponse
  } catch {
    return null
  }
}

function parseRuntimeLogErrorEvent(raw: string) {
  try {
    const parsed = JSON.parse(raw) as { message?: string }
    return String(parsed.message || '').trim()
  } catch {
    return ''
  }
}

function resetRuntimeLogView() {
  runtimeLogs.value = { pods: [], warnings: [], tail: runtimeLogTail.value }
  runtimeLogRows.value = []
  runtimeLogPendingRows.value = []
  runtimeLogPaused.value = false
  runtimeLogDroppedRows.value = 0
  runtimeLogSequence = 0
  runtimeLogScrollTop.value = 0
  runtimeLogLastDataAt.value = ''
}

function applyRuntimeLogResponse(next: AifarRuntimeLogsResponse, replace: boolean) {
  runtimeLogs.value = replace ? next : mergeRuntimeLogGroups(runtimeLogs.value, next)
  const rows = runtimeLogRowsFromResponse(next)
  appendRuntimeLogRows(rows)
}

function mergeRuntimeLogGroups(current: AifarRuntimeLogsResponse, next: AifarRuntimeLogsResponse): AifarRuntimeLogsResponse {
  const groups = new Map<string, AifarRuntimeLogPod>()
  for (const pod of asArray<AifarRuntimeLogPod>(current.pods)) {
    groups.set(pod.containerName, { ...pod, logs: [] })
  }
  for (const pod of asArray<AifarRuntimeLogPod>(next.pods)) {
    const previous = groups.get(pod.containerName)
    const previousCount = Number(previous?.lineCount ?? 0)
    const nextCount = Number(pod.lineCount ?? asArray<string>(pod.logs).length)
    groups.set(pod.containerName, {
      ...(previous ?? {}),
      ...pod,
      logs: [],
      lineCount: previous ? previousCount + nextCount : nextCount
    })
  }
  return {
    ...current,
    ...next,
    pods: Array.from(groups.values()),
    warnings: uniqueValues([...asArray<string>(current.warnings), ...asArray<string>(next.warnings)])
  }
}

function runtimeLogRowsFromResponse(next: AifarRuntimeLogsResponse) {
  const rows: RuntimeLogRow[] = []
  for (const pod of asArray<AifarRuntimeLogPod>(next.pods)) {
    for (const line of asArray<string>(pod.logs)) {
      const parsed = parseRuntimeLogLine(line)
      const sequence = runtimeLogSequence++
      rows.push({
        id: `${pod.containerName}:${sequence}`,
        time: parsed.time,
        timestamp: parsed.timestamp,
        sequence,
        serviceName: pod.serviceName || '-',
        pod: pod.containerName || pod.podId || '-',
        level: parsed.level,
        message: parsed.message
      })
    }
  }
  return rows
}

function appendRuntimeLogRows(rows: RuntimeLogRow[]) {
  if (!rows.length) {
    return
  }
  if (runtimeLogPaused.value) {
    runtimeLogPendingRows.value = boundedRuntimeLogRows([...runtimeLogPendingRows.value, ...rows])
    return
  }
  runtimeLogRows.value = boundedRuntimeLogRows([...runtimeLogRows.value, ...rows])
  if (runtimeLogAutoScroll.value) {
    void scrollRuntimeLogsToBottom()
  }
}

function boundedRuntimeLogRows(rows: RuntimeLogRow[]) {
  if (rows.length <= runtimeLogMaxRows) {
    return rows
  }
  const dropped = rows.length - runtimeLogMaxRows
  runtimeLogDroppedRows.value += dropped
  return rows.slice(dropped)
}

function toggleRuntimeLogPaused() {
  runtimeLogPaused.value = !runtimeLogPaused.value
  if (!runtimeLogPaused.value && runtimeLogPendingRows.value.length) {
    const pending = runtimeLogPendingRows.value
    runtimeLogPendingRows.value = []
    appendRuntimeLogRows(pending)
  }
}

function clearRuntimeLogView() {
  const shouldRestart = runtimeLogSelectionReady.value && (Boolean(runtimeLogSource) || runtimeLogsLoadedForCurrentScope.value)
  runtimeLogSinceSeconds.value = -1
  runtimeLogRows.value = []
  runtimeLogPendingRows.value = []
  runtimeLogs.value = {
    ...runtimeLogs.value,
    pods: runtimeLogGroups.value.map((pod) => ({ ...pod, logs: [], lineCount: 0 }))
  }
  runtimeLogDroppedRows.value = 0
  runtimeLogScrollTop.value = 0
  if (shouldRestart) {
    openRuntimeLogStream(true)
  }
}

function handleRuntimeLogScroll(event: Event) {
  runtimeLogScrollTop.value = (event.target as HTMLElement | null)?.scrollTop ?? 0
}

async function scrollRuntimeLogsToBottom() {
  await nextTick()
  await nextFrame()
  const viewport = runtimeLogViewport.value
  if (viewport) {
    setRuntimeLogScrollBottom(viewport)
  }
  await nextTick()
  await nextFrame()
  if (runtimeLogViewport.value) {
    setRuntimeLogScrollBottom(runtimeLogViewport.value)
  }
}

function setRuntimeLogScrollBottom(viewport: HTMLElement) {
  const maxScroll = Math.max(0, viewport.scrollHeight - viewport.clientHeight)
  viewport.scrollTop = maxScroll
  runtimeLogScrollTop.value = maxScroll
}

function nextFrame() {
  return new Promise<void>((resolve) => {
    window.requestAnimationFrame(() => resolve())
  })
}

function clearRuntimeLogServiceFilter() {
  runtimeLogServiceFilter.value = []
}

async function loadActive(force = false) {
  return withLoading(async () => {
    if (tab.value === 'overview') {
      await loadSummary(true, force)
    } else if (tab.value === 'aifar-runtime') {
      if (runtimeResourceTab.value === 'logs') {
        loadRuntimeLogs(force)
      } else {
        await loadAifarRuntime(force)
      }
    } else {
      await loadCollection(force)
    }
  })
}

function trackTask(taskId?: string, label = '') {
  if (taskId) {
    taskProgress.track(taskId, label)
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
    const result = await apiPost<{ taskId?: string }>(`/containers/${encodeURIComponent(id)}/${action}?${query}`)
    trackTask(result.taskId, containerDisplayName(row) || id)
    ElMessage.success(t('containers.actionAccepted'))
    setTimeout(() => {
      void load(true)
    }, 800)
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
    const result = await apiPost<{ taskId?: string }>(`/containers/actions?${query}`, { action, ids })
    trackTask(result.taskId, t('containers.batchActionAccepted'))
    ElMessage.success(t('containers.batchActionAccepted'))
    selectedContainerRows.value = []
    setTimeout(() => {
      void load(true)
    }, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.actionFailed'))
  }
}

async function deleteImage(row: any) {
  await removeImages([row], 'single')
}

async function deleteSelectedImages() {
  await removeImages(selectedImageRows.value, 'batch')
}

async function removeImages(rows: any[], mode: 'single' | 'batch') {
  if (!canManageContainers.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  const ids = uniqueValues(rows.map(imageReference).filter(Boolean))
  if (!ids.length) {
    ElMessage.warning(mode === 'batch' ? t('containers.selectImages') : t('containers.selectImage'))
    return
  }
  try {
    const message = mode === 'batch'
      ? t('containers.confirmDeleteSelectedImages', { count: ids.length })
      : t('containers.confirmDeleteImage', { image: ids[0] })
    const title = mode === 'batch' ? t('containers.batchDeleteImages') : t('containers.deleteImage')
    await ElMessageBox.confirm(message, title, {
      type: 'warning',
      confirmButtonText: title,
      cancelButtonText: t('common.cancel')
    })
  } catch {
    return
  }
  try {
    const result = await apiPost<{ taskId?: string }>(`/containers/images/remove?${query}`, mode === 'batch' ? { ids } : { id: ids[0] })
    trackTask(result.taskId, mode === 'batch' ? t('containers.batchDeleteImages') : t('containers.deleteImage'))
    ElMessage.success(mode === 'batch' ? t('containers.imageBatchRemoveAccepted') : t('containers.imageRemoveAccepted'))
    selectedImageRows.value = []
    setTimeout(() => {
      void load(true)
    }, 800)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.imageRemoveFailed'))
  }
}

function openLogs(id: string) {
  const query = targetQuery()
  if (!query) {
    ElMessage.warning(t('containers.selectDockerHost'))
    return
  }
  closeContainerLogStream()
  logsText.value = ''
  logsVisible.value = true
  const params = new URLSearchParams(query)
  params.set('tail', '300')
  params.set('batch', '200')
  const source = new EventSource(apiEventSourceUrl(`/containers/${encodeURIComponent(id)}/logs/events?${params.toString()}`))
  containerLogSource = source
  const applySnapshot = (event: Event) => {
    if (containerLogSource !== source) return
    const next = parseContainerLogsEvent((event as MessageEvent).data)
    if (!next) return
    setContainerLogLines(next.logs)
  }
  const applyBatch = (event: Event) => {
    if (containerLogSource !== source) return
    const next = parseContainerLogsEvent((event as MessageEvent).data)
    if (!next) return
    appendContainerLogLines(next.logs)
    if (next.warnings?.length) {
      appendContainerLogLines(next.warnings)
    }
  }
  source.addEventListener('container-logs-snapshot', applySnapshot)
  source.addEventListener('container-logs-batch', applyBatch)
  source.addEventListener('container-logs-error', (event) => {
    if (containerLogSource !== source) return
    const message = parseRuntimeLogErrorEvent((event as MessageEvent).data)
    if (message) {
      appendContainerLogLines([message])
    }
  })
}

function closeContainerLogStream() {
  if (containerLogSource) {
    containerLogSource.close()
    containerLogSource = null
  }
}

function parseContainerLogsEvent(raw: string) {
  try {
    return JSON.parse(raw) as { logs?: string[]; warnings?: string[] }
  } catch {
    return null
  }
}

function setContainerLogLines(lines?: string[]) {
  const next = asArray<string>(lines)
  logsText.value = next.slice(-containerLogMaxLines).join('\n')
}

function appendContainerLogLines(lines?: string[]) {
  const next = asArray<string>(lines)
  if (!next.length) return
  const current = logsText.value ? logsText.value.split('\n') : []
  logsText.value = current.concat(next).slice(-containerLogMaxLines).join('\n')
}

function parseRuntimeLogLine(line: string) {
  const raw = String(line ?? '')
  const match = raw.match(/^(\d{4}-\d{2}-\d{2}[T ][^\s]+)\s+(?:(TRACE|DEBUG|INFO|WARN|WARNING|ERROR|FATAL|SEVERE)\s+)?(.*)$/i)
  if (!match) {
    return {
      time: '-',
      timestamp: 0,
      level: detectRuntimeLogLevel(raw),
      message: raw
    }
  }
  const time = match[1]
  const parsedTime = Date.parse(time)
  return {
    time,
    timestamp: Number.isFinite(parsedTime) ? parsedTime : 0,
    level: (match[2] || detectRuntimeLogLevel(raw)).toUpperCase(),
    message: (match[3] || raw).trim() || raw
  }
}

function detectRuntimeLogLevel(line: string) {
  const upper = String(line || '').toUpperCase()
  if (/\b(FATAL|SEVERE|ERROR)\b/.test(upper)) return 'ERROR'
  if (/\b(WARN|WARNING)\b/.test(upper)) return 'WARN'
  if (/\bINFO\b/.test(upper)) return 'INFO'
  if (/\b(DEBUG|TRACE)\b/.test(upper)) return 'DEBUG'
  return ''
}

function runtimeLogLevelTag(level: string) {
  switch (String(level || '').toUpperCase()) {
    case 'ERROR':
    case 'FATAL':
    case 'SEVERE':
      return 'danger'
    case 'WARN':
    case 'WARNING':
      return 'warning'
    case 'INFO':
      return 'success'
    case 'DEBUG':
    case 'TRACE':
      return 'info'
    default:
      return ''
  }
}

function runtimeDiscoveryTarget(row: AifarRuntimeService) {
  return row.proxyName || row.appName || row.serviceName || '-'
}

function onContainerSelectionChange(rows: any[]) {
  selectedContainerRows.value = rows.filter(containerRowSelectable)
}

function onImageSelectionChange(rows: any[]) {
  selectedImageRows.value = rows.filter((row) => imageReference(row))
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
    case 'offline':
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

function runtimeEndpointText(row: AifarRuntimeService) {
  const ready = Number(row.readyEndpointCount ?? row.activeEndpoints ?? row.readyReplicas ?? 0)
  const total = Number(row.endpointCount ?? row.activeEndpoints ?? ready)
  return `${Number.isFinite(ready) ? ready : 0} / ${Number.isFinite(total) ? total : 0}`
}

function runtimeDeploymentReplicaText(row: AifarRuntimeDeployment) {
  const ready = Number(row.readyReplicas ?? row.availableReplicas ?? 0)
  const desired = Number(row.desiredReplicas ?? 0)
  const updated = Number(row.updatedReplicas ?? 0)
  const base = `${Number.isFinite(ready) ? ready : 0} / ${Number.isFinite(desired) ? desired : 0}`
  if (updated > 0 && updated !== ready) {
    return `${base} (${updated})`
  }
  return base
}

function runtimeServiceForDeployment(row: AifarRuntimeDeployment): AifarRuntimeService {
  const existing = runtimeServiceMap.value.get(row.serviceName)
  if (existing) {
    return existing
  }
  return {
    instanceId: row.instanceId,
    serviceName: row.serviceName,
    appName: row.appName || row.deploymentName || row.serviceName,
    desiredReplicas: row.desiredReplicas,
    readyReplicas: row.readyReplicas,
    image: row.image,
    status: row.status,
    rolloutStatus: row.status,
    failureReason: row.failureReason
  }
}

function runtimeNacosStatus(row: AifarRuntimeService) {
  if (row.nacosReady) {
    return 'ready'
  }
  if (row.lastNacosError) {
    return 'failed'
  }
  if (row.nacosRegistered) {
    return 'running'
  }
  if (row.status === 'offline') {
    return 'offline'
  }
  return 'unknown'
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
    nacosEphemeral: true,
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
  runtimeConfigForm.value = { ...global, nacosEphemeral: state.nacosEphemeral !== false }
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
    trackTask(result.taskId, t('apps.updateService'))
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
      services,
      nacosEphemeral: runtimeConfigForm.value.nacosEphemeral
    })
    runtimeConfigVisible.value = false
    ElMessage.success(t('containers.runtimeConfigApplyStarted'))
    trackTask(result.taskId, t('containers.runtimeConfig'))
    void loadAifarRuntime(true)
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
    trackTask(result.taskId, t('containers.reconcileRuntime'))
    void loadAifarRuntime(true)
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
    trackTask(result.taskId, t('containers.cleanupStaleRuntime'))
    void loadAifarRuntime(true)
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
    trackTask(result.taskId, t('containers.uninstallAgent'))
    void loadAifarRuntime(true)
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
    trackTask(result.taskId, t('containers.installServices'))
    void loadAifarRuntime(true)
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
    void loadAifarRuntime(true)
  })
}

function aifarRuntimeScaleInDisabledReason(row: AifarRuntimeDeployment) {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  const desired = Number(row.desiredReplicas ?? 0)
  if (!Number.isFinite(desired) || desired <= 0) return t('containers.serviceAlreadyOffline')
  if (desired <= 1) return t('containers.serviceScaleInMinimum')
  return ''
}

async function scaleInAifarDeployment(row: AifarRuntimeDeployment) {
  const reason = aifarRuntimeScaleInDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  const currentReplicas = Number(row.desiredReplicas ?? 0)
  await submitAifarScaleIn(row.serviceName, instanceId, currentReplicas, currentReplicas - 1, () => {
    void loadAifarRuntime(true)
  })
}

function aifarRuntimeOfflineDisabledReason(row: AifarRuntimeService) {
  if (aifarRuntimeActionDisabledReason.value) return aifarRuntimeActionDisabledReason.value
  if (Number(row.desiredReplicas || 0) === 0) return t('containers.serviceAlreadyOffline')
  return ''
}

async function offlineAifarService(row: AifarRuntimeService) {
  const reason = aifarRuntimeOfflineDisabledReason(row)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const instanceId = selectedRuntimeInstance.value?.id
  if (!instanceId) {
    ElMessage.warning(t('containers.selectAifarInstance'))
    return
  }
  await submitAifarOffline(row.serviceName, instanceId, () => {
    void loadAifarRuntime(true)
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
    trackTask(result.taskId, `${t('containers.scaleOut')} ${service}`)
    afterSubmitted?.()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function submitAifarScaleIn(service: string, instanceId: string, currentReplicas: number, nextReplicas: number, afterSubmitted?: () => void) {
  try {
    await ElMessageBox.confirm(t('containers.confirmScaleIn', { service, current: currentReplicas, next: nextReplicas }), t('containers.scaleIn'), {
      type: 'warning',
      confirmButtonText: t('containers.scaleIn'),
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
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/services/${encodeURIComponent(service)}/scale-in?${query}`, { instanceId })
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, `${t('containers.scaleIn')} ${service}`)
    afterSubmitted?.()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('containers.runtimeActionFailed'))
  }
}

async function submitAifarOffline(service: string, instanceId: string, afterSubmitted?: () => void) {
  try {
    await ElMessageBox.confirm(t('containers.confirmOfflineDeployment', { service }), t('containers.offlineDeployment'), {
      type: 'warning',
      confirmButtonText: t('containers.offlineDeployment'),
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
    const result = await apiPost<{ taskId: string }>(`/containers/aifar/services/${encodeURIComponent(service)}/offline?${query}`, { instanceId })
    ElMessage.success(t('containers.runtimeActionAccepted'))
    trackTask(result.taskId, `${t('containers.offlineDeployment')} ${service}`)
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

function imageRowKey(row: any) {
  return imageReference(row) || `${String(row?.repository ?? '').trim()}:${String(row?.tag ?? '').trim()}:${String(row?.id ?? '').trim()}`
}

function uniqueValues(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const next = value.trim()
    if (!next || seen.has(next)) continue
    seen.add(next)
    out.push(next)
  }
  return out
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
  deletePromptInstance.value = selectedDockerInstance.value
  deletePromptVisible.value = true
}

function openAifarAppUninstall() {
  const reason = aifarAppUninstallDisabledReason.value
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  deletePromptInstance.value = selectedRuntimeAppInstance.value
  deletePromptVisible.value = true
}

async function confirmAppUninstall(password: string) {
  const instance = deletePromptInstance.value
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
    deletePromptInstance.value = null
    ElMessage.success(t('apps.uninstallServiceAccepted'))
    trackTask(result.taskId, instance.app === 'aifar' ? t('containers.uninstallAifarApp') : t('apps.uninstallService'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    deleteSubmitting.value = false
  }
}

watch(tab, (next) => {
  if (next !== 'aifar-runtime') {
    closeRuntimeLogStream()
  }
  void loadActive(false)
})
watch(resourceTab, () => {
  if (tab.value === 'images') {
    void loadActive(false)
  }
})
watch(runtimeResourceTab, (next) => {
  if (next === 'pods') {
    void ensureRuntimePodsLoaded(false)
  } else if (next === 'logs') {
    void ensureRuntimePodsLoaded(false)
    if (runtimeLogSelectionReady.value) {
      void loadRuntimeLogs(false)
    }
  } else {
    closeRuntimeLogStream()
  }
})
watch(selectedRuntimeInstanceId, () => {
  runtimePodServiceFilter.value = ''
  runtimeLogServiceFilter.value = []
  runtimeLogPodFilter.value = []
  runtimeLogLevelFilter.value = []
  runtimeLogKeyword.value = ''
  runtimeLogSinceSeconds.value = 0
  resetRuntimeLogView()
  runtimeLogsLoaded.value = {}
  closeRuntimeLogStream()
  if (runtimeResourceTab.value === 'logs') {
    void ensureRuntimePodsLoaded(false)
  }
})
watch(runtimeLogServiceFilter, () => {
  const validPods = new Set(runtimeLogPodOptions.value.map((item) => item.value))
  runtimeLogPodFilter.value = runtimeLogPodFilter.value.filter((pod) => validPods.has(pod))
}, { deep: true })
watch([runtimeLogServiceFilter, runtimeLogPodFilter, runtimeLogTail], () => {
  runtimeLogSinceSeconds.value = 0
  resetRuntimeLogView()
  runtimeLogsLoaded.value = {}
  closeRuntimeLogStream()
  if (runtimeResourceTab.value === 'logs' && runtimeLogSelectionReady.value) {
    loadRuntimeLogs(true)
  }
}, { deep: true })
watch(
  () => [filteredRuntimeLogRows.value.length, runtimeLogLevelFilter.value.join(','), runtimeLogKeyword.value],
  () => {
    if (runtimeLogAutoScroll.value) {
      void scrollRuntimeLogsToBottom()
    }
  },
  { flush: 'post' }
)
watch(() => realtime.revision, () => {
  const event = realtime.lastEvent
  if (!event?.resource) {
    return
  }
  if (event.resource === 'server') {
    void loadServers()
    return
  }
  if (event.resource === 'docker.summary' && event.serverId === selectedServerId.value) {
    if (!applyDockerSummaryEvent(event)) {
      summaryCache.value = {}
    }
    return
  }
  if (event.resource === 'aifar.runtime' && tab.value === 'aifar-runtime') {
    if (event.serverId && event.serverId !== selectedServerId.value) {
      return
    }
    runtimeCache.value = {}
  }
})
watch(deletePromptVisible, (visible) => {
  if (!visible && !deleteSubmitting.value) {
    deletePromptInstance.value = null
  }
})
watch(logsVisible, (visible) => {
  if (!visible) {
    closeContainerLogStream()
  }
})
watch([aifarUpdateService, aifarUpdateMode], () => {
  aifarArtifactFile.value = null
})
watch(selectedServerId, () => {
  runtimeLogServiceFilter.value = []
  runtimeLogPodFilter.value = []
  runtimeLogLevelFilter.value = []
  runtimeLogKeyword.value = ''
  runtimeLogSinceSeconds.value = 0
  resetRuntimeLogView()
  runtimeLogsLoaded.value = {}
  closeRuntimeLogStream()
  if (pageReady.value) {
    void load(true)
  }
})
onMounted(async () => {
  await loadServers()
  pageReady.value = true
  await load()
})
onBeforeUnmount(() => {
  closeContainerLogStream()
  closeRuntimeLogStream()
})
</script>

<style scoped>
.containers-page.is-runtime-logs-page {
  overflow: hidden;
  padding-right: 0;
}

.containers-main {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  padding: 10px;
}

.workspace-card.containers-main.is-runtime-logs {
  overflow: hidden;
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

.resource-tabs {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.resource-tabs :deep(.el-tabs__content) {
  flex: 1 1 auto;
  min-height: 0;
}

.resource-tabs :deep(.el-tab-pane) {
  height: 100%;
}

.resource-panel {
  height: 100%;
  min-height: 360px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.runtime-workspace {
  display: flex;
  flex: 1 1 auto;
  min-height: 0;
  flex-direction: column;
  gap: 10px;
  overflow: hidden;
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

.runtime-resource-tabs {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
}

.runtime-resource-tabs :deep(.el-tabs__header) {
  flex: 0 0 auto;
}

.runtime-resource-tabs :deep(.el-tabs__content) {
  flex: 1 1 auto;
  min-height: 0;
  height: 100%;
  overflow: hidden;
}

.runtime-resource-tabs :deep(.el-tab-pane) {
  height: 100%;
  min-height: 0;
}

.runtime-resource-panel {
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.runtime-log-panel {
  min-height: 0;
  overflow: hidden;
}

.runtime-log-panel .runtime-tab-toolbar,
.runtime-log-panel > .el-alert {
  flex: 0 0 auto;
}

.runtime-tab-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
  min-height: 32px;
}

.runtime-tab-actions {
  display: flex;
  align-items: center;
  gap: 8px;
}

.runtime-log-filters {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.runtime-service-filter {
  width: 180px;
}

.runtime-pod-filter {
  width: min(360px, 28vw);
}

.runtime-level-filter {
  width: 150px;
}

.runtime-log-keyword {
  width: min(260px, 20vw);
}

.runtime-log-tail {
  width: 128px;
}

.runtime-log-pod-option {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.runtime-log-pod-option span:first-child {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-log-workbench {
  flex: 1 1 auto;
  min-height: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
  overflow: hidden;
}

.runtime-log-pod-strip {
  flex: 0 0 auto;
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  max-height: 72px;
  overflow: auto;
}

.runtime-log-pod-chip {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  max-width: 360px;
  padding: 6px 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #fff;
}

.runtime-log-pod-chip > div {
  display: grid;
  min-width: 0;
}

.runtime-log-pod-chip strong {
  color: var(--aifar-ink);
  font-size: 12px;
  line-height: 18px;
}

.runtime-log-pod-chip span {
  min-width: 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  line-height: 18px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-log-stats {
  flex: 0 0 auto;
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 24px;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.runtime-log-virtual-list {
  flex: 1 1 auto;
  min-height: 0;
  height: 100%;
  max-height: 100%;
  overflow: auto;
  overscroll-behavior: contain;
  scrollbar-gutter: stable;
  scrollbar-width: thin;
  scrollbar-color: rgba(148, 163, 184, 0.55) rgba(15, 23, 42, 0.35);
  border: 1px solid #17243a;
  border-radius: var(--aifar-radius);
  background: #08111f;
}

.runtime-log-virtual-list::-webkit-scrollbar {
  width: 10px;
  height: 10px;
}

.runtime-log-virtual-list::-webkit-scrollbar-thumb {
  border: 2px solid #08111f;
  border-radius: 999px;
  background: rgba(148, 163, 184, 0.6);
}

.runtime-log-virtual-list::-webkit-scrollbar-track {
  background: rgba(15, 23, 42, 0.35);
}

.runtime-log-virtual-list.is-empty {
  display: grid;
  place-items: center;
}

.runtime-log-empty-state {
  display: grid;
  place-items: center;
  min-height: 100%;
  color: rgba(203, 213, 225, 0.62);
  font-size: 13px;
  line-height: 20px;
}

.runtime-log-row {
  display: grid;
  grid-template-columns: 190px 108px 260px 72px max-content;
  gap: 8px;
  align-items: center;
  width: max-content;
  min-width: 100%;
  height: 32px;
  min-height: 32px;
  box-sizing: border-box;
  padding: 6px 10px;
  border-bottom: 1px solid rgba(148, 163, 184, 0.14);
  color: #dbeafe;
  font-family: ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace;
  font-size: 12px;
  line-height: 20px;
}

.runtime-log-time {
  color: #93a4ba;
  white-space: nowrap;
}

.runtime-log-service,
.runtime-log-pod {
  min-width: 0;
  overflow: hidden;
  color: #c7d2fe;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-log-pod {
  color: #bfdbfe;
}

.runtime-log-level {
  justify-self: start;
  min-width: 52px;
  padding: 0 6px;
  border-radius: 4px;
  background: rgba(148, 163, 184, 0.16);
  color: #cbd5e1;
  text-align: center;
  font-weight: 700;
  font-size: 11px;
  line-height: 20px;
}

.runtime-log-level.is-danger {
  background: rgba(239, 68, 68, 0.2);
  color: #fecaca;
}

.runtime-log-level.is-warning {
  background: rgba(245, 158, 11, 0.2);
  color: #fde68a;
}

.runtime-log-level.is-success {
  background: rgba(34, 197, 94, 0.18);
  color: #bbf7d0;
}

.runtime-log-level.is-info {
  background: rgba(59, 130, 246, 0.2);
  color: #bfdbfe;
}

.runtime-log-message {
  min-width: 560px;
  overflow: visible;
  word-break: normal;
  white-space: pre;
}

.runtime-entry-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
}

.runtime-entry-card {
  display: grid;
  gap: 6px;
  min-width: 0;
  padding: 10px 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #fff;
}

.runtime-entry-card-head {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 8px;
}

.runtime-entry-card strong {
  color: var(--aifar-ink);
  font-size: 13px;
  line-height: 20px;
}

.runtime-entry-card span {
  min-width: 0;
  color: var(--aifar-text);
  font-size: 13px;
  line-height: 20px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.runtime-entry-card small {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 18px;
}

.runtime-lazy-state {
  flex: 1 1 auto;
  min-height: 220px;
  display: grid;
  place-items: center;
  border: 1px dashed var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f8fbff;
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

  .runtime-tab-toolbar {
    align-items: stretch;
    flex-direction: column;
  }

  .runtime-log-filters {
    width: 100%;
    flex-wrap: wrap;
  }

  .runtime-service-filter,
  .runtime-pod-filter,
  .runtime-level-filter,
  .runtime-log-keyword,
  .runtime-log-tail {
    width: 100%;
  }

  .runtime-log-virtual-list {
    min-height: 320px;
  }

  .runtime-log-row {
    grid-template-columns: 160px 96px 220px 72px max-content;
    min-width: 100%;
  }

  .runtime-log-message {
    min-width: 420px;
  }

  .runtime-log-pod-chip {
    max-width: 100%;
    width: 100%;
  }

  .runtime-entry-grid {
    grid-template-columns: 1fr;
  }

  .runtime-instance-select {
    width: 100%;
  }

  .runtime-config-form {
    grid-template-columns: 1fr;
  }
}
</style>
