<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('storage.title') }}</h1>
        <p class="page-subtitle">{{ t('storage.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <span class="status-pill success">{{ t('common.connected') }}</span>
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
      </div>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('storage.instances')" name="instances" />
      <el-tab-pane :label="t('storage.buckets')" name="buckets" />
      <el-tab-pane :label="t('storage.objects')" name="objects" />
      <el-tab-pane :label="t('storage.access')" name="access" />
      <el-tab-pane :label="t('storage.replica')" name="replica" />
      <el-tab-pane :label="t('storage.runs')" name="runs" />
      <el-tab-pane :label="t('storage.settings')" name="settings" />
    </el-tabs>

    <div class="workspace-card storage-main">
      <div class="table-toolbar">
        <el-input v-model="search" :placeholder="t('storage.search')" clearable class="toolbar-control" />
        <div class="head-actions">
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
          <el-button :loading="checkingInstances" @click="loadActive(true)">{{ t('storage.refreshStatus') }}</el-button>
        </div>
      </div>

      <template v-if="tab === 'instances'">
        <div v-if="storageGroups.length" class="storage-card-grid">
          <article v-for="group in storageGroups" :key="group.id" class="storage-card">
            <div class="storage-head">
              <div class="app-icon small">S3</div>
              <div class="storage-title-block">
                <strong>{{ group.title }}</strong>
                <span>{{ group.topology }} / {{ t('storage.minioNodes') }} {{ group.nodes.length }}</span>
              </div>
              <div class="storage-head-actions">
                <StatusTag :status="group.status" />
              </div>
            </div>

            <div class="storage-info-grid">
              <div>
                <span>{{ t('storage.service') }}</span>
                <strong>minio</strong>
              </div>
              <div>
                <span>{{ t('common.version') }}</span>
                <strong>{{ group.version || '-' }}</strong>
              </div>
              <div>
                <span>{{ t('dashboard.topology') }}</span>
                <strong>{{ group.topology || '-' }}</strong>
              </div>
              <div>
                <span>{{ t('storage.minioNodes') }}</span>
                <strong>{{ group.nodes.length }}</strong>
              </div>
              <div class="storage-info-wide">
                <span>{{ t('storage.replicationBuckets') }}</span>
                <strong>{{ displayBuckets(group) }}</strong>
              </div>
              <div v-if="isBucketReplication(group)" class="storage-info-wide">
                <span>{{ t('storage.replicationProfile') }}</span>
                <strong>{{ replicationProfileText(group) }}</strong>
              </div>
            </div>

            <div v-if="isBucketReplication(group)" class="bucket-sync-list">
              <div class="section-label">{{ t('storage.bucketSync') }}</div>
              <div v-for="pair in replicationPairs(group)" :key="pair.key" class="sync-row">
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
              <div v-for="node in group.nodes" :key="node.instance.id" class="storage-node-row">
                <div class="storage-node-main">
                  <strong>{{ node.serverLabel }}</strong>
                  <span>{{ node.endpoint }}</span>
                </div>
                <div class="storage-node-tags">
                  <el-tag size="small" type="info">{{ node.roleLabel }}</el-tag>
                  <StatusTag :status="node.status" />
                  <el-tooltip :content="deniedText" :disabled="canManageApps" placement="top">
                    <span>
                      <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteInstance(node.instance)">{{ t('common.uninstall') }}</el-button>
                    </span>
                  </el-tooltip>
                </div>
              </div>
            </div>
          </article>
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

    <el-dialog v-model="deleteDialogVisible" :title="t('apps.uninstallService')" width="460px" destroy-on-close @closed="resetDeleteDialog">
      <p class="secret-confirm-message">{{ deletePromptMessage }}</p>
      <el-input v-model="deletePassword" type="password" :placeholder="t('apps.deleteServicePasswordPlaceholder')" show-password autofocus @keyup.enter="confirmDeleteInstance" />
      <el-checkbox v-if="deleteInstanceUsesMountedDisks" v-model="deleteRemoveMountedDisks" class="delete-disk-option">
        {{ t('storage.removeMountedDisks') }}
      </el-checkbox>
      <p v-if="deleteInstanceUsesMountedDisks" class="delete-disk-hint">{{ t('storage.removeMountedDisksHint') }}</p>
      <template #footer>
        <el-button :disabled="deleteSubmitting" @click="deleteDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="danger" :loading="deleteSubmitting" @click="confirmDeleteInstance">{{ t('common.uninstall') }}</el-button>
      </template>
    </el-dialog>

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
import ConfirmAction from '../components/ConfirmAction.vue'
import KeyValueGrid from '../components/KeyValueGrid.vue'
import RunRecordTable from '../components/RunRecordTable.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

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
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const selectedInstanceId = ref('')
const tab = ref('instances')
const search = ref('')
const checkingInstances = ref(false)
const checkingInstanceIds = ref<Set<string>>(new Set())
const itemDialogVisible = ref(false)
const deleteDialogVisible = ref(false)
const deleteSubmitting = ref(false)
const deletePassword = ref('')
const deleteRemoveMountedDisks = ref(false)
const pendingDeleteInstance = ref<AppInstance | null>(null)
const itemKind = ref<StorageKind>('bucket')
const itemForm = reactive({ name: '', policy: '', accessKey: '', secretKey: '' })
const collection = reactive<Record<string, any[]>>({
  buckets: [],
  objects: [],
  users: [],
  accessKeys: [],
  replicas: []
})
const canManageStorage = computed(() => can(permissions.storageManage))
const canManageApps = computed(() => can(permissions.appsManage))

const storageGroups = computed(() => {
  const q = search.value.trim().toLowerCase()
  const groups = buildStorageGroups(instances.value.filter((item) => item.app === 'minio'))
  if (!q) return groups
  return groups.filter((group) => storageGroupSearchText(group).includes(q))
})
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.minio.') || item.type?.startsWith('storage.')))
const settingsItems = computed(() => [
  { label: t('storage.instances'), value: instances.value.length },
  { label: t('common.provider'), value: t('common.real') },
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
    apiGet<AppInstance[] | null>('/storage/instances').catch(() => []),
    apiGet<any[] | null>('/servers').catch(() => []),
    apiGet<any[] | null>('/tasks').catch(() => [])
  ])
  instances.value = asArray(nextInstances)
  servers.value = asArray(nextServers)
  tasks.value = asArray(nextTasks)
}

async function loadActive(manual = false) {
  if (tab.value === 'instances') {
    await refreshMinioStatus(manual)
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
  const result = await apiGet<{ items?: any[] }>(path).catch(() => ({ items: [] }))
  collection[collectionKey(kind)] = asArray(result.items)
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
    void router.push({ path: '/tasks', query: { taskId: result.taskId } })
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
    roleLabel: storageNodeRole(topology, index)
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
  const statuses = nodes.map((node) => node.status)
  if (!statuses.length) return 'unknown'
  if (statuses.some((status) => ['checking', 'probing', 'pending'].includes(status))) return 'checking'
  if (statuses.every(isHealthyStatus)) return 'available'
  if (statuses.some(isHealthyStatus)) return 'degraded'
  if (statuses.some((status) => ['unavailable', 'failed', 'error', 'missing', 'stopped'].includes(status))) return 'unavailable'
  return statuses[0] || 'unknown'
}

function isHealthyStatus(status: string) {
  return ['ok', 'success', 'installed', 'running', 'available'].includes(status)
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
  if (checkingInstanceIds.value.has(item.id)) {
    return 'checking'
  }
  const lastCheck = metadataOf(item).lastCheck as Record<string, any> | undefined
  const checkedStatus = String(lastCheck?.status || '').trim()
  if (checkedStatus) {
    return checkedStatus
  }
  if (item.status === 'installed') {
    return 'checking'
  }
  return item.status
}

async function refreshMinioStatus(manual = false) {
  if (!canManageApps.value) {
    if (manual) {
      ElMessage.warning(deniedText.value)
    }
    return
  }
  if (checkingInstances.value) {
    return
  }
  const rows = instances.value.filter((item) => item.app === 'minio')
  if (!rows.length) {
    return
  }
  checkingInstances.value = true
  checkingInstanceIds.value = new Set(rows.map((item) => item.id))
  try {
    const taskIds: string[] = []
    for (const item of rows) {
      try {
        const result = await apiPost<{ taskId: string }>(`/apps/instances/${item.id}/check`)
        if (result.taskId) {
          taskIds.push(result.taskId)
        }
      } catch (err) {
        if (manual) {
          ElMessage.warning(err instanceof Error ? err.message : t('apps.checkServiceFailed'))
        }
      }
    }
    if (taskIds.length) {
      await waitForTasks(taskIds)
    }
    await reloadStorageState()
  } finally {
    checkingInstances.value = false
    checkingInstanceIds.value = new Set()
  }
}

async function waitForTasks(taskIds: string[]) {
  const pending = new Set(taskIds)
  const deadline = Date.now() + 30000
  while (pending.size && Date.now() < deadline) {
    await delay(500)
    const latest = asArray<any>(await apiGet<any[] | null>('/tasks').catch(() => []))
    tasks.value = latest
    for (const task of latest) {
      if (pending.has(task.id) && !['pending', 'running'].includes(task.status)) {
        pending.delete(task.id)
      }
    }
  }
}

function delay(ms: number) {
  return new Promise((resolve) => setTimeout(resolve, ms))
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function instanceLabel(item: AppInstance) {
  return `${item.app}-${item.id.slice(-6)} / ${serverName(item.serverId)}`
}

watch([tab, selectedInstanceId], () => {
  void loadActive()
})
onMounted(load)
</script>

<style scoped>
.storage-main {
  display: flex;
  flex-direction: column;
  min-height: 0;
}

.storage-empty {
  min-height: clamp(180px, 28vh, 260px);
}

.storage-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr));
  gap: 12px;
  padding: 12px;
  min-height: 0;
  overflow: auto;
}

.storage-card {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 12px;
  box-shadow: 0 1px 2px rgba(15, 35, 68, .03);
  transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease;
}

.storage-card:hover {
  border-color: #91caff;
  box-shadow: var(--aifar-shadow-raised);
  transform: translateY(-1px);
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

.storage-info-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 8px;
}

.storage-info-grid div {
  min-height: 54px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius);
  background: #f7fbff;
  padding: 8px;
  min-width: 0;
}

.storage-info-grid .storage-info-wide {
  grid-column: 1 / -1;
}

.storage-info-grid span {
  display: block;
  color: var(--aifar-text-tertiary);
  font-size: 11px;
}

.storage-info-grid strong {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-info-wide strong {
  overflow: visible;
  text-overflow: clip;
  white-space: normal;
  word-break: break-word;
  line-height: 1.35;
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
  align-items: center;
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
.storage-node-main span {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.storage-node-main span {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
}

.storage-node-tags {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 6px;
  white-space: nowrap;
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
  .access-grid {
    grid-template-columns: 1fr;
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
