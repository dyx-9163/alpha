<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('database.title') }}</h1>
        <p class="page-subtitle">{{ t('database.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
        <el-button type="primary" @click="router.push('/apps')">{{ t('database.deployFromApps') }}</el-button>
      </div>
    </div>

    <div class="aifar-panel status-line">
      <span class="subtle-note">{{ t('database.notChecked') }}</span>
      <span class="status-pill success">{{ t('common.connected') }}</span>
      <span class="subtle-note">{{ t('database.instanceCount', { count: instances.length }) }}</span>
    </div>

    <el-tabs v-model="tab" class="tab-strip">
      <el-tab-pane :label="t('database.instances')" name="instances" />
      <el-tab-pane :label="t('database.backups')" name="backups" />
      <el-tab-pane :label="t('database.runs')" name="runs" />
      <el-tab-pane :label="t('apps.settings')" name="settings" />
    </el-tabs>

    <div class="workspace-card database-main">
      <template v-if="tab === 'instances'">
        <div class="table-toolbar">
          <div class="head-actions">
            <span class="status-pill">{{ t('common.all') }} {{ instances.length }}</span>
            <span class="status-pill">MySQL {{ mysqlCount }}</span>
            <span class="status-pill">Redis {{ redisCount }}</span>
            <span class="status-pill">{{ t('database.managed') }} {{ instances.length }}</span>
          </div>
          <el-input v-model="search" :placeholder="t('common.search')" clearable class="toolbar-control is-sm" />
        </div>

        <div class="db-card-grid" v-if="filteredInstances.length">
          <article v-for="item in filteredInstances" :key="item.id" class="db-card">
            <div class="db-head">
              <div class="app-icon small">{{ item.app === 'redis' ? 'RE' : 'MY' }}</div>
              <div>
                <strong>{{ item.app }}-{{ item.id.slice(-6) }}</strong>
                <span>{{ serverName(item.serverId) }} / {{ item.topology || '-' }}</span>
              </div>
            </div>
            <div class="db-grid">
              <div><span>{{ t('database.engine') }}</span><strong>{{ item.app }}</strong></div>
              <div><span>{{ t('dashboard.topology') }}</span><strong>{{ item.topology || '-' }}</strong></div>
              <div><span>{{ t('common.version') }}</span><strong>{{ item.version }}</strong></div>
              <div><span>{{ t('servers.port') }}</span><strong>{{ metadataOf(item).port || defaultPort(item.app) }}</strong></div>
              <div><span>{{ t('common.endpoint') }}</span><strong>{{ metadataOf(item).endpoint || '-' }}</strong></div>
              <div><span>{{ t('common.status') }}</span><StatusTag :status="item.status" /></div>
            </div>
            <div class="card-actions">
              <el-tooltip :content="deniedText" :disabled="canManageApps" placement="top">
                <span><el-button size="small" :disabled="!canManageApps" @click="checkInstance(item.id)">{{ t('common.check') }}</el-button></span>
              </el-tooltip>
              <el-tooltip :content="deniedText" :disabled="canManageDatabase" placement="top">
                <span><el-button size="small" type="primary" plain :disabled="!canManageDatabase" @click="backupInstance(item.id)">{{ t('database.backupNow') }}</el-button></span>
              </el-tooltip>
            </div>
          </article>
        </div>
        <div v-else class="empty-state"><div><strong>{{ t('database.noInstancesTitle') }}</strong><span>{{ t('database.noInstancesDesc') }}</span></div></div>
      </template>

      <template v-else-if="tab === 'backups'">
        <div class="muted-strip">{{ t('database.backupHint') }}</div>
        <RunRecordTable :records="backupTasks" :type-width="180" />
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
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
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
  createdAt: string
}

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const router = useRouter()
const instances = ref<AppInstance[]>([])
const servers = ref<any[]>([])
const tasks = ref<any[]>([])
const tab = ref('instances')
const search = ref('')
const mysqlCount = computed(() => instances.value.filter((item) => item.app === 'mysql').length)
const redisCount = computed(() => instances.value.filter((item) => item.app === 'redis').length)
const canManageDatabase = computed(() => can(permissions.databaseManage))
const canManageApps = computed(() => can(permissions.appsManage))
const filteredInstances = computed(() => {
  const q = search.value.trim().toLowerCase()
  if (!q) return instances.value
  return instances.value.filter((item) => `${item.app} ${item.version} ${item.topology} ${serverName(item.serverId)}`.toLowerCase().includes(q))
})
const backupTasks = computed(() => tasks.value.filter((item) => item.type === 'database.backup'))
const runTasks = computed(() => tasks.value.filter((item) => item.type?.startsWith('apps.mysql.') || item.type?.startsWith('apps.redis.') || item.type?.startsWith('database.')))
const settingsItems = computed(() => [
  { label: 'MySQL', value: t('database.mysqlSettings') },
  { label: 'Redis', value: t('database.redisSettings') },
  { label: t('common.provider'), value: t('common.real') },
  { label: t('database.backups'), value: t('database.backupHint') }
])

async function load() {
  instances.value = asArray(await apiGet<AppInstance[] | null>('/database/instances').catch(() => []))
  servers.value = asArray(await apiGet<any[] | null>('/servers').catch(() => []))
  tasks.value = asArray(await apiGet<any[] | null>('/tasks').catch(() => []))
}

function metadataOf(item: AppInstance) {
  try {
    return JSON.parse(item.metadata || '{}') as Record<string, any>
  } catch {
    return {}
  }
}

function defaultPort(app: string) {
  return app === 'redis' ? 6379 : 3306
}

function serverName(id: string) {
  const server = servers.value.find((item) => item.id === id)
  return server ? `${server.name} (${server.host})` : id || '-'
}

async function backupInstance(id: string) {
  if (!canManageDatabase.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const result = await apiPost<{ taskId: string }>(`/database/instances/${id}/backup`)
  ElMessage.success(t('database.backupAccepted'))
  await load()
  void router.push({ path: '/tasks', query: { taskId: result.taskId } })
}

async function checkInstance(id: string) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  const result = await apiPost<{ taskId: string }>(`/apps/instances/${id}/check`)
  ElMessage.success(t('apps.checkServiceAccepted'))
  void router.push({ path: '/tasks', query: { taskId: result.taskId } })
}

function openTaskDetails(row: { id: string }) {
  void router.push({ path: '/tasks', query: { taskId: row.id } })
}

onMounted(load)
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

.db-card-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  gap: 12px;
  padding: 12px;
  min-height: 0;
  overflow: auto;
}

.db-card {
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  background: #fff;
  padding: 12px;
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

.db-head strong,
.db-head span {
  display: block;
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

.card-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  margin-top: 12px;
}

.settings-grid {
  padding: 12px;
}
</style>
