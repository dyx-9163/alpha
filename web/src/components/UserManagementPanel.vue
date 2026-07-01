<template>
  <div class="settings-block">
    <div class="panel-head">
      <div>
        <label>{{ t('settings.userManagement') }}</label>
        <span class="subtle-note">{{ t('settings.userManagementNote') }}</span>
      </div>
      <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
        <span>
          <el-button type="primary" :disabled="!canManage" @click="openCreate">
            {{ t('settings.addUser') }}
          </el-button>
        </span>
      </el-tooltip>
    </div>

    <DataTable
      class="user-table"
      :rows="users as unknown as Record<string, unknown>[]"
      :columns="columns"
      :title="t('settings.userAccounts')"
      row-key="username"
      :height="260"
      :loading="loading"
      :fit="false"
      :table-width="880"
    >
      <template #toolbar>
        <el-button size="small" :disabled="!canManage" @click="refresh">{{ t('common.refresh') }}</el-button>
      </template>
      <template #role="{ row }">
        <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
          <span>
            <el-select
              :model-value="row.role"
              size="small"
              class="role-select"
              :disabled="!canManage"
              @change="updateRole(row.username, $event)"
            >
              <el-option v-for="role in roles" :key="role" :label="roleLabel(role)" :value="role" />
            </el-select>
          </span>
        </el-tooltip>
      </template>
      <template #createdAt="{ row }">
        {{ formatDate(row.createdAt) }}
      </template>
      <template #action="{ row }">
        <el-tooltip :content="disabledReason" :disabled="canManage" placement="top">
          <span>
            <el-button size="small" :disabled="!canManage" @click="openPassword(row.username)">
              {{ t('settings.resetPassword') }}
            </el-button>
          </span>
        </el-tooltip>
      </template>
    </DataTable>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="420px">
      <el-form label-position="top">
        <el-form-item v-if="dialogMode === 'create'" :label="t('settings.accountUsername')">
          <el-input v-model="userForm.username" autocomplete="off" />
        </el-form-item>
        <el-form-item v-if="dialogMode === 'create'" :label="t('settings.accountRole')">
          <el-select v-model="userForm.role" class="full-control">
            <el-option v-for="role in roles" :key="role" :label="roleLabel(role)" :value="role" />
          </el-select>
        </el-form-item>
        <el-form-item :label="t('settings.accountPassword')">
          <el-input v-model="userForm.password" type="password" show-password autocomplete="new-password" />
        </el-form-item>
        <span class="subtle-note">{{ t('settings.passwordPolicy') }}</span>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="primary" :loading="saving" @click="submitDialog">{{ t('common.save') }}</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiGet, apiPost, apiPut } from '../api/client'
import { useI18n } from '../i18n'
import DataTable from './DataTable.vue'

const props = defineProps<{
  canManage: boolean
  disabledReason: string
}>()

type UserSummary = {
  id: string
  username: string
  role: string
  tokenVersion: number
  createdAt: string
}

const roles = ['owner', 'admin', 'operator', 'viewer', 'auditor']
const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const dialogVisible = ref(false)
const dialogMode = ref<'create' | 'password'>('create')
const selectedUsername = ref('')
const users = ref<UserSummary[]>([])
const userForm = reactive({ username: '', password: '', role: 'viewer' })

const columns = computed(() => [
  { prop: 'username', label: t('settings.accountUsername'), width: 240 },
  { prop: 'role', label: t('settings.accountRole'), width: 170, slot: 'role' },
  { prop: 'tokenVersion', label: t('settings.tokenVersion'), width: 130 },
  { prop: 'createdAt', label: t('common.time'), width: 190, slot: 'createdAt' },
  { label: t('common.operation'), width: 150, slot: 'action' }
])
const dialogTitle = computed(() => dialogMode.value === 'create' ? t('settings.addUser') : t('settings.resetPasswordFor', { username: selectedUsername.value }))

async function refresh() {
  if (!props.canManage) {
    users.value = []
    return
  }
  loading.value = true
  try {
    const res = await apiGet<{ items?: UserSummary[] }>('/users')
    users.value = Array.isArray(res.items) ? res.items : []
  } catch {
    users.value = []
  } finally {
    loading.value = false
  }
}

function openCreate() {
  userForm.username = ''
  userForm.password = ''
  userForm.role = 'viewer'
  selectedUsername.value = ''
  dialogMode.value = 'create'
  dialogVisible.value = true
}

function openPassword(username: unknown) {
  if (typeof username !== 'string') {
    return
  }
  userForm.username = username
  userForm.password = ''
  selectedUsername.value = username
  dialogMode.value = 'password'
  dialogVisible.value = true
}

async function submitDialog() {
  if (!props.canManage) {
    ElMessage.warning(props.disabledReason)
    return
  }
  if (!userForm.password || userForm.password.length < 8) {
    ElMessage.warning(t('settings.passwordPolicy'))
    return
  }
  saving.value = true
  try {
    if (dialogMode.value === 'create') {
      await apiPost('/users', { username: userForm.username, password: userForm.password, role: userForm.role })
      ElMessage.success(t('settings.userCreated'))
    } else {
      await apiPut(`/users/${encodeURIComponent(selectedUsername.value)}/password`, { password: userForm.password })
      ElMessage.success(t('settings.passwordReset'))
    }
    dialogVisible.value = false
    await refresh()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.userOperationFailed'))
  } finally {
    saving.value = false
  }
}

async function updateRole(username: unknown, role: unknown) {
  if (!props.canManage || typeof username !== 'string' || typeof role !== 'string') {
    ElMessage.warning(props.disabledReason)
    return
  }
  try {
    await apiPut(`/users/${encodeURIComponent(username)}/role`, { role })
    ElMessage.success(t('settings.roleUpdated'))
    await refresh()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('settings.userOperationFailed'))
    await refresh()
  }
}

function roleLabel(role: string) {
  const key = `settings.roles.${role}`
  const text = t(key)
  return text === key ? role : text
}

function formatDate(value: unknown) {
  if (!value) {
    return '-'
  }
  const date = new Date(String(value))
  if (Number.isNaN(date.getTime())) {
    return String(value)
  }
  return date.toLocaleString()
}

onMounted(refresh)
defineExpose({ refresh })
</script>

<style scoped>
.settings-block {
  margin-bottom: 0;
  padding: 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-lg);
  background: #fbfdff;
}

.settings-block label {
  display: block;
  margin-bottom: 8px;
  color: var(--aifar-ink);
  font-weight: 800;
}

.settings-block .subtle-note {
  display: block;
  margin-top: 8px;
}

.panel-head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.user-table {
  height: 300px;
  margin-top: 12px;
  border: 1px solid var(--aifar-border-soft);
  border-radius: var(--aifar-radius-md);
  overflow: hidden;
}

.role-select {
  width: 140px;
}

.full-control {
  width: 100%;
}
</style>
