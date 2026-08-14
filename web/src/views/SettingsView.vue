<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('settings.title') }}</h1>
        <p class="page-subtitle">{{ t('settings.subtitle') }}</p>
      </div>
      <el-button @click="load">{{ t('common.refresh') }}</el-button>
    </div>

    <div class="workspace-card settings-card">
      <el-tabs v-model="activeTab" class="settings-tabs">
        <el-tab-pane :label="t('settings.generalSettings')" name="general">
          <div class="settings-tab-pane">
            <div class="settings-block">
              <label>{{ t('settings.language') }}</label>
              <el-radio-group v-model="form.language" @change="changeLanguage">
                <el-radio-button label="zh">{{ t('settings.chinese') }}</el-radio-button>
                <el-radio-button label="en">{{ t('settings.english') }}</el-radio-button>
              </el-radio-group>
              <span class="subtle-note">{{ t('settings.languageNote') }}</span>
            </div>

            <div class="settings-block">
              <label>{{ t('settings.concurrency') }}</label>
              <div class="head-actions">
                <el-input-number v-model="form.deploymentConcurrency" :min="1" :max="20" />
                <el-tooltip :content="deniedText" :disabled="canManageSettings" placement="top">
                  <span><el-button type="primary" :disabled="!canManageSettings" @click="save">{{ t('common.save') }}</el-button></span>
                </el-tooltip>
              </div>
              <span class="subtle-note">{{ t('settings.concurrencyNote') }}</span>
            </div>
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('settings.dataMaintenance')" name="maintenance">
          <div class="settings-tab-pane">
            <DataMaintenancePanel
              ref="maintenancePanel"
              :backup-dir="form.databaseBackupDir"
              :can-manage="canManageSettings"
              :disabled-reason="deniedText"
            />
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('settings.logMaintenance')" name="logs">
          <div class="settings-tab-pane">
            <LogMaintenancePanel
              :log-retention-days="form.logRetentionDays"
              :can-manage="canManageSettings"
              :disabled-reason="deniedText"
            />
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('settings.userManagement')" name="users">
          <div class="settings-tab-pane">
            <UserManagementPanel
              ref="userPanel"
              :can-manage="canManageUsers"
              :disabled-reason="deniedText"
            />
          </div>
        </el-tab-pane>

        <el-tab-pane :label="t('settings.statusOverview')" name="status">
          <div class="settings-tab-pane">
            <ControlPlaneHealthPanel ref="healthPanel" />

            <h2 class="settings-title">{{ t('settings.moduleStatus') }}</h2>
            <el-table :data="moduleRows" :fit="false" style="width: 1150px; max-width: 100%;">
              <el-table-column prop="module" :label="t('common.module')" width="160" />
              <el-table-column :label="t('common.status')" width="140"><template #default><span class="status-pill success">{{ t('settings.connected') }}</span></template></el-table-column>
              <el-table-column prop="message" :label="t('common.message')" width="520" show-overflow-tooltip />
              <el-table-column prop="time" :label="t('common.time')" width="190" />
            </el-table>
          </div>
        </el-tab-pane>
      </el-tabs>
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiGet, apiPut } from '../api/client'
import ControlPlaneHealthPanel from '../components/ControlPlaneHealthPanel.vue'
import DataMaintenancePanel from '../components/DataMaintenancePanel.vue'
import LogMaintenancePanel from '../components/LogMaintenancePanel.vue'
import UserManagementPanel from '../components/UserManagementPanel.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

type RefreshablePanel = {
  refresh: () => Promise<void> | void
}

const { locale, setLocale, t } = useI18n()
const { can, deniedText } = usePermissions()
const form = reactive<any>({ language: locale.value, deploymentConcurrency: 2, moduleStatus: {} })
const activeTab = ref('general')
const now = ref('')
const maintenancePanel = ref<RefreshablePanel | null>(null)
const healthPanel = ref<RefreshablePanel | null>(null)
const userPanel = ref<RefreshablePanel | null>(null)
const canManageSettings = computed(() => can(permissions.settingsManage))
const canManageUsers = computed(() => can(permissions.usersManage))
const moduleRows = computed(() => {
  const modules = form.moduleStatus ?? {}
  return Object.keys(modules).map((module) => ({ module, message: moduleMessage(module), time: now.value }))
})

function moduleMessage(module: string) {
  const key = `settings.moduleMessages.${module}`
  const text = t(key)
  return text === key ? t('settings.moduleAvailable', { module }) : text
}

function changeLanguage(value: string | number | boolean | undefined) {
  if (typeof value === 'string') {
    setLocale(value)
  }
}

async function load() {
  Object.assign(form, await apiGet('/settings'))
  form.language = locale.value
  form.deploymentConcurrency = Number(form.deploymentConcurrency)
  now.value = new Date().toISOString()
  await Promise.all([maintenancePanel.value?.refresh(), userPanel.value?.refresh(), healthPanel.value?.refresh()])
}
async function save() {
  if (!canManageSettings.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  setLocale(form.language)
  Object.assign(form, await apiPut('/settings', { language: form.language, deploymentConcurrency: form.deploymentConcurrency }))
  form.language = locale.value
  form.deploymentConcurrency = Number(form.deploymentConcurrency)
  now.value = new Date().toISOString()
}
onMounted(load)
</script>

<style scoped>
.settings-card {
  padding: 0 12px 12px;
}

.settings-tabs {
  min-width: 0;
}

.settings-tabs :deep(.el-tabs__header) {
  margin: 0 0 12px;
}

.settings-tab-pane {
  min-width: 0;
  display: grid;
  gap: 12px;
  align-content: start;
}

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

.settings-title {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 16px;
  font-weight: 850;
}

</style>
