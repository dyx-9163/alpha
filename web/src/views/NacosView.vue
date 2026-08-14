<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('nacos.title') }}</h1>
        <p class="page-subtitle">{{ t('nacos.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <span v-if="visibleManagementHeaderActions.nacos.includes('connected')" class="status-pill success">{{ t('common.connected') }}</span>
      </div>
    </div>

    <div class="aifar-panel status-line">
      <span class="subtle-note">{{ t('nacos.instanceCount', { count: instances.length }) }}</span>
      <span class="status-pill" :class="{ success: canManageApps }">{{ monitoringStatusLabel }}</span>
      <span v-if="lastMonitorAt" class="subtle-note">{{ t('nacos.lastMonitoredAt') }} {{ lastMonitorAt }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane v-for="tabName in visibleManagementTabs.nacos" :key="tabName" :label="t('nacos.instances')" :name="tabName" />
    </el-tabs>

    <div class="workspace-card nacos-main">
      <template v-if="tab === 'instances'">
        <div class="table-toolbar">
          <div class="head-actions">
            <span class="status-pill">{{ t('common.all') }} {{ nacosGroups.length }}</span>
            <span class="status-pill">{{ t('nacos.cluster') }} {{ clusterGroupCount }}</span>
            <span class="status-pill">{{ t('nacos.standalone') }} {{ standaloneGroupCount }}</span>
            <span class="status-pill">{{ t('nacos.nodes') }} {{ nacosNodeCount }}</span>
          </div>
          <div class="monitor-actions">
            <el-input v-model="search" :placeholder="t('nacos.search')" clearable class="toolbar-control is-sm" />
          </div>
        </div>

        <div v-if="filteredGroups.length" class="nacos-resource-shell">
          <div class="nacos-resource-list">
          <article
            v-for="group in filteredGroups"
            :key="group.id"
            class="nacos-resource-row"
            :class="{ active: activeNacosWorkbenchGroup?.id === group.id }"
            @click="selectNacosResource(group)"
          >
            <div class="nacos-head">
              <div class="app-icon small">NA</div>
              <div class="nacos-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ group.topology }} / {{ t('nacos.nodes') }} {{ group.nodes.length }}</span>
                <div class="nacos-summary">
                  <span><strong>{{ t('common.version') }}</strong>{{ group.version || '-' }}</span>
                  <span><strong>{{ t('dashboard.topology') }}</strong>{{ group.topology || '-' }}</span>
                  <span class="nacos-summary-wide"><strong>{{ t('nacos.consoleEndpoint') }}</strong>{{ group.endpoint || '-' }}</span>
                </div>
              </div>
              <div class="nacos-head-actions">
                <StatusTag :status="group.status" />
                <el-button size="small" @click.stop="openNacosResource(group)">{{ t('common.details') }}</el-button>
              </div>
            </div>
          </article>
          </div>
          <aside v-if="activeNacosWorkbenchGroup" class="resource-inline-detail nacos-inline-detail">
            <div class="nacos-head detail-head">
              <div class="app-icon small">NA</div>
              <div class="nacos-title-block">
                <strong>{{ activeNacosWorkbenchGroup.title }}</strong>
                <span>{{ activeNacosWorkbenchGroup.topology }} / {{ t('nacos.nodes') }} {{ activeNacosWorkbenchGroup.nodes.length }}</span>
              </div>
              <div class="nacos-head-actions">
                <StatusTag :status="activeNacosWorkbenchGroup.status" />
              </div>
            </div>
            <div class="nacos-summary detail-summary">
              <span><strong>{{ t('common.version') }}</strong>{{ activeNacosWorkbenchGroup.version || '-' }}</span>
              <span><strong>{{ t('dashboard.topology') }}</strong>{{ activeNacosWorkbenchGroup.topology || '-' }}</span>
              <span><strong>{{ t('nacos.nodes') }}</strong>{{ activeNacosWorkbenchGroup.nodes.length }}</span>
              <span><strong>{{ t('nacos.auth') }}</strong>{{ activeNacosWorkbenchGroup.authLabel }}</span>
              <span v-if="activeNacosWorkbenchGroup.raftPort"><strong>{{ t('nacos.raftPort') }}</strong>{{ activeNacosWorkbenchGroup.raftPort }}</span>
              <span class="nacos-summary-wide"><strong>{{ t('nacos.consoleEndpoint') }}</strong>{{ activeNacosWorkbenchGroup.endpoint || '-' }}</span>
            </div>
            <div class="nacos-node-list">
              <div class="section-label">{{ t('nacos.nodes') }}</div>
              <div v-for="node in activeNacosWorkbenchGroup.nodes" :key="node.instance.id" class="nacos-node-row">
                <div class="nacos-node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint }}</span>
                </div>
                <div class="nacos-node-tags">
                  <el-tag size="small" type="info" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" type="info" effect="plain">{{ node.grpcPorts }}</el-tag>
                  <StatusTag :status="node.status" />
                </div>
              </div>
            </div>
            <div class="inline-detail-actions">
              <el-button size="small" @click="openNacosResource(activeNacosWorkbenchGroup)">{{ t('common.details') }}</el-button>
            </div>
          </aside>
        </div>
        <div v-else class="empty-state">
          <div>
            <strong>{{ t('nacos.noInstancesTitle') }}</strong>
            <span>{{ t('nacos.noInstancesDesc') }}</span>
          </div>
        </div>
      </template>

      <template v-else-if="tab === 'configs'">
        <div class="config-publish-grid">
          <div class="config-form-panel">
            <div class="config-section-head">
              <strong>{{ t('nacos.configTarget') }}</strong>
              <el-tag size="small" effect="plain">{{ configForm.namespace }} / {{ configForm.group }}</el-tag>
            </div>
            <el-form label-position="top" class="config-form">
              <el-form-item :label="t('nacos.configNacosInstance')">
                <el-select v-model="configForm.nacosInstanceId" filterable class="full-width" :placeholder="t('nacos.selectNacosInstance')" @change="handleConfigNacosChange">
                  <el-option v-for="option in nacosConfigInstanceOptions" :key="option.value" :label="option.label" :value="option.value" />
                </el-select>
              </el-form-item>
              <el-form-item :label="t('nacos.configNacosCredential')">
                <el-select v-model="configForm.nacosCredentialId" filterable class="full-width" :placeholder="t('nacos.selectCredential')">
                  <el-option v-for="credential in nacosCredentials" :key="credential.id" :label="credentialLabel(credential)" :value="credential.id" />
                </el-select>
              </el-form-item>
              <div class="config-field-row">
                <el-form-item :label="t('nacos.namespace')">
                  <el-input v-model="configForm.namespace" />
                </el-form-item>
                <el-form-item :label="t('nacos.group')">
                  <el-input v-model="configForm.group" />
                </el-form-item>
              </div>
              <el-form-item :label="t('nacos.dataId')">
                <el-input v-model="configForm.dataId" />
              </el-form-item>

              <div class="config-section-toggle">
                <el-checkbox v-model="configForm.includeDatasource">{{ t('nacos.configDatasource') }}</el-checkbox>
              </div>
              <template v-if="configForm.includeDatasource">
                <el-form-item :label="t('nacos.configMysqlInstance')">
                  <el-select v-model="configForm.mysqlInstanceId" filterable class="full-width" :placeholder="t('nacos.selectMysqlInstance')" @change="handleConfigMysqlChange">
                    <el-option v-for="instance in mysqlConfigInstances" :key="instance.id" :label="databaseInstanceLabel(instance)" :value="instance.id" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('nacos.configMysqlCredential')">
                  <el-select v-model="configForm.mysqlCredentialId" filterable class="full-width" :placeholder="t('nacos.selectCredential')">
                    <el-option v-for="credential in mysqlCredentials" :key="credential.id" :label="credentialLabel(credential)" :value="credential.id" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('nacos.databaseName')">
                  <el-input v-model="configForm.databaseName" />
                </el-form-item>
              </template>

              <div class="config-section-toggle">
                <el-checkbox v-model="configForm.includeRedis">{{ t('nacos.configRedis') }}</el-checkbox>
              </div>
              <template v-if="configForm.includeRedis">
                <el-form-item :label="t('nacos.configRedisInstance')">
                  <el-select v-model="configForm.redisInstanceId" filterable class="full-width" :placeholder="t('nacos.selectRedisInstance')" @change="handleConfigRedisChange">
                    <el-option v-for="instance in redisConfigInstances" :key="instance.id" :label="databaseInstanceLabel(instance)" :value="instance.id" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('nacos.configRedisCredential')">
                  <el-select v-model="configForm.redisCredentialId" filterable class="full-width" :placeholder="t('nacos.selectCredential')">
                    <el-option v-for="credential in redisCredentials" :key="credential.id" :label="credentialLabel(credential)" :value="credential.id" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('nacos.redisDatabase')">
                  <el-input-number v-model="configForm.redisDatabase" :min="0" :max="15" controls-position="right" class="full-width" />
                </el-form-item>
              </template>

              <div class="config-section-toggle">
                <el-checkbox v-model="configForm.includeMinio">{{ t('nacos.configMinio') }}</el-checkbox>
              </div>
              <template v-if="configForm.includeMinio">
                <el-form-item :label="t('nacos.configMinioInstance')">
                  <el-select v-model="configForm.minioInstanceId" filterable class="full-width" :placeholder="t('nacos.selectMinioInstance')" @change="handleConfigMinioChange">
                    <el-option v-for="instance in minioConfigInstances" :key="instance.id" :label="storageInstanceLabel(instance)" :value="instance.id" />
                  </el-select>
                </el-form-item>
                <el-form-item :label="t('nacos.configMinioCredential')">
                  <el-select v-model="configForm.minioCredentialId" filterable class="full-width" :placeholder="t('nacos.selectCredential')">
                    <el-option v-for="credential in minioCredentials" :key="credential.id" :label="credentialLabel(credential)" :value="credential.id" />
                  </el-select>
                </el-form-item>
                <div class="config-field-row">
                  <el-form-item :label="t('nacos.minioBucket')">
                    <el-input v-model="configForm.minioBucket" />
                  </el-form-item>
                  <el-form-item :label="t('nacos.minioPlatform')">
                    <el-input v-model="configForm.minioPlatform" />
                  </el-form-item>
                </div>
              </template>
            </el-form>
          </div>

          <div class="config-preview-panel">
            <div class="config-preview-toolbar">
              <div class="config-summary">
                <el-tag :type="configPreview?.changed ? 'warning' : 'success'" effect="plain">
                  {{ configPreview ? (configPreview.changed ? t('nacos.configChanged') : t('nacos.configUnchanged')) : t('nacos.configNotPreviewed') }}
                </el-tag>
                <span v-for="item in configSummary" :key="item">{{ item }}</span>
              </div>
              <div class="head-actions">
                <el-button :loading="configPreviewLoading" :disabled="!canManageApps" @click="previewNacosConfig">{{ t('nacos.previewConfig') }}</el-button>
                <el-button type="primary" :loading="configPublishLoading" :disabled="!canManageApps" @click="publishNacosConfig">{{ t('nacos.publishConfig') }}</el-button>
              </div>
            </div>
            <div class="config-code-grid">
              <div>
                <div class="section-label">{{ t('nacos.generatedConfig') }}</div>
                <pre class="config-code">{{ configPreview?.generated || t('common.noData') }}</pre>
              </div>
              <div>
                <div class="section-label">{{ t('nacos.currentConfig') }}</div>
                <pre class="config-code">{{ configPreview?.current || t('common.noData') }}</pre>
              </div>
            </div>
            <div class="config-revision-head">
              <strong>{{ t('nacos.configRevisions') }}</strong>
              <span class="subtle-note">{{ t('nacos.configRevisionKeep') }}</span>
            </div>
            <el-table :data="configRevisions" size="small" class="config-revision-table" :empty-text="t('common.noData')">
              <el-table-column prop="dataId" :label="t('nacos.dataId')" min-width="180" />
              <el-table-column prop="contentHash" :label="t('nacos.contentHash')" min-width="160">
                <template #default="{ row }">{{ shortHash(row.contentHash) }}</template>
              </el-table-column>
              <el-table-column prop="createdBy" :label="t('common.actor')" width="120" />
              <el-table-column prop="publishedAt" :label="t('common.time')" width="190" />
              <el-table-column :label="t('common.operation')" width="110" fixed="right">
                <template #default="{ row }">
                  <el-button size="small" :loading="configRollbackId === row.id" :disabled="!canManageApps" @click="rollbackNacosConfig(row)">{{ t('nacos.rollbackConfig') }}</el-button>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </template>

      <template v-else-if="tab === 'runs'">
        <RunRecordTable :records="runTasks" show-details @details="openTaskDetails" />
      </template>

      <template v-else>
        <div class="settings-grid">
          <KeyValueGrid :items="settingsItems" />
        </div>
      </template>
    </div>

    <el-drawer
      v-model="nacosDetailVisible"
      :title="activeNacosGroup?.title || t('nacos.instances')"
      size="min(720px, calc(100vw - 24px))"
      class="nacos-detail-drawer"
    >
      <div v-if="activeNacosGroup" class="resource-detail-stack">
        <div class="nacos-head detail-head">
          <div class="app-icon small">NA</div>
          <div class="nacos-title-block">
            <strong>{{ activeNacosGroup.title }}</strong>
            <span>{{ activeNacosGroup.topology }} / {{ t('nacos.nodes') }} {{ activeNacosGroup.nodes.length }}</span>
          </div>
          <div class="nacos-head-actions">
            <StatusTag :status="activeNacosGroup.status" />
          </div>
        </div>
        <div class="nacos-summary detail-summary">
          <span><strong>{{ t('common.version') }}</strong>{{ activeNacosGroup.version || '-' }}</span>
          <span><strong>{{ t('dashboard.topology') }}</strong>{{ activeNacosGroup.topology || '-' }}</span>
          <span><strong>{{ t('nacos.nodes') }}</strong>{{ activeNacosGroup.nodes.length }}</span>
          <span><strong>{{ t('nacos.auth') }}</strong>{{ activeNacosGroup.authLabel }}</span>
          <span v-if="activeNacosGroup.raftPort"><strong>{{ t('nacos.raftPort') }}</strong>{{ activeNacosGroup.raftPort }}</span>
          <span class="nacos-summary-wide"><strong>{{ t('nacos.consoleEndpoint') }}</strong>{{ activeNacosGroup.endpoint || '-' }}</span>
          <span class="nacos-summary-wide"><strong>{{ t('nacos.externalDatabase') }}</strong>{{ activeNacosGroup.database || t('nacos.noDatabase') }}</span>
        </div>
        <div v-if="isInstallFailedGroup(activeNacosGroup)" class="service-notice danger">{{ t('apps.installFailedCleanupHint') }}</div>
        <div class="nacos-node-list">
          <div class="section-label">{{ t('nacos.nodes') }}</div>
          <div v-for="node in activeNacosGroup.nodes" :key="node.instance.id" class="nacos-node-row">
            <div class="nacos-node-main">
              <strong>{{ node.serverLabel }}</strong>
              <span>{{ node.endpoint }}</span>
            </div>
            <div class="nacos-node-tags">
              <el-tag size="small" type="info" effect="plain">{{ node.roleLabel }}</el-tag>
              <el-tag size="small" type="info" effect="plain">{{ node.grpcPorts }}</el-tag>
              <StatusTag :status="node.status" />
            </div>
          </div>
        </div>
      </div>
    </el-drawer>

  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
import { keepPreviousArrayOnLoadFailure } from '../api/resilientLoad'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import RunRecordTable from '../components/RunRecordTable.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { installLifecycleDisplayStatus, isInstallLifecycleSelectable, moduleRuntimeGroupStatus, runtimeHealthDisplayStatus } from '../status/semantics'
import { applyRealtimeStatusToAppInstance, useRealtimeStore } from '../stores/realtime'
import { useTaskProgressStore } from '../stores/taskProgress'
import { visibleManagementHeaderActions, visibleManagementTabs } from './managementEntries'

type AppInstance = {
  id: string
  app: string
  version: string
  serverId: string
  status: string
  topology: string
  metadata: string
  createdAt: string
}

type Credential = {
  id: string
  name: string
  kind: string
  username?: string
  endpoint?: string
  status: string
  appInstanceId?: string
  serverId?: string
}

type NacosConfigRevision = {
  id: string
  nacosInstanceId: string
  namespace: string
  group: string
  dataId: string
  contentHash: string
  createdBy: string
  publishedAt: string
}

type NacosConfigPreview = {
  generated: string
  current: string
  changed: boolean
  summary: string[]
  revisions: NacosConfigRevision[]
}

type ConfigForm = {
  nacosInstanceId: string
  nacosCredentialId: string
  namespace: string
  group: string
  dataId: string
  appName: string
  profile: string
  includeDatasource: boolean
  mysqlInstanceId: string
  mysqlCredentialId: string
  databaseName: string
  includeRedis: boolean
  redisInstanceId: string
  redisCredentialId: string
  redisDatabase: number
  includeMinio: boolean
  minioInstanceId: string
  minioCredentialId: string
  minioBucket: string
  minioPlatform: string
}

type TaskRecord = {
  id: string
  type: string
  target: string
  status: string
}

type InstanceMetadata = Record<string, any>

type NacosNode = {
  instance: AppInstance
  metadata: InstanceMetadata
  serverLabel: string
  endpoint: string
  status: string
  roleLabel: string
  grpcPorts: string
}

type NacosGroup = {
  id: string
  title: string
  version: string
  topology: string
  status: string
  endpoint: string
  database: string
  authLabel: string
  raftPort: string
  nodes: NacosNode[]
}

type NacosState = {
  instances: AppInstance[]
  servers: any[]
  tasks: TaskRecord[]
  credentials: Credential[]
  databaseInstances: AppInstance[]
  storageInstances: AppInstance[]
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const realtime = useRealtimeStore()
const taskProgress = useTaskProgressStore()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<TaskRecord[]>([])
const credentials = ref<Credential[]>([])
const databaseInstances = ref<AppInstance[]>([])
const storageInstances = ref<AppInstance[]>([])
const tab = ref('instances')
const search = ref('')
const configForm = ref<ConfigForm>({
  nacosInstanceId: '',
  nacosCredentialId: '',
  namespace: 'prod',
  group: 'DEFAULT_GROUP',
  dataId: 'application-prod.yml',
  appName: 'aifar',
  profile: 'prod',
  includeDatasource: true,
  mysqlInstanceId: '',
  mysqlCredentialId: '',
  databaseName: 'aifar_admin',
  includeRedis: true,
  redisInstanceId: '',
  redisCredentialId: '',
  redisDatabase: 1,
  includeMinio: false,
  minioInstanceId: '',
  minioCredentialId: '',
  minioBucket: 'aifar',
  minioPlatform: 'minio-1'
})
const configPreview = ref<NacosConfigPreview | null>(null)
const configRevisions = ref<NacosConfigRevision[]>([])
const configPreviewLoading = ref(false)
const configPublishLoading = ref(false)
const configRollbackId = ref('')
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const pendingDeleteGroup = ref<NacosGroup | null>(null)
const deletePasswords = ref<Record<string, string>>({})
const sameDeletePassword = ref(false)
const deleteSharedPassword = ref('')
const activeNacosGroupKey = ref('')
const nacosDetailVisible = ref(false)
const canManageApps = computed(() => can(permissions.appsManage))
const liveInstances = computed(() => instances.value.map((instance) => applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id))))
const liveDatabaseInstances = computed(() => databaseInstances.value.map((instance) => applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id))))
const liveStorageInstances = computed(() => storageInstances.value.map((instance) => applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id))))
const nacosGroups = computed(() => buildNacosGroups(liveInstances.value))
const activeNacosGroup = computed(() => nacosGroups.value.find((group) => group.id === activeNacosGroupKey.value) ?? null)
const filteredGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return nacosGroups.value
  return nacosGroups.value.filter((group) => nacosGroupSearchText(group).includes(q))
})
const activeNacosWorkbenchGroup = computed(() => {
  return filteredGroups.value.find((group) => group.id === activeNacosGroupKey.value) ?? filteredGroups.value[0] ?? null
})
const clusterGroupCount = computed(() => nacosGroups.value.filter((group) => group.topology === 'cluster').length)
const standaloneGroupCount = computed(() => nacosGroups.value.filter((group) => group.topology !== 'cluster').length)
const nacosNodeCount = computed(() => nacosGroups.value.reduce((total, group) => total + group.nodes.length, 0))
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.nacos.')))
const activeCredentials = computed(() => credentials.value.filter((item) => item.status === 'active'))
const nacosCredentials = computed(() => activeCredentials.value.filter((item) => item.kind === 'nacos' || item.kind === 'generic'))
const mysqlCredentials = computed(() => activeCredentials.value.filter((item) => item.kind === 'mysql' || item.kind === 'generic'))
const redisCredentials = computed(() => activeCredentials.value.filter((item) => item.kind === 'redis' || item.kind === 'generic'))
const minioCredentials = computed(() => activeCredentials.value.filter((item) => item.kind === 'minio' || item.kind === 'generic'))
const mysqlConfigInstances = computed(() => liveDatabaseInstances.value.filter((item) => ['mysql', 'mysql-router'].includes(item.app) && isInstallLifecycleSelectable({ status: item.status, metadata: metadataOf(item) })))
const redisConfigInstances = computed(() => liveDatabaseInstances.value.filter((item) => item.app === 'redis' && isInstallLifecycleSelectable({ status: item.status, metadata: metadataOf(item) })))
const minioConfigInstances = computed(() => liveStorageInstances.value.filter((item) => item.app === 'minio' && isInstallLifecycleSelectable({ status: item.status, metadata: metadataOf(item) })))
const nacosConfigInstanceOptions = computed(() => nacosGroups.value.flatMap((group) => group.nodes.map((node) => ({
  value: node.instance.id,
  label: `${group.title} / ${node.endpoint}`
}))))
const configSummary = computed(() => configPreview.value?.summary?.length ? configPreview.value.summary : [configForm.value.dataId])
const lastMonitorAt = computed(() => latestSnapshotTime(liveInstances.value.map((instance) => realtime.appInstanceSnapshot(instance.id))))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) return t('nacos.monitorPermissionRequired')
  return t('nacos.backendPushReady')
})
const settingsItems = computed(() => [
  { label: t('nacos.defaultPorts'), value: t('nacos.defaultPortsHint') },
  { label: t('nacos.cluster'), value: t('nacos.clusterSettings') },
  { label: t('nacos.standalone'), value: t('nacos.standaloneSettings') }
])
const deletePromptMessage = computed(() => {
  const group = pendingDeleteGroup.value
  return group ? t('nacos.uninstallPasswordPrompt', { name: group.title, count: deleteServers.value.length }) : ''
})
const deleteServers = computed(() => {
  const group = pendingDeleteGroup.value
  if (!group) return []
  const seen = new Set<string>()
  const out: Array<{ id: string; label: string }> = []
  for (const node of group.nodes) {
    const id = node.instance.serverId
    if (!id || seen.has(id)) continue
    seen.add(id)
    out.push({ id, label: serverName(id) })
  }
  return out
})
const visibleDeleteServers = computed(() => {
  if (sameDeletePassword.value && deleteServers.value.length > 1) {
    return []
  }
  return deleteServers.value
})

async function load() {
  applyNacosState(await fetchNacosState())
}

async function fetchNacosState(): Promise<NacosState> {
  const [nextInstances, nextServers, nextTasks, nextCredentials, nextDatabaseInstances, nextStorageInstances] = await Promise.all([
    keepPreviousArrayOnLoadFailure(apiGet<AppInstance[] | null>('/nacos/instances'), instances.value),
    keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/servers'), servers.value),
    keepPreviousArrayOnLoadFailure(apiGet<TaskRecord[] | null>('/tasks'), tasks.value),
    keepPreviousArrayOnLoadFailure(apiGet<Credential[] | null>('/credentials'), credentials.value),
    keepPreviousArrayOnLoadFailure(apiGet<AppInstance[] | null>('/database/instances'), databaseInstances.value),
    keepPreviousArrayOnLoadFailure(apiGet<AppInstance[] | null>('/storage/instances'), storageInstances.value)
  ])
  return {
    instances: nextInstances.filter((item) => item.app === 'nacos'),
    servers: nextServers,
    tasks: nextTasks,
    credentials: nextCredentials,
    databaseInstances: nextDatabaseInstances,
    storageInstances: nextStorageInstances
  }
}

function applyNacosState(state: NacosState) {
  instances.value = state.instances
  servers.value = state.servers
  tasks.value = state.tasks
  credentials.value = state.credentials
  databaseInstances.value = state.databaseInstances
  storageInstances.value = state.storageInstances
  syncConfigDefaults()
}

function syncConfigDefaults() {
  const firstNacos = nacosConfigInstanceOptions.value[0]
  if (!configForm.value.nacosInstanceId && firstNacos) {
    configForm.value.nacosInstanceId = firstNacos.value
  }
  if (!configForm.value.nacosCredentialId) {
    configForm.value.nacosCredentialId = bestCredentialId('nacos', configForm.value.nacosInstanceId)
  }
  if (!configForm.value.mysqlInstanceId && mysqlConfigInstances.value.length) {
    const router = mysqlConfigInstances.value.find((item) => item.app === 'mysql-router')
    configForm.value.mysqlInstanceId = (router ?? mysqlConfigInstances.value[0]).id
  }
  if (!configForm.value.mysqlCredentialId) {
    configForm.value.mysqlCredentialId = bestCredentialId('mysql', configForm.value.mysqlInstanceId)
  }
  if (!configForm.value.redisInstanceId && redisConfigInstances.value.length) {
    configForm.value.redisInstanceId = redisConfigInstances.value[0].id
  }
  if (!configForm.value.redisCredentialId) {
    configForm.value.redisCredentialId = bestCredentialId('redis', configForm.value.redisInstanceId)
  }
  if (!configForm.value.minioInstanceId && minioConfigInstances.value.length) {
    configForm.value.minioInstanceId = minioConfigInstances.value[0].id
  }
  if (!configForm.value.minioCredentialId) {
    configForm.value.minioCredentialId = bestCredentialId('minio', configForm.value.minioInstanceId)
  }
}

function handleConfigNacosChange() {
  configForm.value.nacosCredentialId = bestCredentialId('nacos', configForm.value.nacosInstanceId)
  clearConfigPreview()
}

function handleConfigMysqlChange() {
  configForm.value.mysqlCredentialId = bestCredentialId('mysql', configForm.value.mysqlInstanceId)
  clearConfigPreview()
}

function handleConfigRedisChange() {
  configForm.value.redisCredentialId = bestCredentialId('redis', configForm.value.redisInstanceId)
  clearConfigPreview()
}

function handleConfigMinioChange() {
  configForm.value.minioCredentialId = bestCredentialId('minio', configForm.value.minioInstanceId)
  clearConfigPreview()
}

function clearConfigPreview() {
  configPreview.value = null
}

function bestCredentialId(kind: string, instanceId: string) {
  const list = activeCredentials.value.filter((item) => item.kind === kind || item.kind === 'generic')
  if (!list.length) return ''
  const instance = findInstanceById(instanceId)
  const exact = list.find((item) => item.appInstanceId === instanceId)
  if (exact) return exact.id
  const byServer = instance?.serverId ? list.find((item) => item.serverId === instance.serverId) : undefined
  if (byServer) return byServer.id
  return list[0].id
}

function findInstanceById(id: string) {
  return [...liveInstances.value, ...liveDatabaseInstances.value, ...liveStorageInstances.value].find((item) => item.id === id)
}

function configPayload() {
  return {
    ...configForm.value,
    redisDatabase: Number(configForm.value.redisDatabase) || 0
  }
}

function validateConfigForm() {
  if (!configForm.value.nacosInstanceId || !configForm.value.nacosCredentialId) {
    ElMessage.warning(t('nacos.configNacosRequired'))
    return false
  }
  if (configForm.value.includeDatasource && (!configForm.value.mysqlInstanceId || !configForm.value.mysqlCredentialId)) {
    ElMessage.warning(t('nacos.configMysqlRequired'))
    return false
  }
  if (configForm.value.includeRedis && (!configForm.value.redisInstanceId || !configForm.value.redisCredentialId)) {
    ElMessage.warning(t('nacos.configRedisRequired'))
    return false
  }
  if (configForm.value.includeMinio && (!configForm.value.minioInstanceId || !configForm.value.minioCredentialId)) {
    ElMessage.warning(t('nacos.configMinioRequired'))
    return false
  }
  if (!configForm.value.includeDatasource && !configForm.value.includeRedis && !configForm.value.includeMinio) {
    ElMessage.warning(t('nacos.configSectionRequired'))
    return false
  }
  return true
}

async function previewNacosConfig() {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  if (!validateConfigForm()) return
  configPreviewLoading.value = true
  try {
    const result = await apiPost<NacosConfigPreview>('/nacos/configs/preview', configPayload())
    configPreview.value = result
    configRevisions.value = asArray<NacosConfigRevision>(result.revisions)
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('nacos.previewFailed'))
  } finally {
    configPreviewLoading.value = false
  }
}

async function publishNacosConfig() {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  if (!validateConfigForm()) return
  configPublishLoading.value = true
  try {
    const result = await apiPost<{ taskId: string }>('/nacos/configs/publish', configPayload())
    ElMessage.success(t('nacos.publishAccepted'))
    taskProgress.track(result.taskId, t('nacos.publishConfig'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('nacos.publishFailed'))
  } finally {
    configPublishLoading.value = false
  }
}

async function loadConfigRevisions() {
  const form = configForm.value
  if (!form.nacosInstanceId || !form.namespace || !form.group || !form.dataId) {
    configRevisions.value = []
    return
  }
  const params = new URLSearchParams({
    nacosInstanceId: form.nacosInstanceId,
    namespace: form.namespace,
    group: form.group,
    dataId: form.dataId,
    limit: '3'
  })
  configRevisions.value = asArray<NacosConfigRevision>(await apiGet<NacosConfigRevision[] | null>(`/nacos/configs/revisions?${params.toString()}`).catch(() => []))
}

async function rollbackNacosConfig(row: NacosConfigRevision) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  if (!configForm.value.nacosCredentialId) {
    ElMessage.warning(t('nacos.configNacosRequired'))
    return
  }
  configRollbackId.value = row.id
  try {
    const result = await apiPost<{ taskId: string }>('/nacos/configs/rollback', {
      revisionId: row.id,
      nacosCredentialId: configForm.value.nacosCredentialId
    })
    ElMessage.success(t('nacos.rollbackAccepted'))
    taskProgress.track(result.taskId, t('nacos.rollbackConfig'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('nacos.rollbackFailed'))
  } finally {
    configRollbackId.value = ''
  }
}

function buildNacosGroups(rows: AppInstance[]) {
  const groups = new Map<string, { items: AppInstance[]; metas: Map<string, InstanceMetadata> }>()
  for (const item of rows.filter((row) => row.app === 'nacos')) {
    const metadata = metadataOf(item)
    const topology = normalizedTopology(item, metadata)
    const key = topology === 'cluster' ? nacosClusterKey(item, metadata) : `instance:${item.id}`
    const group = groups.get(key) ?? { items: [], metas: new Map<string, InstanceMetadata>() }
    group.items.push(item)
    group.metas.set(item.id, metadata)
    groups.set(key, group)
  }
  return Array.from(groups.entries())
    .map(([id, group]) => createNacosGroup(id, group.items, group.metas))
    .sort((a, b) => a.title.localeCompare(b.title))
}

function createNacosGroup(id: string, items: AppInstance[], metas: Map<string, InstanceMetadata>): NacosGroup {
  const sortedItems = [...items].sort((a, b) => serverName(a.serverId).localeCompare(serverName(b.serverId)))
  const first = sortedItems[0]
  const firstMetadata = first ? metas.get(first.id) ?? metadataOf(first) : {}
  const topology = first ? normalizedTopology(first, firstMetadata) : 'standalone'
  const nodes = sortedItems.map((item, index) => createNacosNode(item, metas.get(item.id) ?? metadataOf(item), topology, index))
  const version = uniqueValues(sortedItems.map((item) => item.version).filter(Boolean)).join(', ')
  return {
    id,
    title: nacosGroupTitle(id, topology, first),
    version,
    topology,
    status: nacosGroupStatus(nodes),
    endpoint: uniqueValues(nodes.map((node) => node.endpoint).filter(Boolean)).join(', '),
    database: nacosDatabaseSummary(firstMetadata),
    authLabel: nacosAuthLabel(sortedItems.map((item) => metas.get(item.id) ?? metadataOf(item))),
    raftPort: stringValue(firstMetadata.raftPort) || '7848',
    nodes
  }
}

function createNacosNode(item: AppInstance, metadata: InstanceMetadata, topology: string, index: number): NacosNode {
  return {
    instance: item,
    metadata,
    serverLabel: serverName(item.serverId),
    endpoint: stringValue(metadata.endpoint) || endpointFromServer(item, metadata),
    status: displayInstanceStatus(item),
    roleLabel: topology === 'cluster' ? `${t('nacos.cluster')} ${index + 1}` : t('nacos.standalone'),
    grpcPorts: `${stringValue(metadata.grpcPort) || numberValue(metadata.port, 8848) + 1000}/${stringValue(metadata.grpcRaftPort) || numberValue(metadata.port, 8848) + 1001}`
  }
}

function nacosGroupTitle(id: string, topology: string, first?: AppInstance) {
  if (topology === 'cluster') {
    const suffix = id.startsWith('cluster:') ? id.replace('cluster:', '').slice(-6) : first?.id.slice(-6)
    return suffix ? `nacos-cluster-${suffix}` : 'nacos-cluster'
  }
  return first ? `nacos-${serverName(first.serverId)}` : 'nacos'
}

function nacosClusterKey(item: AppInstance, metadata: InstanceMetadata) {
  const clusterID = stringValue(metadata.clusterId)
  if (clusterID) {
    return `cluster:${clusterID}`
  }
  const nodes = stringList(metadata.clusterNodes).join('|')
  if (nodes) {
    return `cluster:${nodes}`
  }
  return `cluster:${item.id}`
}

function nacosGroupStatus(nodes: NacosNode[]) {
  return moduleRuntimeGroupStatus(nodes.map((node) => node.status))
}

function displayInstanceStatus(item: AppInstance) {
  const metadata = metadataOf(item)
  if (isInstallFailedInstance(item, metadata)) {
    return 'failed'
  }
  return runtimeHealthDisplayStatus(item.status)
}

function isInstallFailedGroup(group: NacosGroup) {
  return group.nodes.some((node) => isInstallFailedInstance(node.instance, node.metadata))
}

function isInstallFailedInstance(item: AppInstance, metadata: InstanceMetadata) {
  return installLifecycleDisplayStatus({ status: item.status, metadata }) === 'failed'
}

function nacosDatabaseSummary(metadata: InstanceMetadata) {
  const host = stringValue(metadata.dbHost)
  if (!host) {
    return ''
  }
  const port = stringValue(metadata.dbPort) || '3306'
  const name = stringValue(metadata.dbName)
  const user = stringValue(metadata.dbUser)
  const prefix = user ? `${user}@` : ''
  return `${prefix}${host}:${port}${name ? `/${name}` : ''}`
}

function nacosAuthLabel(metas: InstanceMetadata[]) {
  const values = metas.map((metadata) => metadata.authEnabled).filter((value) => value !== undefined && value !== null)
  if (!values.length) {
    return t('nacos.authUnknown')
  }
  return values.every(truthyValue) ? t('nacos.authEnabled') : t('nacos.authDisabled')
}

function normalizedTopology(item: AppInstance, metadata: InstanceMetadata) {
  const topology = stringValue(item.topology) || stringValue(metadata.topology) || stringValue(metadata.mode)
  return topology === 'cluster' ? 'cluster' : 'standalone'
}

function endpointFromServer(item: AppInstance, metadata: InstanceMetadata) {
  const server = servers.value.find((candidate) => candidate.id === item.serverId)
  const port = stringValue(metadata.port) || '8848'
  return server?.host ? `http://${server.host}:${port}/nacos` : '-'
}

function databaseInstanceLabel(item: AppInstance) {
  const metadata = metadataOf(item)
  const topology = normalizedTopology(item, metadata)
  const endpoint = stringValue(metadata.endpoint) || stringValue(metadata.clusterEndpoint) || instanceEndpoint(item, metadata)
  return `${item.app} / ${topology} / ${endpoint || serverName(item.serverId)}`
}

function storageInstanceLabel(item: AppInstance) {
  const metadata = metadataOf(item)
  const endpoint = stringValue(metadata.endpoint) || instanceEndpoint(item, metadata)
  return `MinIO / ${item.topology || stringValue(metadata.topology) || '-'} / ${endpoint || serverName(item.serverId)}`
}

function instanceEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const server = servers.value.find((candidate) => candidate.id === item.serverId)
  const port = stringValue(metadata.port) || stringValue(metadata.apiPort)
  if (!server?.host) return ''
  return port ? `${server.host}:${port}` : server.host
}

function credentialLabel(item: Credential) {
  const parts = [item.name || item.kind, item.username, item.endpoint].map((part) => String(part || '').trim()).filter(Boolean)
  return parts.join(' / ')
}

function shortHash(value: string) {
  return value ? value.slice(0, 12) : '-'
}

function metadataOf(item: AppInstance) {
  try {
    return JSON.parse(item.metadata || '{}') as InstanceMetadata
  } catch {
    return {}
  }
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function stringValue(value: unknown) {
  const out = String(value ?? '').trim()
  return out === '<nil>' ? '' : out
}

function latestSnapshotTime(snapshots: Array<{ collectedAt?: string; updatedAt?: string } | undefined>) {
  const latest = snapshots
    .map((snapshot) => snapshot?.collectedAt || snapshot?.updatedAt || '')
    .map((value) => new Date(value).getTime())
    .filter((value) => Number.isFinite(value) && value > 0)
    .sort((a, b) => b - a)[0]
  return latest ? new Date(latest).toLocaleTimeString() : ''
}

function numberValue(value: unknown, fallback: number) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : fallback
}

function stringList(value: unknown) {
  if (Array.isArray(value)) {
    return value.map((item) => stringValue(item)).filter(Boolean)
  }
  const raw = stringValue(value)
  if (!raw) return []
  return raw.split(',').map((item) => item.trim()).filter(Boolean)
}

function truthyValue(value: unknown) {
  if (typeof value === 'boolean') return value
  return ['true', '1', 'yes', 'on'].includes(stringValue(value).toLowerCase())
}

function uniqueValues(values: string[]) {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)))
}

function nacosGroupSearchText(group: NacosGroup) {
  return [
    group.title,
    group.version,
    group.topology,
    group.endpoint,
    group.database,
    group.authLabel,
    ...group.nodes.flatMap((node) => [node.serverLabel, node.endpoint, node.status, node.roleLabel])
  ].join(' ').toLowerCase()
}

function selectNacosResource(group: NacosGroup) {
  activeNacosGroupKey.value = group.id
}

function openNacosResource(group: NacosGroup) {
  selectNacosResource(group)
  nacosDetailVisible.value = true
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

function openDeleteGroup(group: NacosGroup) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  pendingDeleteGroup.value = group
  const initialPasswords: Record<string, string> = {}
  for (const node of group.nodes) {
    if (node.instance.serverId) {
      initialPasswords[node.instance.serverId] = ''
    }
  }
  deletePasswords.value = initialPasswords
  deletePromptVisible.value = true
}

function resetDeletePrompt() {
  if (!deleteSubmitting.value) {
    pendingDeleteGroup.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
  }
}

function handleSamePasswordToggle() {
  if (sameDeletePassword.value && !deleteSharedPassword.value) {
    const firstServer = deleteServers.value[0]
    if (firstServer) {
      deleteSharedPassword.value = deletePasswords.value[firstServer.id] || ''
    }
  }
}

function deletePasswordPayload() {
  const out: Record<string, string> = {}
  if (sameDeletePassword.value && deleteServers.value.length > 1) {
    const password = deleteSharedPassword.value.trim()
    if (!password) {
      return out
    }
    for (const server of deleteServers.value) {
      out[server.id] = password
    }
    return out
  }
  for (const server of deleteServers.value) {
    const password = String(deletePasswords.value[server.id] || '').trim()
    if (!password) {
      return {}
    }
    out[server.id] = password
  }
  return out
}

async function confirmDeleteScope() {
  const group = pendingDeleteGroup.value
  if (!group) {
    return
  }
  const passwords = deletePasswordPayload()
  if (Object.keys(passwords).length !== deleteServers.value.length) {
    ElMessage.warning(t('nacos.deletePasswordsRequired'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>('/apps/instances/batch-delete', {
      instanceIds: group.nodes.map((node) => node.instance.id),
      serverPasswords: passwords
    })
    deletePromptVisible.value = false
    pendingDeleteGroup.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
    ElMessage.success(t('nacos.uninstallTaskAccepted', { count: group.nodes.length }))
    taskProgress.track(result.taskId, t('apps.uninstallService'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    deleteSubmitting.value = false
  }
}

watch(
  () => [configForm.value.nacosInstanceId, configForm.value.namespace, configForm.value.group, configForm.value.dataId].join('|'),
  () => {
    clearConfigPreview()
    void loadConfigRevisions()
  }
)

onMounted(async () => {
  await load()
  await loadConfigRevisions()
})
</script>

<style scoped>
.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
}

.nacos-main {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.monitor-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  min-width: 0;
}

.nacos-resource-shell {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(340px, .9fr);
  gap: 14px;
  min-height: 0;
  padding: 12px;
}

.nacos-resource-list {
  display: grid;
  gap: 10px;
  padding: 0;
  min-height: 0;
  overflow: auto;
}

.nacos-resource-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 16px 18px;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  cursor: pointer;
  transition: background .16s ease, border-color .16s ease, box-shadow .16s ease;
}

.nacos-resource-row:hover {
  position: relative;
  border-color: #91caff;
  background: #f7fbff;
  box-shadow: 0 2px 8px rgba(15, 35, 68, .06);
  z-index: 1;
}

.nacos-resource-row.active {
  border-color: #1677ff;
  background: linear-gradient(90deg, #f0f8ff 0%, #fff 72%);
  box-shadow: inset 3px 0 0 #1677ff, 0 2px 8px rgba(22, 119, 255, .08);
}

.resource-inline-detail {
  min-width: 0;
  max-height: min(640px, calc(100vh - 280px));
  overflow: auto;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: rgba(255, 255, 255, .96);
  padding: 16px;
  box-shadow: var(--aifar-shadow-card);
}

.inline-detail-actions {
  display: flex;
  justify-content: flex-end;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: 14px;
  padding-top: 12px;
  border-top: 1px solid var(--aifar-border-soft);
}

.nacos-head {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  margin-bottom: 10px;
}

.nacos-title-block {
  min-width: 0;
  flex: 1;
}

.nacos-head strong,
.nacos-head span {
  display: block;
}

.nacos-head strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nacos-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.nacos-head-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
  max-width: 240px;
}

.nacos-head-actions :deep(.el-tag) {
  min-height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 1;
}

.nacos-head-actions :deep(.el-tag__content) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 1;
}

.app-icon.small {
  width: 34px;
  height: 34px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  background: var(--aifar-primary-soft);
  color: var(--aifar-primary);
  border: 1px solid #bae0ff;
  font-weight: 850;
}

.nacos-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.nacos-summary span {
  min-width: 0;
  max-width: 100%;
  border: 1px solid var(--aifar-border-soft);
  border-radius: 999px;
  background: #f7fbff;
  color: var(--aifar-text-secondary);
  padding: 4px 8px;
  font-size: 12px;
  line-height: 1.35;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nacos-summary strong {
  margin-right: 4px;
  color: var(--aifar-text-tertiary);
  font-weight: 650;
}

.nacos-summary-wide {
  flex: 1 1 100%;
}

.detail-summary {
  padding: 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f8fbff;
}

.resource-detail-stack {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.detail-head {
  align-items: center;
  margin-bottom: 0;
}

.service-notice {
  margin-top: 8px;
  border-radius: var(--aifar-radius);
  padding: 8px 10px;
  font-size: 12px;
  font-weight: 700;
}

.service-notice.danger {
  color: #cf1322;
  background: #fff2f0;
  border: 1px solid #ffccc7;
}

.nacos-node-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed var(--aifar-border-soft);
}

.section-label {
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 700;
}

.nacos-node-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #fff;
}

.nacos-node-main {
  min-width: 0;
}

.nacos-node-main strong,
.nacos-node-main span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.nacos-node-main span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.nacos-node-tags {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  white-space: nowrap;
}

.settings-grid {
  padding: 12px;
}

.config-publish-grid {
  display: grid;
  grid-template-columns: minmax(320px, 420px) minmax(0, 1fr);
  gap: 12px;
  padding: 12px;
  min-height: 0;
}

.config-form-panel,
.config-preview-panel {
  min-width: 0;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 12px;
}

.config-section-head,
.config-preview-toolbar,
.config-revision-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  margin-bottom: 12px;
}

.config-section-head strong,
.config-revision-head strong {
  font-size: 14px;
}

.config-form {
  display: grid;
  gap: 2px;
}

.config-field-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px;
}

.config-section-toggle {
  border-top: 1px dashed var(--aifar-border-soft);
  padding-top: 10px;
  margin-top: 2px;
}

.full-width {
  width: 100%;
}

.config-preview-panel {
  display: flex;
  flex-direction: column;
  gap: 12px;
  overflow: hidden;
}

.config-summary {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  min-width: 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.config-code-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  min-height: 360px;
}

.config-code {
  min-height: 330px;
  max-height: 520px;
  overflow: auto;
  margin: 6px 0 0;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #0f172a;
  color: #d7e2f3;
  padding: 10px;
  font-size: 12px;
  line-height: 1.55;
  white-space: pre-wrap;
  word-break: break-word;
}

.config-revision-table {
  width: 100%;
}

.multi-secret-form {
  display: grid;
  gap: 8px;
}

.secret-confirm-message {
  margin: 0 0 12px;
  color: var(--aifar-text-secondary);
  font-size: 14px;
  line-height: 22px;
}

@media (max-width: 720px) {
  .config-publish-grid,
  .config-code-grid,
  .config-field-row,
  .nacos-resource-shell {
    grid-template-columns: 1fr;
  }

  .resource-inline-detail {
    max-height: none;
  }

  .monitor-actions,
  .nacos-node-tags {
    flex-wrap: wrap;
    justify-content: flex-start;
  }

  .nacos-head {
    flex-wrap: wrap;
  }

  .nacos-head-actions {
    justify-content: flex-start;
    max-width: none;
    width: 100%;
  }

  .nacos-node-row {
    grid-template-columns: 1fr;
  }
}
</style>
