<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('storage.title') }}</h1>
        <p class="page-subtitle">{{ t('storage.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <span v-if="visibleManagementHeaderActions.storage.includes('connected')" class="status-pill success">{{ t('common.connected') }}</span>
      </div>
    </div>

    <div class="aifar-panel status-line">
      <span class="subtle-note">{{ t('storage.instanceCount', { count: minioInstances.length }) }}</span>
      <span class="status-pill" :class="{ success: canManageApps }">{{ monitoringStatusLabel }}</span>
      <span v-if="lastMonitorAt" class="subtle-note">{{ t('storage.lastMonitoredAt') }} {{ lastMonitorAt }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane v-for="tabName in visibleManagementTabs.storage" :key="tabName" :label="t('storage.instances')" :name="tabName" />
    </el-tabs>

    <div class="workspace-card storage-main">
      <div class="table-toolbar">
        <el-input v-model="search" :placeholder="t('storage.search')" clearable class="toolbar-control" />
        <div v-if="tab !== 'instances'" class="head-actions">
          <el-select v-if="tab !== 'instances'" v-model="selectedInstanceId" :placeholder="t('storage.selectInstance')" class="toolbar-control">
            <el-option v-for="item in instances" :key="item.id" :label="instanceLabel(item)" :value="item.id" />
          </el-select>
          <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
            <span><el-button v-if="tab === 'buckets'" type="primary" :disabled="!canManageStorage" @click="openItemDialog('bucket')">{{ t('storage.addBucket') }}</el-button></span>
          </el-tooltip>
          <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
            <span><el-button v-if="tab === 'objects'" type="primary" :disabled="!canManageStorage" @click="openItemDialog('object')">{{ t('storage.addObject') }}</el-button></span>
          </el-tooltip>
          <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
            <span><el-button v-if="tab === 'access'" :disabled="!canManageStorage" @click="openItemDialog('user')">{{ t('storage.addUser') }}</el-button></span>
          </el-tooltip>
          <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
            <span><el-button v-if="tab === 'access'" type="primary" :disabled="!canManageStorage" @click="openItemDialog('accessKey')">{{ t('storage.addAccessKey') }}</el-button></span>
          </el-tooltip>
          <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
            <span><el-button v-if="tab === 'replica'" type="primary" :disabled="!canManageStorage" @click="openItemDialog('replica')">{{ t('storage.addReplica') }}</el-button></span>
          </el-tooltip>
        </div>
      </div>

      <template v-if="tab === 'instances'">
        <div v-if="storageGroups.length" class="storage-resource-shell">
          <div class="storage-resource-list">
          <article
            v-for="group in storageGroups"
            :key="group.id"
            class="storage-resource-row"
            :class="{ active: activeStorageWorkbenchGroup?.id === group.id }"
            @click="selectStorageResource(group)"
          >
            <div class="storage-head">
              <div class="app-icon small">S3</div>
              <div class="storage-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ group.topology }} / {{ t('storage.minioNodes') }} {{ group.nodes.length }}</span>
                <div class="storage-summary">
                  <span><strong>{{ t('common.version') }}</strong>{{ group.version || '-' }}</span>
                  <span><strong>{{ t('dashboard.topology') }}</strong>{{ group.topology || '-' }}</span>
                  <span><strong>{{ t('storage.minioNodes') }}</strong>{{ group.nodes.length }}</span>
                  <span class="storage-summary-wide"><strong>{{ t('storage.assignedCapacity') }}</strong>{{ groupCapacityText(group) }}</span>
                </div>
              </div>
              <div class="storage-head-actions">
                <StatusTag :status="group.status" />
                <el-button size="small" @click.stop="openStorageResource(group)">{{ t('common.details') }}</el-button>
              </div>
            </div>




          </article>
          </div>
          <aside v-if="activeStorageWorkbenchGroup" class="resource-inline-detail storage-inline-detail">
            <div class="storage-head detail-head">
              <div class="app-icon small">S3</div>
              <div class="storage-title-block">
                <strong>{{ activeStorageWorkbenchGroup.title }}</strong>
                <span>{{ activeStorageWorkbenchGroup.topology }} / {{ t('storage.minioNodes') }} {{ activeStorageWorkbenchGroup.nodes.length }}</span>
              </div>
              <div class="storage-head-actions">
                <StatusTag :status="activeStorageWorkbenchGroup.status" />
              </div>
            </div>
            <div class="storage-summary detail-summary">
              <span><strong>{{ t('common.version') }}</strong>{{ activeStorageWorkbenchGroup.version || '-' }}</span>
              <span><strong>{{ t('dashboard.topology') }}</strong>{{ activeStorageWorkbenchGroup.topology || '-' }}</span>
              <span><strong>{{ t('storage.minioNodes') }}</strong>{{ activeStorageWorkbenchGroup.nodes.length }}</span>
              <span><strong>{{ t('storage.assignedCapacity') }}</strong>{{ groupCapacityText(activeStorageWorkbenchGroup) }}</span>
              <span><strong>{{ t('storage.storageUsedAvailable') }}</strong>{{ groupUsedAvailableText(activeStorageWorkbenchGroup) }}</span>
              <span class="storage-summary-wide"><strong>{{ t('storage.replicationBuckets') }}</strong>{{ displayBuckets(activeStorageWorkbenchGroup) }}</span>
            </div>
            <div class="storage-node-list">
              <div class="section-label">{{ t('storage.minioNodes') }}</div>
              <div v-for="node in activeStorageWorkbenchGroup.nodes" :key="node.instance.id" class="storage-node-row">
                <div class="storage-node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint }}</span>
                  <small>{{ nodeInsightText(node) }}</small>
                </div>
                <div class="storage-node-tags">
                  <el-tag size="small" type="info">{{ node.roleLabel }}</el-tag>
                  <StatusTag :status="node.status" />
                </div>
              </div>
            </div>
            <div class="inline-detail-actions">
              <el-button size="small" @click="openStorageResource(activeStorageWorkbenchGroup)">{{ t('common.details') }}</el-button>
            </div>
          </aside>
        </div>
        <div v-else class="empty-state storage-empty">
          <div>
            <strong>{{ t('storage.emptyTitle') }}</strong>
            <span>{{ t('storage.emptyDesc') }}</span>
          </div>
        </div>
      </template>

      <template v-else-if="tab === 'access'">
        <div class="access-grid">
          <div class="sub-panel">
            <h2 class="section-title">{{ t('storage.users') }}</h2>
            <el-table :data="collection.users" height="100%">
              <el-table-column prop="name" :label="t('storage.name')" min-width="160" />
              <el-table-column prop="policy" :label="t('storage.policy')" min-width="160" />
              <el-table-column prop="createdAt" :label="t('common.time')" min-width="180" />
              <el-table-column :label="t('common.operation')" width="100">
                <template #default="{ row }">
                  <ConfirmAction
                    :message="t('storage.confirmDeleteItem')"
                    :title="t('common.delete')"
                    :confirm-text="t('common.delete')"
                    :cancel-text="t('common.cancel')"
                    :disabled="!canManageStorage"
                    @confirm="deleteItem('user', row.id)"
                  >
                    <template #default="{ confirm }">
                      <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
                        <span><el-button size="small" type="danger" plain :disabled="!canManageStorage" @click="confirm">{{ t('common.delete') }}</el-button></span>
                      </el-tooltip>
                    </template>
                  </ConfirmAction>
                </template>
              </el-table-column>
            </el-table>
          </div>
          <div class="sub-panel">
            <h2 class="section-title">{{ t('storage.accessKeys') }}</h2>
            <el-table :data="collection.accessKeys" height="100%">
              <el-table-column prop="name" :label="t('storage.name')" min-width="140" />
              <el-table-column prop="accessKey" :label="t('storage.accessKey')" min-width="180" show-overflow-tooltip />
              <el-table-column prop="policy" :label="t('storage.policy')" min-width="140" />
              <el-table-column :label="t('common.operation')" width="100">
                <template #default="{ row }">
                  <ConfirmAction
                    :message="t('storage.confirmDeleteItem')"
                    :title="t('common.delete')"
                    :confirm-text="t('common.delete')"
                    :cancel-text="t('common.cancel')"
                    :disabled="!canManageStorage"
                    @confirm="deleteItem('accessKey', row.id)"
                  >
                    <template #default="{ confirm }">
                      <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
                        <span><el-button size="small" type="danger" plain :disabled="!canManageStorage" @click="confirm">{{ t('common.delete') }}</el-button></span>
                      </el-tooltip>
                    </template>
                  </ConfirmAction>
                </template>
              </el-table-column>
            </el-table>
          </div>
        </div>
      </template>

      <template v-else-if="tab === 'runs'">
        <RunRecordTable :records="runTasks" show-details @details="openTaskDetails" />
      </template>

      <template v-else-if="tab === 'settings'">
        <div class="settings-grid">
          <KeyValueGrid :items="settingsItems" />
        </div>
      </template>

      <template v-else>
        <div v-if="selectedInstanceId" class="collection-panel">
          <p v-if="tab === 'objects'" class="muted-strip">{{ t('storage.objectsHint') }}</p>
          <p v-if="tab === 'replica'" class="muted-strip">{{ t('storage.replicaHint') }}</p>
          <el-table :data="activeCollection" height="100%">
            <el-table-column prop="name" :label="t('storage.name')" min-width="180" />
            <el-table-column prop="policy" :label="t('storage.policy')" min-width="160" show-overflow-tooltip />
            <el-table-column prop="accessKey" :label="t('storage.accessKey')" min-width="180" show-overflow-tooltip />
            <el-table-column prop="createdAt" :label="t('common.time')" min-width="180" />
            <el-table-column :label="t('common.operation')" width="100">
              <template #default="{ row }">
                <ConfirmAction
                  :message="t('storage.confirmDeleteItem')"
                  :title="t('common.delete')"
                  :confirm-text="t('common.delete')"
                  :cancel-text="t('common.cancel')"
                  :disabled="!canManageStorage"
                  @confirm="deleteItem(activeKind, row.id)"
                >
                  <template #default="{ confirm }">
                    <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
                      <span><el-button size="small" type="danger" plain :disabled="!canManageStorage" @click="confirm">{{ t('common.delete') }}</el-button></span>
                    </el-tooltip>
                  </template>
                </ConfirmAction>
              </template>
            </el-table-column>
          </el-table>
        </div>
        <div v-else class="empty-state"><div><strong>{{ t('storage.noInstanceSelected') }}</strong><span>{{ t('storage.emptyDesc') }}</span></div></div>
      </template>
    </div>

    <el-drawer
      v-model="storageDetailVisible"
      :title="activeStorageGroup?.title || t('storage.instances')"
      size="min(720px, calc(100vw - 24px))"
      class="storage-detail-drawer"
    >
      <div v-if="activeStorageGroup" class="resource-detail-stack">
        <div class="storage-head detail-head">
          <div class="app-icon small">S3</div>
          <div class="storage-title-block">
            <strong>{{ activeStorageGroup.title }}</strong>
            <span>{{ activeStorageGroup.topology }} / {{ t('storage.minioNodes') }} {{ activeStorageGroup.nodes.length }}</span>
          </div>
          <div class="storage-head-actions">
            <StatusTag :status="activeStorageGroup.status" />
          </div>
        </div>

        <div class="storage-summary detail-summary">
          <span><strong>{{ t('common.version') }}</strong>{{ activeStorageGroup.version || '-' }}</span>
          <span><strong>{{ t('dashboard.topology') }}</strong>{{ activeStorageGroup.topology || '-' }}</span>
          <span><strong>{{ t('storage.minioNodes') }}</strong>{{ activeStorageGroup.nodes.length }}</span>
          <span><strong>{{ t('storage.assignedCapacity') }}</strong>{{ groupCapacityText(activeStorageGroup) }}</span>
          <span><strong>{{ t('storage.storageUsedAvailable') }}</strong>{{ groupUsedAvailableText(activeStorageGroup) }}</span>
          <span><strong>{{ t('storage.cleanupPolicy') }}</strong>{{ groupCleanupPolicyText(activeStorageGroup) }}</span>
          <span><strong>{{ t('storage.cleanupEstimate') }}</strong>{{ groupCleanupEstimateText(activeStorageGroup) }}</span>
          <span class="storage-summary-wide"><strong>{{ t('storage.replicationBuckets') }}</strong>{{ displayBuckets(activeStorageGroup) }}</span>
          <span v-if="isBucketReplication(activeStorageGroup)" class="storage-summary-wide"><strong>{{ t('storage.replicationProfile') }}</strong>{{ replicationProfileText(activeStorageGroup) }}</span>
        </div>

        <div v-if="isInstallFailedGroup(activeStorageGroup)" class="service-notice danger">{{ t('apps.installFailedCleanupHint') }}</div>

        <div v-if="isBucketReplication(activeStorageGroup)" class="bucket-sync-list">
          <div class="section-label">{{ t('storage.bucketSync') }}</div>
          <div v-for="pair in replicationPairs(activeStorageGroup)" :key="pair.key" class="sync-row">
            <div class="sync-bucket">{{ pair.bucket }}</div>
            <div class="sync-endpoint">
              <span>{{ pair.source?.roleLabel || '-' }}</span>
              <strong>{{ syncEndpointLabel(pair.source, pair.bucket) }}</strong>
            </div>
            <div class="sync-arrow">{{ t('storage.twoWaySync') }}</div>
            <div class="sync-endpoint">
              <span>{{ pair.target?.roleLabel || '-' }}</span>
              <strong>{{ syncEndpointLabel(pair.target, pair.bucket) }}</strong>
            </div>
          </div>
        </div>

        <div class="storage-node-list">
          <div class="section-label">{{ t('storage.minioNodes') }}</div>
          <div v-for="node in activeStorageGroup.nodes" :key="node.instance.id" class="storage-node-row">
            <div class="storage-node-main">
              <strong>{{ node.serverLabel }}</strong>
              <span>{{ node.endpoint }}</span>
              <small>{{ nodeInsightText(node) }}</small>
              <div v-if="node.storageDisks.length" class="storage-disk-list">
                <div v-for="disk in node.storageDisks" :key="`${node.instance.id}:${disk.index}:${disk.path}`" class="storage-disk-row">
                  <div>
                    <strong>{{ diskLabel(disk) }}</strong>
                    <span>{{ disk.device || '-' }}</span>
                  </div>
                  <div>
                    <strong>{{ formatBytes(disk.totalBytes) }}</strong>
                    <span>{{ diskUsageText(disk) }}</span>
                  </div>
                  <small>{{ disk.mountPoint || '-' }} / {{ disk.path }}</small>
                </div>
              </div>
            </div>
            <div class="storage-node-tags">
              <el-tag size="small" type="info">{{ node.roleLabel }}</el-tag>
              <StatusTag :status="node.status" />
            </div>
          </div>
        </div>
      </div>
    </el-drawer>

    <el-dialog v-model="itemDialogVisible" :title="dialogTitle" width="520px">
      <el-form label-position="top">
        <el-form-item :label="t('storage.name')">
          <el-input v-model="itemForm.name" />
        </el-form-item>
        <el-form-item v-if="itemKind !== 'bucket' && itemKind !== 'object'" :label="t('storage.policy')">
          <el-input v-model="itemForm.policy" />
        </el-form-item>
        <el-form-item v-if="itemKind === 'accessKey'" :label="t('storage.accessKey')">
          <el-input v-model="itemForm.accessKey" />
        </el-form-item>
        <el-form-item v-if="itemKind === 'accessKey'" :label="t('storage.secretKey')">
          <el-input v-model="itemForm.secretKey" type="password" show-password />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="itemDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
          <span><el-button type="primary" :disabled="!canManageStorage" @click="saveItem">{{ t('common.save') }}</el-button></span>
        </el-tooltip>
      </template>
    </el-dialog>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiDelete, apiGet, apiPost, asArray } from '../api/client'
import { keepPreviousArrayOnLoadFailure } from '../api/resilientLoad'
import ConfirmAction from '../components/ConfirmAction.vue'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import RunRecordTable from '../components/RunRecordTable.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { installLifecycleDisplayStatus, moduleRuntimeGroupStatus, runtimeHealthDisplayStatus } from '../status/semantics'
import {
  applyMinioCleanupPolicy,
  fetchMinioCleanupEstimate,
  formatBytes,
  formatMinioUsedAvailable,
  summarizeMinioInstallDisks,
  type MinioCleanupEstimate,
  type MinioCleanupPolicy,
  type MinioStorageDisk,
  type MinioStorageInsight,
  minioCleanupEstimateFromMetadata,
  minioCleanupPolicyFromMetadata,
  minioStorageDisksFromMetadata,
  minioStorageInsightFromMetadata
} from '../storage/minioInsights'
import { filterMinioInstances, latestSnapshotTime } from '../storage/monitoringStatus'
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
}

type InstanceMetadata = Record<string, any>

type StorageNode = {
  instance: AppInstance
  metadata: InstanceMetadata
  serverLabel: string
  endpoint: string
  status: string
  roleLabel: string
  storageInsight: MinioStorageInsight | null
  storageDisks: MinioStorageDisk[]
  cleanupEstimate: MinioCleanupEstimate | null
  cleanupPolicy: MinioCleanupPolicy | null
}

type StorageGroup = {
  id: string
  title: string
  version: string
  topology: string
  status: string
  nodes: StorageNode[]
  buckets: string[]
  priority: string
  maxWorkers: string
  maxLargeWorkers: string
  replicateDeletes: boolean
}

type ReplicationPair = {
  key: string
  bucket: string
  source?: StorageNode
  target?: StorageNode
}

type StorageKind = 'bucket' | 'object' | 'user' | 'accessKey' | 'replica'

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const realtime = useRealtimeStore()
const taskProgress = useTaskProgressStore()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const selectedInstanceId = ref('')
const tab = ref('instances')
const search = ref('')
const activeStorageGroupKey = ref('')
const storageDetailVisible = ref(false)
const itemDialogVisible = ref(false)
const deleteDialogVisible = ref(false)
const deleteSubmitting = ref(false)
const cleanupRetentionDays = ref(30)
const cleanupRetentionOptions = [30, 60, 180]
const cleanupPolicyBucket = ref('aifar')
const cleanupPolicyPrefix = ref('')
const cleanupEstimating = ref(false)
const cleanupPolicyApplying = ref(false)
const deletePassword = ref('')
const deleteRemoveMountedDisks = ref(false)
const pendingDeleteInstance = ref<AppInstance | null>(null)
const itemKind = ref<StorageKind>('bucket')
const itemForm = reactive({ name: '', policy: '', accessKey: '', secretKey: '' })
const cleanupEstimates = reactive<Record<string, MinioCleanupEstimate>>({})
const collection = reactive<Record<string, any[]>>({
  buckets: [],
  objects: [],
  users: [],
  accessKeys: [],
  replicas: []
})
const canManageStorage = computed(() => can(permissions.storageManage))
const canManageApps = computed(() => can(permissions.appsManage))
const liveInstances = computed(() => instances.value.map((instance) => applyRealtimeStatusToAppInstance(instance, realtime.appInstanceSnapshot(instance.id))))
const minioInstances = computed(() => filterMinioInstances(liveInstances.value))
const lastMonitorAt = computed(() => latestSnapshotTime(
  minioInstances.value.map((instance) => realtime.appInstanceSnapshot(instance.id))
))
const monitoringStatusLabel = computed(() => {
  if (!canManageApps.value) return t('storage.monitorPermissionRequired')
  return t('storage.backendPushReady')
})

const storageGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  const groups = buildStorageGroups(minioInstances.value)
  if (!q) return groups
  return groups.filter((group) => storageGroupSearchText(group).includes(q))
})
const activeStorageGroup = computed(() => storageGroups.value.find((group) => group.id === activeStorageGroupKey.value) ?? null)
const activeStorageWorkbenchGroup = computed(() => {
  return storageGroups.value.find((group) => group.id === activeStorageGroupKey.value) ?? storageGroups.value[0] ?? null
})
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.minio.') || item.type?.startsWith('storage.')))
const settingsItems = computed(() => [
  { label: t('storage.instances'), value: instances.value.length },
  { label: t('storage.settings'), value: t('storage.settingsHint') },
  { label: t('storage.selectInstance'), value: selectedInstanceId.value || '-' }
])
const activeKind = computed<StorageKind>(() => {
  if (tab.value === 'buckets') return 'bucket'
  if (tab.value === 'objects') return 'object'
  if (tab.value === 'replica') return 'replica'
  return 'bucket'
})
const activeCollection = computed(() => collection[collectionKey(activeKind.value)] ?? [])
const dialogTitle = computed(() => {
  const labels: Record<StorageKind, string> = {
    bucket: t('storage.addBucket'),
    object: t('storage.addObject'),
    user: t('storage.addUser'),
    accessKey: t('storage.addAccessKey'),
    replica: t('storage.addReplica')
  }
  return labels[itemKind.value]
})
const deletePromptMessage = computed(() => {
  const row = pendingDeleteInstance.value
  return row ? t('apps.deleteServicePasswordPrompt', { server: serverName(row.serverId) }) : ''
})
const deleteInstanceUsesMountedDisks = computed(() => {
  const row = pendingDeleteInstance.value
  return row ? String(metadataOf(row).storageMode || '') === 'unmounted-disk' : false
})

async function load() {
  await reloadStorageState()
  if (!selectedInstanceId.value && instances.value.length) {
    selectedInstanceId.value = instances.value[0].id
  }
  await loadActive()
}

async function reloadStorageState() {
  const [nextInstances, nextServers, nextTasks] = await Promise.all([
    keepPreviousArrayOnLoadFailure(apiGet<AppInstance[] | null>('/storage/instances'), instances.value),
    keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/servers'), servers.value),
    keepPreviousArrayOnLoadFailure(apiGet<any[] | null>('/tasks'), tasks.value)
  ])
  instances.value = nextInstances
  servers.value = nextServers
  tasks.value = nextTasks
}

async function loadActive() {
  if (tab.value === 'instances') {
    return
  }
  if (!selectedInstanceId.value) return
  if (tab.value === 'buckets') await loadCollection('bucket')
  if (tab.value === 'objects') await loadCollection('object')
  if (tab.value === 'access') {
    await loadCollection('user')
    await loadCollection('accessKey')
  }
  if (tab.value === 'replica') await loadCollection('replica')
}

async function loadCollection(kind: StorageKind) {
  const path = collectionPath(kind)
  const key = collectionKey(kind)
  const result = await keepPreviousArrayOnLoadFailure(
    apiGet<{ items?: any[] }>(path).then((value) => value?.items ?? []),
    collection[key]
  )
  collection[key] = result
}

function openItemDialog(kind: StorageKind) {
  if (!canManageStorage.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  if (!selectedInstanceId.value) {
    ElMessage.warning(t('storage.noInstanceSelected'))
    return
  }
  itemKind.value = kind
  itemForm.name = ''
  itemForm.policy = ''
  itemForm.accessKey = ''
  itemForm.secretKey = ''
  itemDialogVisible.value = true
}

async function saveItem() {
  if (!canManageStorage.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  await apiPost(collectionPath(itemKind.value), { ...itemForm })
  ElMessage.success(t('storage.itemSaved'))
  itemDialogVisible.value = false
  await loadActive()
}

async function deleteItem(kind: StorageKind, id: string) {
  if (!canManageStorage.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  await apiDelete(`${collectionPath(kind)}/${encodeURIComponent(id)}`)
  ElMessage.success(t('storage.itemDeleted'))
  await loadActive()
}

function openDeleteInstance(row: AppInstance) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  pendingDeleteInstance.value = row
  deletePassword.value = ''
  deleteRemoveMountedDisks.value = false
  deleteDialogVisible.value = true
}

function resetDeleteDialog() {
  deletePassword.value = ''
  deleteRemoveMountedDisks.value = false
  pendingDeleteInstance.value = null
}

async function confirmDeleteInstance() {
  const row = pendingDeleteInstance.value
  if (!row) {
    return
  }
  if (!deletePassword.value.trim()) {
    ElMessage.warning(t('apps.deleteServicePasswordPlaceholder'))
    return
  }
  deleteSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${row.id}/delete`, {
      serverPassword: deletePassword.value,
      removeMountedDisks: deleteInstanceUsesMountedDisks.value && deleteRemoveMountedDisks.value
    })
    deleteDialogVisible.value = false
    ElMessage.success(t('apps.uninstallServiceAccepted'))
    taskProgress.track(result.taskId, t('apps.uninstallService'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    deleteSubmitting.value = false
  }
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

function collectionPath(kind: StorageKind) {
  const id = encodeURIComponent(selectedInstanceId.value)
  const paths: Record<StorageKind, string> = {
    bucket: `/storage/${id}/buckets`,
    object: `/storage/${id}/objects`,
    user: `/storage/${id}/users`,
    accessKey: `/storage/${id}/access-keys`,
    replica: `/storage/${id}/replicas`
  }
  return paths[kind]
}

function collectionKey(kind: StorageKind) {
  const keys: Record<StorageKind, string> = {
    bucket: 'buckets',
    object: 'objects',
    user: 'users',
    accessKey: 'accessKeys',
    replica: 'replicas'
  }
  return keys[kind]
}

function metadataOf(item: AppInstance) {
  try {
    return JSON.parse(item.metadata || '{}') as InstanceMetadata
  } catch {
    return {}
  }
}

function buildStorageGroups(rows: AppInstance[]): StorageGroup[] {
  const groups = new Map<string, { items: AppInstance[]; metas: Map<string, InstanceMetadata> }>()
  for (const item of rows) {
    const metadata = metadataOf(item)
    const replicationGroupId = stringValue(metadata.replicationGroupId)
    const key = replicationGroupId ? `replication:${replicationGroupId}` : `instance:${item.id}`
    const group = groups.get(key) ?? { items: [], metas: new Map<string, InstanceMetadata>() }
    group.items.push(item)
    group.metas.set(item.id, metadata)
    groups.set(key, group)
  }
  return Array.from(groups.entries()).map(([id, group]) => createStorageGroup(id, group.items, group.metas))
}

function createStorageGroup(id: string, items: AppInstance[], metas: Map<string, InstanceMetadata>): StorageGroup {
  const sortedItems = [...items].sort((a, b) => serverName(a.serverId).localeCompare(serverName(b.serverId)))
  const first = sortedItems[0]
  const firstMetadata = first ? metas.get(first.id) ?? metadataOf(first) : {}
  const topology = stringValue(firstMetadata.topology) || first?.topology || '-'
  const buckets = uniqueValues(sortedItems.flatMap((item) => bucketsFromMetadata(metas.get(item.id) ?? metadataOf(item))))
  const nodes = sortedItems.map((item, index) => createStorageNode(item, metas.get(item.id) ?? metadataOf(item), topology, index))
  const versions = uniqueValues(sortedItems.map((item) => item.version).filter(Boolean))
  return {
    id,
    title: storageGroupTitle(id, topology, first),
    version: versions.join(', '),
    topology,
    status: storageGroupStatus(nodes),
    nodes,
    buckets,
    priority: stringValue(firstMetadata.replicationPriority),
    maxWorkers: stringValue(firstMetadata.replicationMaxWorkers),
    maxLargeWorkers: stringValue(firstMetadata.replicationMaxLargeWorkers),
    replicateDeletes: truthyValue(firstMetadata.replicateDeletes)
  }
}

function createStorageNode(item: AppInstance, metadata: InstanceMetadata, topology: string, index: number): StorageNode {
  return {
    instance: item,
    metadata,
    serverLabel: serverName(item.serverId),
    endpoint: stringValue(metadata.endpoint) || endpointFromServer(item, metadata),
    status: displayInstanceStatus(item),
    roleLabel: storageNodeRole(topology, index),
    storageInsight: minioStorageInsightFromMetadata(metadata),
    storageDisks: minioStorageDisksFromMetadata(metadata),
    cleanupEstimate: minioCleanupEstimateFromMetadata(metadata),
    cleanupPolicy: minioCleanupPolicyFromMetadata(metadata)
  }
}

function storageGroupTitle(id: string, topology: string, first?: AppInstance) {
  if (isBucketReplicationTopology(topology)) {
    const suffix = id.startsWith('replication:') ? id.replace('replication:', '').slice(-6) : first?.id.slice(-6)
    return suffix ? `minio-bucket-replication-${suffix}` : 'minio-bucket-replication'
  }
  if (topology === 'distributed') {
    return 'minio-distributed'
  }
  return first ? `minio-${serverName(first.serverId)}` : 'minio'
}

function storageNodeRole(topology: string, index: number) {
  if (isBucketReplicationTopology(topology)) {
    if (index === 0) return t('storage.siteA')
    if (index === 1) return t('storage.siteB')
    return `${t('storage.site')} ${index + 1}`
  }
  return `${t('storage.minioNode')} ${index + 1}`
}

function endpointFromServer(item: AppInstance, metadata: InstanceMetadata) {
  const server = servers.value.find((candidate) => candidate.id === item.serverId)
  const port = stringValue(metadata.apiPort) || '9000'
  return server?.host ? `http://${server.host}:${port}` : '-'
}

function bucketsFromMetadata(metadata: InstanceMetadata) {
  return [
    ...stringList(metadata.replicationBuckets),
    ...stringList(metadata.buckets),
    stringValue(metadata.bucket)
  ].filter(Boolean)
}

function storageGroupStatus(nodes: StorageNode[]) {
  const status = moduleRuntimeGroupStatus(nodes.map((node) => node.status))
  return status === 'running' ? 'available' : status
}

function isBucketReplication(group: StorageGroup) {
  return isBucketReplicationTopology(group.topology)
}

function isBucketReplicationTopology(topology: string) {
  return topology === 'bucket-replication'
}

function replicationPairs(group: StorageGroup): ReplicationPair[] {
  const buckets = group.buckets.length ? group.buckets : ['-']
  return buckets.map((bucket) => ({
    key: `${group.id}:${bucket}`,
    bucket,
    source: group.nodes[0],
    target: group.nodes[1]
  }))
}

function displayBuckets(group: StorageGroup) {
  return group.buckets.length ? group.buckets.join(', ') : '-'
}

function replicationProfileText(group: StorageGroup) {
  const priority = group.priority || '-'
  const workers = group.maxWorkers || '-'
  const largeWorkers = group.maxLargeWorkers || '-'
  const deleteMode = group.replicateDeletes ? t('storage.deleteSyncOn') : t('storage.deleteSyncOff')
  return `${t('storage.replicationPriority')} ${priority} | ${t('storage.replicationWorkers')} ${workers}/${largeWorkers} | ${deleteMode}`
}

async function estimateVisibleStorageCleanup() {
  const nodes = uniqueStorageNodes(storageGroups.value.flatMap((group) => group.nodes))
  if (!nodes.length) {
    return
  }
  cleanupEstimating.value = true
  const failures: string[] = []
  try {
    await Promise.all(nodes.map(async (node) => {
      try {
        cleanupEstimates[node.instance.id] = await fetchMinioCleanupEstimate(node.instance.id, cleanupRetentionDays.value)
      } catch (err) {
        failures.push(node.serverLabel)
        cleanupEstimates[node.instance.id] = {
          status: 'unavailable',
          retentionDays: cleanupRetentionDays.value,
          objectCount: 0,
          bytes: 0,
          source: 'api'
        }
      }
    }))
    if (failures.length) {
      ElMessage.warning(`${t('storage.cleanupEstimateFailed')}: ${failures.join(', ')}`)
    } else {
      ElMessage.success(t('storage.cleanupEstimateUpdated'))
    }
  } finally {
    cleanupEstimating.value = false
  }
}

async function applyVisibleStorageCleanupPolicy() {
  await submitVisibleStorageCleanupPolicy(true)
}

async function disableVisibleStorageCleanupPolicy() {
  await submitVisibleStorageCleanupPolicy(false)
}

async function submitVisibleStorageCleanupPolicy(enabled: boolean) {
  if (!canManageStorage.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const bucket = cleanupPolicyBucket.value.trim() || 'aifar'
  const prefix = cleanupPolicyPrefix.value.trim().replace(/^\/+/, '')
  const nodes = uniqueStorageNodes(storageGroups.value.flatMap((group) => group.nodes))
  if (!nodes.length) {
    return
  }
  cleanupPolicyApplying.value = true
  const failures: string[] = []
  try {
    await Promise.all(nodes.map(async (node) => {
      try {
        const result = await applyMinioCleanupPolicy(node.instance.id, {
          enabled,
          bucket,
          prefix,
          retentionDays: cleanupRetentionDays.value
        })
        taskProgress.track(result.taskId, enabled ? t('storage.cleanupPolicyApplyTask') : t('storage.cleanupPolicyDisableTask'))
      } catch (err) {
        failures.push(node.serverLabel)
      }
    }))
    if (failures.length) {
      ElMessage.warning(`${t('storage.cleanupPolicyFailed')}: ${failures.join(', ')}`)
    } else {
      ElMessage.success(enabled ? t('storage.cleanupPolicyApplied') : t('storage.cleanupPolicyDisableSubmitted'))
    }
    await reloadStorageState()
  } finally {
    cleanupPolicyApplying.value = false
  }
}

function groupCapacityText(group: StorageGroup) {
  const summary = groupStorageSummary(group)
  if (!summary) {
    return '-'
  }
  const disks = `${summary.pathCount} ${t('storage.dataDisks')}`
  if (summary.nodeCount === 1) {
    return `${disks} / ${formatBytes(summary.aggregateTotalBytes)}`
  }
  return `${summary.nodeCount} ${t('storage.minioNodes')} / ${disks} / ${t('storage.physicalCapacity')} ${formatBytes(summary.aggregateTotalBytes)}`
}

function groupUsedAvailableText(group: StorageGroup) {
  const summary = groupStorageSummary(group)
  if (!summary) {
    return '-'
  }
  return `${formatBytes(summary.aggregateUsedBytes)} / ${formatBytes(summary.aggregateAvailableBytes)} (${usagePercent(summary.aggregateUsedBytes, summary.aggregateTotalBytes)}%)`
}

function groupCleanupEstimateText(group: StorageGroup) {
  const estimates = group.nodes.map(cleanupEstimateForNode).filter((item): item is MinioCleanupEstimate => Boolean(item))
  const available = estimates.filter((item) => item.status === 'available')
  if (!estimates.length || !available.length) {
    return t('storage.cleanupNotEstimated')
  }
  if (available.length === 1) {
    return cleanupEstimateDisplay(available[0])
  }
  const first = available[0]
  const uniform = available.every((item) => item.bytes === first.bytes && item.objectCount === first.objectCount)
  if (uniform) {
    return `${t('storage.perNode')} ${cleanupEstimateDisplay(first)} x ${available.length}`
  }
  return t('storage.seeNodeDetails')
}

function groupCleanupPolicyText(group: StorageGroup) {
  const policies = group.nodes.map((node) => node.cleanupPolicy).filter((item): item is MinioCleanupPolicy => Boolean(item))
  const enabled = policies.filter((item) => item.enabled && item.status === 'enabled')
  if (!enabled.length) {
    return t('storage.cleanupPolicyDisabled')
  }
  const first = enabled[0]
  const uniform = enabled.every((item) =>
    item.bucket === first.bucket &&
    item.prefix === first.prefix &&
    item.retentionDays === first.retentionDays
  )
  if (!uniform) {
    return t('storage.seeNodeDetails')
  }
  const suffix = group.nodes.length > 1 ? ` x ${enabled.length}` : ''
  return `${cleanupPolicyDisplay(first)}${suffix}`
}

function nodeInsightText(node: StorageNode) {
  const insight = node.storageInsight
  const estimate = cleanupEstimateForNode(node)
  const capacity = insight ? `${formatMinioUsedAvailable(insight)} | ${t('storage.assignedCapacity')}: ${formatBytes(insight.totalBytes)}` : '-'
  const cleanup = estimate ? cleanupEstimateDisplay(estimate) : t('storage.cleanupNotEstimated')
  const policy = node.cleanupPolicy ? cleanupPolicyDisplay(node.cleanupPolicy) : t('storage.cleanupPolicyDisabled')
  return `${t('storage.storageUsedAvailable')}: ${capacity} | ${t('storage.cleanupEstimate')}: ${cleanup} | ${t('storage.cleanupPolicy')}: ${policy}`
}

function cleanupPolicyDisplay(policy: MinioCleanupPolicy) {
  if (!policy.enabled || policy.status !== 'enabled') {
    return t('storage.cleanupPolicyDisabled')
  }
  const scope = policy.prefix ? `${policy.bucket}/${policy.prefix}` : policy.bucket
  return `${scope} / ${policy.retentionDays} ${t('storage.daysUnit')}`
}

function cleanupEstimateDisplay(estimate: MinioCleanupEstimate) {
  if (estimate.status && estimate.status !== 'available') {
    return t('storage.cleanupUnavailable')
  }
  return `${formatBytes(estimate.bytes)} / ${estimate.objectCount} ${t('storage.objects')}`
}

function cleanupEstimateForNode(node: StorageNode) {
  const fresh = cleanupEstimates[node.instance.id]
  if (fresh && fresh.retentionDays === cleanupRetentionDays.value) {
    return fresh
  }
  if (node.cleanupEstimate && node.cleanupEstimate.retentionDays === cleanupRetentionDays.value) {
    return node.cleanupEstimate
  }
  return null
}

function diskLabel(disk: MinioStorageDisk) {
  return `${t('storage.disk')} ${disk.index}`
}

function diskUsageText(disk: MinioStorageDisk) {
  return `${formatBytes(disk.usedBytes)} / ${formatBytes(disk.availableBytes)} (${disk.usagePercent}%)`
}

function groupStorageSummary(group: StorageGroup) {
  return summarizeMinioInstallDisks(group.nodes.map((node) => node.storageInsight))
}

function usagePercent(usedBytes: number, totalBytes: number) {
  return totalBytes > 0 ? Math.round((usedBytes * 100) / totalBytes) : 0
}

function uniqueStorageNodes(nodes: StorageNode[]) {
  const seen = new Set<string>()
  const out: StorageNode[] = []
  for (const node of nodes) {
    if (seen.has(node.instance.id)) {
      continue
    }
    seen.add(node.instance.id)
    out.push(node)
  }
  return out
}

function syncEndpointLabel(node: StorageNode | undefined, bucket: string) {
  if (!node) return '-'
  return bucket && bucket !== '-' ? `${node.endpoint}/${bucket}` : node.endpoint
}

function storageGroupSearchText(group: StorageGroup) {
  return [
    group.title,
    group.version,
    group.topology,
    ...group.buckets,
    ...group.nodes.flatMap((node) => [node.serverLabel, node.endpoint, node.roleLabel, node.status])
  ].join(' ').toLowerCase()
}

function selectStorageResource(group: StorageGroup) {
  activeStorageGroupKey.value = group.id
}

function openStorageResource(group: StorageGroup) {
  selectStorageResource(group)
  storageDetailVisible.value = true
}

function uniqueValues(values: string[]) {
  return Array.from(new Set(values.map((value) => value.trim()).filter(Boolean)))
}

function stringValue(value: unknown) {
  return value === null || value === undefined ? '' : String(value).trim()
}

function stringList(value: unknown) {
  if (Array.isArray(value)) {
    return value.map(stringValue).filter(Boolean)
  }
  const raw = stringValue(value)
  if (!raw) return []
  if (raw.startsWith('[')) {
    try {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed)) {
        return parsed.map(stringValue).filter(Boolean)
      }
    } catch {
      return []
    }
  }
  return raw.split(',').map((item) => item.trim()).filter(Boolean)
}

function truthyValue(value: unknown) {
  if (typeof value === 'boolean') return value
  return ['true', '1', 'yes', 'on'].includes(stringValue(value).toLowerCase())
}

function displayInstanceStatus(item: AppInstance) {
  const metadata = metadataOf(item)
  if (isInstallFailedInstance(item, metadata)) {
    return 'failed'
  }
  const lastCheck = metadata.lastCheck as Record<string, any> | undefined
  const checkedStatus = String(lastCheck?.status || '').trim()
  if (checkedStatus) {
    return displayHealthStatus(checkedStatus)
  }
  if (item.status === 'installed') {
    return 'checking'
  }
  return displayHealthStatus(item.status)
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function isInstallFailedGroup(group: StorageGroup) {
  return group.nodes.some((node) => isInstallFailedInstance(node.instance, node.metadata))
}

function isInstallFailedInstance(item: AppInstance, metadata: InstanceMetadata) {
  return installLifecycleDisplayStatus({ status: item.status, metadata }) === 'failed'
}

function displayHealthStatus(status: unknown) {
  return runtimeHealthDisplayStatus(status)
}

function instanceLabel(item: AppInstance) {
  return `${item.app}-${item.id.slice(-6)} / ${serverName(item.serverId)}`
}

watch([tab, selectedInstanceId], () => {
  void loadActive()
})

watch(itemDialogVisible, (visible) => {
  if (!visible) {
    itemForm.secretKey = ''
  }
})
onMounted(load)
</script>

<style scoped>
.status-line {
  display: flex;
  align-items: center;
  gap: 8px;
}

.storage-main {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.storage-empty {
  min-height: clamp(180px, 28vh, 260px);
}

.storage-resource-shell {
  display: grid;
  grid-template-columns: minmax(0, 1.35fr) minmax(340px, .9fr);
  gap: 14px;
  min-height: 0;
  padding: 12px;
}

.storage-resource-list {
  display: grid;
  gap: 10px;
  padding: 0;
  min-height: 0;
  overflow: auto;
}

.storage-resource-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 14px 16px;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  cursor: pointer;
  transition: background .16s ease, border-color .16s ease, box-shadow .16s ease;
}

.storage-resource-row:hover {
  background: #f8fbff;
  border-color: #91caff;
  box-shadow: var(--aifar-shadow-raised);
}

.storage-resource-row.active {
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

.storage-head {
  display: flex;
  gap: 10px;
  align-items: flex-start;
  margin-bottom: 10px;
}

.storage-title-block {
  min-width: 0;
  flex: 1;
}

.storage-head strong,
.storage-head span {
  display: block;
}

.storage-head strong {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-head span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.storage-head-actions {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  flex-wrap: wrap;
  max-width: 240px;
}

.storage-head-actions :deep(.el-tag) {
  min-height: 22px;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 1;
}

.storage-head-actions :deep(.el-tag__content) {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  line-height: 1;
}

.cleanup-retention-control {
  width: 128px;
}

.cleanup-policy-control {
  width: 132px;
}

.cleanup-day-presets {
  flex: 0 0 auto;
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

.storage-summary {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.storage-summary span {
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

.storage-summary strong {
  margin-right: 4px;
  color: var(--aifar-text-tertiary);
  font-weight: 650;
}

.storage-summary-wide {
  flex: 1 1 100%;
}

.detail-head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}

.resource-detail-stack {
  display: flex;
  flex-direction: column;
  gap: 14px;
}

.detail-summary {
  margin-top: 0;
}

.bucket-sync-list,
.storage-node-list {
  display: grid;
  gap: 8px;
  margin-top: 12px;
}

.storage-node-list {
  padding-top: 10px;
  border-top: 1px dashed var(--aifar-border-soft);
}

.section-label {
  color: var(--aifar-text-secondary);
  font-size: 12px;
  font-weight: 700;
}

.sync-row {
  display: grid;
  grid-template-columns: auto minmax(0, 1fr) auto minmax(0, 1fr);
  align-items: center;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #f8fbff;
}

.sync-bucket {
  max-width: 120px;
  padding: 3px 8px;
  border: 1px solid #91caff;
  border-radius: var(--aifar-radius);
  background: #e6f4ff;
  color: var(--aifar-primary);
  font-size: 12px;
  font-weight: 800;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sync-endpoint {
  min-width: 0;
}

.sync-endpoint span,
.sync-endpoint strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.sync-endpoint span {
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.sync-endpoint strong {
  font-size: 12px;
}

.sync-arrow {
  padding: 2px 8px;
  border-radius: 999px;
  background: #f6ffed;
  border: 1px solid #b7eb8f;
  color: #389e0d;
  font-size: 12px;
  font-weight: 800;
  white-space: nowrap;
}

.storage-node-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  align-items: flex-start;
  gap: 8px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  padding: 8px;
  background: #fff;
}

.storage-node-main {
  min-width: 0;
}

.storage-node-main strong,
.storage-node-main span,
.storage-node-main small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-node-main span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.storage-node-main small {
  color: var(--aifar-text-secondary);
  font-size: 11px;
  line-height: 18px;
}

.storage-disk-list {
  margin-top: 8px;
  display: grid;
  gap: 6px;
}

.storage-disk-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) minmax(86px, auto);
  gap: 8px;
  align-items: center;
  border: 1px solid var(--aifar-border-soft);
  border-radius: 6px;
  padding: 6px 8px;
  background: #f8fbff;
}

.storage-disk-row > div {
  min-width: 0;
}

.storage-disk-row strong,
.storage-disk-row span,
.storage-disk-row small {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-disk-row strong {
  font-size: 12px;
}

.storage-disk-row span,
.storage-disk-row small {
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.storage-disk-row small {
  grid-column: 1 / -1;
}

.storage-node-tags {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  white-space: nowrap;
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

.access-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  min-height: 0;
  padding: 12px;
  flex: 1;
}

.sub-panel {
  min-height: 0;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  overflow: hidden;
  display: flex;
  flex-direction: column;
}

.collection-panel,
.settings-grid {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  height: 100%;
  padding: 12px;
}

.secret-confirm-message {
  margin: 0 0 12px;
  color: var(--aifar-text-secondary);
  font-size: 14px;
  line-height: 22px;
}

.delete-disk-option {
  margin-top: 12px;
}

.delete-disk-hint {
  margin: 4px 0 0;
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  line-height: 20px;
}

@media (max-width: 1100px) {
  .access-grid,
  .storage-resource-shell {
    grid-template-columns: 1fr;
  }

  .resource-inline-detail {
    max-height: none;
  }

  .sync-row {
    grid-template-columns: 1fr;
  }

  .sync-arrow {
    width: fit-content;
  }

  .storage-node-row {
    grid-template-columns: 1fr;
  }

  .storage-node-tags {
    justify-content: flex-start;
    flex-wrap: wrap;
  }
}
</style>
