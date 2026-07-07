<template>
  <PageShell class="apps-page" :title="t('apps.title')" :subtitle="t('apps.subtitle')">
    <template #actions>
      <el-tooltip :content="deniedText" :disabled="canScanResources" placement="top">
        <span>
          <el-button :disabled="!canScanResources" @click="rescan">{{ t('common.refresh') }}</el-button>
        </span>
      </el-tooltip>
    </template>

    <el-tabs v-model="activeTab" class="aifar-panel top-tabs">
      <el-tab-pane :label="t('common.all')" name="all" />
      <el-tab-pane :label="t('apps.installed')" name="installed" />
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
      <el-table :data="installedGroups" row-key="id" class="installed-groups-table">
        <el-table-column prop="appLabel" :label="t('table.app')" min-width="140" />
        <el-table-column prop="version" :label="t('table.version')" min-width="150" show-overflow-tooltip />
        <el-table-column :label="t('dashboard.topology')" min-width="150">
          <template #default="{ row }">
            <div class="topology-cell">
              <span>{{ row.topologyLabel }}</span>
              <el-tag size="small" effect="plain" :type="row.mode === 'cluster' ? 'warning' : 'info'">{{ row.modeLabel }}</el-tag>
            </div>
          </template>
        </el-table-column>
        <el-table-column :label="t('table.server')" min-width="260" show-overflow-tooltip>
          <template #default="{ row }">
            <span>{{ row.serverText }}</span>
          </template>
        </el-table-column>
        <el-table-column :label="t('common.status')" width="120">
          <template #default="{ row }">
            <StatusTag :status="row.status" :label="row.statusLabel" />
          </template>
        </el-table-column>
        <el-table-column :label="t('common.operation')" width="150" fixed="right">
          <template #default="{ row }">
            <el-tooltip :content="deniedText" :disabled="canManageApps" placement="top">
              <span>
                <el-button size="small" type="danger" plain :disabled="!canManageApps" @click="openUninstallGroup(row)">{{ t('common.uninstall') }}</el-button>
              </span>
            </el-tooltip>
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
        <AppInstanceTable
          :instances="instances"
          :servers="servers"
          show-actions
          :can-check="canManageApps"
          :can-delete="false"
          :show-delete="false"
          :disabled-reason="deniedText"
          @check="checkDeploymentService"
        />
      </section>
    </div>

    <div v-else class="empty-panel">
      <p>{{ t('apps.settingsComing') }}</p>
    </div>

    <component
      :is="moduleDialogComponent"
      v-if="moduleDialogComponent"
      v-model="moduleDialogVisible"
      :app="moduleDialogApp"
      :servers="moduleDialogServers"
      :submitting="installSubmitting"
      :locale="locale"
      v-bind="moduleDialogProps"
      @submit="submitModuleInstall"
    />

    <el-dialog v-model="uninstallDialogVisible" :title="t('apps.uninstallService')" width="560px" destroy-on-close @closed="resetUninstallDialog">
      <div v-if="pendingUninstallGroup" class="uninstall-dialog">
        <el-alert
          v-if="pendingUninstallGroup.mode === 'cluster'"
          type="warning"
          show-icon
          :closable="false"
          :title="t('apps.clusterUninstallOnly')"
        />
        <p class="secret-confirm-message">
          {{ t('apps.uninstallGroupPasswordPrompt', { app: pendingUninstallGroup.appLabel, topology: pendingUninstallGroup.topologyLabel, count: uninstallServers.length }) }}
        </p>
        <el-form label-position="top" class="multi-secret-form">
          <el-checkbox v-if="uninstallServers.length > 1" v-model="sameUninstallPassword" @change="handleSameUninstallPasswordToggle">{{ t('database.samePassword') }}</el-checkbox>
          <el-form-item v-if="sameUninstallPassword && uninstallServers.length > 1" :label="t('database.samePasswordLabel')">
            <el-input
              v-model="uninstallSharedPassword"
              type="password"
              :placeholder="t('apps.deleteServicePasswordPlaceholder')"
              show-password
              @keyup.enter="confirmUninstallGroup"
            />
          </el-form-item>
          <el-form-item v-for="server in visibleUninstallServers" v-else :key="server.id" :label="server.label">
            <el-input
              v-model="uninstallPasswords[server.id]"
              type="password"
              :placeholder="t('apps.deleteServicePasswordPlaceholder')"
              show-password
              @keyup.enter="confirmUninstallGroup"
            />
          </el-form-item>
          <el-checkbox v-if="uninstallUsesMountedDisks" v-model="uninstallRemoveMountedDisks" class="delete-disk-option">
            {{ t('storage.removeMountedDisks') }}
          </el-checkbox>
          <p v-if="uninstallUsesMountedDisks" class="delete-disk-hint">{{ t('storage.removeMountedDisksHint') }}</p>
        </el-form>
      </div>
      <template #footer>
        <el-button :disabled="uninstallSubmitting" @click="uninstallDialogVisible = false">{{ t('common.cancel') }}</el-button>
        <el-button type="danger" :loading="uninstallSubmitting" :disabled="!uninstallServers.length" @click="confirmUninstallGroup">{{ t('common.uninstall') }}</el-button>
      </template>
    </el-dialog>

  </PageShell>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, shallowRef } from 'vue'
import type { Component } from 'vue'
import { ElMessage } from 'element-plus'
import { apiGet, apiPost, asArray } from '../api/client'
import { pairedAppCatalog, type AppCatalogResponse, type AppStoreItem } from '../apps/registry/catalog'
import { frontendModuleFor } from '../apps/registry/loader'
import { resolveAppLocale } from '../apps/registry/types'
import type { AppFrontendModule, AppInstallDialogContext, AppInstallFieldValues, AppInstallPayload, AppInstallValidationContext, CredentialOption, ServerOption } from '../apps/registry/contract'
import AppInstanceTable from '../components/AppInstanceTable.vue'
import PageShell from '../components/PageShell.vue'
import StatusTag from '../components/StatusTag.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'
import { useTaskProgressStore } from '../stores/taskProgress'

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const taskProgress = useTaskProgressStore()
const backendCatalog = ref<AppCatalogResponse>({})
const instances = ref<AppInstanceTableRecord[]>([])
const servers = ref<ServerOption[]>([])
const appSettings = ref<{ defaultDeployDir?: string; maxRequestBodyBytes?: number }>({})
const credentials = ref<CredentialOption[]>([])
const activeTab = ref('all')
const category = ref('all')
const installSubmitting = ref(false)
const moduleDialogVisible = ref(false)
const moduleDialogApp = ref<AppStoreItem | null>(null)
const moduleDialogModule = shallowRef<AppFrontendModule | null>(null)
const locale = computed(() => resolveAppLocale())
const canManageApps = computed(() => can(permissions.appsManage))
const canScanResources = computed(() => can(permissions.resourcesScan))

const apps = computed(() => pairedAppCatalog(backendCatalog.value, locale.value))
const filteredApps = computed(() => (category.value === 'all' ? apps.value : apps.value.filter((app) => app.category === category.value)))
const moduleDialogComponent = computed<Component | null>(() => moduleDialogModule.value?.installDialog ?? null)
const installDialogContext = computed<AppInstallDialogContext>(() => ({
  servers: servers.value,
  instances: instances.value,
  credentials: credentials.value,
  defaultDeployDir: appSettings.value.defaultDeployDir || '/aifar/apps'
}))
const moduleDialogConfig = computed(() => moduleDialogModule.value?.installDialogProps?.(locale.value, installDialogContext.value) ?? {})
const moduleDialogServers = computed(() => {
  const filter = moduleDialogConfig.value.targetServerFilter
  return filter ? servers.value.filter((server) => filter(server, installDialogContext.value)) : servers.value
})
const moduleDialogProps = computed(() => {
  const { targetServerFilter, targetValidationResolver, ...props } = moduleDialogConfig.value
  return {
    ...props,
    targetValidationResolver: (values: AppInstallFieldValues, context: AppInstallValidationContext) => {
      return installTargetConflictReason(moduleDialogApp.value, context) || targetValidationResolver?.(values, context) || ''
    }
  }
})
type AppInstanceTableRecord = {
  id: string
  app: string
  version: string
  serverId?: string
  status?: string
  topology?: string
  metadata?: string
  createdAt?: string
}
type InstanceMetadata = Record<string, unknown>
type InstalledAppGroup = {
  id: string
  app: string
  appLabel: string
  version: string
  topologyLabel: string
  mode: 'standalone' | 'cluster'
  modeLabel: string
  serverText: string
  status: string
  statusLabel: string
  members: AppInstanceTableRecord[]
}
type UninstallServer = {
  id: string
  label: string
}

const installedGroups = computed(() => buildInstalledGroups(instances.value))
const uninstallDialogVisible = ref(false)
const uninstallSubmitting = ref(false)
const pendingUninstallGroup = ref<InstalledAppGroup | null>(null)
const uninstallPasswords = ref<Record<string, string>>({})
const uninstallSharedPassword = ref('')
const sameUninstallPassword = ref(true)
const uninstallRemoveMountedDisks = ref(false)
const uninstallServers = computed<UninstallServer[]>(() => {
  const group = pendingUninstallGroup.value
  if (!group) {
    return []
  }
  const seen = new Set<string>()
  const out: UninstallServer[] = []
  for (const member of group.members) {
    const id = String(member.serverId || '').trim()
    if (!id || seen.has(id)) {
      continue
    }
    seen.add(id)
    out.push({ id, label: serverLabel(id) })
  }
  return out
})
const visibleUninstallServers = computed(() => sameUninstallPassword.value && uninstallServers.value.length > 1 ? [] : uninstallServers.value)
const uninstallUsesMountedDisks = computed(() => {
  const group = pendingUninstallGroup.value
  return Boolean(group?.members.some((member) => String(metadataOf(member).storageMode || '') === 'unmounted-disk'))
})

function buildInstalledGroups(rows: AppInstanceTableRecord[]) {
  const groups = new Map<string, AppInstanceTableRecord[]>()
  for (const row of rows) {
    if (!isManagedAppInstance(row)) {
      continue
    }
    const key = lifecycleGroupKey(row)
    const members = groups.get(key) ?? []
    members.push(row)
    groups.set(key, members)
  }
  return Array.from(groups.entries()).map(([id, members]) => {
    const first = members[0]
    const app = lifecycleAppFamily(first.app)
    const topology = groupTopology(members)
    const mode = groupLifecycleMode(members)
    const serverText = uniqueValues(members.map((member) => serverLabel(member.serverId)).filter((label) => label !== '-')).join(', ') || '-'
    const status = groupInstalledStatus(members)
    return {
      id,
      app,
      appLabel: appLabel(app),
      version: uniqueValues(members.map((member) => member.version || '-')).join(', '),
      topologyLabel: topologyLabel(topology),
      mode,
      modeLabel: mode === 'cluster' ? t('apps.clusterMode') : t('apps.standaloneMode'),
      serverText,
      status,
      statusLabel: status === 'installed' ? t('apps.installed') : '',
      members
    } satisfies InstalledAppGroup
  }).sort((left, right) => left.appLabel.localeCompare(right.appLabel) || left.serverText.localeCompare(right.serverText))
}

function isManagedAppInstance(row: AppInstanceTableRecord) {
  return ['aifar', 'docker', 'mysql', 'mysql-router', 'redis', 'minio', 'nacos'].includes(String(row.app || '').toLowerCase())
}

function lifecycleAppFamily(app: string) {
  const normalized = String(app || '').toLowerCase()
  return normalized === 'mysql-router' ? 'mysql' : normalized
}

function lifecycleRawAppNames(app: string) {
  const family = lifecycleAppFamily(app)
  if (family === 'mysql') {
    return new Set(['mysql', 'mysql-router'])
  }
  return new Set([family])
}

function lifecycleGroupKey(row: AppInstanceTableRecord) {
  const family = lifecycleAppFamily(row.app)
  const metadata = metadataOf(row)
  const sharedGroup = stringMeta(metadata, 'clusterId') || stringMeta(metadata, 'replicationGroupId') || stringMeta(metadata, 'replicaGroupId')
  if (sharedGroup) {
    return `${family}:group:${sharedGroup}`
  }
  return `${family}:single:${row.id}`
}

function groupLifecycleMode(members: AppInstanceTableRecord[]): 'standalone' | 'cluster' {
  if (members.length > 1) {
    return 'cluster'
  }
  const topology = groupTopology(members).toLowerCase()
  return /cluster|sentinel|distributed|replication|router/.test(topology) ? 'cluster' : 'standalone'
}

function groupTopology(members: AppInstanceTableRecord[]) {
  const topologies = uniqueValues(members.map((member) => String(member.topology || '').trim()).filter(Boolean))
  return topologies[0] || '-'
}

function topologyLabel(value: string) {
  const normalized = value.toLowerCase()
  if (!value || value === '-') {
    return '-'
  }
  if (normalized === 'standalone') {
    return t('apps.standaloneMode')
  }
  return value
}

function groupInstalledStatus(members: AppInstanceTableRecord[]) {
  if (members.some((member) => ['failed', 'install_failed', 'error', 'unavailable'].includes(String(member.status || '').toLowerCase()) || truthyValue(metadataOf(member).installFailed))) {
    return 'failed'
  }
  return 'installed'
}

function appLabel(app: string) {
  const item = apps.value.find((candidate) => candidate.name === app || candidate.installName === app)
  return item?.title || app
}

function installTargetConflictReason(app: AppStoreItem | null, context: AppInstallValidationContext) {
  if (!app || !context.selectedServers.length) {
    return ''
  }
  const targetIds = new Set(context.selectedServers.map((server) => server.id))
  const relatedApps = lifecycleRawAppNames(app.installName || app.name)
  const conflicts = instances.value.filter((instance) => {
    return targetIds.has(String(instance.serverId || '')) && relatedApps.has(String(instance.app || '').toLowerCase())
  })
  if (!conflicts.length) {
    return ''
  }
  const conflictServers = uniqueValues(conflicts.map((instance) => serverLabel(instance.serverId)).filter((label) => label !== '-')).join(', ')
  return t('apps.installTargetConflict', { app: app.title, servers: conflictServers })
}

function metadataOf(row: AppInstanceTableRecord): InstanceMetadata {
  if (!row.metadata) {
    return {}
  }
  try {
    const parsed = JSON.parse(row.metadata)
    return parsed && typeof parsed === 'object' && !Array.isArray(parsed) ? parsed as InstanceMetadata : {}
  } catch {
    return {}
  }
}

function stringMeta(metadata: InstanceMetadata, key: string) {
  const value = metadata[key]
  return typeof value === 'string' ? value.trim() : ''
}

function truthyValue(value: unknown) {
  return value === true || value === 1 || ['true', '1', 'yes', 'y'].includes(String(value ?? '').trim().toLowerCase())
}

function uniqueValues(values: string[]) {
  const seen = new Set<string>()
  const out: string[] = []
  for (const value of values) {
    const next = String(value ?? '').trim()
    if (!next || seen.has(next)) {
      continue
    }
    seen.add(next)
    out.push(next)
  }
  return out
}

function displayVersion(app: AppStoreItem) {
  return app.versions.at(-1) || app.fallbackVersion || '-'
}

function countByCategory(name: string) {
  return apps.value.filter((app) => app.category === name).length
}

function missingText(app: AppStoreItem) {
  return app.missing.length ? app.missing.join(', ') : t('apps.missingResource')
}

function deployDisabledReason(app: AppStoreItem) {
  if (!canManageApps.value) {
    return deniedText.value
  }
  if (!app.backendReady) {
    return t('apps.missingBackend')
  }
  if (!app.deployable) {
    return t('apps.missing', { value: missingText(app) })
  }
  const module = frontendModuleFor(app.name)
  const moduleReason = module?.deployDisabledReason?.(locale.value, installDialogContext.value)
  if (moduleReason) {
    return moduleReason
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
  backendCatalog.value = await apiGet<AppCatalogResponse>(`/apps/catalog?lang=${locale.value}`).catch(() => ({}))
  instances.value = asArray(await apiGet<AppInstanceTableRecord[] | null>('/apps/instances').catch(() => []))
  servers.value = asArray(await apiGet<ServerOption[] | null>('/servers').catch(() => []))
  credentials.value = asArray(await apiGet<CredentialOption[] | null>('/credentials?status=active').catch(() => []))
  appSettings.value = await apiGet<{ defaultDeployDir?: string; maxRequestBodyBytes?: number }>('/settings').catch(() => ({ defaultDeployDir: '/aifar/apps' }))
}

async function rescan() {
  if (!canScanResources.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  await apiPost('/resources/rescan')
  backendCatalog.value = await apiGet<AppCatalogResponse>(`/apps/catalog?lang=${locale.value}`).catch(() => ({}))
}

async function submitModuleInstall(payload: AppInstallPayload) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
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
    taskProgress.track(result.taskId, app.title)
    ElMessage.success(t('apps.installTaskAccepted'))
  } finally {
    installSubmitting.value = false
  }
}

async function checkDeploymentService(row: AppInstanceTableRecord) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  try {
    const result = await apiPost<{ taskId: string }>(`/apps/instances/${row.id}/check`, {
      language: locale.value
    })
    taskProgress.track(result.taskId, row.app)
    ElMessage.success(t('apps.checkServiceAccepted'))
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.checkServiceFailed'))
  }
}

function openUninstallGroup(group: InstalledAppGroup) {
  if (!canManageApps.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  pendingUninstallGroup.value = group
  sameUninstallPassword.value = uninstallServers.value.length > 1
  uninstallSharedPassword.value = ''
  uninstallRemoveMountedDisks.value = false
  uninstallPasswords.value = Object.fromEntries(uninstallServers.value.map((server) => [server.id, '']))
  uninstallDialogVisible.value = true
}

function resetUninstallDialog() {
  if (uninstallSubmitting.value) {
    return
  }
  pendingUninstallGroup.value = null
  uninstallSharedPassword.value = ''
  uninstallPasswords.value = {}
  uninstallRemoveMountedDisks.value = false
  sameUninstallPassword.value = true
}

function handleSameUninstallPasswordToggle() {
  if (sameUninstallPassword.value) {
    uninstallSharedPassword.value = uninstallServers.value[0] ? uninstallPasswords.value[uninstallServers.value[0].id] || '' : ''
    return
  }
  if (uninstallSharedPassword.value.trim()) {
    uninstallPasswords.value = Object.fromEntries(uninstallServers.value.map((server) => [server.id, uninstallSharedPassword.value]))
  }
}

function collectUninstallPasswords() {
  const serversToConfirm = uninstallServers.value
  if (!serversToConfirm.length) {
    return null
  }
  if (sameUninstallPassword.value && serversToConfirm.length > 1) {
    const password = uninstallSharedPassword.value.trim()
    if (!password) {
      return null
    }
    return Object.fromEntries(serversToConfirm.map((server) => [server.id, password]))
  }
  const out: Record<string, string> = {}
  for (const server of serversToConfirm) {
    const password = String(uninstallPasswords.value[server.id] || '').trim()
    if (!password) {
      return null
    }
    out[server.id] = password
  }
  return out
}

async function confirmUninstallGroup() {
  const group = pendingUninstallGroup.value
  if (!group) {
    return
  }
  const passwords = collectUninstallPasswords()
  if (!passwords) {
    ElMessage.warning(t('database.deletePasswordsRequired'))
    return
  }
  uninstallSubmitting.value = true
  try {
    const body: Record<string, unknown> = {
      instanceIds: group.members.map((member) => member.id),
      serverPasswords: passwords,
      language: locale.value
    }
    if (uninstallUsesMountedDisks.value) {
      body.removeMountedDisks = uninstallRemoveMountedDisks.value
    }
    const result = await apiPost<{ taskId: string }>('/apps/instances/batch-delete', body)
    uninstallDialogVisible.value = false
    ElMessage.success(t('apps.uninstallGroupAccepted', { count: group.members.length }))
    taskProgress.track(result.taskId, `${t('apps.uninstallService')} ${group.appLabel}`)
    await load()
  } catch (err) {
    ElMessage.error(err instanceof Error ? err.message : t('apps.deleteServiceFailed'))
  } finally {
    uninstallSubmitting.value = false
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

.installed-groups-table {
  width: 100%;
}

.topology-cell {
  min-width: 0;
  display: inline-flex;
  align-items: center;
  gap: 8px;
}

.uninstall-dialog {
  display: grid;
  gap: 12px;
}

.multi-secret-form {
  display: grid;
  gap: 4px;
}

.delete-disk-option {
  margin-top: 4px;
}

.delete-disk-hint {
  margin: 0;
  color: var(--aifar-text-secondary);
  font-size: 12px;
  line-height: 18px;
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
