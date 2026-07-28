<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('database.title') }}</h1>
        <p class="page-subtitle">{{ t('database.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <span v-if="visibleManagementHeaderActions.database.includes('connected')" class="status-pill success">{{ t('common.connected') }}</span>
        <el-button v-if="visibleManagementHeaderActions.database.includes('refresh')" @click="load">{{ t('common.refresh') }}</el-button>
      </div>
    </div>

    <div class="aifar-panel status-line">
      <span class="subtle-note">{{ t('database.instanceCount', { count: instances.length }) }}</span>
      <span class="status-pill" :class="{ success: canManageApps }">{{ monitoringStatusLabel }}</span>
      <span v-if="lastMonitorAt" class="subtle-note">{{ t('database.lastMonitoredAt') }} {{ lastMonitorAt }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane v-for="tabName in visibleManagementTabs.database" :key="tabName" :label="t('database.instances')" :name="tabName" />
    </el-tabs>

    <div class="workspace-card database-main">
      <template v-if="tab === 'instances'">
        <div class="table-toolbar">
          <div class="head-actions">
            <span class="status-pill">{{ t('common.all') }} {{ instanceGroups.length }}</span>
            <span class="status-pill">MySQL {{ mysqlGroupCount }}</span>
            <span class="status-pill">Redis {{ redisGroupCount }}</span>
            <span class="status-pill">{{ t('database.nodes') }} {{ databaseNodeCount }}</span>
            <span v-if="sentinelNodeCount" class="status-pill">{{ roleLabel('sentinel') }} {{ sentinelNodeCount }}</span>
            <span v-if="routerInstanceCount" class="status-pill">{{ t('database.mysqlRouter') }} {{ routerInstanceCount }}</span>
          </div>
          <div class="monitor-actions">
            <el-input v-model="search" :placeholder="t('common.search')" clearable class="toolbar-control is-sm" />
          </div>
        </div>

        <div class="db-card-grid" v-if="filteredGroups.length">
          <article v-for="group in filteredGroups" :key="group.id" class="db-card">
            <div class="db-head">
              <div class="app-icon small">{{ group.app === 'redis' ? 'RE' : 'MY' }}</div>
              <div class="db-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ groupSubtitle(group) }}</span>
              </div>
              <div class="db-head-actions">
                <StatusTag :status="group.status" />
                <el-tooltip v-if="hasMysqlClusterStart(group)" :content="mysqlClusterStartReason(group)" :disabled="!isMysqlClusterStartDisabled(group)" placement="top">
                  <span>
                    <el-button
                      size="small"
                      type="primary"
                      plain
                      :loading="startingClusterId === group.id"
                      :disabled="isMysqlClusterStartDisabled(group)"
                      @click="startMysqlCluster(group)"
                    >
                      {{ t('database.startMysqlCluster') }}
                    </el-button>
                  </span>
                </el-tooltip>
              </div>
            </div>
            <div class="db-grid">
              <div><span>{{ t('database.engine') }}</span><strong>{{ group.app }}</strong></div>
              <div><span>{{ t('dashboard.topology') }}</span><strong>{{ group.topology || '-' }}</strong></div>
              <div><span>{{ t('common.version') }}</span><strong>{{ group.version }}</strong></div>
              <div><span>{{ t('database.nodes') }}</span><strong>{{ group.nodes.length }}</strong></div>
              <div v-if="group.sentinels.length">
                <span>{{ roleLabel('sentinel') }}</span>
                <strong>{{ group.sentinels.length }}</strong>
                <StatusTag class="db-grid-tag" :status="group.sentinelStatus" />
              </div>
              <div v-if="showGroupEndpoint(group)" class="db-grid-wide"><span>{{ groupEndpointLabel(group) }}</span><strong>{{ group.endpoint || '-' }}</strong></div>
              <div><span>{{ t('common.status') }}</span><StatusTag :status="group.status" /></div>
              <div v-if="group.routers.length">
                <span>{{ t('database.mysqlRouter') }}</span>
                <strong>{{ group.routers.length }}</strong>
                <StatusTag class="db-grid-tag" :status="group.routerStatus" />
              </div>
            </div>
            <div v-if="isInstallFailedGroup(group)" class="service-notice danger">{{ t('apps.installFailedCleanupHint') }}</div>
            <div v-if="isUnavailable(group.nodeStatus)" class="service-notice danger">{{ databaseServiceUnavailableText(group) }}</div>
            <div v-if="isUnavailable(group.routerStatus)" class="service-notice danger">{{ t('database.routerServiceUnavailable') }}</div>
            <div
              v-if="mysqlGroupReconciliation(group).kind === 'required'"
              class="maintenance-banner reconciliation"
              role="alert"
              aria-live="polite"
            >
              <strong>{{ t('database.mysqlBackup.reconciliationTitle') }}</strong>
              <span>{{ t('database.mysqlBackup.reconciliationBoundary') }}</span>
              <span>{{ reconciliationSummary(group) }}</span>
            </div>
            <div
              v-else-if="mysqlGroupReconciliation(group).kind === 'invalid' && group.app === 'mysql'"
              class="maintenance-banner invalid"
              role="alert"
              aria-live="assertive"
            >
              <strong>{{ t('database.mysqlBackup.reconciliationInvalidTitle') }}</strong>
              <span>{{ t('database.mysqlBackup.reconciliationInvalid') }}</span>
            </div>
            <div
              v-if="mysqlGroupMaintenance(group).kind === 'required'"
              class="maintenance-banner"
              role="alert"
              aria-live="polite"
            >
              <strong>{{ t('database.mysqlBackup.maintenanceTitle') }}</strong>
              <span>{{ t('database.mysqlBackup.maintenanceExternalClients') }}</span>
              <span>{{ maintenanceSummary(group) }}</span>
            </div>
            <div
              v-else-if="mysqlGroupMaintenance(group).kind === 'invalid' && group.app === 'mysql'"
              class="maintenance-banner invalid"
              role="alert"
              aria-live="assertive"
            >
              <strong>{{ t('database.mysqlBackup.maintenanceInvalidTitle') }}</strong>
              <span>{{ t('database.mysqlBackup.maintenanceInvalid') }}</span>
            </div>
            <div v-if="group.nodes.length" class="node-list">
              <div v-for="node in group.nodes" :key="node.instance.id" class="node-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                </div>
              </div>
            </div>
            <div v-if="group.sentinels.length" class="router-list">
              <div class="section-label">{{ roleLabel('sentinel') }}</div>
              <div v-for="node in group.sentinels" :key="`sentinel-${node.instance.id}`" class="node-row router-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                </div>
              </div>
            </div>
            <div v-if="group.routers.length" class="router-list">
              <div class="section-label">{{ t('database.mysqlRouter') }}</div>
              <div v-for="node in group.routers" :key="node.instance.id" class="node-row router-row">
                <div class="node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint || '-' }}</span>
                </div>
                <div class="node-tags">
                  <el-tag size="small" :type="roleTagType(node.role)" effect="plain">{{ node.roleLabel }}</el-tag>
                  <el-tag size="small" :type="nodeHealthType(node)">{{ nodeHealthLabel(node) }}</el-tag>
                </div>
              </div>
            </div>
            <div v-if="mysqlAvailability(group).visible" class="mysql-backup-actions" :aria-label="t('database.mysqlBackup.actionsLabel')">
              <el-tooltip :content="mysqlActionReason(group)" :disabled="mysqlAvailability(group).backup" placement="top">
                <span><el-button :disabled="!mysqlAvailability(group).backup" @click="openMySQLBackup(group)">{{ t('database.mysqlBackup.createAction') }}</el-button></span>
              </el-tooltip>
              <el-button @click="openMySQLBackupRecords(group)">{{ t('database.mysqlBackup.recordsAction') }}</el-button>
              <el-tooltip :content="mysqlActionReason(group)" :disabled="mysqlAvailability(group).verify" placement="top">
                <span><el-button :disabled="!mysqlAvailability(group).verify" @click="verifyLatestMySQLBackup(group)">{{ t('database.mysqlBackup.verifyAction') }}</el-button></span>
              </el-tooltip>
              <el-tooltip :content="mysqlActionReason(group)" :disabled="mysqlAvailability(group).restore" placement="top">
                <span><el-button type="primary" plain :disabled="!mysqlAvailability(group).restore" @click="openLatestMySQLRestore(group)">{{ t('database.mysqlBackup.restoreAction') }}</el-button></span>
              </el-tooltip>
              <el-button v-if="mysqlAvailability(group).disaster" type="danger" plain @click="openLatestMySQLDisaster(group)">{{ t('database.mysqlBackup.disasterAction') }}</el-button>
              <el-button
                v-if="mysqlAvailability(group).reconcile"
                type="warning"
                plain
                :loading="isReconciliationSubmitting(group)"
                @click="runReconciliation(group)"
              >{{ t('database.mysqlBackup.reconciliationAction') }}</el-button>
              <el-button
                v-if="mysqlGroupMaintenance(group).kind === 'required' && isOwner && canManageDatabase"
                type="warning"
                plain
                :disabled="!mysqlAvailability(group).clearMaintenance"
                @click="openMaintenanceClear(group)"
              >{{ t('database.mysqlBackup.clearMaintenance') }}</el-button>
            </div>
          </article>
        </div>
        <div v-else class="empty-state"><div><strong>{{ t('database.noInstancesTitle') }}</strong><span>{{ t('database.noInstancesDesc') }}</span></div></div>
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

    <MySQLBackupDialog
      v-model="backupDialogVisible"
      :instance-id="activeTarget.instanceId"
      :source-label="activeTarget.label"
      :defaults="backupListDefaults"
      :submission-allowed="activeAvailability.backup"
      :before-submit="guardActiveBackupSubmission"
      @submitted="refreshActiveBackups"
    />
    <MySQLBackupDrawer
      v-model="backupDrawerVisible"
      :source-label="activeTarget.label"
      :version="activeMysqlGroup?.version || ''"
      :topology="activeTarget.topology"
      :target="activeTarget"
      :records="backupRecords"
      :loading="backupListLoading"
      :can-verify="activeAvailability.verify"
      :can-restore="activeAvailability.restore"
      @verify="verifySelectedMySQLBackup"
      @restore="openSelectedMySQLRestore"
      @open-task="openTaskById"
    />
    <MySQLRestoreDialog
      v-model="restoreDialogVisible"
      :backup="activeBackup"
      :target="activeTarget"
      :default-threads="backupListDefaults.threads"
      :submission-allowed="activeAvailability.restore"
      :before-submit="guardActiveRestoreSubmission"
      @submitted="refreshActiveBackups"
    />
    <MySQLDisasterRebuildDialog
      v-model="disasterDialogVisible"
      :instance-id="activeTarget.instanceId"
      :cluster-id="activeTarget.clusterId || ''"
      :mysql-version="activeTarget.mysqlVersion"
      :backup="activeBackup"
      :nodes="activeDisasterNodes"
      :default-threads="backupListDefaults.threads"
      :submission-allowed="activeAvailability.disaster"
      :before-submit="guardActiveDisasterSubmission"
      @submitted="refreshActiveBackups"
    />
    <el-dialog
      v-model="maintenanceClearVisible"
      :title="t('database.mysqlBackup.clearMaintenanceTitle')"
      width="min(520px, calc(100vw - 32px))"
      :close-on-click-modal="false"
      destroy-on-close
    >
      <el-alert type="warning" :closable="false" show-icon :title="t('database.mysqlBackup.clearMaintenanceWarning')" />
      <el-checkbox v-model="maintenanceClearConfirmed" class="maintenance-clear-confirm">
        {{ t('database.mysqlBackup.clearMaintenanceConfirm') }}
      </el-checkbox>
      <template #footer>
        <el-button @click="maintenanceClearVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="warning" :loading="maintenanceClearSubmitting" :disabled="!maintenanceClearConfirmed" @click="confirmMaintenanceClear">
          {{ t('database.mysqlBackup.clearMaintenance') }}
        </el-button>
      </template>
    </el-dialog>

  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
import { keepPreviousArrayOnLoadFailure } from '../api/resilientLoad'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import RunRecordTable from '../components/RunRecordTable.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import MySQLBackupDialog from '../database/MySQLBackupDialog.vue'
import MySQLBackupDrawer from '../database/MySQLBackupDrawer.vue'
import MySQLDisasterRebuildDialog from '../database/MySQLDisasterRebuildDialog.vue'
import MySQLRestoreDialog from '../database/MySQLRestoreDialog.vue'
import {
  backupTargetCompatibility,
  clearMySQLMaintenance,
  groupMySQLMaintenance,
  groupMySQLReconciliation,
  isMySQLBackupVerifiable,
  latestVerifiableMySQLBackup,
  listMySQLBackups,
  mysqlOperationAvailability,
  selectMaintenanceDisasterBackup,
  runMySQLReconciliation,
  verifyMySQLBackup,
  type MySQLBackupDefaults,
  type MySQLBackupRecord,
  type MySQLMaintenanceResult,
  type MySQLReconciliationResult,
  type MySQLOperationAvailability,
  type MySQLRestoreTarget
} from '../database/mysqlBackup'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { applyRealtimeStatusToAppInstance, useRealtimeStore } from '../stores/realtime'
import { useSessionStore } from '../stores/session'
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

type TaskRecord = {
  id: string
  type: string
  target: string
  status: string
}

type InstanceMetadata = Record<string, any>

type DatabaseNode = {
  instance: AppInstance
  metadata: InstanceMetadata
  serverLabel: string
  endpoint: string
  role: string
  roleLabel: string
  virtual?: boolean
}

type DatabaseGroup = {
  id: string
  app: string
  topology: string
  version: string
  title: string
  endpoint: string
  status: string
  nodeStatus: string
  routerStatus: string
  sentinelStatus: string
  createdAt: string
  metadata: InstanceMetadata
  nodes: DatabaseNode[]
  routers: DatabaseNode[]
  sentinels: DatabaseNode[]
}

type DatabaseHealth = 'online' | 'offline' | 'unknown' | 'probing'

type DeleteScopeKind = 'single' | 'mysql-group' | 'redis-group'

type DeleteScope = {
  kind: DeleteScopeKind
  title: string
  message: string
  nodes: DatabaseNode[]
  groupId?: string
}

type DatabaseState = {
  instances: AppInstance[]
  servers: any[]
  tasks: TaskRecord[]
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const realtime = useRealtimeStore()
const session = useSessionStore()
const taskProgress = useTaskProgressStore()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<TaskRecord[]>([])
const tab = ref('instances')
const search = ref('')
const deletePromptVisible = ref(false)
const deleteSubmitting = ref(false)
const pendingDeleteScope = ref<DeleteScope | null>(null)
const deletePasswords = ref<Record<string, string>>({})
const sameDeletePassword = ref(false)
const deleteSharedPassword = ref('')
const startingClusterId = ref('')
const activeMysqlGroupKey = ref('')
const activeBackup = ref<MySQLBackupRecord | null>(null)
const backupRecords = ref<MySQLBackupRecord[]>([])
const backupListDefaults = ref<MySQLBackupDefaults>({ threads: 4, maxRateMBps: 0 })
const backupListLoading = ref(false)
const backupDialogVisible = ref(false)
const backupDrawerVisible = ref(false)
const restoreDialogVisible = ref(false)
const disasterDialogVisible = ref(false)
const maintenanceClearVisible = ref(false)
const maintenanceClearConfirmed = ref(false)
const maintenanceClearSubmitting = ref(false)
const maintenanceClearIdentity = ref('')
const reconciliationSubmittingId = ref('')
const liveInstances = computed(() => instances.value.map((instance) => applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id))))
const instanceGroups = computed(() => groupDatabaseInstances(liveInstances.value))
const activeMysqlGroup = computed(() => instanceGroups.value.find((group) => group.id === activeMysqlGroupKey.value) ?? null)
const mysqlGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'mysql').length)
const redisGroupCount = computed(() => instanceGroups.value.filter((item) => item.app === 'redis').length)
const databaseNodeCount = computed(() => instanceGroups.value.reduce((total, group) => total + group.nodes.length, 0))
const sentinelNodeCount = computed(() => instanceGroups.value.reduce((total, group) => total + group.sentinels.length, 0))
const routerInstanceCount = computed(() => liveInstances.value.filter((item) => item.app === 'mysql-router').length)
const canManageApps = computed(() => can(permissions.appsManage))
const canManageDatabase = computed(() => can(permissions.databaseManage))
const isOwner = computed(() => session.role.trim().toLowerCase() === 'owner')
const activeTarget = computed(() => mysqlRestoreTarget(activeMysqlGroup.value))
const activeAvailability = computed<MySQLOperationAvailability>(() => activeMysqlGroup.value
  ? mysqlAvailability(activeMysqlGroup.value)
  : emptyMysqlAvailability())
const activeDisasterNodes = computed(() => (activeMysqlGroup.value?.nodes ?? []).filter((node) => !node.virtual).map((node) => ({
  instanceId: node.instance.id,
  instanceLabel: node.serverLabel,
  serverId: node.instance.serverId,
  serverLabel: serverName(node.instance.serverId)
})))
const lastMonitorAt = computed(() => latestSnapshotTime(liveInstances.value.map((instance) => realtime.appInstanceSnapshot(instance.id))))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) {
    return t('database.monitorPermissionRequired')
  }
  return t('database.backendPushReady')
})
const deletePromptMessage = computed(() => {
  return pendingDeleteScope.value?.message || ''
})
const deletePromptTitle = computed(() => {
  return pendingDeleteScope.value?.title || t('apps.uninstallService')
})
const deleteServers = computed(() => {
  const scope = pendingDeleteScope.value
  if (!scope) return []
  const seen = new Set<string>()
  const out: Array<{ id: string; label: string }> = []
  for (const node of scope.nodes) {
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
const filteredGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return instanceGroups.value
  return instanceGroups.value.filter((group) => groupSearchText(group).includes(q))
})
const runTasks = computed(() =>
  tasks.value.filter((item) =>
    item.type?.startsWith('apps.mysql.') ||
    item.type?.startsWith('apps.mysql-router.') ||
    item.type?.startsWith('apps.redis.') ||
    item.type?.startsWith('apps.redis-sentinel.') ||
    item.type?.startsWith('database.mysql.')
  )
)
const settingsItems = computed(() => [
  { label: 'MySQL', value: t('database.mysqlSettings') },
  { label: 'Redis', value: t('database.redisSettings') },
  { label: t('common.provider'), value: t('common.real') }
])

async function load() {
  applyDatabaseState(await fetchDatabaseState())
}

async function fetchDatabaseState(): Promise<DatabaseState> {
  const [nextInstances, nextServers, nextTasks] = await Promise.all([
    keepPreviousArrayOnLoadFailure(apiGet<AppInstance[] | null>('/database/instances'), instances.value),
    keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/servers'), servers.value),
    keepPreviousArrayOnLoadFailure(apiGet<TaskRecord[] | null>('/tasks'), tasks.value)
  ])
  return {
    instances: nextInstances,
    servers: nextServers,
    tasks: nextTasks
  }
}

function applyDatabaseState(state: DatabaseState) {
  instances.value = state.instances
  servers.value = state.servers
  tasks.value = state.tasks
}

function metadataOf(item: AppInstance) {
  try {
    return JSON.parse(item.metadata || '{}') as InstanceMetadata
  } catch {
    return {}
  }
}

function groupDatabaseInstances(items: AppInstance[]) {
  const groups = new Map<string, DatabaseGroup>()
  const routers: Array<{ item: AppInstance; metadata: InstanceMetadata; node: DatabaseNode }> = []
  for (const item of items) {
    const metadata = metadataOf(item)
    if (item.app === 'mysql-router') {
      routers.push({ item, metadata, node: databaseNode(item, metadata) })
      continue
    }
    const topology = normalizedTopology(item, metadata)
    const key = groupKey(item, metadata, topology)
    const node = databaseNode(item, metadata)
    let group = groups.get(key)
    if (!group) {
      group = {
        id: key,
        app: item.app,
        topology,
        version: item.version,
        title: groupTitle(item, metadata, topology),
        endpoint: '',
        status: item.status,
        nodeStatus: item.status,
        routerStatus: 'unknown',
        sentinelStatus: 'unknown',
        createdAt: item.createdAt,
        metadata,
        nodes: [],
        routers: [],
        sentinels: []
      }
      groups.set(key, group)
    }
    if (item.app === 'redis' && topology === 'sentinel') {
      if (nodeRunsSentinel(node)) {
        group.sentinels.push(redisSentinelNode(item, metadata))
      }
      if (nodeIsRedisDataNode(node)) {
        group.nodes.push(node)
      }
    } else {
      group.nodes.push(node)
    }
    if (new Date(item.createdAt).getTime() > new Date(group.createdAt).getTime()) {
      group.createdAt = item.createdAt
    }
  }
  for (const routerNode of routers) {
    const key = routerClusterGroupKey(routerNode.item, routerNode.metadata)
    let group = groups.get(key)
    if (!group) {
      group = {
        id: key,
        app: 'mysql',
        topology: 'innodb-cluster',
        version: routerNode.item.version,
        title: routerClusterTitle(routerNode.item, routerNode.metadata),
        endpoint: '',
        status: routerNode.item.status,
        nodeStatus: 'unknown',
        routerStatus: routerNode.item.status,
        sentinelStatus: 'unknown',
        createdAt: routerNode.item.createdAt,
        metadata: routerNode.metadata,
        nodes: [],
        routers: [],
        sentinels: []
      }
      groups.set(key, group)
    }
    group.routers.push(routerNode.node)
    if (new Date(routerNode.item.createdAt).getTime() > new Date(group.createdAt).getTime()) {
      group.createdAt = routerNode.item.createdAt
    }
  }
  return Array.from(groups.values())
    .map((group) => {
      const hydratedGroup = withRedisSentinelDiscoveredNodes(group)
      const nodes = uniqueDatabaseNodes(normalizeGroupNodeRoles(hydratedGroup))
      const routers = uniqueDatabaseNodes(hydratedGroup.routers)
      const sentinels = uniqueDatabaseNodes(hydratedGroup.sentinels)
      const normalizedGroup = {
        ...hydratedGroup,
        nodes: nodes.sort((a, b) => roleRank(a.role) - roleRank(b.role) || a.serverLabel.localeCompare(b.serverLabel)),
        routers: routers.sort((a, b) => a.serverLabel.localeCompare(b.serverLabel)),
        sentinels: sentinels.sort((a, b) => a.serverLabel.localeCompare(b.serverLabel))
      }
      const nodeStatus = normalizedGroup.app === 'mysql' && normalizedGroup.topology === 'innodb-cluster'
        ? mysqlClusterServiceStatus(normalizedGroup)
        : serviceStatus(normalizedGroup.nodes)
      const routerStatus = serviceStatus(normalizedGroup.routers)
      const sentinelStatus = serviceStatus(normalizedGroup.sentinels)
      return {
        ...normalizedGroup,
        endpoint: isUnavailable(nodeStatus) ? t('database.serviceUnavailable') : groupEndpoint(normalizedGroup),
        nodeStatus,
        routerStatus,
        sentinelStatus,
        status: groupStatus(
          nodeStatus,
          normalizedGroup.routers.length > 0 ? routerStatus : sentinelStatus,
          normalizedGroup.routers.length > 0 || normalizedGroup.sentinels.length > 0
        )
      }
    })
    .sort((a, b) => new Date(b.createdAt).getTime() - new Date(a.createdAt).getTime())
}

function normalizedTopology(item: AppInstance, metadata: InstanceMetadata) {
  return String(item.topology || metadata.topology || 'standalone')
}

function groupKey(item: AppInstance, metadata: InstanceMetadata, topology: string) {
  if (!isClusterTopology(topology)) {
    return item.id
  }
  const stable = stringValue(metadata.clusterId) || stringValue(metadata.clusterName) || stringValue(metadata.masterName)
  return stable ? `${item.app}:${topology}:${stable}` : `${item.app}:${topology}:${item.id}`
}

function routerClusterGroupKey(item: AppInstance, metadata: InstanceMetadata) {
  const stable = stringValue(metadata.clusterId) || stringValue(metadata.clusterName)
  return stable ? `mysql:innodb-cluster:${stable}` : `mysql:innodb-cluster:${item.id}`
}

function isClusterTopology(topology: string) {
  return ['innodb-cluster', 'sentinel', 'cluster', 'distributed'].includes(topology)
}

function groupTitle(item: AppInstance, metadata: InstanceMetadata, topology: string) {
  if (item.app === 'mysql' && topology === 'innodb-cluster') {
    return `mysql-${stringValue(metadata.clusterName) || item.id.slice(-6)}`
  }
  if (item.app === 'redis' && topology === 'sentinel') {
    return `redis-${stringValue(metadata.masterName) || item.id.slice(-6)}`
  }
  if (item.app === 'redis' && topology === 'cluster') {
    return `redis-${t('database.cluster')}`
  }
  return `${item.app}-${item.id.slice(-6)}`
}

function routerClusterTitle(item: AppInstance, metadata: InstanceMetadata) {
  return `mysql-${stringValue(metadata.clusterName) || stringValue(metadata.clusterId) || item.id.slice(-6)}`
}

function databaseNode(item: AppInstance, metadata: InstanceMetadata): DatabaseNode {
  const role = nodeRole(item, metadata)
  return {
    instance: item,
    metadata,
    serverLabel: serverName(item.serverId),
    endpoint: nodeEndpoint(item, metadata),
    role,
    roleLabel: roleLabel(role)
  }
}

function redisSentinelNode(item: AppInstance, metadata: InstanceMetadata): DatabaseNode {
  const role = 'sentinel'
  const sentinelMetadata = redisSentinelNodeMetadata(metadata)
  return {
    instance: {
      ...item,
      status: stringValue(sentinelMetadata.lastCheck?.status) || item.status
    },
    metadata: sentinelMetadata,
    serverLabel: serverName(item.serverId),
    endpoint: redisSentinelEndpoint(item, metadata),
    role,
    roleLabel: roleLabel(role)
  }
}

function redisSentinelNodeMetadata(metadata: InstanceMetadata): InstanceMetadata {
  const sentinelLastCheck = metadata.sentinelLastCheck
  if (!sentinelLastCheck || typeof sentinelLastCheck !== 'object') {
    return metadata
  }
  return {
    ...metadata,
    lastCheck: sentinelLastCheck
  }
}

function withRedisSentinelDiscoveredNodes(group: DatabaseGroup): DatabaseGroup {
  if (group.app !== 'redis' || group.topology !== 'sentinel') {
    return group
  }
  const next: DatabaseGroup = {
    ...group,
    nodes: [...group.nodes],
    sentinels: [...group.sentinels]
  }
  const masterEndpoint = redisMasterEndpoint(next)
  if (masterEndpoint) {
    ensureRedisDataNode(next, masterEndpoint, 'master')
  }
  for (const endpoint of groupMetadataList(next, 'replicaEndpoints')) {
    ensureRedisDataNode(next, endpoint, 'replica')
  }
  for (const endpoint of groupMetadataList(next, 'sentinelEndpoints')) {
    ensureRedisSentinelNode(next, endpoint)
  }
  return next
}

function ensureRedisDataNode(group: DatabaseGroup, endpoint: string, role: string) {
  if (!endpoint || hasEndpoint(group.nodes, endpoint)) {
    return
  }
  group.nodes.push(virtualRedisNode(group, endpoint, role))
}

function ensureRedisSentinelNode(group: DatabaseGroup, endpoint: string) {
  if (!endpoint || hasEndpoint(group.sentinels, endpoint)) {
    return
  }
  group.sentinels.push(virtualRedisNode(group, endpoint, 'sentinel'))
}

function virtualRedisNode(group: DatabaseGroup, endpoint: string, role: string): DatabaseNode {
  const server = serverByEndpoint(endpoint)
  const metadata: InstanceMetadata = {
    topology: 'sentinel',
    role,
    endpoint,
    lastCheck: { status: 'unknown' }
  }
  if (role === 'sentinel') {
    metadata.sentinel = true
  }
  const instance: AppInstance = {
    id: `virtual-${group.id}-${role}-${normalizeEndpoint(endpoint)}`,
    app: 'redis',
    version: group.version,
    serverId: server?.id || '',
    status: 'unknown',
    topology: 'sentinel',
    metadata: '',
    createdAt: group.createdAt
  }
  return {
    instance,
    metadata,
    serverLabel: server ? `${server.name} (${server.host})` : endpointHost(endpoint) || endpoint,
    endpoint,
    role,
    roleLabel: roleLabel(role),
    virtual: true
  }
}

function nodeRole(item: AppInstance, metadata: InstanceMetadata) {
  if (item.app === 'mysql-router') {
    return 'router'
  }
  const topology = normalizedTopology(item, metadata)
  if (topology === 'standalone') {
    return 'standalone'
  }
  if (item.app === 'mysql' && topology === 'innodb-cluster') {
    const currentPrimary = stringValue(metadata.currentPrimaryEndpoint)
    const endpoint = stringValue(metadata.endpoint)
    if (currentPrimary && normalizeEndpoint(endpoint) === normalizeEndpoint(currentPrimary)) {
      return 'primary'
    }
    if (currentPrimary) {
      return 'secondary'
    }
  }
  if (item.app === 'redis' && topology === 'sentinel') {
    const dataRole = redisSentinelDataRole(item, metadata)
    if (dataRole) {
      return dataRole
    }
    if (metadataBool(metadata, 'sentinel') || checkedOrStaticSentinelRole(metadata)) {
      return 'sentinel'
    }
  }
  const checkedRole = checkedMetadataRole(metadata)
  if (checkedRole) {
    return checkedRole
  }
  return 'node'
}

function redisSentinelDataRole(item: AppInstance, metadata: InstanceMetadata) {
  const staticRole = stringValue(metadata.role) || stringValue(metadata.clusterRole)
  if (['master', 'replica'].includes(staticRole)) {
    return staticRole
  }
  if (staticRole === 'sentinel' || metadataBool(metadata, 'sentinel')) {
    return ''
  }
  if (item.app === 'redis' && normalizedTopology(item, metadata) === 'sentinel') {
    const currentMaster = stringValue(metadata.currentMasterEndpoint)
    const endpoint = redisDataEndpoint(item, metadata)
    if (currentMaster && normalizeEndpoint(endpoint) === normalizeEndpoint(currentMaster)) {
      return 'master'
    }
    if (currentMaster && endpoint) {
      return 'replica'
    }
  }
  return ''
}

function normalizeGroupNodeRoles(group: DatabaseGroup) {
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    const primaryEndpoint = groupCurrentPrimaryEndpoint(group)
    if (!primaryEndpoint) {
      return group.nodes
    }
    return group.nodes.map((node) => {
      const role = normalizeEndpoint(node.endpoint) === normalizeEndpoint(primaryEndpoint) ? 'primary' : 'secondary'
      return nodeWithRole(node, role)
    })
  }
  if (group.app === 'redis' && group.topology === 'sentinel') {
    const masterEndpoint = redisMasterEndpoint(group)
    return group.nodes.map((node) => {
      if (node.role === 'sentinel') {
        return node
      }
      if (masterEndpoint && normalizeEndpoint(node.endpoint) === normalizeEndpoint(masterEndpoint)) {
        return nodeWithRole(node, 'master')
      }
      if (masterEndpoint && node.endpoint && node.endpoint !== '-') {
        return nodeWithRole(node, 'replica')
      }
      return ['master', 'replica'].includes(node.role) ? nodeWithRole(node, 'node') : node
    })
  }
  return group.nodes
}

function uniqueDatabaseNodes(nodes: DatabaseNode[]) {
  const byKey = new Map<string, DatabaseNode>()
  for (const node of nodes) {
    const key = normalizeNodeIdentity(node)
    const current = byKey.get(key)
    if (!current || shouldPreferNode(node, current)) {
      byKey.set(key, node)
    }
  }
  return Array.from(byKey.values())
}

function normalizeNodeIdentity(node: DatabaseNode) {
  const endpoint = normalizeEndpoint(node.endpoint)
  if (endpoint && endpoint !== '-') {
    return endpoint
  }
  const serverKey = node.instance.serverId || endpointHost(node.endpoint) || node.serverLabel
  return `${serverKey}:${node.role}`
}

function shouldPreferNode(candidate: DatabaseNode, current: DatabaseNode) {
  if (current.virtual !== candidate.virtual) {
    return current.virtual && !candidate.virtual
  }
  const candidateRank = roleRank(candidate.role)
  const currentRank = roleRank(current.role)
  if (candidateRank !== currentRank) {
    return candidateRank < currentRank
  }
  return nodeLastCheckedAt(candidate) > nodeLastCheckedAt(current)
}

function nodeWithRole(node: DatabaseNode, role: string): DatabaseNode {
  if (node.role === role) {
    return node
  }
  return {
    ...node,
    role,
    roleLabel: roleLabel(role)
  }
}

function checkedMetadataRole(metadata: InstanceMetadata) {
  const lastCheck = metadata.lastCheck || {}
  const status = stringValue(lastCheck.status)
  if (!['ok', 'success', 'running', 'available'].includes(status)) {
    return ''
  }
  return stringValue(metadata.role) || stringValue(metadata.clusterRole)
}

function checkedOrStaticSentinelRole(metadata: InstanceMetadata) {
  return stringValue(metadata.role) === 'sentinel' || stringValue(metadata.clusterRole) === 'sentinel'
}

function roleLabel(role: string) {
  switch (role) {
    case 'primary':
      return t('database.role.primary')
    case 'secondary':
      return t('database.role.secondary')
    case 'master':
      return t('database.role.master')
    case 'replica':
      return t('database.role.replica')
    case 'sentinel':
      return t('database.role.sentinel')
    case 'router':
      return t('database.role.router')
    case 'standalone':
      return t('database.role.standalone')
    default:
      return t('database.role.node')
  }
}

function roleTagType(role: string) {
  switch (role) {
    case 'primary':
    case 'master':
      return 'primary'
    case 'secondary':
    case 'replica':
      return 'info'
    case 'sentinel':
    case 'router':
      return 'warning'
    case 'standalone':
      return 'success'
    default:
      return 'info'
  }
}

function nodeHealthLabel(node: DatabaseNode) {
  switch (nodeHealth(node)) {
    case 'online':
      return t('database.health.online')
    case 'offline':
      return t('database.health.offline')
    case 'probing':
      return t('database.health.probing')
    default:
      return t('database.health.unknown')
  }
}

function nodeHealthType(node: DatabaseNode) {
  switch (nodeHealth(node)) {
    case 'online':
      return 'success'
    case 'offline':
      return 'danger'
    case 'probing':
      return 'warning'
    default:
      return 'info'
  }
}

function nodeHealth(node: DatabaseNode): DatabaseHealth {
  if (isMysqlInnoDBNode(node)) {
    const runtimeHealth = mysqlRuntimeHealth(node)
    if (runtimeHealth !== 'unknown') {
      return runtimeHealth
    }
  }
  return baseNodeHealth(node)
}

function baseNodeHealth(node: DatabaseNode): DatabaseHealth {
  if (serverStatusOffline(nodeServerStatus(node))) {
    return 'offline'
  }
  const lastCheckStatus = stringValue(node.metadata.lastCheck?.status)
  const instanceStatus = stringValue(node.instance.status).toLowerCase()
  const status = lastCheckStatus || instanceStatus
  if (node.virtual && !serverStatusOnline(nodeServerStatus(node))) {
    return 'unknown'
  }
  if (lastCheckStatus && statusIsOnline(lastCheckStatus)) {
    return 'online'
  }
  if (lastCheckStatus && statusIsOffline(lastCheckStatus)) {
    return 'offline'
  }
  if (statusIsOffline(instanceStatus)) {
    return 'offline'
  }
  if (statusIsOnline(instanceStatus)) {
    return 'online'
  }
  return 'unknown'
}

function isMysqlInnoDBNode(node: DatabaseNode) {
  return node.instance.app === 'mysql' && normalizedTopology(node.instance, node.metadata) === 'innodb-cluster'
}

function nodeLastCheckedAt(node: DatabaseNode) {
  const value = stringValue(node.metadata.lastCheck?.checkedAt || node.metadata.lastCheck?.details?.checkedAt)
  if (!value) {
    return 0
  }
  const time = new Date(value).getTime()
  return Number.isNaN(time) ? 0 : time
}

function nodeServerStatus(node: DatabaseNode) {
  const server = node.instance.serverId
    ? servers.value.find((item) => item.id === node.instance.serverId)
    : serverByEndpoint(node.endpoint)
  return String(server?.status || '').trim().toLowerCase()
}

function serverStatusOnline(status: string) {
  return ['available', 'running', 'success', 'ok'].includes(status)
}

function serverStatusOffline(status: string) {
  return ['failed', 'error', 'missing', 'stopped', 'offline', 'unavailable'].includes(status)
}

function statusIsOnline(status: string) {
  return ['ok', 'success', 'running', 'available'].includes(String(status ?? '').trim().toLowerCase())
}

function statusIsOffline(status: string) {
  return ['failed', 'error', 'missing', 'stopped', 'offline', 'unavailable', 'unhealthy', 'down', 'no-endpoints'].includes(String(status ?? '').trim().toLowerCase())
}

function nodeRunsSentinel(node: DatabaseNode) {
  return node.instance.app === 'redis' && normalizedTopology(node.instance, node.metadata) === 'sentinel' && metadataBool(node.metadata, 'sentinel')
}

function nodeIsRedisDataNode(node: DatabaseNode) {
  if (node.role === 'sentinel') {
    return false
  }
  const endpoint = normalizeEndpoint(node.endpoint)
  return !!endpoint && endpoint !== '-'
}

function roleRank(role: string) {
  switch (role) {
    case 'primary':
    case 'master':
      return 0
    case 'standalone':
      return 1
    case 'secondary':
    case 'replica':
      return 2
    case 'sentinel':
      return 3
    case 'router':
      return 4
    default:
      return 5
  }
}

function nodeEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const endpoint = stringValue(metadata.endpoint)
  if (endpoint) {
    return endpoint
  }
  const host = serverHost(item.serverId)
  if (!host) {
    return '-'
  }
  const topology = normalizedTopology(item, metadata)
  if (item.app === 'redis' && topology === 'sentinel' && nodeRole(item, metadata) === 'sentinel') {
    return `${host}:${numberValue(metadata.sentinelPort) || 26379}`
  }
  if (item.app === 'mysql-router') {
    return `${host}:${numberValue(metadata.basePort) || 6446}`
  }
  return `${host}:${numberValue(metadata.port) || defaultPort(item.app)}`
}

function redisSentinelEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const host = serverHost(item.serverId)
  if (!host) {
    return '-'
  }
  return `${host}:${numberValue(metadata.sentinelPort) || 26379}`
}

function redisDataEndpoint(item: AppInstance, metadata: InstanceMetadata) {
  const endpoint = stringValue(metadata.endpoint)
  if (endpoint) {
    return endpoint
  }
  const host = serverHost(item.serverId)
  if (!host) {
    return ''
  }
  return `${host}:${numberValue(metadata.port) || 6379}`
}

function groupEndpoint(group: DatabaseGroup) {
  if (group.app === 'redis' && group.topology === 'sentinel') {
    const currentMaster = redisMasterEndpoint(group)
    const masterName = groupMetadataValue(group, 'masterName')
    if (currentMaster) {
      return `${masterName || 'master'} -> ${currentMaster}`
    }
    return masterName || group.title
  }
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    const primaryEndpoint = groupCurrentPrimaryEndpoint(group)
    if (primaryEndpoint) {
      return primaryEndpoint
    }
  }
  const preferred = group.nodes.find((node) => ['primary', 'master'].includes(node.role)) || group.nodes[0]
  return preferred?.endpoint || '-'
}

function redisMasterEndpoint(group: DatabaseGroup) {
  return groupMetadataValue(group, 'currentMasterEndpoint')
}

function groupEndpointLabel(group: DatabaseGroup) {
  if (group.app === 'redis' && group.topology === 'sentinel') {
    return redisMasterEndpoint(group) ? t('database.currentMaster') : t('database.masterGroup')
  }
  if (group.app === 'mysql' && group.topology === 'innodb-cluster') {
    return groupCurrentPrimaryEndpoint(group) ? t('database.currentPrimary') : t('database.accessEndpoint')
  }
  return t('common.endpoint')
}

function showGroupEndpoint(group: DatabaseGroup) {
  return !(group.app === 'redis' && group.topology === 'sentinel')
}

function groupCurrentPrimaryEndpoint(group: DatabaseGroup) {
  return groupMetadataValue(group, 'currentPrimaryEndpoint')
}

function groupMetadataValue(group: DatabaseGroup, key: string) {
  const direct = stringValue(group.metadata[key])
  if (direct) {
    return direct
  }
  for (const node of group.nodes) {
    const value = stringValue(node.metadata[key])
    if (value) {
      return value
    }
  }
  for (const node of group.routers) {
    const value = stringValue(node.metadata[key])
    if (value) {
      return value
    }
  }
  for (const node of group.sentinels) {
    const value = stringValue(node.metadata[key])
    if (value) {
      return value
    }
  }
  return ''
}

function groupMetadataList(group: DatabaseGroup, key: string) {
  const out: string[] = []
  const add = (value: unknown) => {
    for (const item of stringListValue(value)) {
      if (!out.some((existing) => normalizeEndpoint(existing) === normalizeEndpoint(item))) {
        out.push(item)
      }
    }
  }
  add(group.metadata[key])
  for (const node of [...group.nodes, ...group.routers, ...group.sentinels]) {
    add(node.metadata[key])
  }
  return out
}

function hasEndpoint(nodes: DatabaseNode[], endpoint: string) {
  return nodes.some((node) => normalizeEndpoint(node.endpoint) === normalizeEndpoint(endpoint))
}

function serviceStatus(nodes: DatabaseNode[]) {
  const healths = nodes.map((node) => nodeHealth(node))
  if (!healths.length) {
    return 'unknown'
  }
  const realHealths = nodes.filter((node) => !node.virtual).map((node) => nodeHealth(node))
  if (realHealths.length && realHealths.every((status) => status === 'offline') && healths.every((status) => status !== 'online')) {
    return 'unavailable'
  }
  if (healths.every((status) => status === 'online')) {
    return 'running'
  }
  if (healths.every((status) => status === 'offline')) {
    return 'unavailable'
  }
  if (healths.some((status) => status === 'probing')) {
    return 'probing'
  }
  if (healths.every((status) => status === 'unknown')) {
    return 'unknown'
  }
  return 'degraded'
}

function mysqlClusterServiceStatus(group: DatabaseGroup) {
  const runtimeHealths = group.nodes.map((node) => mysqlRuntimeHealth(node))
  if (!runtimeHealths.length) {
    return 'unknown'
  }
  if (runtimeHealths.every((status) => status === 'offline')) {
    return 'unavailable'
  }
  if (runtimeHealths.some((status) => status === 'probing')) {
    return 'probing'
  }
  const clusterHealths = group.nodes.map((node) => baseNodeHealth(node))
  const hasPrimary = !!groupCurrentPrimaryEndpoint(group)
  if (hasPrimary && clusterHealths.every((status) => status === 'online')) {
    return 'running'
  }
  if (runtimeHealths.every((status) => status === 'online')) {
    return 'unavailable'
  }
  if (runtimeHealths.some((status) => status === 'online')) {
    return 'degraded'
  }
  return 'unknown'
}

function groupStatus(nodeStatus: string, routerStatus: string, hasRouters: boolean) {
  if (nodeStatus === 'unavailable') {
    return 'unavailable'
  }
  if (!hasRouters) {
    return nodeStatus
  }
  if (nodeStatus === 'probing' || routerStatus === 'probing') {
    return 'probing'
  }
  if (nodeStatus === 'running' && routerStatus === 'running') {
    return 'running'
  }
  if (nodeStatus === 'unknown' && routerStatus === 'unknown') {
    return 'unknown'
  }
  return 'degraded'
}

function isUnavailable(status: string) {
  return status === 'unavailable'
}

function isInstallFailedGroup(group: DatabaseGroup) {
  return [...group.nodes, ...group.routers, ...group.sentinels].some((node) => isInstallFailedInstance(node.instance, node.metadata))
}

function isInstallFailedInstance(instance: AppInstance, metadata: InstanceMetadata) {
  const status = String(instance.status || '').trim().toLowerCase()
  return status === 'install_failed' || metadataBool(metadata, 'installFailed')
}

function databaseServiceUnavailableText(group: DatabaseGroup) {
  return group.app === 'redis' ? t('database.redisServiceUnavailable') : t('database.mysqlServiceUnavailable')
}

function groupSubtitle(group: DatabaseGroup) {
  const parts = [`${group.topology || '-'}`, `${t('database.nodes')} ${group.nodes.length}`]
  if (group.sentinels.length) {
    parts.push(`${roleLabel('sentinel')} ${group.sentinels.length}`)
  }
  if (group.routers.length) {
    parts.push(`${t('database.mysqlRouter')} ${group.routers.length}`)
  }
  return parts.join(' / ')
}

function routerEndpointSummary(group: DatabaseGroup) {
  if (isUnavailable(group.routerStatus)) {
    return t('database.serviceUnavailable')
  }
  const endpoints = Array.from(new Set(group.routers.map((node) => node.endpoint).filter((endpoint) => endpoint && endpoint !== '-')))
  if (!endpoints.length) {
    return '-'
  }
  return endpoints.join(', ')
}

function groupSearchText(group: DatabaseGroup) {
  return [
    group.title,
    group.app,
    group.version,
    group.topology,
    group.endpoint,
    groupCurrentPrimaryEndpoint(group),
    stringValue(group.metadata.clusterName),
    stringValue(group.metadata.masterName),
    routerEndpointSummary(group),
    ...group.nodes.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel]),
    ...group.routers.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel]),
    ...group.sentinels.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel])
  ].join(' ').toLowerCase()
}

function defaultPort(app: string) {
  if (app === 'redis') {
    return 6379
  }
  if (app === 'mysql-router') {
    return 6446
  }
  return 3306
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function serverHost(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server?.host || ''
}

function serverByEndpoint(endpoint: string) {
  const host = endpointHost(endpoint)
  return servers.value.find((item) => String(item.host || '').trim() === host)
}

function endpointHost(endpoint: string) {
  const normalized = normalizeEndpoint(endpoint)
  const idx = normalized.lastIndexOf(':')
  return idx > 0 ? normalized.slice(0, idx) : normalized
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

function stringListValue(value: unknown): string[] {
  if (Array.isArray(value)) {
    return value.map((item) => stringValue(item)).filter(Boolean)
  }
  const text = stringValue(value)
  if (!text) {
    return []
  }
  if (text.startsWith('[')) {
    try {
      const parsed = JSON.parse(text)
      if (Array.isArray(parsed)) {
        return parsed.map((item) => stringValue(item)).filter(Boolean)
      }
    } catch {
      return []
    }
  }
  return text.split(',').map((item) => item.trim()).filter(Boolean)
}

function numberValue(value: unknown) {
  const parsed = Number(value)
  return Number.isFinite(parsed) && parsed > 0 ? parsed : 0
}

function metadataBool(metadata: InstanceMetadata, key: string) {
  const value = metadata[key]
  if (typeof value === 'boolean') {
    return value
  }
  return ['true', '1', 'yes'].includes(String(value ?? '').trim().toLowerCase())
}

function normalizeEndpoint(value: string) {
  return value.trim().toLowerCase().replace(/^tcp:\/\//, '').replace(/^mysql:\/\//, '').replace(/^redis:\/\//, '')
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

function openTaskById(taskId: string) {
  if (taskId) void router.push({ path: '/tasks', query: { taskId } })
}

function mysqlGroupMaintenance(group: DatabaseGroup): MySQLMaintenanceResult {
  if (group.app !== 'mysql') return { kind: 'none' }
  const clusterId = group.topology === 'innodb-cluster' ? groupMetadataValue(group, 'clusterId') : ''
  return groupMySQLMaintenance(group.topology, group.nodes.map((node) => node.instance.metadata), clusterId)
}

function mysqlGroupReconciliation(group: DatabaseGroup): MySQLReconciliationResult {
  if (group.app !== 'mysql') return { kind: 'none' }
  return groupMySQLReconciliation(group.nodes.filter((node) => !node.virtual).map((node) => ({
    instanceId: node.instance.id,
    metadata: node.instance.metadata
  })))
}

function mysqlAvailability(group: DatabaseGroup) {
  return mysqlOperationAvailability({
    app: group.app,
    topology: group.topology,
    status: group.status,
    canManage: canManageDatabase.value,
    isOwner: isOwner.value,
    nodeCount: group.nodes.filter((node) => !node.virtual).length,
    maintenance: mysqlGroupMaintenance(group),
    reconciliation: mysqlGroupReconciliation(group)
  })
}

function emptyMysqlAvailability(): MySQLOperationAvailability {
  return {
    visible: false,
    backup: false,
    records: false,
    verify: false,
    restore: false,
    disaster: false,
    clearMaintenance: false,
    reconcile: false,
    lifecycleBlocked: false,
    controlStateInvalid: false,
    reasonKey: ''
  }
}

function mysqlActionReason(group: DatabaseGroup) {
  const availability = mysqlAvailability(group)
  if (availability.reasonKey) return t(availability.reasonKey)
  if (!availability.restore && !isOwner.value) return t('database.mysqlBackup.ownerRequired')
  return t('database.mysqlBackup.actionUnavailable')
}

function maintenanceSummary(group: DatabaseGroup) {
  const maintenance = mysqlGroupMaintenance(group)
  if (maintenance.kind !== 'required') return ''
  return t('database.mysqlBackup.maintenanceSummary', {
    backup: maintenance.state.backupId,
    task: maintenance.state.taskId,
    phase: t(`database.mysqlBackup.phase.${maintenance.state.restorePhase}`),
    time: new Date(maintenance.state.recordedAt).toLocaleString()
  })
}

function reconciliationSummary(group: DatabaseGroup) {
  const reconciliation = mysqlGroupReconciliation(group)
  if (reconciliation.kind !== 'required') return ''
  const node = group.nodes.find((candidate) => candidate.instance.id === reconciliation.instanceId)
  return t('database.mysqlBackup.reconciliationSummary', {
    instance: node?.serverLabel || reconciliation.instanceId,
    value: reconciliation.state.originalValue,
    task: reconciliation.state.taskId,
    time: new Date(reconciliation.state.recordedAt).toLocaleString()
  })
}

function isReconciliationSubmitting(group: DatabaseGroup) {
  const reconciliation = mysqlGroupReconciliation(group)
  return reconciliation.kind === 'required' && reconciliationSubmittingId.value === reconciliation.instanceId
}

async function runReconciliation(group: DatabaseGroup) {
  const reconciliation = mysqlGroupReconciliation(group)
  if (!mysqlAvailability(group).reconcile || reconciliation.kind !== 'required' || reconciliationSubmittingId.value) return
  activateMySQLGroup(group)
  reconciliationSubmittingId.value = reconciliation.instanceId
  try {
    await runMySQLReconciliation(reconciliation.instanceId, taskProgress, t('database.mysqlBackup.reconciliationTaskLabel'))
    ElMessage.success(t('database.mysqlBackup.reconciliationAccepted'))
    await load()
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.reconciliationFailed'))
  } finally {
    reconciliationSubmittingId.value = ''
  }
}

function representativeMySQLNode(group: DatabaseGroup | null) {
  if (!group) return undefined
  return group.nodes.find((node) => node.role === 'primary') || group.nodes.find((node) => !node.virtual)
}

function mysqlRestoreTarget(group: DatabaseGroup | null): MySQLRestoreTarget & { label: string } {
  const node = representativeMySQLNode(group)
  return {
    topology: group?.topology || '',
    mysqlVersion: group?.version || '',
    instanceId: node?.instance.id || '',
    serverId: node?.instance.serverId || '',
    clusterId: group?.topology === 'innodb-cluster' ? groupMetadataValue(group, 'clusterId') : undefined,
    label: group?.title || '-'
  }
}

function activateMySQLGroup(group: DatabaseGroup) {
  activeMysqlGroupKey.value = group.id
  activeBackup.value = null
  backupRecords.value = []
  backupListDefaults.value = { threads: 4, maxRateMBps: 0 }
}

async function refreshActiveBackups() {
  const group = activeMysqlGroup.value
  if (!group) return false
  backupListLoading.value = true
  try {
    const response = await listMySQLBackups(mysqlRestoreTarget(group).instanceId)
    backupRecords.value = response.items
    backupListDefaults.value = response.defaults
    return true
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.listFailed'))
    return false
  } finally {
    backupListLoading.value = false
  }
}

async function loadBackupsForGroup(group: DatabaseGroup) {
  activateMySQLGroup(group)
  return refreshActiveBackups()
}

async function openMySQLBackup(group: DatabaseGroup) {
  if (!mysqlAvailability(group).backup) return
  if (!await loadBackupsForGroup(group)) return
  backupDialogVisible.value = true
}

async function openMySQLBackupRecords(group: DatabaseGroup) {
  activateMySQLGroup(group)
  backupDrawerVisible.value = true
  await refreshActiveBackups()
}

function compatibleBackup(group: DatabaseGroup) {
  const target = mysqlRestoreTarget(group)
  return backupRecords.value.find((record) => backupTargetCompatibility(record, target).compatible) || null
}

async function verifyLatestMySQLBackup(group: DatabaseGroup) {
  if (!mysqlAvailability(group).verify) return
  if (!await loadBackupsForGroup(group)) return
  const record = latestVerifiableMySQLBackup(backupRecords.value)
  if (!record) {
    ElMessage.warning(t('database.mysqlBackup.noVerifiableBackup'))
    return
  }
  await verifySelectedMySQLBackup(record)
}

async function verifySelectedMySQLBackup(record: MySQLBackupRecord) {
  if (!activeAvailability.value.verify || !isMySQLBackupVerifiable(record)) return
  try {
    await verifyMySQLBackup(record.id, taskProgress, t('database.mysqlBackup.verifyTaskLabel'))
    ElMessage.success(t('database.mysqlBackup.verifyAccepted'))
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.verifyFailed'))
  }
}

async function openLatestMySQLRestore(group: DatabaseGroup) {
  if (!mysqlAvailability(group).restore) return
  if (!await loadBackupsForGroup(group)) return
  const record = compatibleBackup(group)
  if (!record) {
    backupDrawerVisible.value = true
    ElMessage.warning(t('database.mysqlBackup.noCompatibleBackup'))
    return
  }
  openSelectedMySQLRestore(record)
}

function openSelectedMySQLRestore(record: MySQLBackupRecord) {
  const group = activeMysqlGroup.value
  if (!group || !mysqlAvailability(group).restore || !backupTargetCompatibility(record, mysqlRestoreTarget(group)).compatible) return
  activeBackup.value = record
  backupDrawerVisible.value = false
  restoreDialogVisible.value = true
}

async function openLatestMySQLDisaster(group: DatabaseGroup) {
  if (!mysqlAvailability(group).disaster) return
  if (!await loadBackupsForGroup(group)) return
  const current = activeMysqlGroup.value
  if (!current) return
  const selection = selectMaintenanceDisasterBackup(backupRecords.value, mysqlGroupMaintenance(current), mysqlRestoreTarget(current))
  if (!selection.backup) {
    ElMessage.warning(t(selection.reasonKey))
    return
  }
  activeBackup.value = selection.backup
  disasterDialogVisible.value = true
}

function openMaintenanceClear(group: DatabaseGroup) {
  const maintenance = mysqlGroupMaintenance(group)
  if (!mysqlAvailability(group).clearMaintenance || maintenance.kind !== 'required') return
  activateMySQLGroup(group)
  maintenanceClearIdentity.value = mysqlMaintenanceIdentity(maintenance)
  maintenanceClearConfirmed.value = false
  maintenanceClearVisible.value = true
}

async function confirmMaintenanceClear() {
  if (!maintenanceClearConfirmed.value) return
  const group = activeMysqlGroup.value
  const maintenance = group ? mysqlGroupMaintenance(group) : { kind: 'invalid' as const }
  if (!group || !activeAvailability.value.clearMaintenance || mysqlMaintenanceIdentity(maintenance) !== maintenanceClearIdentity.value) {
    ElMessage.warning(t('database.mysqlBackup.staleOperationBlocked'))
    return
  }
  maintenanceClearSubmitting.value = true
  try {
    await clearMySQLMaintenance(activeTarget.value.instanceId, taskProgress, t('database.mysqlBackup.clearTaskLabel'))
    maintenanceClearVisible.value = false
    maintenanceClearConfirmed.value = false
    maintenanceClearIdentity.value = ''
    ElMessage.success(t('database.mysqlBackup.clearAccepted'))
  } catch (error) {
    ElMessage.error(error instanceof Error ? error.message : t('database.mysqlBackup.clearFailed'))
  } finally {
    maintenanceClearSubmitting.value = false
  }
}

function mysqlMaintenanceIdentity(maintenance: MySQLMaintenanceResult) {
  if (maintenance.kind !== 'required') return ''
  const state = maintenance.state
  return JSON.stringify([
    state.version,
    state.state,
    state.reason,
    state.scope,
    state.clusterId ?? '',
    state.backupId,
    state.taskId,
    state.restorePhase,
    state.recordedAt
  ])
}

function guardActiveBackupSubmission() {
  return !!activeMysqlGroup.value && activeAvailability.value.backup
}

function guardActiveRestoreSubmission() {
  const group = activeMysqlGroup.value
  const backup = activeBackup.value
  return !!group && !!backup && mysqlAvailability(group).restore && backupTargetCompatibility(backup, mysqlRestoreTarget(group)).compatible
}

function guardActiveDisasterSubmission() {
  const group = activeMysqlGroup.value
  const backup = activeBackup.value
  if (!group || !backup || !mysqlAvailability(group).disaster) return false
  const selection = selectMaintenanceDisasterBackup(backupRecords.value, mysqlGroupMaintenance(group), mysqlRestoreTarget(group))
  return selection.backup?.id === backup.id
}

function hasMysqlGroupDelete(group: DatabaseGroup) {
  return group.app === 'mysql' && mysqlGroupDeleteNodes(group).length > 0
}

function hasMysqlClusterStart(group: DatabaseGroup) {
  return group.app === 'mysql' && group.topology === 'innodb-cluster' && group.nodes.length > 0
}

function isMysqlClusterStartDisabled(group: DatabaseGroup) {
  return !canManageDatabase.value || mysqlGroupMaintenance(group).kind !== 'none' || !isMysqlClusterStartable(group) || (!!startingClusterId.value && startingClusterId.value !== group.id)
}

function mysqlClusterStartReason(group: DatabaseGroup) {
  if (!canManageDatabase.value) return deniedText.value
  if (mysqlGroupMaintenance(group).kind !== 'none') return mysqlActionReason(group)
  return t('database.mysqlBackup.actionUnavailable')
}

function isMysqlClusterStartable(group: DatabaseGroup) {
  const nodes = group.nodes.filter((node) => !node.virtual)
  return nodes.length === 3 && isMysqlClusterIneffective(group) && nodes.every((node) => mysqlRuntimeHealth(node) === 'online')
}

function isMysqlClusterIneffective(group: DatabaseGroup) {
  return ['unavailable', 'degraded', 'failed', 'error'].includes(group.nodeStatus)
}

function mysqlRuntimeHealth(node: DatabaseNode): DatabaseHealth {
  if (serverStatusOffline(nodeServerStatus(node))) {
    return 'offline'
  }
  const runtimeStatus = mysqlRuntimeStatus(node)
  if (statusIsOnline(runtimeStatus)) {
    return 'online'
  }
  if (statusIsOffline(runtimeStatus)) {
    return 'offline'
  }
  return baseNodeHealth(node)
}

function mysqlRuntimeStatus(node: DatabaseNode) {
  const details = node.metadata.lastCheck?.details || {}
  return stringValue(
    details.runtimeStatus ||
    details.mysqlServiceStatus ||
    details.mysqlPortStatus ||
    node.metadata.mysqlRuntimeStatus ||
    node.metadata.runtimeStatus
  )
}

function hasRedisGroupDelete(group: DatabaseGroup) {
  return group.app === 'redis' && redisGroupDeleteNodes(group).length > 0
}

function showNodeDeleteButton(group: DatabaseGroup, node?: DatabaseNode) {
  if (node?.virtual) {
    return false
  }
  if (group.app === 'redis' || group.app === 'mysql') {
    return false
  }
  return !(group.app === 'mysql' && group.topology === 'innodb-cluster')
}

function showSentinelDeleteButton(group: DatabaseGroup, node: DatabaseNode) {
  return showNodeDeleteButton(group, node) && !redisSentinelDataRole(node.instance, node.metadata)
}

function openDeleteGroup(group: DatabaseGroup, kind: DeleteScopeKind) {
  if (group.app === 'mysql' && mysqlGroupMaintenance(group).kind !== 'none') {
    ElMessage.warning(mysqlActionReason(group))
    return
  }
  const nodes = groupDeleteNodes(group, kind)
  openDeleteNodes(nodes, kind, group)
}

function groupDeleteNodes(group: DatabaseGroup, kind: DeleteScopeKind) {
  if (kind === 'mysql-group') {
    return mysqlGroupDeleteNodes(group)
  }
  if (kind === 'redis-group') {
    return redisGroupDeleteNodes(group)
  }
  return group.nodes
}

function mysqlGroupDeleteNodes(group: DatabaseGroup) {
  if (group.app !== 'mysql') {
    return []
  }
  if (group.topology === 'innodb-cluster') {
    if (group.nodes.filter((node) => !node.virtual).length !== 3) return []
    return uniqueRealNodes([...group.routers, ...group.nodes])
  }
  return uniqueRealNodes(group.nodes)
}

function redisGroupDeleteNodes(group: DatabaseGroup) {
  return uniqueRealNodes([...group.nodes, ...group.sentinels])
}

function uniqueRealNodes(nodes: DatabaseNode[]) {
  const seen = new Set<string>()
  const out: DatabaseNode[] = []
  for (const node of nodes) {
    const id = node.instance.id
    if (node.virtual || !id || !node.instance.serverId || seen.has(id)) {
      continue
    }
    seen.add(id)
    out.push(node)
  }
  return out
}

async function startMysqlCluster(group: DatabaseGroup) {
  if (!canManageDatabase.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const current = instanceGroups.value.find((item) => item.id === group.id)
  if (!current || mysqlGroupMaintenance(current).kind !== 'none' || !isMysqlClusterStartable(current)) {
    ElMessage.warning(t('database.mysqlBackup.staleOperationBlocked'))
    return
  }
  const instanceIds = current.nodes.filter((node) => !node.virtual).map((node) => node.instance.id).filter(Boolean)
  if (!instanceIds.length) {
    return
  }
  startingClusterId.value = group.id
  try {
    const result = await apiPost<{ taskId: string }>('/database/mysql/clusters/start', { instanceIds })
    ElMessage.success(t('database.startMysqlClusterAccepted'))
    taskProgress.track(result.taskId, t('database.startMysqlCluster'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('database.startMysqlClusterFailed'))
  } finally {
    startingClusterId.value = ''
  }
}

function openDeleteNodes(nodes: DatabaseNode[], kind: DeleteScopeKind, group?: DatabaseGroup) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const cleanNodes = uniqueRealNodes(nodes)
  if (!cleanNodes.length) {
    return
  }
  pendingDeleteScope.value = {
    kind,
    title: deleteScopeTitle(kind),
    message: deleteScopeMessage(kind, cleanNodes, group),
    nodes: cleanNodes,
    ...(group ? { groupId: group.id } : {})
  }
  const initialPasswords: Record<string, string> = {}
  for (const node of cleanNodes) {
    if (node.instance.serverId) {
      initialPasswords[node.instance.serverId] = ''
    }
  }
  deletePasswords.value = initialPasswords
  deletePromptVisible.value = true
}

function deleteScopeTitle(kind: DeleteScopeKind) {
  if (kind === 'mysql-group') {
    return t('database.uninstallMysql')
  }
  if (kind === 'redis-group') {
    return t('database.uninstallRedisGroup')
  }
  return t('apps.uninstallService')
}

function deleteScopeMessage(kind: DeleteScopeKind, nodes: DatabaseNode[], group?: DatabaseGroup) {
  if (kind === 'single') {
    return t('apps.deleteServicePasswordPrompt', { server: nodes[0]?.serverLabel || '' })
  }
  const name = deleteScopeName(kind, group)
  return t('database.uninstallGroupPasswordPrompt', {
    name,
    count: uniqueServerCount(nodes)
  })
}

function deleteScopeName(kind: DeleteScopeKind, group?: DatabaseGroup) {
  if (kind === 'mysql-group' && group?.routers.length) {
    return `${group.title} / ${t('database.mysqlRouter')}`
  }
  return group?.title || t('database.cluster')
}

function uniqueServerCount(nodes: DatabaseNode[]) {
  return new Set(nodes.map((node) => node.instance.serverId).filter(Boolean)).size
}

function resetDeletePrompt() {
  if (!deleteSubmitting.value) {
    pendingDeleteScope.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
  }
}

async function confirmDeleteScope() {
  const scope = pendingDeleteScope.value
  if (!scope) {
    return
  }
  if (!canManageApps.value) {
    ElMessage.warning(t('database.mysqlBackup.staleOperationBlocked'))
    return
  }
  if (scope.kind === 'mysql-group') {
    const current = instanceGroups.value.find((group) => group.id === scope.groupId)
    if (!current || mysqlGroupMaintenance(current).kind !== 'none' || mysqlGroupDeleteNodes(current).length === 0) {
      ElMessage.warning(t('database.mysqlBackup.staleOperationBlocked'))
      return
    }
    scope.nodes = mysqlGroupDeleteNodes(current)
  }
  const passwords = deletePasswordPayload()
  if (Object.keys(passwords).length !== deleteServers.value.length) {
    ElMessage.warning(t('database.deletePasswordsRequired'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>('/apps/instances/batch-delete', {
      instanceIds: scope.nodes.map((node) => node.instance.id),
      serverPasswords: passwords
    })
    deletePromptVisible.value = false
    pendingDeleteScope.value = null
    deletePasswords.value = {}
    sameDeletePassword.value = false
    deleteSharedPassword.value = ''
    ElMessage.success(t('database.uninstallTaskAccepted', { count: scope.nodes.length }))
    taskProgress.track(result.taskId, deleteScopeTitle(scope.kind))
  } catch (err) {
    ElMessage.error(deleteErrorMessage(err))
  } finally {
    deleteSubmitting.value = false
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

function deleteErrorMessage(err: unknown) {
  if (!(err instanceof Error)) {
    return t('apps.deleteServiceFailed')
  }
  const details = (err as Error & { details?: Record<string, unknown> }).details
  const serverId = stringValue(details?.serverId)
  if (serverId) {
    return `${serverName(serverId)}: ${err.message}`
  }
  return err.message
}

onMounted(async () => {
  await load()
})
</script>

<style scoped>
.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
}

.database-main {
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

.db-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr));
  gap: 16px;
  padding: 24px;
  min-height: 0;
  overflow: auto;
}

.db-card {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 16px;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease;
}

.db-card:hover {
  border-color: #91caff;
  box-shadow: var(--aifar-shadow-raised);
  transform: translateY(-1px);
}

.db-head {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  margin-bottom: 10px;
}

.db-head-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
  max-width: 320px;
}

.db-title-block {
  min-width: 0;
  flex: 1;
}

.db-head strong,
.db-head span {
  display: block;
}

.db-head strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.db-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
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

.db-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.db-grid div {
  min-height: 54px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f7fbff;
  padding: 8px;
  min-width: 0;
}

.db-grid .db-grid-wide {
  grid-column: 1 / -1;
}

.db-grid span {
  display: block;
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.db-grid strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.db-grid-wide strong {
  overflow: visible;
  text-overflow: clip;
  white-space: normal;
  word-break: break-word;
  line-height: 1.35;
}

.db-grid-tag {
  margin-top: 6px;
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

.maintenance-banner {
  display: grid;
  gap: 4px;
  margin-top: 16px;
  padding: 12px 16px;
  border: 1px solid #ffe58f;
  border-radius: 8px;
  background: #fffbe6;
  color: #874d00;
  line-height: 20px;
}

.maintenance-banner.invalid {
  border-color: #ffccc7;
  background: #fff2f0;
  color: #a8071a;
}

.maintenance-banner span {
  font-size: 12px;
}

.mysql-backup-actions {
  display: flex;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 16px;
  padding-top: 16px;
  border-top: 1px solid var(--aifar-border-soft);
}

.mysql-backup-actions :deep(.el-button + .el-button) {
  margin-left: 0;
}

.maintenance-clear-confirm {
  align-items: flex-start;
  margin-top: 24px;
  white-space: normal;
}

.node-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}

.router-list {
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

.node-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: center;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #fff;
}

.router-row {
  background: #f8fbff;
}

.node-main {
  min-width: 0;
}

.node-main strong,
.node-main span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.node-main span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.node-tags {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  white-space: nowrap;
}

.settings-grid {
  padding: 12px;
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

@media (max-width: 767px) {
  .monitor-actions {
    flex-wrap: wrap;
    justify-content: flex-start;
  }

  .db-head {
    flex-wrap: wrap;
  }

  .db-head-actions {
    justify-content: flex-start;
    max-width: none;
    width: 100%;
  }

  .node-row {
    grid-template-columns: minmax(0, 1fr) auto;
  }

  .node-tags {
    justify-content: flex-start;
  }

  .db-card-grid {
    grid-template-columns: 1fr;
    padding: 16px;
  }

  .mysql-backup-actions {
    justify-content: flex-start;
  }

}
</style>
