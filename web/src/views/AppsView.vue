<template>
  <section class="apps-page">
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('apps.title') }}</h1>
        <p class="page-subtitle">{{ t('apps.subtitle') }}</p>
      </div>
      <el-button @click="rescan">{{ t('common.refresh') }}</el-button>
    </div>

    <el-tabs v-model="activeTab" class="aifar-panel top-tabs">
      <el-tab-pane :label="t('common.all')" name="all" />
      <el-tab-pane :label="t('apps.installed')" name="installed" />
      <el-tab-pane :label="t('apps.updates')" name="updates" />
      <el-tab-pane :label="t('apps.tasks')" name="tasks" />
      <el-tab-pane :label="t('apps.settings')" name="settings" />
    </el-tabs>

    <div class="aifar-panel filter-panel">
      <el-radio-group v-model="category" size="large">
        <el-radio-button label="all">{{ t('common.all') }} {{ apps.length }}</el-radio-button>
        <el-radio-button label="database">{{ t('category.database') }} {{ countByCategory('database') }}</el-radio-button>
        <el-radio-button label="devops">{{ t('category.devops') }} {{ countByCategory('devops') }}</el-radio-button>
        <el-radio-button label="storage">{{ t('category.storage') }} {{ countByCategory('storage') }}</el-radio-button>
      </el-radio-group>
    </div>

    <div v-if="activeTab === 'all'" class="apps-section">
      <div class="muted-strip">{{ t('apps.protocolHint') }}</div>

      <div v-if="filteredApps.length" class="app-grid">
        <article v-for="app in filteredApps" :key="app.name" class="app-card" :class="{ 'not-deployable': !app.deployable }">
          <div class="app-card-main">
            <div class="app-icon">{{ app.icon }}</div>
            <div class="app-copy">
              <div class="app-title-row">
                <h2>{{ app.title }}</h2>
                <span class="version">{{ displayVersion(app) }}</span>
              </div>
              <p>{{ app.description }}</p>
              <div class="tag-row">
                <span class="meta-tag blue">{{ app.categoryLabel }}</span>
                <span class="meta-tag blue">{{ app.sourceLabel }}</span>
                <span class="meta-tag gray">{{ t('common.builtIn') }}</span>
              </div>
              <div class="tag-row">
                <span class="meta-tag green">{{ t('apps.frontendReady') }}</span>
                <span class="meta-tag green">{{ t('apps.backendReady') }}</span>
                <span v-if="app.deployable" class="meta-tag green">{{ t('apps.resourceReady') }}</span>
                <span v-else class="meta-tag orange">{{ t('apps.missing', { value: missingText(app) }) }}</span>
              </div>
            </div>
          </div>
          <div class="app-card-footer">
            <span class="app-deploy-note">{{ app.deployable ? t('apps.resourceReady') : t('apps.missingResource') }}</span>
            <el-tooltip :content="deployDisabledReason(app)" :disabled="!deployDisabledReason(app)" placement="top">
              <span class="install-button-wrap">
                <el-button size="small" type="primary" :disabled="Boolean(deployDisabledReason(app))" @click="openInstallDialog(app)">{{ t('apps.install') }}</el-button>
              </span>
            </el-tooltip>
          </div>
        </article>
      </div>

      <div v-else class="empty-panel">
        <p>{{ t('apps.empty') }}</p>
      </div>
    </div>

    <div v-else-if="activeTab === 'installed'" class="table-panel">
      <el-table :data="instances">
        <el-table-column prop="app" :label="t('table.app')" />
        <el-table-column prop="version" :label="t('table.version')" />
        <el-table-column prop="serverId" :label="t('table.server')" />
        <el-table-column prop="status" :label="t('common.status')">
          <template #default="{ row }">
            <StatusTag :status="row.status" />
          </template>
        </el-table-column>
      </el-table>
    </div>

    <div v-else-if="activeTab === 'tasks'" class="deployment-records">
      <section class="record-panel">
        <div class="record-head">
          <div>
            <h2>{{ t('apps.deployedServices') }}</h2>
            <p>{{ t('apps.deployedServicesHint') }}</p>
          </div>
          <el-button size="small" @click="load">{{ t('common.refresh') }}</el-button>
        </div>
        <el-table :data="instances">
          <el-table-column prop="app" :label="t('table.app')" min-width="120" />
          <el-table-column prop="version" :label="t('table.version')" min-width="120" />
          <el-table-column :label="t('table.server')" min-width="180">
            <template #default="{ row }">
              {{ serverLabel(row.serverId) }}
            </template>
          </el-table-column>
          <el-table-column prop="status" :label="t('common.status')" width="120">
            <template #default="{ row }">
              <StatusTag :status="row.status" />
            </template>
          </el-table-column>
          <el-table-column :label="t('common.time')" min-width="180">
            <template #default="{ row }">
              {{ formatTime(row.createdAt) }}
            </template>
          </el-table-column>
          <el-table-column :label="t('common.operation')" width="220" fixed="right">
            <template #default="{ row }">
              <el-button size="small" plain @click="checkDeploymentService(row)">{{ t('common.check') }}</el-button>
              <el-button size="small" type="danger" plain @click="deleteDeploymentService(row)">{{ t('common.delete') }}</el-button>
            </template>
          </el-table-column>
        </el-table>
      </section>
    </div>

    <div v-else class="empty-panel">
      <p>{{ activeTab === 'updates' ? t('apps.noUpdates') : t('apps.settingsComing') }}</p>
    </div>

    <component
      :is="moduleDialogComponent"
      v-if="moduleDialogComponent"
      v-model="moduleDialogVisible"
      :app="moduleDialogApp"
      :servers="servers"
      :submitting="installSubmitting"
      :locale="locale"
      v-bind="moduleDialogProps"
      @submit="submitModuleInstall"
    />
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import type { Component } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { useRouter } from 'vue-router'
import { apiGet, apiPost, asArray } from '../api/client'
import { pairedAppCatalog, type AppCatalogResponse, type AppStoreItem } from '../apps/registry/catalog'
import { frontendModuleFor } from '../apps/registry/loader'
import { resolveAppLocale } from '../apps/registry/locale'
import type { AppFrontendModule, AppInstallDialogConfig, AppInstallField, AppInstallPayload, ServerOption } from '../apps/registry/contract'
import StatusTag from '../components/StatusTag.vue'
import { useI18n } from '../i18n'

const { t } = useI18n()
const router = useRouter()
const backendCatalog = ref<AppCatalogResponse>({})
const instances = ref<any[]>([])
const servers = ref<ServerOption[]>([])
const settings = ref<{ defaultPassword?: string } | null>(null)
const activeTab = ref('all')
const category = ref('all')
const installSubmitting = ref(false)
const moduleDialogVisible = ref(false)
const moduleDialogApp = ref<AppStoreItem | null>(null)
const moduleDialogModule = shallowRef<AppFrontendModule | null>(null)
const locale = computed(() => resolveAppLocale())

const apps = computed(() => pairedAppCatalog(backendCatalog.value, locale.value))
const filteredApps = computed(() => (category.value === 'all' ? apps.value : apps.value.filter((app) => app.category === category.value)))
const moduleDialogComponent = computed<Component | null>(() => moduleDialogModule.value?.installDialog ?? null)
const moduleDialogProps = computed(() => withDefaultPasswords(moduleDialogModule.value?.installDialogProps?.(locale.value) ?? {}))

type AppInstanceRecord = {
  id: string
  app: string
  version: string
  serverId: string
  status: string
  createdAt?: string
}

function displayVersion(app: AppStoreItem) {
  return app.versions.at(-1) ?? app.fallbackVersion
}

function countByCategory(name: string) {
  return apps.value.filter((app) => app.category === name).length
}

function missingText(app: AppStoreItem) {
  return app.missing.length ? app.missing.join(', ') : t('apps.missingResource')
}

function deployDisabledReason(app: AppStoreItem) {
  if (!app.backendReady) {
    return t('apps.missingBackend')
  }
  if (!app.deployable) {
    return t('apps.missing', { value: missingText(app) })
  }
  return ''
}

function openInstallDialog(app: AppStoreItem) {
  const reason = deployDisabledReason(app)
  if (reason) {
    ElMessage.warning(reason)
    return
  }
  const module = frontendModuleFor(app.name)
  if (!module?.installDialog) {
    ElMessage.warning(t('apps.missingFrontend'))
    return
  }
  moduleDialogApp.value = app
  moduleDialogModule.value = module
  moduleDialogVisible.value = true
}

async function load() {
  settings.value = await apiGet<{ defaultPassword?: string }>('/settings').catch(() => ({ defaultPassword: 'Oversea.123' }))
  backendCatalog.value = await apiGet<AppCatalogResponse>(`/apps/catalog?lang=${locale.value}`).catch(() => ({}))
  instances.value = asArray(await apiGet<any[] | null>('/apps/instances').catch(() => []))
  servers.value = asArray(await apiGet<ServerOption[] | null>('/servers').catch(() => []))
}

function withDefaultPasswords(config: AppInstallDialogConfig): AppInstallDialogConfig {
  const defaultPassword = settings.value?.defaultPassword || 'Oversea.123'
  const fields = config.fields?.map((field) => withDefaultPassword(field, defaultPassword))
  return { ...config, fields }
}

function withDefaultPassword(field: AppInstallField, defaultPassword: string): AppInstallField {
  if (field.type !== 'password' || field.defaultValue !== undefined) {
    return field
  }
  return { ...field, defaultValue: defaultPassword }
}

async function rescan() {
  await apiPost('/resources/rescan')
  backendCatalog.value = await apiGet<AppCatalogResponse>(`/apps/catalog?lang=${locale.value}`).catch(() => ({}))
}

async function submitModuleInstall(payload: AppInstallPayload) {
  const app = moduleDialogApp.value
  if (!app) {
    return
  }
  installSubmitting.value = true
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/${app.installName}/install`, {
      ...payload,
      language: locale.value
    })
    moduleDialogVisible.value = false
    openTaskCenter(result.taskId)
  } finally {
    installSubmitting.value = false
  }
}

async function checkDeploymentService(row: AppInstanceRecord) {
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${row.id}/check`, {
      language: locale.value
    })
    openTaskCenter(result.taskId)
    ElMessage.success(t('apps.checkServiceAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.checkServiceFailed'))
  }
}

async function deleteDeploymentService(row: AppInstanceRecord) {
  let password = ''
  try {
    const result = await ElMessageBox.prompt(
      t('apps.deleteServicePasswordPrompt', { server: serverLabel(row.serverId) }),
      t('apps.deleteService'),
      {
        inputType: 'password',
        inputPlaceholder: t('apps.deleteServicePasswordPlaceholder'),
        confirmButtonText: t('common.delete'),
        cancelButtonText: t('common.cancel'),
        type: 'warning'
      }
    )
    password = String(result.value ?? '')
  } catch {
    return
  }
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${row.id}/delete`, {
      serverPassword: password,
      language: locale.value
    })
    openTaskCenter(result.taskId)
    ElMessage.success(t('apps.deleteServiceAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  }
}

function serverLabel(serverId?: string) {
  if (!serverId) {
    return '-'
  }
  const server = servers.value.find((item) => item.id === serverId)
  if (!server) {
    return serverId
  }
  return server.name && server.host ? `${server.name} (${server.host})` : server.name || server.host || serverId
}

function formatTime(value?: string) {
  if (!value) {
    return '-'
  }
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) {
    return value
  }
  return date.toLocaleString()
}

function openTaskCenter(taskId: string) {
  void router.push({ path: '/tasks', query: { taskId } })
}

onMounted(load)
</script>

<style scoped>
.apps-section {
  display: grid;
  gap: 10px;
  min-width: 0;
  align-content: start;
}

.top-tabs {
  padding-bottom: 0;
}

.top-tabs :deep(.el-tabs__header) {
  margin: 0;
}

.top-tabs :deep(.el-tabs__nav-wrap::after) {
  height: 1px;
  background: var(--aifar-border-soft);
}

.filter-panel {
  display: flex;
  justify-content: flex-start;
  align-items: center;
  min-height: 48px;
  overflow-x: auto;
}

.filter-panel :deep(.el-radio-button__inner) {
  border-color: var(--aifar-border);
  color: #61718a;
  font-weight: 800;
  background: var(--aifar-surface-subtle);
}

.filter-panel :deep(.el-radio-button__original-radio:checked + .el-radio-button__inner) {
  color: var(--aifar-primary);
  background: var(--aifar-primary-soft);
  border-color: #8ec5ff;
  box-shadow: -1px 0 0 0 #8ec5ff;
}

.app-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  gap: 12px;
  align-items: stretch;
}

.app-card {
  min-height: 176px;
  background: var(--aifar-surface);
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  box-shadow: var(--aifar-shadow-card);
  padding: 14px;
  display: flex;
  flex-direction: column;
  gap: 12px;
  position: relative;
  transition: border-color .16s ease, box-shadow .16s ease, transform .16s ease;
}

.app-card:hover {
  border-color: #91caff;
  box-shadow: var(--aifar-shadow-raised);
  transform: translateY(-1px);
}

.app-card-main {
  display: grid;
  grid-template-columns: 48px minmax(0, 1fr);
  gap: 12px;
  min-width: 0;
}

.app-icon {
  width: 44px;
  height: 44px;
  border-radius: 6px;
  display: grid;
  place-items: center;
  color: var(--aifar-primary);
  background: var(--aifar-primary-soft);
  border: 1px solid #bae0ff;
  font-weight: 850;
  font-size: 16px;
}

.app-title-row {
  min-width: 0;
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: 12px;
}

.app-copy h2 {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 16px;
  line-height: 22px;
  font-weight: 850;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

.app-copy p {
  margin: 6px 0 10px;
  color: var(--aifar-text-secondary);
  font-size: 13px;
  line-height: 20px;
  min-height: 42px;
}

.tag-row {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 7px;
  flex-wrap: wrap;
}

.meta-tag {
  border-radius: 4px;
  border: 1px solid currentColor;
  padding: 1px 6px;
  font-size: 11px;
  line-height: 16px;
  font-weight: 850;
  background: #fff;
}

.meta-tag.blue {
  color: var(--aifar-primary);
  background: #f0f7ff;
}

.meta-tag.gray {
  color: #6b7a90;
  background: #f8fafc;
}

.meta-tag.green {
  color: #389e0d;
  background: #f6ffed;
}

.meta-tag.orange {
  color: #d48806;
  background: #fffbe6;
}

.version {
  color: var(--aifar-text-tertiary);
  font-size: 11px;
  font-weight: 700;
  flex: 0 0 auto;
  padding-top: 4px;
}

.not-deployable {
  background: #fffdf8;
}

.app-card-footer {
  margin-top: auto;
  padding-top: 10px;
  border-top: 1px solid var(--aifar-border-soft);
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.app-deploy-note {
  color: var(--aifar-text-tertiary);
  font-size: 12px;
  font-weight: 750;
}

.install-button-wrap {
  display: inline-flex;
  flex: 0 0 auto;
}

.install-button-wrap :deep(.el-button) {
  min-width: 64px;
  height: 30px;
  padding: 0 14px;
  font-size: 12px;
}

.table-panel,
.empty-panel,
.record-panel {
  background: var(--aifar-surface);
  border: 1px solid var(--aifar-border);
  border-radius: var(--aifar-radius-lg);
  box-shadow: var(--aifar-shadow-card);
  padding: 10px;
}

.deployment-records {
  display: grid;
  gap: 12px;
  min-width: 0;
}

.record-head {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  gap: 12px;
  margin-bottom: 10px;
}

.record-head h2 {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 15px;
  line-height: 22px;
  font-weight: 850;
}

.record-head p {
  margin: 3px 0 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
}

.empty-panel {
  color: var(--aifar-text-secondary);
}

@media (max-width: 980px) {
  .filter-panel {
    display: grid;
    gap: 10px;
    justify-content: stretch;
  }

  .app-grid {
    grid-template-columns: 1fr;
  }

  .app-card-main {
    grid-template-columns: 1fr;
  }

  .app-card-footer {
    align-items: flex-start;
    flex-direction: column;
  }
}

@media (min-width: 1600px) {
  .app-grid {
    grid-template-columns: repeat(auto-fill, minmax(330px, 1fr));
  }
}
</style>
