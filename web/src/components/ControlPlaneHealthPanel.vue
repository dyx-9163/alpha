<template>
  <section class="health-panel">
    <div class="health-panel__head">
      <h2 class="settings-title">{{ t('settings.healthStatus') }}</h2>
      <el-button size="small" :loading="loading" @click="refresh">{{ t('common.refresh') }}</el-button>
    </div>
    <KeyValueGrid :items="healthItems" class="provider-grid">
      <template #value="{ item }">
        <StatusTag v-if="item.key === 'status'" :status="String(item.value)" />
        <span v-else>{{ item.value || '-' }}</span>
      </template>
    </KeyValueGrid>
    <el-table :data="healthRows" v-loading="loading">
      <el-table-column prop="name" :label="t('common.module')" width="180" />
      <el-table-column :label="t('common.status')" width="120">
        <template #default="{ row }"><StatusTag :status="row.status" /></template>
      </el-table-column>
      <el-table-column prop="message" :label="t('common.message')" min-width="180" />
      <el-table-column prop="path" :label="t('settings.path')" min-width="260" show-overflow-tooltip />
    </el-table>
  </section>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { apiGet } from '../api/client'
import { useI18n } from '../i18n'
import KeyValueGrid from './KeyValueGrid.vue'
import StatusTag from './StatusTag.vue'

type HealthReport = {
  status: string
  checkedAt: string
  components: Record<string, { status: string; message?: string; details?: Record<string, unknown> }>
}

const { t } = useI18n()
const loading = ref(false)
const health = ref<HealthReport | null>(null)

const healthItems = computed(() => [
  { key: 'status', label: t('common.status'), value: health.value?.status || 'unknown' },
  { key: 'checkedAt', label: t('settings.checkedAt'), value: formatDate(health.value?.checkedAt) }
])
const healthRows = computed(() => {
  const components = health.value?.components || {}
  return Object.entries(components).map(([name, component]) => ({
    name,
    status: component.status || 'unknown',
    message: component.message || '-',
    path: component.details?.path || '-'
  }))
})

async function refresh() {
  loading.value = true
  try {
    health.value = await apiGet<HealthReport>('/health')
  } catch {
    health.value = null
  } finally {
    loading.value = false
  }
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
.health-panel {
  display: grid;
  gap: 12px;
}

.health-panel__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.settings-title {
  margin: 0;
  color: var(--aifar-ink);
  font-size: 16px;
  font-weight: 850;
}

.provider-grid {
  grid-template-columns: minmax(150px, 180px) minmax(0, 1fr);
}

@media (max-width: 720px) {
  .provider-grid {
    grid-template-columns: 120px minmax(0, 1fr);
  }
}
</style>
