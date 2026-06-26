<template>
  <section>
    <div class="page-head">
      <div>
        <h1 class="page-title">{{ t('toolbox.title') }}</h1>
        <p class="page-subtitle">{{ t('toolbox.subtitle') }}</p>
      </div>
      <div class="head-actions">
        <el-button @click="rescan">{{ t('toolbox.rescan') }}</el-button>
        <el-button @click="load">{{ t('common.refresh') }}</el-button>
      </div>
    </div>

    <div class="workspace-card">
      <h2 class="section-title">{{ t('toolbox.offlineResources') }}</h2>
      <el-table :data="resources">
        <el-table-column prop="app" :label="t('table.app')" />
        <el-table-column prop="version" :label="t('common.version')" />
        <el-table-column prop="rpmCount" :label="t('toolbox.rpms')" />
        <el-table-column prop="path" :label="t('toolbox.path')" />
      </el-table>
    </div>
  </section>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { apiGet, apiPost, asArray } from '../api/client'
import { useI18n } from '../i18n'

const { t } = useI18n()
const resources = ref<any[]>([])
async function load() {
  resources.value = asArray(await apiGet<any[] | null>('/resources').catch(() => []))
}
async function rescan() {
  await apiPost('/resources/rescan')
  await load()
}
onMounted(load)
</script>
