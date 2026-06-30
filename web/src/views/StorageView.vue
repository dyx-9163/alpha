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
          <el-select v-if="tab === 'instances'" v-model="serverId" :placeholder="t('database.targetServer')" class="toolbar-control">
            <el-option v-for="server in servers" :key="server.id" :label="server.name" :value="server.id" />
          </el-select>
          <el-tooltip :content="deniedText" :disabled="canManageStorage" placement="top">
            <span><el-button v-if="tab === 'instances'" type="primary" :disabled="!canManageStorage" @click="installMinio">{{ t('storage.install') }}</el-button></span>
          </el-tooltip>
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
          <el-button @click="loadActive">{{ t('storage.refreshStatus') }}</el-button>
        </div>
      </div>

      <template v-if="tab === 'instances'">
        <el-table v-if="filteredInstances.length" :data="filteredInstances" height="100%">
          <el-table-column prop="app" :label="t('storage.service')" width="120" />
          <el-table-column prop="version" :label="t('common.version')" min-width="180" />
          <el-table-column :label="t('storage.server')" min-width="220"><template #default="{ row }">{{ serverName(row.serverId) }}</template></el-table-column>
          <el-table-column :label="t('common.endpoint')" min-width="220" show-overflow-tooltip><template #default="{ row }">{{ metadataOf(row).endpoint || '-' }}</template></el-table-column>
          <el-table-column prop="topology" :label="t('dashboard.topology')" width="140" />
          <el-table-column prop="status" :label="t('common.status')" width="120"><template #default="{ row }"><StatusTag :status="row.status" /></template></el-table-column>
          <el-table-column :label="t('common.operation')" width="120" fixed="right">
            <template #default="{ row }">
              <el-tooltip :content="deniedText" :disabled="canManageApps" placement="top">
                <span>
                  <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openDeleteInstance(row)">{{ t('common.uninstall') }}</el-button>
                </span>
              </el-tooltip>
            </template>
          </el-table-column>
        </el-table>
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

type StorageKind = 'bucket' | 'object' | 'user' | 'accessKey' | 'replica'

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const serverId = ref('')
const selectedInstanceId = ref('')
const tab = ref('instances')
const search = ref('')
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

const filteredInstances = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return instances.value
  return instances.value.filter((item) => `${item.app} ${item.version} ${item.topology} ${serverName(item.serverId)}`.toLowerCase().includes(q))
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
  instances.value = asArray(await apiGet<AppInstance[] | null>('/storage/instances').catch(() => []))
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  tasks.value = asArray(await apiGet<any[] | null>('/tasks').catch(() => []))
  if (!selectedInstanceId.value && instances.value.length) {
    selectedInstanceId.value = instances.value[0].id
  }
  await loadActive()
}

async function loadActive() {
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

async function installMinio() {
  if (!canManageStorage.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const result = await apiPost<{ taskId: string }>('/storage/instances', { serverId: serverId.value, version: 'latest' })
  void router.push({ path: '/tasks', query: { taskId: result.taskId } })
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
    return JSON.parse(item.metadata || '{}') as Record<string, any>
  } catch {
    return {}
  }
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

function instanceLabel(item: AppInstance) {
  return `${item.app}-${item.id.slice(-6)} / ${serverName(item.serverId)}`
}

watch([tab, selectedInstanceId], loadActive)
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
}
</style>
