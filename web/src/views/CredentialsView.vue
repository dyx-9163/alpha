<template>
  <PageShell :title="t('credentials.title')" :subtitle="t('credentials.subtitle')">
    <template #actions>
      <el-button @click="load">{{ t('common.refresh') }}</el-button>
      <el-tooltip :content="deniedText" :disabled="canManageCredentials" placement="top">
        <span>
          <el-button type="primary" :disabled="!canManageCredentials" @click="openCreate">
            {{ t('credentials.add') }}
          </el-button>
        </span>
      </el-tooltip>
    </template>

    <div class="workspace-card credentials-card">
      <div class="table-toolbar">
        <el-input
          v-model="search"
          :placeholder="t('credentials.search')"
          clearable
          class="toolbar-control"
          @clear="load"
          @keyup.enter="load"
        />
        <div class="head-actions">
          <el-select v-model="kindFilter" :placeholder="t('credentials.kind')" clearable class="toolbar-control is-sm" @change="load">
            <el-option v-for="kind in kindOptions" :key="kind" :label="kindLabel(kind)" :value="kind" />
          </el-select>
          <el-select v-model="statusFilter" :placeholder="t('common.status')" clearable class="toolbar-control is-sm" @change="load">
            <el-option :label="t('credentials.statusActive')" value="active" />
            <el-option :label="t('credentials.statusRetired')" value="retired" />
            <el-option :label="t('credentials.statusInvalid')" value="invalid" />
          </el-select>
        </div>
      </div>

      <el-table v-loading="loading" :data="items" height="100%">
        <el-table-column prop="name" :label="t('credentials.name')" min-width="170" show-overflow-tooltip />
        <el-table-column :label="t('credentials.kind')" width="110">
          <template #default="{ row }">
            <el-tag size="small" effect="light">{{ kindLabel(row.kind) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="username" :label="t('credentials.username')" min-width="170" show-overflow-tooltip />
        <el-table-column prop="endpoint" :label="t('credentials.endpoint')" min-width="190" show-overflow-tooltip />
        <el-table-column :label="t('common.status')" width="105">
          <template #default="{ row }">
            <el-tag :type="statusTagType(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column :label="t('credentials.secret')" width="120">
          <template #default="{ row }">
            <span :class="row.hasSecret ? 'secret-ok' : 'secret-missing'">
              {{ row.hasSecret ? t('credentials.secretConfigured') : t('credentials.secretMissing') }}
            </span>
          </template>
        </el-table-column>
        <el-table-column prop="scope" :label="t('credentials.scope')" width="120" />
        <el-table-column prop="purpose" :label="t('credentials.purpose')" min-width="120" show-overflow-tooltip />
        <el-table-column prop="currentVersion" :label="t('credentials.version')" width="90" />
        <el-table-column prop="updatedAt" :label="t('credentials.updatedAt')" min-width="180" show-overflow-tooltip />
        <el-table-column :label="t('common.operation')" width="150" fixed="right">
          <template #default="{ row }">
            <el-tooltip :content="deniedText" :disabled="canManageCredentials" placement="top">
              <span>
                <el-button size="small" :disabled="!canManageCredentials" @click="openEdit(row)">
                  {{ t('credentials.edit') }}
                </el-button>
              </span>
            </el-tooltip>
            <ConfirmAction
              :message="t('credentials.confirmDelete', { name: row.name })"
              :title="t('common.delete')"
              :confirm-text="t('common.delete')"
              :cancel-text="t('common.cancel')"
              :disabled="!canManageCredentials"
              @confirm="deleteCredential(row)"
            >
              <template #default="{ confirm }">
                <el-tooltip :content="deniedText" :disabled="canManageCredentials" placement="top">
                  <span>
                    <el-button size="small" type="danger" plain :disabled="!canManageCredentials" @click="confirm">
                      {{ t('common.delete') }}
                    </el-button>
                  </span>
                </el-tooltip>
              </template>
            </ConfirmAction>
          </template>
        </el-table-column>
      </el-table>

      <div v-if="!loading && !items.length" class="empty-state credentials-empty">
        <div>
          <strong>{{ t('credentials.emptyTitle') }}</strong>
          <span>{{ t('credentials.emptyDesc') }}</span>
        </div>
      </div>
    </div>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="min(760px, calc(100vw - 32px))" destroy-on-close>
      <el-form label-position="top" class="credential-form">
        <div class="credential-form-grid">
          <el-form-item :label="t('credentials.name')" required>
            <el-input v-model="form.name" />
          </el-form-item>
          <el-form-item :label="t('credentials.kind')" required>
            <el-select v-model="form.kind" style="width: 100%">
              <el-option v-for="kind in kindOptions" :key="kind" :label="kindLabel(kind)" :value="kind" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('credentials.username')">
            <el-input v-model="form.username" />
          </el-form-item>
          <el-form-item :label="t('credentials.secret')" :required="!form.id">
            <el-input v-model="form.secret" type="password" show-password :placeholder="t('credentials.secretPlaceholder')" />
          </el-form-item>
          <el-form-item :label="t('credentials.endpoint')">
            <el-input v-model="form.endpoint" />
          </el-form-item>
          <el-form-item :label="t('credentials.scope')">
            <el-select v-model="form.scope" style="width: 100%">
              <el-option label="global" value="global" />
              <el-option label="server" value="server" />
              <el-option label="app" value="app" />
              <el-option label="app-instance" value="app-instance" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('common.status')">
            <el-select v-model="form.status" style="width: 100%">
              <el-option :label="t('credentials.statusActive')" value="active" />
              <el-option :label="t('credentials.statusRetired')" value="retired" />
              <el-option :label="t('credentials.statusInvalid')" value="invalid" />
            </el-select>
          </el-form-item>
          <el-form-item :label="t('credentials.purpose')">
            <el-input v-model="form.purpose" />
          </el-form-item>
          <el-form-item :label="t('credentials.app')">
            <el-input v-model="form.app" />
          </el-form-item>
          <el-form-item :label="t('credentials.serverId')">
            <el-input v-model="form.serverId" />
          </el-form-item>
          <el-form-item :label="t('credentials.appInstanceId')" class="form-wide">
            <el-input v-model="form.appInstanceId" />
          </el-form-item>
          <el-form-item :label="t('credentials.tags')" class="form-wide">
            <el-input v-model="form.tags" />
          </el-form-item>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-tooltip :content="deniedText" :disabled="canManageCredentials" placement="top">
          <span>
            <el-button type="primary" :loading="saving" :disabled="!dialogCanSave" @click="saveCredential">
              {{ t('common.save') }}
            </el-button>
          </span>
        </el-tooltip>
      </template>
    </el-dialog>
  </PageShell>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiDelete, apiGet, apiPost, apiPut, asArray } from '../api/client'
import ConfirmAction from '../components/ConfirmAction.vue'
import PageShell from '../components/PageShell.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

type Credential = {
  id: string
  name: string
  kind: string
  username?: string
  endpoint?: string
  scope: string
  status: string
  app?: string
  serverId?: string
  appInstanceId?: string
  purpose?: string
  tags?: string
  hasSecret: boolean
  currentVersion: number
  createdBy?: string
  createdAt?: string
  updatedAt?: string
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const items = ref<Credential[]>([])
const loading = ref(false)
const saving = ref(false)
const search = ref('')
const kindFilter = ref('')
const statusFilter = ref('active')
const dialogVisible = ref(false)
const kindOptions = ['mysql', 'redis', 'minio', 'nacos', 'ssh', 'registry', 'generic']
const form = reactive({
  id: '',
  name: '',
  kind: 'generic',
  username: '',
  secret: '',
  endpoint: '',
  scope: 'global',
  status: 'active',
  app: '',
  serverId: '',
  appInstanceId: '',
  purpose: '',
  tags: ''
})
const canManageCredentials = computed(() => can(permissions.credentialsManage))
const dialogTitle = computed(() => form.id ? t('credentials.edit') : t('credentials.add'))
const dialogCanSave = computed(() => {
  return canManageCredentials.value &&
    Boolean(form.name.trim()) &&
    Boolean(form.kind.trim()) &&
    (Boolean(form.id) || Boolean(form.secret.trim())) &&
    !saving.value
})

async function load() {
  loading.value = true
  try {
    const params = new URLSearchParams()
    if (kindFilter.value) params.set('kind', kindFilter.value)
    if (statusFilter.value) params.set('status', statusFilter.value)
    if (search.value.trim()) params.set('q', search.value.trim())
    const suffix = params.toString() ? `?${params.toString()}` : ''
    items.value = asArray<Credential>(await apiGet<Credential[] | null>(`/credentials${suffix}`))
  } finally {
    loading.value = false
  }
}

function openCreate() {
  if (!canManageCredentials.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  resetForm()
  dialogVisible.value = true
}

function openEdit(row: Credential) {
  if (!canManageCredentials.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  Object.assign(form, {
    id: row.id,
    name: row.name ?? '',
    kind: row.kind || 'generic',
    username: row.username ?? '',
    secret: '',
    endpoint: row.endpoint ?? '',
    scope: row.scope || 'global',
    status: row.status || 'active',
    app: row.app ?? '',
    serverId: row.serverId ?? '',
    appInstanceId: row.appInstanceId ?? '',
    purpose: row.purpose ?? '',
    tags: row.tags ?? ''
  })
  dialogVisible.value = true
}

async function saveCredential() {
  if (!dialogCanSave.value) {
    return
  }
  saving.value = true
  try {
    const payload: Record<string, unknown> = {
      name: form.name.trim(),
      kind: form.kind,
      username: form.username.trim(),
      endpoint: form.endpoint.trim(),
      scope: form.scope,
      status: form.status,
      app: form.app.trim(),
      serverId: form.serverId.trim(),
      appInstanceId: form.appInstanceId.trim(),
      purpose: form.purpose.trim(),
      tags: form.tags.trim()
    }
    const secret = form.secret.trim()
    if (secret) {
      if (form.kind === 'minio') {
        payload.secretKey = secret
      } else {
        payload.password = secret
      }
    }
    if (form.id) {
      await apiPut(`/credentials/${encodeURIComponent(form.id)}`, payload)
    } else {
      await apiPost('/credentials', payload)
    }
    ElMessage.success(t('credentials.saved'))
    dialogVisible.value = false
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('credentials.saveFailed'))
  } finally {
    saving.value = false
  }
}

async function deleteCredential(row: Credential) {
  if (!canManageCredentials.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  try {
    await apiDelete(`/credentials/${encodeURIComponent(row.id)}`)
    ElMessage.success(t('credentials.deleted'))
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('credentials.deleteFailed'))
  }
}

function resetForm() {
  Object.assign(form, {
    id: '',
    name: '',
    kind: 'generic',
    username: '',
    secret: '',
    endpoint: '',
    scope: 'global',
    status: 'active',
    app: '',
    serverId: '',
    appInstanceId: '',
    purpose: '',
    tags: ''
  })
}

function kindLabel(kind: string) {
  const key = `credentials.kind.${kind}`
  const label = t(key)
  return label === key ? kind : label
}

function statusLabel(status: string) {
  if (status === 'active') return t('credentials.statusActive')
  if (status === 'retired') return t('credentials.statusRetired')
  if (status === 'invalid') return t('credentials.statusInvalid')
  return status || t('common.unknown')
}

function statusTagType(status: string) {
  if (status === 'active') return 'success'
  if (status === 'retired') return 'info'
  return 'warning'
}

onMounted(load)
</script>

<style scoped>
.credentials-card {
  display: grid;
  grid-template-rows: auto minmax(0, 1fr);
  min-height: 0;
  position: relative;
}

.credentials-card :deep(.el-table) {
  min-height: 0;
}

.credentials-card :deep(.el-table__body-wrapper) {
  min-height: 160px;
}

.credentials-card .empty-state {
  position: absolute;
  inset: 56px 0 0;
  pointer-events: none;
  background: rgba(255, 255, 255, .86);
}

.secret-ok {
  color: #238636;
  font-weight: 800;
}

.secret-missing {
  color: var(--aifar-text-tertiary);
  font-weight: 800;
}

.credential-form-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 4px 12px;
}

.form-wide {
  grid-column: 1 / -1;
}

.credential-form :deep(.el-form-item) {
  margin-bottom: 12px;
}

.credential-form :deep(.el-form-item__label) {
  font-weight: 800;
  color: var(--aifar-ink);
}

@media (max-width: 720px) {
  .credential-form-grid {
    grid-template-columns: 1fr;
  }
}
</style>
