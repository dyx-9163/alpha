<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('toolbox.title') }}</h1>
        <p class="page-subtitle">{{ t('toolbox.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-tooltip :content="deniedText" :disabled="canScanResources" placement="top">
          <span><el-button :disabled="!canScanResources" @click="rescan">{{ t('toolbox.rescan') }}</el-button></span>
        </el-tooltip>
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
      </div>
    </div>

    <div class="workspace-card">
      <DataTable :rows="resources" :columns="resourceColumns" :title="t('toolbox.offlineResources')" />
    </div>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { apiGet, apiPost, asArray } from '../api/client'
import DataTable from '../components/DataTable.vue'
import { usePermissions } from '../composables/usePermissions'
import { useI18n } from '../i18n'
import { permissions } from '../rbac'

const { t } = useI18n()
const { can, deniedText } = usePermissions()
const resources = ref<any[]>([])
const canScanResources = computed(() => can(permissions.resourcesScan))
const resourceColumns = computed(() => [
  { prop: 'app', label: t('table.app'), minWidth: 120 },
  { prop: 'version', label: t('common.version'), minWidth: 150 },
  { prop: 'rpmCount', label: t('toolbox.rpms'), width: 100, align: 'right' as const },
  { prop: 'path', label: t('toolbox.path'), minWidth: 360 }
])
async function load() {
  resources.value = asArray(await apiGet<any[] | null>('/resources').catch(() => []))
}
async function rescan() {
  if (!canScanResources.value) {
    ElMessage.warning(deniedText.value)
    return
  }
  await apiPost('/resources/rescan')
  await load()
}
onMounted(load)
</script>
